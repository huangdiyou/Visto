import { useEffect, useMemo, useState } from "react";
import type { Notification, ReviewSession } from "@review-studio/contracts";
import {
  ArrowUpRight,
  Check,
  CheckCheck,
  Inbox,
  RefreshCw,
  X,
} from "lucide-react";
import {
  listNotifications,
  markAllNotificationsRead,
  markNotificationRead,
} from "./api/activity";
import { getReviewSession } from "./api/reviews";
import { listShares } from "./api/shares";

type NotificationFilter = "all" | "unread";

export function NotificationCenter({
  open,
  onClose,
  onUnreadCountChange,
  onOpenReview,
}: {
  open: boolean;
  onClose: () => void;
  onUnreadCountChange: (count: number) => void;
  onOpenReview: (review: ReviewSession) => void;
}) {
  const [items, setItems] = useState<Notification[]>([]);
  const [unreadCount, setUnreadCount] = useState(0);
  const [filter, setFilter] = useState<NotificationFilter>("all");
  const [loading, setLoading] = useState(false);
  const [refreshing, setRefreshing] = useState(false);
  const [busyId, setBusyId] = useState<string | null>(null);
  const [error, setError] = useState<string | null>(null);

  useEffect(() => {
    if (!open) return;
    const controller = new AbortController();
    setLoading(true);
    setError(null);
    void loadNotifications(controller.signal)
      .then(({ items: nextItems, unreadCount: nextUnreadCount }) => {
        setItems(nextItems);
        updateUnreadCount(nextUnreadCount);
      })
      .catch((loadError: unknown) => {
        if (!isAbort(loadError)) {
          setError(errorMessage(loadError, "无法读取通知"));
        }
      })
      .finally(() => setLoading(false));
    return () => controller.abort();
  }, [open]);

  useEffect(() => {
    if (!open) return;
    const handleKeyDown = (event: KeyboardEvent) => {
      if (event.key === "Escape") {
        onClose();
      }
    };
    window.addEventListener("keydown", handleKeyDown);
    return () => window.removeEventListener("keydown", handleKeyDown);
  }, [onClose, open]);

  const visibleItems = useMemo(
    () => (filter === "unread" ? items.filter((item) => !item.readAt) : items),
    [filter, items],
  );

  function updateUnreadCount(count: number) {
    setUnreadCount(count);
    onUnreadCountChange(count);
  }

  async function refresh() {
    setRefreshing(true);
    setError(null);
    try {
      const result = await loadNotifications();
      setItems(result.items);
      updateUnreadCount(result.unreadCount);
    } catch (refreshError) {
      setError(errorMessage(refreshError, "无法刷新通知"));
    } finally {
      setRefreshing(false);
    }
  }

  async function read(item: Notification) {
    if (item.readAt) return item;
    setBusyId(item.id);
    setError(null);
    try {
      const updated = await markNotificationRead(item);
      setItems((current) =>
        current.map((candidate) =>
          candidate.id === updated.id ? updated : candidate,
        ),
      );
      updateUnreadCount(Math.max(0, unreadCount - 1));
      return updated;
    } catch (actionError) {
      setError(errorMessage(actionError, "无法标记通知"));
      return null;
    } finally {
      setBusyId(null);
    }
  }

  async function readAll() {
    if (unreadCount === 0) return;
    setBusyId("all");
    setError(null);
    try {
      await markAllNotificationsRead();
      const readAt = new Date().toISOString();
      setItems((current) =>
        current.map((item) => ({ ...item, readAt: item.readAt ?? readAt })),
      );
      updateUnreadCount(0);
    } catch (actionError) {
      setError(errorMessage(actionError, "无法将通知全部标为已读"));
    } finally {
      setBusyId(null);
    }
  }

  async function openTarget(item: Notification) {
    setBusyId(item.id);
    setError(null);
    try {
      if (!item.readAt) {
        const updated = await markNotificationRead(item);
        setItems((current) =>
          current.map((candidate) =>
            candidate.id === updated.id ? updated : candidate,
          ),
        );
        updateUnreadCount(Math.max(0, unreadCount - 1));
      }
      const review = await resolveNotificationReview(item);
      onOpenReview(review);
      onClose();
    } catch (actionError) {
      setError(errorMessage(actionError, "无法打开通知对应的审阅"));
    } finally {
      setBusyId(null);
    }
  }

  if (!open) return null;

  return (
    <div className="studio-notification-layer" role="presentation">
      <button
        className="studio-notification-scrim"
        type="button"
        aria-label="关闭通知"
        onClick={onClose}
      />
      <aside
        className="studio-notification-drawer"
        role="dialog"
        aria-modal="true"
        aria-labelledby="studio-notification-title"
      >
        <header className="studio-notification-header">
          <div>
            <span className="studio-notification-kicker">INBOX</span>
            <h2 id="studio-notification-title">通知</h2>
          </div>
          <div className="studio-notification-header-actions">
            <button
              className="studio-icon-button"
              type="button"
              aria-label="刷新通知"
              title="刷新通知"
              disabled={refreshing}
              onClick={() => void refresh()}
            >
              <RefreshCw
                size={17}
                className={refreshing ? "is-spinning" : undefined}
              />
            </button>
            <button
              className="studio-icon-button"
              type="button"
              aria-label="关闭通知"
              onClick={onClose}
            >
              <X size={18} />
            </button>
          </div>
        </header>

        <div className="studio-notification-toolbar">
          <div
            className="studio-notification-filter"
            role="tablist"
            aria-label="通知筛选"
          >
            <button
              className={filter === "all" ? "is-active" : ""}
              type="button"
              role="tab"
              aria-selected={filter === "all"}
              onClick={() => setFilter("all")}
            >
              全部
              <span>{items.length}</span>
            </button>
            <button
              className={filter === "unread" ? "is-active" : ""}
              type="button"
              role="tab"
              aria-selected={filter === "unread"}
              onClick={() => setFilter("unread")}
            >
              未读
              <span>{unreadCount}</span>
            </button>
          </div>
          <button
            className="studio-notification-read-all"
            type="button"
            disabled={busyId !== null || unreadCount === 0}
            onClick={() => void readAll()}
          >
            <CheckCheck size={15} />
            全部已读
          </button>
        </div>

        {error ? (
          <div className="studio-notification-error" role="alert">
            {error}
          </div>
        ) : null}

        <div className="studio-notification-list">
          {loading ? (
            <NotificationLoading />
          ) : visibleItems.length === 0 ? (
            <div className="studio-notification-empty">
              {filter === "unread" ? <Check size={23} /> : <Inbox size={23} />}
              <strong>
                {filter === "unread" ? "没有未读通知" : "目前没有通知"}
              </strong>
              {filter === "unread" ? (
                <span>需要处理的新消息已经全部看过。</span>
              ) : null}
            </div>
          ) : (
            visibleItems.map((item) => {
              const actionable = notificationHasReviewTarget(item);
              return (
                <article
                  className={`studio-notification-item${
                    item.readAt ? "" : " is-unread"
                  }`}
                  key={item.id}
                >
                  <span
                    className="studio-notification-dot"
                    aria-hidden="true"
                  />
                  <div className="studio-notification-copy">
                    <div>
                      <strong>{item.title}</strong>
                      <time dateTime={item.createdAt}>
                        {formatNotificationTime(item.createdAt)}
                      </time>
                    </div>
                    <p>{item.body}</p>
                    <div className="studio-notification-actions">
                      {actionable ? (
                        <button
                          type="button"
                          disabled={busyId !== null}
                          onClick={() => void openTarget(item)}
                        >
                          <ArrowUpRight size={14} />
                          打开审阅
                        </button>
                      ) : null}
                      {!item.readAt ? (
                        <button
                          type="button"
                          disabled={busyId !== null}
                          onClick={() => void read(item)}
                        >
                          <Check size={14} />
                          标为已读
                        </button>
                      ) : (
                        <span>已读</span>
                      )}
                    </div>
                  </div>
                </article>
              );
            })
          )}
        </div>
      </aside>
    </div>
  );
}

