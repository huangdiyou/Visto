import { describe, expect, it } from "vitest";
import {
  formatAnnotationDraft,
  formatMediaTime,
  formatThreadTime,
  publicCommentReadonlyMessage,
  publicReviewPermissionLabel,
  secondsToUs,
} from "./review-comments";

describe("review comment time helpers", () => {
  it("converts player seconds to integer microseconds", () => {
    expect(secondsToUs(12.345678)).toBe(12_345_678);
    expect(secondsToUs(-1)).toBe(0);
  });

  it("formats point and range annotations consistently", () => {
    expect(formatMediaTime(12_345_000)).toBe("00:12.345");
    expect(formatAnnotationDraft("time_range", 1_200_000, 2_600_000)).toBe(
      "00:01.200 - 00:02.600",
    );
    expect(
      formatThreadTime({
        kind: "time_range",
        timeStartUs: 1_200_000,
        timeEndUs: 2_600_000,
        geometry: null,
        geometryVersion: 1,
      }),
    ).toBe("00:01.200 - 00:02.600");
  });

  it("shows effective comment availability from the review lifecycle", () => {
    expect(publicReviewPermissionLabel("open", true)).toBe("可评论");
    expect(publicReviewPermissionLabel("closed", true)).toBe("审阅已结束");
    expect(publicCommentReadonlyMessage("closed")).toContain(
      "已有评论仍可查看",
    );
  });
});
