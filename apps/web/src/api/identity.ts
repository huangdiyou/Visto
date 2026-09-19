import type {
  SessionInfo,
  SetupInput,
  SetupStatus,
  UpdateProfileInput,
  WorkspaceRegistrationSettings,
  WorkspaceRegistrationSettingsInput,
} from "@review-studio/contracts";
import { requestJSON, requestVoid } from "./client";

export async function getSetupStatus(
  signal?: AbortSignal,
): Promise<SetupStatus> {
  const options: RequestOptions = {};
  if (signal) {
    options.signal = signal;
  }
  return requestJSON<SetupStatus>("/api/v1/setup/status", options);
}

export async function getSession(signal?: AbortSignal): Promise<SessionInfo> {
  const options: RequestOptions = {};
  if (signal) {
    options.signal = signal;
  }
  return requestJSON<SessionInfo>("/api/v1/session", options);
}

export async function createSetup(input: SetupInput): Promise<SessionInfo> {
  return requestJSON<SessionInfo>("/api/v1/setup", {
    method: "POST",
    body: input,
  });
}

export async function claimHostSetup(token: string): Promise<void> {
  return requestVoid("/api/v1/setup/claim", {
    method: "POST",
    body: { token: token.trim() },
  });
}

export async function login(
  email: string,
  password: string,
): Promise<SessionInfo> {
  return requestJSON<SessionInfo>("/api/v1/session", {
    method: "POST",
    body: { email: email.trim(), password },
  });
}

export async function logout(): Promise<void> {
  return requestVoid("/api/v1/session", {
    method: "DELETE",
  });
}

export function updateProfile(input: UpdateProfileInput): Promise<SessionInfo> {
  return requestJSON<SessionInfo>("/api/v1/profile", {
    method: "PATCH",
    body: input,
  });
}

export function getRegistrationSettings(
  signal?: AbortSignal,
): Promise<WorkspaceRegistrationSettings> {
  return requestJSON<WorkspaceRegistrationSettings>(
    "/api/v1/workspace/registration-settings",
    signal ? { signal } : {},
  );
}

export function updateRegistrationSettings(
  input: WorkspaceRegistrationSettingsInput,
): Promise<WorkspaceRegistrationSettings> {
  return requestJSON<WorkspaceRegistrationSettings>(
    "/api/v1/workspace/registration-settings",
    { method: "PUT", body: input },
  );
}

interface RequestOptions {
  signal?: AbortSignal;
}
