import {
  type Dispatch,
  type DragEvent,
  type SetStateAction,
  useEffect,
  useMemo,
  useRef,
  useState,
} from "react";
import {
  AlertTriangle,
  Copy,
  Download,
  Eye,
  ListPlus,
  MoreVertical,
  MoveRight,
  RefreshCcw,
  Share2,
  X,
} from "lucide-react";
import type {
  AssetVersion,
  Job,
  MediaLibraryAsset,
  MediaLibraryPage,
  MediaLibraryQuery,
  MediaLibraryState,
  MediaProbe,
  Project,
  ProjectAsset,
  Rendition,
  StorageObject,
  UploadCheck,
} from "@review-studio/contracts";
import { HlsVideo } from "./HlsVideo";
import { useI18n } from "./lib/i18n-react";
import {
  assignMediaAssetProject,
  copyAssetToProject,
  getProjectUploadTarget,
  listAssetVersions,
  listProjectAssetTrash,
  queryProjectMediaCandidates,
  queryProjectMediaLibrary,
  restoreProjectAsset,
  setCurrentAssetVersion,
  moveAssetToProject,
  trashAssetFromProject,
  updateMediaAssetName,
  uploadAssetVersion,
} from "./api/storage";
import { retryJob } from "./api/jobs";
import { listProjects } from "./api/catalog";
import type { ReviewCreationSeed } from "./ReviewWorkspace";

type MediaViewMode = "grid" | "list";
type MediaItemState =
  | "ready"
  | "ingested"
  | "preview_ready"
  | "optimizing"
  | "complete"
  | "enhancement_failed"
  | "processing"
  | "failed"
  | "missing"
  | "waiting"
  | "quarantined";
type MediaSort = NonNullable<MediaLibraryQuery["sort"]>;
type MediaTypeFilter = NonNullable<MediaLibraryQuery["mediaType"]>;
type UploadSecurityTone = "success" | "warning" | "danger" | "neutral";

interface MediaItem {
  object: StorageObject;
  asset: MediaLibraryAsset | undefined;
  probe: MediaProbe | undefined;
  mediaType: MediaProbe["mediaType"] | "other";
  thumbnail: Rendition | undefined;
  preview: Rendition | undefined;
  storyboard: Rendition | undefined;
  jobs: Job[];
  failedJobs: Job[];
  failedRenditions: Rendition[];
  state: MediaItemState;
  stateLabel: string;
  progressLabel: string | null;
  errorMessage: string | null;
  uploadCheck: UploadCheck | undefined;
}

interface UploadProgressState {
  filename: string;
  label: string;
  loaded: number;
  total: number | null;
  percent: number | null;
  phase: "uploading" | "processing";
}

interface UploadNoticeState {
  filename: string;
  title: string;
  detail: string;
  tone: UploadSecurityTone;
}

