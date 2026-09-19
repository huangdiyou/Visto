import {
  type FormEvent,
  type ReactNode,
  useEffect,
  useMemo,
  useRef,
  useState,
} from "react";
import {
  CalendarDays,
  Check,
  Copy,
  KeyRound,
  Plus,
  RefreshCw,
  Search,
  X,
} from "lucide-react";
import type {
  CommentThread,
  MediaLibraryItem,
  Project,
  ProjectMember,
  ReviewDecision,
  ReviewDecisionRule,
  ReviewDecisionValue,
  ReviewParticipantInput,
  ReviewSession,
  Share,
  ShareCredentials,
  ShareLink,
  UpdateReviewSessionInput,
} from "@review-studio/contracts";
import {
  addReviewThreadComment,
  closeReviewSession,
  createReviewDecision,
  createReviewSession,
  deleteReviewComment,
  getReviewSession,
  listProjectReviewSessions,
  listReviewDecisions,
  listReviewThreads,
  openReviewSession,
  reopenReviewThread,
  resolveReviewThread,
  updateReviewComment,
  updateReviewSession,
  uploadReviewCommentAttachment,
} from "./api/reviews";
import {
  createShare,
  createShareLink,
  getShareCredentials,
  listShares,
  revokeShare,
  revokeShareLink,
} from "./api/shares";
import { queryProjectMediaLibrary } from "./api/storage";
import { formatImageGeometry } from "./lib/image-annotations";
import { formatThreadTime } from "./lib/review-comments";
import { useI18n } from "./lib/i18n-react";
import {
  attachmentIds,
  CommentAttachmentField,
  CommentAttachmentGallery,
  type CommentAttachmentDraft,
} from "./CommentAttachments";

export interface ReviewCreationSeed {
  projectId: string;
  assetId: string;
  assetVersionId: string;
  assetName: string;
  versionNumber: number;
  intent: "queue" | "create";
}

interface ReviewDraft {
  name: string;
  dueAt: string;
  responsibleUserId: string;
  participants: ReviewParticipantInput[];
}

const decisionOptions: Array<{
  value: ReviewDecisionValue;
  label: string;
}> = [
  { value: "approved", label: "通过" },
  { value: "changes_requested", label: "需修改" },
  { value: "rejected", label: "拒绝" },
];

function ReviewDetail({
  review,
  initialThreadId = null,
  busy,
  canManage = true,
  onEdit,
  onOpen,
  onClose,
  onCreateShare,
  sharePanel = null,
  onReviewUpdated,
}: {
  review: ReviewSession;
  initialThreadId?: string | null;
  busy: boolean;
  canManage?: boolean;
  onEdit?: () => void;
  onOpen: () => void;
  onClose: () => void;
  onCreateShare?: () => void;
  sharePanel?: ReactNode;
  onReviewUpdated: (review: ReviewSession) => void;
}) {
  return (
    <>
      <header className="project-detail-header review-detail-header">
        <div>
          <span className="detail-kicker">
            <i className={`review-status-dot is-${review.status}`} />
            {reviewStatusLabel(review.status)}
          </span>
          <h2>{review.name}</h2>
          <p>
            {review.projectName} · 创建于 {formatDateTime(review.createdAt)}
          </p>
        </div>
        <div className="detail-actions">
          {canManage && review.status === "open" && onCreateShare ? (
            <button
              className="secondary-button"
              type="button"
              disabled={busy}
              onClick={onCreateShare}
            >
              创建分享
            </button>
          ) : null}
          {canManage && review.status !== "closed" && onEdit ? (
            <button
              className="secondary-button"
              type="button"
              disabled={busy}
              onClick={onEdit}
            >
              编辑
            </button>
          ) : null}
          {canManage &&
          (review.status === "draft" || review.status === "closed") ? (
            <button
              className="primary-button"
              type="button"
              disabled={busy}
              onClick={onOpen}
            >
              {review.status === "closed" ? "重新开启" : "开启审阅"}
            </button>
          ) : null}
          {canManage && review.status === "open" ? (
            <button
              className="text-danger-button"
              type="button"
              disabled={busy}
              onClick={() => {
                if (
                  window.confirm(
                    "结束后访客仍可查看历史，但不能新增评论。确定结束？",
                  )
                ) {
                  onClose();
                }
              }}
            >
              结束审阅
            </button>
          ) : null}
        </div>
      </header>

      <ReviewStatusNote status={review.status} />
      {sharePanel}

      <div className="review-workflow-summary">
        <div>
          <span>工作流</span>
          <strong>
            {review.templateRevision
              ? `模板快照 V${review.templateRevision}`
              : "自定义审阅"}
          </strong>
        </div>
        <div>
          <span>负责人</span>
          <strong>{review.responsibleName || "未指定"}</strong>
        </div>
        <div>
          <span>决策规则</span>
          <strong>{decisionRuleLabel(review.decisionRule)}</strong>
        </div>
        <div>
          <span>分享下载</span>
          <strong>{review.allowDownload ? "默认允许" : "默认关闭"}</strong>
        </div>
      </div>

      <div className="review-metrics" aria-label="审阅摘要">
        <div>
          <span>固定版本</span>
          <strong>{review.items.length}</strong>
        </div>
        <div>
          <span>参与者</span>
          <strong>{review.participants.length}</strong>
        </div>
        <div>
          <span>截止时间</span>
          <strong>{review.dueAt ? formatDate(review.dueAt) : "未设置"}</strong>
        </div>
      </div>

      <ReviewDecisionPanel review={review} onReviewUpdated={onReviewUpdated} />

      <section className="review-section">
        <div className="section-heading">
          <div>
            <p className="eyebrow">PINNED VERSIONS</p>
            <h2>审阅内容</h2>
          </div>
        </div>
        <div className="review-item-list">
          {review.items.map((item) => (
            <article className="review-item-row" key={item.id}>
              <span className="review-version-mark">V{item.versionNumber}</span>
              <div>
                <strong>{item.assetName}</strong>
                <span>固定资产版本</span>
              </div>
              <em>{reviewItemStatusLabel(item.status)}</em>
            </article>
          ))}
        </div>
      </section>

      <section className="review-section">
        <div className="section-heading">
          <div>
            <p className="eyebrow">PARTICIPANTS</p>
            <h2>参与者</h2>
          </div>
        </div>
        {review.participants.length > 0 ? (
          <div className="review-participant-list">
            {review.participants.map((participant) => (
              <span key={participant.id}>
                <i aria-hidden="true">
                  {participant.displayName.slice(0, 1).toUpperCase()}
                </i>
                <strong>{participant.displayName}</strong>
                <em>{participant.role === "observer" ? "观察者" : "审阅者"}</em>
              </span>
            ))}
          </div>
        ) : (
          <p className="review-muted">暂未指定参与者。</p>
        )}
      </section>

      <ReviewThreadPanel review={review} initialThreadId={initialThreadId} />
    </>
  );
}

function ReviewDecisionPanel({
  review,
  onReviewUpdated,
}: {
  review: ReviewSession;
  onReviewUpdated: (review: ReviewSession) => void;
}) {
  const [decisions, setDecisions] = useState<ReviewDecision[]>([]);
  const [reviewItemId, setReviewItemId] = useState("");
  const [decision, setDecision] = useState<ReviewDecisionValue>("approved");
  const [note, setNote] = useState("");
  const [loading, setLoading] = useState(true);
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState<string | null>(null);
  const canDecide = review.status !== "draft" && review.status !== "closed";

  useEffect(() => {
    const controller = new AbortController();
    setLoading(true);
    setError(null);
    listReviewDecisions(review.id, controller.signal)
      .then(setDecisions)
      .catch((loadError: unknown) => {
        if (!isAbort(loadError)) {
          setError(errorMessage(loadError, "无法读取审核结论"));
        }
      })
      .finally(() => setLoading(false));
    return () => controller.abort();
  }, [review.id]);

  async function submit(event: FormEvent<HTMLFormElement>) {
    event.preventDefault();
    setBusy(true);
    setError(null);
    try {
      const created = await createReviewDecision(review.id, {
        reviewItemId: reviewItemId || null,
        decision,
        note: note.trim(),
      });
      setDecisions((current) => [created, ...current]);
      setNote("");
      onReviewUpdated(await getReviewSession(review.id));
    } catch (submitError) {
      setError(errorMessage(submitError, "无法提交审核结论"));
    } finally {
      setBusy(false);
    }
  }

  return (
    <section className="review-section review-decision-panel">
      <div className="section-heading">
        <div>
          <p className="eyebrow">REVIEW DECISIONS</p>
          <h2>审核结论</h2>
        </div>
        <span className="review-decision-count">{decisions.length} 条记录</span>
      </div>

      {canDecide ? (
        <form className="review-decision-form" onSubmit={submit}>
          <label>
            <span>结论范围</span>
            <select
              value={reviewItemId}
              onChange={(event) => setReviewItemId(event.target.value)}
            >
              <option value="">整场审阅</option>
              {review.items.map((item) => (
                <option value={item.id} key={item.id}>
                  V{item.versionNumber} · {item.assetName}
                </option>
              ))}
            </select>
          </label>
          <div>
            <span>正式结论</span>
            <div
              className="review-decision-options"
              role="radiogroup"
              aria-label="审核结论"
            >
              {decisionOptions.map((option) => (
                <button
                  className={decision === option.value ? "is-active" : ""}
                  type="button"
                  role="radio"
                  aria-checked={decision === option.value}
                  key={option.value}
                  onClick={() => setDecision(option.value)}
                >
                  {option.label}
                </button>
              ))}
            </div>
          </div>
          <label>
            <span>说明（可选）</span>
            <textarea
              value={note}
              maxLength={4000}
              placeholder="记录通过依据，或说明需要调整的内容"
              onChange={(event) => setNote(event.target.value)}
            />
          </label>
          <div className="review-decision-submit">
            <span>提交后将保留在结论历史中。</span>
            <button className="primary-button" type="submit" disabled={busy}>
              {busy ? "提交中" : "提交结论"}
            </button>
          </div>
        </form>
      ) : (
        <p className="review-muted">
          {review.status === "draft"
            ? "开启审阅后才能提交正式结论。"
            : "审阅已结束，结论历史保持只读。"}
        </p>
      )}

      {error ? (
        <p className="catalog-error" role="alert">
          {error}
        </p>
      ) : null}
      {loading ? <p className="review-muted">正在读取结论历史。</p> : null}
      {!loading && decisions.length === 0 ? (
        <p className="review-muted">还没有正式结论。</p>
      ) : null}
      <div className="review-decision-history">
        {decisions.map((item) => (
          <article key={item.id}>
            <span className={`is-${item.decision}`}>
              {reviewDecisionLabel(item.decision)}
            </span>
            <div>
              <strong>{reviewDecisionTarget(review, item)}</strong>
              <p>{item.note || "未填写说明"}</p>
              <em>
                {item.actor.displayName} · {formatDateTime(item.createdAt)}
              </em>
            </div>
          </article>
        ))}
      </div>
    </section>
  );
}

