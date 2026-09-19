import type {
  CommentThread,
  CreateCommentInput,
  CreateReviewDecisionInput,
  CreateReviewSessionInput,
  ItemList,
  ReviewCommentAttachment,
  ReviewDecision,
  ReviewSession,
  UpdateCommentInput,
  UpdateReviewSessionInput,
} from "@review-studio/contracts";
import { requestFormJSON, requestJSON, requestVoid } from "./client";

export async function listReviewSessions(
  assetVersionId?: string,
  signal?: AbortSignal,
  projectId?: string,
): Promise<ReviewSession[]> {
  const baseParams = new URLSearchParams();
  if (assetVersionId) {
    baseParams.set("assetVersionId", assetVersionId);
  }
  if (projectId) {
    baseParams.set("projectId", projectId);
  }
  const items: ReviewSession[] = [];
  let offset: number | undefined = 0;
  while (offset !== undefined) {
    const params = new URLSearchParams(baseParams);
    params.set("limit", "100");
    params.set("offset", String(offset));
    const response = await requestJSON<
      ItemList<ReviewSession> & { nextOffset?: number }
    >(`/api/v1/review-sessions?${params.toString()}`, signal ? { signal } : {});
    items.push(...response.items);
    offset = response.nextOffset;
  }
  return items;
}

export function listProjectReviewSessions(
  projectId: string,
  signal?: AbortSignal,
): Promise<ReviewSession[]> {
  return listReviewSessions(undefined, signal, projectId);
}

export function getReviewSession(
  reviewId: string,
  signal?: AbortSignal,
): Promise<ReviewSession> {
  return requestJSON<ReviewSession>(
    `/api/v1/review-sessions/${encodeURIComponent(reviewId)}`,
    signal ? { signal } : {},
  );
}

export function createReviewSession(
  input: CreateReviewSessionInput,
): Promise<ReviewSession> {
  return requestJSON<ReviewSession>("/api/v1/review-sessions", {
    method: "POST",
    body: input,
  });
}

export function updateReviewSession(
  review: ReviewSession,
  input: Omit<UpdateReviewSessionInput, "revision">,
): Promise<ReviewSession> {
  return requestJSON<ReviewSession>(`/api/v1/review-sessions/${review.id}`, {
    method: "PATCH",
    body: { ...input, revision: review.revision },
  });
}

export function openReviewSession(
  review: ReviewSession,
): Promise<ReviewSession> {
  return requestJSON<ReviewSession>(
    `/api/v1/review-sessions/${review.id}/open`,
    {
      method: "POST",
      body: { revision: review.revision },
    },
  );
}

export function closeReviewSession(
  review: ReviewSession,
): Promise<ReviewSession> {
  return requestJSON<ReviewSession>(
    `/api/v1/review-sessions/${review.id}/close`,
    {
      method: "POST",
      body: { revision: review.revision },
    },
  );
}

export async function listReviewThreads(
  reviewId: string,
  signal?: AbortSignal,
): Promise<CommentThread[]> {
  const response = await requestJSON<ItemList<CommentThread>>(
    `/api/v1/review-sessions/${encodeURIComponent(reviewId)}/threads`,
    signal ? { signal } : {},
  );
  return response.items;
}

export function addReviewThreadComment(
  reviewId: string,
  threadId: string,
  input: CreateCommentInput,
): Promise<CommentThread> {
  return requestJSON<CommentThread>(
    `/api/v1/review-sessions/${encodeURIComponent(
      reviewId,
    )}/threads/${encodeURIComponent(threadId)}/comments`,
    {
      method: "POST",
      body: input,
    },
  );
}

export function uploadReviewCommentAttachment(
  reviewId: string,
  reviewItemId: string,
  file: File,
): Promise<ReviewCommentAttachment> {
  const body = new FormData();
  body.append("reviewItemId", reviewItemId);
  body.append("file", file, file.name);
  return requestFormJSON<ReviewCommentAttachment>(
    `/api/v1/review-sessions/${encodeURIComponent(reviewId)}/attachments`,
    body,
  );
}

export function updateReviewComment(
  reviewId: string,
  threadId: string,
  commentId: string,
  input: UpdateCommentInput,
): Promise<CommentThread> {
  return requestJSON<CommentThread>(
    `/api/v1/review-sessions/${encodeURIComponent(
      reviewId,
    )}/threads/${encodeURIComponent(threadId)}/comments/${encodeURIComponent(
      commentId,
    )}`,
    {
      method: "PATCH",
      body: input,
    },
  );
}

export function deleteReviewComment(
  reviewId: string,
  threadId: string,
  commentId: string,
): Promise<void> {
  return requestVoid(
    `/api/v1/review-sessions/${encodeURIComponent(
      reviewId,
    )}/threads/${encodeURIComponent(threadId)}/comments/${encodeURIComponent(
      commentId,
    )}`,
    {
      method: "DELETE",
    },
  );
}

export function resolveReviewThread(
  reviewId: string,
  thread: CommentThread,
): Promise<CommentThread> {
  return requestJSON<CommentThread>(
    `/api/v1/review-sessions/${encodeURIComponent(
      reviewId,
    )}/threads/${encodeURIComponent(thread.id)}/resolve`,
    {
      method: "POST",
      body: { revision: thread.revision },
    },
  );
}

export function reopenReviewThread(
  reviewId: string,
  thread: CommentThread,
): Promise<CommentThread> {
  return requestJSON<CommentThread>(
    `/api/v1/review-sessions/${encodeURIComponent(
      reviewId,
    )}/threads/${encodeURIComponent(thread.id)}/reopen`,
    {
      method: "POST",
      body: { revision: thread.revision },
    },
  );
}

export async function listReviewDecisions(
  reviewId: string,
  signal?: AbortSignal,
): Promise<ReviewDecision[]> {
  const response = await requestJSON<ItemList<ReviewDecision>>(
    `/api/v1/review-sessions/${encodeURIComponent(reviewId)}/decisions`,
    signal ? { signal } : {},
  );
  return response.items;
}

export function createReviewDecision(
  reviewId: string,
  input: CreateReviewDecisionInput,
): Promise<ReviewDecision> {
  return requestJSON<ReviewDecision>(
    `/api/v1/review-sessions/${encodeURIComponent(reviewId)}/decisions`,
    {
      method: "POST",
      body: input,
    },
  );
}
