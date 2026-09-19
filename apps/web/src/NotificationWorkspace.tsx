import { useEffect, useState } from "react";
import type {
  AuditLog,
  Membership,
  Notification,
  NotificationChannel,
  NotificationChannelInput,
  NotificationChannelKind,
  NotificationDelivery,
  NotificationPreferences,
} from "@review-studio/contracts";
import {
  listAuditLogs,
  listNotifications,
  markAllNotificationsRead,
  markNotificationRead,
} from "./api/activity";
import { listMembers } from "./api/members";
import {
  createNotificationChannel,
  deleteNotificationChannel,
  getNotificationPreferences,
  listNotificationChannels,
  listNotificationDeliveries,
  retryNotificationDelivery,
  testNotificationChannel,
  updateNotificationPreferences,
} from "./api/notifications";
import { PRODUCT_NAME_FULL } from "./brand";

interface ActivityFilter {
  actorId: string;
  action: string;
  resourceType: string;
}

interface ChannelForm {
  kind: NotificationChannelKind;
  name: string;
  smtpHost: string;
  smtpPort: string;
  smtpSecurity: "starttls" | "tls" | "plain";
  smtpFromAddress: string;
  smtpFromName: string;
  smtpUsername: string;
  smtpPassword: string;
  testRecipient: string;
  webhookUrl: string;
  allowPrivateNetwork: boolean;
}

interface InlineFeedback {
  kind: "success" | "error";
  message: string;
}

const emptyFilter: ActivityFilter = {
  actorId: "",
  action: "",
  resourceType: "",
};

const emptyChannelForm: ChannelForm = {
  kind: "email",
  name: "",
  smtpHost: "",
  smtpPort: "587",
  smtpSecurity: "starttls",
  smtpFromAddress: "",
  smtpFromName: PRODUCT_NAME_FULL,
  smtpUsername: "",
  smtpPassword: "",
  testRecipient: "",
  webhookUrl: "",
  allowPrivateNetwork: false,
};

export type NotificationWorkspaceSurface =
  | "legacy"
  | "owner-notifications"
  | "owner-activity";

