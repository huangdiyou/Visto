import { useEffect, useMemo, useState, type ReactNode } from "react";
import type {
  Project,
  ProjectOverview,
  ProjectOverviewFailedTask,
  ProjectOverviewFeedback,
  ProjectOverviewReview,
  ProjectOverviewVersion,
} from "@review-studio/contracts";
import {
  AlertTriangle,
  ArrowRight,
  CheckCircle2,
  Clock3,
  Film,
  MessageSquareText,
  RefreshCcw,
  Upload,
} from "lucide-react";
import { getProjectOverview } from "./api/catalog";
import { ApiError } from "./api/client";
import {
  formatOverviewLocation,
  getProjectPulse,
} from "./lib/project-overview";
import { useI18n } from "./lib/i18n-react";
import type { Translator } from "./lib/i18n";

interface ProjectOverviewProps {
  project: Project;
  onOpenReview: (reviewId: string, threadId?: string) => void;
  onOpenReviews: () => void;
  onOpenMedia: (assetId?: string) => void;
}

export function ProjectOverviewPanel({
  project,
  onOpenReview,
  onOpenReviews,
  onOpenMedia,
}: ProjectOverviewProps) {
  const { t, formatDateTime } = useI18n();
  const [overview, setOverview] = useState<ProjectOverview | null>(null);
  const [loading, setLoading] = useState(true);
  const [error, setError] = useState<string | null>(null);
  const [forbidden, setForbidden] = useState(false);
  const [reloadVersion, setReloadVersion] = useState(0);

  useEffect(() => {
    const controller = new AbortController();
    setLoading(true);
    setError(null);
    setForbidden(false);
    getProjectOverview(project.id, controller.signal)
      .then(setOverview)
      .catch((loadError: unknown) => {
        if (
          loadError instanceof DOMException &&
          loadError.name === "AbortError"
        ) {
          return;
        }
        if (loadError instanceof ApiError && loadError.status === 403) {
          setForbidden(true);
          setOverview(null);
          return;
        }
        setError(
          loadError instanceof Error ? loadError.message : "无法读取项目概览",
        );
        setOverview(null);
      })
      .finally(() => setLoading(false));
    return () => controller.abort();
  }, [project.id, reloadVersion]);

  const pulse = useMemo(
    () => (overview ? getProjectPulse(overview, t) : null),
    [overview, t],
  );
  const firstFailedAssetId = overview?.failedTasks.items[0]?.assetId;

  const retry = () => setReloadVersion((current) => current + 1);

  return (
    <section className="studio-work-section studio-project-overview">
      <div className="studio-section-heading studio-overview-heading">
        <div>
          <p className="studio-kicker">PROJECT PULSE</p>
          <h2>{t("projectOverview.title")}</h2>
        </div>
        {overview ? (
          <time dateTime={overview.generatedAt}>
            {t("common.updatedAt", {
              date: formatDateTime(overview.generatedAt),
            })}
          </time>
        ) : null}
      </div>

      <div
        className={`studio-pulse-summary${pulse ? ` is-${pulse.tone}` : ""}`}
      >
        <span className="studio-pulse-rail" aria-hidden="true" />
        <span className="studio-pulse-icon" aria-hidden="true">
          {pulse?.tone === "danger" ? (
            <AlertTriangle size={19} />
          ) : pulse?.tone === "attention" ? (
            <Clock3 size={19} />
          ) : (
            <CheckCircle2 size={19} />
          )}
        </span>
        <div>
          <span>{t("projectOverview.priority")}</span>
          {loading ? (
            <strong>{t("projectOverview.loadingTitle")}</strong>
          ) : forbidden ? (
            <strong>{t("projectOverview.forbiddenTitle")}</strong>
          ) : error ? (
            <strong>{t("projectOverview.unavailableTitle")}</strong>
          ) : (
            <strong>{pulse?.title}</strong>
          )}
          <p>
            {loading
              ? t("projectOverview.loadingDetail")
              : forbidden
                ? t("projectOverview.forbiddenDetail")
                : error
                  ? error
                  : pulse?.detail}
          </p>
        </div>
        {error ? (
          <button className="secondary-button" type="button" onClick={retry}>
            <RefreshCcw size={15} />
            {t("projectOverview.reload")}
          </button>
        ) : pulse ? (
          <button
            className="secondary-button"
            type="button"
            onClick={() =>
              pulse.target === "media"
                ? onOpenMedia(
                    pulse.tone === "danger" ? firstFailedAssetId : undefined,
                  )
                : onOpenReviews()
            }
          >
            {pulse.actionLabel}
            <ArrowRight size={15} />
          </button>
        ) : null}
      </div>

      <div className="studio-signal-board">
        <SignalSection
          icon={<Film size={17} />}
          title={t("projectOverview.reviews")}
          total={overview?.reviews.total ?? 0}
          loading={loading}
          error={error}
          forbidden={forbidden}
          emptyText={t("projectOverview.emptyReviews")}
          onRetry={retry}
          onOpenAll={onOpenReviews}
        >
          {overview?.reviews.items.map((item) => (
            <ReviewSignal
              item={item}
              key={item.id}
              onOpen={() => onOpenReview(item.id)}
            />
          ))}
        </SignalSection>

        <SignalSection
          icon={<MessageSquareText size={17} />}
          title={t("projectOverview.feedback")}
          total={overview?.openFeedback.total ?? 0}
          loading={loading}
          error={error}
          forbidden={forbidden}
          emptyText={t("projectOverview.emptyFeedback")}
          onRetry={retry}
          onOpenAll={onOpenReviews}
        >
          {overview?.openFeedback.items.map((item) => (
            <FeedbackSignal
              item={item}
              key={item.id}
              onOpen={() => onOpenReview(item.reviewId, item.id)}
            />
          ))}
        </SignalSection>

        <SignalSection
          icon={<Upload size={17} />}
          title={t("projectOverview.recentVersions")}
          total={overview?.recentVersions.total ?? 0}
          loading={loading}
          error={error}
          forbidden={forbidden}
          emptyText={t("projectOverview.emptyRecentVersions")}
          onRetry={retry}
          onOpenAll={() => onOpenMedia()}
        >
          {overview?.recentVersions.items.map((item) => (
            <VersionSignal
              item={item}
              key={item.id}
              onOpen={() => onOpenMedia(item.assetId)}
            />
          ))}
        </SignalSection>

        <SignalSection
          icon={<AlertTriangle size={17} />}
          title={t("projectOverview.failedTasks")}
          total={overview?.failedTasks.total ?? 0}
          loading={loading}
          error={error}
          forbidden={forbidden}
          emptyText={t("projectOverview.emptyFailedTasks")}
          onRetry={retry}
          onOpenAll={() => onOpenMedia()}
        >
          {overview?.failedTasks.items.map((item) => (
            <FailedTaskSignal
              item={item}
              key={item.id}
              onOpen={() => onOpenMedia(item.assetId)}
            />
          ))}
        </SignalSection>
      </div>
    </section>
  );
}

