import { type FormEvent, useEffect, useState } from "react";
import type { InvitationPreview } from "@review-studio/contracts";
import { acceptInvitation, previewInvitation } from "./api/invitations";
import { VistoMark, PRODUCT_NAME_FULL } from "./brand";
import { buildInvitationTitle, setDocumentTitle } from "./lib/page-title";
import { browserLocale } from "./lib/setup";

type EntryState =
  | { status: "loading" }
  | { status: "invalid"; message: string }
  | { status: "ready"; preview: InvitationPreview };

export function InvitationEntry() {
  const [token] = useState(() => window.location.hash.slice(1));
  const [state, setState] = useState<EntryState>({ status: "loading" });
  const [displayName, setDisplayName] = useState("");
  const [password, setPassword] = useState("");
  const [confirmPassword, setConfirmPassword] = useState("");
  const [submitting, setSubmitting] = useState(false);
  const [error, setError] = useState<string | null>(null);

  useEffect(() => {
    if (!token) {
      setState({ status: "invalid", message: "邀请链接缺少访问令牌" });
      return;
    }
    window.history.replaceState(null, "", window.location.pathname);
    previewInvitation(token)
      .then((preview) => setState({ status: "ready", preview }))
      .catch((loadError: unknown) =>
        setState({
          status: "invalid",
          message:
            loadError instanceof Error
              ? loadError.message
              : "邀请链接不存在或已经失效",
        }),
      );
  }, [token]);

  useEffect(() => {
    setDocumentTitle(
      buildInvitationTitle(
        state.status === "ready" ? state.preview.workspaceName : null,
      ),
    );
  }, [state]);

  async function submit(event: FormEvent<HTMLFormElement>) {
    event.preventDefault();
    if (!displayName.trim()) {
      setError("请输入你的显示名称");
      return;
    }
    if (password.length < 10) {
      setError("密码至少需要 10 个字符");
      return;
    }
    if (password !== confirmPassword) {
      setError("两次输入的密码不一致");
      return;
    }
    setSubmitting(true);
    setError(null);
    try {
      await acceptInvitation({
        token,
        displayName: displayName.trim(),
        password,
        locale: browserLocale(),
      });
      window.location.replace("/");
    } catch (acceptError) {
      setError(
        acceptError instanceof Error ? acceptError.message : "暂时无法接受邀请",
      );
      setSubmitting(false);
    }
  }

  return (
    <main className="auth-shell invitation-entry">
      <section className="auth-card login-card">
        <div className="brand auth-brand">
          <span className="brand-mark" aria-hidden="true">
            <VistoMark className="visto-mark" />
          </span>
          <span>{PRODUCT_NAME_FULL}</span>
        </div>

        {state.status === "loading" ? (
          <>
            <p className="eyebrow">TEAM INVITATION</p>
            <h1>正在验证邀请</h1>
            <div className="auth-loading-lines" aria-hidden="true">
              <span />
              <span />
            </div>
          </>
        ) : null}

        {state.status === "invalid" ? (
          <>
            <p className="eyebrow">INVITATION UNAVAILABLE</p>
            <h1>这个邀请无法使用</h1>
            <p role="alert">{state.message}</p>
          </>
        ) : null}

        {state.status === "ready" ? (
          <>
            <p className="eyebrow">JOIN WORKSPACE</p>
            <h1>加入 {state.preview.workspaceName}</h1>
            <p>
              你将使用 <strong>{state.preview.email}</strong> 作为账号，并以{" "}
              {roleLabel(state.preview.role)} 身份加入。
            </p>
            <div className="invitation-summary">
              <span>邀请有效期</span>
              <strong>{formatDate(state.preview.expiresAt)}</strong>
            </div>
            <form className="setup-form compact-form" onSubmit={submit}>
              <label className="field">
                <span>显示名称</span>
                <input
                  name="displayName"
                  value={displayName}
                  autoComplete="name"
                  autoFocus
                  onChange={(event) => setDisplayName(event.target.value)}
                />
              </label>
              <label className="field">
                <span>设置密码</span>
                <input
                  name="password"
                  type="password"
                  value={password}
                  autoComplete="new-password"
                  placeholder="至少 10 个字符"
                  onChange={(event) => setPassword(event.target.value)}
                />
              </label>
              <label className="field">
                <span>再次输入密码</span>
                <input
                  name="confirmPassword"
                  type="password"
                  value={confirmPassword}
                  autoComplete="new-password"
                  onChange={(event) => setConfirmPassword(event.target.value)}
                />
              </label>
              {error ? (
                <p className="form-error" role="alert">
                  {error}
                </p>
              ) : null}
              <button
                className="primary-button auth-submit"
                type="submit"
                disabled={submitting}
              >
                {submitting ? "正在加入…" : "接受邀请并加入"}
              </button>
            </form>
          </>
        ) : null}
      </section>
    </main>
  );
}

function roleLabel(role: InvitationPreview["role"]) {
  return role.slice(0, 1).toUpperCase() + role.slice(1);
}

function formatDate(value: string) {
  return new Intl.DateTimeFormat("zh-CN", {
    year: "numeric",
    month: "long",
    day: "numeric",
    hour: "2-digit",
    minute: "2-digit",
  }).format(new Date(value));
}
