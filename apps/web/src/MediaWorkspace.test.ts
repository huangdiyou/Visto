import { describe, expect, it } from "vitest";
import type {
  Job,
  MediaProbe,
  Rendition,
  StorageObject,
  UploadCheck,
} from "@review-studio/contracts";
import { buildMediaItem } from "./MediaWorkspace";

const object: StorageObject = {
  id: "object-1",
  authorizedRootId: "root-1",
  objectKey: "media/example.mp4",
  status: "available",
  sizeBytes: 1024,
  modifiedAt: "2026-06-10T00:00:00Z",
  mimeType: "video/mp4",
  firstDiscoveredAt: "2026-06-10T00:00:00Z",
  lastSeenAt: "2026-06-10T00:00:00Z",
  missingSince: null,
};

const probe: MediaProbe = {
  storageObjectId: object.id,
  status: "succeeded",
  mediaType: "video",
  formatName: "mov,mp4",
  formatLongName: "QuickTime / MOV",
  durationUs: 2_000_000,
  bitRate: 1_000_000,
  width: 1280,
  height: 720,
  rotationDegrees: 0,
  frameRate: 30,
  videoCodec: "h264",
  audioCodec: "aac",
  errorCode: null,
  errorMessage: null,
  probedAt: "2026-06-10T00:00:00Z",
};

function rendition(
  kind: Rendition["kind"],
  status: Rendition["status"],
): Rendition {
  return {
    id: `${kind}-1`,
    sourceStorageObjectId: object.id,
    kind,
    profileKey: "profile",
    profileVersion: 1,
    sourceFingerprint: "fingerprint",
    status,
    mimeType: status === "ready" ? "video/mp4" : null,
    width: 1280,
    height: 720,
    durationUs: 2_000_000,
    sizeBytes: 1024,
    metadataJson: "{}",
    errorCode: status === "failed" ? "rendition.failed" : null,
    errorMessage: status === "failed" ? "encoder failed" : null,
    contentUrl: status === "ready" ? "/content" : null,
    updatedAt: "2026-06-10T00:00:00Z",
    completedAt: status === "processing" ? null : "2026-06-10T00:00:00Z",
  };
}

function job(
  status: Job["status"],
  type = "media.generate_video_renditions",
): Job {
  return {
    id: "job-1",
    type,
    status,
    priority: 4,
    subject: { type: "storageObject", id: object.id },
    progress:
      status === "running"
        ? { current: 2, total: 4, unit: "renditions" }
        : null,
    error:
      status === "failed"
        ? { code: "job.failed", message: "worker failed" }
        : null,
    attemptCount: 1,
    maxAttempts: 3,
    createdAt: "2026-06-10T00:00:00Z",
    updatedAt: "2026-06-10T00:00:00Z",
    startedAt: null,
    completedAt: null,
  };
}

function uploadCheck(
  status: UploadCheck["status"],
  resultCode: string,
): UploadCheck {
  return {
    id: "upload-check-1",
    projectId: "project-1",
    assetId: "asset-1",
    assetVersionId: "version-1",
    storageObjectId: object.id,
    sourceType: "user",
    uploadSecurityPolicy: "enhanced",
    status,
    resultCode,
    message: null,
    createdAt: "2026-06-10T00:00:00Z",
    updatedAt: "2026-06-10T00:00:00Z",
    completedAt: "2026-06-10T00:00:00Z",
    quarantinedAt: status === "quarantined" ? "2026-06-10T00:00:00Z" : null,
    rejectedAt: status === "rejected" ? "2026-06-10T00:00:00Z" : null,
  };
}

describe("buildMediaItem", () => {
  it("uses the active job as the authoritative processing state", () => {
    const item = buildMediaItem(
      object,
      probe,
      [rendition("poster", "ready")],
      [job("running")],
    );
    expect(item.state).toBe("processing");
    expect(item.progressLabel).toBe("2/4 renditions");
  });

  it("reports a failed rendition instead of treating its thumbnail as ready", () => {
    const item = buildMediaItem(
      object,
      probe,
      [rendition("poster", "ready"), rendition("proxy", "failed")],
      [],
    );
    expect(item.state).toBe("failed");
    expect(item.errorMessage).toBe("encoder failed");
  });

  it("keeps missing source state visible even when cached previews exist", () => {
    const item = buildMediaItem(
      { ...object, status: "missing", missingSince: "2026-06-10T01:00:00Z" },
      probe,
      [rendition("poster", "ready"), rendition("proxy", "ready")],
      [],
    );
    expect(item.state).toBe("missing");
    expect(item.stateLabel).toBe("文件缺失");
  });

  it("marks a complete proxy as ready", () => {
    const item = buildMediaItem(
      object,
      probe,
      [rendition("poster", "ready"), rendition("proxy", "ready")],
      [job("succeeded")],
    );
    expect(item.state).toBe("preview_ready");
    expect(item.preview?.kind).toBe("proxy");
  });

  it("keeps previews usable when background enhancement fails", () => {
    const item = buildMediaItem(
      object,
      probe,
      [
        rendition("poster", "ready"),
        rendition("proxy", "ready"),
        rendition("storyboard", "failed"),
      ],
      [job("failed")],
    );
    expect(item.state).toBe("enhancement_failed");
    expect(item.preview?.kind).toBe("proxy");
    expect(item.stateLabel).toBe("增强失败");
    expect(item.failedJobs).toHaveLength(1);
    expect(item.failedJobs[0]?.error?.message).toBe("worker failed");
  });

  it("shows background optimization while video enhancements are queued", () => {
    const item = buildMediaItem(
      object,
      probe,
      [rendition("poster", "ready"), rendition("proxy", "ready")],
      [job("queued", "media.generate_video_enhancements")],
    );
    expect(item.state).toBe("optimizing");
    expect(item.stateLabel).toBe("后台优化中");
    expect(item.preview?.kind).toBe("proxy");
  });

  it("keeps quarantined uploads blocked even when previews exist", () => {
    const item = buildMediaItem(
      object,
      probe,
      [rendition("poster", "ready"), rendition("proxy", "ready")],
      [job("succeeded")],
      undefined,
      uploadCheck("quarantined", "malware_scan_unavailable"),
    );
    expect(item.state).toBe("quarantined");
    expect(item.stateLabel).toBe("已隔离");
    expect(item.uploadCheck?.resultCode).toBe("malware_scan_unavailable");
  });
});
