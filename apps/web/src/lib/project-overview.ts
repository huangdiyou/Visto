import type { ProjectOverview } from "@review-studio/contracts";
import { createTranslator, type Translator } from "./i18n";

export interface ProjectPulse {
  tone: "danger" | "attention" | "steady";
  title: string;
  detail: string;
  actionLabel: string;
  target: "media" | "reviews";
}

const zh = createTranslator("zh-CN");

export function getProjectPulse(
  overview: ProjectOverview,
  t: Translator = zh,
): ProjectPulse {
  if (overview.failedTasks.total > 0) {
    return {
      tone: "danger",
      title: t("projectOverview.pulseFailedTitle", {
        count: overview.failedTasks.total,
      }),
      detail: t("projectOverview.pulseFailedDetail"),
      actionLabel: t("projectOverview.actionOpenFailedTasks"),
      target: "media",
    };
  }
  if (overview.openFeedback.total > 0) {
    return {
      tone: "attention",
      title: t("projectOverview.pulseFeedbackTitle", {
        count: overview.openFeedback.total,
      }),
      detail: t("projectOverview.pulseFeedbackDetail"),
      actionLabel: t("projectOverview.actionHandleFeedback"),
      target: "reviews",
    };
  }
  if (overview.reviews.total > 0) {
    return {
      tone: "attention",
      title: t("projectOverview.pulseReviewsTitle", {
        count: overview.reviews.total,
      }),
      detail: t("projectOverview.pulseReviewsDetail"),
      actionLabel: t("projectOverview.actionOpenReviews"),
      target: "reviews",
    };
  }
  if (overview.recentVersions.total > 0) {
    return {
      tone: "steady",
      title: t("projectOverview.pulseReadyTitle"),
      detail: t("projectOverview.pulseReadyDetail"),
      actionLabel: t("projectOverview.actionOpenMedia"),
      target: "media",
    };
  }
  return {
    tone: "steady",
    title: t("projectOverview.pulseStartTitle"),
    detail: t("projectOverview.pulseStartDetail"),
    actionLabel: t("projectOverview.actionAddMedia"),
    target: "media",
  };
}

export function formatOverviewLocation(
  kind: string,
  timeStartUs: number | null,
  timeEndUs: number | null,
  t: Translator = zh,
): string {
  if (kind === "time_point" && timeStartUs !== null) {
    return formatTimecode(timeStartUs);
  }
  if (kind === "time_range" && timeStartUs !== null && timeEndUs !== null) {
    return `${formatTimecode(timeStartUs)} - ${formatTimecode(timeEndUs)}`;
  }
  if (kind === "point") return t("projectOverview.locationPoint");
  if (kind === "region") return t("projectOverview.locationRegion");
  if (kind === "drawing") return t("projectOverview.locationDrawing");
  if (kind === "page_region") return t("projectOverview.locationPageRegion");
  return t("projectOverview.locationWhole");
}

function formatTimecode(microseconds: number): string {
  const totalSeconds = Math.max(0, Math.floor(microseconds / 1_000_000));
  const minutes = Math.floor(totalSeconds / 60);
  const seconds = totalSeconds % 60;
  return `${minutes}:${seconds.toString().padStart(2, "0")}`;
}