async function loadNotifications(signal?: AbortSignal) {
  return listNotifications(signal);
}

function notificationHasReviewTarget(item: Notification) {
  return (
    item.resourceType === "review_session" || item.resourceType === "share"
  );
}

async function resolveNotificationReview(item: Notification) {
  if (item.resourceType === "review_session") {
    return getReviewSession(item.resourceId);
  }
  if (item.resourceType === "share") {
    const shares = await listShares();
    const share = shares.find((candidate) => candidate.id === item.resourceId);
    if (!share?.reviewSessionId) {
      throw new Error("这个分享没有关联可打开的审阅");
    }
    return getReviewSession(share.reviewSessionId);
  }
  throw new Error("这个通知没有可打开的审阅");
}

function NotificationLoading() {
  return (
    <div className="studio-notification-loading" aria-label="正在读取通知">
      <span />
      <span />
      <span />
    </div>
  );
}

function formatNotificationTime(value: string) {
  const date = new Date(value);
  const difference = Date.now() - date.getTime();
  const minute = 60 * 1000;
  const hour = 60 * minute;
  const day = 24 * hour;
  if (difference >= 0 && difference < minute) return "刚刚";
  if (difference >= minute && difference < hour) {
    return `${Math.floor(difference / minute)} 分钟前`;
  }
  if (difference >= hour && difference < day) {
    return `${Math.floor(difference / hour)} 小时前`;
  }
  return new Intl.DateTimeFormat("zh-CN", {
    month: "numeric",
    day: "numeric",
    hour: "2-digit",
    minute: "2-digit",
  }).format(date);
}

function errorMessage(error: unknown, fallback: string) {
  return error instanceof Error ? error.message : fallback;
}

function isAbort(error: unknown) {
  return error instanceof DOMException && error.name === "AbortError";
}
