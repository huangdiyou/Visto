import type { SystemMediaEncodingSettings } from "@review-studio/contracts";
import { ApiError } from "../api/client";
import type { Translator } from "./i18n";

/**
 * Display names for the encoders Core can select. An unknown name is shown as-is
 * rather than hidden: a stored value the page cannot label is exactly the case an
 * Owner needs to see, not one to silently drop.
 */
const encoderLabels: Record<string, string> = {
  h264_videotoolbox: "VideoToolbox（macOS 硬件）",
  h264_nvenc: "NVENC（NVIDIA 硬件）",
  h264_qsv: "Quick Sync（Intel 硬件）",
  h264_amf: "AMF（AMD 硬件）",
  h264_vaapi: "VAAPI（Linux 硬件）",
  libopenh264: "OpenH264（软件）",
  libx264: "x264（软件）",
};

export function mediaEncoderLabel(encoder: string): string {
  return encoderLabels[encoder] ?? encoder;
}

/**
 * The states the encoding card can be in. Pure so the priority order and the
 * breaker rules are testable without a DOM.
 */
export interface MediaEncodingViewState {
  /** True when the instance follows the probe rather than an explicit choice. */
  automatic: boolean;
  /** True while the breaker has moved the instance off an encoder. */
  tripped: boolean;
  /** True when nothing has been probed yet, so the list is unknown. */
  notProbed: boolean;
  /** The encoder in effect, already human-readable. */
  effectiveLabel: string;
  /** The line the card shows under the selector. */
  summary: string;
  /** False while the failure count cannot be explained yet. */
  showFailureCount: boolean;
}

export function mediaEncodingViewState(
  settings: SystemMediaEncodingSettings | null,
): MediaEncodingViewState {
  if (!settings) {
    return {
      automatic: true,
      tripped: false,
      notProbed: true,
      effectiveLabel: "",
      summary: "",
      showFailureCount: false,
    };
  }
  const automatic = settings.preferredEncoder === "";
  const tripped = settings.trippedEncoder !== null;
  const notProbed = settings.detectedAt === null;
  const effectiveLabel = mediaEncoderLabel(settings.effectiveEncoder);
  // A trip is the only state that has to explain itself: the Owner did not ask
  // for this encoder, so the card says which one failed and why it changed.
  const summary = tripped
    ? `已自动切换：${mediaEncoderLabel(settings.trippedEncoder ?? "")} 连续失败，现使用 ${effectiveLabel}`
    : automatic
      ? `自动选择，当前使用 ${effectiveLabel}`
      : `已指定 ${effectiveLabel}`;
  return {
    automatic,
    tripped,
    notProbed,
    effectiveLabel,
    summary,
    // The count only means something while an encoder is on its way to tripping.
    showFailureCount: !tripped && settings.failureCount > 0,
  };
}

/**
 * The options the selector offers: automatic first, then everything the probe
 * found. A stored choice that the probe did not report is kept in the list, so
 * saving the form again cannot silently drop it.
 */
export function mediaEncodingOptions(
  settings: SystemMediaEncodingSettings | null,
): string[] {
  if (!settings) {
    return [];
  }
  const detected = settings.detectedEncoders ?? [];
  const options = detected.filter((encoder) => encoder !== "h264_vaapi");
  const stored = settings.preferredEncoder;
  if (stored !== "" && stored !== "h264_vaapi" && !options.includes(stored)) {
    options.push(stored);
  }
  return options;
}

/**
 * Turns a save failure into the line the card shows. The revision conflict is
 * called out by name because the page refreshes itself in that case, so the
 * Owner needs to know the retry is safe rather than that the save was lost.
 */
export function mediaEncodingSaveErrorMessage(
  error: unknown,
  t: Translator,
): string {
  if (error instanceof ApiError) {
    if (error.code === "system_settings.encoder_apply_failed") {
      return "设置已保存，但编码器未能生效。请检查运行时并重新选择。";
    }
    if (error.code === "system_settings.revision_conflict") {
      return t("owner.encodingRevisionConflict");
    }
    return error.message;
  }
  return error instanceof Error ? error.message : t("owner.encodingSaveFailed");
}

/**
 * The re-probe has two refusals the Owner can act on: a sweep is already
 * running, or this build cannot probe at all. Everything else is reported as-is.
 */
export function mediaEncodingReprobeErrorMessage(
  error: unknown,
  t: Translator,
): string {
  if (error instanceof ApiError) {
    if (error.code === "system_settings.probe_in_progress") {
      return t("owner.encodingReprobeConflict");
    }
    if (error.code === "system_settings.probe_unavailable") {
      return t("owner.encodingReprobeUnavailable");
    }
    return error.message;
  }
  return error instanceof Error
    ? error.message
    : t("owner.encodingReprobeFailed");
}