function ReviewThreadPanel({
  review,
  initialThreadId,
}: {
  review: ReviewSession;
  initialThreadId?: string | null;
}) {
  const [threads, setThreads] = useState<CommentThread[]>([]);
  const [activeThreadId, setActiveThreadId] = useState<string | null>(null);
  const [loading, setLoading] = useState(true);
  const [error, setError] = useState<string | null>(null);
  const openCount = threads.filter((thread) => thread.status === "open").length;
  const resolvedCount = threads.filter(
    (thread) => thread.status === "resolved",
  ).length;

  useEffect(() => {
    const controller = new AbortController();
    setLoading(true);
    setError(null);
    listReviewThreads(review.id, controller.signal)
      .then((items) => {
        setThreads(items);
        setActiveThreadId((current) =>
          current && items.some((item) => item.id === current)
            ? current
            : initialThreadId &&
                items.some((item) => item.id === initialThreadId)
              ? initialThreadId
              : (items[0]?.id ?? null),
        );
      })
      .catch((loadError: unknown) => {
        if (!isAbort(loadError)) {
          setError(errorMessage(loadError, "无法读取评论线程"));
        }
      })
      .finally(() => setLoading(false));
    return () => controller.abort();
  }, [initialThreadId, review.id]);

  function replaceThread(updated: CommentThread) {
    setThreads((current) =>
      current.map((thread) => (thread.id === updated.id ? updated : thread)),
    );
  }

  function removeThread(threadId: string) {
    setThreads((current) => current.filter((thread) => thread.id !== threadId));
    setActiveThreadId((current) => (current === threadId ? null : current));
  }

  return (
    <section className="review-section review-thread-panel">
      <div className="section-heading">
        <div>
          <p className="eyebrow">COMMENT THREADS</p>
          <h2>评论线程</h2>
        </div>
        <div className="review-thread-summary">
          <span>{openCount} 待处理</span>
          <span>{resolvedCount} 已解决</span>
        </div>
      </div>
      {error ? (
        <p className="catalog-error" role="alert">
          {error}
        </p>
      ) : null}
      {loading ? <p className="review-muted">正在读取评论线程。</p> : null}
      {!loading && threads.length === 0 ? (
        <p className="review-muted">还没有评论线程。</p>
      ) : null}
      <div className="review-thread-list">
        {threads.map((thread) => (
          <ReviewThreadCard
            active={activeThreadId === thread.id}
            allowReply={review.status === "open" && thread.canReply}
            key={thread.id}
            reviewId={review.id}
            thread={thread}
            onSelect={() => setActiveThreadId(thread.id)}
            onThreadDeleted={removeThread}
            onThreadUpdated={replaceThread}
          />
        ))}
      </div>
    </section>
  );
}

function ReviewThreadCard({
  reviewId,
  thread,
  active,
  allowReply,
  onSelect,
  onThreadUpdated,
  onThreadDeleted,
}: {
  reviewId: string;
  thread: CommentThread;
  active: boolean;
  allowReply: boolean;
  onSelect: () => void;
  onThreadUpdated: (thread: CommentThread) => void;
  onThreadDeleted: (threadId: string) => void;
}) {
  const [replyBody, setReplyBody] = useState("");
  const [replyAttachments, setReplyAttachments] = useState<
    CommentAttachmentDraft[]
  >([]);
  const [editingId, setEditingId] = useState<string | null>(null);
  const [editingBody, setEditingBody] = useState("");
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState<string | null>(null);
  const firstComment = thread.comments[0];
  const latestComment = thread.comments[thread.comments.length - 1];

  async function submitReply(event: FormEvent<HTMLFormElement>) {
    event.preventDefault();
    const body = replyBody.trim();
    if (!body) {
      setError("请输入回复内容");
      return;
    }
    if (replyAttachments.some((item) => item.uploading)) {
      setError("图片还在上传，请稍后再提交");
      return;
    }
    if (replyAttachments.some((item) => item.error || !item.attachment)) {
      setError("有图片上传失败，请删除后重试");
      return;
    }
    setBusy(true);
    setError(null);
    try {
      const updated = await addReviewThreadComment(reviewId, thread.id, {
        body,
        attachmentIds: attachmentIds(replyAttachments),
      });
      onThreadUpdated(updated);
      setReplyBody("");
      setReplyAttachments([]);
    } catch (replyError) {
      setError(replyError instanceof Error ? replyError.message : "回复失败");
    } finally {
      setBusy(false);
    }
  }

  async function submitEdit(
    event: FormEvent<HTMLFormElement>,
    commentId: string,
  ) {
    event.preventDefault();
    const body = editingBody.trim();
    if (!body) {
      setError("评论内容不能为空");
      return;
    }
    setBusy(true);
    setError(null);
    try {
      const updated = await updateReviewComment(
        reviewId,
        thread.id,
        commentId,
        { body },
      );
      onThreadUpdated(updated);
      setEditingId(null);
      setEditingBody("");
    } catch (editError) {
      setError(errorMessage(editError, "编辑失败"));
    } finally {
      setBusy(false);
    }
  }

  async function removeComment(commentId: string) {
    if (!window.confirm("确定删除这条评论吗？")) {
      return;
    }
    setBusy(true);
    setError(null);
    try {
      await deleteReviewComment(reviewId, thread.id, commentId);
      if (thread.comments.length <= 1) {
        onThreadDeleted(thread.id);
      } else {
        onThreadUpdated({
          ...thread,
          comments: thread.comments.filter(
            (comment) => comment.id !== commentId,
          ),
          revision: thread.revision + 1,
          updatedAt: new Date().toISOString(),
        });
      }
    } catch (deleteError) {
      setError(errorMessage(deleteError, "删除失败"));
    } finally {
      setBusy(false);
    }
  }

  async function updateStatus(action: "resolve" | "reopen") {
    setBusy(true);
    setError(null);
    try {
      const updated =
        action === "resolve"
          ? await resolveReviewThread(reviewId, thread)
          : await reopenReviewThread(reviewId, thread);
      onThreadUpdated(updated);
    } catch (statusError) {
      setError(errorMessage(statusError, "无法更新线程状态"));
    } finally {
      setBusy(false);
    }
  }

  return (
    <article className={`review-thread-card${active ? " is-active" : ""}`}>
      <button className="review-thread-top" type="button" onClick={onSelect}>
        <span>{reviewThreadLocation(thread)}</span>
        <strong>{firstComment?.body ?? ""}</strong>
        <em>
          {commentThreadStatusLabel(thread.status)} ·{" "}
          {latestComment?.author.displayName ?? thread.author.displayName} ·{" "}
          {formatDateTime(latestComment?.createdAt ?? thread.createdAt)}
        </em>
      </button>
      {active ? (
        <div className="review-thread-body">
          <div className="review-thread-actions">
            {thread.canResolve ? (
              <button
                className="secondary-button"
                type="button"
                disabled={busy}
                onClick={() => void updateStatus("resolve")}
              >
                标记解决
              </button>
            ) : null}
            {thread.canReopen ? (
              <button
                className="secondary-button"
                type="button"
                disabled={busy}
                onClick={() => void updateStatus("reopen")}
              >
                重新打开
              </button>
            ) : null}
          </div>
          {thread.status === "resolved" ? (
            <p className="review-thread-state">
              已由 {thread.resolvedBy?.displayName ?? "管理员"} 解决
              {thread.resolvedAt
                ? ` · ${formatDateTime(thread.resolvedAt)}`
                : ""}
            </p>
          ) : null}
          <div className="review-thread-comments">
            {thread.comments.map((comment) => (
              <article className="review-thread-comment" key={comment.id}>
                <header>
                  <strong>{comment.author.displayName}</strong>
                  <span>
                    {formatDateTime(comment.createdAt)}
                    {comment.editedAt ? " · 已编辑" : ""}
                  </span>
                </header>
                {editingId === comment.id ? (
                  <form
                    className="review-inline-comment-form"
                    onSubmit={(event) => submitEdit(event, comment.id)}
                  >
                    <textarea
                      value={editingBody}
                      maxLength={4000}
                      onChange={(event) => setEditingBody(event.target.value)}
                    />
                    <div>
                      <button
                        className="secondary-button"
                        type="button"
                        disabled={busy}
                        onClick={() => {
                          setEditingId(null);
                          setEditingBody("");
                        }}
                      >
                        取消
                      </button>
                      <button
                        className="primary-button"
                        type="submit"
                        disabled={busy}
                      >
                        保存
                      </button>
                    </div>
                  </form>
                ) : (
                  <>
                    <p>{comment.body}</p>
                    <CommentAttachmentGallery
                      attachments={comment.attachments ?? []}
                    />
                  </>
                )}
                {comment.canEdit || comment.canDelete ? (
                  <div className="review-comment-actions">
                    {comment.canEdit ? (
                      <button
                        type="button"
                        disabled={busy}
                        onClick={() => {
                          setEditingId(comment.id);
                          setEditingBody(comment.body);
                        }}
                      >
                        编辑
                      </button>
                    ) : null}
                    {comment.canDelete ? (
                      <button
                        type="button"
                        disabled={busy}
                        onClick={() => void removeComment(comment.id)}
                      >
                        删除
                      </button>
                    ) : null}
                  </div>
                ) : null}
              </article>
            ))}
          </div>
          {allowReply ? (
            <form className="review-thread-reply-form" onSubmit={submitReply}>
              <CommentAttachmentField
                value={replyBody}
                maxLength={4000}
                placeholder="回复这个问题"
                disabled={busy}
                attachments={replyAttachments}
                onAttachmentsChange={setReplyAttachments}
                uploadFile={(file) =>
                  uploadReviewCommentAttachment(
                    reviewId,
                    thread.reviewItemId,
                    file,
                  )
                }
                onChange={setReplyBody}
              />
              <button className="primary-button" type="submit" disabled={busy}>
                {busy ? "发送中" : "回复"}
              </button>
            </form>
          ) : (
            <p className="review-thread-state">
              {thread.status === "resolved"
                ? "这个问题已解决；需要继续讨论时先重新打开。"
                : "审阅未开启，暂时不能继续回复。"}
            </p>
          )}
          {error ? (
            <p className="catalog-error" role="alert">
              {error}
            </p>
          ) : null}
        </div>
      ) : null}
    </article>
  );
}

