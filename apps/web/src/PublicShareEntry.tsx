import {
  type CSSProperties,
  type FormEvent,
  type PointerEvent as ReactPointerEvent,
  type RefObject,
  useEffect,
  useRef,
  useState,
} from "react";
import type {
  AnnotationGeometry,
  CommentThread,
  DrawingAnnotationGeometry,
  DrawingElement,
  IdentifyPublicShareInput,
  PublicShare,
  PublicShareItem,
  ReviewDecisionValue,
} from "@review-studio/contracts";
import {
  addPublicThreadComment,
  createPublicDecision,
  createPublicThread,
  deletePublicComment,
  getPublicShare,
  identifyPublicShare,
  listPublicThreads,
  openPublicShare,
  updatePublicComment,
  uploadPublicCommentAttachment,
  verifyPublicShare,
} from "./api/shares";
import { VistoMark, PRODUCT_NAME_FULL } from "./brand";
import { HlsVideo } from "./HlsVideo";
import {
  formatAnnotationDraft,
  formatMediaTime,
  formatThreadTime,
  publicCommentReadonlyMessage,
  publicReviewPermissionLabel,
  secondsToUs,
} from "./lib/review-comments";
import {
  appendDrawingElement,
  createArrowDrawingElement,
  createBrushDrawingElement,
  createDrawingRectElement,
  createPointGeometry,
  createRegionGeometry,
  formatImageGeometry,
  normalizePointerPosition,
  removeLastDrawingElement,
  type NormalizedPoint,
} from "./lib/image-annotations";
import { buildPublicShareTitle, setDocumentTitle } from "./lib/page-title";
import {
  attachmentIds,
  CommentAttachmentField,
  CommentAttachmentGallery,
  type CommentAttachmentDraft,
} from "./CommentAttachments";

type PublicState =
  | { status: "opening" }
  | { status: "password" }
  | { status: "ready"; share: PublicShare }
  | { status: "error"; message: string };

type ImageAnnotationMode =
  | "point"
  | "region"
  | "brush"
  | "arrow"
  | "drawing_rect";

const defaultDrawingColor = "#f6cf5a";
const drawingColors = [
  defaultDrawingColor,
  "#ff5c7a",
  "#54d6ff",
  "#64d081",
] as const;
const drawingStrokeWidth = 4;
const publicDecisionOptions: Array<{
  value: ReviewDecisionValue;
  label: string;
}> = [
  { value: "approved", label: "通过" },
  { value: "changes_requested", label: "需修改" },
  { value: "rejected", label: "拒绝" },
];
const publicPlaybackRates = [0.5, 0.75, 1, 1.25, 1.5, 2] as const;
type PublicPlaybackRate = (typeof publicPlaybackRates)[number];
const publicPlaybackRateStorageKey = "visto:public-review-playback-rate";