export function MediaWorkspace({
  project,
  initialAssetId,
  onCreateReview,
  uploadRevision,
  onQueueUploads,
}: {
  project: Project;
  initialAssetId?: string | null;
  onCreateReview: (seed: ReviewCreationSeed) => void;
  uploadRevision: number;
  onQueueUploads: (files: FileList | File[]) => void;
}) {
  const { t } = useI18n();
  const projectSurface = project;
  const fixedProjectId = project.id;
  const [objects, setObjects] = useState<StorageObject[]>([]);
  const [assetsByObject, setAssetsByObject] = useState<
    Record<string, MediaLibraryAsset>
  >({});
  const [uploadChecksByObject, setUploadChecksByObject] = useState<
    Record<string, UploadCheck>
  >({});
  const [projects, setProjects] = useState<Project[]>([]);
  const [probes, setProbes] = useState<MediaProbe[]>([]);
  const [renditions, setRenditions] = useState<Rendition[]>([]);
  const [renditionJobs, setRenditionJobs] = useState<Job[]>([]);
  const [videoRenditionJobs, setVideoRenditionJobs] = useState<Job[]>([]);
  const [versionJobs, setVersionJobs] = useState<Job[]>([]);
  const [assetVersions, setAssetVersions] = useState<AssetVersion[]>([]);
  const [selectedVersionId, setSelectedVersionId] = useState<string | null>(
    null,
  );
  const [loadingVersions, setLoadingVersions] = useState(false);
  const [viewMode, setViewMode] = useState<MediaViewMode>("grid");
  const [selectedObjectId, setSelectedObjectId] = useState<string | null>(null);
  const [searchDraft, setSearchDraft] = useState("");
  const [search, setSearch] = useState("");
  const [mediaType, setMediaType] = useState<MediaTypeFilter>("all");
  const [mediaState, setMediaState] = useState<MediaLibraryState | "all">(
    "all",
  );
  const [projectId, setProjectId] = useState(fixedProjectId ?? "all");
  const [projectMediaPanel, setProjectMediaPanel] = useState<
    "library" | "add" | "trash"
  >("library");
  const [modifiedFrom, setModifiedFrom] = useState("");
  const [modifiedTo, setModifiedTo] = useState("");
  const [sort, setSort] = useState<MediaSort>("modified_desc");
  const [page, setPage] = useState(1);
  const [pageSize] = useState(48);
  const [totalObjects, setTotalObjects] = useState(0);
  const [refreshVersion, setRefreshVersion] = useState(0);
  const [loading, setLoading] = useState(true);
  const [loadingObjects, setLoadingObjects] = useState(false);
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState<string | null>(null);
  const [uploadProgress, setUploadProgress] =
    useState<UploadProgressState | null>(null);
  const [uploadNotice, setUploadNotice] = useState<UploadNoticeState | null>(
    null,
  );
  const [projectUploadTargetLoading, setProjectUploadTargetLoading] =
    useState(false);
  const [projectUploadTargetError, setProjectUploadTargetError] = useState<
    string | null
  >(null);
  const [isProjectDropActive, setProjectDropActive] = useState(false);
  const [trashItems, setTrashItems] = useState<ProjectAsset[]>([]);
  const [trashLoading, setTrashLoading] = useState(false);
  const [trashError, setTrashError] = useState<string | null>(null);
  const [trashNotice, setTrashNotice] = useState<string | null>(null);
  const [candidatePage, setCandidatePage] = useState<MediaLibraryPage | null>(
    null,
  );
  const [candidateSearchDraft, setCandidateSearchDraft] = useState("");
  const [candidateSearch, setCandidateSearch] = useState("");
  const [candidateMediaType, setCandidateMediaType] =
    useState<MediaTypeFilter>("all");
  const [candidateLoading, setCandidateLoading] = useState(false);
  const [candidateError, setCandidateError] = useState<string | null>(null);
  const [candidateNotice, setCandidateNotice] = useState<string | null>(null);
  const openedInitialAssetId = useRef<string | null>(null);
  const uploadNoticeTimerRef = useRef<number | null>(null);
  const observedUploadRevision = useRef(uploadRevision);

  useEffect(() => {
    setProjectId(fixedProjectId ?? "all");
    setPage(1);
  }, [fixedProjectId]);

  useEffect(() => {
    if (observedUploadRevision.current === uploadRevision) return;
    observedUploadRevision.current = uploadRevision;
    resetLibraryFiltersForUpload();
    setRefreshVersion((current) => current + 1);
  }, [uploadRevision]);

  useEffect(
    () => () => {
      if (uploadNoticeTimerRef.current !== null) {
        window.clearTimeout(uploadNoticeTimerRef.current);
      }
    },
    [],
  );

  useEffect(() => {
    const controller = new AbortController();
    setProjectUploadTargetLoading(true);
    setProjectUploadTargetError(null);
    getProjectUploadTarget(fixedProjectId, {}, controller.signal)
      .then(() => setProjectUploadTargetError(null))
      .catch((loadError: unknown) => {
        if (!isAbort(loadError)) {
          setProjectUploadTargetError(
            message(loadError, "无法读取当前项目的上传位置"),
          );
        }
      })
      .finally(() => setProjectUploadTargetLoading(false));
    return () => controller.abort();
  }, [fixedProjectId]);

  const libraryQuery = useMemo<MediaLibraryQuery>(
    () => ({
      search,
      mediaType,
      state: mediaState,
      projectId,
      modifiedFrom,
      modifiedTo,
      sort,
      page,
      pageSize,
    }),
    [
      mediaState,
      mediaType,
      modifiedFrom,
      modifiedTo,
      page,
      pageSize,
      projectId,
      search,
      sort,
    ],
  );

  useEffect(() => {
    const timer = window.setTimeout(() => {
      setSearch(searchDraft.trim());
      setPage(1);
    }, 250);
    return () => window.clearTimeout(timer);
  }, [searchDraft]);

  useEffect(() => {
    const timer = window.setTimeout(() => {
      setCandidateSearch(candidateSearchDraft.trim());
    }, 250);
    return () => window.clearTimeout(timer);
  }, [candidateSearchDraft]);

  useEffect(() => {
    const controller = new AbortController();
    listProjects(controller.signal)
      .then((nextProjects) => {
        setProjects(nextProjects);
      })
      .catch((loadError: unknown) => {
        if (!isAbort(loadError)) {
          setError(message(loadError, "无法读取项目列表"));
        }
      })
      .finally(() => setLoading(false));
    return () => controller.abort();
  }, []);

  useEffect(() => {
    const controller = new AbortController();
    setLoadingObjects(true);
    queryProjectMediaLibrary(fixedProjectId, libraryQuery, controller.signal)
      .then((library) => {
        applyLibraryPage(
          library,
          setObjects,
          setProbes,
          setRenditions,
          setAssetsByObject,
          setUploadChecksByObject,
          setRenditionJobs,
          setVideoRenditionJobs,
          setVersionJobs,
        );
        setTotalObjects(library.total);
        setSelectedObjectId((current) =>
          current && library.items.some((item) => item.object.id === current)
            ? current
            : null,
        );
      })
      .catch((loadError: unknown) => {
        if (!isAbort(loadError)) {
          setError(message(loadError, "无法读取目录内容"));
        }
      })
      .finally(() => setLoadingObjects(false));
    return () => controller.abort();
  }, [fixedProjectId, libraryQuery, refreshVersion]);

  useEffect(() => {
    if (!fixedProjectId || projectMediaPanel !== "trash") {
      return;
    }
    const controller = new AbortController();
    setTrashLoading(true);
    setTrashError(null);
    listProjectAssetTrash(fixedProjectId, controller.signal)
      .then(setTrashItems)
      .catch((loadError: unknown) => {
        if (!isAbort(loadError)) {
          setTrashError(message(loadError, "无法读取项目回收站"));
        }
      })
      .finally(() => setTrashLoading(false));
    return () => controller.abort();
  }, [fixedProjectId, projectMediaPanel, refreshVersion]);

  useEffect(() => {
    if (!fixedProjectId || projectMediaPanel !== "add") {
      return;
    }
    const controller = new AbortController();
    setCandidateLoading(true);
    setCandidateError(null);
    queryProjectMediaCandidates(
      fixedProjectId,
      {
        search: candidateSearch,
        mediaType: candidateMediaType,
        sort: "modified_desc",
        page: 1,
        pageSize: 24,
      },
      controller.signal,
    )
      .then(setCandidatePage)
      .catch((loadError: unknown) => {
        if (!isAbort(loadError)) {
          setCandidateError(message(loadError, "无法读取已有媒体"));
        }
      })
      .finally(() => setCandidateLoading(false));
    return () => controller.abort();
  }, [
    candidateMediaType,
    candidateSearch,
    fixedProjectId,
    projectMediaPanel,
    refreshVersion,
  ]);

  useEffect(() => {
    if (
      !renditionJobs.some(isActiveJob) &&
      !videoRenditionJobs.some(isActiveJob) &&
      !versionJobs.some(isActiveJob)
    ) {
      return;
    }
    const controller = new AbortController();
    const timer = window.setTimeout(() => {
      queryProjectMediaLibrary(fixedProjectId, libraryQuery, controller.signal)
        .then((library) => {
          applyLibraryPage(
            library,
            setObjects,
            setProbes,
            setRenditions,
            setAssetsByObject,
            setUploadChecksByObject,
            setRenditionJobs,
            setVideoRenditionJobs,
            setVersionJobs,
          );
          setTotalObjects(library.total);
        })
        .catch((loadError: unknown) => {
          if (!isAbort(loadError)) {
            setError(message(loadError, "无法更新图片预览状态"));
          }
        });
    }, 650);
    return () => {
      controller.abort();
      window.clearTimeout(timer);
    };
  }, [
    fixedProjectId,
    libraryQuery,
    renditionJobs,
    versionJobs,
    videoRenditionJobs,
  ]);

  function upsertProcessingJob(updated: Job) {
    const updateList = (current: Job[]) => [
      updated,
      ...current.filter((item) => item.id !== updated.id),
    ];
    if (updated.type === "media.generate_image_renditions") {
      setRenditionJobs(updateList);
      return;
    }
    if (
      updated.type === "media.generate_video_renditions" ||
      updated.type === "media.generate_video_enhancements"
    ) {
      setVideoRenditionJobs(updateList);
      return;
    }
    if (updated.type === "media.process_asset_version") {
      setVersionJobs(updateList);
    }
  }

  async function retryProcessingJob(jobId: string): Promise<Job> {
    setBusy(true);
    setError(null);
    try {
      const updated = await retryJob(jobId);
      upsertProcessingJob(updated);
      setRefreshVersion((current) => current + 1);
      return updated;
    } catch (retryError) {
      const fallback = "无法重试这个处理任务";
      setError(message(retryError, fallback));
      throw retryError;
    } finally {
      setBusy(false);
    }
  }

  async function assignProject(item: MediaItem, nextProjectId: string) {
    setBusy(true);
    setError(null);
    try {
      const asset = await assignMediaAssetProject(
        item.object.id,
        nextProjectId || null,
      );
      setAssetsByObject((current) => ({
        ...current,
        [item.object.id]: asset,
      }));
      setProjects(await listProjects());
      if (projectId !== "all") {
        setRefreshVersion((current) => current + 1);
      }
    } catch (assignError) {
      setError(message(assignError, "无法更新媒体所属项目"));
    } finally {
      setBusy(false);
    }
  }

  async function renameMediaAsset(item: MediaItem, name: string) {
    if (!item.asset) return;
    setBusy(true);
    setError(null);
    try {
      const asset = await updateMediaAssetName(item.asset, name);
      setAssetsByObject((current) => ({
        ...current,
        [item.object.id]: asset,
      }));
      setRefreshVersion((current) => current + 1);
    } catch (renameError) {
      setError(message(renameError, "无法重命名媒体"));
      throw renameError;
    } finally {
      setBusy(false);
    }
  }

  async function copyMediaToProject(item: MediaItem, targetProjectId: string) {
    if (!item.asset || !targetProjectId) return;
    setBusy(true);
    setError(null);
    try {
      await copyAssetToProject(targetProjectId, { assetId: item.asset.id });
      setProjects(await listProjects());
      setRefreshVersion((current) => current + 1);
    } catch (copyError) {
      setError(message(copyError, "无法复制媒体到目标项目"));
      throw copyError;
    } finally {
      setBusy(false);
    }
  }

  async function moveMediaToProject(
    item: MediaItem,
    sourceProjectId: string,
    targetProjectId: string,
  ) {
    if (!item.asset || !sourceProjectId || !targetProjectId) return;
    setBusy(true);
    setError(null);
    try {
      await moveAssetToProject(sourceProjectId, item.asset.id, {
        targetProjectId,
      });
      setProjects(await listProjects());
      setRefreshVersion((current) => current + 1);
    } catch (moveError) {
      setError(message(moveError, "无法移动媒体到目标项目"));
      throw moveError;
    } finally {
      setBusy(false);
    }
  }

  async function removeMediaFromProject(
    item: MediaItem,
    sourceProjectId: string,
  ) {
    if (!item.asset || !sourceProjectId) return;
    setBusy(true);
    setError(null);
    try {
      await trashAssetFromProject(sourceProjectId, item.asset.id);
      setProjects(await listProjects());
      setRefreshVersion((current) => current + 1);
      if (projectSurface && fixedProjectId === sourceProjectId) {
        setSelectedObjectId(null);
        setProjectMediaPanel("trash");
        setTrashNotice("已移入项目回收站。");
      }
    } catch (trashError) {
      setError(message(trashError, "无法从当前项目移除媒体"));
      throw trashError;
    } finally {
      setBusy(false);
    }
  }

  async function addExistingMediaToProject(item: MediaItem) {
    if (!fixedProjectId || !item.asset) return;
    setBusy(true);
    setCandidateNotice(null);
    setCandidateError(null);
    try {
      await copyAssetToProject(fixedProjectId, { assetId: item.asset.id });
      setCandidateNotice(`已把“${mediaDisplayName(item)}”加入当前项目`);
      setRefreshVersion((current) => current + 1);
    } catch (copyError) {
      setCandidateError(message(copyError, "无法把这个媒体加入项目"));
    } finally {
      setBusy(false);
    }
  }

  async function restoreMediaToProject(item: ProjectAsset) {
    if (!fixedProjectId) return;
    setBusy(true);
    setTrashNotice(null);
    setTrashError(null);
    try {
      await restoreProjectAsset(fixedProjectId, item.assetId);
      setTrashNotice(`已恢复“${item.assetName}”到当前项目`);
      setRefreshVersion((current) => current + 1);
    } catch (restoreError) {
      setTrashError(message(restoreError, "无法恢复这个媒体"));
    } finally {
      setBusy(false);
    }
  }

  async function switchCurrentVersion(item: MediaItem, versionId: string) {
    if (!item.asset) return;
    setBusy(true);
    setError(null);
    try {
      const asset = await setCurrentAssetVersion(item.asset, versionId);
      setAssetsByObject((current) => ({
        ...current,
        [item.object.id]: asset,
      }));
      const versions = await listAssetVersions(asset.id);
      setAssetVersions(versions);
      setSelectedVersionId(asset.versionId);
      setRefreshVersion((current) => current + 1);
    } catch (switchError) {
      setError(message(switchError, "Unable to switch current version"));
    } finally {
      setBusy(false);
    }
  }

  function showUploadProgress(
    label: string,
    file: File,
    phase: UploadProgressState["phase"] = "uploading",
  ) {
    setUploadProgress({
      filename: file.name,
      label,
      loaded: 0,
      total: file.size > 0 ? file.size : null,
      percent: file.size > 0 ? 0 : null,
      phase,
    });
  }

  function updateUploadProgress(
    progress: Pick<UploadProgressState, "loaded" | "total" | "percent">,
  ) {
    setUploadProgress((current) =>
      current
        ? {
            ...current,
            loaded: progress.loaded,
            total: progress.total,
            percent: progress.percent,
            phase: "uploading",
          }
        : current,
    );
  }

  function markUploadProcessing() {
    setUploadProgress((current) =>
      current
        ? {
            ...current,
            loaded: current.total ?? current.loaded,
            percent: 100,
            phase: "processing",
          }
        : current,
    );
  }

  function showUploadNotice(
    notice: UploadNoticeState | null,
    autoDismissMs?: number,
  ) {
    if (uploadNoticeTimerRef.current !== null) {
      window.clearTimeout(uploadNoticeTimerRef.current);
      uploadNoticeTimerRef.current = null;
    }
    setUploadNotice(notice);
    if (notice && autoDismissMs) {
      uploadNoticeTimerRef.current = window.setTimeout(() => {
        setUploadNotice(null);
        uploadNoticeTimerRef.current = null;
      }, autoDismissMs);
    }
  }

  function resetLibraryFiltersForUpload() {
    setSearchDraft("");
    setSearch("");
    setMediaType("all");
    setMediaState("all");
    setProjectId(fixedProjectId ?? "all");
    setModifiedFrom("");
    setModifiedTo("");
    setSort("modified_desc");
    setPage(1);
  }

  async function ensureProjectUploadTarget(
    projectId: string | null,
    file: File,
  ) {
    if (!projectId) return;
    await getProjectUploadTarget(projectId, { sizeBytes: file.size });
  }

  function uploadProjectFiles(files: FileList | File[]) {
    if (projectSurface && projectUploadTargetLoading) {
      return;
    }
    if (projectSurface && projectUploadTargetError) {
      setError(projectUploadTargetError);
      return;
    }
    onQueueUploads(files);
  }

  function dragHasFiles(event: DragEvent<HTMLElement>) {
    return Array.from(event.dataTransfer.types).includes("Files");
  }

  function handleProjectDragEnter(event: DragEvent<HTMLElement>) {
    if (
      !projectSurface ||
      projectUploadTargetLoading ||
      projectUploadTargetError ||
      !dragHasFiles(event)
    )
      return;
    event.preventDefault();
    event.dataTransfer.dropEffect = "copy";
    setProjectDropActive(true);
  }

  function handleProjectDragOver(event: DragEvent<HTMLElement>) {
    if (
      !projectSurface ||
      projectUploadTargetLoading ||
      projectUploadTargetError ||
      !dragHasFiles(event)
    )
      return;
    event.preventDefault();
    event.dataTransfer.dropEffect = "copy";
    setProjectDropActive(true);
  }

  function handleProjectDragLeave(event: DragEvent<HTMLElement>) {
    if (!projectSurface) return;
    const nextTarget = event.relatedTarget as Node | null;
    if (!nextTarget || !event.currentTarget.contains(nextTarget)) {
      setProjectDropActive(false);
    }
  }

  function handleProjectDrop(event: DragEvent<HTMLElement>) {
    if (
      !projectSurface ||
      projectUploadTargetLoading ||
      projectUploadTargetError ||
      !dragHasFiles(event)
    )
      return;
    event.preventDefault();
    setProjectDropActive(false);
    uploadProjectFiles(event.dataTransfer.files);
  }

  async function uploadVersion(item: MediaItem, file: File) {
    if (!item.asset) return;
    setBusy(true);
    setError(null);
    setUploadProgress(null);
    showUploadNotice(null);
    try {
      await ensureProjectUploadTarget(
        item.asset.projectId ?? fixedProjectId,
        file,
      );
      showUploadProgress("上传新版本", file);
      const version = await uploadAssetVersion(item.asset, file, {
        onProgress: updateUploadProgress,
      });
      markUploadProcessing();
      const asset: MediaLibraryAsset = {
        ...item.asset,
        type:
          version.probe?.mediaType ??
          inferMediaType(version.sourceMime ?? file.type),
        revision: item.asset.revision + 1,
        versionId: version.id,
        versionNumber: version.versionNumber,
      };
      const nextObject: StorageObject = {
        ...item.object,
        id: version.storageObjectId,
        authorizedRootId: version.authorizedRootId,
        objectKey: version.storageObjectKey,
        status:
          version.storageObjectStatus === "missing" ? "missing" : "available",
        sizeBytes: version.sourceSizeBytes,
        modifiedAt: version.createdAt,
        mimeType: version.sourceMime ?? file.type ?? "application/octet-stream",
        firstDiscoveredAt: version.createdAt,
        lastSeenAt: version.createdAt,
        missingSince: null,
      };
      setObjects((current) =>
        current.map((candidate) =>
          candidate.id === item.object.id ? nextObject : candidate,
        ),
      );
      setAssetsByObject((current) => {
        const next = { ...current };
        delete next[item.object.id];
        next[nextObject.id] = asset;
        return next;
      });
      setUploadChecksByObject((current) => {
        const next = { ...current };
        delete next[item.object.id];
        if (version.uploadCheck) {
          next[nextObject.id] = version.uploadCheck;
        }
        return next;
      });
      setProbes((current) =>
        current.filter((probe) => probe.storageObjectId !== item.object.id),
      );
      setRenditions((current) =>
        current.filter(
          (rendition) => rendition.sourceStorageObjectId !== item.object.id,
        ),
      );
      setSelectedObjectId(nextObject.id);
      setAssetVersions((current) => [
        version,
        ...current.map((candidate) => ({
          ...candidate,
          isCurrent: false,
        })),
      ]);
      setSelectedVersionId(version.id);
      showUploadNotice(uploadNoticeFromCheck(version.uploadCheck, file.name));
      setRefreshVersion((current) => current + 1);
    } catch (uploadError) {
      if (isUploadQuotaExceeded(uploadError)) {
        showUploadNotice(
          {
            filename: file.name,
            title: "文件超过容量上限",
            detail: message(
              uploadError,
              "这个文件超过当前项目上传位置的容量限制。",
            ),
            tone: "danger",
          },
          5200,
        );
      } else {
        setError(message(uploadError, "无法上传新版本"));
      }
    } finally {
      setBusy(false);
      window.setTimeout(() => setUploadProgress(null), 500);
    }
  }

  const probesByObject = useMemo(
    () => new Map(probes.map((probe) => [probe.storageObjectId, probe])),
    [probes],
  );
  const renditionsByObject = useMemo(() => {
    const result = new Map<string, Rendition[]>();
    for (const rendition of renditions) {
      const current = result.get(rendition.sourceStorageObjectId) ?? [];
      current.push(rendition);
      result.set(rendition.sourceStorageObjectId, current);
    }
    return result;
  }, [renditions]);
  const jobsByObject = useMemo(() => {
    const result = new Map<string, Job[]>();
    for (const item of [
      ...renditionJobs,
      ...videoRenditionJobs,
      ...versionJobs,
    ]) {
      const current = result.get(item.subject.id) ?? [];
      current.push(item);
      result.set(item.subject.id, current);
    }
    return result;
  }, [renditionJobs, versionJobs, videoRenditionJobs]);
  const mediaItems = useMemo(
    () =>
      objects.map((object) =>
        buildMediaItem(
          object,
          probesByObject.get(object.id),
          renditionsByObject.get(object.id) ?? [],
          jobsByObject.get(object.id) ?? [],
          assetsByObject[object.id],
          uploadChecksByObject[object.id],
        ),
      ),
    [
      assetsByObject,
      jobsByObject,
      objects,
      probesByObject,
      renditionsByObject,
      uploadChecksByObject,
    ],
  );
  const candidateItems = useMemo(
    () =>
      candidatePage?.items.map((item) =>
        buildMediaItem(
          item.object,
          item.probe ?? undefined,
          item.renditions,
          item.jobs,
          item.asset ?? undefined,
          item.uploadCheck ?? undefined,
        ),
      ) ?? [],
    [candidatePage],
  );
  useEffect(() => {
    if (!initialAssetId || openedInitialAssetId.current === initialAssetId) {
      return;
    }
    const item = mediaItems.find(
      (candidate) => candidate.asset?.id === initialAssetId,
    );
    if (item) {
      openedInitialAssetId.current = initialAssetId;
      setSelectedObjectId(item.object.id);
    }
  }, [initialAssetId, mediaItems]);
  const selectedMediaItem = useMemo(
    () =>
      mediaItems.find((item) => item.object.id === selectedObjectId) ?? null,
    [mediaItems, selectedObjectId],
  );
  useEffect(() => {
    if (!selectedMediaItem?.asset) {
      setAssetVersions([]);
      setSelectedVersionId(null);
      setLoadingVersions(false);
      return;
    }
    const controller = new AbortController();
    setLoadingVersions(true);
    listAssetVersions(selectedMediaItem.asset.id, controller.signal)
      .then((items) => {
        setAssetVersions(items);
        setSelectedVersionId(
          items.find((version) => version.isCurrent)?.id ??
            selectedMediaItem.asset?.versionId ??
            items[0]?.id ??
            null,
        );
      })
      .catch((loadError: unknown) => {
        if (!isAbort(loadError)) {
          setError(message(loadError, "Unable to load asset versions"));
        }
      })
      .finally(() => setLoadingVersions(false));
    return () => controller.abort();
  }, [selectedMediaItem?.asset?.id, selectedMediaItem?.asset?.versionId]);

  useEffect(() => {
    if (!selectedMediaItem?.asset || !versionJobs.some(isActiveJob)) {
      return;
    }
    const controller = new AbortController();
    const timer = window.setTimeout(() => {
      listAssetVersions(selectedMediaItem.asset!.id, controller.signal)
        .then(setAssetVersions)
        .catch((loadError: unknown) => {
          if (!isAbort(loadError)) {
            setError(message(loadError, "无法更新版本处理状态"));
          }
        });
    }, 700);
    return () => {
      controller.abort();
      window.clearTimeout(timer);
    };
  }, [selectedMediaItem?.asset, versionJobs]);

  const mediaMetrics = useMemo(
    () => ({
      ready: mediaItems.filter((item) =>
        [
          "ready",
          "preview_ready",
          "optimizing",
          "complete",
          "enhancement_failed",
        ].includes(item.state),
      ).length,
      processing: mediaItems.filter((item) => item.state === "processing")
        .length,
      attention: mediaItems.filter(
        (item) =>
          item.state === "failed" ||
          item.state === "missing" ||
          item.state === "quarantined" ||
          item.state === "enhancement_failed",
      ).length,
      securityWarnings: mediaItems.filter(
        (item) =>
          shouldSurfaceUploadCheck(item.uploadCheck) &&
          uploadCheckTone(item.uploadCheck) === "warning",
      ).length,
      securityBlocked: mediaItems.filter(
        (item) =>
          shouldSurfaceUploadCheck(item.uploadCheck) &&
          uploadCheckTone(item.uploadCheck) === "danger",
      ).length,
    }),
    [mediaItems],
  );

  useEffect(() => {
    if (!selectedMediaItem) return;
    function closeOnEscape(event: KeyboardEvent) {
      if (event.key === "Escape") {
        setSelectedObjectId(null);
      }
    }
    window.addEventListener("keydown", closeOnEscape);
    return () => window.removeEventListener("keydown", closeOnEscape);
  }, [selectedMediaItem]);

  function sendMediaToReview(
    item: MediaItem,
    intent: ReviewCreationSeed["intent"],
  ) {
    if (!item.asset || !fixedProjectId) {
      setError("请先把资产加入当前项目");
      return;
    }
    onCreateReview({
      projectId: fixedProjectId,
      assetId: item.asset.id,
      assetVersionId: item.asset.versionId,
      assetName: item.asset.name,
      versionNumber: item.asset.versionNumber,
      intent,
    });
  }

  const detailPanel = selectedMediaItem ? (
    <MediaDetailPanel
      item={selectedMediaItem}
      projects={projects}
      activeProjectId={fixedProjectId}
      currentProject={projectSurface}
      versions={assetVersions}
      selectedVersionId={selectedVersionId}
      loadingVersions={loadingVersions}
      busy={busy}
      onClose={() => setSelectedObjectId(null)}
      onPreviewVersion={setSelectedVersionId}
      onSetCurrentVersion={(versionId) =>
        void switchCurrentVersion(selectedMediaItem, versionId)
      }
      onRenameAsset={(name) => renameMediaAsset(selectedMediaItem, name)}
      onUploadVersion={(file) => void uploadVersion(selectedMediaItem, file)}
      onCreateReview={(version) => {
        const asset = selectedMediaItem.asset;
        const reviewProjectId = fixedProjectId ?? asset?.projectId ?? null;
        if (!reviewProjectId) {
          setError("请先把资产归入项目，再创建审阅");
          return;
        }
        onCreateReview({
          projectId: reviewProjectId,
          assetId: asset?.id ?? version.assetId,
          assetVersionId: version.id,
          assetName: asset?.name ?? fileName(version.sourceFilename),
          versionNumber: version.versionNumber,
          intent: "create",
        });
      }}
      onAssignProject={(nextProjectId) =>
        void assignProject(selectedMediaItem, nextProjectId)
      }
      onCopyToProject={(targetProjectId) =>
        copyMediaToProject(selectedMediaItem, targetProjectId)
      }
      onMoveToProject={(sourceProjectId, targetProjectId) =>
        moveMediaToProject(selectedMediaItem, sourceProjectId, targetProjectId)
      }
      onRemoveFromProject={(sourceProjectId) =>
        removeMediaFromProject(selectedMediaItem, sourceProjectId)
      }
      onRetryJob={retryProcessingJob}
    />
  ) : null;

  return (
    <section
      className={`project-media-workspace${
        isProjectDropActive ? " is-drop-active" : ""
      }`}
      onDragEnter={handleProjectDragEnter}
      onDragOver={handleProjectDragOver}
      onDragLeave={handleProjectDragLeave}
      onDrop={handleProjectDrop}
    >
      <div className="project-media-toolbar">
        <div>
          <p className="studio-kicker">PROJECT MEDIA</p>
          <h2>{t("projectMedia.title")}</h2>
        </div>
        <div className="project-media-actions">
          <label className="upload-file-button">
            <input
              type="file"
              multiple
              disabled={
                projectUploadTargetLoading || Boolean(projectUploadTargetError)
              }
              onChange={(event) => {
                const files = event.target.files;
                if (files?.length) {
                  uploadProjectFiles(files);
                }
                event.target.value = "";
              }}
            />
            {t("projectMedia.localUpload")}
          </label>
          <button
            className="secondary-button"
            type="button"
            onClick={() => setProjectMediaPanel("add")}
          >
            {t("projectMedia.addExisting")}
          </button>
        </div>
      </div>

      {projectUploadTargetError ? (
        <ProjectUploadTargetNotice error={projectUploadTargetError} />
      ) : null}

      {error ? (
        <p className="catalog-error" role="alert">
          {error}
        </p>
      ) : null}
      {uploadProgress ? (
        <UploadProgressBanner progress={uploadProgress} />
      ) : null}
      {uploadNotice ? (
        <UploadNoticeBanner
          notice={uploadNotice}
          onDismiss={() => showUploadNotice(null)}
        />
      ) : null}
      {isProjectDropActive ? (
        <div className="project-drop-hint" aria-hidden="true">
          <strong>{t("projectMedia.uploadHintTitle")}</strong>
          <span>{t("projectMedia.uploadHintDetail")}</span>
        </div>
      ) : null}

      <div className="project-media-tabs" aria-label={t("projectMedia.title")}>
        <button
          className={projectMediaPanel === "library" ? "is-active" : ""}
          type="button"
          onClick={() => setProjectMediaPanel("library")}
        >
          <span>{t("projectMedia.projectMedia")}</span>
          <strong>{totalObjects}</strong>
        </button>
        <button
          className={projectMediaPanel === "add" ? "is-active" : ""}
          type="button"
          onClick={() => setProjectMediaPanel("add")}
        >
          <span>{t("projectMedia.addExisting")}</span>
          <strong>{candidatePage?.total ?? 0}</strong>
        </button>
        <button
          className={projectMediaPanel === "trash" ? "is-active" : ""}
          type="button"
          onClick={() => setProjectMediaPanel("trash")}
        >
          <span>{t("projectMedia.trash")}</span>
          <strong>{trashItems.length}</strong>
        </button>
      </div>

      {projectMediaPanel === "library" ? (
        <div className="project-media-panel">
          <div className="project-media-headline">
            <div>
              <h3>{t("projectMedia.current")}</h3>
            </div>
            <div className="view-switcher" aria-label={t("projectMedia.title")}>
              <button
                className={viewMode === "grid" ? "is-active" : ""}
                type="button"
                aria-pressed={viewMode === "grid"}
                onClick={() => setViewMode("grid")}
              >
                {t("projectMedia.grid")}
              </button>
              <button
                className={viewMode === "list" ? "is-active" : ""}
                type="button"
                aria-pressed={viewMode === "list"}
                onClick={() => setViewMode("list")}
              >
                {t("projectMedia.list")}
              </button>
            </div>
          </div>
          <MediaLibraryFilters
            search={searchDraft}
            mediaType={mediaType}
            state={mediaState}
            projects={projects}
            projectId={projectSurface.id}
            modifiedFrom={modifiedFrom}
            modifiedTo={modifiedTo}
            sort={sort}
            projectFilterVisible={false}
            onSearchChange={setSearchDraft}
            onMediaTypeChange={(value) => {
              setMediaType(value);
              setPage(1);
            }}
            onStateChange={(value) => {
              setMediaState(value);
              setPage(1);
            }}
            onProjectChange={() => undefined}
            onModifiedFromChange={(value) => {
              setModifiedFrom(value);
              setPage(1);
            }}
            onModifiedToChange={(value) => {
              setModifiedTo(value);
              setPage(1);
            }}
            onSortChange={(value) => {
              setSort(value);
              setPage(1);
            }}
            onReset={() => {
              setSearchDraft("");
              setSearch("");
              setMediaType("all");
              setMediaState("all");
              setModifiedFrom("");
              setModifiedTo("");
              setSort("modified_desc");
              setPage(1);
            }}
          />
          <div className="media-library-summary" aria-label="媒体处理摘要">
            <span>
              <strong>{mediaMetrics.ready}</strong> {t("projectMedia.ready")}
            </span>
            <span>
              <strong>{mediaMetrics.processing}</strong>{" "}
              {t("projectMedia.processing")}
            </span>
            <span>
              <strong>{mediaMetrics.attention}</strong>{" "}
              {t("projectMedia.needsAttention")}
            </span>
            <span>
              <strong>{mediaMetrics.securityWarnings}</strong>{" "}
              {t("projectMedia.securityWarnings")}
            </span>
            <span>
              <strong>{mediaMetrics.securityBlocked}</strong>{" "}
              {t("projectMedia.securityBlocked")}
            </span>
          </div>
          {loadingObjects ? <MediaSkeleton /> : null}
          {!loadingObjects && mediaItems.length === 0 ? (
            <div className="collection-empty">
              <strong>
                {hasVisibleLibraryFilters(libraryQuery)
                  ? t("projectMedia.emptyFiltered")
                  : t("projectMedia.emptyProject")}
              </strong>
              <span>
                {hasVisibleLibraryFilters(libraryQuery)
                  ? t("projectMedia.emptyFilteredDetail")
                  : t("projectMedia.emptyProjectDetail")}
              </span>
            </div>
          ) : null}
          {!loadingObjects && mediaItems.length > 0 ? (
            <MediaBrowser
              items={mediaItems}
              mode={viewMode}
              projects={projects}
              activeProjectId={fixedProjectId}
              busy={busy}
              onSelect={(item) => setSelectedObjectId(item.object.id)}
              onReviewAction={sendMediaToReview}
              onCopyToProject={(item, targetProjectId) =>
                copyMediaToProject(item, targetProjectId)
              }
              onMoveToProject={(item, targetProjectId) => {
                if (fixedProjectId) {
                  return moveMediaToProject(
                    item,
                    fixedProjectId,
                    targetProjectId,
                  );
                }
                return Promise.resolve();
              }}
            />
          ) : null}
          {!loadingObjects && totalObjects > pageSize ? (
            <LibraryPagination
              page={page}
              pageSize={pageSize}
              total={totalObjects}
              onChange={setPage}
            />
          ) : null}
        </div>
      ) : projectMediaPanel === "add" ? (
        <ExistingMediaPanel
          search={candidateSearchDraft}
          mediaType={candidateMediaType}
          items={candidateItems}
          total={candidatePage?.total ?? 0}
          loading={candidateLoading}
          busy={busy}
          error={candidateError}
          notice={candidateNotice}
          onSearchChange={setCandidateSearchDraft}
          onMediaTypeChange={setCandidateMediaType}
          onAdd={(item) => void addExistingMediaToProject(item)}
        />
      ) : (
        <ProjectTrashPanel
          project={projectSurface}
          items={trashItems}
          loading={trashLoading}
          busy={busy}
          error={trashError}
          notice={trashNotice}
          onRestore={(item) => void restoreMediaToProject(item)}
        />
      )}
      {detailPanel}
    </section>
  );
}