function ReviewStatusNote({ status }: { status: ReviewSession["status"] }) {
  const copy = reviewStatusCopy(status);
  return (
    <div className={`review-status-note is-${status}`} role="status">
      <strong>{copy.title}</strong>
      {copy.description ? <span>{copy.description}</span> : null}
    </div>
  );
}

function ProjectReviewEditorForm({
  review,
  members,
  busy,
  onCancel,
  onSubmit,
}: {
  review: ReviewSession;
  members: ProjectMember[];
  busy: boolean;
  onCancel: () => void;
  onSubmit: (input: Omit<UpdateReviewSessionInput, "revision">) => void;
}) {
  const [draft, setDraft] = useState<ReviewDraft>(() => ({
    name: review.name,
    dueAt: toDateTimeLocal(review.dueAt),
    responsibleUserId: review.responsibleUserId ?? "",
    participants: review.participants.map((participant) => ({
      userId: participant.userId,
      displayName: participant.displayName,
      role: participant.role,
    })),
  }));
  const [localError, setLocalError] = useState<string | null>(null);

  function updateParticipant(
    index: number,
    participant: ReviewParticipantInput,
  ) {
    setDraft((current) => {
      const participants = [...current.participants];
      participants[index] = participant;
      return { ...current, participants };
    });
  }

  function submit(event: FormEvent<HTMLFormElement>) {
    event.preventDefault();
    const name = draft.name.trim();
    if (!name) {
      setLocalError("请输入审阅名称");
      return;
    }
    const participants = draft.participants
      .map((item) => ({
        userId: item.userId,
        displayName: item.displayName.trim(),
        role: item.role,
      }))
      .filter((item) => item.userId || item.displayName);
    const duplicateNames = new Set<string>();
    const duplicateUsers = new Set<string>();
    for (const participant of participants) {
      if (participant.userId) {
        if (duplicateUsers.has(participant.userId)) {
          setLocalError("同一个成员不能重复加入审阅。");
          return;
        }
        duplicateUsers.add(participant.userId);
      }
      const key = participant.displayName.toLocaleLowerCase();
      if (duplicateNames.has(key)) {
        setLocalError("参与者名称不能重复。");
        return;
      }
      duplicateNames.add(key);
    }
    setLocalError(null);
    onSubmit({
      name,
      dueAt: draft.dueAt ? new Date(draft.dueAt).toISOString() : null,
      responsibleUserId: draft.responsibleUserId || null,
      participants,
    });
  }

  return (
    <form className="review-editor project-review-editor" onSubmit={submit}>
      <header>
        <div>
          <p className="eyebrow">EDIT REVIEW</p>
          <h2>编辑审阅设置</h2>
        </div>
      </header>

      <div className="review-workflow-summary">
        <div>
          <span>模板</span>
          <strong>
            {review.templateRevision
              ? `模板快照 V${review.templateRevision}`
              : "自定义审阅"}
          </strong>
        </div>
        <div>
          <span>固定版本</span>
          <strong>{review.items.length}</strong>
        </div>
      </div>

      <div className="review-form-grid">
        <label className="field">
          <span>审阅名称</span>
          <input
            value={draft.name}
            autoFocus
            maxLength={120}
            onChange={(event) =>
              setDraft((current) => ({
                ...current,
                name: event.target.value,
              }))
            }
          />
        </label>
        <label className="field">
          <span>截止时间</span>
          <input
            type="datetime-local"
            value={draft.dueAt}
            onChange={(event) =>
              setDraft((current) => ({
                ...current,
                dueAt: event.target.value,
              }))
            }
          />
        </label>
      </div>

      <div className="review-form-grid">
        <label className="field">
          <span>负责人</span>
          <select
            value={draft.responsibleUserId}
            onChange={(event) =>
              setDraft((current) => ({
                ...current,
                responsibleUserId: event.target.value,
              }))
            }
          >
            <option value="">未指定</option>
            {members.map((member) => (
              <option key={member.userId} value={member.userId}>
                {member.displayName} · {projectMemberRoleLabel(member.roleKey)}
              </option>
            ))}
          </select>
        </label>
        <label className="field">
          <span>模板来源</span>
          <input
            value={
              review.templateRevision
                ? `模板快照 V${review.templateRevision}`
                : "自定义审阅"
            }
            readOnly
          />
        </label>
      </div>

      <section className="review-participant-editor">
        <div className="section-heading">
          <div>
            <p className="eyebrow">PARTICIPANTS</p>
            <h2>参与者</h2>
          </div>
          <button
            className="secondary-button"
            type="button"
            onClick={() =>
              setDraft((current) => ({
                ...current,
                participants: [
                  ...current.participants,
                  { userId: null, displayName: "", role: "reviewer" },
                ],
              }))
            }
          >
            添加参与者
          </button>
        </div>
        <div className="review-participant-inputs">
          {draft.participants.map((participant, index) => (
            <div className="review-participant-input" key={index}>
              <select
                aria-label={`参与者 ${index + 1}`}
                value={
                  participant.userId ??
                  (participant.displayName ? `legacy:${index}` : "")
                }
                onChange={(event) => {
                  const member = members.find(
                    (item) => item.userId === event.target.value,
                  );
                  updateParticipant(index, {
                    ...participant,
                    userId: member?.userId ?? null,
                    displayName: member?.displayName ?? participant.displayName,
                  });
                }}
              >
                <option value="">选择项目成员</option>
                {!participant.userId && participant.displayName ? (
                  <option value={`legacy:${index}`}>
                    {participant.displayName} · 外部称呼
                  </option>
                ) : null}
                {members.map((member) => (
                  <option key={member.userId} value={member.userId}>
                    {member.displayName}
                  </option>
                ))}
              </select>
              <select
                aria-label={`参与者 ${index + 1} 角色`}
                value={participant.role}
                onChange={(event) =>
                  updateParticipant(index, {
                    ...participant,
                    role: event.target.value as ReviewParticipantInput["role"],
                  })
                }
              >
                <option value="reviewer">审阅者</option>
                <option value="observer">观察者</option>
              </select>
              <button
                className="text-danger-button"
                type="button"
                onClick={() =>
                  setDraft((current) => ({
                    ...current,
                    participants: current.participants.filter(
                      (_, participantIndex) => participantIndex !== index,
                    ),
                  }))
                }
              >
                移除
              </button>
            </div>
          ))}
          {draft.participants.length === 0 ? (
            <p className="review-muted">暂未指定参与者。</p>
          ) : null}
        </div>
      </section>

      {localError ? (
        <p className="catalog-error" role="alert">
          {localError}
        </p>
      ) : null}
      <div className="editor-actions review-editor-actions">
        <button
          className="secondary-button"
          type="button"
          disabled={busy}
          onClick={onCancel}
        >
          取消
        </button>
        <button className="primary-button" type="submit" disabled={busy}>
          {busy ? "保存中" : "保存审阅"}
        </button>
      </div>
    </form>
  );
}

interface ProjectReviewQueueItem {
  assetId: string;
  assetVersionId: string;
  assetName: string;
  versionNumber: number;
  processingStatus: string;
}

type ProjectReviewSort =
  | "workflow"
  | "updated_desc"
  | "created_desc"
  | "due_asc";
type ProjectReviewStatusFilter = ReviewSession["status"] | "all";
type CopyNotice = { tone: "success" | "error"; message: string } | null;
type ReviewToast = { id: number; tone: "success" | "error"; message: string };
type ReviewExpiryMode = "forever" | "custom";
type ReviewLaunchResult = {
  reviewName: string;
  url: string | null;
  password: string | null;
};

