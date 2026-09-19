import { type FormEvent, useEffect, useState } from "react";
import type {
  Invitation,
  Membership,
  SessionInfo,
  WorkspaceRegistrationSettings,
} from "@review-studio/contracts";
import {
  createInvitation,
  listInvitations,
  resendInvitation,
  revokeInvitation,
} from "./api/invitations";
import {
  getSession,
  getRegistrationSettings,
  updateRegistrationSettings,
} from "./api/identity";
import { listMembers, updateMember } from "./api/members";

type Notice = { tone: "success" | "error"; message: string } | null;

export function TeamWorkspace({
  session,
  embedded = false,
  onSessionUpdated,
}: {
  session: SessionInfo;
  embedded?: boolean;
  onSessionUpdated?: (session: SessionInfo) => void;
}) {
  const [members, setMembers] = useState<Membership[]>([]);
  const [invitations, setInvitations] = useState<Invitation[]>([]);
  const [loading, setLoading] = useState(true);
  const [busyID, setBusyID] = useState<string | null>(null);
  const [error, setError] = useState<string | null>(null);
  const [inviteEmail, setInviteEmail] = useState("");
  const [inviteRole, setInviteRole] = useState<Invitation["role"]>("member");
  const [latestURL, setLatestURL] = useState<string | null>(null);
  const [notice, setNotice] = useState<Notice>(null);
  const [registrationSettings, setRegistrationSettings] =
    useState<WorkspaceRegistrationSettings | null>(null);
  const [registrationDraft, setRegistrationDraft] = useState({
    teamName: session.workspace.teamName || session.workspace.name,
    registrationEnabled: false,
    emailVerificationRequired: false,
  });
  const [settingsBusy, setSettingsBusy] = useState(false);
  const [settingsNotice, setSettingsNotice] = useState<Notice>(null);
  const canManage = session.role === "owner" || session.role === "admin";
  const isOwner = session.role === "owner";

  useEffect(() => {
    const controller = new AbortController();
    const loadInvitations = canManage
      ? listInvitations(controller.signal)
      : Promise.resolve([]);
    Promise.all([listMembers(controller.signal), loadInvitations])
      .then(([memberItems, invitationItems]) => {
        setMembers(memberItems);
        setInvitations(invitationItems);
      })
      .catch((loadError: unknown) => {
        if (
          !(
            loadError instanceof DOMException && loadError.name === "AbortError"
          )
        ) {
          setError(
            loadError instanceof Error ? loadError.message : "无法读取团队成员",
          );
        }
      })
      .finally(() => setLoading(false));
    return () => controller.abort();
  }, [canManage]);

  useEffect(() => {
    if (!isOwner) {
      return;
    }
    const controller = new AbortController();
    getRegistrationSettings(controller.signal)
      .then((settings) => {
        setRegistrationSettings(settings);
        setRegistrationDraft({
          teamName: settings.teamName,
          registrationEnabled: settings.registrationEnabled,
          emailVerificationRequired: settings.emailVerificationRequired,
        });
      })
      .catch((loadError: unknown) => {
        if (
          !(
            loadError instanceof DOMException && loadError.name === "AbortError"
          )
        ) {
          setSettingsNotice({
            tone: "error",
            message:
              loadError instanceof Error
                ? loadError.message
                : "无法读取注册设置",
          });
        }
      });
    return () => controller.abort();
  }, [isOwner]);

  async function saveMember(
    item: Membership,
    role: Membership["role"],
    status: Membership["status"],
  ) {
    setBusyID(`member:${item.id}`);
    setError(null);
    try {
      const updated = await updateMember(item, role, status);
      setMembers((current) =>
        current.map((candidate) =>
          candidate.id === updated.id ? updated : candidate,
        ),
      );
    } catch (actionError) {
      setError(
        actionError instanceof Error ? actionError.message : "无法更新成员",
      );
    } finally {
      setBusyID(null);
    }
  }

  async function submitInvitation(event: FormEvent<HTMLFormElement>) {
    event.preventDefault();
    if (!inviteEmail.trim()) {
      setNotice({ tone: "error", message: "请输入受邀人的邮箱" });
      return;
    }
    setBusyID("invitation:new");
    setNotice(null);
    try {
      const created = await createInvitation({
        email: inviteEmail.trim(),
        role: inviteRole,
      });
      setInvitations((current) => [created.invitation, ...current]);
      setLatestURL(absoluteURL(created.url));
      setInviteEmail("");
      setNotice({
        tone: "success",
        message: "邀请已创建。请复制下方链接发送给对方。",
      });
    } catch (actionError) {
      setNotice({
        tone: "error",
        message:
          actionError instanceof Error ? actionError.message : "无法创建邀请",
      });
    } finally {
      setBusyID(null);
    }
  }

  async function resend(item: Invitation) {
    setBusyID(`invitation:${item.id}`);
    setNotice(null);
    try {
      const result = await resendInvitation(item.id);
      replaceInvitation(result.invitation);
      setLatestURL(absoluteURL(result.url));
      setNotice({
        tone: "success",
        message: "新链接已生成，旧链接已经失效。",
      });
    } catch (actionError) {
      showInvitationError(actionError);
    } finally {
      setBusyID(null);
    }
  }

  async function revoke(item: Invitation) {
    setBusyID(`invitation:${item.id}`);
    setNotice(null);
    try {
      replaceInvitation(await revokeInvitation(item.id));
      setLatestURL(null);
      setNotice({ tone: "success", message: "邀请已撤销" });
    } catch (actionError) {
      showInvitationError(actionError);
    } finally {
      setBusyID(null);
    }
  }

  function replaceInvitation(updated: Invitation) {
    setInvitations((current) =>
      current.map((item) => (item.id === updated.id ? updated : item)),
    );
  }

  function showInvitationError(actionError: unknown) {
    setNotice({
      tone: "error",
      message:
        actionError instanceof Error ? actionError.message : "无法更新邀请",
    });
  }

  async function copyLatestURL() {
    if (!latestURL) return;
    const copied = await copyText(latestURL);
    setNotice(
      copied
        ? { tone: "success", message: "邀请链接已复制" }
        : { tone: "error", message: "复制失败，请手动复制链接" },
    );
  }

  async function saveRegistrationSettings() {
    if (!registrationSettings) {
      return;
    }
    if (!registrationDraft.teamName.trim()) {
      setSettingsNotice({ tone: "error", message: "请输入团队名称" });
      return;
    }
    setSettingsBusy(true);
    setSettingsNotice(null);
    try {
      const updated = await updateRegistrationSettings({
        ...registrationDraft,
        revision: registrationSettings.revision,
      });
      setRegistrationSettings(updated);
      setRegistrationDraft({
        teamName: updated.teamName,
        registrationEnabled: updated.registrationEnabled,
        emailVerificationRequired: updated.emailVerificationRequired,
      });
      if (onSessionUpdated) {
        onSessionUpdated(await getSession());
      }
      setSettingsNotice({ tone: "success", message: "全局设置已保存" });
    } catch (actionError) {
      setSettingsNotice({
        tone: "error",
        message:
          actionError instanceof Error
            ? actionError.message
            : "无法保存注册设置",
      });
    } finally {
      setSettingsBusy(false);
    }
  }

  return (
    <div className={`team-workspace${embedded ? " is-embedded" : ""}`}>
      {!embedded ? (
        <header className="catalog-header">
          <div>
            <p className="eyebrow">TEAM ACCESS</p>
            <h1>成员与角色</h1>
            <p className="page-intro">
              每位成员使用独立账号。角色决定管理、上传、下载、审阅和分享权限。
            </p>
          </div>
          <span className="soft-badge">{members.length} members</span>
        </header>
      ) : null}

      {error ? (
        <p className="catalog-error" role="alert">
          {error}
        </p>
      ) : null}

      <section className="team-permission-strip" aria-label="角色权限摘要">
        <div>
          <strong>Owner</strong>
          <span>工作空间、成员与全部资源</span>
        </div>
        <div>
          <strong>项目主管</strong>
          <span>自己的项目、成员与审阅流程</span>
        </div>
        <div>
          <strong>普通账户</strong>
          <span>受邀项目内上传、审阅与分享</span>
        </div>
        <div>
          <strong>临时账户</strong>
          <span>项目绑定的临时权限</span>
        </div>
      </section>

      {isOwner ? (
        <section className="team-registration-section">
          <div className="section-heading">
            <div>
              <p className="eyebrow">WORKSPACE BRAND</p>
              <h2>团队名称</h2>
              <span>显示在工作台、邀请页和分享页。</span>
            </div>
            <button
              className="primary-button"
              type="button"
              disabled={settingsBusy || !registrationSettings}
              onClick={() => void saveRegistrationSettings()}
            >
              {settingsBusy ? "保存中…" : "保存设置"}
            </button>
          </div>
          <label className="team-brand-field">
            <span>团队名称</span>
            <input
              value={registrationDraft.teamName}
              maxLength={80}
              placeholder={session.workspace.name}
              disabled={settingsBusy || !registrationSettings}
              onChange={(event) =>
                setRegistrationDraft({
                  ...registrationDraft,
                  teamName: event.target.value,
                })
              }
            />
          </label>
          <div className="section-heading team-registration-heading">
            <div>
              <p className="eyebrow">GLOBAL REGISTRATION</p>
              <h2>开放注册</h2>
            </div>
          </div>
          <div className="team-registration-controls">
            <label>
              <input
                type="checkbox"
                checked={registrationDraft.registrationEnabled}
                disabled={settingsBusy || !registrationSettings}
                onChange={(event) =>
                  setRegistrationDraft({
                    ...registrationDraft,
                    registrationEnabled: event.target.checked,
                  })
                }
              />
              <span>允许新用户自行注册为普通账户</span>
            </label>
            <label>
              <input
                type="checkbox"
                checked={registrationDraft.emailVerificationRequired}
                disabled={settingsBusy || !registrationSettings}
                onChange={(event) =>
                  setRegistrationDraft({
                    ...registrationDraft,
                    emailVerificationRequired: event.target.checked,
                  })
                }
              />
              <span>要求邮箱验证后才能完成注册</span>
            </label>
          </div>
          <p className="team-empty-copy">
            注册用户默认没有项目权限，必须被项目主管或 Owner
            加入具体项目后才能看到内容。
          </p>
          {settingsNotice ? (
            <p
              className={`team-notice is-${settingsNotice.tone}`}
              role={settingsNotice.tone === "error" ? "alert" : "status"}
            >
              {settingsNotice.message}
            </p>
          ) : null}
        </section>
      ) : null}

      {canManage ? (
        <section className="team-invitation-section">
          <div className="section-heading">
            <div>
              <p className="eyebrow">INVITATIONS</p>
              <h2>邀请成员</h2>
            </div>
          </div>
          <form className="team-invite-form" onSubmit={submitInvitation}>
            <label>
              <span>邮箱</span>
              <input
                type="email"
                value={inviteEmail}
                autoComplete="email"
                placeholder="member@example.com"
                disabled={busyID === "invitation:new"}
                onChange={(event) => setInviteEmail(event.target.value)}
              />
            </label>
            <label>
              <span>角色</span>
              <select
                value={inviteRole}
                disabled={busyID === "invitation:new"}
                onChange={(event) =>
                  setInviteRole(event.target.value as Invitation["role"])
                }
              >
                <option value="admin">项目主管账户</option>
                <option value="member">普通账户</option>
                <option value="guest">临时账户</option>
              </select>
            </label>
            <button
              className="primary-button"
              type="submit"
              disabled={busyID === "invitation:new"}
            >
              {busyID === "invitation:new" ? "创建中…" : "创建邀请"}
            </button>
          </form>

          {latestURL ? (
            <div className="team-invite-secret">
              <div>
                <strong>本次邀请链接</strong>
                <span>链接只显示这一次，重新发送会使旧链接失效。</span>
              </div>
              <input
                aria-label="本次邀请链接"
                value={latestURL}
                readOnly
                onFocus={(event) => event.currentTarget.select()}
              />
              <button
                className="secondary-button"
                type="button"
                onClick={() => void copyLatestURL()}
              >
                复制链接
              </button>
            </div>
          ) : null}

          {notice ? (
            <p
              className={`team-notice is-${notice.tone}`}
              role={notice.tone === "error" ? "alert" : "status"}
              aria-live="polite"
            >
              {notice.message}
            </p>
          ) : null}

          <div className="team-invitation-list">
            {invitations.length === 0 ? (
              <p className="team-empty-copy">尚未创建邀请。</p>
            ) : (
              invitations.map((item) => (
                <InvitationRow
                  key={item.id}
                  item={item}
                  busy={busyID === `invitation:${item.id}`}
                  onResend={resend}
                  onRevoke={revoke}
                />
              ))
            )}
          </div>
        </section>
      ) : null}

      <section className="team-list-section">
        <div className="section-heading">
          <div>
            <p className="eyebrow">MEMBERS</p>
            <h2>当前成员</h2>
          </div>
        </div>
        {loading ? (
          <div className="activity-loading" aria-label="正在读取成员">
            <span />
            <span />
            <span />
          </div>
        ) : (
          <div className="team-member-list">
            {members.map((item) => (
              <MemberRow
                key={item.id}
                item={item}
                currentUserID={session.user.id}
                canManage={canManage}
                busy={busyID === `member:${item.id}`}
                onSave={saveMember}
              />
            ))}
          </div>
        )}
      </section>
    </div>
  );
}