export function NotificationWorkspace({
  showAudit,
  surface = "legacy",
}: {
  showAudit: boolean;
  surface?: NotificationWorkspaceSurface;
}) {
  const showInbox = surface === "legacy";
  const showExternal = showAudit && surface !== "owner-activity";
  const showActivity = showAudit && surface !== "owner-notifications";
  const [notifications, setNotifications] = useState<Notification[]>([]);
  const [auditLogs, setAuditLogs] = useState<AuditLog[]>([]);
  const [members, setMembers] = useState<Membership[]>([]);
  const [channels, setChannels] = useState<NotificationChannel[]>([]);
  const [deliveries, setDeliveries] = useState<NotificationDelivery[]>([]);
  const [preferences, setPreferences] =
    useState<NotificationPreferences | null>(null);
  const [filter, setFilter] = useState<ActivityFilter>(emptyFilter);
  const [channelForm, setChannelForm] = useState<ChannelForm>(emptyChannelForm);
  const [unreadCount, setUnreadCount] = useState(0);
  const [loadingNotifications, setLoadingNotifications] = useState(showInbox);
  const [loadingAudit, setLoadingAudit] = useState(showActivity);
  const [loadingExternal, setLoadingExternal] = useState(showExternal);
  const [busy, setBusy] = useState(false);
  const [preferenceSaving, setPreferenceSaving] = useState(false);
  const [channelCreating, setChannelCreating] = useState(false);
  const [preferenceFeedback, setPreferenceFeedback] =
    useState<InlineFeedback | null>(null);
  const [channelFeedback, setChannelFeedback] = useState<InlineFeedback | null>(
    null,
  );
  const [error, setError] = useState<string | null>(null);
  const [notice, setNotice] = useState<string | null>(null);

  useEffect(() => {
    const controller = new AbortController();
    const notificationRequest = showInbox
      ? listNotifications(controller.signal)
      : Promise.resolve({ items: [], unreadCount: 0 });
    const preferenceRequest = showInbox
      ? getNotificationPreferences(controller.signal)
      : Promise.resolve(null);
    const memberRequest = showActivity
      ? listMembers(controller.signal)
      : Promise.resolve([]);
    const channelRequest = showExternal
      ? listNotificationChannels(controller.signal)
      : Promise.resolve([]);
    const deliveryRequest = showExternal
      ? listNotificationDeliveries(controller.signal)
      : Promise.resolve([]);
    Promise.all([
      notificationRequest,
      memberRequest,
      preferenceRequest,
      channelRequest,
      deliveryRequest,
    ])
      .then(
        ([
          notificationResult,
          memberItems,
          preferenceResult,
          channelItems,
          deliveryItems,
        ]) => {
          setNotifications(notificationResult.items);
          setUnreadCount(notificationResult.unreadCount);
          setMembers(memberItems);
          setPreferences(preferenceResult);
          setChannels(channelItems);
          setDeliveries(deliveryItems);
        },
      )
      .catch((loadError: unknown) => {
        if (!isAbort(loadError)) {
          setError(
            loadError instanceof Error ? loadError.message : "无法读取通知数据",
          );
        }
      })
      .finally(() => {
        setLoadingNotifications(false);
        setLoadingExternal(false);
      });
    return () => controller.abort();
  }, [showActivity, showExternal, showInbox]);

  useEffect(() => {
    if (!showActivity) {
      setAuditLogs([]);
      setLoadingAudit(false);
      return;
    }
    const controller = new AbortController();
    setLoadingAudit(true);
    listAuditLogs(filter, controller.signal)
      .then(setAuditLogs)
      .catch((loadError: unknown) => {
        if (!isAbort(loadError)) {
          setError(
            loadError instanceof Error ? loadError.message : "无法读取活动记录",
          );
        }
      })
      .finally(() => setLoadingAudit(false));
    return () => controller.abort();
  }, [filter.actorId, filter.action, filter.resourceType, showActivity]);

  async function read(item: Notification) {
    if (item.readAt) return;
    setBusy(true);
    setError(null);
    try {
      const updated = await markNotificationRead(item);
      setNotifications((current) =>
        current.map((candidate) =>
          candidate.id === updated.id ? updated : candidate,
        ),
      );
      setUnreadCount((current) => Math.max(0, current - 1));
    } catch (actionError) {
      setError(
        actionError instanceof Error ? actionError.message : "无法更新通知状态",
      );
    } finally {
      setBusy(false);
    }
  }

  async function readAll() {
    if (unreadCount === 0) return;
    setBusy(true);
    setError(null);
    try {
      await markAllNotificationsRead();
      const readAt = new Date().toISOString();
      setNotifications((current) =>
        current.map((item) => ({ ...item, readAt: item.readAt ?? readAt })),
      );
      setUnreadCount(0);
    } catch (actionError) {
      setError(
        actionError instanceof Error ? actionError.message : "无法标记全部已读",
      );
    } finally {
      setBusy(false);
    }
  }

  async function savePreferences() {
    if (!preferences) return;
    setBusy(true);
    setPreferenceSaving(true);
    setError(null);
    setNotice(null);
    setPreferenceFeedback(null);
    try {
      const updated = await updateNotificationPreferences({
        emailEnabled: preferences.emailEnabled,
        feishuEnabled: preferences.feishuEnabled,
        wechatWorkEnabled: preferences.wechatWorkEnabled,
      });
      setPreferences(updated);
      setPreferenceFeedback({ kind: "success", message: "接收偏好已保存" });
    } catch (actionError) {
      setPreferenceFeedback({
        kind: "error",
        message:
          actionError instanceof Error
            ? actionError.message
            : "无法保存接收偏好",
      });
    } finally {
      setPreferenceSaving(false);
      setBusy(false);
    }
  }

  async function createChannel() {
    const validationError = validateChannelForm(channelForm);
    if (validationError) {
      setChannelFeedback({ kind: "error", message: validationError });
      return;
    }
    setBusy(true);
    setChannelCreating(true);
    setError(null);
    setNotice(null);
    setChannelFeedback(null);
    try {
      const input = buildChannelInput(channelForm);
      const created = await createNotificationChannel(input);
      setChannels((current) => [...current, created]);
      setChannelForm({
        ...emptyChannelForm,
        kind: channelForm.kind,
      });
      setChannelFeedback({
        kind: "success",
        message: "通知渠道已创建，测试成功后会开始投递",
      });
    } catch (actionError) {
      setChannelFeedback({
        kind: "error",
        message:
          actionError instanceof Error
            ? actionError.message
            : "无法创建通知渠道",
      });
    } finally {
      setChannelCreating(false);
      setBusy(false);
    }
  }

  async function testChannel(channel: NotificationChannel) {
    setBusy(true);
    setError(null);
    setNotice(null);
    try {
      await testNotificationChannel(channel);
      const refreshed = await listNotificationChannels();
      setChannels(refreshed);
      setNotice(`${channel.name} 测试成功，渠道已启用`);
    } catch (actionError) {
      const refreshed = await listNotificationChannels().catch(() => channels);
      setChannels(refreshed);
      setError(
        actionError instanceof Error ? actionError.message : "通知渠道测试失败",
      );
    } finally {
      setBusy(false);
    }
  }

  async function removeChannel(channel: NotificationChannel) {
    setBusy(true);
    setError(null);
    setNotice(null);
    try {
      await deleteNotificationChannel(channel);
      setChannels((current) =>
        current.filter((candidate) => candidate.id !== channel.id),
      );
      setNotice("通知渠道已删除");
    } catch (actionError) {
      setError(
        actionError instanceof Error ? actionError.message : "无法删除通知渠道",
      );
    } finally {
      setBusy(false);
    }
  }

  async function retryDelivery(delivery: NotificationDelivery) {
    setBusy(true);
    setError(null);
    setNotice(null);
    try {
      const updated = await retryNotificationDelivery(delivery);
      setDeliveries((current) =>
        current.map((item) => (item.id === updated.id ? updated : item)),
      );
      setNotice("投递已放回队列，后台会重新发送");
    } catch (actionError) {
      setError(
        actionError instanceof Error ? actionError.message : "无法重试投递",
      );
    } finally {
      setBusy(false);
    }
  }

  const filtered = Boolean(
    filter.actorId || filter.action || filter.resourceType,
  );

  return (
    <div className="activity-workspace">
      {showInbox ? (
        <header className="catalog-header activity-header">
          <div>
            <p className="eyebrow">INBOX & ACTIVITY</p>
            <h1>通知与团队活动</h1>
          </div>
          <button
            className="secondary-button"
            type="button"
            disabled={busy || unreadCount === 0}
            onClick={() => void readAll()}
          >
            全部标为已读
          </button>
        </header>
      ) : null}

      {error ? (
        <p className="catalog-error" role="alert">
          {error}
        </p>
      ) : null}
      {notice ? <p className="catalog-success">{notice}</p> : null}

      {showInbox ? (
        <div className={`activity-summary${showAudit ? "" : " is-compact"}`}>
          <div>
            <span>未读通知</span>
            <strong>{unreadCount}</strong>
          </div>
          <div>
            <span>最近通知</span>
            <strong>{notifications.length}</strong>
          </div>
          {showAudit ? (
            <div>
              <span>外部渠道</span>
              <strong>{channels.length}</strong>
            </div>
          ) : null}
          {showAudit ? (
            <div>
              <span>活动记录</span>
              <strong>{auditLogs.length}</strong>
            </div>
          ) : null}
        </div>
      ) : null}

      {showInbox ? (
        <section className="activity-section">
          <div className="section-heading">
            <div>
              <p className="eyebrow">NOTIFICATIONS</p>
              <h2>站内通知</h2>
            </div>
            <span className="soft-badge">{unreadCount} unread</span>
          </div>
          {loadingNotifications ? (
            <ActivityLoading label="正在读取通知" />
          ) : notifications.length === 0 ? (
            <div className="activity-empty">
              <strong>目前没有通知</strong>
            </div>
          ) : (
            <div className="notification-list">
              {notifications.map((item) => (
                <article
                  className={`notification-row${item.readAt ? "" : " is-unread"}`}
                  key={item.id}
                >
                  <span className="notification-state" aria-hidden="true" />
                  <div>
                    <strong>{item.title}</strong>
                    <p>{item.body}</p>
                    <span>{formatDateTime(item.createdAt)}</span>
                  </div>
                  {!item.readAt ? (
                    <button
                      className="text-button"
                      type="button"
                      disabled={busy}
                      onClick={() => void read(item)}
                    >
                      标为已读
                    </button>
                  ) : (
                    <span className="activity-read-label">已读</span>
                  )}
                </article>
              ))}
            </div>
          )}
        </section>
      ) : null}

      {showInbox ? (
        <section className="activity-section">
          <div className="section-heading">
            <div>
              <p className="eyebrow">PREFERENCES</p>
              <h2>接收偏好</h2>
            </div>
            <button
              className="secondary-button"
              type="button"
              disabled={busy || preferenceSaving || !preferences}
              onClick={() => void savePreferences()}
            >
              {preferenceSaving ? "保存中..." : "保存偏好"}
            </button>
          </div>
          {loadingExternal || !preferences ? (
            <ActivityLoading label="正在读取接收偏好" />
          ) : (
            <div className="notification-settings">
              <label>
                <input
                  type="checkbox"
                  checked={preferences.emailEnabled}
                  onChange={(event) =>
                    setPreferences((current) =>
                      current
                        ? { ...current, emailEnabled: event.target.checked }
                        : current,
                    )
                  }
                />
                <span>邮件通知</span>
              </label>
              <label>
                <input
                  type="checkbox"
                  checked={preferences.feishuEnabled}
                  onChange={(event) =>
                    setPreferences((current) =>
                      current
                        ? { ...current, feishuEnabled: event.target.checked }
                        : current,
                    )
                  }
                />
                <span>飞书通知</span>
              </label>
              <label>
                <input
                  type="checkbox"
                  checked={preferences.wechatWorkEnabled}
                  onChange={(event) =>
                    setPreferences((current) =>
                      current
                        ? {
                            ...current,
                            wechatWorkEnabled: event.target.checked,
                          }
                        : current,
                    )
                  }
                />
                <span>企业微信通知</span>
              </label>
            </div>
          )}
          {preferenceFeedback ? (
            <InlineFeedbackMessage feedback={preferenceFeedback} />
          ) : null}
        </section>
      ) : null}

      {showExternal ? (
        <section className="activity-section">
          <div className="section-heading">
            <div>
              <p className="eyebrow">EXTERNAL CHANNELS</p>
              <h2>外部通知渠道</h2>
            </div>
          </div>

          <div className="notification-admin-grid">
            <form
              className="notification-channel-form"
              onSubmit={(event) => {
                event.preventDefault();
                void createChannel();
              }}
            >
              <label>
                <span>渠道类型</span>
                <select
                  value={channelForm.kind}
                  onChange={(event) =>
                    setChannelForm((current) => ({
                      ...current,
                      kind: event.target.value as NotificationChannelKind,
                    }))
                  }
                >
                  <option value="email">邮件</option>
                  <option value="feishu">飞书机器人</option>
                  <option value="wechat_work">企业微信机器人</option>
                </select>
              </label>
              <label>
                <span>名称</span>
                <input
                  value={channelForm.name}
                  onChange={(event) =>
                    setChannelForm((current) => ({
                      ...current,
                      name: event.target.value,
                    }))
                  }
                  placeholder="例如 审阅提醒"
                />
              </label>
              {channelForm.kind === "email" ? (
                <>
                  <label>
                    <span>SMTP 主机</span>
                    <input
                      value={channelForm.smtpHost}
                      onChange={(event) =>
                        setChannelForm((current) => ({
                          ...current,
                          smtpHost: event.target.value,
                        }))
                      }
                      placeholder="smtp.example.com"
                    />
                  </label>
                  <div className="notification-form-row">
                    <label>
                      <span>端口</span>
                      <input
                        inputMode="numeric"
                        value={channelForm.smtpPort}
                        onChange={(event) =>
                          setChannelForm((current) => ({
                            ...current,
                            smtpPort: event.target.value,
                          }))
                        }
                      />
                    </label>
                    <label>
                      <span>安全</span>
                      <select
                        value={channelForm.smtpSecurity}
                        onChange={(event) =>
                          setChannelForm((current) => ({
                            ...current,
                            smtpSecurity: event.target.value as
                              | "starttls"
                              | "tls"
                              | "plain",
                          }))
                        }
                      >
                        <option value="starttls">STARTTLS</option>
                        <option value="tls">TLS</option>
                        <option value="plain">Plain</option>
                      </select>
                    </label>
                  </div>
                  <label>
                    <span>发件邮箱</span>
                    <input
                      value={channelForm.smtpFromAddress}
                      onChange={(event) =>
                        setChannelForm((current) => ({
                          ...current,
                          smtpFromAddress: event.target.value,
                        }))
                      }
                      placeholder="review@example.com"
                    />
                  </label>
                  <label>
                    <span>发件名称</span>
                    <input
                      value={channelForm.smtpFromName}
                      onChange={(event) =>
                        setChannelForm((current) => ({
                          ...current,
                          smtpFromName: event.target.value,
                        }))
                      }
                    />
                  </label>
                  <label>
                    <span>SMTP 用户名</span>
                    <input
                      value={channelForm.smtpUsername}
                      onChange={(event) =>
                        setChannelForm((current) => ({
                          ...current,
                          smtpUsername: event.target.value,
                        }))
                      }
                    />
                  </label>
                  <label>
                    <span>SMTP 密码</span>
                    <input
                      type="password"
                      value={channelForm.smtpPassword}
                      onChange={(event) =>
                        setChannelForm((current) => ({
                          ...current,
                          smtpPassword: event.target.value,
                        }))
                      }
                    />
                  </label>
                  <label>
                    <span>测试收件人</span>
                    <input
                      value={channelForm.testRecipient}
                      onChange={(event) =>
                        setChannelForm((current) => ({
                          ...current,
                          testRecipient: event.target.value,
                        }))
                      }
                      placeholder="owner@example.com"
                    />
                  </label>
                </>
              ) : (
                <label>
                  <span>Webhook URL</span>
                  <input
                    value={channelForm.webhookUrl}
                    onChange={(event) =>
                      setChannelForm((current) => ({
                        ...current,
                        webhookUrl: event.target.value,
                      }))
                    }
                    placeholder="https://..."
                  />
                </label>
              )}
              <label className="checkbox-line">
                <input
                  type="checkbox"
                  checked={channelForm.allowPrivateNetwork}
                  onChange={(event) =>
                    setChannelForm((current) => ({
                      ...current,
                      allowPrivateNetwork: event.target.checked,
                    }))
                  }
                />
                <span>允许连接内网通知服务</span>
              </label>
              {channelFeedback ? (
                <InlineFeedbackMessage feedback={channelFeedback} />
              ) : null}
              <div className="notification-channel-form-actions">
                <button
                  className="primary-button"
                  type="submit"
                  disabled={busy || channelCreating}
                >
                  {channelCreating ? "添加中..." : "添加渠道"}
                </button>
              </div>
            </form>

            <div className="notification-channel-list">
              {channels.length === 0 ? (
                <div className="activity-empty compact">
                  <strong>还没有外部渠道</strong>
                  <span>添加邮件、飞书或企业微信后，测试成功才会启用。</span>
                </div>
              ) : (
                channels.map((channel) => (
                  <article
                    className="notification-channel-row"
                    key={channel.id}
                  >
                    <div>
                      <strong>{channel.name}</strong>
                      <span>
                        {channelKindLabel(channel.kind)} ·{" "}
                        {channelStatusLabel(channel.status)}
                      </span>
                      {channel.lastErrorMessage ? (
                        <p>{channel.lastErrorMessage}</p>
                      ) : null}
                    </div>
                    <div>
                      <button
                        className="secondary-button"
                        type="button"
                        disabled={busy}
                        onClick={() => void testChannel(channel)}
                      >
                        测试
                      </button>
                      <button
                        className="text-button danger"
                        type="button"
                        disabled={busy}
                        onClick={() => void removeChannel(channel)}
                      >
                        删除
                      </button>
                    </div>
                  </article>
                ))
              )}
            </div>
          </div>
        </section>
      ) : null}

      {showExternal ? (
        <section className="activity-section">
          <div className="section-heading">
            <div>
              <p className="eyebrow">DELIVERIES</p>
              <h2>外部投递状态</h2>
            </div>
            <button
              className="secondary-button"
              type="button"
              disabled={busy}
              onClick={() => {
                void listNotificationDeliveries().then(setDeliveries);
              }}
            >
              刷新
            </button>
          </div>
          {deliveries.length === 0 ? (
            <div className="activity-empty">
              <strong>暂时没有外部投递记录</strong>
            </div>
          ) : (
            <div className="notification-delivery-list">
              {deliveries.map((delivery) => (
                <article
                  className="notification-delivery-row"
                  key={delivery.id}
                >
                  <div>
                    <strong>
                      {channelKindLabel(delivery.channelKind)} ·{" "}
                      {deliveryStatusLabel(delivery.status)}
                    </strong>
                    <span>
                      {delivery.channelName || "已删除渠道"} ·{" "}
                      {delivery.attempts}/{delivery.maxAttempts} 次
                    </span>
                    {delivery.lastError ? <p>{delivery.lastError}</p> : null}
                  </div>
                  {delivery.status === "failed" ? (
                    <button
                      className="secondary-button"
                      type="button"
                      disabled={busy}
                      onClick={() => void retryDelivery(delivery)}
                    >
                      重试
                    </button>
                  ) : (
                    <time dateTime={delivery.updatedAt}>
                      {formatDateTime(delivery.updatedAt)}
                    </time>
                  )}
                </article>
              ))}
            </div>
          )}
        </section>
      ) : null}

      {showActivity ? (
        <section className="activity-section">
          <div className="section-heading">
            <div>
              <p className="eyebrow">
                {surface === "owner-activity"
                  ? "SYSTEM ACTIVITY"
                  : "TEAM ACTIVITY"}
              </p>
              <h2>
                {surface === "owner-activity" ? "系统活动记录" : "团队活动记录"}
              </h2>
            </div>
            {filtered ? (
              <button
                className="text-button"
                type="button"
                onClick={() => setFilter(emptyFilter)}
              >
                清除筛选
              </button>
            ) : null}
          </div>

          <div className="activity-filter-bar" aria-label="活动筛选">
            <label>
              <span>成员</span>
              <select
                value={filter.actorId}
                onChange={(event) =>
                  setFilter((current) => ({
                    ...current,
                    actorId: event.target.value,
                  }))
                }
              >
                <option value="">全部成员</option>
                {members.map((item) => (
                  <option key={item.userId} value={item.userId}>
                    {item.displayName}
                  </option>
                ))}
              </select>
            </label>
            <label>
              <span>动作</span>
              <select
                value={filter.action}
                onChange={(event) =>
                  setFilter((current) => ({
                    ...current,
                    action: event.target.value,
                  }))
                }
              >
                <option value="">全部动作</option>
                {activityActions.map((item) => (
                  <option key={item.value} value={item.value}>
                    {item.label}
                  </option>
                ))}
              </select>
            </label>
            <label>
              <span>资源</span>
              <select
                value={filter.resourceType}
                onChange={(event) =>
                  setFilter((current) => ({
                    ...current,
                    resourceType: event.target.value,
                  }))
                }
              >
                <option value="">全部资源</option>
                {resourceTypes.map((item) => (
                  <option key={item.value} value={item.value}>
                    {item.label}
                  </option>
                ))}
              </select>
            </label>
          </div>
          {surface === "owner-activity" ? (
            <div className="activity-quick-filters" aria-label="活动快捷筛选">
              <button
                className={
                  filter.resourceType === "upload_check" ? "is-active" : ""
                }
                type="button"
                onClick={() =>
                  setFilter((current) => ({
                    ...current,
                    resourceType:
                      current.resourceType === "upload_check"
                        ? ""
                        : "upload_check",
                  }))
                }
              >
                上传安全事件
              </button>
              <button
                className={
                  filter.action === "upload_check.quarantined"
                    ? "is-active"
                    : ""
                }
                type="button"
                onClick={() =>
                  setFilter((current) => ({
                    ...current,
                    action:
                      current.action === "upload_check.quarantined"
                        ? ""
                        : "upload_check.quarantined",
                    resourceType: "upload_check",
                  }))
                }
              >
                只看隔离
              </button>
            </div>
          ) : null}

          {loadingAudit ? (
            <ActivityLoading label="正在读取活动记录" />
          ) : auditLogs.length === 0 ? (
            <div className="activity-empty">
              <strong>{filtered ? "没有匹配的活动" : "还没有活动记录"}</strong>
            </div>
          ) : (
            <div className="audit-list">
              {auditLogs.map((item) => (
                <article className="audit-row" key={item.id}>
                  <time className="audit-time" dateTime={item.occurredAt}>
                    {formatDateTime(item.occurredAt)}
                  </time>
                  <div>
                    <strong>{auditActionLabel(item.action)}</strong>
                    <span>
                      {item.actorName || actorTypeLabel(item.actorType)} ·{" "}
                      {resourceTypeLabel(item.resourceType)}
                    </span>
                    <AuditDetails item={item} />
                  </div>
                  <code title={item.resourceId}>
                    #{shortIdentifier(item.resourceId)}
                  </code>
                </article>
              ))}
            </div>
          )}
        </section>
      ) : null}
    </div>
  );
}