export function ProjectReviewsWorkspace({
  project,
  members,
  canCreateReviews,
  canCreateShares,
  seed,
  initialReviewId = null,
  initialThreadId = null,
  onSeedConsumed,
  onOpenMedia,
}: {
  project: Project;
  members: ProjectMember[];
  canCreateReviews: boolean;
  canCreateShares: boolean;
  seed: ReviewCreationSeed | null;
  initialReviewId?: string | null;
  initialThreadId?: string | null;
  onSeedConsumed: () => void;
  onOpenMedia: () => void;
}) {
  const { t } = useI18n();
  const activeMembers = useMemo(
    () =>
      members.filter(
        (member) =>
          member.status === "active" &&
          (member.permissions["reviews.create"] ||
            member.permissions["reviews.comment"] ||
            member.permissions["reviews.decide"] ||
            member.roleKey === "primary_owner" ||
            member.roleKey === "supervisor"),
      ),
    [members],
  );
  const [reviews, setReviews] = useState<ReviewSession[]>([]);
  const [selectedId, setSelectedId] = useState<string | null>(null);
  const [candidateItems, setCandidateItems] = useState<MediaLibraryItem[]>([]);
  const [candidateTotal, setCandidateTotal] = useState(0);
  const [candidatePage, setCandidatePage] = useState(1);
  const [candidateSearchDraft, setCandidateSearchDraft] = useState("");
  const [candidateSearch, setCandidateSearch] = useState("");
  const [mediaPickerOpen, setMediaPickerOpen] = useState(false);
  const [queue, setQueue] = useState<ProjectReviewQueueItem[]>([]);
  const [name, setName] = useState("");
  const [nameTouched, setNameTouched] = useState(false);
  const [expiryMode, setExpiryMode] = useState<ReviewExpiryMode>("forever");
  const [expiryDate, setExpiryDate] = useState("");
  const [responsibleUserId, setResponsibleUserId] = useState("");
  const [selectedMemberIds, setSelectedMemberIds] = useState<string[]>([]);
  const [decisionRule, setDecisionRule] =
    useState<ReviewDecisionRule>("any_reviewer");
  const [allowDownload, setAllowDownload] = useState(false);
  const [allowComment, setAllowComment] = useState(true);
  const [passwordEnabled, setPasswordEnabled] = useState(false);
  const [accessPassword, setAccessPassword] = useState(() =>
    generateReviewAccessCode(),
  );
  const [launchResult, setLaunchResult] = useState<ReviewLaunchResult | null>(
    null,
  );
  const [launchNotice, setLaunchNotice] = useState<CopyNotice>(null);
  const [editingReview, setEditingReview] = useState<ReviewSession | null>(
    null,
  );
  const [statusFilter, setStatusFilter] =
    useState<ProjectReviewStatusFilter>("all");
  const [sort, setSort] = useState<ProjectReviewSort>("workflow");
  const [search, setSearch] = useState("");
  const [loading, setLoading] = useState(true);
  const [mediaLoading, setMediaLoading] = useState(false);
  const [busy, setBusy] = useState(false);
  const [, setError] = useState<string | null>(null);
  const [toast, setToast] = useState<ReviewToast | null>(null);
  const toastTimerRef = useRef<number | null>(null);
  const composerRef = useRef<HTMLElement | null>(null);
  const nameInputRef = useRef<HTMLInputElement | null>(null);

  const visibleReviews = useMemo(() => {
    const query = search.trim().toLocaleLowerCase();
    return reviews
      .filter((review) => {
        if (statusFilter !== "all" && review.status !== statusFilter) {
          return false;
        }
        if (!query) {
          return true;
        }
        return (
          review.name.toLocaleLowerCase().includes(query) ||
          review.items.some((item) =>
            item.assetName.toLocaleLowerCase().includes(query),
          )
        );
      })
      .sort((left, right) => sortProjectReviews(left, right, sort));
  }, [reviews, search, sort, statusFilter]);

  const selected =
    visibleReviews.find((review) => review.id === selectedId) ??
    visibleReviews[0] ??
    null;
  const activeEditingReview =
    editingReview && selected?.id === editingReview.id ? editingReview : null;
  const candidatePageCount = Math.max(1, Math.ceil(candidateTotal / 10));

  function showToast(message: string, tone: ReviewToast["tone"] = "error") {
    if (toastTimerRef.current !== null) {
      window.clearTimeout(toastTimerRef.current);
    }
    const id = Date.now();
    setToast({ id, tone, message });
    toastTimerRef.current = window.setTimeout(() => {
      setToast((current) => (current?.id === id ? null : current));
      toastTimerRef.current = null;
    }, 3600);
  }

  function showError(message: string) {
    setError(message);
    showToast(message, "error");
  }

  const reviewStats = useMemo(
    () => ({
      open: reviews.filter((review) => review.status === "open").length,
      draft: reviews.filter((review) => review.status === "draft").length,
      approved: reviews.filter((review) => review.status === "approved").length,
      changesRequested: reviews.filter(
        (review) => review.status === "changes_requested",
      ).length,
    }),
    [reviews],
  );

  useEffect(() => {
    return () => {
      if (toastTimerRef.current !== null) {
        window.clearTimeout(toastTimerRef.current);
      }
    };
  }, []);

  useEffect(() => {
    const controller = new AbortController();
    setLoading(true);
    setError(null);
    listProjectReviewSessions(project.id, controller.signal)
      .then((items) => {
        setReviews(items);
        setSelectedId((current) =>
          current && items.some((item) => item.id === current)
            ? current
            : initialReviewId &&
                items.some((item) => item.id === initialReviewId)
              ? initialReviewId
              : (items[0]?.id ?? null),
        );
      })
      .catch((loadError: unknown) => {
        if (!isAbort(loadError)) {
          showError(errorMessage(loadError, "无法读取项目审阅"));
        }
      })
      .finally(() => {
        if (!controller.signal.aborted) {
          setLoading(false);
        }
      });
    return () => controller.abort();
  }, [initialReviewId, project.id]);

  useEffect(() => {
    if (!mediaPickerOpen) {
      return;
    }
    const timeout = window.setTimeout(() => {
      setCandidateSearch(candidateSearchDraft.trim());
      setCandidatePage(1);
    }, 250);
    return () => window.clearTimeout(timeout);
  }, [candidateSearchDraft, mediaPickerOpen]);

  useEffect(() => {
    if (!mediaPickerOpen) {
      return;
    }
    const controller = new AbortController();
    setMediaLoading(true);
    queryProjectMediaLibrary(
      project.id,
      {
        page: candidatePage,
        pageSize: 10,
        search: candidateSearch,
        sort: "modified_desc",
      },
      controller.signal,
    )
      .then((page) => {
        setCandidateItems(page.items.filter((item) => item.asset));
        setCandidateTotal(page.total);
      })
      .catch((loadError: unknown) => {
        if (!isAbort(loadError)) {
          showError(errorMessage(loadError, "无法读取项目媒体"));
        }
      })
      .finally(() => {
        if (!controller.signal.aborted) {
          setMediaLoading(false);
        }
      });
    return () => controller.abort();
  }, [candidatePage, candidateSearch, mediaPickerOpen, project.id]);

  useEffect(() => {
    if (!mediaPickerOpen) {
      return;
    }
    const closeOnEscape = (event: KeyboardEvent) => {
      if (event.key === "Escape") {
        setMediaPickerOpen(false);
      }
    };
    window.addEventListener("keydown", closeOnEscape);
    return () => window.removeEventListener("keydown", closeOnEscape);
  }, [mediaPickerOpen]);

  useEffect(() => {
    if (!nameTouched) {
      setName(defaultReviewName(queue));
    }
  }, [nameTouched, queue]);

  useEffect(() => {
    if (responsibleUserId && !selectedMemberIds.includes(responsibleUserId)) {
      setResponsibleUserId("");
    }
  }, [responsibleUserId, selectedMemberIds]);

  useEffect(() => {
    if (!seed || seed.projectId !== project.id) {
      return;
    }
    addQueueItem({
      assetId: seed.assetId,
      assetVersionId: seed.assetVersionId,
      assetName: seed.assetName,
      versionNumber: seed.versionNumber,
      processingStatus: "ready",
    });
    if (seed.intent === "create") {
      window.setTimeout(() => {
        composerRef.current?.scrollIntoView({
          behavior: "smooth",
          block: "start",
        });
        nameInputRef.current?.focus();
      }, 0);
    }
    onSeedConsumed();
  }, [onSeedConsumed, project.id, seed]);

  function addQueueItem(item: ProjectReviewQueueItem) {
    setQueue((current) =>
      current.some(
        (candidate) => candidate.assetVersionId === item.assetVersionId,
      )
        ? current
        : [...current, item],
    );
    setError(null);
  }

  function addCandidate(item: MediaLibraryItem) {
    if (!item.asset) {
      return;
    }
    addQueueItem({
      assetId: item.asset.id,
      assetVersionId: item.asset.versionId,
      assetName: item.asset.name,
      versionNumber: item.asset.versionNumber,
      processingStatus: item.asset.type,
    });
  }

  async function submitReview(event: FormEvent<HTMLFormElement>) {
    event.preventDefault();
    const trimmedName = name.trim();
    if (!trimmedName) {
      showError("请输入审阅名称");
      return;
    }
    if (
      reviews.some(
        (review) =>
          review.name.trim().toLocaleLowerCase() ===
          trimmedName.toLocaleLowerCase(),
      )
    ) {
      showError("当前项目已经有同名审阅，请换一个名称。");
      return;
    }
    if (queue.length === 0) {
      showError("请先把媒体版本加入待审阅清单");
      return;
    }
    const participants = selectedMemberIds
      .map((userId) => activeMembers.find((member) => member.userId === userId))
      .filter((member): member is ProjectMember => Boolean(member))
      .map<ReviewParticipantInput>((member) => ({
        userId: member.userId,
        displayName: member.displayName,
        role: "reviewer",
      }));
    if (participants.length === 0) {
      showError("请至少选择一位审阅成员");
      return;
    }
    if (decisionRule === "responsible_only" && !responsibleUserId) {
      showError("请选择负责最终通过的成员");
      return;
    }

    setBusy(true);
    setError(null);
    setLaunchResult(null);
    setLaunchNotice(null);
    let createdReview: ReviewSession | null = null;
    try {
      const expiresAt =
        expiryMode === "custom" && expiryDate
          ? endOfLocalDayISO(expiryDate)
          : null;
      createdReview = await createReviewSession({
        projectId: project.id,
        collectionId: null,
        name: trimmedName,
        dueAt: expiresAt,
        templateId: null,
        responsibleUserId:
          decisionRule === "responsible_only"
            ? responsibleUserId || null
            : null,
        allowDownload,
        decisionRule,
        participants,
        items: queue.map((item) => ({
          assetId: item.assetId,
          assetVersionId: item.assetVersionId,
        })),
      });

      let activeReview = createdReview;
      let shareURL: string | null = null;
      if (canCreateShares) {
        activeReview = await openReviewSession(createdReview);
        createdReview = activeReview;
        const result = await createShare({
          reviewSessionId: activeReview.id,
          name: `${trimmedName} 分享`,
          allowComment,
          allowDownload,
          requireNickname: false,
          expiresAt,
          password: passwordEnabled ? accessPassword : null,
          notifyUserIds: selectedMemberIds,
        });
        shareURL = absolutePublicURL(result.url);
        const sharePassword =
          result.password ?? (passwordEnabled ? accessPassword : null);
        await copyShareText(
          shareURL,
          setLaunchNotice,
          "审阅已创建，分享链接已复制。",
        );
        setLaunchResult({
          reviewName: trimmedName,
          url: shareURL,
          password: sharePassword,
        });
      }

      setReviews((current) => [
        activeReview,
        ...current.filter((review) => review.id !== activeReview.id),
      ]);
      setSelectedId(activeReview.id);
      if (!canCreateShares) {
        setLaunchResult({
          reviewName: trimmedName,
          url: shareURL,
          password: null,
        });
      }
      setQueue([]);
      setName("");
      setNameTouched(false);
      setExpiryMode("forever");
      setExpiryDate("");
      setResponsibleUserId("");
      setSelectedMemberIds([]);
      setDecisionRule("any_reviewer");
      setAllowDownload(false);
      setAllowComment(true);
      setPasswordEnabled(false);
      setAccessPassword(generateReviewAccessCode());
    } catch (submitError) {
      if (createdReview) {
        setReviews((current) => [
          createdReview!,
          ...current.filter((review) => review.id !== createdReview!.id),
        ]);
        setSelectedId(createdReview.id);
        showError(
          errorMessage(
            submitError,
            "审阅已经创建，但分享链接创建失败。可以在下方审阅详情中继续创建分享。",
          ),
        );
      } else {
        showError(errorMessage(submitError, "无法创建审阅"));
      }
    } finally {
      setBusy(false);
    }
  }

  function replaceReview(updated: ReviewSession) {
    setReviews((current) =>
      current
        .map((review) => (review.id === updated.id ? updated : review))
        .sort((left, right) => right.updatedAt.localeCompare(left.updatedAt)),
    );
  }

  async function changeStatus(review: ReviewSession, action: "open" | "close") {
    setBusy(true);
    setError(null);
    try {
      replaceReview(
        action === "open"
          ? await openReviewSession(review)
          : await closeReviewSession(review),
      );
    } catch (actionError) {
      showError(errorMessage(actionError, "无法更新审阅状态"));
    } finally {
      setBusy(false);
    }
  }

  async function saveReview(
    review: ReviewSession,
    input: Omit<UpdateReviewSessionInput, "revision">,
  ) {
    setBusy(true);
    setError(null);
    try {
      const updated = await updateReviewSession(review, input);
      replaceReview(updated);
      setSelectedId(updated.id);
      setEditingReview(null);
    } catch (saveError) {
      showError(errorMessage(saveError, "无法保存审阅设置"));
    } finally {
      setBusy(false);
    }
  }

  return (
    <div className="project-reviews-workspace">
      <header className="studio-section-heading project-review-heading">
        <div>
          <p className="studio-kicker">PROJECT REVIEWS</p>
          <h2>{t("projectReview.title")}</h2>
        </div>
        <div className="detail-actions">
          {canCreateReviews ? (
            <button
              className="primary-button"
              type="button"
              onClick={() => {
                setMediaPickerOpen(true);
                setCandidatePage(1);
              }}
            >
              <Plus size={16} aria-hidden="true" />
              {t("projectReview.addMedia")}
            </button>
          ) : null}
          {canCreateReviews ? (
            <button
              className="secondary-button"
              type="button"
              onClick={onOpenMedia}
            >
              {t("projectReview.mediaLibrary")}
            </button>
          ) : null}
        </div>
      </header>

      {toast ? (
        <div className={`review-toast is-${toast.tone}`} role="status">
          {toast.message}
        </div>
      ) : null}

      {launchResult ? (
        <div className="review-launch-modal-backdrop">
          <section
            className="review-launch-modal"
            role="dialog"
            aria-modal="true"
            aria-labelledby="review-launch-title"
          >
            <button
              className="review-launch-modal-close"
              type="button"
              title="关闭"
              aria-label="关闭"
              onClick={() => setLaunchResult(null)}
            >
              <X size={18} aria-hidden="true" />
            </button>
            <div className="review-launch-modal-mark" aria-hidden="true">
              <Check size={24} />
            </div>
            <div className="review-launch-modal-heading">
              <p className="studio-kicker">REVIEW READY</p>
              <h3 id="review-launch-title">
                {launchResult.url ? "审阅分享已创建" : "审阅草稿已创建"}
              </h3>
              <p>
                <strong>{launchResult.reviewName}</strong>
                {launchResult.url
                  ? " 已开始收集反馈，可以把下面的链接发给参与者。"
                  : " 已保存为审阅草稿，可以稍后在审阅详情里继续生成分享。"}
              </p>
            </div>
            {launchResult.url ? (
              <div className="review-launch-secrets">
                <label>
                  <span>分享链接</span>
                  <span>
                    <input value={launchResult.url} readOnly />
                    <button
                      type="button"
                      title="复制分享链接"
                      aria-label="复制分享链接"
                      onClick={() =>
                        void copyShareText(
                          launchResult.url!,
                          setLaunchNotice,
                          "分享链接已复制。",
                        )
                      }
                    >
                      <Copy size={16} aria-hidden="true" />
                    </button>
                  </span>
                </label>
                {launchResult.password ? (
                  <label>
                    <span>访问码</span>
                    <span>
                      <input value={launchResult.password} readOnly />
                      <button
                        type="button"
                        title="复制访问码"
                        aria-label="复制访问码"
                        onClick={() =>
                          void copyShareText(
                            launchResult.password!,
                            setLaunchNotice,
                            "访问码已复制。",
                          )
                        }
                      >
                        <Copy size={16} aria-hidden="true" />
                      </button>
                    </span>
                  </label>
                ) : null}
              </div>
            ) : null}
            {launchNotice ? (
              <p className={`share-copy-notice is-${launchNotice.tone}`}>
                {launchNotice.message}
              </p>
            ) : null}
            <div className="review-launch-modal-actions">
              {launchResult.url ? (
                <button
                  className="primary-button"
                  type="button"
                  onClick={() =>
                    void copyShareText(
                      launchResult.url!,
                      setLaunchNotice,
                      "分享链接已复制。",
                    )
                  }
                >
                  <Copy size={16} aria-hidden="true" />
                  复制分享链接
                </button>
              ) : null}
              <button
                className="secondary-button"
                type="button"
                onClick={() => setLaunchResult(null)}
              >
                关闭
              </button>
            </div>
          </section>
        </div>
      ) : null}

      {canCreateReviews ? (
        <section className="review-composer" ref={composerRef}>
          <header className="review-composer-heading">
            <div>
              <p className="eyebrow">NEW REVIEW</p>
              <h3>{t("projectReview.newReview")}</h3>
            </div>
            <span>{t("projectReview.versions", { count: queue.length })}</span>
          </header>

          <div className="review-composer-grid">
            <div className="review-composer-queue">
              <div className="review-pane-heading">
                <div>
                  <strong>{t("projectReview.queue")}</strong>
                </div>
                <button
                  className="secondary-button"
                  type="button"
                  onClick={() => {
                    setMediaPickerOpen(true);
                    setCandidatePage(1);
                  }}
                >
                  <Plus size={15} aria-hidden="true" />
                  {t("projectReview.addMedia")}
                </button>
              </div>
              {queue.length === 0 ? (
                <button
                  className="review-queue-empty"
                  type="button"
                  onClick={() => setMediaPickerOpen(true)}
                >
                  <Plus size={20} aria-hidden="true" />
                  <strong>{t("projectReview.selectMedia")}</strong>
                </button>
              ) : (
                <div className="review-queue-list">
                  {queue.map((item) => (
                    <article key={item.assetVersionId}>
                      <span>V{item.versionNumber}</span>
                      <strong>{item.assetName}</strong>
                      <button
                        type="button"
                        title={`从清单移除 ${item.assetName}`}
                        aria-label={`从清单移除 ${item.assetName}`}
                        onClick={() =>
                          setQueue((current) =>
                            current.filter(
                              (candidate) =>
                                candidate.assetVersionId !==
                                item.assetVersionId,
                            ),
                          )
                        }
                      >
                        <X size={15} aria-hidden="true" />
                      </button>
                    </article>
                  ))}
                </div>
              )}
            </div>

            <form className="review-composer-form" onSubmit={submitReview}>
              <label className="field">
                <span>{t("projectReview.name")}</span>
                <input
                  ref={nameInputRef}
                  value={name}
                  maxLength={120}
                  placeholder={t("projectReview.namePlaceholder")}
                  onChange={(event) => {
                    setName(event.target.value);
                    setNameTouched(true);
                  }}
                />
              </label>

              <fieldset className="review-choice-group">
                <legend>{t("projectReview.due")}</legend>
                <div className="review-segmented-control">
                  <button
                    className={expiryMode === "forever" ? "is-active" : ""}
                    type="button"
                    aria-pressed={expiryMode === "forever"}
                    onClick={() => setExpiryMode("forever")}
                  >
                    {t("projectReview.forever")}
                  </button>
                  <button
                    className={expiryMode === "custom" ? "is-active" : ""}
                    type="button"
                    aria-pressed={expiryMode === "custom"}
                    onClick={() => setExpiryMode("custom")}
                  >
                    {t("projectReview.customDate")}
                  </button>
                </div>
                {expiryMode === "custom" ? (
                  <label className="review-date-field">
                    <CalendarDays size={16} aria-hidden="true" />
                    <input
                      type="date"
                      aria-label={t("projectReview.dateAria")}
                      min={localDateValue(new Date())}
                      value={expiryDate}
                      onChange={(event) => setExpiryDate(event.target.value)}
                      required
                    />
                  </label>
                ) : null}
              </fieldset>

              <fieldset className="review-member-picker">
                <legend>{t("projectReview.members")}</legend>
                {activeMembers.length === 0 ? (
                  <span>{t("projectReview.noMembers")}</span>
                ) : (
                  <div>
                    {activeMembers.map((member) => {
                      const selected = selectedMemberIds.includes(
                        member.userId,
                      );
                      return (
                        <label
                          className={selected ? "is-selected" : ""}
                          key={member.userId}
                        >
                          <input
                            type="checkbox"
                            checked={selected}
                            onChange={(event) =>
                              setSelectedMemberIds((current) =>
                                event.target.checked
                                  ? [...current, member.userId]
                                  : current.filter(
                                      (userId) => userId !== member.userId,
                                    ),
                              )
                            }
                          />
                          <span aria-hidden="true">
                            {member.displayName.slice(0, 1).toUpperCase()}
                          </span>
                          <strong>{member.displayName}</strong>
                          <em>{projectMemberRoleLabel(member.roleKey)}</em>
                        </label>
                      );
                    })}
                  </div>
                )}
              </fieldset>

              <fieldset className="review-choice-group">
                <legend>{t("projectReview.decisionRule")}</legend>
                <div className="review-decision-options">
                  {(
                    [
                      ["any_reviewer", t("projectReview.anyMember")],
                      ["all_reviewers", t("projectReview.allMembers")],
                      ["responsible_only", t("projectReview.responsibleOnly")],
                    ] as Array<[ReviewDecisionRule, string]>
                  ).map(([value, label]) => (
                    <label
                      className={decisionRule === value ? "is-selected" : ""}
                      key={value}
                    >
                      <input
                        type="radio"
                        name="decisionRule"
                        value={value}
                        checked={decisionRule === value}
                        onChange={() => setDecisionRule(value)}
                      />
                      <span>{label}</span>
                    </label>
                  ))}
                </div>
                {decisionRule === "responsible_only" ? (
                  <label className="field">
                    <span>{t("projectReview.responsible")}</span>
                    <select
                      value={responsibleUserId}
                      onChange={(event) =>
                        setResponsibleUserId(event.target.value)
                      }
                      required
                    >
                      <option value="">
                        {t("projectReview.selectResponsible")}
                      </option>
                      {activeMembers
                        .filter((member) =>
                          selectedMemberIds.includes(member.userId),
                        )
                        .map((member) => (
                          <option key={member.userId} value={member.userId}>
                            {member.displayName}
                          </option>
                        ))}
                    </select>
                  </label>
                ) : null}
              </fieldset>

              {canCreateShares ? (
                <fieldset className="review-access-policy">
                  <legend>{t("projectReview.access")}</legend>
                  <ReviewToggle
                    checked={passwordEnabled}
                    icon={<KeyRound size={16} aria-hidden="true" />}
                    label={t("projectReview.password")}
                    onChange={(checked) => {
                      setPasswordEnabled(checked);
                      if (checked && accessPassword.length !== 4) {
                        setAccessPassword(generateReviewAccessCode());
                      }
                    }}
                  />
                  {passwordEnabled ? (
                    <div className="review-access-code">
                      <span>{accessPassword}</span>
                      <button
                        type="button"
                        title={t("projectReview.regeneratePassword")}
                        aria-label={t("projectReview.regeneratePassword")}
                        onClick={() =>
                          setAccessPassword(generateReviewAccessCode())
                        }
                      >
                        <RefreshCw size={15} aria-hidden="true" />
                      </button>
                    </div>
                  ) : null}
                  <ReviewToggle
                    checked={allowDownload}
                    label={t("projectReview.allowDownload")}
                    onChange={setAllowDownload}
                  />
                  <ReviewToggle
                    checked={allowComment}
                    label={t("projectReview.allowComment")}
                    onChange={setAllowComment}
                  />
                </fieldset>
              ) : (
                <p className="review-share-permission-note">
                  {t("projectReview.noSharePermission")}
                </p>
              )}

              <button
                className="primary-button review-create-button"
                type="submit"
                disabled={busy || queue.length === 0}
              >
                {busy
                  ? t("projectReview.creating")
                  : canCreateShares
                    ? t("projectReview.createShare")
                    : t("projectReview.createDraft")}
              </button>
            </form>
          </div>
        </section>
      ) : (
        <section className="project-review-permission-note">
          <strong>{t("projectReview.readOnlyTitle")}</strong>
          <span>{t("projectReview.readOnlyDetail")}</span>
        </section>
      )}

      {mediaPickerOpen ? (
        <ReviewMediaPicker
          items={candidateItems}
          total={candidateTotal}
          page={candidatePage}
          pageCount={candidatePageCount}
          search={candidateSearchDraft}
          loading={mediaLoading}
          queuedVersionIds={queue.map((item) => item.assetVersionId)}
          onSearchChange={setCandidateSearchDraft}
          onPageChange={setCandidatePage}
          onAdd={addCandidate}
          onClose={() => setMediaPickerOpen(false)}
        />
      ) : null}

      <section className="project-review-board">
        <aside className="project-review-list-panel">
          <div className="project-review-toolbar">
            <div>
              <p className="eyebrow">{t("projectReview.boardEyebrow")}</p>
              <h3>{t("projectReview.board")}</h3>
            </div>
            <span>
              {visibleReviews.length} / {reviews.length}
            </span>
          </div>
          <div
            className="review-board-stats"
            aria-label={t("projectReview.reviewBoard")}
          >
            <span>
              {reviewStats.open} {t("projectOverview.statusOpen")}
            </span>
            <span>
              {reviewStats.changesRequested}{" "}
              {t("projectOverview.statusChangesRequested")}
            </span>
            <span>
              {reviewStats.draft} {t("projectOverview.statusDraft")}
            </span>
            <span>
              {reviewStats.approved} {t("projectOverview.statusApproved")}
            </span>
          </div>
          <div className="project-review-filters">
            <label>
              <span>{t("projectReview.search")}</span>
              <input
                value={search}
                placeholder={t("projectReview.searchPlaceholder")}
                onChange={(event) => setSearch(event.target.value)}
              />
            </label>
            <label>
              <span>{t("projectReview.status")}</span>
              <select
                value={statusFilter}
                onChange={(event) =>
                  setStatusFilter(
                    event.target.value as ProjectReviewStatusFilter,
                  )
                }
              >
                <option value="all">{t("projectReview.statusAll")}</option>
                <option value="open">{t("projectOverview.statusOpen")}</option>
                <option value="changes_requested">
                  {t("projectOverview.statusChangesRequested")}
                </option>
                <option value="draft">
                  {t("projectOverview.statusDraft")}
                </option>
                <option value="approved">
                  {t("projectOverview.statusApproved")}
                </option>
                <option value="closed">
                  {t("projectOverview.statusClosed")}
                </option>
              </select>
            </label>
            <label>
              <span>{t("projectReview.sort")}</span>
              <select
                value={sort}
                onChange={(event) =>
                  setSort(event.target.value as ProjectReviewSort)
                }
              >
                <option value="workflow">
                  {t("projectReview.sortWorkflow")}
                </option>
                <option value="updated_desc">
                  {t("projectReview.sortUpdated")}
                </option>
                <option value="created_desc">
                  {t("projectReview.sortCreated")}
                </option>
                <option value="due_asc">{t("projectReview.sortDue")}</option>
              </select>
            </label>
          </div>
          {loading ? <ReviewSkeleton /> : null}
          {!loading && visibleReviews.length === 0 ? (
            <div className="rail-empty">
              <strong>{t("projectReview.emptyReviewsTitle")}</strong>
              <span>{t("projectReview.emptyReviewsDetail")}</span>
            </div>
          ) : null}
          <div className="project-review-list">
            {visibleReviews.map((review) => (
              <button
                className={`project-review-row${
                  selected?.id === review.id ? " is-selected" : ""
                }`}
                type="button"
                key={review.id}
                onClick={() => {
                  setSelectedId(review.id);
                  setEditingReview(null);
                }}
              >
                <span className="project-card-topline">
                  <i className={`review-status-dot is-${review.status}`} />
                  {reviewStatusLabel(review.status)}
                </span>
                <strong>{review.name}</strong>
                <span>
                  {review.items[0]?.assetName ?? "审阅内容"} ·{" "}
                  {review.items.length} 个版本
                </span>
                <em>{reviewDueLabel(review)}</em>
              </button>
            ))}
          </div>
        </aside>

        <section className="project-review-detail-panel">
          {activeEditingReview ? (
            <ProjectReviewEditorForm
              key={activeEditingReview.id}
              review={activeEditingReview}
              members={activeMembers}
              busy={busy}
              onCancel={() => setEditingReview(null)}
              onSubmit={(input) => void saveReview(activeEditingReview, input)}
            />
          ) : selected ? (
            <ReviewDetail
              review={selected}
              initialThreadId={
                selected.id === initialReviewId ? initialThreadId : null
              }
              busy={busy}
              canManage={canCreateReviews}
              {...(canCreateReviews
                ? { onEdit: () => setEditingReview(selected) }
                : {})}
              onOpen={() => void changeStatus(selected, "open")}
              onClose={() => void changeStatus(selected, "close")}
              sharePanel={
                <ProjectReviewSharePanel
                  review={selected}
                  members={activeMembers}
                  canCreateShares={canCreateShares}
                />
              }
              onReviewUpdated={replaceReview}
            />
          ) : (
            <div className="catalog-empty-state review-empty-state">
              <span className="review-empty-mark" aria-hidden="true">
                审
              </span>
              <h2>选择或创建一个审阅</h2>
            </div>
          )}
        </section>
      </section>
    </div>
  );
}