function UploadProgressBanner({ progress }: { progress: UploadProgressState }) {
  const percent = progress.percent ?? 0;
  const visiblePercent =
    progress.phase === "processing"
      ? 100
      : percent === 0 && progress.loaded > 0
        ? 1
        : percent;
  const percentLabel =
    progress.phase === "processing"
      ? "处理中"
      : progress.percent === null
        ? "上传中"
        : progress.percent === 0 && progress.loaded > 0
          ? "<1%"
          : `${progress.percent}%`;
  const amount = progress.total
    ? `${formatBytes(progress.loaded)} / ${formatBytes(progress.total)}`
    : formatBytes(progress.loaded);
  return (
    <div className="upload-progress-banner" role="status" aria-live="polite">
      <div className="upload-progress-copy">
        <strong>{progress.label}</strong>
        <span>
          {progress.filename} ·{" "}
          {progress.phase === "processing"
            ? "上传完成，正在进行安全检查和预览排队"
            : amount}
        </span>
      </div>
      <div
        className="upload-progress-track"
        role="progressbar"
        aria-valuemin={0}
        aria-valuemax={100}
        aria-valuenow={progress.percent ?? undefined}
      >
        <span style={{ width: `${visiblePercent}%` }} />
      </div>
      <span className="upload-progress-percent">{percentLabel}</span>
    </div>
  );
}

