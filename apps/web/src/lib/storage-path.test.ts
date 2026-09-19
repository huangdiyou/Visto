import { describe, expect, it } from "vitest";
import { resolveManagedBucketPath } from "./storage-path";

describe("resolveManagedBucketPath", () => {
  it("places a Windows drive root inside a dedicated Visto directory", () => {
    expect(resolveManagedBucketPath("D:\\")).toEqual({
      path: "D:\\Visto",
      expandedDriveRoot: true,
    });
    expect(resolveManagedBucketPath(" d:/ ")).toEqual({
      path: "D:\\Visto",
      expandedDriveRoot: true,
    });
  });

  it("keeps an explicit folder unchanged", () => {
    expect(resolveManagedBucketPath(" D:\\Media\\Visto ")).toEqual({
      path: "D:\\Media\\Visto",
      expandedDriveRoot: false,
    });
  });
});