function ReviewToggle({
  checked,
  label,
  icon = null,
  onChange,
}: {
  checked: boolean;
  label: string;
  icon?: ReactNode;
  onChange: (checked: boolean) => void;
}) {
  return (
    <label className="review-toggle">
      <span>
        {icon}
        {label}
      </span>
      <input
        type="checkbox"
        checked={checked}
        onChange={(event) => onChange(event.target.checked)}
      />
      <i aria-hidden="true" />
    </label>
  );
}

function ReviewMediaPicker({
  items,
  total,
  page,
  pageCount,
  search,
  loading,
  queuedVersionIds,
  onSearchChange,
  onPageChange,
  onAdd,
  onClose,
}: {
  items: MediaLibraryItem[];
  total: number;
  page: number;
  pageCount: number;
  search: string;
  loading: boolean;
  queuedVersionIds: string[];
  onSearchChange: (value: string) => void;
  onPageChange: (page: number) => void;
  onAdd: (item: MediaLibraryItem) => void;
  onClose: () => void;
}) {
  return (
    <div
      className="review-media-picker-overlay"
      role="presentation"
      onClick={onClose}
    >
      <section
        className="review-media-picker"
        role="dialog"
        aria-modal="true"
        aria-label="选择审阅媒体"
        onClick={(event) => event.stopPropagation()}
      >
        <header>
          <div>
            <p className="eyebrow">PROJECT MEDIA</p>
            <h2>选择审阅媒体</h2>
          </div>
          <button
            type="button"
            title="关闭"
            aria-label="关闭媒体选择"
            onClick={onClose}
          >
            <X size={18} aria-hidden="true" />
          </button>
        </header>
        <label className="review-media-search">
          <Search size={17} aria-hidden="true" />
          <input
            autoFocus
            value={search}
            placeholder="搜索媒体名称"
            onChange={(event) => onSearchChange(event.target.value)}
          />
        </label>
        <div className="review-media-picker-summary">
          <span>{total} 个媒体</span>
          <span>
            第 {page} / {pageCount} 页
          </span>
        </div>
        <div className="review-media-picker-list">
          {loading ? <ReviewSkeleton /> : null}
          {!loading && items.length === 0 ? (
            <div className="rail-empty">
              <strong>没有匹配的媒体</strong>
              <span>换一个名称搜索，或先去媒体库上传文件。</span>
            </div>
          ) : null}
          {items.map((item) =>
            item.asset ? (
              <article key={item.asset.versionId}>
                <span>{assetTypeMark(item.asset.type)}</span>
                <div>
                  <strong>{item.asset.name}</strong>
                  <small>当前版本 V{item.asset.versionNumber}</small>
                </div>
                <button
                  className="secondary-button"
                  type="button"
                  disabled={queuedVersionIds.includes(item.asset.versionId)}
                  onClick={() => onAdd(item)}
                >
                  {queuedVersionIds.includes(item.asset.versionId)
                    ? "已加入"
                    : "加入清单"}
                </button>
              </article>
            ) : null,
          )}
        </div>
        <footer>
          <button
            className="secondary-button"
            type="button"
            disabled={page <= 1 || loading}
            onClick={() => onPageChange(page - 1)}
          >
            上一页
          </button>
          <button
            className="secondary-button"
            type="button"
            disabled={page >= pageCount || loading}
            onClick={() => onPageChange(page + 1)}
          >
            下一页
          </button>
        </footer>
      </section>
    </div>
  );
}