function UploadNoticeBanner({
  notice,
  onDismiss,
}: {
  notice: UploadNoticeState;
  onDismiss: () => void;
}) {
  return (
    <div
      className={`upload-security-notice is-${notice.tone}`}
      role={notice.tone === "danger" ? "alert" : "status"}
    >
      <div>
        <strong>{notice.title}</strong>
        <span>
          {notice.filename} · {notice.detail}
        </span>
      </div>
      <button type="button" onClick={onDismiss}>
        关闭
      </button>
    </div>
  );
}

function ProjectUploadTargetNotice({ error }: { error: string }) {
  return (
    <div className="project-upload-target is-error" role="alert">
      <div>
        <strong>当前不能上传文件</strong>
        <span>{error}</span>
      </div>
    </div>
  );
}

function ExistingMediaPanel({
  search,
  mediaType,
  items,
  total,
  loading,
  busy,
  error,
  notice,
  onSearchChange,
  onMediaTypeChange,
  onAdd,
}: {
  search: string;
  mediaType: MediaTypeFilter;
  items: MediaItem[];
  total: number;
  loading: boolean;
  busy: boolean;
  error: string | null;
  notice: string | null;
  onSearchChange: (value: string) => void;
  onMediaTypeChange: (value: MediaTypeFilter) => void;
  onAdd: (item: MediaItem) => void;
}) {
  return (
    <div className="project-media-panel">
      <div className="project-media-headline">
        <div>
          <h3>从已有媒体加入</h3>
        </div>
        <span className="soft-badge">{total} 个可加入</span>
      </div>
      <div className="existing-media-filter">
        <label className="media-search-field">
          <span>搜索</span>
          <input
            type="search"
            value={search}
            placeholder="按文件名搜索"
            onChange={(event) => onSearchChange(event.target.value)}
          />
        </label>
        <label>
          <span>类型</span>
          <select
            value={mediaType}
            onChange={(event) =>
              onMediaTypeChange(event.target.value as MediaTypeFilter)
            }
          >
            <option value="all">全部类型</option>
            <option value="video">视频</option>
            <option value="image">图片</option>
            <option value="audio">音频</option>
            <option value="pdf">PDF</option>
            <option value="design">设计稿</option>
            <option value="document">文档</option>
            <option value="other">其他</option>
          </select>
        </label>
      </div>
      {error ? (
        <p className="catalog-error" role="alert">
          {error}
        </p>
      ) : null}
      {notice ? (
        <p className="project-media-notice" role="status">
          {notice}
        </p>
      ) : null}
      {loading ? <MediaSkeleton /> : null}
      {!loading && items.length === 0 ? (
        <div className="collection-empty">
          <strong>没有可加入的媒体</strong>
          <span>可以换个搜索词，或直接上传新文件到这个项目。</span>
        </div>
      ) : null}
      {!loading && items.length > 0 ? (
        <div className="existing-media-list">
          {items.map((item) => (
            <article key={item.object.id}>
              <div>
                <FileMark
                  mimeType={item.object.mimeType}
                  mediaType={item.mediaType}
                  thumbnail={item.thumbnail}
                />
                <span>
                  <strong>{mediaDisplayName(item)}</strong>
                  <small>
                    {item.asset?.projectName ?? "未归入项目"} ·{" "}
                    {probeSummary(item.probe, item.object.sizeBytes)}
                  </small>
                </span>
              </div>
              <button
                className="secondary-button"
                type="button"
                disabled={busy || !item.asset}
                onClick={() => onAdd(item)}
              >
                加入项目
              </button>
            </article>
          ))}
        </div>
      ) : null}
    </div>
  );
}

function ProjectTrashPanel({
  project,
  items,
  loading,
  busy,
  error,
  notice,
  onRestore,
}: {
  project: Project;
  items: ProjectAsset[];
  loading: boolean;
  busy: boolean;
  error: string | null;
  notice: string | null;
  onRestore: (item: ProjectAsset) => void;
}) {
  return (
    <div className="project-media-panel">
      <div className="project-media-headline">
        <div>
          <h3>项目回收站</h3>
          <p>移除的媒体保留 30 天。</p>
        </div>
        <span className="soft-badge">{items.length} 个待恢复</span>
      </div>
      {error ? (
        <p className="catalog-error" role="alert">
          {error}
        </p>
      ) : null}
      {notice ? (
        <p className="project-media-notice" role="status">
          {notice}
        </p>
      ) : null}
      {loading ? <MediaSkeleton /> : null}
      {!loading && items.length === 0 ? (
        <div className="collection-empty">
          <strong>回收站为空</strong>
        </div>
      ) : null}
      {!loading && items.length > 0 ? (
        <div className="project-trash-table">
          {items.map((item) => (
            <article key={item.id}>
              <span className="file-mark">{assetTypeMark(item.assetType)}</span>
              <span>
                <strong>{item.assetName}</strong>
                <small>
                  {assetTypeLabel(item.assetType)} · 移除于{" "}
                  {item.trashedAt ? formatDateTime(item.trashedAt) : "未知"}
                </small>
              </span>
              <span>
                保留至{" "}
                {item.trashExpiresAt
                  ? formatDateTime(item.trashExpiresAt)
                  : "未设置"}
              </span>
              <button
                className="secondary-button"
                type="button"
                disabled={busy}
                onClick={() => onRestore(item)}
              >
                恢复到项目
              </button>
            </article>
          ))}
        </div>
      ) : null}
    </div>
  );
}

function MediaLibraryFilters({
  search,
  mediaType,
  state,
  projects,
  projectId,
  modifiedFrom,
  modifiedTo,
  sort,
  onSearchChange,
  onMediaTypeChange,
  onStateChange,
  onProjectChange,
  onModifiedFromChange,
  onModifiedToChange,
  onSortChange,
  onReset,
  projectFilterVisible = true,
}: {
  search: string;
  mediaType: MediaTypeFilter;
  state: MediaLibraryState | "all";
  projects: Project[];
  projectId: string;
  modifiedFrom: string;
  modifiedTo: string;
  sort: MediaSort;
  onSearchChange: (value: string) => void;
  onMediaTypeChange: (value: MediaTypeFilter) => void;
  onStateChange: (value: MediaLibraryState | "all") => void;
  onProjectChange: (value: string) => void;
  onModifiedFromChange: (value: string) => void;
  onModifiedToChange: (value: string) => void;
  onSortChange: (value: MediaSort) => void;
  onReset: () => void;
  projectFilterVisible?: boolean;
}) {
  return (
    <div className="media-filter-bar" aria-label="媒体搜索与筛选">
      <label className="media-search-field">
        <span>搜索</span>
        <input
          type="search"
          value={search}
          placeholder="按文件名搜索"
          onChange={(event) => onSearchChange(event.target.value)}
        />
      </label>
      <label>
        <span>类型</span>
        <select
          value={mediaType}
          onChange={(event) =>
            onMediaTypeChange(event.target.value as MediaTypeFilter)
          }
        >
          <option value="all">全部类型</option>
          <option value="video">视频</option>
          <option value="image">图片</option>
          <option value="audio">音频</option>
          <option value="pdf">PDF</option>
          <option value="design">设计稿</option>
          <option value="document">文档</option>
          <option value="other">其他</option>
        </select>
      </label>
      <label>
        <span>状态</span>
        <select
          value={state}
          onChange={(event) =>
            onStateChange(event.target.value as MediaLibraryState | "all")
          }
        >
          <option value="all">全部状态</option>
          <option value="ready">可预览</option>
          <option value="processing">处理中</option>
          <option value="waiting">待处理</option>
          <option value="failed">失败</option>
          <option value="missing">源文件缺失</option>
        </select>
      </label>
      <label>
        <span>开始日期</span>
        <input
          type="date"
          value={modifiedFrom}
          onChange={(event) => onModifiedFromChange(event.target.value)}
        />
      </label>
      <label>
        <span>结束日期</span>
        <input
          type="date"
          value={modifiedTo}
          onChange={(event) => onModifiedToChange(event.target.value)}
        />
      </label>
      <label>
        <span>排序</span>
        <select
          value={sort}
          onChange={(event) => onSortChange(event.target.value as MediaSort)}
        >
          <option value="modified_desc">最近修改</option>
          <option value="modified_asc">最早修改</option>
          <option value="name_asc">名称 A-Z</option>
          <option value="name_desc">名称 Z-A</option>
          <option value="size_desc">文件从大到小</option>
          <option value="size_asc">文件从小到大</option>
        </select>
      </label>
      {projectFilterVisible ? (
        <label className="media-project-filter">
          <span>项目</span>
          <select
            value={projectId}
            onChange={(event) => onProjectChange(event.target.value)}
          >
            <option value="all">全部项目</option>
            <option value="unassigned">未归入项目</option>
            {projects.map((project) => (
              <option value={project.id} key={project.id}>
                {project.name}
              </option>
            ))}
          </select>
        </label>
      ) : null}
      <button className="filter-reset" type="button" onClick={onReset}>
        重置
      </button>
    </div>
  );
}

function LibraryPagination({
  page,
  pageSize,
  total,
  onChange,
}: {
  page: number;
  pageSize: number;
  total: number;
  onChange: (page: number) => void;
}) {
  const pages = Math.ceil(total / pageSize);
  return (
    <nav className="library-pagination" aria-label="媒体库分页">
      <span>
        第 {page} / {pages} 页
      </span>
      <button
        type="button"
        disabled={page <= 1}
        onClick={() => onChange(page - 1)}
      >
        上一页
      </button>
      <button
        type="button"
        disabled={page >= pages}
        onClick={() => onChange(page + 1)}
      >
        下一页
      </button>
    </nav>
  );
}