function ActivityLoading({ label }: { label: string }) {
  return (
    <div className="activity-loading" aria-label={label}>
      <span />
      <span />
      <span />
    </div>
  );
}

function InlineFeedbackMessage({ feedback }: { feedback: InlineFeedback }) {
  return (
    <p
      className={`inline-feedback is-${feedback.kind}`}
      role={feedback.kind === "error" ? "alert" : "status"}
    >
      {feedback.message}
    </p>
  );
}

function AuditDetails({ item }: { item: AuditLog }) {
  if (item.resourceType !== "upload_check" || !item.details) {
    return null;
  }
  const details = item.details;
  const facts = [
    ["策略", uploadSecurityPolicyLabel(details.uploadSecurityPolicy)],
    ["状态", uploadCheckStatusLabel(details.status)],
    ["结果", uploadCheckResultLabel(details.resultCode)],
    [
      "版本",
      details.assetVersionId ? shortIdentifier(details.assetVersionId) : "",
    ],
  ].filter(([, value]) => value);
  if (facts.length === 0) {
    return null;
  }
  return (
    <dl className="audit-detail-pills">
      {facts.map(([label, value]) => (
        <div key={label}>
          <dt>{label}</dt>
          <dd>{value}</dd>
        </div>
      ))}
    </dl>
  );
}