function ProjectReviewSharePanel({
  review,
  members,
  canCreateShares,
}: {
  review: ReviewSession;
  members: ProjectMember[];
  canCreateShares: boolean;
}) {
  const [shares, setShares] = useState<Share[]>([]);
  const [notifyUserIds, setNotifyUserIds] = useState<string[]>(
    review.participants
      .map((participant) => participant.userId)
      .filter((userId): userId is string => Boolean(userId)),
  );
  const [allowComment, setAllowComment] = useState(true);
  const [allowDownload, setAllowDownload] = useState(review.allowDownload);
  const [requireNickname, setRequireNickname] = useState(false);
  const [loading, setLoading] = useState(true);
  const [busy, setBusy] = useState(false);
  const [credentialBusyId, setCredentialBusyId] = useState<string | null>(null);
  const [credentialsByShareId, setCredentialsByShareId] = useState<
    Record<string, ShareCredentials>
  >({});
  const [freshURL, setFreshURL] = useState<string | null>(null);
  const [notice, setNotice] = useState<CopyNotice>(null);
  const [error, setError] = useState<string | null>(null);

  useEffect(() => {
    const controller = new AbortController();
    setLoading(true);
    setError(null);
    setFreshURL(null);
    setCredentialsByShareId({});
    listShares(controller.signal)
      .then((items) =>
        setShares(items.filter((share) => share.reviewSessionId === review.id)),
      )
      .catch((loadError: unknown) => {
        if (!isAbort(loadError)) {
          setError(errorMessage(loadError, "无法读取审阅分享"));
        }
      })
      .finally(() => {
        if (!controller.signal.aborted) {
          setLoading(false);
        }
      });
    return () => controller.abort();
  }, [review.id]);

  useEffect(() => {
    setAllowDownload(review.allowDownload);
    setNotifyUserIds(
      review.participants
        .map((participant) => participant.userId)
        .filter((userId): userId is string => Boolean(userId)),
    );
  }, [review.allowDownload, review.id, review.participants]);

  async function createReviewShare() {
    if (!canCreateShares) {
      return;
    }
    setBusy(true);
    setError(null);
    setNotice(null);
    try {
      const result = await createShare({
        reviewSessionId: review.id,
        name: `${review.name} 分享`,
        allowComment,
        allowDownload,
        requireNickname,
        expiresAt: null,
        password: null,
        notifyUserIds,
      });
      if (result.share) {
        setShares((current) => [result.share!, ...current]);
      }
      const url = absolutePublicURL(result.url);
      setFreshURL(url);
      if (result.share) {
        setCredentialsByShareId((current) => ({
          ...current,
          [result.share!.id]: {
            shareId: result.share!.id,
            password: result.password ?? null,
            links: [{ linkId: result.link.id, url: result.url }],
          },
        }));
      }
      await copyShareText(
        url,
        setNotice,
        shareNotifyMessage(notifyUserIds.length),
      );
    } catch (submitError) {
      setError(errorMessage(submitError, "无法创建分享"));
    } finally {
      setBusy(false);
    }
  }

  async function issueLink(share: Share) {
    if (!canCreateShares) {
      return;
    }
    setBusy(true);
    setError(null);
    setNotice(null);
    try {
      const result = await createShareLink(share);
      const url = absolutePublicURL(result.url);
      setFreshURL(url);
      replaceShare({
        ...share,
        links: [result.link, ...share.links],
      });
      setCredentialsByShareId((current) => {
        const existing = current[share.id];
        if (!existing) {
          return current;
        }
        return {
          ...current,
          [share.id]: {
            ...existing,
            links: [
              { linkId: result.link.id, url: result.url },
              ...existing.links,
            ],
          },
        };
      });
      await copyShareText(url, setNotice, "新链接已创建并复制。");
    } catch (actionError) {
      setError(errorMessage(actionError, "无法创建新链接"));
    } finally {
      setBusy(false);
    }
  }

  async function revokeCurrentShare(share: Share) {
    if (!canCreateShares) {
      return;
    }
    setBusy(true);
    setError(null);
    try {
      replaceShare(await revokeShare(share));
      setCredentialsByShareId((current) => {
        const next = { ...current };
        delete next[share.id];
        return next;
      });
      setFreshURL(null);
      setNotice({ tone: "success", message: "分享已撤销。" });
    } catch (actionError) {
      setError(errorMessage(actionError, "无法撤销分享"));
    } finally {
      setBusy(false);
    }
  }

  async function revokeLink(share: Share, link: ShareLink) {
    if (!canCreateShares) {
      return;
    }
    setBusy(true);
    setError(null);
    try {
      const updated = await revokeShareLink(link);
      replaceShare({
        ...share,
        links: share.links.map((item) =>
          item.id === updated.id ? updated : item,
        ),
      });
      setCredentialsByShareId((current) => {
        const existing = current[share.id];
        if (!existing) {
          return current;
        }
        return {
          ...current,
          [share.id]: {
            ...existing,
            links: existing.links.filter((item) => item.linkId !== link.id),
          },
        };
      });
      setNotice({ tone: "success", message: "链接已撤销。" });
    } catch (actionError) {
      setError(errorMessage(actionError, "无法撤销链接"));
    } finally {
      setBusy(false);
    }
  }

  function replaceShare(updated: Share) {
    setShares((current) =>
      current
        .map((share) => (share.id === updated.id ? updated : share))
        .sort((left, right) => right.updatedAt.localeCompare(left.updatedAt)),
    );
  }

  async function revealShareCredentials(share: Share) {
    if (!canCreateShares) {
      return;
    }
    setCredentialBusyId(share.id);
    setError(null);
    setNotice(null);
    try {
      const credentials = await getShareCredentials(share);
      setCredentialsByShareId((current) => ({
        ...current,
        [share.id]: credentials,
      }));
      setNotice({ tone: "success", message: "链接和密码已展开。" });
    } catch (loadError) {
      setError(errorMessage(loadError, "无法读取分享链接和密码"));
    } finally {
      setCredentialBusyId(null);
    }
  }

  return (
    <section className="review-section project-review-share-panel">
      <div className="section-heading">
        <div>
          <p className="eyebrow">SHARE & NOTIFY</p>
          <h2>分享与通知</h2>
        </div>
        <span className="soft-badge">{shares.length} 个分享</span>
      </div>
      {canCreateShares ? (
        <>
          <div className="project-share-grid">
            <div className="project-share-policy">
              <strong>访问策略</strong>
              <label>
                <input
                  type="checkbox"
                  checked={allowComment}
                  onChange={(event) => setAllowComment(event.target.checked)}
                />
                允许访客评论
              </label>
              <label>
                <input
                  type="checkbox"
                  checked={allowDownload}
                  onChange={(event) => setAllowDownload(event.target.checked)}
                />
                允许下载源文件
              </label>
              <label>
                <input
                  type="checkbox"
                  checked={requireNickname}
                  onChange={(event) => setRequireNickname(event.target.checked)}
                />
                评论前填写昵称
              </label>
            </div>
            <div className="project-share-notify">
              <strong>通知对象</strong>
              {members.length === 0 ? (
                <span>当前项目暂无可选成员。</span>
              ) : null}
              {members.map((member) => (
                <label key={member.userId}>
                  <input
                    type="checkbox"
                    checked={notifyUserIds.includes(member.userId)}
                    onChange={(event) =>
                      setNotifyUserIds((current) =>
                        event.target.checked
                          ? [...current, member.userId]
                          : current.filter((id) => id !== member.userId),
                      )
                    }
                  />
                  {member.displayName}
                </label>
              ))}
            </div>
          </div>
          <button
            className="primary-button"
            type="button"
            disabled={busy || review.status !== "open"}
            onClick={() => void createReviewShare()}
          >
            {review.status !== "open"
              ? "开启审阅后可分享"
              : busy
                ? "处理中"
                : "创建并复制分享"}
          </button>
        </>
      ) : (
        <p className="review-muted">
          你可以查看现有分享记录；创建、撤销或重新生成链接需要项目分享权限。
        </p>
      )}
      {freshURL ? (
        <div className="share-secret compact" role="status">
          <input value={freshURL} readOnly aria-label="新分享链接" />
          <button
            className="secondary-button"
            type="button"
            onClick={() =>
              void copyShareText(freshURL, setNotice, "链接已复制。")
            }
          >
            复制
          </button>
        </div>
      ) : null}
      {notice ? (
        <p className={`share-copy-notice is-${notice.tone}`} role="status">
          {notice.message}
        </p>
      ) : null}
      {error ? (
        <p className="catalog-error" role="alert">
          {error}
        </p>
      ) : null}
      {loading ? <p className="review-muted">正在读取分享。</p> : null}
      <div className="project-share-list">
        {shares.map((share) => {
          const credentials = credentialsByShareId[share.id] ?? null;
          return (
            <article key={share.id}>
              <div>
                <span className={`share-status-dot is-${share.status}`} />
                <strong>{share.name}</strong>
                <small>
                  {projectShareStatusLabel(share.status)} ·{" "}
                  {projectShareExpirationLabel(share)}
                </small>
              </div>
              <div className="project-share-actions">
                {canCreateShares ? (
                  <button
                    className="secondary-button"
                    type="button"
                    disabled={credentialBusyId === share.id}
                    onClick={() => void revealShareCredentials(share)}
                  >
                    {credentialBusyId === share.id
                      ? "读取中"
                      : credentials
                        ? "刷新链接/密码"
                        : "查看链接/密码"}
                  </button>
                ) : null}
                {canCreateShares && share.status === "active" ? (
                  <>
                    <button
                      className="secondary-button"
                      type="button"
                      disabled={busy}
                      onClick={() => void issueLink(share)}
                    >
                      新建链接
                    </button>
                    <button
                      className="text-danger-button"
                      type="button"
                      disabled={busy}
                      onClick={() => void revokeCurrentShare(share)}
                    >
                      撤销分享
                    </button>
                  </>
                ) : null}
              </div>
              <div className="project-share-links">
                {share.links.map((link) => (
                  <span key={link.id}>
                    {link.status === "active" ? "有效" : "已撤销"} ·{" "}
                    {link.tokenPrefix}••••
                    {canCreateShares &&
                    link.status === "active" &&
                    share.status === "active" ? (
                      <button
                        type="button"
                        disabled={busy}
                        onClick={() => void revokeLink(share, link)}
                      >
                        撤销
                      </button>
                    ) : null}
                  </span>
                ))}
              </div>
              {credentials ? (
                <div className="project-share-credentials">
                  <div>
                    <span>访问密码</span>
                    <strong>
                      {credentials.password ? credentials.password : "未设置"}
                    </strong>
                    {credentials.password ? (
                      <button
                        type="button"
                        onClick={() =>
                          void copyShareText(
                            credentials.password ?? "",
                            setNotice,
                            "访问密码已复制。",
                          )
                        }
                      >
                        复制
                      </button>
                    ) : null}
                  </div>
                  {credentials.links.length > 0 ? (
                    credentials.links.map((link) => {
                      const url = absolutePublicURL(link.url);
                      return (
                        <div key={link.linkId}>
                          <span>分享链接</span>
                          <input value={url} readOnly aria-label="分享链接" />
                          <button
                            type="button"
                            onClick={() =>
                              void copyShareText(url, setNotice, "链接已复制。")
                            }
                          >
                            复制
                          </button>
                        </div>
                      );
                    })
                  ) : (
                    <p>旧链接没有保存完整地址，请新建链接后再查看。</p>
                  )}
                </div>
              ) : null}
            </article>
          );
        })}
      </div>
    </section>
  );
}

