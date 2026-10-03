import { lazy, Suspense, type FormEvent, useEffect, useState } from "react";
import type {
  SessionInfo,
  SetupStatus,
  SystemInfo,
} from "@review-studio/contracts";
import {
  createSetup,
  claimHostSetup,
  getSession,
  getSetupStatus,
  login,
  logout,
} from "./api/identity";
import { ApiError } from "./api/client";
import { getSystemInfo } from "./api/system";
import { formatRuntimeLabel } from "./lib/format";
import {
  browserLocale,
  browserTimezone,
  buildSetupInput,
  type SetupDraft,
  validateSetupDraft,
} from "./lib/setup";
import { setDocumentTitle } from "./lib/page-title";
import { I18nProvider } from "./lib/i18n-react";
import { StudioWorkspace } from "./StudioWorkspace";
import { VistoMark, PRODUCT_NAME_FULL, PRODUCT_NAME_CN } from "./brand";

const PublicShareEntry = lazy(() =>
  import("./PublicShareEntry").then((module) => ({
    default: module.PublicShareEntry,
  })),
);
const InvitationEntry = lazy(() =>
  import("./InvitationEntry").then((module) => ({
    default: module.InvitationEntry,
  })),
);

type AppState =
  | { status: "loading" }
  | { status: "setup"; system: SystemInfo; setup: SetupStatus }
  | { status: "login"; system: SystemInfo }
  | { status: "ready"; system: SystemInfo; session: SessionInfo }
  | { status: "error"; message: string };

export function App() {
  if (window.location.pathname.startsWith("/s/")) {
    return (
      <Suspense fallback={<LoadingScreen />}>
        <PublicShareEntry />
      </Suspense>
    );
  }
  if (window.location.pathname.startsWith("/join/")) {
    return (
      <Suspense fallback={<LoadingScreen />}>
        <InvitationEntry />
      </Suspense>
    );
  }
  return <WorkspaceApp />;
}

function WorkspaceApp() {
  const [state, setState] = useState<AppState>({ status: "loading" });

  useEffect(() => {
    if (state.status !== "ready") {
      setDocumentTitle("Visto");
    }
  }, [state.status]);

  useEffect(() => {
    const controller = new AbortController();

    loadApplication(controller.signal)
      .then(setState)
      .catch((error: unknown) => {
        if (error instanceof DOMException && error.name === "AbortError") {
          return;
        }
        setState({
          status: "error",
          message: error instanceof Error ? error.message : "无法连接本地服务",
        });
      });

    return () => {
      controller.abort();
    };
  }, []);

  if (state.status === "loading") {
    return <LoadingScreen />;
  }

  if (state.status === "error") {
    return <ConnectionError message={state.message} />;
  }

  if (state.status === "setup") {
    return (
      <SetupScreen
        system={state.system}
        setup={state.setup}
        onComplete={(session) => {
          setState({ status: "ready", system: state.system, session });
        }}
      />
    );
  }

  if (state.status === "login") {
    return (
      <LoginScreen
        system={state.system}
        onComplete={(session) => {
          setState({ status: "ready", system: state.system, session });
        }}
      />
    );
  }

  return (
    <I18nProvider locale={state.session.user.locale}>
      <StudioWorkspace
        system={state.system}
        session={state.session}
        onSessionUpdated={(session) =>
          setState({ status: "ready", system: state.system, session })
        }
        onLogout={async () => {
          await logout();
          setState({ status: "login", system: state.system });
        }}
      />
    </I18nProvider>
  );
}

async function loadApplication(signal: AbortSignal): Promise<AppState> {
  const [system, setup] = await Promise.all([
    getSystemInfo(signal),
    getSetupStatus(signal),
  ]);

  if (setup.setupRequired) {
    return { status: "setup", system, setup };
  }

  try {
    const session = await getSession(signal);
    return { status: "ready", system, session };
  } catch (error) {
    if (error instanceof ApiError && error.status === 401) {
      return { status: "login", system };
    }
    throw error;
  }
}

