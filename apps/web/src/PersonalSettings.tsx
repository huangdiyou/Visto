import { type FormEvent, type ReactNode, useEffect, useState } from "react";
import type {
  NotificationPreferences,
  SessionInfo,
} from "@review-studio/contracts";
import {
  BellRing,
  Check,
  CircleUserRound,
  Mail,
  MessageSquare,
  X,
} from "lucide-react";
import { updateProfile } from "./api/identity";
import {
  getNotificationPreferences,
  updateNotificationPreferences,
} from "./api/notifications";
import {
  createTranslator,
  localeLabel,
  normalizeLocale,
  supportedLocales,
  type AppLocale,
} from "./lib/i18n";
import { useI18n } from "./lib/i18n-react";

export type PersonalSettingsSection = "profile" | "preferences";

interface Feedback {
  kind: "success" | "error";
  message: string;
}

export function PersonalSettings({
  open,
  initialSection,
  session,
  onClose,
  onSessionUpdated,
}: {
  open: boolean;
  initialSection: PersonalSettingsSection;
  session: SessionInfo;
  onClose: () => void;
  onSessionUpdated: (session: SessionInfo) => void;
}) {
  const { locale, t, formatDate } = useI18n();
  const [section, setSection] =
    useState<PersonalSettingsSection>(initialSection);
  const [displayName, setDisplayName] = useState(session.user.displayName);
  const [selectedLocale, setSelectedLocale] = useState<AppLocale>(() =>
    normalizeLocale(session.user.locale),
  );
  const [preferences, setPreferences] =
    useState<NotificationPreferences | null>(null);
  const [loadingPreferences, setLoadingPreferences] = useState(false);
  const [savingProfile, setSavingProfile] = useState(false);
  const [savingPreferences, setSavingPreferences] = useState(false);
  const [profileFeedback, setProfileFeedback] = useState<Feedback | null>(null);
  const [preferenceFeedback, setPreferenceFeedback] = useState<Feedback | null>(
    null,
  );

  useEffect(() => {
    if (!open) return;
    setSection(initialSection);
    setDisplayName(session.user.displayName);
    setSelectedLocale(normalizeLocale(session.user.locale));
    setProfileFeedback(null);
    setPreferenceFeedback(null);
    const controller = new AbortController();
    setLoadingPreferences(true);
    void getNotificationPreferences(controller.signal)
      .then(setPreferences)
      .catch((error: unknown) => {
        if (!isAbort(error)) {
          setPreferenceFeedback({
            kind: "error",
            message: errorMessage(error, "无法读取接收偏好"),
          });
        }
      })
      .finally(() => setLoadingPreferences(false));
    return () => controller.abort();
  }, [initialSection, open]);

  useEffect(() => {
    if (!open) return;
    const handleKeyDown = (event: KeyboardEvent) => {
      if (event.key === "Escape") onClose();
    };
    window.addEventListener("keydown", handleKeyDown);
    return () => window.removeEventListener("keydown", handleKeyDown);
  }, [onClose, open]);

  async function saveProfile(event: FormEvent<HTMLFormElement>) {
    event.preventDefault();
    const value = displayName.trim();
    if (!value) {
      setProfileFeedback({
        kind: "error",
        message: t("profile.displayNameEmpty"),
      });
      return;
    }
    if (Array.from(value).length > 80) {
      setProfileFeedback({
        kind: "error",
        message: t("profile.displayNameTooLong"),
      });
      return;
    }
    setSavingProfile(true);
    setProfileFeedback(null);
    try {
      const updated = await updateProfile({
        displayName: value,
        locale: selectedLocale,
      });
      setDisplayName(updated.user.displayName);
      onSessionUpdated(updated);
      setProfileFeedback({
        kind: "success",
        message: createTranslator(updated.user.locale)("profile.saved"),
      });
    } catch (error) {
      setProfileFeedback({
        kind: "error",
        message: errorMessage(error, "无法保存个人资料"),
      });
    } finally {
      setSavingProfile(false);
    }
  }

  async function savePreferences() {
    if (!preferences) return;
    setSavingPreferences(true);
    setPreferenceFeedback(null);
    try {
      const updated = await updateNotificationPreferences({
        emailEnabled: preferences.emailEnabled,
        feishuEnabled: preferences.feishuEnabled,
        wechatWorkEnabled: preferences.wechatWorkEnabled,
      });
      setPreferences(updated);
      setPreferenceFeedback({
        kind: "success",
        message: t("profile.preferencesSaved"),
      });
    } catch (error) {
      setPreferenceFeedback({
        kind: "error",
        message: errorMessage(error, "无法保存接收偏好"),
      });
    } finally {
      setSavingPreferences(false);
    }
  }

  if (!open) return null;

  return (
    <div className="studio-personal-layer" role="presentation">
      <button
        className="studio-personal-scrim"
        type="button"
        aria-label={t("common.close")}
        onClick={onClose}
      />
      <aside
        className="studio-personal-panel"
        role="dialog"
        aria-modal="true"
        aria-labelledby="studio-personal-title"
      >
        <header className="studio-personal-header">
          <div>
            <span className="studio-notification-kicker">MY ACCOUNT</span>
            <h2 id="studio-personal-title">{t("profile.title")}</h2>
          </div>
          <button
            className="studio-icon-button"
            type="button"
            aria-label={t("common.close")}
            onClick={onClose}
          >
            <X size={18} />
          </button>
        </header>

        <nav className="studio-personal-nav" aria-label={t("profile.title")}>
          <button
            className={section === "profile" ? "is-active" : ""}
            type="button"
            onClick={() => setSection("profile")}
          >
            <CircleUserRound size={16} />
            {t("profile.profile")}
          </button>
          <button
            className={section === "preferences" ? "is-active" : ""}
            type="button"
            onClick={() => setSection("preferences")}
          >
            <BellRing size={16} />
            {t("profile.preferences")}
          </button>
        </nav>

        <div className="studio-personal-content">
          {section === "profile" ? (
            <>
              <div className="studio-personal-identity">
                <span className="studio-personal-avatar" aria-hidden="true">
                  {initials(session.user.displayName)}
                </span>
                <div>
                  <strong>{session.user.displayName}</strong>
                  <span>{session.user.email ?? t("profile.emailMissing")}</span>
                </div>
                <em>{roleLabel(session.role, t)}</em>
              </div>

              <dl className="studio-personal-facts">
                <div>
                  <dt>{t("profile.team")}</dt>
                  <dd>
                    {session.workspace.teamName || session.workspace.name}
                  </dd>
                </div>
                <div>
                  <dt>{t("profile.language")}</dt>
                  <dd>{localeLabel(session.user.locale, locale)}</dd>
                </div>
                <div>
                  <dt>{t("profile.validUntil")}</dt>
                  <dd>
                    {formatDate(session.expiresAt, {
                      year: "numeric",
                      month: "long",
                      day: "numeric",
                    })}
                  </dd>
                </div>
              </dl>

              <form
                className="studio-personal-form"
                onSubmit={(event) => void saveProfile(event)}
              >
                <div className="studio-personal-section-heading">
                  <span>{t("profile.identity")}</span>
                  <h3>{t("profile.displayName")}</h3>
                </div>
                <label>
                  <span>{t("profile.name")}</span>
                  <input
                    value={displayName}
                    autoComplete="name"
                    onChange={(event) => setDisplayName(event.target.value)}
                  />
                </label>
                <label>
                  <span>{t("profile.language")}</span>
                  <span className="studio-personal-select">
                    <select
                      value={selectedLocale}
                      onChange={(event) =>
                        setSelectedLocale(event.target.value as AppLocale)
                      }
                    >
                      {supportedLocales.map((item) => (
                        <option value={item} key={item}>
                          {localeLabel(item, locale)}
                        </option>
                      ))}
                    </select>
                  </span>
                </label>
                <p>{t("profile.usage")}</p>
                <p>{t("profile.languageHelp")}</p>
                {profileFeedback ? (
                  <PersonalFeedback feedback={profileFeedback} />
                ) : null}
                <button
                  className="primary-button"
                  type="submit"
                  disabled={
                    savingProfile ||
                    (displayName.trim() === session.user.displayName &&
                      selectedLocale === normalizeLocale(session.user.locale))
                  }
                >
                  {savingProfile ? t("profile.saving") : t("profile.save")}
                </button>
              </form>
            </>
          ) : (
            <div className="studio-personal-preferences">
              <div className="studio-personal-section-heading">
                <span>DELIVERY</span>
                <h3>{t("profile.preferences")}</h3>
                <p>以下设置只影响外部通知。</p>
              </div>
              {loadingPreferences ? (
                <PersonalLoading />
              ) : preferences ? (
                <div className="studio-preference-list">
                  <PreferenceRow
                    icon={<Mail size={18} />}
                    title="邮件"
                    detail={session.user.email ?? t("profile.emailMissing")}
                    checked={preferences.emailEnabled}
                    disabled={!session.user.email}
                    onChange={(checked) =>
                      setPreferences((current) =>
                        current
                          ? { ...current, emailEnabled: checked }
                          : current,
                      )
                    }
                  />
                  <PreferenceRow
                    icon={<BellRing size={18} />}
                    title="飞书"
                    detail="通过 Owner 已配置的飞书机器人接收"
                    checked={preferences.feishuEnabled}
                    onChange={(checked) =>
                      setPreferences((current) =>
                        current
                          ? { ...current, feishuEnabled: checked }
                          : current,
                      )
                    }
                  />
                  <PreferenceRow
                    icon={<MessageSquare size={18} />}
                    title="企业微信"
                    detail="通过 Owner 已配置的企业微信机器人接收"
                    checked={preferences.wechatWorkEnabled}
                    onChange={(checked) =>
                      setPreferences((current) =>
                        current
                          ? { ...current, wechatWorkEnabled: checked }
                          : current,
                      )
                    }
                  />
                </div>
              ) : (
                <div className="studio-personal-preference-empty">
                  {t("profile.preferencesEmpty")}
                </div>
              )}
              {preferenceFeedback ? (
                <PersonalFeedback feedback={preferenceFeedback} />
              ) : null}
              <button
                className="primary-button"
                type="button"
                disabled={
                  loadingPreferences || savingPreferences || !preferences
                }
                onClick={() => void savePreferences()}
              >
                {savingPreferences
                  ? t("profile.saving")
                  : t("profile.savePreferences")}
              </button>
            </div>
          )}
        </div>
      </aside>
    </div>
  );
}

