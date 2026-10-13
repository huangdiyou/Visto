import type {
  SystemHostAccess,
  SystemInfo,
  SystemMediaEncodingReprobeResult,
  SystemMediaEncodingSettings,
  SystemMediaEncodingSettingsInput,
  SystemNetworkSettings,
  SystemNetworkSettingsInput,
  SystemUpdateStatus,
} from "@review-studio/contracts";
import { requestJSON } from "./client";

export async function getSystemInfo(signal?: AbortSignal): Promise<SystemInfo> {
  const options = signal ? { signal } : {};
  return requestJSON<SystemInfo>("/api/v1/system/info", options);
}

// Read-only by design: a switch that grants host-level reach must not be
// changeable by the session that benefits from it, so there is no updater here
// and no write route behind it.
export function getHostAccess(signal?: AbortSignal): Promise<SystemHostAccess> {
  return requestJSON<SystemHostAccess>(
    "/api/v1/system/host-access",
    signal ? { signal } : {},
  );
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

export function getMediaEncodingSettings(
  signal?: AbortSignal,
): Promise<SystemMediaEncodingSettings> {
  return requestJSON<SystemMediaEncodingSettings>(
    "/api/v1/system/media-encoding",
    signal ? { signal } : {},
  );
}

// An empty preferredEncoder returns the instance to automatic selection, so the
// caller passes it deliberately rather than by omitting the field.
export function updateMediaEncodingSettings(
  input: SystemMediaEncodingSettingsInput,
): Promise<SystemMediaEncodingSettings> {
  return requestJSON<SystemMediaEncodingSettings>(
    "/api/v1/system/media-encoding",
    {
      method: "PUT",
      body: input,
    },
  );
}

// A sweep tries every candidate encoder against the installed FFmpeg and can
// take minutes, so this only starts one; the page reads the outcome from the
// next getMediaEncodingSettings call.
export function reprobeMediaEncodingSettings(): Promise<SystemMediaEncodingReprobeResult> {
  return requestJSON<SystemMediaEncodingReprobeResult>(
    "/api/v1/system/media-encoding/reprobe",
    { method: "POST" },
  );
}
