import type {
  AccessEvent,
  CommentThread,
  CreateCommentInput,
  CreateCommentThreadInput,
  CreateReviewDecisionInput,
  CreateVisitorCodeInput,
  CreateShareInput,
  IdentifyPublicShareInput,
  ItemList,
  PublicShare,
  PublicShareEntry,
  ReviewCommentAttachment,
  ReviewDecision,
  Share,
  ShareCredentials,
  ShareLink,
  ShareSecret,
  UpdateShareInput,
  UpdateCommentInput,
  VisitorCodeSecret,
} from "@review-studio/contracts";
import { requestFormJSON, requestJSON, requestVoid } from "./client";

export async function listShares(signal?: AbortSignal): Promise<Share[]> {
  const response = await requestJSON<ItemList<Share>>(
    "/api/v1/shares",
    signal ? { signal } : {},
  );
  return response.items;
}

export async function listShareAccessEvents(
  shareId: string,
  signal?: AbortSignal,
): Promise<AccessEvent[]> {
  const response = await requestJSON<ItemList<AccessEvent>>(
    `/api/v1/shares/${encodeURIComponent(shareId)}/access-events`,
    signal ? { signal } : {},
  );
  return response.items;
}

export function createShare(input: CreateShareInput): Promise<ShareSecret> {
  return requestJSON<ShareSecret>("/api/v1/shares", {
    method: "POST",
    body: input,
  });
}

export function updateShare(
  share: Share,
  input: UpdateShareInput,
): Promise<Share> {
  return requestJSON<Share>(`/api/v1/shares/${share.id}`, {
    method: "PATCH",
    body: { ...input, revision: share.revision },
  });
}

export function createShareLink(share: Share): Promise<ShareSecret> {
  return requestJSON<ShareSecret>(`/api/v1/shares/${share.id}/links`, {
    method: "POST",
  });
}

export function getShareCredentials(share: Share): Promise<ShareCredentials> {
  return requestJSON<ShareCredentials>(
    `/api/v1/shares/${encodeURIComponent(share.id)}/credentials`,
  );
}

export function createVisitorCode(
  share: Share,
  input: CreateVisitorCodeInput,
): Promise<VisitorCodeSecret> {
  return requestJSON<VisitorCodeSecret>(
    `/api/v1/shares/${share.id}/visitor-codes`,
    {
      method: "POST",
      body: input,
    },
  );
}

export function revokeShare(share: Share): Promise<Share> {
  return requestJSON<Share>(`/api/v1/shares/${share.id}/revoke`, {
    method: "POST",
    body: { revision: share.revision },
  });
}

export function revokeShareLink(link: ShareLink): Promise<ShareLink> {
  return requestJSON<ShareLink>(`/api/v1/share-links/${link.id}/revoke`, {
    method: "POST",
  });
}

export function openPublicShare(token: string): Promise<PublicShareEntry> {
  return requestJSON<PublicShareEntry>(
    `/share-api/v1/entries/${encodeURIComponent(token)}/open`,
    { method: "POST" },
  );
}

export function verifyPublicShare(password: string): Promise<PublicShareEntry> {
  return requestJSON<PublicShareEntry>("/share-api/v1/session/verify", {
    method: "POST",
    body: { password },
  });
}

export function identifyPublicShare(
  input: IdentifyPublicShareInput,
): Promise<PublicShare> {
  return requestJSON<PublicShare>("/share-api/v1/session/identity", {
    method: "POST",
    body: input,
  });
}

export async function listPublicThreads(
  itemId: string,
  signal?: AbortSignal,
): Promise<CommentThread[]> {
  const result = await requestJSON<ItemList<CommentThread>>(
    `/share-api/v1/items/${encodeURIComponent(itemId)}/threads`,
    signal ? { signal } : {},
  );
  return result.items;
}

export function createPublicThread(
  itemId: string,
  input: CreateCommentThreadInput,
): Promise<CommentThread> {
  return requestJSON<CommentThread>(
    `/share-api/v1/items/${encodeURIComponent(itemId)}/threads`,
    {
      method: "POST",
      body: input,
    },
  );
}

export function addPublicThreadComment(
  itemId: string,
  threadId: string,
  input: CreateCommentInput,
): Promise<CommentThread> {
  return requestJSON<CommentThread>(
    `/share-api/v1/items/${encodeURIComponent(
      itemId,
    )}/threads/${encodeURIComponent(threadId)}/comments`,
    {
      method: "POST",
      body: input,
    },
  );
}

export function uploadPublicCommentAttachment(
  itemId: string,
  file: File,
): Promise<ReviewCommentAttachment> {
  const body = new FormData();
  body.append("file", file, file.name);
  return requestFormJSON<ReviewCommentAttachment>(
    `/share-api/v1/items/${encodeURIComponent(itemId)}/attachments`,
    body,
  );
}

export function updatePublicComment(
  itemId: string,
  threadId: string,
  commentId: string,
  input: UpdateCommentInput,
): Promise<CommentThread> {
  return requestJSON<CommentThread>(
    `/share-api/v1/items/${encodeURIComponent(
      itemId,
    )}/threads/${encodeURIComponent(threadId)}/comments/${encodeURIComponent(
      commentId,
    )}`,
    {
      method: "PATCH",
      body: input,
    },
  );
}

export function deletePublicComment(
  itemId: string,
  threadId: string,
  commentId: string,
): Promise<void> {
  return requestVoid(
    `/share-api/v1/items/${encodeURIComponent(
      itemId,
    )}/threads/${encodeURIComponent(threadId)}/comments/${encodeURIComponent(
      commentId,
    )}`,
    {
      method: "DELETE",
    },
  );
}

export function getPublicShare(signal?: AbortSignal): Promise<PublicShare> {
  return requestJSON<PublicShare>(
    "/share-api/v1/share",
    signal ? { signal } : {},
  );
}

export function createPublicDecision(
  input: CreateReviewDecisionInput,
): Promise<ReviewDecision> {
  return requestJSON<ReviewDecision>("/share-api/v1/decisions", {
    method: "POST",
    body: input,
  });
}
