import type {
  SystemInfo,
  SystemNetworkSettings,
  SystemNetworkSettingsInput,
  SystemUpdateStatus,
} from "@review-studio/contracts";
import { requestJSON } from "./client";

export async function getSystemInfo(signal?: AbortSignal): Promise<SystemInfo> {
  const options = signal ? { signal } : {};
  return requestJSON<SystemInfo>("/api/v1/system/info", options);
}

export function getNetworkSettings(
  signal?: AbortSignal,
): Promise<SystemNetworkSettings> {
  return requestJSON<SystemNetworkSettings>(
    "/api/v1/system/network-settings",
    signal ? { signal } : {},
  );
}

// check=1 asks Core to fetch the signed update manifest from the configured
// public sources. Without it the page reuses the last result, so simply opening
// the page never contacts an external platform.
export function getSystemUpdateStatus(
  check = false,
  signal?: AbortSignal,
): Promise<SystemUpdateStatus> {
  const target = check
    ? "/api/v1/system/update-status?check=1"
    : "/api/v1/system/update-status";
  return requestJSON<SystemUpdateStatus>(target, signal ? { signal } : {});
}

export function updateNetworkSettings(
  input: SystemNetworkSettingsInput,
): Promise<SystemNetworkSettings> {
  return requestJSON<SystemNetworkSettings>("/api/v1/system/network-settings", {
    method: "PUT",
    body: input,
  });
}