function MediaBrowser({
  items,
  mode,
  projects = [],
  activeProjectId = null,
  busy = false,
  onSelect,
  onReviewAction,
  onCopyToProject,
  onMoveToProject,
}: {
  items: MediaItem[];
  mode: MediaViewMode;
  projects?: Project[];
  activeProjectId?: string | null;
  busy?: boolean;
  onSelect: (item: MediaItem) => void;
  onReviewAction?: (
    item: MediaItem,
    intent: ReviewCreationSeed["intent"],
  ) => void;
  onCopyToProject?: (item: MediaItem, targetProjectId: string) => Promise<void>;
  onMoveToProject?: (item: MediaItem, targetProjectId: string) => Promise<void>;
}) {
  const projectTargets = projects.filter(
    (project) => project.status === "active" && project.id !== activeProjectId,
  );
  const mediaGridRef = useRef<HTMLDivElement>(null);
  const [openMenuObjectId, setOpenMenuObjectId] = useState<string | null>(null);
  const [projectTransfer, setProjectTransfer] = useState<{
    item: MediaItem;
    intent: "copy" | "move";
    targetProjectId: string;
  } | null>(null);
  const [projectTransferBusy, setProjectTransferBusy] = useState(false);
  const [projectTransferError, setProjectTransferError] = useState<
    string | null
  >(null);

  useEffect(() => {
    if (!openMenuObjectId) return;
    function closeMenu(event: PointerEvent) {
      const target = event.target;
      const openMenu = mediaGridRef.current?.querySelector<HTMLDetailsElement>(
        ".media-card-menu[open]",
      );
      if (target instanceof Node && openMenu?.contains(target)) return;
      setOpenMenuObjectId(null);
    }
    function closeMenuWithKeyboard(event: KeyboardEvent) {
      if (event.key === "Escape") {
        setOpenMenuObjectId(null);
      }
    }
    document.addEventListener("pointerdown", closeMenu);
    document.addEventListener("keydown", closeMenuWithKeyboard);
    return () => {
      document.removeEventListener("pointerdown", closeMenu);
      document.removeEventListener("keydown", closeMenuWithKeyboard);
    };
  }, [openMenuObjectId]);

  useEffect(() => {
    if (!projectTransfer) return;
    function closeTransferWithKeyboard(event: KeyboardEvent) {
      if (event.key === "Escape" && !projectTransferBusy) {
        setProjectTransfer(null);
        setProjectTransferError(null);
      }
    }
    document.addEventListener("keydown", closeTransferWithKeyboard);
    return () =>
      document.removeEventListener("keydown", closeTransferWithKeyboard);
  }, [projectTransfer, projectTransferBusy]);

  function openProjectTransfer(item: MediaItem, intent: "copy" | "move") {
    setOpenMenuObjectId(null);
    setProjectTransferError(null);
    setProjectTransfer({
      item,
      intent,
      targetProjectId: projectTargets[0]?.id ?? "",
    });
  }

  async function submitProjectTransfer() {
    if (!projectTransfer?.targetProjectId) return;
    const action =
      projectTransfer.intent === "copy" ? onCopyToProject : onMoveToProject;
    if (!action) return;
    setProjectTransferBusy(true);
    setProjectTransferError(null);
    try {
      await action(projectTransfer.item, projectTransfer.targetProjectId);
      setProjectTransfer(null);
    } catch (transferError) {
      setProjectTransferError(
        message(
          transferError,
          projectTransfer.intent === "copy"
            ? "无法复制媒体到目标项目"
            : "无法移动媒体到目标项目",
        ),
      );
    } finally {
      setProjectTransferBusy(false);
    }
  }

  if (mode === "grid") {
    return (
      <div className="media-grid" ref={mediaGridRef}>
        {items.map((item) => (
          <article className="media-card" key={item.object.id}>
            <button
              className="media-card-open"
              type="button"
              onClick={() => onSelect(item)}
            >
              <MediaPreview item={item} />
            </button>
            <div className="media-card-copy">
              <span className="media-card-title">
                <strong>{mediaDisplayName(item)}</strong>
                <MediaStateChip item={item} />
              </span>
              <small>
                {mediaTypeLabel(item.mediaType)} ·{" "}
                {probeSummary(item.probe, item.object.sizeBytes)}
              </small>
              {item.progressLabel ? (
                <span className="media-card-progress">
                  <i />
                  {item.progressLabel}
                </span>
              ) : null}
              <UploadCheckInline check={item.uploadCheck} />
            </div>
            {onReviewAction ? (
              <details
                className="media-card-menu"
                open={openMenuObjectId === item.object.id}
              >
                <summary
                  title="更多操作"
                  aria-label={`打开“${mediaDisplayName(item)}”的更多操作`}
                  aria-expanded={openMenuObjectId === item.object.id}
                  onClick={(event) => {
                    event.preventDefault();
                    setOpenMenuObjectId((current) =>
                      current === item.object.id ? null : item.object.id,
                    );
                  }}
                >
                  <MoreVertical size={17} aria-hidden="true" />
                </summary>
                <div>
                  <button
                    type="button"
                    onClick={() => {
                      setOpenMenuObjectId(null);
                      onSelect(item);
                    }}
                  >
                    <Eye size={16} aria-hidden="true" />
                    查看详情
                  </button>
                  {item.object.status === "available" ? (
                    <a
                      href={sourceObjectDownloadURL(item.object)}
                      download={sourceFileName(item)}
                      onClick={() => setOpenMenuObjectId(null)}
                    >
                      <Download size={16} aria-hidden="true" />
                      下载源文件
                    </a>
                  ) : (
                    <button type="button" disabled>
                      <Download size={16} aria-hidden="true" />
                      源文件不可下载
                    </button>
                  )}
                  <button
                    type="button"
                    disabled={!item.asset}
                    onClick={() => {
                      setOpenMenuObjectId(null);
                      onReviewAction(item, "queue");
                    }}
                  >
                    <ListPlus size={16} aria-hidden="true" />
                    加入审阅清单
                  </button>
                  <button
                    type="button"
                    disabled={!item.asset}
                    onClick={() => {
                      setOpenMenuObjectId(null);
                      onReviewAction(item, "create");
                    }}
                  >
                    <Share2 size={16} aria-hidden="true" />
                    创建审阅分享
                  </button>
                  {onCopyToProject ? (
                    <button
                      type="button"
                      disabled={
                        busy || !item.asset || projectTargets.length === 0
                      }
                      title={
                        projectTargets.length === 0
                          ? "没有其他可用项目"
                          : undefined
                      }
                      onClick={() => openProjectTransfer(item, "copy")}
                    >
                      <Copy size={16} aria-hidden="true" />
                      复制到其他项目…
                    </button>
                  ) : null}
                  {onMoveToProject ? (
                    <button
                      type="button"
                      disabled={
                        busy || !item.asset || projectTargets.length === 0
                      }
                      title={
                        projectTargets.length === 0
                          ? "没有其他可用项目"
                          : undefined
                      }
                      onClick={() => openProjectTransfer(item, "move")}
                    >
                      <MoveRight size={16} aria-hidden="true" />
                      移动到其他项目…
                    </button>
                  ) : null}
                </div>
              </details>
            ) : null}
          </article>
        ))}
        {projectTransfer ? (
          <div
            className="media-transfer-overlay"
            role="presentation"
            onClick={() => {
              if (!projectTransferBusy) {
                setProjectTransfer(null);
                setProjectTransferError(null);
              }
            }}
          >
            <section
              className="media-transfer-dialog"
              role="dialog"
              aria-modal="true"
              aria-labelledby="media-transfer-title"
              onClick={(event) => event.stopPropagation()}
            >
              <header>
                <div>
                  <p className="eyebrow">PROJECT MEDIA</p>
                  <h2 id="media-transfer-title">
                    {projectTransfer.intent === "copy"
                      ? "复制到其他项目"
                      : "移动到其他项目"}
                  </h2>
                </div>
                <button
                  className="studio-icon-button"
                  type="button"
                  aria-label="关闭"
                  disabled={projectTransferBusy}
                  onClick={() => {
                    setProjectTransfer(null);
                    setProjectTransferError(null);
                  }}
                >
                  <X size={18} aria-hidden="true" />
                </button>
              </header>
              <div className="media-transfer-content">
                <div className="media-transfer-item">
                  <span>当前媒体</span>
                  <strong>{mediaDisplayName(projectTransfer.item)}</strong>
                </div>
                <label>
                  <span>目标项目</span>
                  <select
                    autoFocus
                    value={projectTransfer.targetProjectId}
                    disabled={projectTransferBusy}
                    onChange={(event) =>
                      setProjectTransfer((current) =>
                        current
                          ? { ...current, targetProjectId: event.target.value }
                          : current,
                      )
                    }
                  >
                    {projectTargets.map((project) => (
                      <option key={project.id} value={project.id}>
                        {project.name}
                      </option>
                    ))}
                  </select>
                </label>
                <p>
                  {projectTransfer.intent === "copy"
                    ? "复制后两个项目都可使用。"
                    : "将从当前项目移至目标项目。"}
                </p>
                {projectTransferError ? (
                  <p className="media-transfer-error" role="alert">
                    {projectTransferError}
                  </p>
                ) : null}
              </div>
              <footer>
                <button
                  className="secondary-button"
                  type="button"
                  disabled={projectTransferBusy}
                  onClick={() => {
                    setProjectTransfer(null);
                    setProjectTransferError(null);
                  }}
                >
                  取消
                </button>
                <button
                  className="primary-button"
                  type="button"
                  disabled={
                    projectTransferBusy || !projectTransfer.targetProjectId
                  }
                  onClick={() => void submitProjectTransfer()}
                >
                  {projectTransferBusy
                    ? "处理中…"
                    : projectTransfer.intent === "copy"
                      ? "复制"
                      : "移动"}
                </button>
              </footer>
            </section>
          </div>
        ) : null}
      </div>
    );
  }

  return (
    <div className="object-list">
      {items.map((item) => (
        <button
          className="object-row"
          type="button"
          key={item.object.id}
          onClick={() => onSelect(item)}
        >
          <FileMark
            mimeType={item.object.mimeType}
            mediaType={item.mediaType}
            thumbnail={item.thumbnail}
          />
          <span className="object-copy">
            <strong>{mediaDisplayName(item)}</strong>
            <span>{item.object.objectKey}</span>
          </span>
          <span className="object-metadata">
            {probeSummary(item.probe, item.object.sizeBytes)}
            <UploadCheckInline check={item.uploadCheck} compact />
          </span>
          <MediaStateChip item={item} />
        </button>
      ))}
    </div>
  );
}

function MediaPreview({ item }: { item: MediaItem }) {
  if (item.thumbnail?.contentUrl) {
    return (
      <span className="media-card-visual">
        <img src={item.thumbnail.contentUrl} alt="" />
        {item.mediaType === "video" ? (
          <span className="media-play-mark" aria-hidden="true">
            ▶
          </span>
        ) : null}
        {item.probe?.durationUs ? (
          <span className="media-duration">
            {formatDuration(item.probe.durationUs)}
          </span>
        ) : null}
      </span>
    );
  }
  return (
    <span className={`media-card-visual is-${item.state}`}>
      <span className="media-placeholder-mark">
        {mediaTypeShortLabel(item.mediaType)}
      </span>
      <small>{item.stateLabel}</small>
    </span>
  );
}

function MediaStateChip({ item }: { item: MediaItem }) {
  return (
    <span className={`media-state-chip is-${item.state}`}>
      {item.stateLabel}
    </span>
  );
}

function UploadCheckInline({
  check,
  compact = false,
}: {
  check: UploadCheck | undefined;
  compact?: boolean;
}) {
  if (!shouldSurfaceUploadCheck(check)) return null;
  return (
    <span
      className={`upload-check-inline is-${uploadCheckTone(check)}${
        compact ? " is-compact" : ""
      }`}
      title={uploadCheckSummary(check)}
    >
      {uploadCheckChipLabel(check)}
    </span>
  );
}

function UploadSecurityPanel({ check }: { check: UploadCheck }) {
  if (!shouldSurfaceUploadCheck(check)) return null;
  const tone = uploadCheckTone(check);
  return (
    <section className={`upload-security-panel is-${tone}`}>
      <div>
        <p className="eyebrow">UPLOAD SECURITY</p>
        <h3>{uploadCheckPanelTitle(check)}</h3>
        <span>{uploadCheckSummary(check)}</span>
      </div>
      <dl>
        <div>
          <dt>策略</dt>
          <dd>{uploadPolicyLabel(check.uploadSecurityPolicy)}</dd>
        </div>
        <div>
          <dt>结果</dt>
          <dd>{uploadResultLabel(check.resultCode)}</dd>
        </div>
        <div>
          <dt>影响</dt>
          <dd>{uploadCheckImpact(check)}</dd>
        </div>
      </dl>
      {check.message ? <small>{check.message}</small> : null}
    </section>
  );
}

function ProcessingDiagnosticPanel({
  item,
  busy,
  retryingJobId,
  notice,
  onRetry,
}: {
  item: MediaItem;
  busy: boolean;
  retryingJobId: string | null;
  notice: string | null;
  onRetry: (jobId: string) => void;
}) {
  const failedRenditionsWithoutJobs =
    item.failedJobs.length === 0 ? item.failedRenditions : [];
  const probeIssue =
    item.probe?.status === "failed"
      ? (item.probe.errorMessage ?? "媒体识别没有完成。")
      : null;

  return (
    <section className="processing-diagnostic-panel" aria-live="polite">
      <header>
        <span className="processing-diagnostic-icon" aria-hidden="true">
          <AlertTriangle size={17} />
        </span>
        <div>
          <p className="eyebrow">需要处理</p>
          <h3>{item.preview ? "部分预览功能不可用" : "预览不可用"}</h3>
          <span>{processingDiagnosticSummary(item)}</span>
        </div>
      </header>

      <div className="processing-diagnostic-list">
        {item.failedJobs.map((job) => (
          <article className="processing-diagnostic-row" key={job.id}>
            <div>
              <strong>{jobTypeLabel(job.type)}</strong>
              <span>{jobImpactLabel(job, item)}</span>
              <details className="processing-technical-details">
                <summary>技术详情</summary>
                <small>
                  {job.error?.code ?? "job.execution_failed"} ·{" "}
                  {job.error?.message ?? "处理任务没有完成"} ·{" "}
                  {formatDateTime(job.updatedAt)} · {job.attemptCount}/
                  {job.maxAttempts} 次尝试
                </small>
              </details>
            </div>
            <div className="processing-diagnostic-actions">
              <button
                className="secondary-button"
                type="button"
                disabled={busy || retryingJobId !== null}
                onClick={() => onRetry(job.id)}
              >
                <RefreshCcw size={14} aria-hidden="true" />
                {retryingJobId === job.id ? "正在重试" : "重试任务"}
              </button>
            </div>
          </article>
        ))}

        {failedRenditionsWithoutJobs.map((rendition) => (
          <article
            className="processing-diagnostic-row"
            key={`${rendition.kind}:${rendition.profileKey}`}
          >
            <div>
              <strong>{renditionKindLabel(rendition.kind)}</strong>
              <span>{renditionImpactLabel(rendition, item)}</span>
              <details className="processing-technical-details">
                <summary>技术详情</summary>
                <small>
                  {rendition.errorMessage ?? "衍生文件生成失败"} ·{" "}
                  {formatDateTime(rendition.updatedAt)}
                </small>
              </details>
            </div>
          </article>
        ))}

        {probeIssue ? (
          <article className="processing-diagnostic-row">
            <div>
              <strong>媒体识别</strong>
              <span>暂时无法生成预览。</span>
              <details className="processing-technical-details">
                <summary>技术详情</summary>
                <small>{probeIssue}</small>
              </details>
            </div>
          </article>
        ) : null}
      </div>

      {notice ? (
        <p
          className={`processing-diagnostic-notice${
            notice.includes("无法") ? " is-error" : ""
          }`}
          role={notice.includes("无法") ? "alert" : "status"}
        >
          {notice}
        </p>
      ) : null}
    </section>
  );
}

