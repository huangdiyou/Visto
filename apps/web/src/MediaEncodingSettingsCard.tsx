import { useEffect, useRef, useState } from "react";
import type { SystemMediaEncodingSettings } from "@review-studio/contracts";
import { RefreshCw } from "lucide-react";
import { ApiError } from "./api/client";
import {
  getMediaEncodingSettings,
  reprobeMediaEncodingSettings,
  updateMediaEncodingSettings,
} from "./api/system";
import {
  mediaEncodingOptions,
  mediaEncodingReprobeErrorMessage,
  mediaEncodingSaveErrorMessage,
  mediaEncodingViewState,
  mediaEncoderLabel,
} from "./lib/media-encoding";
import { useI18n } from "./lib/i18n-react";

type FeedbackTone = "success" | "error" | "info";

interface FeedbackState {
  tone: FeedbackTone;
  message: string;
}

// A sweep test-encodes every candidate, so the page waits rather than polls
// tightly; three minutes covers a cold hardware probe on a slow machine.
const probePollIntervalMs = 2000;
const probePollAttempts = 90;

export function MediaEncodingSettingsCard() {
  const { t, formatDateTime } = useI18n();
  const [settings, setSettings] = useState<SystemMediaEncodingSettings | null>(
    null,
  );
  const [preferredEncoder, setPreferredEncoder] = useState("");
  const [loading, setLoading] = useState(true);
  const [busy, setBusy] = useState(false);
  const [probing, setProbing] = useState(false);
  const [feedback, setFeedback] = useState<FeedbackState | null>(null);
  // The sweep outlives the request, so the poll has to stop when the card goes
  // away instead of writing state into an unmounted component.
  const cancelled = useRef(false);

  function applyLoaded(loaded: SystemMediaEncodingSettings) {
    setSettings(loaded);
    setPreferredEncoder(loaded.preferredEncoder);
  }

  function load(signal?: AbortSignal) {
    return getMediaEncodingSettings(signal)
      .then(applyLoaded)
      .catch((error: unknown) => {
        if (!isAbort(error)) {
          setFeedback({
            tone: "error",
            message:
              error instanceof Error
                ? error.message
                : t("owner.encodingLoadFailed"),
          });
        }
      });
  }

  useEffect(() => {
    const controller = new AbortController();
    cancelled.current = false;
    void load(controller.signal).finally(() => setLoading(false));
    return () => {
      cancelled.current = true;
      controller.abort();
    };
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [t]);

  async function save() {
    if (!settings) {
      return;
    }
    setBusy(true);
    setFeedback(null);
    try {
      const updated = await updateMediaEncodingSettings({
        preferredEncoder,
        revision: settings.revision,
      });
      applyLoaded(updated);
      setFeedback({ tone: "success", message: t("owner.encodingSaved") });
    } catch (error) {
      setFeedback({
        tone: "error",
        message: mediaEncodingSaveErrorMessage(error, t),
      });
      if (
        error instanceof ApiError &&
        error.code === "system_settings.revision_conflict"
      ) {
        // Pull the winning revision so the next save is not a guess.
        await load();
      }
    } finally {
      setBusy(false);
    }
  }

  async function reprobe() {
    if (!settings) {
      return;
    }
    setProbing(true);
    setFeedback(null);
    const before = settings.detectedAt;
    try {
      await reprobeMediaEncodingSettings();
      setFeedback({
        tone: "info",
        message: t("owner.encodingReprobeStarted"),
      });
      for (let attempt = 0; attempt < probePollAttempts; attempt += 1) {
        await new Promise((resolve) =>
          setTimeout(resolve, probePollIntervalMs),
        );
        if (cancelled.current) {
          return;
        }
        const loaded = await getMediaEncodingSettings();
        if (cancelled.current) {
          return;
        }
        if (loaded.detectedAt !== before) {
          applyLoaded(loaded);
          setFeedback(null);
          return;
        }
      }
      // The sweep is still running; leave the "started" note up rather than
      // claiming a result the page never saw.
    } catch (error) {
      setFeedback({
        tone: "error",
        message: mediaEncodingReprobeErrorMessage(error, t),
      });
    } finally {
      setProbing(false);
    }
  }

  const view = mediaEncodingViewState(settings);
  const options = mediaEncodingOptions(settings);

  return (
    <div className="owner-encoding-settings">
      <section className="team-registration-section owner-network-card">
        <div className="section-heading">
          <div>
            <p className="eyebrow">{t("owner.encodingEyebrow")}</p>
            <h2>{t("owner.encodingCardTitle")}</h2>
            <span>{t("owner.encodingCardDescription")}</span>
          </div>
          <div className="owner-encoding-actions">
            <button
              className="ghost-button"
              type="button"
              disabled={probing || busy || loading}
              onClick={() => void reprobe()}
            >
              <RefreshCw size={15} />
              {probing
                ? t("owner.encodingReprobing")
                : t("owner.encodingReprobe")}
            </button>
            <button
              className="primary-button"
              type="button"
              disabled={busy || loading || !settings}
              onClick={() => void save()}
            >
              {busy ? t("owner.encodingSaving") : t("owner.encodingSave")}
            </button>
          </div>
        </div>

        {loading ? (
          <div
            className="activity-loading"
            aria-label={t("owner.encodingLoading")}
            aria-live="polite"
          >
            <span />
            <span />
            <span />
          </div>
        ) : settings ? (
          <>
            <div className="owner-encoding-field">
              <label htmlFor="owner-media-encoder">
                {t("owner.encodingSelectLabel")}
              </label>
              <select
                id="owner-media-encoder"
                value={preferredEncoder}
                disabled={busy}
                onChange={(event) => setPreferredEncoder(event.target.value)}
              >
                <option value="">{t("owner.encodingAutomatic")}</option>
                {options.map((encoder) => (
                  <option value={encoder} key={encoder}>
                    {mediaEncoderLabel(encoder)}
                  </option>
                ))}
              </select>
              <p className="owner-network-hint">{view.summary}</p>
            </div>

            {view.tripped ? (
              <p className="owner-network-notice is-warning" role="status">
                <strong>{t("owner.encodingTripped")}</strong>{" "}
                {t("owner.encodingTrippedDetail", {
                  encoder: mediaEncoderLabel(settings.trippedEncoder ?? ""),
                  effective: view.effectiveLabel,
                })}
              </p>
            ) : null}

            <div className="owner-network-status">
              <span>{t("owner.encodingEffective")}</span>
              <strong>{view.effectiveLabel}</strong>
            </div>

            <div className="owner-network-status">
              <span>{t("owner.encodingProbeTime")}</span>
              <strong>
                {settings.detectedAt
                  ? formatDateTime(settings.detectedAt)
                  : t("owner.encodingNotProbed")}
              </strong>
            </div>

            {view.showFailureCount ? (
              <p className="owner-network-notice is-warning" role="status">
                {t("owner.encodingFailureCount", {
                  count: settings.failureCount,
                })}
              </p>
            ) : null}

            <p className="owner-network-hint">{t("owner.encodingHint")}</p>
          </>
        ) : null}

        {feedback ? (
          <p
            className={`team-notice is-${feedback.tone}`}
            role={feedback.tone === "error" ? "alert" : "status"}
          >
            {feedback.message}
          </p>
        ) : null}
      </section>
    </div>
  );
}

function isAbort(error: unknown) {
  return error instanceof DOMException && error.name === "AbortError";
}
