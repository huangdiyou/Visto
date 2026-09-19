import type {
  AnnotationGeometry,
  DrawingAnnotationGeometry,
  DrawingElement,
  PointAnnotationGeometry,
  RegionAnnotationGeometry,
} from "@review-studio/contracts";

export interface NormalizedPoint {
  x: number;
  y: number;
}

export function normalizePointerPosition(
  clientX: number,
  clientY: number,
  bounds: Pick<DOMRect, "left" | "top" | "width" | "height">,
): NormalizedPoint {
  if (bounds.width <= 0 || bounds.height <= 0) {
    return { x: 0, y: 0 };
  }
  return {
    x: clamp01((clientX - bounds.left) / bounds.width),
    y: clamp01((clientY - bounds.top) / bounds.height),
  };
}

export function createPointGeometry(
  point: NormalizedPoint,
): PointAnnotationGeometry {
  return {
    shape: "point",
    x: clamp01(point.x),
    y: clamp01(point.y),
  };
}

export function createRegionGeometry(
  start: NormalizedPoint,
  end: NormalizedPoint,
  minimumSize = 0.01,
): RegionAnnotationGeometry | null {
  const x = clamp01(Math.min(start.x, end.x));
  const y = clamp01(Math.min(start.y, end.y));
  const width = clamp01(Math.abs(end.x - start.x));
  const height = clamp01(Math.abs(end.y - start.y));
  if (width < minimumSize || height < minimumSize) {
    return null;
  }
  return {
    shape: "rect",
    x,
    y,
    width: Math.min(width, 1 - x),
    height: Math.min(height, 1 - y),
  };
}

export function annotationPixelBounds(
  geometry: AnnotationGeometry,
  width: number,
  height: number,
) {
  if (geometry.shape === "point") {
    return {
      x: geometry.x * width,
      y: geometry.y * height,
      width: 0,
      height: 0,
    };
  }
  if (geometry.shape === "drawing") {
    const bounds = drawingGeometryBounds(geometry);
    return {
      x: bounds.x * width,
      y: bounds.y * height,
      width: bounds.width * width,
      height: bounds.height * height,
    };
  }
  return {
    x: geometry.x * width,
    y: geometry.y * height,
    width: geometry.width * width,
    height: geometry.height * height,
  };
}

export function formatImageGeometry(geometry: AnnotationGeometry | null) {
  if (!geometry) {
    return "尚未在图片上标记";
  }
  if (geometry.shape === "point") {
    return `点位 ${formatPercent(geometry.x)}, ${formatPercent(geometry.y)}`;
  }
  if (geometry.shape === "drawing") {
    return `绘制 ${geometry.elements.length} 笔`;
  }
  return `区域 ${formatPercent(geometry.width)} × ${formatPercent(
    geometry.height,
  )}`;
}

export function createDrawingGeometry(
  elements: DrawingElement[],
): DrawingAnnotationGeometry | null {
  const validElements = elements.slice(0, 20);
  return validElements.length === 0
    ? null
    : { shape: "drawing", elements: validElements };
}

export function appendDrawingElement(
  draft: AnnotationGeometry | null,
  element: DrawingElement | null,
): DrawingAnnotationGeometry | null {
  if (!element) {
    return draft?.shape === "drawing" ? draft : null;
  }
  const elements = draft?.shape === "drawing" ? draft.elements : [];
  return createDrawingGeometry([...elements, element]);
}

export function removeLastDrawingElement(
  draft: AnnotationGeometry | null,
): DrawingAnnotationGeometry | null {
  if (draft?.shape !== "drawing") {
    return null;
  }
  return createDrawingGeometry(draft.elements.slice(0, -1));
}

export function createBrushDrawingElement(
  points: NormalizedPoint[],
  color: string,
  strokeWidth = 4,
): DrawingElement | null {
  const normalizedPoints = normalizeDrawingPoints(points);
  if (normalizedPoints.length < 2) {
    return null;
  }
  return {
    tool: "brush",
    color: normalizeDrawingColor(color),
    strokeWidth: normalizeStrokeWidth(strokeWidth),
    points: normalizedPoints,
  };
}

export function createArrowDrawingElement(
  start: NormalizedPoint,
  end: NormalizedPoint,
  color: string,
  strokeWidth = 4,
  minimumDistance = 0.01,
): DrawingElement | null {
  if (distanceBetween(start, end) < minimumDistance) {
    return null;
  }
  return {
    tool: "arrow",
    color: normalizeDrawingColor(color),
    strokeWidth: normalizeStrokeWidth(strokeWidth),
    points: [normalizeDrawingPoint(start), normalizeDrawingPoint(end)],
  };
}

export function createDrawingRectElement(
  start: NormalizedPoint,
  end: NormalizedPoint,
  color: string,
  strokeWidth = 4,
  minimumSize = 0.01,
): DrawingElement | null {
  const region = createRegionGeometry(start, end, minimumSize);
  if (!region) {
    return null;
  }
  return {
    tool: "rect",
    color: normalizeDrawingColor(color),
    strokeWidth: normalizeStrokeWidth(strokeWidth),
    x: region.x,
    y: region.y,
    width: region.width,
    height: region.height,
  };
}

export function drawingGeometryBounds(geometry: DrawingAnnotationGeometry) {
  const points = geometry.elements.flatMap((element) => {
    if (element.tool === "rect") {
      return [
        { x: element.x, y: element.y },
        { x: element.x + element.width, y: element.y + element.height },
      ];
    }
    return element.points;
  });
  if (points.length === 0) {
    return { x: 0, y: 0, width: 0, height: 0 };
  }
  const xs = points.map((point) => point.x);
  const ys = points.map((point) => point.y);
  const x = Math.min(...xs);
  const y = Math.min(...ys);
  return {
    x,
    y,
    width: Math.max(...xs) - x,
    height: Math.max(...ys) - y,
  };
}

function formatPercent(value: number) {
  return `${Math.round(clamp01(value) * 100)}%`;
}

function clamp01(value: number) {
  return Math.min(1, Math.max(0, value));
}

function normalizeDrawingPoints(points: NormalizedPoint[]) {
  const normalized: NormalizedPoint[] = [];
  for (const point of points) {
    const next = normalizeDrawingPoint(point);
    const last = normalized[normalized.length - 1];
    if (!last || distanceBetween(last, next) >= 0.002) {
      normalized.push(next);
    }
    if (normalized.length >= 200) {
      break;
    }
  }
  return normalized;
}

function normalizeDrawingPoint(point: NormalizedPoint) {
  return {
    x: clamp01(point.x),
    y: clamp01(point.y),
  };
}

function normalizeDrawingColor(value: string) {
  return /^#[0-9a-f]{6}$/i.test(value) ? value : "#f6cf5a";
}

function normalizeStrokeWidth(value: number) {
  if (!Number.isFinite(value)) {
    return 4;
  }
  return Math.min(24, Math.max(1, Math.round(value)));
}

function distanceBetween(left: NormalizedPoint, right: NormalizedPoint) {
  return Math.hypot(left.x - right.x, left.y - right.y);
}
