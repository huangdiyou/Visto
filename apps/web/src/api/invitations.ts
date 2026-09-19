import type {
  Invitation,
  InvitationPreview,
  InvitationSecret,
  ItemList,
  SessionInfo,
} from "@review-studio/contracts";
import { requestJSON } from "./client";

export async function listInvitations(
  signal?: AbortSignal,
): Promise<Invitation[]> {
  const options: { signal?: AbortSignal } = {};
  if (signal) options.signal = signal;
  const result = await requestJSON<ItemList<Invitation>>(
    "/api/v1/invitations",
    options,
  );
  return result.items;
}

export function createInvitation(input: {
  email: string;
  role: Invitation["role"];
  expiresAt?: string;
}): Promise<InvitationSecret> {
  return requestJSON<InvitationSecret>("/api/v1/invitations", {
    method: "POST",
    body: input,
  });
}

export function resendInvitation(id: string): Promise<InvitationSecret> {
  return requestJSON<InvitationSecret>(`/api/v1/invitations/${id}/resend`, {
    method: "POST",
  });
}

export function revokeInvitation(id: string): Promise<Invitation> {
  return requestJSON<Invitation>(`/api/v1/invitations/${id}/revoke`, {
    method: "POST",
  });
}

export function previewInvitation(token: string): Promise<InvitationPreview> {
  return requestJSON<InvitationPreview>("/join-api/v1/invitations/preview", {
    method: "POST",
    body: { token },
  });
}

export function acceptInvitation(input: {
  token: string;
  displayName: string;
  password: string;
  locale: string;
}): Promise<SessionInfo> {
  return requestJSON<SessionInfo>("/join-api/v1/invitations/accept", {
    method: "POST",
    body: input,
  });
}
