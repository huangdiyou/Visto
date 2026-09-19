import { describe, expect, it } from "vitest";
import {
  createTranslator,
  formatRelativeDate,
  localeLabel,
  normalizeLocale,
} from "./i18n";

describe("i18n helpers", () => {
  it("normalizes supported locale families", () => {
    expect(normalizeLocale("en")).toBe("en-US");
    expect(normalizeLocale("en-GB")).toBe("en-US");
    expect(normalizeLocale("zh-Hans-CN")).toBe("zh-CN");
    expect(normalizeLocale(null)).toBe("zh-CN");
  });

  it("translates keyed copy with interpolation", () => {
    expect(
      createTranslator("zh-CN")("projectOverview.pulseFailedTitle", {
        count: 2,
      }),
    ).toBe("2 个处理失败需要查看");
    expect(
      createTranslator("en-US")("projectOverview.pulseFailedTitle", {
        count: 2,
      }),
    ).toBe("2 failed tasks need review");
  });

  it("formats locale labels in the display language", () => {
    expect(localeLabel("zh-CN", "zh-CN")).toBe("简体中文");
    expect(localeLabel("en-US", "en-US")).toBe("English");
  });

  it("formats relative dates with localized labels", () => {
    const today = new Date().toISOString();
    expect(formatRelativeDate(today, "zh-CN")).toBe("今天");
    expect(formatRelativeDate(today, "en-US")).toBe("Today");
  });
});
