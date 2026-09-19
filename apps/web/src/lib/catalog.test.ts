import { describe, expect, it } from "vitest";
import { nullableDescription, validateCatalogDraft } from "./catalog";

describe("catalog draft", () => {
  it("requires a project or collection name", () => {
    expect(validateCatalogDraft({ name: "  ", description: "" })).toBe(
      "名称需要 1 到 120 个字符",
    );
  });

  it("normalizes an empty description to null", () => {
    expect(nullableDescription("   ")).toBeNull();
    expect(nullableDescription("  首轮交付  ")).toBe("首轮交付");
  });
});