function PreferenceRow({
  icon,
  title,
  detail,
  checked,
  disabled = false,
  onChange,
}: {
  icon: ReactNode;
  title: string;
  detail: string;
  checked: boolean;
  disabled?: boolean;
  onChange: (checked: boolean) => void;
}) {
  return (
    <label className={disabled ? "is-disabled" : ""}>
      <span className="studio-preference-icon" aria-hidden="true">
        {icon}
      </span>
      <span>
        <strong>{title}</strong>
        <small>{detail}</small>
      </span>
      <input
        type="checkbox"
        checked={checked}
        disabled={disabled}
        onChange={(event) => onChange(event.target.checked)}
      />
    </label>
  );
}

function PersonalFeedback({ feedback }: { feedback: Feedback }) {
  return (
    <div
      className={`studio-personal-feedback is-${feedback.kind}`}
      role={feedback.kind === "error" ? "alert" : "status"}
    >
      {feedback.kind === "success" ? <Check size={15} /> : null}
      <span>{feedback.message}</span>
    </div>
  );
}

function PersonalLoading() {
  const { t } = useI18n();
  return (
    <div className="studio-personal-loading" aria-label={t("common.loading")}>
      <span />
      <span />
      <span />
    </div>
  );
}

function initials(value: string) {
  return value.trim().slice(0, 2).toUpperCase() || "U";
}

function roleLabel(role: string, t: ReturnType<typeof useI18n>["t"]) {
  switch (role) {
    case "owner":
      return "Owner";
    case "admin":
      return t("role.admin");
    case "member":
      return t("role.member");
    case "guest":
      return t("role.guest");
    default:
      return role;
  }
}

function errorMessage(error: unknown, fallback: string) {
  return error instanceof Error ? error.message : fallback;
}

function isAbort(error: unknown) {
  return error instanceof DOMException && error.name === "AbortError";
}