function LoadingScreen() {
  return (
    <main className="auth-shell">
      <div className="auth-card is-loading" aria-live="polite">
        <Brand />
        <p className="eyebrow">LOCAL-FIRST MEDIA REVIEW</p>
        <h1>正在打开你的工作空间</h1>
        <p>检查数据库、身份和本机媒体能力。</p>
        <div className="auth-loading-lines" aria-hidden="true">
          <span />
          <span />
        </div>
      </div>
    </main>
  );
}

function ConnectionError({ message }: { message: string }) {
  return (
    <main className="auth-shell">
      <div className="auth-card">
        <Brand />
        <p className="eyebrow">CORE OFFLINE</p>
        <h1>本地服务没有响应</h1>
        <p>{message}</p>
        <code className="command-hint">make dev-core</code>
      </div>
    </main>
  );
}

function SetupScreen({
  system,
  setup,
  onComplete,
}: {
  system: SystemInfo;
  setup: SetupStatus;
  onComplete: (session: SessionInfo) => void;
}) {
  const [draft, setDraft] = useState<SetupDraft>({
    workspaceName: "我的工作空间",
    ownerName: "",
    password: "",
    confirmPassword: "",
  });
  const [error, setError] = useState<string | null>(null);
  const [hostToken, setHostToken] = useState("");
  const [submitting, setSubmitting] = useState(false);
  // D2, docs/FREE_TIER_BOUNDARY_DESIGN.md §2.2: the documented default is
  // enabled, and the consequence is stated on the same screen rather than
  // folded into a help link.
  const [allowWebHostPaths, setAllowWebHostPaths] = useState(true);

  async function submit(event: FormEvent<HTMLFormElement>) {
    event.preventDefault();
    const validationError = validateSetupDraft(draft);
    if (validationError) {
      setError(validationError);
      return;
    }

    setSubmitting(true);
    setError(null);

    const input = buildSetupInput(
      draft,
      allowWebHostPaths,
      browserLocale(),
      browserTimezone(),
    );

    try {
      if (setup.hostClaimRequired) {
        await claimHostSetup(hostToken);
      }
      onComplete(await createSetup(input));
    } catch (submitError) {
      setError(
        submitError instanceof Error ? submitError.message : "无法完成首次设置",
      );
      setSubmitting(false);
    }
  }

  return (
    <main className="auth-shell">
      <section className="auth-card setup-card">
        <div className="auth-intro">
          <Brand />
          <p className="eyebrow">FIRST RUN</p>
          <h1>先建立你的本地工作空间</h1>
          <p>这一步只在本机创建数据库和 Owner。媒体文件不会被上传或移动。</p>
          <RuntimePill system={system} />
        </div>

        <form className="setup-form" onSubmit={submit}>
          {setup.hostClaimRequired ? (
            <Field
              label="初始化令牌"
              name="hostToken"
              type="password"
              value={hostToken}
              autoComplete="one-time-code"
              placeholder="从部署终端或配置文件中获取"
              onChange={setHostToken}
            />
          ) : null}
          <Field
            label="工作空间名称"
            name="workspaceName"
            value={draft.workspaceName}
            autoComplete="organization"
            onChange={(value) => {
              setDraft((current) => ({ ...current, workspaceName: value }));
            }}
          />
          <Field
            label="你的名字"
            name="ownerName"
            value={draft.ownerName}
            autoComplete="name"
            placeholder="用于评论和审阅记录"
            autoFocus
            onChange={(value) => {
              setDraft((current) => ({ ...current, ownerName: value }));
            }}
          />
          <Field
            label="本地访问密码"
            name="password"
            type="password"
            value={draft.password}
            autoComplete="new-password"
            placeholder="至少 10 个字符"
            onChange={(value) => {
              setDraft((current) => ({ ...current, password: value }));
            }}
          />
          <Field
            label="再次输入密码"
            name="confirmPassword"
            type="password"
            value={draft.confirmPassword}
            autoComplete="new-password"
            onChange={(value) => {
              setDraft((current) => ({
                ...current,
                confirmPassword: value,
              }));
            }}
          />

          <div className="team-registration-controls owner-network-controls setup-host-access">
            <label>
              <input
                type="checkbox"
                checked={allowWebHostPaths}
                onChange={(event) => setAllowWebHostPaths(event.target.checked)}
              />
              <span>
                <strong>允许 Owner 在网页端添加本机目录作为存储位置</strong>
                <small>
                  开启后，任何能登录 Owner
                  的账号都能读写本机上的任意目录。仅当你独占管理这台机器时才建议开启。
                </small>
              </span>
            </label>
          </div>
          <p className="form-note">
            这个开关只在首次设置时询问一次，之后不能在网页上修改；如需变更，请在主机上编辑配置文件。
          </p>

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
            {submitting ? "正在创建…" : "创建工作空间"}
          </button>
          <p className="form-note">
            密码使用 Argon2id 保存在本机，{PRODUCT_NAME_CN} 不保存明文。
          </p>
        </form>
      </section>
    </main>
  );
}