function validateChannelForm(form: ChannelForm): string | null {
  if (!form.name.trim()) {
    return "请先填写渠道名称";
  }
  if (form.kind === "email") {
    if (!form.smtpHost.trim()) return "请填写 SMTP 主机";
    if (!form.smtpFromAddress.trim()) return "请填写发件邮箱";
    if (!form.testRecipient.trim()) return "请填写测试收件人";
    return null;
  }
  if (!form.webhookUrl.trim()) {
    return "请填写 Webhook URL";
  }
  if (!/^https?:\/\//i.test(form.webhookUrl.trim())) {
    return "Webhook URL 需要以 http:// 或 https:// 开头";
  }
  return null;
}

function buildChannelInput(form: ChannelForm): NotificationChannelInput {
  if (form.kind === "email") {
    return {
      kind: "email",
      name: form.name,
      smtpHost: form.smtpHost,
      smtpPort: Number(form.smtpPort) || 587,
      smtpSecurity: form.smtpSecurity,
      smtpFromAddress: form.smtpFromAddress,
      smtpFromName: form.smtpFromName,
      smtpUsername: form.smtpUsername,
      smtpPassword: form.smtpPassword,
      testRecipient: form.testRecipient,
      allowPrivateNetwork: form.allowPrivateNetwork,
    };
  }
  return {
    kind: form.kind,
    name: form.name,
    webhookUrl: form.webhookUrl,
    allowPrivateNetwork: form.allowPrivateNetwork,
  };
}