function InvitationRow({
  item,
  busy,
  onResend,
  onRevoke,
}: {
  item: Invitation;
  busy: boolean;
  onResend: (item: Invitation) => Promise<void>;
  onRevoke: (item: Invitation) => Promise<void>;
}) {
  return (
    <article className="team-invitation-row">
      <div className="team-invitation-identity">
        <strong>{item.email}</strong>
        <span>
          {roleLabel(item.role)} · 发送 {item.sendCount} 次 · 有效至{" "}
          {formatDate(item.expiresAt)}
        </span>
      </div>
      <span className={`team-status is-${item.status}`}>
        {invitationStatusLabel(item.status)}
      </span>
      {item.status === "pending" ? (
        <div className="team-invitation-actions">
          <button
            className="secondary-button"
            type="button"
            disabled={busy}
            onClick={() => void onResend(item)}
          >
            重新发送
          </button>
          <button
            className="text-danger-button"
            type="button"
            disabled={busy}
            onClick={() => void onRevoke(item)}
          >
            撤销
          </button>
        </div>
      ) : (
        <time dateTime={item.updatedAt}>{formatDate(item.updatedAt)}</time>
      )}
    </article>
  );
}

function MemberRow({
  item,
  currentUserID,
  canManage,
  busy,
  onSave,
}: {
  item: Membership;
  currentUserID: string;
  canManage: boolean;
  busy: boolean;
  onSave: (
    item: Membership,
    role: Membership["role"],
    status: Membership["status"],
  ) => Promise<void>;
}) {
  const [role, setRole] = useState(item.role);
  const [status, setStatus] = useState(item.status);
  const changed = role !== item.role || status !== item.status;

  return (
    <article className="team-member-row">
      <div className="team-avatar" aria-hidden="true">
        {item.displayName.slice(0, 1).toUpperCase()}
      </div>
      <div className="team-member-identity">
        <strong>
          {item.displayName}
          {item.userId === currentUserID ? "（你）" : ""}
        </strong>
        <span>{item.email ?? "旧版 Owner · 尚未设置邮箱"}</span>
      </div>
      {canManage ? (
        <div className="team-member-controls">
          <label>
            <span>角色</span>
            <select
              value={role}
              disabled={busy}
              onChange={(event) =>
                setRole(event.target.value as Membership["role"])
              }
            >
              <option value="owner">Owner</option>
              <option value="admin">项目主管账户</option>
              <option value="member">普通账户</option>
              <option value="guest">临时账户</option>
            </select>
          </label>
          <label>
            <span>状态</span>
            <select
              value={status}
              disabled={busy}
              onChange={(event) =>
                setStatus(event.target.value as Membership["status"])
              }
            >
              <option value="active">有效</option>
              <option value="disabled">停用</option>
            </select>
          </label>
          <button
            className="secondary-button"
            type="button"
            disabled={!changed || busy}
            onClick={() => void onSave(item, role, status)}
          >
            {busy ? "保存中…" : "保存"}
          </button>
        </div>
      ) : (
        <div className="team-member-readonly">
          <strong>{roleLabel(item.role)}</strong>
          <span>{item.status === "active" ? "有效" : "已停用"}</span>
        </div>
      )}
    </article>
  );
}

