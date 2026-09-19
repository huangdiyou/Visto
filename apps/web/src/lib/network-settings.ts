import type { SystemNetworkSettings } from "@review-studio/contracts";

/**
 * The states the access-security card can be in. Keeping this pure makes the
 * self-lockout and deployment-override rules testable without a DOM.
 */
export interface NetworkSettingsViewState {
  /** True when the deployment pins the policy on and the toggle is read-only. */
  environmentForced: boolean;
  /**
   * True when the Owner is on remote plaintext HTTP and enforcement is off.
   * Enabling it from here would make the next request fail with 426, so the
   * toggle stays read-only and the page explains why.
   */
  cannotEnable: boolean;
  toggleDisabled: boolean;
  /** The pending value differs from what is stored. */
  dirty: boolean;
  saveDisabled: boolean;
  /** Warn that plaintext remote access is currently accepted. */
  showPlaintextRisk: boolean;
  /** Warn before switching enforcement on, because it applies immediately. */
  showEnableWarning: boolean;
}

export function networkSettingsViewState(input: {
  settings: SystemNetworkSettings | null;
  pendingRequireRemoteHTTPS: boolean;
  loading: boolean;
  busy: boolean;
}): NetworkSettingsViewState {
  const { settings, pendingRequireRemoteHTTPS, loading, busy } = input;
  if (!settings) {
    return {
      environmentForced: false,
      cannotEnable: false,
      toggleDisabled: true,
      dirty: false,
      saveDisabled: true,
      showPlaintextRisk: false,
      showEnableWarning: false,
    };
  }
  const environmentForced = settings.environmentForced;
  const cannotEnable =
    !settings.currentRequestSecure && !settings.requireRemoteHTTPS;
  const toggleDisabled = busy || environmentForced || cannotEnable;
  const dirty = pendingRequireRemoteHTTPS !== settings.requireRemoteHTTPS;
  return {
    environmentForced,
    cannotEnable,
    toggleDisabled,
    dirty,
    saveDisabled: busy || loading || !dirty || toggleDisabled,
    showPlaintextRisk: !settings.effectiveRequireRemoteHTTPS,
    showEnableWarning: dirty && pendingRequireRemoteHTTPS,
  };
}