function MediaDetailPanel({
  item,
  projects,
  activeProjectId,
  currentProject,
  versions,
  selectedVersionId,
  loadingVersions,
  busy,
  onClose,
  onPreviewVersion,
  onSetCurrentVersion,
  onRenameAsset,
  onUploadVersion,
  onCreateReview,
  onAssignProject,
  onCopyToProject,
  onMoveToProject,
  onRemoveFromProject,
  onRetryJob,
}: {
  item: MediaItem;
  projects: Project[];
  activeProjectId: string | null;
  currentProject: Project | null;
  versions: AssetVersion[];
  selectedVersionId: string | null;
  loadingVersions: boolean;
  busy: boolean;
  onClose: () => void;
  onPreviewVersion: (versionId: string) => void;
  onSetCurrentVersion: (versionId: string) => void;
  onRenameAsset: (name: string) => Promise<void>;
  onUploadVersion: (file: File) => void;
  onCreateReview: (version: AssetVersion) => void;
  onAssignProject: (projectId: string) => void;
  onCopyToProject: (targetProjectId: string) => Promise<void>;
  onMoveToProject: (
    sourceProjectId: string,
    targetProjectId: string,
  ) => Promise<void>;
  onRemoveFromProject: (sourceProjectId: string) => Promise<void>;
  onRetryJob: (jobId: string) => Promise<Job>;
}) {
  const title = mediaDisplayName(item);
  const selectedVersion =
    versions.find((version) => version.id === selectedVersionId) ??
    versions.find((version) => version.isCurrent) ??
    null;
  const selectedPreview = selectedVersion
    ? versionPreview(selectedVersion)
    : null;
  const selectedUploadCheck = selectedVersion?.uploadCheck ?? item.uploadCheck;
  const projectActionSourceId = activeProjectId ?? item.asset?.projectId ?? "";
  const projectActionTargets = useMemo(
    () =>
      projects.filter(
        (project) =>
          project.status === "active" && project.id !== projectActionSourceId,
      ),
    [projectActionSourceId, projects],
  );
  const [targetProjectId, setTargetProjectId] = useState(
    projectActionTargets[0]?.id ?? "",
  );
  const [projectActionNotice, setProjectActionNotice] = useState<string | null>(
    null,
  );
  const [renaming, setRenaming] = useState(false);
  const [nameDraft, setNameDraft] = useState(title);
  const [renameNotice, setRenameNotice] = useState<string | null>(null);
  const [renameError, setRenameError] = useState<string | null>(null);
  const [retryingJobId, setRetryingJobId] = useState<string | null>(null);
  const [processingNotice, setProcessingNotice] = useState<string | null>(null);
  const hasProcessingDiagnostics =
    item.failedJobs.length > 0 ||
    item.failedRenditions.length > 0 ||
    item.probe?.status === "failed";
  useEffect(() => {
    setTargetProjectId((current) =>
      current && projectActionTargets.some((project) => project.id === current)
        ? current
        : (projectActionTargets[0]?.id ?? ""),
    );
  }, [projectActionTargets]);
  useEffect(() => {
    setNameDraft(title);
    setRenaming(false);
    setRenameNotice(null);
    setRenameError(null);
  }, [item.asset?.id, item.asset?.name, item.object.objectKey, title]);
  return (
    <div className="media-detail-overlay" role="presentation" onClick={onClose}>
      <section
        className="media-inspector"
        role="dialog"
        aria-modal="true"
        aria-label={`${title} 详情`}
        onClick={(event) => event.stopPropagation()}
      >
        <header className="media-inspector-header">
          <div>
            <p className="eyebrow">{mediaTypeLabel(item.mediaType)}</p>
            <div className="media-inspector-title-row">
              <h2>{title}</h2>
              {item.asset ? (
                <button
                  className="inline-action-button"
                  type="button"
                  disabled={busy}
                  onClick={() => {
                    setRenaming(true);
                    setRenameNotice(null);
                    setRenameError(null);
                  }}
                >
                  重命名
                </button>
              ) : null}
            </div>
            {renaming && item.asset ? (
              <form
                className="media-rename-form"
                onSubmit={(event) => {
                  event.preventDefault();
                  const nextName = nameDraft.trim();
                  if (!nextName) {
                    setRenameError("名称不能为空");
                    return;
                  }
                  if (nextName.length > 160) {
                    setRenameError("名称不能超过 160 个字符");
                    return;
                  }
                  setRenameNotice(null);
                  setRenameError(null);
                  onRenameAsset(nextName)
                    .then(() => {
                      setRenaming(false);
                      setRenameNotice("名称已保存");
                    })
                    .catch((renameFailure: unknown) => {
                      setRenameError(message(renameFailure, "无法保存名称"));
                    });
                }}
              >
                <input
                  value={nameDraft}
                  maxLength={160}
                  disabled={busy}
                  onChange={(event) => setNameDraft(event.target.value)}
                  aria-label="媒体名称"
                />
                <button type="submit" disabled={busy}>
                  保存
                </button>
                <button
                  type="button"
                  disabled={busy}
                  onClick={() => {
                    setRenaming(false);
                    setNameDraft(title);
                    setRenameError(null);
                  }}
                >
                  取消
                </button>
              </form>
            ) : null}
            {renameNotice ? (
              <small className="media-rename-feedback" role="status">
                {renameNotice}
              </small>
            ) : null}
            {renameError ? (
              <small className="media-rename-feedback is-error" role="alert">
                {renameError}
              </small>
            ) : null}
            <span>{item.object.objectKey}</span>
          </div>
          <button
            className="inspector-close"
            type="button"
            aria-label="关闭媒体详情"
            onClick={onClose}
            autoFocus
          >
            关闭
          </button>
        </header>

        <div className="media-inspector-stage">
          <DetailMedia item={item} preview={selectedPreview} />
        </div>

        <div className="media-inspector-body">
          <div className="inspector-status">
            <MediaStateChip item={item} />
            <span>{item.progressLabel ?? detailStateMessage(item)}</span>
          </div>
          {selectedUploadCheck ? (
            <UploadSecurityPanel check={selectedUploadCheck} />
          ) : null}
          {hasProcessingDiagnostics ? (
            <ProcessingDiagnosticPanel
              item={item}
              busy={busy}
              retryingJobId={retryingJobId}
              notice={processingNotice}
              onRetry={(jobId) => {
                setProcessingNotice(null);
                setRetryingJobId(jobId);
                onRetryJob(jobId)
                  .then((updated) => {
                    setProcessingNotice(
                      updated.status === "queued" ||
                        updated.status === "leased" ||
                        updated.status === "running"
                        ? "处理任务已重新排队，稍后会自动刷新状态。"
                        : "处理任务已更新，请稍后查看状态。",
                    );
                  })
                  .catch((retryError: unknown) => {
                    setProcessingNotice(
                      message(retryError, "无法重试这个处理任务"),
                    );
                  })
                  .finally(() => setRetryingJobId(null));
              }}
            />
          ) : null}
          {currentProject ? (
            <div className="media-project-assignment is-readonly">
              <span>当前项目</span>
              <strong>{currentProject.name}</strong>
            </div>
          ) : (
            <label className="media-project-assignment">
              <span>所属项目</span>
              <select
                value={item.asset?.projectId ?? ""}
                disabled={busy || !item.asset}
                onChange={(event) => onAssignProject(event.target.value)}
              >
                <option value="">未归入项目</option>
                {projects
                  .filter((project) => project.status === "active")
                  .map((project) => (
                    <option value={project.id} key={project.id}>
                      {project.name}
                    </option>
                  ))}
              </select>
              <small>
                {item.asset
                  ? `逻辑资产 · V${item.asset.versionNumber}`
                  : "正在建立逻辑资产关系"}
              </small>
            </label>
          )}
          {item.asset ? (
            <div className="project-asset-actions">
              <div>
                <p className="eyebrow">PROJECT ASSET</p>
                <h3>跨项目复制 / 剪切</h3>
                <span>
                  当前操作项目：
                  {projectActionSourceId
                    ? (projects.find(
                        (project) => project.id === projectActionSourceId,
                      )?.name ?? projectActionSourceId)
                    : "未归入项目"}
                </span>
              </div>
              <label className="field">
                <span>目标项目</span>
                <select
                  value={targetProjectId}
                  disabled={busy || projectActionTargets.length === 0}
                  onChange={(event) => setTargetProjectId(event.target.value)}
                >
                  {projectActionTargets.length === 0 ? (
                    <option value="">没有其他可用项目</option>
                  ) : null}
                  {projectActionTargets.map((project) => (
                    <option key={project.id} value={project.id}>
                      {project.name}
                    </option>
                  ))}
                </select>
              </label>
              <div className="project-asset-action-buttons">
                <button
                  className="secondary-button"
                  type="button"
                  disabled={busy || !targetProjectId}
                  onClick={() => {
                    setProjectActionNotice(null);
                    onCopyToProject(targetProjectId)
                      .then(() => setProjectActionNotice("已复制到目标项目"))
                      .catch(() => setProjectActionNotice(null));
                  }}
                >
                  复制
                </button>
                <button
                  className="secondary-button"
                  type="button"
                  disabled={busy || !projectActionSourceId || !targetProjectId}
                  onClick={() => {
                    setProjectActionNotice(null);
                    onMoveToProject(projectActionSourceId, targetProjectId)
                      .then(() => setProjectActionNotice("已移动到目标项目"))
                      .catch(() => setProjectActionNotice(null));
                  }}
                >
                  剪切
                </button>
                <button
                  className="text-danger-button"
                  type="button"
                  disabled={busy || !projectActionSourceId}
                  onClick={() => {
                    if (
                      !window.confirm("从当前项目移除此媒体？可在回收站恢复。")
                    ) {
                      return;
                    }
                    setProjectActionNotice(null);
                    onRemoveFromProject(projectActionSourceId)
                      .then(() =>
                        setProjectActionNotice("已从当前项目移入回收站"),
                      )
                      .catch(() => setProjectActionNotice(null));
                  }}
                >
                  从当前项目移除
                </button>
              </div>
              {projectActionNotice ? (
                <small role="status">{projectActionNotice}</small>
              ) : null}
            </div>
          ) : null}
          {item.asset ? (
            <div className="version-history">
              <div className="version-history-heading">
                <span>Version history</span>
                <div className="version-history-actions">
                  <small>
                    {loadingVersions ? "Loading" : `${versions.length} total`}
                  </small>
                  <button
                    className="version-review-button"
                    type="button"
                    disabled={
                      busy ||
                      !selectedVersion ||
                      (!activeProjectId && !item.asset?.projectId)
                    }
                    title={
                      activeProjectId || item.asset?.projectId
                        ? "为所选版本创建审阅"
                        : "请先把资产归入项目"
                    }
                    onClick={() => {
                      if (selectedVersion) {
                        onCreateReview(selectedVersion);
                      }
                    }}
                  >
                    创建审阅
                  </button>
                  <label className="version-upload-button">
                    <input
                      type="file"
                      disabled={busy}
                      onChange={(event) => {
                        const file = event.target.files?.[0];
                        if (file) {
                          onUploadVersion(file);
                        }
                        event.target.value = "";
                      }}
                    />
                    {busy ? "处理中" : "上传新版本"}
                  </label>
                </div>
              </div>
              <div className="version-list">
                {versions.map((version) => (
                  <button
                    className={`version-card${
                      version.id === selectedVersion?.id ? " is-selected" : ""
                    }`}
                    type="button"
                    key={version.id}
                    onClick={() => onPreviewVersion(version.id)}
                  >
                    <span>
                      <strong>V{version.versionNumber}</strong>
                      <span className="version-badges">
                        {version.isCurrent ? <em>Current</em> : null}
                        {version.processingStage !== "complete" ? (
                          <em className={`is-${version.processingStage}`}>
                            {versionStageLabel(version.processingStage)}
                          </em>
                        ) : null}
                        {shouldSurfaceUploadCheck(version.uploadCheck) ? (
                          <em
                            className={`is-upload-${uploadCheckTone(
                              version.uploadCheck,
                            )}`}
                          >
                            {uploadCheckChipLabel(version.uploadCheck)}
                          </em>
                        ) : null}
                      </span>
                    </span>
                    <small>{fileName(version.sourceFilename)}</small>
                    <small>
                      {mediaTypeLabel(
                        version.probe?.mediaType ??
                          inferMediaType(version.sourceMime ?? ""),
                      )}{" "}
                      / {formatBytes(version.sourceSizeBytes)}
                    </small>
                    {shouldSurfaceUploadCheck(version.uploadCheck) ? (
                      <small
                        className={`version-upload-check is-${uploadCheckTone(
                          version.uploadCheck,
                        )}`}
                      >
                        上传安全：{uploadCheckSummary(version.uploadCheck)}
                      </small>
                    ) : null}
                  </button>
                ))}
              </div>
              {selectedVersion && !selectedVersion.isCurrent ? (
                <button
                  className="secondary-button"
                  type="button"
                  disabled={busy}
                  onClick={() => onSetCurrentVersion(selectedVersion.id)}
                >
                  Set as current
                </button>
              ) : null}
            </div>
          ) : null}
          {item.errorMessage && !hasProcessingDiagnostics ? (
            <div className="inspector-error" role="alert">
              <strong>无法生成网页预览</strong>
              <span>原文件仍然安全。{item.errorMessage}</span>
            </div>
          ) : null}
          <dl className="media-facts">
            <div>
              <dt>类型</dt>
              <dd>{mediaTypeLabel(item.mediaType)}</dd>
            </div>
            <div>
              <dt>大小</dt>
              <dd>
                {formatBytes(
                  selectedVersion?.sourceSizeBytes ?? item.object.sizeBytes,
                )}
              </dd>
            </div>
            <div>
              <dt>尺寸</dt>
              <dd>
                {item.probe?.width && item.probe.height
                  ? `${item.probe.width} × ${item.probe.height}`
                  : "未知"}
              </dd>
            </div>
            <div>
              <dt>时长</dt>
              <dd>
                {item.probe?.durationUs
                  ? formatDuration(item.probe.durationUs)
                  : "不适用"}
              </dd>
            </div>
            <div>
              <dt>格式</dt>
              <dd>{item.probe?.formatLongName ?? item.object.mimeType}</dd>
            </div>
            <div>
              <dt>修改时间</dt>
              <dd>{formatDateTime(item.object.modifiedAt)}</dd>
            </div>
          </dl>
          {item.storyboard?.contentUrl ? (
            <div className="storyboard-preview">
              <span>时间轴缩略图</span>
              <img
                src={item.storyboard.contentUrl}
                alt={`${title} 时间轴缩略图`}
              />
            </div>
          ) : null}
        </div>
      </section>
    </div>
  );
}