function roleLabel(role: Membership["role"] | Invitation["role"]) {
  switch (role) {
    case "owner":
      return "Owner";
    case "admin":
      return "项目主管账户";
    case "member":
      return "普通账户";
    case "guest":
      return "临时账户";
  }
}

function invitationStatusLabel(status: Invitation["status"]) {
  switch (status) {
    case "pending":
      return "待接受";
    case "accepted":
      return "已加入";
    case "revoked":
      return "已撤销";
    case "expired":
      return "已过期";
  }
}

function formatDate(value: string) {
  return new Intl.DateTimeFormat("zh-CN", {
    year: "numeric",
    month: "2-digit",
    day: "2-digit",
    hour: "2-digit",
    minute: "2-digit",
  }).format(new Date(value));
}

function absoluteURL(value: string) {
  return new URL(value, window.location.origin).href;
}

async function copyText(value: string) {
  try {
    if (navigator.clipboard?.writeText) {
      await navigator.clipboard.writeText(value);
      return true;
    }
  } catch {
    // HTTP LAN origins may not expose the Clipboard API.
  }
  const textarea = document.createElement("textarea");
  textarea.value = value;
  textarea.setAttribute("readonly", "true");
  textarea.style.position = "fixed";
  textarea.style.left = "-9999px";
  document.body.appendChild(textarea);
  textarea.select();
  let copied = false;
  try {
    copied = document.execCommand("copy");
  } finally {
    document.body.removeChild(textarea);
  }
  return copied;
}