function LoginScreen({
  system,
  onComplete,
}: {
  system: SystemInfo;
  onComplete: (session: SessionInfo) => void;
}) {
  const [email, setEmail] = useState("");
  const [password, setPassword] = useState("");
  const [error, setError] = useState<string | null>(null);
  const [submitting, setSubmitting] = useState(false);

  async function submit(event: FormEvent<HTMLFormElement>) {
    event.preventDefault();
    if (!password) {
      setError("请输入本地访问密码");
      return;
    }

    setSubmitting(true);
    setError(null);
    try {
      onComplete(await login(email, password));
    } catch (loginError) {
      setError(
        loginError instanceof Error ? loginError.message : "无法完成登录",
      );
      setSubmitting(false);
    }
  }

  return (
    <main className="auth-shell">
      <section className="auth-card login-card">
        <Brand />
        <p className="eyebrow">WELCOME BACK</p>
        <h1>打开本地工作空间</h1>
        <p>使用成员邮箱和密码进入。</p>
        <RuntimePill system={system} />

        <form className="setup-form compact-form" onSubmit={submit}>
          <Field
            label="账号邮箱"
            detail="旧工作空间可留空，使用原访问密码登录。"
            name="email"
            value={email}
            autoComplete="email"
            autoFocus
            onChange={setEmail}
          />
          <Field
            label="密码"
            name="password"
            type="password"
            value={password}
            autoComplete="current-password"
            onChange={setPassword}
          />
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
            {submitting ? "正在验证…" : "进入工作空间"}
          </button>
        </form>
      </section>
    </main>
  );
}

function Field({
  label,
  detail,
  name,
  value,
  type = "text",
  placeholder,
  autoComplete,
  autoFocus = false,
  onChange,
}: {
  label: string;
  detail?: string;
  name: string;
  value: string;
  type?: "text" | "password";
  placeholder?: string;
  autoComplete?: string;
  autoFocus?: boolean;
  onChange: (value: string) => void;
}) {
  return (
    <label className="field">
      <span>{label}</span>
      <input
        name={name}
        type={type}
        value={value}
        placeholder={placeholder}
        autoComplete={autoComplete}
        autoFocus={autoFocus}
        onChange={(event) => {
          onChange(event.target.value);
        }}
      />
      {detail ? <small>{detail}</small> : null}
    </label>
  );
}

function Brand() {
  return (
    <div className="brand auth-brand">
      <span className="brand-mark" aria-hidden="true">
        <VistoMark className="visto-mark" />
      </span>
      <span>{PRODUCT_NAME_FULL}</span>
    </div>
  );
}

function RuntimePill({ system }: { system: SystemInfo }) {
  return (
    <div className="runtime-pill">
      <span className="status-pulse" aria-hidden="true" />
      {formatRuntimeLabel(system.mode, system.database)}
    </div>
  );
}