function SignalSection({
  icon,
  title,
  total,
  loading,
  error,
  forbidden,
  emptyText,
  onRetry,
  onOpenAll,
  children,
}: {
  icon: ReactNode;
  title: string;
  total: number;
  loading: boolean;
  error: string | null;
  forbidden: boolean;
  emptyText: string;
  onRetry: () => void;
  onOpenAll: () => void;
  children: ReactNode;
}) {
  const { t } = useI18n();
  return (
    <section className="studio-signal-section">
      <header>
        <span className="studio-signal-title">
          {icon}
          <strong>{title}</strong>
          {!loading && !error && !forbidden ? <em>{total}</em> : null}
        </span>
        {!loading && !error && !forbidden && total > 0 ? (
          <button
            type="button"
            aria-label={`${title}, view all`}
            onClick={onOpenAll}
          >
            {t("common.viewAll")}
            <ArrowRight size={14} />
          </button>
        ) : null}
      </header>
      <div className="studio-signal-list">
        {loading ? (
          <SignalLoading />
        ) : forbidden ? (
          <SignalState
            title={t("projectOverview.forbiddenTitle")}
            detail={t("projectOverview.forbiddenDetail")}
          />
        ) : error ? (
          <SignalState
            title={t("projectOverview.readFailedTitle")}
            detail={error}
            action={
              <button type="button" onClick={onRetry}>
                {t("common.retry")}
              </button>
            }
          />
        ) : total === 0 ? (
          <SignalState title={emptyText} />
        ) : (
          children
        )}
      </div>
    </section>
  );
}

function SignalLoading() {
  return (
    <div className="studio-signal-loading" aria-label="正在加载">
      <span />
      <span />
      <span />
    </div>
  );
}

function SignalState({
  title,
  detail,
  action,
}: {
  title: string;
  detail?: string;
  action?: ReactNode;
}) {
  return (
    <div className="studio-signal-state">
      <strong>{title}</strong>
      {detail ? <span>{detail}</span> : null}
      {action}
    </div>
  );
}

function ReviewSignal({
  item,
  onOpen,
}: {
  item: ProjectOverviewReview;
  onOpen: () => void;
}) {
  const { t, formatDate } = useI18n();
  return (
    <button className="studio-signal-row" type="button" onClick={onOpen}>
      <span className={`studio-signal-status is-${item.status}`}>
        {reviewStatusLabel(item.status, t)}
      </span>
      <span className="studio-signal-copy">
        <strong>{item.name}</strong>
        <small>
          {item.assetName
            ? `${item.assetName} V${item.versionNumber}`
            : t("projectOverview.fixedVersions", { count: item.itemCount })}
          {" · "}
          {item.responsibleName || t("projectOverview.noResponsible")}
        </small>
      </span>
      <span className="studio-signal-meta">
        {item.openFeedbackCount > 0
          ? t("projectOverview.feedbackCount", {
              count: item.openFeedbackCount,
            })
          : item.dueAt
            ? t("projectOverview.dueAt", {
                date: formatDate(item.dueAt, {
                  month: "numeric",
                  day: "numeric",
                }),
              })
            : t("projectOverview.noDueDate")}
      </span>
    </button>
  );
}