export function PublicShareEntry() {
  const [state, setState] = useState<PublicState>({ status: "opening" });
  const opened = useRef(false);

  useEffect(() => {
    if (opened.current) {
      return;
    }
    opened.current = true;
    const fragmentToken = window.location.hash.replace(/^#/, "");
    const pathToken =
      window.location.pathname.replace(/^\/s\//, "").split("/")[0] ?? "";
    const token = decodeURIComponent(fragmentToken || pathToken);
    if (token === "active") {
      getPublicShare()
        .then((share) => setState({ status: "ready", share }))
        .catch((error: unknown) =>
          setState({
            status: "error",
            message: error instanceof Error ? error.message : "分享暂时不可用",
          }),
        );
      return;
    }
    if (!token) {
      setState({ status: "error", message: "分享地址无效或已失效" });
      return;
    }
    openPublicShare(token)
      .then((entry) => {
        window.history.replaceState(null, "", "/s/active");
        if (entry.status === "password_required") {
          setState({ status: "password" });
        } else if (entry.share) {
          setState({ status: "ready", share: entry.share });
        } else {
          setState({ status: "error", message: "无法读取分享内容" });
        }
      })
      .catch((error: unknown) =>
        setState({
          status: "error",
          message: error instanceof Error ? error.message : "分享暂时不可用",
        }),
      );
  }, []);

  useEffect(() => {
    setDocumentTitle(
      buildPublicShareTitle({
        shareName: state.status === "ready" ? state.share.name : null,
        teamName: state.status === "ready" ? state.share.teamName : null,
      }),
    );
  }, [state]);

  if (state.status === "opening") {
    return (
      <main className="public-share-shell is-centered">
        <PublicBrand />
        <p className="public-share-status">正在打开审阅</p>
      </main>
    );
  }
  if (state.status === "password") {
    return (
      <SharePassword
        onReady={(share) => setState({ status: "ready", share })}
      />
    );
  }
  if (state.status === "error") {
    return (
      <main className="public-share-shell is-centered">
        <PublicBrand />
        <h1>这个分享无法打开</h1>
        <p className="public-share-status">{state.message}</p>
      </main>
    );
  }
  if (shouldRequestIdentity(state.share)) {
    return (
      <VisitorIdentityGate
        share={state.share}
        onReady={(share) => setState({ status: "ready", share })}
      />
    );
  }
  return <PublicReviewRoom share={state.share} />;
}

function SharePassword({ onReady }: { onReady: (share: PublicShare) => void }) {
  const [password, setPassword] = useState("");
  const [error, setError] = useState<string | null>(null);
  const [busy, setBusy] = useState(false);

  async function submit(event: FormEvent<HTMLFormElement>) {
    event.preventDefault();
    if (!password) {
      setError("请输入访问密码");
      return;
    }
    setBusy(true);
    setError(null);
    try {
      const result = await verifyPublicShare(password);
      if (!result.share) {
        throw new Error("无法读取分享内容");
      }
      onReady(result.share);
    } catch (submitError) {
      setError(
        submitError instanceof Error ? submitError.message : "密码验证失败",
      );
      setBusy(false);
    }
  }

  return (
    <main className="public-share-shell is-centered">
      <form className="public-password-form" onSubmit={submit}>
        <PublicBrand />
        <p className="eyebrow">PRIVATE REVIEW</p>
        <h1>请输入访问密码</h1>
        <label className="field">
          <span>密码</span>
          <input
            type="password"
            value={password}
            autoFocus
            autoComplete="current-password"
            onChange={(event) => setPassword(event.target.value)}
          />
        </label>
        {error ? (
          <p className="form-error" role="alert">
            {error}
          </p>
        ) : null}
        <button className="primary-button" type="submit" disabled={busy}>
          {busy ? "验证中" : "进入审阅"}
        </button>
      </form>
    </main>
  );
}

function VisitorIdentityGate({
  share,
  onReady,
}: {
  share: PublicShare;
  onReady: (share: PublicShare) => void;
}) {
  return (
    <main className="public-share-shell is-centered">
      <VisitorIdentityForm share={share} required onReady={onReady} />
    </main>
  );
}

function VisitorIdentityForm({
  share,
  required,
  onReady,
  onCancel,
}: {
  share: PublicShare;
  required: boolean;
  onReady: (share: PublicShare) => void;
  onCancel?: () => void;
}) {
  const [method, setMethod] =
    useState<IdentifyPublicShareInput["method"]>("nickname");
  const [displayName, setDisplayName] = useState(
    share.visitor.displayName ?? "",
  );
  const [code, setCode] = useState("");
  const [error, setError] = useState<string | null>(null);
  const [busy, setBusy] = useState(false);

  async function submit(event: FormEvent<HTMLFormElement>) {
    event.preventDefault();
    const input =
      method === "verification_code"
        ? { method, code }
        : { method, displayName };
    if (method === "nickname" && !displayName.trim()) {
      setError("请输入你的称呼");
      return;
    }
    if (method === "verification_code" && !code.trim()) {
      setError("请输入访客验证码");
      return;
    }
    setBusy(true);
    setError(null);
    try {
      onReady(await identifyPublicShare(input));
    } catch (submitError) {
      setError(
        submitError instanceof Error ? submitError.message : "访客身份设置失败",
      );
      setBusy(false);
    }
  }

  return (
    <form
      className="public-password-form public-identity-form"
      onSubmit={submit}
    >
      <PublicBrand teamName={share.teamName} />
      <p className="eyebrow">VISITOR IDENTITY</p>
      <h1>{required ? "留下你的称呼" : "访客身份"}</h1>
      <div
        className="public-identity-tabs"
        role="tablist"
        aria-label="访客身份方式"
      >
        <button
          type="button"
          className={method === "nickname" ? "is-active" : ""}
          onClick={() => setMethod("nickname")}
        >
          昵称
        </button>
        <button
          type="button"
          className={method === "verification_code" ? "is-active" : ""}
          onClick={() => setMethod("verification_code")}
        >
          验证码
        </button>
      </div>
      {method === "verification_code" ? (
        <label className="field">
          <span>访客验证码</span>
          <input
            type="text"
            value={code}
            autoFocus
            autoComplete="one-time-code"
            inputMode="text"
            placeholder="ABCD-1234-EF56"
            onChange={(event) => setCode(event.target.value)}
          />
        </label>
      ) : (
        <label className="field">
          <span>称呼</span>
          <input
            type="text"
            value={displayName}
            autoFocus
            autoComplete="name"
            maxLength={80}
            placeholder="客户 A"
            onChange={(event) => setDisplayName(event.target.value)}
          />
        </label>
      )}
      {error ? (
        <p className="form-error" role="alert">
          {error}
        </p>
      ) : null}
      <div className="public-identity-actions">
        {onCancel ? (
          <button className="secondary-button" type="button" onClick={onCancel}>
            取消
          </button>
        ) : null}
        <button className="primary-button" type="submit" disabled={busy}>
          {busy ? "保存中" : "继续审阅"}
        </button>
      </div>
    </form>
  );
}

function PublicReviewRoom({ share }: { share: PublicShare }) {
  const [currentShare, setCurrentShare] = useState(share);
  const [identityOpen, setIdentityOpen] = useState(false);
  const [selectedId, setSelectedId] = useState(share.items[0]?.id ?? "");
  const [imageThreads, setImageThreads] = useState<CommentThread[]>([]);
  const [imageThreadsLoading, setImageThreadsLoading] = useState(false);
  const [imageLoadError, setImageLoadError] = useState<string | null>(null);
  const [imageMode, setImageMode] = useState<ImageAnnotationMode>("point");
  const [imageDraft, setImageDraft] = useState<AnnotationGeometry | null>(null);
  const [imageColor, setImageColor] = useState(defaultDrawingColor);
  const [activeImageThreadId, setActiveImageThreadId] = useState<string | null>(
    null,
  );
  const [playbackRate, setPlaybackRate] = useState<PublicPlaybackRate>(
    readPublicPlaybackRate,
  );
  const videoRef = useRef<HTMLVideoElement>(null);
  useEffect(() => {
    setCurrentShare(share);
    setSelectedId((current) => current || share.items[0]?.id || "");
  }, [share]);
  const selected =
    currentShare.items.find((item) => item.id === selectedId) ??
    currentShare.items[0] ??
    null;
  useEffect(() => {
    const video = videoRef.current;
    if (!video || selected?.mediaType !== "video") {
      return;
    }
    video.defaultPlaybackRate = playbackRate;
    video.playbackRate = playbackRate;
  }, [playbackRate, selected?.id, selected?.mediaType, selected?.previewUrl]);
  useEffect(() => {
    writePublicPlaybackRate(playbackRate);
  }, [playbackRate]);
  useEffect(() => {
    setImageDraft(null);
    setActiveImageThreadId(null);
    setImageLoadError(null);
    if (selected?.mediaType !== "image") {
      setImageThreads([]);
      setImageThreadsLoading(false);
      return;
    }
    const controller = new AbortController();
    setImageThreadsLoading(true);
    listPublicThreads(selected.id, controller.signal)
      .then(setImageThreads)
      .catch((loadError: unknown) => {
        if (
          !(
            loadError instanceof DOMException && loadError.name === "AbortError"
          )
        ) {
          setImageLoadError(
            loadError instanceof Error ? loadError.message : "无法读取图片评论",
          );
        }
      })
      .finally(() => setImageThreadsLoading(false));
    return () => controller.abort();
  }, [selected?.id, selected?.mediaType]);
  return (
    <main className="public-review-room">
      <header className="public-review-header">
        <PublicBrand teamName={currentShare.teamName} />
        <div>
          <p className="eyebrow">SHARED REVIEW</p>
          <h1>{currentShare.name}</h1>
          <span>{currentShare.reviewName}</span>
        </div>
        <div className="public-permissions">
          <span>
            {publicReviewPermissionLabel(
              currentShare.reviewStatus,
              currentShare.allowComment,
            )}
          </span>
          <span>{currentShare.allowDownload ? "可下载" : "禁止下载"}</span>
          <span>{visitorLabel(currentShare.visitor)}</span>
          {currentShare.expiresAt ? (
            <span>有效至 {formatDate(currentShare.expiresAt)}</span>
          ) : null}
        </div>
      </header>

      <section className="public-review-layout">
        <div
          className={`public-review-stage${
            selected?.mediaType === "video" ? " is-video" : ""
          }`}
        >
          {selected?.mediaType === "video" && selected.previewUrl ? (
            <PublicPlaybackRateControl
              value={playbackRate}
              onChange={setPlaybackRate}
            />
          ) : null}
          {selected?.mediaType === "image" ? (
            <PublicImagePreview
              item={selected}
              threads={imageThreads}
              draft={imageDraft}
              mode={imageMode}
              color={imageColor}
              activeThreadId={activeImageThreadId}
              allowComment={currentShare.allowComment}
              onDraftChange={setImageDraft}
              onThreadSelect={setActiveImageThreadId}
            />
          ) : selected ? (
            <PublicPreview item={selected} videoRef={videoRef} />
          ) : (
            <div className="public-preview-empty">分享中没有可显示的版本</div>
          )}
        </div>
        <aside className="public-review-sidebar">
          <div className="public-visitor-panel">
            <span>访客身份</span>
            <strong>{visitorLabel(currentShare.visitor)}</strong>
            {currentShare.visitor.verified ? <em>验证码已识别</em> : null}
            <button type="button" onClick={() => setIdentityOpen(true)}>
              {currentShare.visitor.identified ? "更改称呼" : "填写称呼"}
            </button>
          </div>
          {identityOpen ? (
            <div className="public-identity-inline">
              <VisitorIdentityForm
                share={currentShare}
                required={currentShare.requireNickname}
                onReady={(updated) => {
                  setCurrentShare(updated);
                  setIdentityOpen(false);
                }}
                onCancel={() => setIdentityOpen(false)}
              />
            </div>
          ) : null}
          {currentShare.items.length > 1 ? (
            <div className="public-item-list" aria-label="审阅文件列表">
              <div className="public-item-list-title">
                <span>文件列表</span>
                <em>{currentShare.items.length} 个文件</em>
              </div>
              {currentShare.items.map((item) => (
                <button
                  className={item.id === selected?.id ? "is-selected" : ""}
                  type="button"
                  key={item.id}
                  onClick={() => setSelectedId(item.id)}
                >
                  <span className="review-version-mark">
                    V{item.versionNumber}
                  </span>
                  <strong>{item.assetName}</strong>
                  <em>{mediaTypeLabel(item.mediaType)}</em>
                </button>
              ))}
            </div>
          ) : null}
          {selected ? (
            <div className="public-selected-meta">
              <span>当前版本</span>
              <strong>{selected.assetName}</strong>
              <em>V{selected.versionNumber}</em>
              {selected.downloadUrl ? (
                <div className="public-download-card">
                  <div>
                    <strong>源文件下载</strong>
                    <span>{downloadQualityLabel(selected)}</span>
                  </div>
                  <a href={selected.downloadUrl}>下载</a>
                </div>
              ) : null}
            </div>
          ) : null}
          {selected ? (
            <PublicDecisionControl
              item={selected}
              allowComment={currentShare.allowComment}
              reviewStatus={currentShare.reviewStatus}
              onShareUpdated={setCurrentShare}
            />
          ) : null}
          {selected?.mediaType === "video" ? (
            <PublicCommentPanel
              key={selected.id}
              item={selected}
              allowComment={currentShare.allowComment}
              reviewStatus={currentShare.reviewStatus}
              videoRef={videoRef}
            />
          ) : null}
          {selected?.mediaType === "image" ? (
            <PublicImageCommentPanel
              key={selected.id}
              item={selected}
              allowComment={currentShare.allowComment}
              reviewStatus={currentShare.reviewStatus}
              mode={imageMode}
              color={imageColor}
              draft={imageDraft}
              threads={imageThreads}
              loading={imageThreadsLoading}
              loadError={imageLoadError}
              activeThreadId={activeImageThreadId}
              onModeChange={(nextMode) => {
                setImageMode(nextMode);
                setImageDraft((current) =>
                  isDrawingMode(nextMode) && current?.shape === "drawing"
                    ? current
                    : null,
                );
              }}
              onColorChange={setImageColor}
              onDraftChange={setImageDraft}
              onThreadsChange={setImageThreads}
              onThreadSelect={setActiveImageThreadId}
            />
          ) : null}
        </aside>
      </section>
    </main>
  );
}

function PublicDecisionControl({
  item,
  allowComment,
  reviewStatus,
  onShareUpdated,
}: {
  item: PublicShareItem;
  allowComment: boolean;
  reviewStatus: PublicShare["reviewStatus"];
  onShareUpdated: (share: PublicShare) => void;
}) {
  const [decision, setDecision] = useState<ReviewDecisionValue>("approved");
  const [note, setNote] = useState("");
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState<string | null>(null);
  const [success, setSuccess] = useState<string | null>(null);
  const canDecide =
    allowComment && reviewStatus !== "draft" && reviewStatus !== "closed";

  useEffect(() => {
    setDecision("approved");
    setNote("");
    setError(null);
    setSuccess(null);
  }, [item.id]);

  async function submit(event: FormEvent<HTMLFormElement>) {
    event.preventDefault();
    setBusy(true);
    setError(null);
    setSuccess(null);
    try {
      await createPublicDecision({
        reviewItemId: item.id,
        decision,
        note: note.trim(),
      });
      setNote("");
      setSuccess(`已记录“${reviewDecisionLabel(decision)}”`);
      onShareUpdated(await getPublicShare());
    } catch (submitError) {
      setError(
        submitError instanceof Error ? submitError.message : "结论提交失败",
      );
    } finally {
      setBusy(false);
    }
  }

  return (
    <section className="public-decision-panel">
      <header>
        <div>
          <span>当前版本结论</span>
          <strong>V{item.versionNumber}</strong>
        </div>
        <em>{publicReviewStatusLabel(reviewStatus)}</em>
      </header>
      {canDecide ? (
        <form onSubmit={submit}>
          <div
            className="public-decision-options"
            role="radiogroup"
            aria-label="当前版本结论"
          >
            {publicDecisionOptions.map((option) => (
              <button
                className={decision === option.value ? "is-active" : ""}
                type="button"
                role="radio"
                aria-checked={decision === option.value}
                key={option.value}
                onClick={() => setDecision(option.value)}
              >
                {option.label}
              </button>
            ))}
          </div>
          <textarea
            value={note}
            maxLength={4000}
            placeholder="补充结论说明（可选）"
            onChange={(event) => setNote(event.target.value)}
          />
          {success ? (
            <p className="public-decision-success">{success}</p>
          ) : null}
          {error ? (
            <p className="form-error" role="alert">
              {error}
            </p>
          ) : null}
          <button className="primary-button" type="submit" disabled={busy}>
            {busy ? "提交中" : "提交正式结论"}
          </button>
        </form>
      ) : (
        <p className="public-comment-readonly">
          {allowComment
            ? "审阅已结束，不能再提交正式结论。"
            : "这个分享已关闭访客反馈。"}
        </p>
      )}
    </section>
  );
}

function PublicImagePreview({
  item,
  threads,
  draft,
  mode,
  color,
  activeThreadId,
  allowComment,
  onDraftChange,
  onThreadSelect,
}: {
  item: PublicShareItem;
  threads: CommentThread[];
  draft: AnnotationGeometry | null;
  mode: ImageAnnotationMode;
  color: string;
  activeThreadId: string | null;
  allowComment: boolean;
  onDraftChange: (geometry: AnnotationGeometry | null) => void;
  onThreadSelect: (threadId: string) => void;
}) {
  const dragStart = useRef<NormalizedPoint | null>(null);
  const drawingBase = useRef<DrawingAnnotationGeometry | null>(null);
  const drawingPoints = useRef<NormalizedPoint[]>([]);
  const [naturalRatio, setNaturalRatio] = useState<number | null>(null);
  if (!item.previewUrl) {
    return (
      <div className="public-preview-empty">
        <span>PREVIEW</span>
        <p>这个版本还没有可用的网页预览。</p>
      </div>
    );
  }
  const imageRatio =
    item.width && item.height
      ? item.width / item.height
      : naturalRatio && naturalRatio > 0
        ? naturalRatio
        : 16 / 9;
  const imageStyle = {
    "--public-image-ratio": imageRatio,
  } as CSSProperties;

  function pointerPosition(event: ReactPointerEvent<HTMLDivElement>) {
    return normalizePointerPosition(
      event.clientX,
      event.clientY,
      event.currentTarget.getBoundingClientRect(),
    );
  }

  function startAnnotation(event: ReactPointerEvent<HTMLDivElement>) {
    if (!allowComment || event.button !== 0) {
      return;
    }
    const point = pointerPosition(event);
    if (mode === "point") {
      onDraftChange(createPointGeometry(point));
      return;
    }
    if (isDrawingMode(mode)) {
      dragStart.current = point;
      drawingBase.current = draft?.shape === "drawing" ? draft : null;
      drawingPoints.current = [point];
      event.currentTarget.setPointerCapture(event.pointerId);
      return;
    }
    dragStart.current = point;
    onDraftChange(null);
    event.currentTarget.setPointerCapture(event.pointerId);
  }

  function updateAnnotation(event: ReactPointerEvent<HTMLDivElement>) {
    if (dragStart.current === null) {
      return;
    }
    const point = pointerPosition(event);
    if (mode === "region") {
      onDraftChange(createRegionGeometry(dragStart.current, point, 0));
      return;
    }
    if (isDrawingMode(mode)) {
      if (mode === "brush") {
        drawingPoints.current = [...drawingPoints.current, point];
      }
      const element = createDrawingElementPreview(mode, {
        start: dragStart.current,
        current: point,
        color,
        points: drawingPoints.current,
      });
      onDraftChange(appendDrawingElement(drawingBase.current, element));
    }
  }

  function finishAnnotation(event: ReactPointerEvent<HTMLDivElement>) {
    if (dragStart.current === null) {
      return;
    }
    const point = pointerPosition(event);
    if (mode === "region") {
      onDraftChange(createRegionGeometry(dragStart.current, point));
    } else if (isDrawingMode(mode)) {
      if (mode === "brush") {
        drawingPoints.current = [...drawingPoints.current, point];
      }
      const element = createDrawingElement(mode, {
        start: dragStart.current,
        current: point,
        color,
        points: drawingPoints.current,
      });
      onDraftChange(appendDrawingElement(drawingBase.current, element));
    }
    dragStart.current = null;
    drawingBase.current = null;
    drawingPoints.current = [];
    if (event.currentTarget.hasPointerCapture(event.pointerId)) {
      event.currentTarget.releasePointerCapture(event.pointerId);
    }
  }

  return (
    <div className="public-image-canvas" style={imageStyle}>
      <img
        src={item.previewUrl}
        alt={item.assetName}
        draggable={false}
        onLoad={(event) => {
          if (event.currentTarget.naturalHeight > 0) {
            setNaturalRatio(
              event.currentTarget.naturalWidth /
                event.currentTarget.naturalHeight,
            );
          }
        }}
      />
      <div
        className={`public-image-annotation-layer${
          allowComment ? " is-editable" : ""
        }`}
        aria-label={imageAnnotationLayerLabel(mode)}
        onPointerDown={startAnnotation}
        onPointerMove={updateAnnotation}
        onPointerUp={finishAnnotation}
        onPointerCancel={finishAnnotation}
      >
        {threads.map((thread, index) =>
          thread.annotation.geometry
            ? renderImageAnnotation({
                geometry: thread.annotation.geometry,
                index: index + 1,
                active: activeThreadId === thread.id,
                markerKey: thread.id,
                title: thread.comments[0]?.body ?? "图片评论",
                passive: allowComment,
                onSelect: () => onThreadSelect(thread.id),
              })
            : null,
        )}
        {draft
          ? renderImageAnnotation({
              geometry: draft,
              index: "新",
              markerKey: "draft",
              active: false,
              draft: true,
              title: "新标注",
            })
          : null}
      </div>
    </div>
  );
}

function PublicImageCommentPanel({
  item,
  allowComment,
  reviewStatus,
  mode,
  color,
  draft,
  threads,
  loading,
  loadError,
  activeThreadId,
  onModeChange,
  onColorChange,
  onDraftChange,
  onThreadsChange,
  onThreadSelect,
}: {
  item: PublicShareItem;
  allowComment: boolean;
  reviewStatus: PublicShare["reviewStatus"];
  mode: ImageAnnotationMode;
  color: string;
  draft: AnnotationGeometry | null;
  threads: CommentThread[];
  loading: boolean;
  loadError: string | null;
  activeThreadId: string | null;
  onModeChange: (mode: ImageAnnotationMode) => void;
  onColorChange: (color: string) => void;
  onDraftChange: (geometry: AnnotationGeometry | null) => void;
  onThreadsChange: (threads: CommentThread[]) => void;
  onThreadSelect: (threadId: string | null) => void;
}) {
  const [body, setBody] = useState("");
  const [attachments, setAttachments] = useState<CommentAttachmentDraft[]>([]);
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState<string | null>(null);

  async function submit(event: FormEvent<HTMLFormElement>) {
    event.preventDefault();
    const annotation = imageAnnotationDraftToRequest(mode, draft);
    if (!annotation) {
      setError(imageAnnotationDraftError(mode));
      return;
    }
    if (!body.trim()) {
      setError("请输入评论内容");
      return;
    }
    if (attachments.some((item) => item.uploading)) {
      setError("图片还在上传，请稍后再提交");
      return;
    }
    if (attachments.some((item) => item.error || !item.attachment)) {
      setError("有图片上传失败，请删除后重试");
      return;
    }
    setBusy(true);
    setError(null);
    try {
      const created = await createPublicThread(item.id, {
        body: body.trim(),
        annotation,
        attachmentIds: attachmentIds(attachments),
      });
      onThreadsChange(
        [...threads, created].sort((left, right) =>
          left.createdAt.localeCompare(right.createdAt),
        ),
      );
      onThreadSelect(created.id);
      onDraftChange(null);
      setBody("");
      setAttachments([]);
    } catch (submitError) {
      setError(
        submitError instanceof Error ? submitError.message : "评论提交失败",
      );
    } finally {
      setBusy(false);
    }
  }

  return (
    <section className="public-comment-panel public-image-comment-panel">
      <header>
        <div>
          <span>图片评论</span>
          <strong>{threads.length}</strong>
        </div>
        <em>{formatImageGeometry(draft)}</em>
      </header>

      {allowComment ? (
        <form className="public-comment-form" onSubmit={submit}>
          <div
            className="public-comment-mode"
            role="tablist"
            aria-label="图片标注类型"
          >
            <button
              type="button"
              className={mode === "point" ? "is-active" : ""}
              onClick={() => onModeChange("point")}
            >
              点位
            </button>
            <button
              type="button"
              className={mode === "region" ? "is-active" : ""}
              onClick={() => onModeChange("region")}
            >
              矩形区域
            </button>
            <button
              type="button"
              className={mode === "brush" ? "is-active" : ""}
              onClick={() => onModeChange("brush")}
            >
              画笔
            </button>
            <button
              type="button"
              className={mode === "arrow" ? "is-active" : ""}
              onClick={() => onModeChange("arrow")}
            >
              箭头
            </button>
            <button
              type="button"
              className={mode === "drawing_rect" ? "is-active" : ""}
              onClick={() => onModeChange("drawing_rect")}
            >
              矩形
            </button>
          </div>
          {isDrawingMode(mode) ? (
            <div className="public-drawing-controls">
              <div className="public-color-swatches" aria-label="标注颜色">
                {drawingColors.map((itemColor) => (
                  <button
                    key={itemColor}
                    type="button"
                    className={itemColor === color ? "is-active" : ""}
                    style={{ "--swatch-color": itemColor } as CSSProperties}
                    aria-label={`使用颜色 ${itemColor}`}
                    onClick={() => onColorChange(itemColor)}
                  />
                ))}
              </div>
              <button
                type="button"
                disabled={draft?.shape !== "drawing"}
                onClick={() => onDraftChange(removeLastDrawingElement(draft))}
              >
                撤销上一笔
              </button>
            </div>
          ) : null}
          <p className="public-image-instruction">
            {imageAnnotationInstruction(mode)}
          </p>
          <CommentAttachmentField
            value={body}
            maxLength={4000}
            placeholder="描述这个画面位置需要调整的内容"
            disabled={busy}
            attachments={attachments}
            onAttachmentsChange={setAttachments}
            uploadFile={(file) => uploadPublicCommentAttachment(item.id, file)}
            onChange={setBody}
          />
          {error || loadError ? (
            <p className="form-error" role="alert">
              {error ?? loadError}
            </p>
          ) : null}
          <button className="primary-button" type="submit" disabled={busy}>
            {busy ? "提交中" : "提交评论"}
          </button>
        </form>
      ) : (
        <p className="public-comment-readonly">
          {publicCommentReadonlyMessage(reviewStatus)}
        </p>
      )}

      <div className="public-thread-list public-image-thread-list">
        {loading ? <p>正在读取评论</p> : null}
        {!loading && threads.length === 0 ? <p>还没有图片评论</p> : null}
        {threads.map((thread, index) => (
          <PublicThreadCard
            active={activeThreadId === thread.id}
            allowComment={allowComment}
            itemId={item.id}
            key={thread.id}
            meta={`${index + 1} · ${formatImageGeometry(
              thread.annotation.geometry,
            )}`}
            thread={thread}
            onSelect={() => onThreadSelect(thread.id)}
            onThreadDeleted={(threadId) => {
              onThreadsChange(threads.filter((item) => item.id !== threadId));
              if (activeThreadId === threadId) {
                onThreadSelect(null);
              }
            }}
            onThreadUpdated={(updated) =>
              onThreadsChange(
                threads.map((item) =>
                  item.id === updated.id ? updated : item,
                ),
              )
            }
          />
        ))}
      </div>
    </section>
  );
}

function PublicThreadCard({
  itemId,
  thread,
  meta,
  active,
  allowComment,
  onSelect,
  onThreadUpdated,
  onThreadDeleted,
}: {
  itemId: string;
  thread: CommentThread;
  meta: string;
  active: boolean;
  allowComment: boolean;
  onSelect: () => void;
  onThreadUpdated: (thread: CommentThread) => void;
  onThreadDeleted: (threadId: string) => void;
}) {
  const [replyBody, setReplyBody] = useState("");
  const [replyAttachments, setReplyAttachments] = useState<
    CommentAttachmentDraft[]
  >([]);
  const [editingId, setEditingId] = useState<string | null>(null);
  const [editingBody, setEditingBody] = useState("");
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState<string | null>(null);
  const firstComment = thread.comments[0];
  const latestComment = thread.comments[thread.comments.length - 1];
  const canReply = allowComment && thread.canReply;

  async function submitReply(event: FormEvent<HTMLFormElement>) {
    event.preventDefault();
    const body = replyBody.trim();
    if (!body) {
      setError("请输入回复内容");
      return;
    }
    if (replyAttachments.some((item) => item.uploading)) {
      setError("图片还在上传，请稍后再提交");
      return;
    }
    if (replyAttachments.some((item) => item.error || !item.attachment)) {
      setError("有图片上传失败，请删除后重试");
      return;
    }
    setBusy(true);
    setError(null);
    try {
      const updated = await addPublicThreadComment(itemId, thread.id, {
        body,
        attachmentIds: attachmentIds(replyAttachments),
      });
      onThreadUpdated(updated);
      setReplyBody("");
      setReplyAttachments([]);
    } catch (replyError) {
      setError(replyError instanceof Error ? replyError.message : "回复失败");
    } finally {
      setBusy(false);
    }
  }

  async function submitEdit(
    event: FormEvent<HTMLFormElement>,
    commentId: string,
  ) {
    event.preventDefault();
    const body = editingBody.trim();
    if (!body) {
      setError("评论内容不能为空");
      return;
    }
    setBusy(true);
    setError(null);
    try {
      const updated = await updatePublicComment(itemId, thread.id, commentId, {
        body,
      });
      onThreadUpdated(updated);
      setEditingId(null);
      setEditingBody("");
    } catch (editError) {
      setError(editError instanceof Error ? editError.message : "编辑失败");
    } finally {
      setBusy(false);
    }
  }

  async function removeComment(commentId: string) {
    if (!window.confirm("确定删除这条评论吗？")) {
      return;
    }
    setBusy(true);
    setError(null);
    try {
      await deletePublicComment(itemId, thread.id, commentId);
      if (thread.comments.length <= 1) {
        onThreadDeleted(thread.id);
      } else {
        onThreadUpdated({
          ...thread,
          comments: thread.comments.filter(
            (comment) => comment.id !== commentId,
          ),
          revision: thread.revision + 1,
          updatedAt: new Date().toISOString(),
        });
      }
    } catch (deleteError) {
      setError(deleteError instanceof Error ? deleteError.message : "删除失败");
    } finally {
      setBusy(false);
    }
  }

  return (
    <article className={`public-thread-card${active ? " is-active" : ""}`}>
      <button
        className="public-thread-summary"
        type="button"
        onClick={onSelect}
      >
        <span>
          {meta} · {threadStatusLabel(thread.status)}
        </span>
        <strong>{firstComment?.body ?? ""}</strong>
        <em>
          {latestComment?.author.displayName ?? thread.author.displayName} ·{" "}
          {formatCommentDate(latestComment?.createdAt ?? thread.createdAt)} ·{" "}
          {thread.comments.length} 条
        </em>
      </button>
      {active ? (
        <div className="public-thread-conversation">
          {thread.status === "resolved" ? (
            <p className="public-thread-state">
              已由 {thread.resolvedBy?.displayName ?? "管理员"} 解决
              {thread.resolvedAt
                ? ` · ${formatCommentDate(thread.resolvedAt)}`
                : ""}
            </p>
          ) : null}
          <div className="public-thread-comments">
            {thread.comments.map((comment) => (
              <article className="public-thread-comment" key={comment.id}>
                <header>
                  <strong>{comment.author.displayName}</strong>
                  <span>
                    {formatCommentDate(comment.createdAt)}
                    {comment.editedAt ? " · 已编辑" : ""}
                  </span>
                </header>
                {editingId === comment.id ? (
                  <form
                    className="public-inline-comment-form"
                    onSubmit={(event) => submitEdit(event, comment.id)}
                  >
                    <textarea
                      value={editingBody}
                      maxLength={4000}
                      onChange={(event) => setEditingBody(event.target.value)}
                    />
                    <div>
                      <button
                        type="button"
                        disabled={busy}
                        onClick={() => {
                          setEditingId(null);
                          setEditingBody("");
                        }}
                      >
                        取消
                      </button>
                      <button type="submit" disabled={busy}>
                        保存
                      </button>
                    </div>
                  </form>
                ) : (
                  <>
                    <p>{comment.body}</p>
                    <CommentAttachmentGallery
                      attachments={comment.attachments ?? []}
                    />
                  </>
                )}
                {comment.canEdit || comment.canDelete ? (
                  <div className="public-comment-actions">
                    {comment.canEdit ? (
                      <button
                        type="button"
                        disabled={busy}
                        onClick={() => {
                          setEditingId(comment.id);
                          setEditingBody(comment.body);
                        }}
                      >
                        编辑
                      </button>
                    ) : null}
                    {comment.canDelete ? (
                      <button
                        type="button"
                        disabled={busy}
                        onClick={() => void removeComment(comment.id)}
                      >
                        删除
                      </button>
                    ) : null}
                  </div>
                ) : null}
              </article>
            ))}
          </div>
          {canReply ? (
            <form className="public-thread-reply-form" onSubmit={submitReply}>
              <CommentAttachmentField
                value={replyBody}
                maxLength={4000}
                placeholder="继续回复这个问题"
                disabled={busy}
                attachments={replyAttachments}
                onAttachmentsChange={setReplyAttachments}
                uploadFile={(file) =>
                  uploadPublicCommentAttachment(itemId, file)
                }
                onChange={setReplyBody}
              />
              {error ? (
                <p className="form-error" role="alert">
                  {error}
                </p>
              ) : null}
              <button type="submit" disabled={busy}>
                {busy ? "发送中" : "回复"}
              </button>
            </form>
          ) : (
            <p className="public-thread-state">
              {thread.status === "resolved"
                ? "这个问题已解决，不能继续回复。"
                : "当前分享不能继续评论。"}
            </p>
          )}
          {error && !canReply ? (
            <p className="form-error" role="alert">
              {error}
            </p>
          ) : null}
        </div>
      ) : null}
    </article>
  );
}

function PublicCommentPanel({
  item,
  allowComment,
  reviewStatus,
  videoRef,
}: {
  item: PublicShareItem;
  allowComment: boolean;
  reviewStatus: PublicShare["reviewStatus"];
  videoRef: RefObject<HTMLVideoElement | null>;
}) {
  const [threads, setThreads] = useState<CommentThread[]>([]);
  const [mode, setMode] = useState<"time_point" | "time_range">("time_point");
  const [body, setBody] = useState("");
  const [attachments, setAttachments] = useState<CommentAttachmentDraft[]>([]);
  const [timeStartUs, setTimeStartUs] = useState<number | null>(null);
  const [timeEndUs, setTimeEndUs] = useState<number | null>(null);
  const [currentTimeUs, setCurrentTimeUs] = useState(0);
  const [activeThreadId, setActiveThreadId] = useState<string | null>(null);
  const [loading, setLoading] = useState(true);
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState<string | null>(null);

  useEffect(() => {
    const controller = new AbortController();
    setLoading(true);
    listPublicThreads(item.id, controller.signal)
      .then(setThreads)
      .catch((loadError: unknown) => {
        if (
          !(
            loadError instanceof DOMException && loadError.name === "AbortError"
          )
        ) {
          setError(
            loadError instanceof Error ? loadError.message : "无法读取评论",
          );
        }
      })
      .finally(() => setLoading(false));
    return () => controller.abort();
  }, [item.id]);

  useEffect(() => {
    const video = videoRef.current;
    if (!video) {
      return;
    }
    const update = () => setCurrentTimeUs(secondsToUs(video.currentTime));
    update();
    video.addEventListener("timeupdate", update);
    video.addEventListener("seeked", update);
    return () => {
      video.removeEventListener("timeupdate", update);
      video.removeEventListener("seeked", update);
    };
  }, [item.id, videoRef]);

  function captureStart() {
    const value = currentVideoTimeUs(videoRef, item.durationUs);
    setCurrentTimeUs(value);
    setTimeStartUs(value);
    if (mode === "time_point") {
      setTimeEndUs(null);
    } else if (timeEndUs !== null && timeEndUs <= value) {
      setTimeEndUs(null);
    }
  }

  function captureEnd() {
    const value = currentVideoTimeUs(videoRef, item.durationUs);
    setCurrentTimeUs(value);
    setTimeEndUs(value);
  }

  async function submit(event: FormEvent<HTMLFormElement>) {
    event.preventDefault();
    if (!body.trim()) {
      setError("请输入评论内容");
      return;
    }
    if (timeStartUs === null) {
      setError(mode === "time_point" ? "请先抓取当前时间" : "请先设置区间起点");
      return;
    }
    if (
      mode === "time_range" &&
      (timeEndUs === null || timeEndUs <= timeStartUs)
    ) {
      setError("区间终点必须晚于起点");
      return;
    }
    if (attachments.some((item) => item.uploading)) {
      setError("图片还在上传，请稍后再提交");
      return;
    }
    if (attachments.some((item) => item.error || !item.attachment)) {
      setError("有图片上传失败，请删除后重试");
      return;
    }
    setBusy(true);
    setError(null);
    try {
      const created = await createPublicThread(item.id, {
        body: body.trim(),
        annotation: {
          kind: mode,
          timeStartUs,
          timeEndUs: mode === "time_range" ? timeEndUs : null,
          geometry: null,
          geometryVersion: 1,
        },
        attachmentIds: attachmentIds(attachments),
      });
      setThreads((current) =>
        [...current, created].sort(
          (left, right) =>
            (left.annotation.timeStartUs ?? 0) -
            (right.annotation.timeStartUs ?? 0),
        ),
      );
      setBody("");
      setAttachments([]);
      setTimeStartUs(null);
      setTimeEndUs(null);
    } catch (submitError) {
      setError(
        submitError instanceof Error ? submitError.message : "评论提交失败",
      );
    } finally {
      setBusy(false);
    }
  }

  function jumpTo(thread: CommentThread) {
    const startUs = thread.annotation.timeStartUs;
    const video = videoRef.current;
    if (startUs === null || !video) {
      return;
    }
    video.pause();
    video.currentTime = startUs / 1_000_000;
    setCurrentTimeUs(startUs);
    setActiveThreadId(thread.id);
  }

  return (
    <section className="public-comment-panel">
      <header>
        <div>
          <span>时间评论</span>
          <strong>{threads.length}</strong>
        </div>
        <em>当前 {formatMediaTime(currentTimeUs)}</em>
      </header>

      {allowComment ? (
        <form className="public-comment-form" onSubmit={submit}>
          <div
            className="public-comment-mode"
            role="tablist"
            aria-label="评论时间类型"
          >
            <button
              type="button"
              className={mode === "time_point" ? "is-active" : ""}
              onClick={() => {
                setMode("time_point");
                setTimeEndUs(null);
              }}
            >
              时间点
            </button>
            <button
              type="button"
              className={mode === "time_range" ? "is-active" : ""}
              onClick={() => setMode("time_range")}
            >
              时间区间
            </button>
          </div>
          <div className="public-time-capture">
            <button type="button" onClick={captureStart}>
              {mode === "time_point" ? "抓取当前时间" : "设为起点"}
            </button>
            {mode === "time_range" ? (
              <button type="button" onClick={captureEnd}>
                设为终点
              </button>
            ) : null}
            <span>{formatAnnotationDraft(mode, timeStartUs, timeEndUs)}</span>
          </div>
          <CommentAttachmentField
            value={body}
            maxLength={4000}
            placeholder="描述这个时间位置需要调整的内容"
            disabled={busy}
            attachments={attachments}
            onAttachmentsChange={setAttachments}
            uploadFile={(file) => uploadPublicCommentAttachment(item.id, file)}
            onChange={setBody}
          />
          {error ? (
            <p className="form-error" role="alert">
              {error}
            </p>
          ) : null}
          <button className="primary-button" type="submit" disabled={busy}>
            {busy ? "提交中" : "提交评论"}
          </button>
        </form>
      ) : (
        <p className="public-comment-readonly">
          {publicCommentReadonlyMessage(reviewStatus)}
        </p>
      )}

      <div className="public-thread-list">
        {loading ? <p>正在读取评论</p> : null}
        {!loading && threads.length === 0 ? <p>还没有时间评论</p> : null}
        {threads.map((thread) => (
          <PublicThreadCard
            active={activeThreadId === thread.id}
            allowComment={allowComment}
            itemId={item.id}
            key={thread.id}
            meta={formatThreadTime(thread.annotation)}
            thread={thread}
            onSelect={() => jumpTo(thread)}
            onThreadDeleted={(threadId) => {
              setThreads((current) =>
                current.filter((item) => item.id !== threadId),
              );
              if (activeThreadId === threadId) {
                setActiveThreadId(null);
              }
            }}
            onThreadUpdated={(updated) =>
              setThreads((current) =>
                current.map((item) =>
                  item.id === updated.id ? updated : item,
                ),
              )
            }
          />
        ))}
      </div>
    </section>
  );
}

function shouldRequestIdentity(share: PublicShare) {
  return share.requireNickname && !share.visitor.identified;
}

function visitorLabel(visitor: PublicShare["visitor"]) {
  if (visitor.displayName) {
    return visitor.displayName;
  }
  if (visitor.identityMethod === "verification_code") {
    return "已验证访客";
  }
  return "匿名访客";
}

function PublicPreview({
  item,
  videoRef,
}: {
  item: PublicShareItem;
  videoRef: RefObject<HTMLVideoElement | null>;
}) {
  if (!item.previewUrl) {
    return (
      <div className="public-preview-empty">
        <span>PREVIEW</span>
        <p>这个版本还没有可用的网页预览。</p>
      </div>
    );
  }
  if (item.mediaType === "video") {
    return (
      <HlsVideo
        ref={videoRef}
        src={item.previewUrl}
        controls
        playsInline
        preload="metadata"
      />
    );
  }
  if (item.mediaType === "image") {
    return <img src={item.previewUrl} alt={item.assetName} />;
  }
  return (
    <div className="public-preview-empty">
      <span>{mediaTypeLabel(item.mediaType)}</span>
      <p>请下载后查看这个版本。</p>
    </div>
  );
}

function PublicPlaybackRateControl({
  value,
  onChange,
}: {
  value: PublicPlaybackRate;
  onChange: (value: PublicPlaybackRate) => void;
}) {
  return (
    <div className="public-playback-rate-control" aria-label="播放速度">
      <span>倍速</span>
      <div role="group" aria-label="选择播放速度">
        {publicPlaybackRates.map((rate) => (
          <button
            className={rate === value ? "is-active" : ""}
            type="button"
            key={rate}
            aria-pressed={rate === value}
            onClick={() => onChange(rate)}
          >
            {formatPlaybackRate(rate)}
          </button>
        ))}
      </div>
    </div>
  );
}

function PublicBrand({ teamName }: { teamName?: string }) {
  return (
    <div className="brand public-brand">
      <span className="brand-mark" aria-hidden="true">
        <VistoMark className="visto-mark" />
      </span>
      <span>
        {PRODUCT_NAME_FULL}
        {teamName ? <small>{teamName}</small> : null}
      </span>
    </div>
  );
}

function readPublicPlaybackRate(): PublicPlaybackRate {
  try {
    const raw = window.localStorage.getItem(publicPlaybackRateStorageKey);
    const parsed = Number(raw);
    const matched = publicPlaybackRates.find((rate) => rate === parsed);
    return matched ?? 1;
  } catch {
    return 1;
  }
}

function writePublicPlaybackRate(value: PublicPlaybackRate) {
  try {
    window.localStorage.setItem(publicPlaybackRateStorageKey, String(value));
  } catch {
    // Ignore storage failures in privacy modes; playback still changes for this session.
  }
}

function formatPlaybackRate(value: PublicPlaybackRate) {
  return `${value}x`;
}

function mediaTypeLabel(value: string) {
  const labels: Record<string, string> = {
    video: "视频",
    image: "图片",
    audio: "音频",
    pdf: "PDF",
    document: "文档",
    design: "设计",
  };
  return labels[value] ?? "文件";
}

function reviewDecisionLabel(value: ReviewDecisionValue) {
  switch (value) {
    case "approved":
      return "通过";
    case "changes_requested":
      return "需修改";
    case "rejected":
      return "拒绝";
  }
}

function publicReviewStatusLabel(value: PublicShare["reviewStatus"]) {
  switch (value) {
    case "draft":
      return "尚未开启";
    case "open":
      return "审阅中";
    case "changes_requested":
      return "需要修改";
    case "approved":
      return "已通过";
    case "closed":
      return "已结束";
  }
}

function downloadQualityLabel(item: PublicShareItem) {
  return `${mediaTypeLabel(item.mediaType)} · 原始质量 · ${formatBytes(
    item.sizeBytes,
  )}`;
}

function formatBytes(value: number): string {
  if (value < 1024) return `${value} B`;
  if (value < 1024 ** 2) return `${(value / 1024).toFixed(1)} KB`;
  if (value < 1024 ** 3) return `${(value / 1024 ** 2).toFixed(1)} MB`;
  return `${(value / 1024 ** 3).toFixed(1)} GB`;
}

function currentVideoTimeUs(
  videoRef: RefObject<HTMLVideoElement | null>,
  durationUs: number | null,
) {
  const value = secondsToUs(videoRef.current?.currentTime ?? 0);
  return durationUs === null ? value : Math.min(value, durationUs);
}

function isDrawingMode(mode: ImageAnnotationMode) {
  return mode === "brush" || mode === "arrow" || mode === "drawing_rect";
}

function imageAnnotationLayerLabel(mode: ImageAnnotationMode) {
  if (mode === "point") {
    return "点击图片放置点位";
  }
  if (mode === "region") {
    return "在图片上拖动框选区域";
  }
  return "在图片上绘制标注";
}

function imageAnnotationInstruction(mode: ImageAnnotationMode) {
  const labels: Record<ImageAnnotationMode, string> = {
    point: "在左侧图片上点击需要反馈的位置。",
    region: "在左侧图片上按住并拖动，框选需要反馈的区域。",
    brush: "在左侧图片上按住并拖动画出手绘标记。",
    arrow: "在左侧图片上按住并拖动，画出指向箭头。",
    drawing_rect: "在左侧图片上按住并拖动，画出矩形框。",
  };
  return labels[mode];
}

function imageAnnotationDraftError(mode: ImageAnnotationMode) {
  if (mode === "point") {
    return "请先在图片上点击一个位置";
  }
  if (mode === "region") {
    return "请先在图片上拖出一个区域";
  }
  return "请先在图片上画出至少一个标注";
}

function imageAnnotationDraftToRequest(
  mode: ImageAnnotationMode,
  draft: AnnotationGeometry | null,
) {
  if (mode === "point" && draft?.shape === "point") {
    return {
      kind: "point" as const,
      timeStartUs: null,
      timeEndUs: null,
      geometry: draft,
      geometryVersion: 1 as const,
    };
  }
  if (mode === "region" && draft?.shape === "rect") {
    return {
      kind: "region" as const,
      timeStartUs: null,
      timeEndUs: null,
      geometry: draft,
      geometryVersion: 1 as const,
    };
  }
  if (
    isDrawingMode(mode) &&
    draft?.shape === "drawing" &&
    draft.elements.length > 0
  ) {
    return {
      kind: "drawing" as const,
      timeStartUs: null,
      timeEndUs: null,
      geometry: draft,
      geometryVersion: 1 as const,
    };
  }
  return null;
}

function createDrawingElementPreview(
  mode: ImageAnnotationMode,
  options: {
    start: NormalizedPoint;
    current: NormalizedPoint;
    color: string;
    points: NormalizedPoint[];
  },
) {
  return createDrawingElement(mode, options, 0);
}

function createDrawingElement(
  mode: ImageAnnotationMode,
  options: {
    start: NormalizedPoint;
    current: NormalizedPoint;
    color: string;
    points: NormalizedPoint[];
  },
  minimumSize = 0.01,
) {
  if (mode === "brush") {
    return createBrushDrawingElement(
      options.points,
      options.color,
      drawingStrokeWidth,
    );
  }
  if (mode === "arrow") {
    return createArrowDrawingElement(
      options.start,
      options.current,
      options.color,
      drawingStrokeWidth,
      minimumSize,
    );
  }
  if (mode === "drawing_rect") {
    return createDrawingRectElement(
      options.start,
      options.current,
      options.color,
      drawingStrokeWidth,
      minimumSize,
    );
  }
  return null;
}

function renderImageAnnotation({
  geometry,
  index,
  markerKey,
  active,
  draft,
  title,
  passive,
  onSelect,
}: {
  geometry: AnnotationGeometry;
  index: number | string;
  markerKey: string;
  active: boolean;
  draft?: boolean;
  title: string;
  passive?: boolean;
  onSelect?: () => void;
}) {
  if (geometry.shape === "drawing") {
    return (
      <div
        className={`public-image-marker is-drawing${
          active ? " is-active" : ""
        }${draft ? " is-draft" : ""}`}
        key={markerKey}
        title={title}
        aria-hidden="true"
      >
        <DrawingGeometry geometry={geometry} markerKey={markerKey} />
        <span style={drawingLabelStyle(geometry)}>{index}</span>
      </div>
    );
  }
  const className = `public-image-marker is-${geometry.shape}${
    active ? " is-active" : ""
  }${draft ? " is-draft" : ""}${passive ? " is-passive" : ""}`;
  if (draft || !onSelect || passive) {
    return (
      <div
        className={className}
        style={imageGeometryStyle(geometry)}
        key={markerKey}
        aria-hidden="true"
      >
        <span>{index}</span>
      </div>
    );
  }
  return (
    <button
      className={className}
      style={imageGeometryStyle(geometry)}
      type="button"
      key={markerKey}
      title={title}
      aria-label={`评论 ${index}：${title}`}
      onPointerDown={(event) => event.stopPropagation()}
      onClick={(event) => {
        event.stopPropagation();
        onSelect();
      }}
    >
      <span>{index}</span>
    </button>
  );
}

function DrawingGeometry({
  geometry,
  markerKey,
}: {
  geometry: DrawingAnnotationGeometry;
  markerKey: string;
}) {
  const markerPrefix = `drawing-${markerKey.replace(/[^a-zA-Z0-9_-]/g, "")}`;
  return (
    <svg
      className="public-image-drawing-svg"
      viewBox="0 0 1000 1000"
      preserveAspectRatio="none"
      aria-hidden="true"
    >
      <defs>
        {geometry.elements.map((element, elementIndex) =>
          element.tool === "arrow" ? (
            <marker
              id={`${markerPrefix}-arrow-${elementIndex}`}
              key={`${markerPrefix}-arrow-${elementIndex}`}
              markerWidth="5"
              markerHeight="5"
              refX="4.4"
              refY="2.5"
              orient="auto"
            >
              <path d="M0,0 L5,2.5 L0,5 Z" fill={element.color} />
            </marker>
          ) : null,
        )}
      </defs>
      {geometry.elements.map((element, elementIndex) =>
        renderDrawingElement(
          element,
          `${markerPrefix}-${elementIndex}`,
          `${markerPrefix}-arrow-${elementIndex}`,
        ),
      )}
    </svg>
  );
}

function renderDrawingElement(
  element: DrawingElement,
  key: string,
  arrowMarkerId: string,
) {
  if (element.tool === "rect") {
    return (
      <rect
        key={key}
        x={toSvgUnit(element.x)}
        y={toSvgUnit(element.y)}
        width={toSvgUnit(element.width)}
        height={toSvgUnit(element.height)}
        fill="none"
        stroke={element.color}
        strokeWidth={element.strokeWidth}
        vectorEffect="non-scaling-stroke"
      />
    );
  }
  if (element.tool === "arrow") {
    const [start, end] = element.points;
    return (
      <line
        key={key}
        x1={toSvgUnit(start.x)}
        y1={toSvgUnit(start.y)}
        x2={toSvgUnit(end.x)}
        y2={toSvgUnit(end.y)}
        fill="none"
        stroke={element.color}
        strokeLinecap="round"
        strokeWidth={element.strokeWidth}
        vectorEffect="non-scaling-stroke"
        markerEnd={`url(#${arrowMarkerId})`}
      />
    );
  }
  return (
    <path
      key={key}
      d={drawingPath(element.points)}
      fill="none"
      stroke={element.color}
      strokeLinecap="round"
      strokeLinejoin="round"
      strokeWidth={element.strokeWidth}
      vectorEffect="non-scaling-stroke"
    />
  );
}

function drawingPath(points: Array<{ x: number; y: number }>) {
  return points
    .map((point, index) => {
      const prefix = index === 0 ? "M" : "L";
      return `${prefix}${toSvgUnit(point.x)} ${toSvgUnit(point.y)}`;
    })
    .join(" ");
}

function drawingLabelStyle(geometry: DrawingAnnotationGeometry): CSSProperties {
  const point = drawingAnchorPoint(geometry);
  return {
    left: `${point.x * 100}%`,
    top: `${point.y * 100}%`,
  };
}

function drawingAnchorPoint(geometry: DrawingAnnotationGeometry) {
  for (const element of geometry.elements) {
    if (element.tool === "rect") {
      return { x: element.x, y: element.y };
    }
    if (element.points[0]) {
      return element.points[0];
    }
  }
  return { x: 0, y: 0 };
}

function toSvgUnit(value: number) {
  return Math.round(Math.min(1, Math.max(0, value)) * 1000);
}

function imageGeometryStyle(geometry: AnnotationGeometry): CSSProperties {
  if (geometry.shape === "point") {
    return {
      left: `${geometry.x * 100}%`,
      top: `${geometry.y * 100}%`,
    };
  }
  if (geometry.shape === "drawing") {
    return {};
  }
  return {
    left: `${geometry.x * 100}%`,
    top: `${geometry.y * 100}%`,
    width: `${geometry.width * 100}%`,
    height: `${geometry.height * 100}%`,
  };
}

function formatCommentDate(value: string) {
  return new Intl.DateTimeFormat("zh-CN", {
    month: "numeric",
    day: "numeric",
    hour: "2-digit",
    minute: "2-digit",
  }).format(new Date(value));
}

function threadStatusLabel(status: string) {
  return status === "resolved" ? "已解决" : "待处理";
}

function formatDate(value: string) {
  return new Intl.DateTimeFormat("zh-CN", {
    year: "numeric",
    month: "short",
    day: "numeric",
    hour: "2-digit",
    minute: "2-digit",
  }).format(new Date(value));
}