function ReviewSkeleton() {
  return (
    <div className="media-skeleton" aria-hidden="true">
      <span />
      <span />
      <span />
    </div>
  );
}

function decisionRuleLabel(rule: ReviewSession["decisionRule"]) {
  switch (rule) {
    case "all_reviewers":
      return "全部审阅者通过";
    case "responsible_only":
      return "仅负责人决定";
    default:
      return "任一审阅者决定";
  }
}

function projectMemberRoleLabel(role: ProjectMember["roleKey"]) {
  switch (role) {
    case "primary_owner":
      return "主负责人";
    case "supervisor":
      return "项目主管";
    case "guest":
      return "临时账户";
    default:
      return "成员";
  }
}

function sortProjectReviews(
  left: ReviewSession,
  right: ReviewSession,
  sort: ProjectReviewSort,
) {
  if (sort === "workflow") {
    const byWorkflow = workflowRank(left) - workflowRank(right);
    if (byWorkflow !== 0) {
      return byWorkflow;
    }
    return right.updatedAt.localeCompare(left.updatedAt);
  }
  if (sort === "created_desc") {
    return right.createdAt.localeCompare(left.createdAt);
  }
  if (sort === "due_asc") {
    if (!left.dueAt && !right.dueAt) {
      return right.updatedAt.localeCompare(left.updatedAt);
    }
    if (!left.dueAt) {
      return 1;
    }
    if (!right.dueAt) {
      return -1;
    }
    return left.dueAt.localeCompare(right.dueAt);
  }
  return right.updatedAt.localeCompare(left.updatedAt);
}

