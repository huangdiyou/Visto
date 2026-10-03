import type { SetupInput } from "@review-studio/contracts";

export interface SetupDraft {
  workspaceName: string;
  ownerName: string;
  password: string;
  confirmPassword: string;
}

/**
 * Builds the first-run payload. The host access answer is always sent
 * explicitly: the field is optional on the wire so that an older client cannot
 * be read as a refusal, but this client has a checkbox and must not rely on that
 * fallback (D2, docs/FREE_TIER_BOUNDARY_DESIGN.md §2.2).
 */
export function buildSetupInput(
  draft: SetupDraft,
  allowWebHostPaths: boolean,
  locale: string,
  timezone: string,
): SetupInput {
  return {
    workspaceName: draft.workspaceName.trim(),
    ownerName: draft.ownerName.trim(),
    password: draft.password,
    locale,
    timezone,
    allowWebHostPaths,
  };
}

export function validateSetupDraft(draft: SetupDraft): string | null {
  const workspaceLength = Array.from(draft.workspaceName.trim()).length;
  const ownerLength = Array.from(draft.ownerName.trim()).length;
  const passwordLength = Array.from(draft.password).length;

  if (workspaceLength < 1 || workspaceLength > 80) {
    return "工作空间名称需要 1–80 个字符";
  }
  if (ownerLength < 1 || ownerLength > 80) {
    return "你的名字需要 1–80 个字符";
  }
  if (passwordLength < 10 || passwordLength > 256) {
    return "本地访问密码至少需要 10 个字符";
  }
  if (draft.password !== draft.confirmPassword) {
    return "两次输入的密码不一致";
  }

  return null;
}

export function browserLocale(): string {
  return navigator.language || "zh-CN";
}

export function browserTimezone(): string {
  return Intl.DateTimeFormat().resolvedOptions().timeZone || "Asia/Shanghai";
}