function FeedbackSignal({
  item,
  onOpen,
}: {
  item: ProjectOverviewFeedback;
  onOpen: () => void;
}) {
  const { t, formatDateTime } = useI18n();
  return (
    <button className="studio-signal-row" type="button" onClick={onOpen}>
      <span className="studio-signal-status is-feedback">
        {formatOverviewLocation(
          item.annotationKind,
          item.timeStartUs,
          item.timeEndUs,
          t,
        )}
      </span>
      <span className="studio-signal-copy">
        <strong>{item.body || t("projectOverview.commentDeleted")}</strong>
        <small>
          {item.reviewName} · {item.assetName} V{item.versionNumber}
        </small>
      </span>
      <span className="studio-signal-meta">
        {item.authorName} · {formatDateTime(item.updatedAt)}
      </span>
    </button>
  );
}

function VersionSignal({
  item,
  onOpen,
}: {
  item: ProjectOverviewVersion;
  onOpen: () => void;
}) {
  const { t, formatDateTime } = useI18n();
  return (
    <button className="studio-signal-row" type="button" onClick={onOpen}>
      <span className={`studio-signal-status is-${item.processingStatus}`}>
        V{item.versionNumber}
      </span>
      <span className="studio-signal-copy">
        <strong>{item.assetName}</strong>
        <small>
          {item.sourceFilename} ·{" "}
          {processingStatusLabel(item.processingStatus, t)}
        </small>
      </span>
      <span className="studio-signal-meta">
        {item.actorName} · {formatDateTime(item.createdAt)}
      </span>
    </button>
  );
}

function FailedTaskSignal({
  item,
  onOpen,
}: {
  item: ProjectOverviewFailedTask;
  onOpen: () => void;
}) {
  const { t, formatDateTime } = useI18n();
  return (
    <button
      className="studio-signal-row is-danger"
      type="button"
      onClick={onOpen}
    >
      <span className="studio-signal-status is-failed">
        {t("projectOverview.failed")}
      </span>
      <span className="studio-signal-copy">
        <strong>{item.assetName}</strong>
        <small>
          {jobTypeLabel(item.type, t)} · {failedTaskImpact(item.type, t)}
        </small>
        <small>
          {item.errorCode || "job.execution_failed"} ·{" "}
          {item.errorMessage || "处理任务没有完成"}
        </small>
      </span>
      <span className="studio-signal-meta">
        {formatDateTime(item.updatedAt)}
      </span>
    </button>
  );
}

function reviewStatusLabel(status: string, t: Translator) {
  if (status === "changes_requested") {
    return t("projectOverview.statusChangesRequested");
  }
  if (status === "open") return t("projectOverview.statusOpen");
  if (status === "draft") return t("projectOverview.statusDraft");
  if (status === "approved") return t("projectOverview.statusApproved");
  return t("projectOverview.statusClosed");
}

function processingStatusLabel(status: string, t: Translator) {
  if (status === "ready") return t("projectOverview.processingReady");
  if (status === "processing") {
    return t("projectOverview.processingProcessing");
  }
  if (status === "pending") return t("projectOverview.processingPending");
  if (status === "partial") return t("projectOverview.processingPartial");
  return t("projectOverview.processingFailed");
}

function jobTypeLabel(type: string, t: Translator) {
  if (type === "media.probe_object") return t("projectOverview.jobMediaProbe");
  if (type === "media.generate_image_renditions") {
    return t("projectOverview.jobImagePreview");
  }
  if (type === "media.generate_video_renditions") {
    return t("projectOverview.jobVideoPreview");
  }
  if (type === "media.generate_video_enhancements") {
    return t("projectOverview.jobVideoEnhancement");
  }
  return type.replaceAll("_", " ").replaceAll(".", " / ");
}

function failedTaskImpact(type: string, t: Translator) {
  if (type === "media.generate_video_enhancements") {
    return t("projectOverview.impactTimeline");
  }
  if (
    type === "media.generate_video_renditions" ||
    type === "media.process_asset_version"
  ) {
    return t("projectOverview.impactReviewPlayback");
  }
  if (type === "media.generate_image_renditions") {
    return t("projectOverview.impactImagePreview");
  }
  if (type === "storage.copy_object") {
    return t("projectOverview.impactCopyDelivery");
  }
  return t("projectOverview.impactUnknown");
}