const activityActions = [
  { value: "identity.profile_updated", label: "更新个人资料" },
  { value: "workspace.registration_settings_updated", label: "更新注册设置" },
  { value: "membership.updated", label: "更新成员权限" },
  { value: "membership.invitation_created", label: "创建成员邀请" },
  { value: "membership.invitation_accepted", label: "接受成员邀请" },
  { value: "project.created", label: "创建项目" },
  { value: "project.updated", label: "更新项目" },
  { value: "project.archived", label: "归档项目" },
  { value: "project.restored", label: "恢复项目" },
  { value: "asset.created", label: "创建资产" },
  { value: "asset.version_uploaded", label: "上传新版本" },
  { value: "asset.current_version_changed", label: "切换当前版本" },
  { value: "upload_check.ready", label: "上传安全通过" },
  { value: "upload_check.quarantined", label: "上传已隔离" },
  { value: "upload_check.rejected", label: "上传已拒绝" },
  { value: "review.session_created", label: "创建审阅" },
  { value: "review.template_created", label: "创建审阅模板" },
  { value: "review.template_updated", label: "更新审阅模板" },
  { value: "review.template_deleted", label: "删除审阅模板" },
  { value: "review.session_opened", label: "开启审阅" },
  { value: "review.session_closed", label: "结束审阅" },
  { value: "review.thread_created", label: "创建反馈" },
  { value: "review.comment_added", label: "回复反馈" },
  { value: "review.decision_submitted", label: "提交审阅结论" },
  { value: "share.created", label: "创建分享" },
  { value: "share.updated", label: "更新分享策略" },
  { value: "share.revoked", label: "撤销分享" },
  { value: "notification.channel_created", label: "创建通知渠道" },
  { value: "notification.channel_updated", label: "更新通知渠道" },
  { value: "notification.channel_deleted", label: "删除通知渠道" },
  { value: "notification.channel_test_succeeded", label: "通知渠道测试成功" },
  { value: "notification.channel_test_failed", label: "通知渠道测试失败" },
  { value: "notification.delivery_retried", label: "重试通知投递" },
  { value: "project_storage.grant_set", label: "更新项目可用存储" },
] as const;