interface VersionPreview {
  mediaType: MediaProbe["mediaType"] | "other";
  thumbnail: Rendition | undefined;
  preview: Rendition | undefined;
  storyboard: Rendition | undefined;
}

function versionPreview(version: AssetVersion): VersionPreview {
  const mediaType =
    version.probe?.mediaType ?? inferMediaType(version.sourceMime ?? "");
  return {
    mediaType,
    thumbnail: version.renditions.find(
      (item) =>
        (item.kind === "thumbnail" || item.kind === "poster") &&
        item.status === "ready",
    ),
    preview: version.renditions.find(
      (item) =>
        ((mediaType === "video" &&
          (item.kind === "proxy" || item.kind === "hls")) ||
          (mediaType === "image" && item.kind === "screen_preview")) &&
        item.status === "ready",
    ),
    storyboard: version.renditions.find(
      (item) => item.kind === "storyboard" && item.status === "ready",
    ),
  };
}

function versionStageLabel(stage: AssetVersion["processingStage"]) {
  switch (stage) {
    case "ingested":
      return "已入库";
    case "processing":
      return "生成预览";
    case "preview_ready":
      return "可预览";
    case "optimizing":
      return "后台优化中";
    case "enhancement_failed":
      return "增强失败";
    case "failed":
      return "处理失败";
    default:
      return "处理完成";
  }
}

function uploadCheckStatusLabel(status: string) {
  switch (status) {
    case "uploaded":
      return "已上传";
    case "checking":
      return "检查中";
    case "processing":
      return "处理中";
    case "ready":
      return "检查通过";
    case "quarantined":
      return "已隔离";
    case "rejected":
      return "已拒绝";
    default:
      return status;
  }
}

function shouldSurfaceUploadCheck(
  check: UploadCheck | null | undefined,
): check is UploadCheck {
  if (!check) return false;
  return check.status !== "ready";
}

function uploadCheckTone(check: UploadCheck | undefined): UploadSecurityTone {
  if (!check) return "neutral";
  if (check.status === "quarantined" || check.status === "rejected") {
    return "danger";
  }
  if (
    check.status === "uploaded" ||
    check.status === "checking" ||
    check.status === "processing"
  ) {
    return "warning";
  }
  if (
    check.resultCode === "malware_not_scanned" ||
    check.resultCode === "malware_scan_unavailable" ||
    check.resultCode === "malware_scan_failed"
  ) {
    return "warning";
  }
  if (check.status === "ready") {
    return "success";
  }
  return "neutral";
}

function uploadCheckChipLabel(check: UploadCheck): string {
  if (check.status === "ready") {
    switch (check.resultCode) {
      case "malware_clean":
      case "basic_checks_passed":
      case "type_checks_passed":
        return "安全通过";
      case "malware_not_scanned":
        return "快速放行";
      case "malware_scan_unavailable":
        return "扫描未配置";
      default:
        return "已放行";
    }
  }
  return uploadCheckStatusLabel(check.status);
}

function uploadCheckPanelTitle(check: UploadCheck): string {
  switch (uploadCheckTone(check)) {
    case "danger":
      return check.status === "rejected" ? "文件已被拒绝" : "文件已被隔离";
    case "warning":
      return check.status === "ready"
        ? "文件已放行，但有安全提示"
        : "正在完成上传安全检查";
    case "success":
      return "文件已通过上传安全检查";
    default:
      return "上传安全状态";
  }
}

function uploadResultLabel(resultCode: string | null): string {
  switch (resultCode) {
    case "basic_checks_passed":
      return "基础上传检查通过";
    case "type_checks_passed":
      return "类型和大小检查通过";
    case "malware_clean":
      return "恶意文件扫描通过";
    case "malware_not_scanned":
      return "快速策略未执行恶意文件扫描";
    case "malware_scan_unavailable":
      return "扫描器未配置或不可用";
    case "malware_detected":
      return "检测到风险文件";
    case "malware_scan_failed":
      return "恶意文件扫描失败";
    case null:
      return "暂无结果";
    default:
      return resultCode;
  }
}

function uploadCheckSummary(check: UploadCheck): string {
  const policy = uploadPolicyLabel(check.uploadSecurityPolicy);
  if (check.status === "quarantined") {
    return `${policy}要求先隔离文件：${uploadResultLabel(check.resultCode)}。`;
  }
  if (check.status === "rejected") {
    return `${policy}拒绝了这个文件：${uploadResultLabel(check.resultCode)}。`;
  }
  if (check.status !== "ready") {
    return `${policy}正在处理：${uploadCheckStatusLabel(check.status)}。`;
  }
  switch (check.resultCode) {
    case "malware_clean":
      return `${policy}已完成恶意文件扫描，文件可以正常使用。`;
    case "basic_checks_passed":
      return `${policy}已完成基础上传检查，文件可以正常使用。`;
    case "type_checks_passed":
      return `${policy}已完成基础类型和大小检查，文件可以正常使用。`;
    case "malware_not_scanned":
      return `${policy}为快速放行模式，本次没有执行恶意文件扫描。`;
    case "malware_scan_unavailable":
      return `${policy}未找到可用扫描器，已按策略放行文件。`;
    default:
      return `${policy}已放行文件。`;
  }
}

function uploadCheckImpact(check: UploadCheck): string {
  if (check.status === "quarantined" || check.status === "rejected") {
    return "不能生成预览、创建审阅或对外分享，需要主管或 Owner 处理。";
  }
  if (uploadCheckTone(check) === "warning") {
    return "可以继续使用，但建议 Owner 检查全局上传安全策略和扫描器配置。";
  }
  return "可以正常预览、审阅、分享和下载。";
}

function uploadNoticeFromCheck(
  check: UploadCheck | null | undefined,
  filename: string,
): UploadNoticeState | null {
  if (!shouldSurfaceUploadCheck(check)) return null;
  const tone = uploadCheckTone(check);
  return {
    filename,
    title: uploadCheckPanelTitle(check),
    detail: `${uploadCheckSummary(check)} ${uploadCheckImpact(check)}`,
    tone,
  };
}

function isUploadQuotaExceeded(error: unknown): boolean {
  return (
    error instanceof Error &&
    "code" in error &&
    String((error as { code?: unknown }).code ?? "") ===
      "project_storage.upload_quota_exceeded"
  );
}

function uploadPolicyLabel(policy: string) {
  switch (policy) {
    case "quick":
      return "快速安全策略";
    case "standard":
      return "标准安全策略";
    case "enhanced":
      return "增强安全策略";
    default:
      return policy || "默认安全策略";
  }
}

function DetailMedia({
  item,
  preview,
}: {
  item: MediaItem;
  preview: VersionPreview | null;
}) {
  const mediaType = preview?.mediaType ?? item.mediaType;
  const mediaPreview = preview?.preview ?? item.preview;
  const thumbnail = preview?.thumbnail ?? item.thumbnail;
  if (mediaType === "video" && mediaPreview?.contentUrl) {
    return (
      <HlsVideo
        className="media-inspector-video"
        controls
        preload="metadata"
        src={mediaPreview.contentUrl}
        poster={thumbnail?.contentUrl ?? undefined}
      />
    );
  }
  if (mediaType === "image" && mediaPreview?.contentUrl) {
    return <img src={mediaPreview.contentUrl} alt={mediaDisplayName(item)} />;
  }
  if (thumbnail?.contentUrl) {
    return <img src={thumbnail.contentUrl} alt={mediaDisplayName(item)} />;
  }
  return (
    <div className="inspector-placeholder">
      <strong>{mediaTypeShortLabel(item.mediaType)}</strong>
      <span>{detailStateMessage(item)}</span>
    </div>
  );
}

function applyLibraryPage(
  page: MediaLibraryPage,
  setObjects: Dispatch<SetStateAction<StorageObject[]>>,
  setProbes: Dispatch<SetStateAction<MediaProbe[]>>,
  setRenditions: Dispatch<SetStateAction<Rendition[]>>,
  setAssetsByObject: Dispatch<
    SetStateAction<Record<string, MediaLibraryAsset>>
  >,
  setUploadChecksByObject: Dispatch<
    SetStateAction<Record<string, UploadCheck>>
  >,
  setRenditionJobs: Dispatch<SetStateAction<Job[]>>,
  setVideoRenditionJobs: Dispatch<SetStateAction<Job[]>>,
  setVersionJobs: Dispatch<SetStateAction<Job[]>>,
) {
  const jobs = page.items.flatMap((item) => item.jobs);
  setObjects(page.items.map((item) => item.object));
  setAssetsByObject(
    Object.fromEntries(
      page.items.flatMap((item) =>
        item.asset ? [[item.object.id, item.asset]] : [],
      ),
    ),
  );
  setUploadChecksByObject(
    Object.fromEntries(
      page.items.flatMap((item) =>
        item.uploadCheck ? [[item.object.id, item.uploadCheck]] : [],
      ),
    ),
  );
  setProbes(page.items.flatMap((item) => (item.probe ? [item.probe] : [])));
  setRenditions(page.items.flatMap((item) => item.renditions));
  setRenditionJobs(
    jobs.filter((item) => item.type === "media.generate_image_renditions"),
  );
  setVideoRenditionJobs(
    jobs.filter(
      (item) =>
        item.type === "media.generate_video_renditions" ||
        item.type === "media.generate_video_enhancements",
    ),
  );
  setVersionJobs(
    jobs.filter((item) => item.type === "media.process_asset_version"),
  );
}

function hasLibraryFilters(query: MediaLibraryQuery) {
  return Boolean(
    query.search ||
    (query.mediaType && query.mediaType !== "all") ||
    (query.state && query.state !== "all") ||
    (query.projectId && query.projectId !== "all") ||
    query.modifiedFrom ||
    query.modifiedTo,
  );
}

function hasVisibleLibraryFilters(query: MediaLibraryQuery) {
  return Boolean(
    query.search ||
    (query.mediaType && query.mediaType !== "all") ||
    (query.state && query.state !== "all") ||
    query.modifiedFrom ||
    query.modifiedTo,
  );
}

export function buildMediaItem(
  object: StorageObject,
  probe: MediaProbe | undefined,
  renditions: Rendition[],
  jobs: Job[],
  asset?: MediaLibraryAsset,
  uploadCheck?: UploadCheck,
): MediaItem {
  const mediaType = probe?.mediaType ?? inferMediaType(object.mimeType);
  const thumbnail = renditions.find(
    (item) =>
      (item.kind === "thumbnail" || item.kind === "poster") &&
      item.status === "ready",
  );
  const preview = renditions.find(
    (item) =>
      ((mediaType === "video" &&
        (item.kind === "proxy" || item.kind === "hls")) ||
        (mediaType === "image" && item.kind === "screen_preview")) &&
      item.status === "ready",
  );
  const storyboard = renditions.find(
    (item) => item.kind === "storyboard" && item.status === "ready",
  );
  const activeJob = jobs.find(isActiveJob);
  const failedJobs = jobs.filter((item) => item.status === "failed");
  const failedJob = failedJobs[0];
  const failedRenditions = renditions.filter(
    (item) => item.status === "failed",
  );
  const failedRendition = failedRenditions[0];
  const base = {
    object,
    asset,
    probe,
    mediaType,
    thumbnail,
    preview,
    storyboard,
    jobs,
    failedJobs,
    failedRenditions,
    uploadCheck,
  };

  if (
    uploadCheck?.status === "quarantined" ||
    uploadCheck?.status === "rejected"
  ) {
    return {
      ...base,
      state: "quarantined",
      stateLabel: uploadCheck.status === "rejected" ? "已拒绝" : "已隔离",
      progressLabel: null,
      errorMessage: uploadCheckImpact(uploadCheck),
    };
  }
  if (object.status === "missing") {
    return {
      ...base,
      state: "missing",
      stateLabel: "文件缺失",
      progressLabel: null,
      errorMessage: "源文件已移动、删除，或授权目录当前不可访问。",
    };
  }
  if (probe?.status === "failed") {
    return {
      ...base,
      state: "failed",
      stateLabel: "识别失败",
      progressLabel: null,
      errorMessage: probe.errorMessage ?? "无法识别这个媒体文件。",
    };
  }
  if (activeJob) {
    const canPreviewDuringProcessing = Boolean(preview);
    return {
      ...base,
      state: canPreviewDuringProcessing ? "optimizing" : "processing",
      stateLabel: canPreviewDuringProcessing ? "后台优化中" : "生成预览",
      progressLabel: canPreviewDuringProcessing
        ? `后台优化中：${formatJobProgress(activeJob)}`
        : formatJobProgress(activeJob),
      errorMessage: null,
    };
  }
  if (failedJob || failedRendition) {
    if (preview) {
      return {
        ...base,
        state: "enhancement_failed",
        stateLabel: "增强失败",
        progressLabel: null,
        errorMessage:
          failedJob?.error?.message ??
          failedRendition?.errorMessage ??
          "核心预览可用，但后台增强没有完成。",
      };
    }
    return {
      ...base,
      state: "failed",
      stateLabel: "处理失败",
      progressLabel: null,
      errorMessage:
        failedJob?.error?.message ??
        failedRendition?.errorMessage ??
        "预览处理没有完成。",
    };
  }
  if (preview || (mediaType !== "video" && mediaType !== "image" && probe)) {
    const complete =
      mediaType !== "video" ||
      renditions.some(
        (item) => item.kind === "storyboard" && item.status === "ready",
      );
    return {
      ...base,
      state: complete ? "complete" : "preview_ready",
      stateLabel: complete ? "处理完成" : "可预览",
      progressLabel: null,
      errorMessage: null,
    };
  }
  return {
    ...base,
    state: probe ? "ingested" : "waiting",
    stateLabel: probe ? "已入库" : "待识别",
    progressLabel: null,
    errorMessage: null,
  };
}

