import { describe, expect, it } from "vitest";
import {
  annotationPixelBounds,
  appendDrawingElement,
  createArrowDrawingElement,
  createBrushDrawingElement,
  createDrawingRectElement,
  createRegionGeometry,
  removeLastDrawingElement,
  normalizePointerPosition,
} from "./image-annotations";

describe("image annotation geometry", () => {
  it("normalizes pointer coordinates inside the displayed image", () => {
    expect(
      normalizePointerPosition(300, 250, {
        left: 100,
        top: 50,
        width: 800,
        height: 400,
      }),
    ).toEqual({ x: 0.25, y: 0.5 });
  });

  it("builds regions in any drag direction and rejects tiny drags", () => {
    expect(
      createRegionGeometry({ x: 0.8, y: 0.7 }, { x: 0.2, y: 0.1 }),
    ).toEqual({
      shape: "rect",
      x: 0.2,
      y: 0.1,
      width: 0.6000000000000001,
      height: 0.6,
    });
    expect(
      createRegionGeometry({ x: 0.2, y: 0.2 }, { x: 0.205, y: 0.21 }),
    ).toBeNull();
  });

  it("keeps the same relative location across display sizes", () => {
    const geometry = {
      shape: "rect" as const,
      x: 0.25,
      y: 0.2,
      width: 0.3,
      height: 0.15,
    };
    expect(annotationPixelBounds(geometry, 1000, 500)).toEqual({
      x: 250,
      y: 100,
      width: 300,
      height: 75,
    });
    expect(annotationPixelBounds(geometry, 400, 200)).toEqual({
      x: 100,
      y: 40,
      width: 120,
      height: 30,
    });
  });

  it("builds drawing elements and supports undoing the latest element", () => {
    const brush = createBrushDrawingElement(
      [
        { x: 0.1, y: 0.1 },
        { x: 0.2, y: 0.2 },
      ],
      "#ff5c7a",
    );
    const arrow = createArrowDrawingElement(
      { x: 0.7, y: 0.3 },
      { x: 0.4, y: 0.6 },
      "#54d6ff",
    );
    const draft = appendDrawingElement(
      appendDrawingElement(null, brush),
      arrow,
    );
    expect(draft?.shape).toBe("drawing");
    expect(draft?.elements).toHaveLength(2);
    expect(removeLastDrawingElement(draft)?.elements).toHaveLength(1);
  });

  it("creates drawing rectangles in any drag direction", () => {
    expect(
      createDrawingRectElement(
        { x: 0.75, y: 0.6 },
        { x: 0.25, y: 0.2 },
        "#64d081",
      ),
    ).toEqual({
      tool: "rect",
      color: "#64d081",
      strokeWidth: 4,
      x: 0.25,
      y: 0.2,
      width: 0.5,
      height: 0.39999999999999997,
    });
  });
});