const resourceTypes = [
  { value: "user", label: "用户" },
  { value: "workspace", label: "工作空间" },
  { value: "membership", label: "成员" },
  { value: "invitation", label: "邀请" },
  { value: "project", label: "项目" },
  { value: "asset", label: "资产" },
  { value: "asset_version", label: "资产版本" },
  { value: "upload_check", label: "上传安全检查" },
  { value: "review_session", label: "审阅" },
  { value: "review_template", label: "审阅模板" },
  { value: "comment_thread", label: "反馈线程" },
  { value: "comment", label: "评论" },
  { value: "share", label: "分享" },
  { value: "share_link", label: "分享入口" },
  { value: "notification_channel", label: "通知渠道" },
  { value: "notification_delivery", label: "通知投递" },
  { value: "project_storage_grant", label: "项目存储授权" },
] as const;

function auditActionLabel(value: string) {
  const match = activityActions.find((item) => item.value === value);
  const labels: Record<string, string> = {
    "identity.workspace_setup": "完成首次设置",
    "identity.session_created": "登录工作空间",
    "identity.session_revoked": "退出工作空间",
    "membership.invitation_resent": "重新发送成员邀请",
    "membership.invitation_revoked": "撤销成员邀请",
    "project.deleted": "删除项目",
    "review.session_updated": "更新审阅",
    "review.comment_updated": "编辑评论",
    "review.comment_deleted": "删除评论",
    "review.thread_resolved": "解决反馈",
    "review.thread_reopened": "重新打开反馈",
    "share.link_created": "创建分享入口",
    "share.link_revoked": "撤销分享入口",
    "share.visitor_code_created": "创建访客验证码",
    "storage.provider_created": "创建远程存储",
    "storage.provider_updated": "更新远程存储",
    "storage.provider_deleted": "删除远程存储",
    "storage.provider_root_created": "登记远程存储位置",
    "upload_check.ready": "上传安全通过",
    "upload_check.quarantined": "上传已隔离",
    "upload_check.rejected": "上传已拒绝",
  };
  return match?.label ?? labels[value] ?? value;
}

