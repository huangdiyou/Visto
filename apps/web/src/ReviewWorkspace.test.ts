import { describe, expect, it } from "vitest";
import { defaultReviewName, generateReviewAccessCode } from "./ReviewWorkspace";

const first = {
  assetId: "asset-1",
  assetVersionId: "version-1",
  assetName: "第一版.mp4",
  versionNumber: 1,
  processingStatus: "ready",
};

describe("review creation defaults", () => {
  it("uses the only filename as the review name", () => {
    expect(defaultReviewName([first])).toBe("第一版.mp4");
  });

  it("summarizes multiple files from the first filename", () => {
    expect(
      defaultReviewName([
        first,
        {
          ...first,
          assetId: "asset-2",
          assetVersionId: "version-2",
          assetName: "第二版.png",
        },
      ]),
    ).toBe("第一版.mp4 等 2 个文件");
  });

  it("generates a four digit access code", () => {
    expect(generateReviewAccessCode()).toMatch(/^\d{4}$/);
  });
});