function FileMark({
  mimeType,
  mediaType,
  thumbnail,
}: {
  mimeType: string;
  mediaType: MediaProbe["mediaType"] | undefined;
  thumbnail: Rendition | undefined;
}) {
  if (thumbnail?.contentUrl) {
    return (
      <span className="file-thumbnail">
        <img src={thumbnail.contentUrl} alt="" />
      </span>
    );
  }
  const label =
    mediaType === "video" || mimeType.startsWith("video/")
      ? "VID"
      : mediaType === "image" || mimeType.startsWith("image/")
        ? "IMG"
        : mediaType === "audio" || mimeType.startsWith("audio/")
          ? "AUD"
          : "FILE";
  return <span className="file-mark">{label}</span>;
}

function inferMediaType(mimeType: string): MediaItem["mediaType"] {
  if (mimeType.startsWith("video/")) return "video";
  if (mimeType.startsWith("image/")) return "image";
  if (mimeType.startsWith("audio/")) return "audio";
  if (mimeType === "application/pdf") return "pdf";
  return "other";
}

function mediaTypeLabel(type: MediaItem["mediaType"]): string {
  switch (type) {
    case "video":
      return "视频";
    case "image":
      return "图片";
    case "audio":
      return "音频";
    case "pdf":
      return "PDF";
    case "design":
      return "设计稿";
    case "document":
      return "文档";
    default:
      return "文件";
  }
}

function mediaTypeShortLabel(type: MediaItem["mediaType"]): string {
  switch (type) {
    case "video":
      return "VID";
    case "image":
      return "IMG";
    case "audio":
      return "AUD";
    case "pdf":
      return "PDF";
    default:
      return "FILE";
  }
}

function assetTypeLabel(type: string): string {
  switch (type) {
    case "video":
      return "视频";
    case "image":
      return "图片";
    case "audio":
      return "音频";
    case "pdf":
      return "PDF";
    case "design":
      return "设计稿";
    case "document":
      return "文档";
    default:
      return "文件";
  }
}

function assetTypeMark(type: string): string {
  switch (type) {
    case "video":
      return "VID";
    case "image":
      return "IMG";
    case "audio":
      return "AUD";
    case "pdf":
      return "PDF";
    default:
      return "FILE";
  }
}

function detailStateMessage(item: MediaItem): string {
  switch (item.state) {
    case "ready":
    case "preview_ready":
      return item.progressLabel
        ? "网页预览已经就绪，后台还在优化流式播放和时间轴缩略图。"
        : "网页预览已经就绪。";
    case "optimizing":
      return "网页预览已经就绪，后台正在补充增强体验。";
    case "complete":
      return "网页预览和增强体验已经完成。";
    case "enhancement_failed":
      return "网页预览仍可使用，但后台增强没有完成，可以稍后重试。";
    case "processing":
      return "后台正在生成网页预览，可以离开此页面。";
    case "failed":
      return "网页预览处理失败，源文件没有被修改。";
    case "missing":
      return "源文件当前不可访问。";
    case "waiting":
      return item.probe ? "媒体已识别，等待生成网页预览。" : "等待媒体识别。";
    case "ingested":
      return "文件已入库，等待生成网页预览。";
    case "quarantined":
      return item.errorMessage ?? "文件处于隔离状态，不能继续处理。";
  }
}

function processingDiagnosticSummary(item: MediaItem): string {
  if (item.preview && item.state === "enhancement_failed") {
    return "可以继续审阅和分享，部分预览功能不可用。";
  }
  if (item.preview) {
    return "可以继续使用，部分预览功能需要处理。";
  }
  if (item.probe?.status === "failed") {
    return "暂时无法生成预览。";
  }
  return "暂时无法预览，审阅和分享会受影响。";
}

function jobTypeLabel(type: string): string {
  if (type === "media.probe_object") return "媒体识别";
  if (type === "media.generate_image_renditions") return "图片预览";
  if (type === "media.generate_video_renditions") return "视频网页预览";
  if (type === "media.generate_video_enhancements") return "视频后台增强";
  if (type === "media.process_asset_version") return "版本处理";
  if (type === "storage.copy_object") return "跨存储复制";
  return type.replaceAll("_", " ").replaceAll(".", " / ");
}

function jobImpactLabel(job: Job, item: MediaItem): string {
  if (job.type === "media.generate_video_enhancements") {
    return item.preview
      ? "不影响当前播放，但时间轴缩略图等增强体验缺失。"
      : "增强任务失败，且当前还没有可用预览，需要先确认核心预览状态。";
  }
  if (job.type === "media.generate_video_renditions") {
    return "会影响视频在审阅页和分享页里的网页播放。";
  }
  if (job.type === "media.generate_image_renditions") {
    return "会影响图片缩略图、详情预览和审阅查看。";
  }
  if (job.type === "media.process_asset_version") {
    return "会影响当前版本进入可预览状态，也可能阻塞后续审阅。";
  }
  if (job.type === "storage.copy_object") {
    return "会影响跨存储或跨来源复制，不会改动当前源文件。";
  }
  return item.preview
    ? "当前预览可用，但这个后台任务需要确认。"
    : "这个后台任务失败，可能影响预览或交付。";
}

function renditionKindLabel(kind: Rendition["kind"]): string {
  if (kind === "thumbnail") return "缩略图";
  if (kind === "poster") return "视频封面";
  if (kind === "screen_preview") return "图片网页预览";
  if (kind === "proxy") return "MP4 网页预览";
  if (kind === "hls") return "流式播放预览";
  if (kind === "storyboard") return "时间轴缩略图";
  return kind;
}

function renditionImpactLabel(rendition: Rendition, item: MediaItem): string {
  if (rendition.kind === "storyboard" && item.preview) {
    return "不影响播放，只是少了时间轴快速定位缩略图。";
  }
  if (rendition.kind === "thumbnail" || rendition.kind === "poster") {
    return "会影响媒体卡片封面和详情入口识别。";
  }
  return item.preview
    ? "核心预览仍可使用，但这个衍生文件需要重新生成。"
    : "会影响网页预览、审阅查看或分享播放。";
}

function formatJobProgress(item: Job): string {
  if (item.status === "queued" || item.status === "leased") {
    return "任务正在排队";
  }
  if (item.status === "cancel_requested") {
    return "正在取消任务";
  }
  if (item.progress) {
    const label = processingStepLabel(item);
    if (label) {
      return `${label} · ${item.progress.current}/${item.progress.total}`;
    }
    return `${item.progress.current}/${item.progress.total} ${item.progress.unit}`;
  }
  return "正在处理";
}

function processingStepLabel(item: Job): string | null {
  if (
    item.type === "media.generate_video_enhancements" &&
    item.progress?.unit === "enhancements"
  ) {
    return "正在生成时间轴缩略图";
  }
  if (
    item.type !== "media.process_asset_version" ||
    item.progress?.unit !== "steps"
  ) {
    return null;
  }
  const { current, total } = item.progress;
  if (total === 3) {
    if (current <= 1) return "正在识别媒体";
    if (current === 2) return "正在生成封面/缩略图";
    return "正在生成网页预览";
  }
  if (total === 5) {
    if (current <= 1) return "正在生成封面";
    if (current === 2) return "正在生成网页预览";
    if (current === 3) return "正在生成流式播放";
    if (current === 4) return "正在生成时间轴缩略图";
    return "正在整理视频预览";
  }
  if (total === 1) return "正在识别媒体";
  return "正在处理媒体";
}

function renditionButtonLabel(items: Job[]): string {
  const active = items.filter(isActiveJob);
  if (active.length > 0) {
    const completed = items.filter(
      (item) => item.status === "succeeded",
    ).length;
    return `生成预览 ${completed}/${items.length}`;
  }
  if (items.some((item) => item.status === "failed")) return "重试图片预览";
  if (items.length > 0) return "刷新图片预览";
  return "生成图片预览";
}

function videoRenditionButtonLabel(items: Job[]): string {
  const active = items.filter(isActiveJob);
  if (active.length > 0) {
    const completed = items.filter(
      (item) => item.status === "succeeded",
    ).length;
    return `生成视频增强 ${completed}/${items.length}`;
  }
  if (items.some((item) => item.status === "failed")) return "重试视频增强";
  if (items.length > 0) return "刷新视频增强";
  return "生成视频增强";
}

function MediaSkeleton() {
  return (
    <div className="media-skeleton" aria-label="正在加载">
      <span />
      <span />
      <span />
    </div>
  );
}

function fileName(objectKey: string): string {
  return objectKey.split("/").at(-1) ?? objectKey;
}

function sourceFileName(item: MediaItem): string {
  return fileName(item.object.objectKey);
}

function mediaDisplayName(item: MediaItem): string {
  return item.asset?.name?.trim() || sourceFileName(item);
}

function sourceObjectDownloadURL(object: StorageObject): string {
  const parameters = new URLSearchParams({ key: object.objectKey });
  return `/api/v1/authorized-roots/${encodeURIComponent(
    object.authorizedRootId,
  )}/objects/content?${parameters.toString()}`;
}

function formatBytes(value: number): string {
  if (value < 1024) return `${value} B`;
  if (value < 1024 ** 2) return `${(value / 1024).toFixed(1)} KB`;
  if (value < 1024 ** 3) return `${(value / 1024 ** 2).toFixed(1)} MB`;
  return `${(value / 1024 ** 3).toFixed(1)} GB`;
}

function probeSummary(probe: MediaProbe | undefined, size: number): string {
  if (!probe || probe.status === "failed") {
    return formatBytes(size);
  }
  const parts = [formatBytes(size)];
  if (probe.width && probe.height) {
    parts.push(`${probe.width}×${probe.height}`);
  }
  if (probe.durationUs) {
    parts.push(formatDuration(probe.durationUs));
  }
  const codec = probe.videoCodec ?? probe.audioCodec;
  if (codec) {
    parts.push(codec.toUpperCase());
  }
  return parts.join(" · ");
}

function formatDuration(microseconds: number): string {
  const seconds = Math.round(microseconds / 1_000_000);
  const minutes = Math.floor(seconds / 60);
  const remainder = String(seconds % 60).padStart(2, "0");
  return `${minutes}:${remainder}`;
}

function formatDateTime(value: string): string {
  return new Intl.DateTimeFormat("zh-CN", {
    dateStyle: "medium",
    timeStyle: "short",
  }).format(new Date(value));
}

function isActiveJob(item: Job | null): boolean {
  return (
    item?.status === "queued" ||
    item?.status === "leased" ||
    item?.status === "running" ||
    item?.status === "cancel_requested"
  );
}

function probeButtonLabel(item: Job | null): string {
  if (!item) return "探测媒体";
  if (item.status === "queued" || item.status === "leased") return "等待探测";
  if (item.status === "running") {
    return item.progress
      ? `正在探测 ${item.progress.current}/${item.progress.total}`
      : "正在探测";
  }
  return "再次探测";
}

function jobStatusLabel(item: Job): string {
  switch (item.status) {
    case "queued":
      return "任务排队中";
    case "leased":
      return "任务已领取";
    case "running":
      return item.progress
        ? `探测 ${item.progress.current}/${item.progress.total}`
        : "正在探测";
    case "succeeded":
      return "探测完成";
    case "failed":
      return "探测失败";
    case "cancel_requested":
      return "正在取消";
    case "cancelled":
      return "已取消";
  }
}

function isAbort(error: unknown): boolean {
  return error instanceof DOMException && error.name === "AbortError";
}

function message(error: unknown, fallback: string): string {
  if (error instanceof Error && "code" in error) {
    const code = String((error as { code?: unknown }).code ?? "");
    if (code === "project_storage.upload_not_configured") {
      return "项目还没有选择上传存储，请先到项目设置里选择上传存储桶。";
    }
    if (code === "project_storage.upload_unavailable") {
      return "项目当前的上传存储不可用，请到项目设置里重新选择上传存储桶。";
    }
  }
  return error instanceof Error ? error.message : fallback;
}
