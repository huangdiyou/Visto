import type { CommentThread, PublicShare } from "@review-studio/contracts";

export function secondsToUs(value: number) {
  if (!Number.isFinite(value) || value < 0) {
    return 0;
  }
  return Math.round(value * 1_000_000);
}

export function formatAnnotationDraft(
  mode: "time_point" | "time_range",
  startUs: number | null,
  endUs: number | null,
) {
  if (startUs === null) {
    return mode === "time_point" ? "尚未抓取" : "尚未设置区间";
  }
  if (mode === "time_point") {
    return formatMediaTime(startUs);
  }
  return `${formatMediaTime(startUs)} - ${
    endUs === null ? "未设置" : formatMediaTime(endUs)
  }`;
}

export function formatThreadTime(annotation: CommentThread["annotation"]) {
  const startUs = annotation.timeStartUs ?? 0;
  if (annotation.kind === "time_range" && annotation.timeEndUs !== null) {
    return `${formatMediaTime(startUs)} - ${formatMediaTime(
      annotation.timeEndUs,
    )}`;
  }
  return formatMediaTime(startUs);
}

export function formatMediaTime(valueUs: number) {
  const totalMilliseconds = Math.max(0, Math.round(valueUs / 1000));
  const hours = Math.floor(totalMilliseconds / 3_600_000);
  const minutes = Math.floor((totalMilliseconds % 3_600_000) / 60_000);
  const seconds = Math.floor((totalMilliseconds % 60_000) / 1000);
  const milliseconds = totalMilliseconds % 1000;
  const prefix = hours > 0 ? `${String(hours).padStart(2, "0")}:` : "";
  return `${prefix}${String(minutes).padStart(2, "0")}:${String(
    seconds,
  ).padStart(2, "0")}.${String(milliseconds).padStart(3, "0")}`;
}

export function publicReviewPermissionLabel(
  reviewStatus: PublicShare["reviewStatus"],
  allowComment: boolean,
) {
  if (reviewStatus === "closed") {
    return "审阅已结束";
  }
  if (reviewStatus === "draft") {
    return "审阅未开启";
  }
  return allowComment ? "可评论" : "只读";
}

export function publicCommentReadonlyMessage(
  reviewStatus: PublicShare["reviewStatus"],
) {
  if (reviewStatus === "closed") {
    return "这轮审阅已经结束，已有评论仍可查看。";
  }
  if (reviewStatus === "draft") {
    return "这轮审阅尚未开启，暂时不能提交评论。";
  }
  return "这个分享当前为只读审阅。";
}
