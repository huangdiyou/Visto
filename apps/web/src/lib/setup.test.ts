import { describe, expect, it } from "vitest";
import { validateSetupDraft } from "./setup";

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