function actorTypeLabel(value: string) {
  if (value === "visitor") return "访客";
  if (value === "system") return "系统";
  if (value === "node") return "处理节点";
  return "成员";
}

function resourceTypeLabel(value: string) {
  const match = resourceTypes.find((item) => item.value === value);
  const labels: Record<string, string> = {
    workspace: "工作空间",
    session: "会话",
    storage_provider: "远程存储",
    authorized_root: "存储位置",
    upload_check: "上传安全检查",
  };
  return match?.label ?? labels[value] ?? value;
}

function uploadSecurityPolicyLabel(value: string | undefined): string {
  if (value === "quick") return "快速";
  if (value === "standard") return "标准";
  if (value === "enhanced") return "增强";
  return value ?? "";
}

function uploadCheckStatusLabel(value: string | undefined): string {
  if (value === "ready") return "已放行";
  if (value === "quarantined") return "已隔离";
  if (value === "rejected") return "已拒绝";
  if (value === "checking") return "检查中";
  if (value === "processing") return "处理中";
  return value ?? "";
}

function uploadCheckResultLabel(value: string | undefined): string {
  switch (value) {
    case "basic_checks_passed":
      return "基础检查通过";
    case "type_checks_passed":
      return "类型检查通过";
    case "malware_not_scanned":
      return "未执行扫描";
    case "malware_clean":
      return "扫描通过";
    case "malware_scan_unavailable":
      return "扫描器不可用";
    case "malware_scan_failed":
      return "扫描失败";
    case "malware_detected":
      return "发现风险";
    default:
      return value ?? "";
  }
}

function channelKindLabel(value: string) {
  if (value === "email") return "邮件";
  if (value === "feishu") return "飞书";
  if (value === "wechat_work") return "企业微信";
  return value;
}

function channelStatusLabel(value: string) {
  if (value === "active") return "已启用";
  if (value === "disabled") return "待测试";
  if (value === "error") return "异常";
  return value;
}

function deliveryStatusLabel(value: string) {
  if (value === "queued") return "排队中";
  if (value === "sending") return "发送中";
  if (value === "succeeded") return "已送达";
  if (value === "failed") return "失败";
  if (value === "skipped") return "已跳过";
  return value;
}

function formatDateTime(value: string) {
  return new Intl.DateTimeFormat("zh-CN", {
    month: "numeric",
    day: "numeric",
    hour: "2-digit",
    minute: "2-digit",
  }).format(new Date(value));
}

function shortIdentifier(value: string) {
  return value.length > 10 ? value.slice(0, 10) : value;
}

function isAbort(error: unknown) {
  return error instanceof DOMException && error.name === "AbortError";
}
