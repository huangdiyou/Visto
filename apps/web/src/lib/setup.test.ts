import { describe, expect, it } from "vitest";
import { buildSetupInput, validateSetupDraft } from "./setup";

describe("validateSetupDraft", () => {
  it("accepts a complete local setup", () => {
    expect(
      validateSetupDraft({
        workspaceName: "我的工作空间",
        ownerName: "Huang",
        password: "local-password-123",
        confirmPassword: "local-password-123",
      }),
    ).toBeNull();
  });

  it("rejects mismatched passwords", () => {
    expect(
      validateSetupDraft({
        workspaceName: "Studio",
        ownerName: "Owner",
        password: "local-password-123",
        confirmPassword: "local-password-456",
      }),
    ).toBe("两次输入的密码不一致");
  });
});

describe("buildSetupInput", () => {
  const draft = {
    workspaceName: "  我的工作空间  ",
    ownerName: " Huang ",
    password: "local-password-123",
    confirmPassword: "local-password-123",
  };

  // The field is optional on the wire, but that is for other clients: this one
  // has a checkbox and must always state the answer, so a refusal can never be
  // silently reinterpreted as the default.
  it("states the host access answer explicitly when enabled", () => {
    const input = buildSetupInput(draft, true, "zh-CN", "Asia/Shanghai");
    expect(input.allowWebHostPaths).toBe(true);
    expect(input.workspaceName).toBe("我的工作空间");
    expect(input.ownerName).toBe("Huang");
  });

  it("states a refusal explicitly too", () => {
    expect(
      buildSetupInput(draft, false, "zh-CN", "Asia/Shanghai").allowWebHostPaths,
    ).toBe(false);
  });
});
