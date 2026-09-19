import { describe, expect, it } from "vitest";
import {
  isActiveUploadTask,
  isPendingUploadTask,
  type UploadTask,
  uploadTaskStatusLabel,
} from "./UploadCenter";

function task(
  status: UploadTask["status"],
  input: Partial<UploadTask> = {},
): UploadTask {
  return {
    id: "upload-1",
    projectId: "project-1",
    projectName: "项目一",
    file: { name: "example.mp4", size: 1024 } as File,
    status,
    loaded: 0,
    total: 1024,
    percent: 0,
    speedBytesPerSecond: null,
    error: null,
    createdAt: 1,
    ...input,
  };
}

describe("upload task presentation", () => {
  it("counts queued and active work as pending", () => {
    expect(isPendingUploadTask(task("queued"))).toBe(true);
    expect(isPendingUploadTask(task("uploading"))).toBe(true);
    expect(isPendingUploadTask(task("storing"))).toBe(true);
    expect(isPendingUploadTask(task("success"))).toBe(false);
  });

  it("keeps transfer and server-side storage stages distinct", () => {
    expect(
      uploadTaskStatusLabel(task("uploading", { loaded: 1, percent: 0 })),
    ).toBe("1%");
    expect(uploadTaskStatusLabel(task("uploading", { percent: 63 }))).toBe(
      "63%",
    );
    expect(uploadTaskStatusLabel(task("storing", { percent: 100 }))).toBe(
      "写入存储",
    );
  });

  it("only treats network and storage phases as actively transferring", () => {
    expect(isActiveUploadTask(task("validating"))).toBe(true);
    expect(isActiveUploadTask(task("queued"))).toBe(false);
    expect(isActiveUploadTask(task("failed"))).toBe(false);
  });
});