function workflowRank(review: ReviewSession) {
  const ranks: Record<ReviewSession["status"], number> = {
    changes_requested: 0,
    open: 1,
    draft: 2,
    approved: 3,
    closed: 4,
  };
  return ranks[review.status];
}

function assetTypeMark(type: string) {
  switch (type) {
    case "video":
      return "VID";
    case "image":
      return "IMG";
    case "audio":
      return "AUD";
    case "pdf":
      return "PDF";
    case "design":
      return "DES";
    case "document":
      return "DOC";
    default:
      return "FILE";
  }
}

function projectShareStatusLabel(status: Share["status"]) {
  switch (status) {
    case "active":
      return "有效";
    case "expired":
      return "已过期";
    case "revoked":
      return "已撤销";
  }
}

function projectShareExpirationLabel(share: Share) {
  if (share.status === "revoked") {
    return "已撤销";
  }
  if (!share.expiresAt) {
    return "长期有效";
  }
  return `有效至 ${formatDate(share.expiresAt)}`;
}

function shareNotifyMessage(count: number) {
  if (count <= 0) {
    return "分享已创建并复制链接，没有选择通知对象。";
  }
  return `分享已创建并复制链接，并已通知 ${count} 位项目成员。`;
}

async function copyShareText(
  value: string,
  setNotice: (notice: CopyNotice) => void,
  successMessage: string,
) {
  try {
    if (navigator.clipboard?.writeText) {
      await navigator.clipboard.writeText(value);
    } else {
      throw new Error("clipboard unavailable");
    }
    setNotice({ tone: "success", message: successMessage });
  } catch {
    setNotice({
      tone: "error",
      message: "浏览器阻止了自动复制，请手动复制输入框里的链接。",
    });
  }
}

function absolutePublicURL(value: string) {
  if (/^https?:\/\//i.test(value)) {
    return value;
  }
  return `${window.location.origin}${value}`;
}

function reviewStatusLabel(status: ReviewSession["status"]) {
  switch (status) {
    case "draft":
      return "草稿";
    case "open":
      return "审阅中";
    case "changes_requested":
      return "需要修改";
    case "approved":
      return "已通过";
    case "closed":
      return "已结束";
  }
}

function reviewStatusCopy(status: ReviewSession["status"]) {
  switch (status) {
    case "draft":
      return {
        title: "草稿",
        description: "开启审阅后可创建分享链接。",
      };
    case "open":
      return {
        title: "审阅已开启",
        description: "",
      };
    case "closed":
      return {
        title: "审阅已结束",
        description: "已停止接收新评论。",
      };
    case "changes_requested":
      return {
        title: "需要修改",
        description: "上传新版本后可发起下一轮审阅。",
      };
    case "approved":
      return {
        title: "已通过",
        description: "",
      };
  }
}

function reviewItemStatusLabel(
  status: ReviewSession["items"][number]["status"],
) {
  switch (status) {
    case "pending":
      return "待审阅";
    case "in_review":
      return "审阅中";
    case "approved":
      return "已通过";
    case "changes_requested":
      return "需要修改";
  }
}

export function defaultReviewName(queue: ProjectReviewQueueItem[]) {
  const first = queue[0];
  if (!first) {
    return "";
  }
  return queue.length === 1
    ? first.assetName
    : `${first.assetName} 等 ${queue.length} 个文件`;
}

export function generateReviewAccessCode() {
  const values = new Uint16Array(1);
  crypto.getRandomValues(values);
  return String(1000 + ((values[0] ?? 0) % 9000));
}

function localDateValue(value: Date) {
  const year = value.getFullYear();
  const month = String(value.getMonth() + 1).padStart(2, "0");
  const day = String(value.getDate()).padStart(2, "0");
  return `${year}-${month}-${day}`;
}

function endOfLocalDayISO(value: string) {
  return new Date(`${value}T23:59:59.999`).toISOString();
}

function reviewThreadLocation(thread: CommentThread) {
  if (thread.annotation.geometry) {
    return formatImageGeometry(thread.annotation.geometry);
  }
  if (
    thread.annotation.kind === "time_point" ||
    thread.annotation.kind === "time_range"
  ) {
    return formatThreadTime(thread.annotation);
  }
  return "整份内容";
}

function commentThreadStatusLabel(status: string) {
  return status === "resolved" ? "已解决" : "待处理";
}

function reviewDecisionLabel(decision: ReviewDecisionValue) {
  switch (decision) {
    case "approved":
      return "通过";
    case "changes_requested":
      return "需修改";
    case "rejected":
      return "拒绝";
  }
}

function reviewDecisionTarget(review: ReviewSession, decision: ReviewDecision) {
  if (!decision.reviewItemId) {
    return "整场审阅";
  }
  const item = review.items.find(
    (candidate) => candidate.id === decision.reviewItemId,
  );
  return item
    ? `V${item.versionNumber} · ${item.assetName}`
    : "已移除的审阅版本";
}

function reviewDueLabel(review: ReviewSession) {
  if (review.status === "closed") {
    return "审阅已结束";
  }
  return review.dueAt ? `截止 ${formatDate(review.dueAt)}` : "未设置截止时间";
}

function formatDate(value: string) {
  return new Intl.DateTimeFormat("zh-CN", {
    month: "short",
    day: "numeric",
  }).format(new Date(value));
}

function formatDateTime(value: string) {
  return new Intl.DateTimeFormat("zh-CN", {
    year: "numeric",
    month: "short",
    day: "numeric",
    hour: "2-digit",
    minute: "2-digit",
  }).format(new Date(value));
}

function toDateTimeLocal(value: string | null) {
  if (!value) {
    return "";
  }
  const date = new Date(value);
  const offset = date.getTimezoneOffset() * 60_000;
  return new Date(date.getTime() - offset).toISOString().slice(0, 16);
}

function errorMessage(error: unknown, fallback: string) {
  return error instanceof Error ? error.message : fallback;
}

function isAbort(error: unknown) {
  return error instanceof DOMException && error.name === "AbortError";
}
