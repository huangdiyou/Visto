import { describe, expect, it } from "vitest";
import type { SystemNetworkSettings } from "@review-studio/contracts";
import { createTranslator } from "./i18n";
import { networkSettingsViewState } from "./network-settings";

function settings(
  overrides: Partial<SystemNetworkSettings> = {},
): SystemNetworkSettings {
  return {
    requireRemoteHTTPS: false,
    effectiveRequireRemoteHTTPS: false,
    environmentForced: false,
    currentRequestSecure: true,
    revision: 1,
    updatedBy: null,
    updatedAt: "2026-08-31T00:00:00Z",
    ...overrides,
  };
}

describe("access security card state", () => {
  it("blocks interaction while the setting is still loading", () => {
    const view = networkSettingsViewState({
      settings: null,
      pendingRequireRemoteHTTPS: false,
      loading: true,
      busy: false,
    });
    expect(view.toggleDisabled).toBe(true);
    expect(view.saveDisabled).toBe(true);
    expect(view.showPlaintextRisk).toBe(false);
  });

  it("warns about plaintext remote access on a default install", () => {
    const view = networkSettingsViewState({
      settings: settings(),
      pendingRequireRemoteHTTPS: false,
      loading: false,
      busy: false,
    });
    expect(view.toggleDisabled).toBe(false);
    expect(view.showPlaintextRisk).toBe(true);
    // Nothing changed yet, so there is nothing to save.
    expect(view.dirty).toBe(false);
    expect(view.saveDisabled).toBe(true);
  });

  it("warns before enabling because the change applies immediately", () => {
    const view = networkSettingsViewState({
      settings: settings(),
      pendingRequireRemoteHTTPS: true,
      loading: false,
      busy: false,
    });
    expect(view.dirty).toBe(true);
    expect(view.showEnableWarning).toBe(true);
    expect(view.saveDisabled).toBe(false);
  });

  it("prevents a remote plaintext Owner from locking itself out", () => {
    const view = networkSettingsViewState({
      settings: settings({ currentRequestSecure: false }),
      pendingRequireRemoteHTTPS: false,
      loading: false,
      busy: false,
    });
    expect(view.cannotEnable).toBe(true);
    expect(view.toggleDisabled).toBe(true);
    expect(view.saveDisabled).toBe(true);
  });

  it("still allows a remote plaintext Owner to turn enforcement off", () => {
    // Reaching this page over plaintext while enforcement is on means the
    // request came through loopback or a gateway, so turning it off is safe.
    const view = networkSettingsViewState({
      settings: settings({
        requireRemoteHTTPS: true,
        effectiveRequireRemoteHTTPS: true,
        currentRequestSecure: false,
      }),
      pendingRequireRemoteHTTPS: false,
      loading: false,
      busy: false,
    });
    expect(view.cannotEnable).toBe(false);
    expect(view.toggleDisabled).toBe(false);
    expect(view.saveDisabled).toBe(false);
  });

  it("locks the toggle when the deployment forces enforcement", () => {
    const view = networkSettingsViewState({
      settings: settings({
        environmentForced: true,
        effectiveRequireRemoteHTTPS: true,
      }),
      pendingRequireRemoteHTTPS: true,
      loading: false,
      busy: false,
    });
    expect(view.environmentForced).toBe(true);
    expect(view.toggleDisabled).toBe(true);
    expect(view.saveDisabled).toBe(true);
    expect(view.showPlaintextRisk).toBe(false);
  });

  it("blocks the toggle while a save is in flight", () => {
    const view = networkSettingsViewState({
      settings: settings(),
      pendingRequireRemoteHTTPS: true,
      loading: false,
      busy: true,
    });
    expect(view.toggleDisabled).toBe(true);
    expect(view.saveDisabled).toBe(true);
  });
});

describe("access security copy", () => {
  const keys = [
    "owner.network",
    "owner.networkDescription",
    "owner.networkTitle",
    "owner.networkDetail",
    "owner.networkCardTitle",
    "owner.networkCardDescription",
    "owner.networkRequireHTTPS",
    "owner.networkRequireHTTPSOff",
    "owner.networkRequireHTTPSOn",
    "owner.networkEffectiveStatus",
    "owner.networkEffectiveRequired",
    "owner.networkEffectiveOptional",
    "owner.networkPlaintextRisk",
    "owner.networkEnableWarning",
    "owner.networkEnvironmentForced",
    "owner.networkInsecureOrigin",
    "owner.networkLoopbackHint",
    "owner.networkNoRestartHint",
    "owner.networkSave",
    "owner.networkSaving",
    "owner.networkSaved",
    "owner.networkSaveFailed",
    "owner.networkLoading",
    "owner.networkLoadFailed",
    "owner.networkRevisionConflict",
  ] as const;

  it("has non-empty Chinese and English copy for every state", () => {
    const zh = createTranslator("zh-CN");
    const en = createTranslator("en-US");
    for (const key of keys) {
      expect(zh(key), `zh-CN ${key}`).not.toBe("");
      expect(zh(key), `zh-CN ${key}`).not.toBe(key);
      expect(en(key), `en-US ${key}`).not.toBe("");
      expect(en(key), `en-US ${key}`).not.toBe(key);
    }
  });

  it("states the toggle name and both switch positions as specified", () => {
    const zh = createTranslator("zh-CN");
    expect(zh("owner.networkRequireHTTPS")).toBe("远程访问必须使用 HTTPS");
    expect(zh("owner.networkRequireHTTPSOff")).toContain(
      "允许通过 HTTP 或 HTTPS 远程访问",
    );
    expect(zh("owner.networkRequireHTTPSOn")).toContain(
      "都必须通过 HTTPS 打开",
    );
    // No restart button, so the copy must say the change is immediate.
    expect(zh("owner.networkNoRestartHint")).toContain("不需要重启");
  });
});
