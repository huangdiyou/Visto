import { useEffect, useMemo, useRef, useState, type ReactNode } from "react";
import type {
  AuthorizedRoot,
  SystemHostAccess,
  SystemNetworkSettings,
  Job,
  LocalManagedBucket,
  LocalManagedBucketPurpose,
  ProjectStorageGrant,
  SessionInfo,
  StorageDeleteImpact,
  StorageProvider,
  StorageProviderInput,
  StorageProviderKind,
  SystemInfo,
  SystemUpdateCommand,
  SystemUpdateStatus,
  UploadCheckListItem,
  UploadCheckStatus,
  UploadSecurityPolicy,
} from "@review-studio/contracts";
import {
  Activity,
  AlertCircle,
  BellRing,
  CheckCircle2,
  Copy,
  Cpu,
  Database,
  HardDrive,
  Info,
  RefreshCw,
  Server,
  ShieldCheck,
  UsersRound,
  Wrench,
  X,
} from "lucide-react";
import { ApiError } from "./api/client";
import { networkSettingsViewState } from "./lib/network-settings";
import { copyText } from "./lib/clipboard";
import {
  deploymentLabel,
  formatReleaseDate,
  updateStatusView,
} from "./lib/update-status";
import {
  getHostAccess,
  getNetworkSettings,
  getSystemUpdateStatus,
  updateNetworkSettings,
} from "./api/system";
import { listJobs } from "./api/jobs";
import {
  createStorageProvider,
  createLocalManagedBucket,
  deleteLocalManagedBucket,
  deleteStorageProvider,
  getLocalManagedBucketDeleteImpact,
  getStorageProviderDeleteImpact,
  listLocalManagedBuckets,
  listUploadChecks,
  listProjectStorageGrants,
  listStorageProviders,
  registerProviderRoot,
  rejectUploadCheck,
  releaseUploadCheck,
  setProjectStorageGrant,
  testStorageProvider,
  updateLocalManagedBucket,
  updateStorageProvider,
} from "./api/storage";
import type { OwnerSection } from "./lib/studio-route";
import { MediaEncodingSettingsCard } from "./MediaEncodingSettingsCard";
import { NotificationWorkspace } from "./NotificationWorkspace";
import { TeamWorkspace } from "./TeamWorkspace";
import { useI18n } from "./lib/i18n-react";
import type { Translator } from "./lib/i18n";
import { resolveManagedBucketPath } from "./lib/storage-path";

interface OwnerSettingsWorkspaceProps {
  system: SystemInfo;
  session: SessionInfo;
  section: OwnerSection;
  onSessionUpdated: (session: SessionInfo) => void;
  onSectionChange: (section: OwnerSection) => void;
}

const ownerSections: Array<{
  id: OwnerSection;
  icon: ReactNode;
}> = [
  {
    id: "accounts",
    icon: <UsersRound size={17} />,
  },
  {
    id: "network",
    icon: <ShieldCheck size={17} />,
  },
  {
    id: "storage",
    icon: <HardDrive size={17} />,
  },
  {
    id: "encoding",
    icon: <Cpu size={17} />,
  },
  {
    id: "notifications",
    icon: <BellRing size={17} />,
  },
  {
    id: "activity",
    icon: <Activity size={17} />,
  },
  {
    id: "diagnostics",
    icon: <Wrench size={17} />,
  },
];

interface OwnerStorageLocation {
  key: string;
  kind: "bucket" | "grant" | "provider";
  title: string;
  subtitle: string;
  meta: string;
  status: string;
  statusLabel: string;
  bucket?: LocalManagedBucket;
  grant?: ProjectStorageGrant;
  provider?: StorageProvider;
}

type InlineFeedbackTone = "success" | "error" | "info";

interface InlineFeedbackState {
  tone: InlineFeedbackTone;
  message: string;
}

type StorageWizardType = "local" | "webdav" | "s3";
type StorageWizardUsage = "upload" | "archive" | "review_upload";
type StorageWizardAvailability = "none" | "all";

export function OwnerSettingsWorkspace({
  system,
  session,
  section,
  onSessionUpdated,
  onSectionChange,
}: OwnerSettingsWorkspaceProps) {
  const { t } = useI18n();
  const activeSection =
    ownerSections.find((candidate) => candidate.id === section) ??
    ownerSections[0]!;

  return (
    <div className="studio-page owner-settings-workspace">
      <header className="studio-page-header owner-settings-heading">
        <div>
          <p className="studio-kicker">OWNER CONTROL</p>
          <h1>{t("owner.title")}</h1>
          <p>{t("owner.description")}</p>
        </div>
        <span className="owner-scope-badge">
          <ShieldCheck size={15} />
          {t("common.ownerOnly")}
        </span>
      </header>

      <div className="owner-settings-shell">
        <nav className="owner-settings-nav" aria-label={t("owner.title")}>
          {ownerSections.map((item) => (
            <button
              className={item.id === section ? "is-active" : ""}
              type="button"
              key={item.id}
              onClick={() => onSectionChange(item.id)}
            >
              {item.icon}
              <span>
                <strong>{ownerSectionNavLabel(item.id, t)}</strong>
                <small>{ownerSectionNavDescription(item.id, t)}</small>
              </span>
            </button>
          ))}
        </nav>

        <section className="owner-settings-content">
          <header className="owner-section-header">
            <p>{ownerSectionNavLabel(activeSection.id, t)}</p>
            <h2>{ownerSectionTitle(section, t)}</h2>
            <span>{ownerSectionDescription(section, t)}</span>
          </header>

          {section === "accounts" ? (
            <TeamWorkspace
              session={session}
              embedded
              onSessionUpdated={onSessionUpdated}
            />
          ) : section === "storage" ? (
            <OwnerStorageSettings
              hostManagement={system.access.hostManagement}
            />
          ) : section === "network" ? (
            <OwnerNetworkSettings />
          ) : section === "encoding" ? (
            <MediaEncodingSettingsCard />
          ) : section === "notifications" ? (
            <NotificationWorkspace showAudit surface="owner-notifications" />
          ) : section === "activity" ? (
            <NotificationWorkspace showAudit surface="owner-activity" />
          ) : (
            <OwnerDiagnostics system={system} />
          )}
        </section>
      </div>
    </div>
  );
}

function OwnerNetworkSettings() {
  const { t } = useI18n();
  const [settings, setSettings] = useState<SystemNetworkSettings | null>(null);
  const [requireRemoteHTTPS, setRequireRemoteHTTPS] = useState(false);
  const [hostAccess, setHostAccess] = useState<SystemHostAccess | null>(null);
  const [loading, setLoading] = useState(true);
  const [busy, setBusy] = useState(false);
  const [feedback, setFeedback] = useState<InlineFeedbackState | null>(null);

  function load(signal?: AbortSignal) {
    // D2, docs/FREE_TIER_BOUNDARY_DESIGN.md §2.5: the Owner page shows the
    // persistent host access state. It is read-only here by design — the switch
    // grants host-level reach, so the session that benefits from it must not be
    // able to change it.
    void getHostAccess(signal)
      .then(setHostAccess)
      .catch(() => {
        // A missing marker must not blank the security card; the network half of
        // the page still has to render.
        if (!signal?.aborted) setHostAccess(null);
      });
    return getNetworkSettings(signal)
      .then((loaded) => {
        setSettings(loaded);
        setRequireRemoteHTTPS(loaded.requireRemoteHTTPS);
      })
      .catch((error: unknown) => {
        if (!isAbort(error)) {
          setFeedback({
            tone: "error",
            message:
              error instanceof Error
                ? error.message
                : t("owner.networkLoadFailed"),
          });
        }
      });
  }

  useEffect(() => {
    const controller = new AbortController();
    void load(controller.signal).finally(() => setLoading(false));
    return () => controller.abort();
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [t]);

  async function save() {
    if (!settings) {
      return;
    }
    setBusy(true);
    setFeedback(null);
    try {
      const updated = await updateNetworkSettings({
        requireRemoteHTTPS,
        revision: settings.revision,
      });
      setSettings(updated);
      setRequireRemoteHTTPS(updated.requireRemoteHTTPS);
      setFeedback({ tone: "success", message: t("owner.networkSaved") });
    } catch (error) {
      setFeedback({
        tone: "error",
        message: networkSaveErrorMessage(error, t),
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

  // The toggle is read-only when the deployment pins it, and it cannot be turned
  // on from a remote plaintext page because that would lock the Owner out.
  const view = networkSettingsViewState({
    settings,
    pendingRequireRemoteHTTPS: requireRemoteHTTPS,
    loading,
    busy,
  });

  return (
    <div className="owner-network-settings">
      <section className="team-registration-section owner-network-card">
        <div className="section-heading">
          <div>
            <p className="eyebrow">{t("owner.networkEyebrow")}</p>
            <h2>{t("owner.networkCardTitle")}</h2>
            <span>{t("owner.networkCardDescription")}</span>
          </div>
          <button
            className="primary-button"
            type="button"
            disabled={view.saveDisabled}
            onClick={() => void save()}
          >
            {busy ? t("owner.networkSaving") : t("owner.networkSave")}
          </button>
        </div>

        {loading ? (
          <OwnerLoading label={t("owner.networkLoading")} />
        ) : settings ? (
          <>
            <div className="team-registration-controls owner-network-controls">
              <label>
                <input
                  type="checkbox"
                  checked={requireRemoteHTTPS}
                  disabled={view.toggleDisabled}
                  onChange={(event) =>
                    setRequireRemoteHTTPS(event.target.checked)
                  }
                />
                <span>
                  <strong>{t("owner.networkRequireHTTPS")}</strong>
                  <small>
                    {requireRemoteHTTPS
                      ? t("owner.networkRequireHTTPSOn")
                      : t("owner.networkRequireHTTPSOff")}
                  </small>
                </span>
              </label>
            </div>

            {view.environmentForced ? (
              <p className="owner-network-notice is-info" role="status">
                {t("owner.networkEnvironmentForced")}
              </p>
            ) : null}

            {view.cannotEnable ? (
              <p className="owner-network-notice is-warning" role="status">
                {t("owner.networkInsecureOrigin")}
              </p>
            ) : null}

            {view.showPlaintextRisk ? (
              <p className="owner-network-notice is-warning" role="status">
                {t("owner.networkPlaintextRisk")}
              </p>
            ) : null}

            {view.showEnableWarning ? (
              <p className="owner-network-notice is-danger" role="alert">
                {t("owner.networkEnableWarning")}
              </p>
            ) : null}

            <div className="owner-network-status">
              <span>{t("owner.networkEffectiveStatus")}</span>
              <strong>
                {settings.effectiveRequireRemoteHTTPS
                  ? t("owner.networkEffectiveRequired")
                  : t("owner.networkEffectiveOptional")}
              </strong>
            </div>

            <p className="owner-network-hint">
              {t("owner.networkLoopbackHint")}
            </p>
            <p className="owner-network-hint">
              {t("owner.networkNoRestartHint")}
            </p>

            {hostAccess ? (
              <div className="owner-host-access">
                <div className="owner-network-status">
                  <span>{t("owner.hostAccessStatus")}</span>
                  <strong>
                    {hostAccess.effectiveAllowWebHostPaths
                      ? t("owner.hostAccessOn")
                      : t("owner.hostAccessOff")}
                  </strong>
                </div>
                <p className="owner-network-hint">
                  {hostAccess.environmentForced
                    ? t("owner.hostAccessEnvironmentForced")
                    : t("owner.hostAccessSetOnce")}
                </p>
              </div>
            ) : null}
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

function networkSaveErrorMessage(error: unknown, t: Translator) {
  if (error instanceof ApiError) {
    switch (error.code) {
      case "system_settings.revision_conflict":
        return t("owner.networkRevisionConflict");
      case "system_settings.insecure_origin":
        return t("owner.networkInsecureOrigin");
      case "system_settings.environment_forced":
        return t("owner.networkEnvironmentForced");
      default:
        break;
    }
  }
  return error instanceof Error ? error.message : t("owner.networkSaveFailed");
}

function OwnerStorageSettings({ hostManagement }: { hostManagement: boolean }) {
  const [providers, setProviders] = useState<StorageProvider[]>([]);
  const [grants, setGrants] = useState<ProjectStorageGrant[]>([]);
  const [buckets, setBuckets] = useState<LocalManagedBucket[]>([]);
  const [uploadChecks, setUploadChecks] = useState<UploadCheckListItem[]>([]);
  const [uploadCheckTotal, setUploadCheckTotal] = useState(0);
  const [quarantineTotal, setQuarantineTotal] = useState(0);
  const [uploadCheckFilter, setUploadCheckFilter] =
    useState<UploadCheckStatus>("quarantined");
  const [uploadCheckMessages, setUploadCheckMessages] = useState<
    Record<string, string>
  >({});
  const [selectedStorageKey, setSelectedStorageKey] = useState("");
  const [showStorageWizard, setShowStorageWizard] = useState(false);
  const [showStorageDanger, setShowStorageDanger] = useState(false);
  const [storageWizardStep, setStorageWizardStep] = useState(1);
  const [storageWizardType, setStorageWizardType] =
    useState<StorageWizardType>("local");
  const [storageWizardUsage, setStorageWizardUsage] =
    useState<StorageWizardUsage>("upload");
  const [storageWizardAvailability, setStorageWizardAvailability] =
    useState<StorageWizardAvailability>("all");
  const [bucketForm, setBucketForm] = useState({
    displayName: "本机上传位置",
    localPath: "",
    purpose: "upload" as LocalManagedBucketPurpose,
    quotaGb: "",
    uploadSecurityPolicy: "standard" as UploadSecurityPolicy,
    projectAvailable: true,
  });
  const [remoteForm, setRemoteForm] = useState({
    name: "",
    endpoint: "",
    basePath: "",
    username: "",
    password: "",
    region: "",
    bucket: "",
    pathStyle: true,
    allowPrivateNetwork: false,
  });
  const [bucketEditForm, setBucketEditForm] = useState({
    displayName: "",
    purpose: "upload" as LocalManagedBucketPurpose,
    quotaGb: "",
    uploadSecurityPolicy: "standard" as UploadSecurityPolicy,
    projectAvailable: true,
    status: "active" as LocalManagedBucket["status"],
  });
  const [providerEditForm, setProviderEditForm] = useState({
    name: "",
    endpoint: "",
    basePath: "",
    username: "",
    password: "",
    region: "",
    bucket: "",
    pathStyle: true,
    allowPrivateNetwork: false,
  });
  const [loading, setLoading] = useState(true);
  const [loadingUploadChecks, setLoadingUploadChecks] = useState(true);
  const [storageDeleteImpact, setStorageDeleteImpact] =
    useState<StorageDeleteImpact | null>(null);
  const [loadingStorageDeleteImpact, setLoadingStorageDeleteImpact] =
    useState(false);
  const [storageDeleteImpactError, setStorageDeleteImpactError] = useState<
    string | null
  >(null);
  const [busyKey, setBusyKey] = useState<string | null>(null);
  const [feedback, setFeedback] = useState<InlineFeedbackState | null>(null);
  const [bucketPathError, setBucketPathError] = useState<string | null>(null);
  const [providerActionFeedback, setProviderActionFeedback] = useState<
    Record<string, InlineFeedbackState>
  >({});
  const bucketPathInputRef = useRef<HTMLInputElement>(null);

  useEffect(() => {
    if (!feedback) {
      return;
    }
    const timeout = window.setTimeout(
      () => setFeedback(null),
      feedback.tone === "error" ? 7000 : 4500,
    );
    return () => window.clearTimeout(timeout);
  }, [feedback]);

  async function refreshStorageSettings(signal?: AbortSignal) {
    const [providerItems, grantItems, bucketItems] = await Promise.all([
      listStorageProviders(signal),
      listProjectStorageGrants(signal),
      listLocalManagedBuckets(signal),
    ]);
    setProviders(providerItems);
    setGrants(grantItems);
    setBuckets(bucketItems);
  }

  async function refreshUploadChecks(
    statusFilter = uploadCheckFilter,
    signal?: AbortSignal,
  ) {
    const [page, quarantinePage] = await Promise.all([
      listUploadChecks({ status: statusFilter, pageSize: 24 }, signal),
      statusFilter === "quarantined"
        ? Promise.resolve(null)
        : listUploadChecks({ status: "quarantined", pageSize: 1 }, signal),
    ]);
    setUploadChecks(page.items);
    setUploadCheckTotal(page.total);
    setQuarantineTotal(
      statusFilter === "quarantined"
        ? page.total
        : (quarantinePage?.total ?? 0),
    );
  }

  async function handleRefreshUploadChecks() {
    setLoadingUploadChecks(true);
    setFeedback(null);
    try {
      await refreshUploadChecks();
    } catch (error) {
      setFeedback({
        tone: "error",
        message:
          error instanceof Error ? error.message : "无法读取上传隔离队列",
      });
    } finally {
      setLoadingUploadChecks(false);
    }
  }

  useEffect(() => {
    const controller = new AbortController();
    refreshStorageSettings(controller.signal)
      .catch((error: unknown) => {
        if (!isAbort(error)) {
          setFeedback({
            tone: "error",
            message:
              error instanceof Error
                ? error.message
                : "无法读取存储与上传安全设置",
          });
        }
      })
      .finally(() => setLoading(false));
    return () => controller.abort();
  }, []);

  useEffect(() => {
    const controller = new AbortController();
    setLoadingUploadChecks(true);
    refreshUploadChecks(uploadCheckFilter, controller.signal)
      .catch((error: unknown) => {
        if (!isAbort(error)) {
          setFeedback({
            tone: "error",
            message:
              error instanceof Error ? error.message : "无法读取上传隔离队列",
          });
        }
      })
      .finally(() => setLoadingUploadChecks(false));
    return () => controller.abort();
  }, [uploadCheckFilter]);

  const activeGrants = grants.filter((item) => item.status === "active");
  const activeBuckets = buckets.filter((item) => item.status === "active");
  const availableBuckets = activeBuckets.filter(
    (item) => item.projectAvailable,
  );
  const defaultBucket =
    availableBuckets.find((item) => item.purpose === "upload") ??
    availableBuckets[0] ??
    activeBuckets.find((item) => item.purpose === "upload") ??
    activeBuckets[0] ??
    buckets[0] ??
    null;
  const remoteProviderCount = providers.filter(isRemoteStorageProvider).length;
  const storageErrorCount =
    buckets.filter((item) => item.status === "error").length +
    providers.filter(
      (provider) =>
        isRemoteStorageProvider(provider) && providerNeedsAttention(provider),
    ).length;
  const defaultBucketUsage =
    defaultBucket?.quotaBytes && defaultBucket.usedBytesEstimate != null
      ? Math.min(
          100,
          Math.round(
            (defaultBucket.usedBytesEstimate / defaultBucket.quotaBytes) * 100,
          ),
        )
      : null;
  const storageLocations = useMemo<OwnerStorageLocation[]>(() => {
    const localLocations = buckets.map((bucket) => ({
      key: `bucket:${bucket.id}`,
      kind: "bucket" as const,
      title: bucket.displayName,
      subtitle: "本机托管存储位置",
      meta: `${bucketPurposeLabel(bucket.purpose)} · ${bucketSecurityPolicyLabel(
        bucket.uploadSecurityPolicy,
      )}`,
      status: bucket.status,
      statusLabel: bucketStatusLabel(bucket.status),
      bucket,
    }));
    const remoteLocations = grants.flatMap((grant) => {
      if (grant.localManagedBucketId) {
        return [];
      }
      const provider = providers.find(
        (item) => item.id === grant.storageProviderId,
      );
      if (!isRemoteStorageProvider(provider)) {
        return [];
      }
      const providerStatus = provider
        ? providerEffectiveStatus(provider)
        : grant.status;
      const remoteUploadReady = canGrantUploadToRemote(grant, provider);
      return [
        {
          key: `grant:${grant.id}`,
          kind: "grant" as const,
          title: storageProviderDisplayName(grant.providerName),
          subtitle: grant.rootName
            ? storageRootDisplayName(grant.rootName)
            : "整个连接",
          meta: `远程存储 · ${
            provider && providerStatus !== "active"
              ? storageStatusLabel(providerStatus)
              : grant.status === "active"
                ? remoteUploadReady
                  ? "上传/预览/归档可用"
                  : "预览/归档可用"
                : "已暂停"
          }`,
          status: providerStatus,
          statusLabel: provider
            ? storageStatusLabel(providerStatus)
            : grant.status === "active"
              ? "项目可用"
              : "已暂停",
          grant,
          ...(provider ? { provider } : {}),
        },
      ];
    });
    const grantedProviderIds = new Set(
      grants.map((grant) => grant.storageProviderId),
    );
    const unopenedProviders = providers
      .filter(
        (provider) =>
          isRemoteStorageProvider(provider) &&
          !grantedProviderIds.has(provider.id),
      )
      .map((provider) => ({
        key: `provider:${provider.id}`,
        kind: "provider" as const,
        title: storageProviderDisplayName(provider.name),
        subtitle: storageKindLabel(provider.kind),
        meta: "已连接，尚未开放给项目预览/归档",
        status: providerEffectiveStatus(provider),
        statusLabel: storageStatusLabel(providerEffectiveStatus(provider)),
        provider,
      }));
    return [...localLocations, ...remoteLocations, ...unopenedProviders];
  }, [buckets, grants, providers]);
  const storageLocationCount = storageLocations.length;
  const selectedStorage =
    storageLocations.find((item) => item.key === selectedStorageKey) ??
    storageLocations[0] ??
    null;
  const selectedProviderActionFeedback = selectedStorage?.provider
    ? (providerActionFeedback[selectedStorage.provider.id] ?? null)
    : null;
  const storageExceptionItems = useMemo(() => {
    const bucketItems = buckets
      .filter((bucket) => bucket.status === "error")
      .map((bucket) => ({
        key: `bucket:${bucket.id}`,
        title: bucket.displayName,
        type: "本机托管",
        status: bucketStatusLabel(bucket.status),
        reason: "本机托管位置异常，请回到主机检查目录、权限或磁盘状态。",
      }));
    const providerItems = providers
      .filter(
        (provider) =>
          isRemoteStorageProvider(provider) && providerNeedsAttention(provider),
      )
      .map((provider) => {
        const grant = grants.find(
          (item) => item.storageProviderId === provider.id,
        );
        const providerStatus = providerEffectiveStatus(provider);
        return {
          key: grant ? `grant:${grant.id}` : `provider:${provider.id}`,
          title: storageProviderDisplayName(provider.name),
          type: storageKindLabel(provider.kind),
          status: storageStatusLabel(providerStatus),
          reason:
            provider.lastErrorMessage ??
            (providerStatus === "offline"
              ? "连接尚未测试或当前离线，请进入详情测试连接。"
              : "连接测试失败，请检查地址、账号、密码或网络访问策略。"),
        };
      });
    return [...bucketItems, ...providerItems];
  }, [buckets, grants, providers]);

  useEffect(() => {
    if (storageLocations.length === 0) {
      if (selectedStorageKey) {
        setSelectedStorageKey("");
      }
      return;
    }
    if (!storageLocations.some((item) => item.key === selectedStorageKey)) {
      setSelectedStorageKey(storageLocations[0]!.key);
    }
  }, [selectedStorageKey, storageLocations]);

  useEffect(() => {
    if (selectedStorage?.bucket) {
      setBucketEditForm({
        displayName: selectedStorage.bucket.displayName,
        purpose: selectedStorage.bucket.purpose,
        quotaGb: formatQuotaGbForInput(selectedStorage.bucket.quotaBytes),
        uploadSecurityPolicy: selectedStorage.bucket.uploadSecurityPolicy,
        projectAvailable: selectedStorage.bucket.projectAvailable,
        status: selectedStorage.bucket.status,
      });
    }
    if (selectedStorage?.provider) {
      setProviderEditForm({
        name: selectedStorage.provider.name,
        endpoint: selectedStorage.provider.endpoint,
        basePath: selectedStorage.provider.basePath,
        username: "",
        password: "",
        region: selectedStorage.provider.region ?? "",
        bucket: selectedStorage.provider.bucket ?? "",
        pathStyle: selectedStorage.provider.pathStyle,
        allowPrivateNetwork: selectedStorage.provider.allowPrivateNetwork,
      });
    }
  }, [selectedStorage?.key]);

  useEffect(() => {
    setShowStorageDanger(false);
  }, [selectedStorage?.key]);

  useEffect(() => {
    if (!selectedStorage) {
      setStorageDeleteImpact(null);
      setStorageDeleteImpactError(null);
      setLoadingStorageDeleteImpact(false);
      return;
    }
    if (!showStorageDanger) {
      setStorageDeleteImpact(null);
      setStorageDeleteImpactError(null);
      setLoadingStorageDeleteImpact(false);
      return;
    }
    const controller = new AbortController();
    setLoadingStorageDeleteImpact(true);
    setStorageDeleteImpact(null);
    setStorageDeleteImpactError(null);
    const impactRequest = selectedStorage.bucket
      ? getLocalManagedBucketDeleteImpact(
          selectedStorage.bucket.id,
          controller.signal,
        )
      : selectedStorage.provider
        ? getStorageProviderDeleteImpact(
            selectedStorage.provider.id,
            controller.signal,
          )
        : null;
    if (!impactRequest) {
      setLoadingStorageDeleteImpact(false);
      return () => controller.abort();
    }
    impactRequest
      .then((impact) => setStorageDeleteImpact(impact))
      .catch((error: unknown) => {
        if (!isAbort(error)) {
          setStorageDeleteImpactError(
            error instanceof Error ? error.message : "无法读取删除影响清单",
          );
        }
      })
      .finally(() => setLoadingStorageDeleteImpact(false));
    return () => controller.abort();
  }, [
    selectedStorage?.key,
    selectedStorage?.bucket?.revision,
    selectedStorage?.bucket?.status,
    selectedStorage?.bucket?.projectAvailable,
    selectedStorage?.provider?.revision,
    selectedStorage?.provider?.status,
    selectedStorage?.grant?.status,
    showStorageDanger,
  ]);

  async function saveGrant(
    nextProviderId: string,
    nextRootId: string | null,
    nextStatus: ProjectStorageGrant["status"],
  ) {
    if (!nextProviderId) {
      setFeedback({ tone: "error", message: "请先选择存储连接" });
      return;
    }
    const key = `${nextProviderId}:${nextRootId ?? "provider"}`;
    setBusyKey(key);
    setFeedback(null);
    try {
      const updated = await setProjectStorageGrant({
        storageProviderId: nextProviderId,
        authorizedRootId: nextRootId,
        status: nextStatus,
      });
      setGrants((current) => {
        const exists = current.some((item) => item.id === updated.id);
        return exists
          ? current.map((item) => (item.id === updated.id ? updated : item))
          : [updated, ...current];
      });
      setFeedback({
        tone: "success",
        message:
          nextStatus === "active"
            ? "存储已开放给项目选择"
            : "存储已从项目可选范围停用",
      });
    } catch (error) {
      setFeedback({
        tone: "error",
        message: error instanceof Error ? error.message : "无法保存存储授权",
      });
    } finally {
      setBusyKey(null);
    }
  }

  async function createBucket() {
    const quotaBytes = parseQuotaGb(bucketForm.quotaGb);
    if (quotaBytes === "invalid") {
      setFeedback({
        tone: "error",
        message: "容量上限需要填写大于或等于 0 的数字",
      });
      return;
    }
    setBusyKey("bucket:create");
    setFeedback(null);
    setBucketPathError(null);
    const resolvedPath = resolveManagedBucketPath(bucketForm.localPath);
    if (resolvedPath.expandedDriveRoot) {
      setBucketForm((current) => ({
        ...current,
        localPath: resolvedPath.path,
      }));
    }
    try {
      const purpose = wizardUsageToBucketPurpose(storageWizardUsage);
      await createLocalManagedBucket({
        displayName: bucketForm.displayName,
        localPath: resolvedPath.path,
        purpose,
        quotaBytes,
        uploadSecurityPolicy: bucketForm.uploadSecurityPolicy,
        projectAvailable: storageWizardAvailability === "all",
      });
      await refreshStorageSettings();
      setBucketForm((current) => ({
        ...current,
        displayName: "本机上传位置",
        localPath: "",
        quotaGb: "",
        uploadSecurityPolicy: "standard",
        projectAvailable: true,
      }));
      setStorageWizardUsage("upload");
      setStorageWizardAvailability("all");
      setShowStorageWizard(false);
      setStorageWizardStep(1);
      setFeedback({
        tone: "success",
        message: "本机托管存储位置已创建",
      });
    } catch (error) {
      if (
        error instanceof ApiError &&
        (error.code === "storage.path_invalid" ||
          error.code === "storage.unavailable")
      ) {
        const message =
          error.code === "storage.path_invalid"
            ? "无法使用这个目录，请选择主机上的可写文件夹。"
            : "无法访问这个目录，请确认文件夹存在且 Visto 有写入权限。";
        setBucketPathError(message);
        setStorageWizardStep(3);
        setFeedback({ tone: "error", message });
        window.requestAnimationFrame(() => bucketPathInputRef.current?.focus());
        return;
      }
      setFeedback({
        tone: "error",
        message:
          error instanceof Error ? error.message : "无法创建本机托管存储位置",
      });
    } finally {
      setBusyKey(null);
    }
  }

  async function createRemoteStorage() {
    const kind = storageWizardType as StorageProviderKind;
    if (kind !== "webdav" && kind !== "s3") {
      setFeedback({ tone: "error", message: "请选择远程存储类型" });
      return;
    }
    if (!remoteForm.name.trim() || !remoteForm.endpoint.trim()) {
      setFeedback({ tone: "error", message: "请填写连接名称和连接地址" });
      return;
    }
    if (
      kind === "s3" &&
      (!remoteForm.region.trim() || !remoteForm.bucket.trim())
    ) {
      setFeedback({ tone: "error", message: "S3 连接需要填写区域和桶名称" });
      return;
    }
    setBusyKey("remote:create");
    setFeedback(null);
    try {
      const input: StorageProviderInput = {
        kind,
        name: remoteForm.name.trim(),
        endpoint: remoteForm.endpoint.trim(),
        basePath: remoteForm.basePath.trim(),
        allowPrivateNetwork: remoteForm.allowPrivateNetwork,
        pathStyle: remoteForm.pathStyle,
      };
      if (kind === "webdav") {
        const username = remoteForm.username.trim();
        if (username) input.username = username;
        if (remoteForm.password) input.password = remoteForm.password;
      } else {
        input.region = remoteForm.region.trim();
        input.bucket = remoteForm.bucket.trim();
        const accessKeyId = remoteForm.username.trim();
        if (accessKeyId) input.accessKeyId = accessKeyId;
        if (remoteForm.password) input.secretAccessKey = remoteForm.password;
      }

      const created = await createStorageProvider(input);
      let root: AuthorizedRoot | null = null;
      let testedProvider = created;
      try {
        const report = await testStorageProvider(created.id);
        testedProvider = {
          ...created,
          status: "active",
          capabilities: report.capabilities,
          lastTestAt: report.testedAt,
          lastTestStatus: "succeeded",
          lastErrorCode: null,
          lastErrorMessage: null,
        };
      } catch {
        testedProvider = {
          ...created,
          status: "error",
          lastTestStatus: "failed",
        };
      }

      const shouldRegisterRoot = storageWizardUsage === "upload";
      if (shouldRegisterRoot && testedProvider.status === "active") {
        root = await registerProviderRoot(created.id, {
          displayName: remoteForm.name.trim(),
          basePath: "",
          mode: "managed",
          scanEnabled: false,
        });
      }
      if (storageWizardAvailability === "all") {
        await setProjectStorageGrant({
          storageProviderId: created.id,
          authorizedRootId: root?.id ?? null,
          status: "active",
        });
      }
      await refreshStorageSettings();
      setRemoteForm({
        name: "",
        endpoint: "",
        basePath: "",
        username: "",
        password: "",
        region: "",
        bucket: "",
        pathStyle: true,
        allowPrivateNetwork: false,
      });
      setStorageWizardType("local");
      setStorageWizardUsage("upload");
      setStorageWizardAvailability("all");
      setShowStorageWizard(false);
      setStorageWizardStep(1);
      setFeedback({
        tone: testedProvider.status === "active" ? "success" : "error",
        message:
          testedProvider.status === "active"
            ? root
              ? storageWizardUsage === "upload"
                ? "远程上传位置已接入"
                : "远程存储已添加"
              : "远程存储已添加"
            : "远程存储已保存，但连接测试未通过，请稍后在存储详情中检查",
      });
    } catch (error) {
      setFeedback({
        tone: "error",
        message: error instanceof Error ? error.message : "无法添加远程存储",
      });
    } finally {
      setBusyKey(null);
    }
  }

  async function saveBucket(
    bucket: LocalManagedBucket,
    patch: Partial<
      Pick<
        LocalManagedBucket,
        | "displayName"
        | "purpose"
        | "quotaBytes"
        | "uploadSecurityPolicy"
        | "projectAvailable"
        | "status"
      >
    > = {},
  ) {
    const nextBucket = { ...bucket, ...patch };
    setBusyKey(`bucket:${bucket.id}`);
    setFeedback(null);
    try {
      const updated = await updateLocalManagedBucket(bucket, {
        displayName: nextBucket.displayName,
        purpose: nextBucket.purpose,
        quotaBytes: nextBucket.quotaBytes,
        uploadSecurityPolicy: nextBucket.uploadSecurityPolicy,
        projectAvailable: nextBucket.projectAvailable,
        status: nextBucket.status,
      });
      setBuckets((current) =>
        current.map((item) => (item.id === updated.id ? updated : item)),
      );
      setFeedback({
        tone: "success",
        message: "本机托管存储位置已更新",
      });
    } catch (error) {
      setFeedback({
        tone: "error",
        message:
          error instanceof Error ? error.message : "无法更新本地托管存储桶",
      });
    } finally {
      setBusyKey(null);
    }
  }

  async function saveBucketDetails(bucket: LocalManagedBucket) {
    const quotaBytes = parseQuotaGb(bucketEditForm.quotaGb);
    if (quotaBytes === "invalid") {
      setFeedback({
        tone: "error",
        message: "容量上限需要填写大于或等于 0 的数字",
      });
      return;
    }
    await saveBucket(bucket, {
      displayName: bucketEditForm.displayName.trim(),
      purpose: bucketEditForm.purpose,
      quotaBytes,
      uploadSecurityPolicy: bucketEditForm.uploadSecurityPolicy,
      projectAvailable: bucketEditForm.projectAvailable,
      status: bucketEditForm.status,
    });
  }

  async function saveProviderDetails(provider: StorageProvider) {
    const kind = provider.kind as StorageProviderKind;
    if (kind !== "webdav" && kind !== "s3") {
      setProviderActionFeedback((current) => ({
        ...current,
        [provider.id]: {
          tone: "error",
          message: "这个存储类型暂不支持编辑",
        },
      }));
      return;
    }
    if (!providerEditForm.name.trim() || !providerEditForm.endpoint.trim()) {
      setProviderActionFeedback((current) => ({
        ...current,
        [provider.id]: {
          tone: "error",
          message: "请填写连接名称和连接地址",
        },
      }));
      return;
    }
    if (
      kind === "s3" &&
      (!providerEditForm.region.trim() || !providerEditForm.bucket.trim())
    ) {
      setProviderActionFeedback((current) => ({
        ...current,
        [provider.id]: {
          tone: "error",
          message: "S3 连接需要填写区域和桶名称",
        },
      }));
      return;
    }
    const savedUsername = providerEditForm.username.trim();
    const passwordWasProvided = providerEditForm.password.length > 0;
    const input: StorageProviderInput = {
      kind,
      name: providerEditForm.name.trim(),
      endpoint: providerEditForm.endpoint.trim(),
      basePath: providerEditForm.basePath.trim(),
      allowPrivateNetwork: providerEditForm.allowPrivateNetwork,
      pathStyle: providerEditForm.pathStyle,
    };
    if (kind === "webdav") {
      const username = providerEditForm.username.trim();
      if (username) input.username = username;
      if (providerEditForm.password) input.password = providerEditForm.password;
    } else {
      input.region = providerEditForm.region.trim();
      input.bucket = providerEditForm.bucket.trim();
      const accessKeyId = providerEditForm.username.trim();
      if (accessKeyId) input.accessKeyId = accessKeyId;
      if (providerEditForm.password) {
        input.secretAccessKey = providerEditForm.password;
      }
    }
    setBusyKey(`provider:${provider.id}:save`);
    setFeedback(null);
    setProviderActionFeedback((current) => ({
      ...current,
      [provider.id]: { tone: "info", message: "正在保存连接信息…" },
    }));
    try {
      const updated = await updateStorageProvider(provider, input);
      setProviders((current) =>
        current.map((item) => (item.id === updated.id ? updated : item)),
      );
      setProviderEditForm((current) => ({
        ...current,
        username: savedUsername || current.username,
        password: "",
      }));
      setProviderActionFeedback((current) => ({
        ...current,
        [provider.id]: {
          tone: "success",
          message: passwordWasProvided
            ? "连接信息已保存，密码已加密保存。请测试连接确认可用性。"
            : "连接信息已保存，未重新输入的凭据会继续保留。请测试连接确认可用性。",
        },
      }));
    } catch (error) {
      setProviderActionFeedback((current) => ({
        ...current,
        [provider.id]: {
          tone: "error",
          message: error instanceof Error ? error.message : "无法保存远程存储",
        },
      }));
    } finally {
      setBusyKey(null);
    }
  }

  async function handleTestProvider(provider: StorageProvider) {
    setBusyKey(`provider:${provider.id}:test`);
    setFeedback(null);
    setProviderActionFeedback((current) => ({
      ...current,
      [provider.id]: { tone: "info", message: "正在测试连接…" },
    }));
    try {
      const report = await testStorageProvider(provider.id);
      await refreshStorageSettings();
      const warningText =
        report.warnings.length > 0 ? `；${report.warnings.join("；")}` : "";
      setProviderActionFeedback((current) => ({
        ...current,
        [provider.id]: {
          tone: "success",
          message: `连接测试通过，延迟 ${report.latencyMs}ms${warningText}`,
        },
      }));
    } catch (error) {
      await refreshStorageSettings().catch(() => undefined);
      setProviderActionFeedback((current) => ({
        ...current,
        [provider.id]: {
          tone: "error",
          message: error instanceof Error ? error.message : "远程存储测试失败",
        },
      }));
    } finally {
      setBusyKey(null);
    }
  }

  async function handleDisableSelectedStorage(location: OwnerStorageLocation) {
    if (location.bucket) {
      await saveBucket(location.bucket, {
        projectAvailable: false,
        status: "disabled",
      });
      return;
    }
    if (location.grant) {
      await saveGrant(
        location.grant.storageProviderId,
        location.grant.authorizedRootId,
        "disabled",
      );
      return;
    }
    setFeedback({
      tone: "info",
      message:
        "这个远程连接尚未开放给项目；如果不再需要，可以在影响清单为空时删除。",
    });
  }

  async function handleDeleteSelectedStorage(
    location: OwnerStorageLocation,
    impact: StorageDeleteImpact | null,
  ) {
    if (!impact?.canDelete) {
      setFeedback({
        tone: "error",
        message: "这个存储位置还有项目、媒体、审阅或任务引用，不能直接删除。",
      });
      return;
    }
    const confirmed = window.confirm(
      "仅删除 Visto 中的存储元数据，不会删除磁盘、WebDAV 或 S3 上的真实文件。确定继续吗？",
    );
    if (!confirmed) {
      return;
    }
    const key = `delete:${location.key}`;
    setBusyKey(key);
    setFeedback(null);
    try {
      if (location.bucket) {
        await deleteLocalManagedBucket(location.bucket);
      } else if (location.provider) {
        await deleteStorageProvider(location.provider);
      } else {
        throw new Error("没有可删除的存储位置");
      }
      await refreshStorageSettings();
      setSelectedStorageKey("");
      setStorageDeleteImpact(null);
      setFeedback({
        tone: "success",
        message: "空存储位置已从 Visto 中移除，真实文件未被删除。",
      });
    } catch (error) {
      setFeedback({
        tone: "error",
        message: error instanceof Error ? error.message : "无法删除存储位置",
      });
    } finally {
      setBusyKey(null);
    }
  }

  async function handleUploadCheckAction(
    item: UploadCheckListItem,
    action: "release" | "reject",
  ) {
    const check = item.uploadCheck;
    const key = `upload-check:${check.id}:${action}`;
    setBusyKey(key);
    setFeedback(null);
    try {
      const message = uploadCheckMessages[check.id]?.trim();
      const input = message ? { message } : {};
      await (action === "release"
        ? releaseUploadCheck(check.id, input)
        : rejectUploadCheck(check.id, input));
      await refreshUploadChecks();
      setUploadCheckMessages((current) => {
        const next = { ...current };
        delete next[check.id];
        return next;
      });
      setFeedback({
        tone: "success",
        message:
          action === "release"
            ? "上传已人工放行，并已重新进入媒体处理队列"
            : "上传已人工拒绝，并会继续阻止审阅、分享和下载",
      });
    } catch (error) {
      setFeedback({
        tone: "error",
        message:
          error instanceof Error ? error.message : "无法处理上传检查记录",
      });
    } finally {
      setBusyKey(null);
    }
  }

  if (loading) {
    return <OwnerLoading label="正在读取存储与上传安全设置" />;
  }

  function openStorageWizard() {
    setShowStorageWizard(true);
    window.setTimeout(() => {
      document
        .getElementById("owner-local-storage-form")
        ?.scrollIntoView({ behavior: "smooth", block: "start" });
    }, 0);
  }

  const hasBucketLocationDraft =
    bucketForm.displayName.trim() !== "" && bucketForm.localPath.trim() !== "";
  const hasRemoteLocationDraft =
    remoteForm.name.trim() !== "" &&
    remoteForm.endpoint.trim() !== "" &&
    (storageWizardType !== "s3" ||
      (remoteForm.region.trim() !== "" && remoteForm.bucket.trim() !== ""));
  const isLocalStorageWizard = storageWizardType === "local";
  const canAdvanceStorageWizard =
    storageWizardStep === 1 ||
    storageWizardStep === 2 ||
    storageWizardStep === 4 ||
    storageWizardStep === 5 ||
    (storageWizardStep === 3 &&
      (isLocalStorageWizard ? hasBucketLocationDraft : hasRemoteLocationDraft));

  return (
    <div className="owner-storage-settings">
      {feedback ? (
        <div
          className={`owner-storage-toast is-${feedback.tone}`}
          role={feedback.tone === "error" ? "alert" : "status"}
          aria-live="polite"
        >
          {feedback.tone === "success" ? (
            <CheckCircle2 size={18} aria-hidden="true" />
          ) : feedback.tone === "error" ? (
            <AlertCircle size={18} aria-hidden="true" />
          ) : (
            <Info size={18} aria-hidden="true" />
          )}
          <span>{feedback.message}</span>
          <button
            type="button"
            aria-label="关闭提示"
            title="关闭"
            onClick={() => setFeedback(null)}
          >
            <X size={16} aria-hidden="true" />
          </button>
        </div>
      ) : null}
      <section className="owner-storage-overview">
        <div className="owner-storage-overview-copy">
          <p className="eyebrow">STORAGE MAP</p>
          <h3>团队文件落点</h3>
          <p>
            团队上传的新文件会保存到 Owner
            开放的存储位置。项目媒体上传只在具体项目里发生，项目成员只能使用已授权给项目的存储。
          </p>
        </div>
        <button
          className="primary-button"
          type="button"
          onClick={openStorageWizard}
        >
          添加存储
        </button>
      </section>

      <div className="owner-storage-summary-grid" aria-label="存储摘要">
        <article>
          <span>存储位置</span>
          <strong>{storageLocationCount}</strong>
          <small>
            本机 {buckets.length} 个，远程连接 {remoteProviderCount} 个
          </small>
        </article>
        <article>
          <span>默认上传</span>
          <strong>{defaultBucket ? "已设置" : "未设置"}</strong>
          <small>
            {defaultBucket?.displayName ?? "先添加本机托管存储位置"}
          </small>
        </article>
        <article>
          <span>项目可用</span>
          <strong>{activeGrants.length}</strong>
          <small>项目主管只能选择这些开放范围</small>
        </article>
        <article className={storageErrorCount > 0 ? "is-warning" : ""}>
          <span>异常 / 隔离</span>
          <strong>
            {storageErrorCount} / {quarantineTotal}
          </strong>
          <small>
            {storageErrorCount > 0 || quarantineTotal > 0
              ? "需要 Owner 查看处理"
              : "当前没有需要处理的异常"}
          </small>
        </article>
      </div>

      {storageExceptionItems.length > 0 ? (
        <section
          className="owner-storage-exception-panel"
          aria-label="存储异常"
        >
          <header>
            <div>
              <p className="eyebrow">NEEDS ATTENTION</p>
              <h3>需要处理的存储</h3>
            </div>
            <span>{storageExceptionItems.length} 个异常</span>
          </header>
          <div>
            {storageExceptionItems.map((item) => (
              <button
                key={item.key}
                type="button"
                onClick={() => setSelectedStorageKey(item.key)}
              >
                <strong>{item.title}</strong>
                <span>
                  {item.type} · {item.status}
                </span>
                <small>{item.reason}</small>
              </button>
            ))}
          </div>
        </section>
      ) : null}

      <section
        className={`owner-default-storage-card ${
          defaultBucket ? "" : "is-empty"
        }`}
      >
        <div>
          <p className="eyebrow">默认上传位置</p>
          <h3>{defaultBucket?.displayName ?? "还没有可用的存储位置"}</h3>
          <p>
            {defaultBucket
              ? `${bucketPurposeLabel(defaultBucket.purpose)} · ${bucketStatusLabel(
                  defaultBucket.status,
                )} · ${bucketSecurityPolicyLabel(defaultBucket.uploadSecurityPolicy)}`
              : "先添加一个本机托管存储位置，项目上传的新文件会保存在这里。"}
          </p>
        </div>
        {defaultBucket ? (
          <div className="owner-default-storage-metrics">
            <span>{defaultBucket.displayPath}</span>
            <strong>
              {defaultBucket.usedBytesEstimate != null
                ? formatBytes(defaultBucket.usedBytesEstimate)
                : "未估算"}
              {defaultBucket.quotaBytes
                ? ` / ${formatBytes(defaultBucket.quotaBytes)}`
                : ""}
            </strong>
            {defaultBucketUsage != null ? (
              <div
                className="owner-storage-meter"
                aria-label={`默认存储已使用 ${defaultBucketUsage}%`}
              >
                <i style={{ width: `${defaultBucketUsage}%` }} />
              </div>
            ) : (
              <small>未设置容量上限</small>
            )}
          </div>
        ) : (
          <button
            className="secondary-button"
            type="button"
            onClick={openStorageWizard}
          >
            去添加存储位置
          </button>
        )}
      </section>

      <section className="owner-storage-workbench">
        <div className="section-heading">
          <div>
            <p className="eyebrow">STORAGE LOCATIONS</p>
            <h3>所有存储位置</h3>
          </div>
          <span className="owner-storage-count">
            {storageLocations.length} 个位置
          </span>
        </div>

        {storageLocations.length === 0 ? (
          <div className="activity-empty compact">
            <strong>还没有可用的存储位置</strong>
            <span>
              先添加一个本机托管、WebDAV 或 S3
              存储。项目上传的新文件必须在项目媒体页里进入这里。
            </span>
          </div>
        ) : (
          <div className="owner-storage-map">
            <div className="owner-storage-location-list">
              {storageLocations.map((item) => (
                <button
                  key={item.key}
                  className={
                    item.key === selectedStorage?.key ? "is-active" : ""
                  }
                  type="button"
                  onClick={() => setSelectedStorageKey(item.key)}
                >
                  <span
                    className={`owner-storage-dot is-${storageStatusTone(
                      item.status,
                    )}`}
                    aria-hidden="true"
                  />
                  <strong>{item.title}</strong>
                  <small>{item.subtitle}</small>
                  <em>{item.meta}</em>
                </button>
              ))}
            </div>

            {selectedStorage ? (
              <article className="owner-storage-detail-panel">
                <header>
                  <div>
                    <p className="eyebrow">
                      {selectedStorage.kind === "bucket"
                        ? "LOCAL MANAGED"
                        : selectedStorage.kind === "grant"
                          ? "PROJECT ACCESS"
                          : "REMOTE CONNECTION"}
                    </p>
                    <h3>{selectedStorage.title}</h3>
                    <span>{selectedStorage.subtitle}</span>
                  </div>
                  <div className="owner-storage-detail-header-actions">
                    <span
                      className={`owner-state-label is-${selectedStorage.status}`}
                    >
                      {selectedStorage.statusLabel}
                    </span>
                  </div>
                </header>

                <div className="owner-storage-detail-grid">
                  <div>
                    <span>类型</span>
                    <strong>
                      {selectedStorage.bucket
                        ? "本机托管"
                        : selectedStorage.provider
                          ? storageKindLabel(selectedStorage.provider.kind)
                          : "远程开放范围"}
                    </strong>
                  </div>
                  <div>
                    <span>用途</span>
                    <strong>
                      {selectedStorage.bucket
                        ? bucketPurposeLabel(selectedStorage.bucket.purpose)
                        : selectedStorage.grant?.bucketPurpose
                          ? bucketPurposeLabel(
                              selectedStorage.grant.bucketPurpose,
                            )
                          : "预览/归档"}
                    </strong>
                  </div>
                  <div>
                    <span>项目可用</span>
                    <strong>
                      {selectedStorage.bucket
                        ? selectedStorage.bucket.projectAvailable
                          ? "允许"
                          : "不允许"
                        : selectedStorage.grant?.status === "active"
                          ? "预览/归档允许"
                          : "未开放"}
                    </strong>
                  </div>
                  <div>
                    <span>容量</span>
                    <strong>
                      {selectedStorage.bucket
                        ? selectedStorage.bucket.quotaBytes
                          ? `${formatBytes(
                              selectedStorage.bucket.usedBytesEstimate ?? 0,
                            )} / ${formatBytes(
                              selectedStorage.bucket.quotaBytes,
                            )}`
                          : "未设上限"
                        : "由远程连接控制"}
                    </strong>
                  </div>
                </div>

                <p className="owner-storage-detail-note">
                  {selectedStorage.bucket
                    ? `${selectedStorage.bucket.displayPath} · ${bucketSecurityPolicyDescription(
                        selectedStorage.bucket.uploadSecurityPolicy,
                      )}`
                    : selectedStorage.grant
                      ? ownerRemoteGrantDescription(
                          selectedStorage.grant,
                          selectedStorage.provider,
                        )
                      : "这个远程连接已经存在，但还没有开放给项目。开放到具体目录后，可在项目设置里作为上传、预览或归档位置选择。"}
                </p>

                {selectedStorage.provider ? (
                  <RemoteTrafficPolicyCard
                    provider={selectedStorage.provider}
                    grant={selectedStorage.grant}
                  />
                ) : null}

                {selectedStorage.bucket ? (
                  <form
                    className="owner-storage-edit-form"
                    onSubmit={(event) => {
                      event.preventDefault();
                      void saveBucketDetails(selectedStorage.bucket!);
                    }}
                  >
                    <label>
                      <span>存储名称</span>
                      <input
                        value={bucketEditForm.displayName}
                        onChange={(event) =>
                          setBucketEditForm((current) => ({
                            ...current,
                            displayName: event.target.value,
                          }))
                        }
                      />
                    </label>
                    <label>
                      <span>用途</span>
                      <select
                        value={bucketEditForm.purpose}
                        onChange={(event) =>
                          setBucketEditForm((current) => ({
                            ...current,
                            purpose: event.target
                              .value as LocalManagedBucketPurpose,
                          }))
                        }
                      >
                        <option value="upload">项目上传</option>
                        <option value="source_archive">归档保存</option>
                        <option value="review_upload">审阅回传</option>
                      </select>
                    </label>
                    <label>
                      <span>上传安全策略</span>
                      <select
                        value={bucketEditForm.uploadSecurityPolicy}
                        onChange={(event) =>
                          setBucketEditForm((current) => ({
                            ...current,
                            uploadSecurityPolicy: event.target
                              .value as UploadSecurityPolicy,
                          }))
                        }
                      >
                        <option value="quick">快速</option>
                        <option value="standard">标准</option>
                        <option value="enhanced">增强</option>
                      </select>
                    </label>
                    <label>
                      <span>容量上限（GB）</span>
                      <input
                        inputMode="decimal"
                        value={bucketEditForm.quotaGb}
                        onChange={(event) =>
                          setBucketEditForm((current) => ({
                            ...current,
                            quotaGb: event.target.value,
                          }))
                        }
                        placeholder="不填则不限"
                      />
                    </label>
                    <footer>
                      <button
                        className="primary-button"
                        type="submit"
                        disabled={
                          busyKey === `bucket:${selectedStorage.bucket.id}`
                        }
                      >
                        {busyKey === `bucket:${selectedStorage.bucket.id}`
                          ? "保存中…"
                          : "保存存储信息"}
                      </button>
                    </footer>
                  </form>
                ) : selectedStorage.provider ? (
                  <form
                    className="owner-storage-edit-form"
                    onSubmit={(event) => {
                      event.preventDefault();
                      void saveProviderDetails(selectedStorage.provider!);
                    }}
                  >
                    <label>
                      <span>连接名称</span>
                      <input
                        value={providerEditForm.name}
                        onChange={(event) =>
                          setProviderEditForm((current) => ({
                            ...current,
                            name: event.target.value,
                          }))
                        }
                      />
                    </label>
                    <label>
                      <span>连接地址</span>
                      <input
                        value={providerEditForm.endpoint}
                        onChange={(event) =>
                          setProviderEditForm((current) => ({
                            ...current,
                            endpoint: event.target.value,
                          }))
                        }
                      />
                    </label>
                    <label>
                      <span>默认路径前缀</span>
                      <input
                        value={providerEditForm.basePath}
                        onChange={(event) =>
                          setProviderEditForm((current) => ({
                            ...current,
                            basePath: event.target.value,
                          }))
                        }
                        placeholder="可留空"
                      />
                    </label>
                    <label>
                      <span>
                        {selectedStorage.provider.kind === "webdav"
                          ? "用户名"
                          : "Access Key"}
                      </span>
                      <input
                        autoComplete="off"
                        value={providerEditForm.username}
                        onChange={(event) =>
                          setProviderEditForm((current) => ({
                            ...current,
                            username: event.target.value,
                          }))
                        }
                        placeholder={
                          selectedStorage.provider.kind === "webdav"
                            ? "输入新用户名可替换；留空则保留"
                            : "输入新 Access Key 可替换；留空则保留"
                        }
                      />
                    </label>
                    <label>
                      <span>
                        {selectedStorage.provider.kind === "webdav"
                          ? "密码"
                          : "Secret Key"}
                      </span>
                      <input
                        type="password"
                        autoComplete="new-password"
                        value={providerEditForm.password}
                        onChange={(event) =>
                          setProviderEditForm((current) => ({
                            ...current,
                            password: event.target.value,
                          }))
                        }
                        placeholder={
                          selectedStorage.provider.kind === "webdav"
                            ? "输入新密码可替换；留空则保留"
                            : "输入新 Secret Key 可替换；留空则保留"
                        }
                      />
                    </label>
                    {selectedStorage.provider.kind === "s3" ? (
                      <>
                        <label>
                          <span>区域</span>
                          <input
                            value={providerEditForm.region}
                            onChange={(event) =>
                              setProviderEditForm((current) => ({
                                ...current,
                                region: event.target.value,
                              }))
                            }
                          />
                        </label>
                        <label>
                          <span>桶名称</span>
                          <input
                            value={providerEditForm.bucket}
                            onChange={(event) =>
                              setProviderEditForm((current) => ({
                                ...current,
                                bucket: event.target.value,
                              }))
                            }
                          />
                        </label>
                      </>
                    ) : null}
                    <label className="provider-inline-check owner-bucket-check">
                      <input
                        type="checkbox"
                        checked={providerEditForm.allowPrivateNetwork}
                        onChange={(event) =>
                          setProviderEditForm((current) => ({
                            ...current,
                            allowPrivateNetwork: event.target.checked,
                          }))
                        }
                      />
                      <span>允许连接局域网或内网地址</span>
                    </label>
                    {selectedStorage.provider.kind === "s3" ? (
                      <label className="provider-inline-check owner-bucket-check">
                        <input
                          type="checkbox"
                          checked={providerEditForm.pathStyle}
                          onChange={(event) =>
                            setProviderEditForm((current) => ({
                              ...current,
                              pathStyle: event.target.checked,
                            }))
                          }
                        />
                        <span>使用 Path Style 访问</span>
                      </label>
                    ) : null}
                    <footer>
                      <button
                        className="primary-button"
                        type="submit"
                        disabled={
                          busyKey ===
                          `provider:${selectedStorage.provider.id}:save`
                        }
                      >
                        {busyKey ===
                        `provider:${selectedStorage.provider.id}:save`
                          ? "保存中…"
                          : "保存连接信息"}
                      </button>
                      <button
                        className="secondary-button"
                        type="button"
                        disabled={
                          busyKey ===
                          `provider:${selectedStorage.provider.id}:test`
                        }
                        onClick={() =>
                          void handleTestProvider(selectedStorage.provider!)
                        }
                      >
                        {busyKey ===
                        `provider:${selectedStorage.provider.id}:test`
                          ? "测试中…"
                          : "测试连接"}
                      </button>
                    </footer>
                    {selectedProviderActionFeedback ? (
                      <p
                        className={`inline-feedback provider-action-feedback is-${selectedProviderActionFeedback.tone}`}
                        role={
                          selectedProviderActionFeedback.tone === "error"
                            ? "alert"
                            : "status"
                        }
                      >
                        {selectedProviderActionFeedback.message}
                      </p>
                    ) : null}
                  </form>
                ) : null}

                <div className="owner-storage-detail-actions">
                  {selectedStorage.bucket ? (
                    <>
                      <button
                        className="secondary-button"
                        type="button"
                        disabled={
                          busyKey === `bucket:${selectedStorage.bucket.id}`
                        }
                        onClick={() =>
                          void saveBucket(selectedStorage.bucket!, {
                            projectAvailable:
                              !selectedStorage.bucket!.projectAvailable,
                          })
                        }
                      >
                        {selectedStorage.bucket.projectAvailable
                          ? "设为不可选"
                          : "允许项目选择"}
                      </button>
                      {selectedStorage.bucket.status !== "active" ? (
                        <button
                          className="secondary-button"
                          type="button"
                          disabled={
                            busyKey === `bucket:${selectedStorage.bucket.id}`
                          }
                          onClick={() =>
                            void saveBucket(selectedStorage.bucket!, {
                              status: "active",
                            })
                          }
                        >
                          重新启用
                        </button>
                      ) : null}
                    </>
                  ) : selectedStorage.grant ? (
                    selectedStorage.grant.status !== "active" ? (
                      <button
                        className="secondary-button"
                        type="button"
                        disabled={
                          busyKey ===
                          `${selectedStorage.grant.storageProviderId}:${
                            selectedStorage.grant.authorizedRootId ?? "provider"
                          }`
                        }
                        onClick={() =>
                          void saveGrant(
                            selectedStorage.grant!.storageProviderId,
                            selectedStorage.grant!.authorizedRootId,
                            "active",
                          )
                        }
                      >
                        重新开放
                      </button>
                    ) : null
                  ) : selectedStorage.provider ? (
                    <button
                      className="secondary-button"
                      type="button"
                      disabled={
                        busyKey === `${selectedStorage.provider.id}:provider`
                      }
                      onClick={() =>
                        void saveGrant(
                          selectedStorage.provider!.id,
                          null,
                          "active",
                        )
                      }
                    >
                      {busyKey === `${selectedStorage.provider.id}:provider`
                        ? "开放中…"
                        : "开放给项目预览/归档"}
                    </button>
                  ) : null}
                  <button
                    className="secondary-button"
                    type="button"
                    aria-expanded={showStorageDanger}
                    onClick={() => setShowStorageDanger((current) => !current)}
                  >
                    {showStorageDanger ? "收起删除与停用" : "删除与停用"}
                  </button>
                </div>

                {showStorageDanger ? (
                  <StorageDeletionPanel
                    location={selectedStorage}
                    impact={storageDeleteImpact}
                    loading={loadingStorageDeleteImpact}
                    error={storageDeleteImpactError}
                    busy={busyKey === `delete:${selectedStorage.key}`}
                    onDisable={() =>
                      void handleDisableSelectedStorage(selectedStorage)
                    }
                    onDelete={() => {
                      void handleDeleteSelectedStorage(
                        selectedStorage,
                        storageDeleteImpact,
                      );
                    }}
                  />
                ) : null}
              </article>
            ) : null}
          </div>
        )}
      </section>

      <section
        className="owner-storage-control owner-bucket-control"
        id="owner-local-storage-form"
      >
        <div className="section-heading">
          <div>
            <p className="eyebrow">ADD STORAGE</p>
            <h3>添加存储位置</h3>
          </div>
          <button
            className="secondary-button"
            type="button"
            onClick={() => {
              setShowStorageWizard((current) => !current);
              setStorageWizardStep(1);
            }}
          >
            {showStorageWizard ? "收起向导" : "添加存储位置"}
          </button>
        </div>

        {!showStorageWizard ? (
          <div className="owner-storage-wizard-intro">
            <strong>用向导添加新的文件保存位置</strong>
            <span>
              选择本机托管、WebDAV 或
              S3，然后决定它作为项目上传、归档或审阅回传位置使用。
            </span>
          </div>
        ) : (
          <form
            className="owner-storage-wizard"
            onSubmit={(event) => {
              event.preventDefault();
              if (storageWizardStep < 5) {
                if (canAdvanceStorageWizard) {
                  if (storageWizardStep === 3 && isLocalStorageWizard) {
                    const resolvedPath = resolveManagedBucketPath(
                      bucketForm.localPath,
                    );
                    setBucketForm((current) => ({
                      ...current,
                      localPath: resolvedPath.path,
                    }));
                    setBucketPathError(null);
                  }
                  setStorageWizardStep((current) => Math.min(5, current + 1));
                }
                return;
              }
              if (storageWizardType === "local") {
                void createBucket();
              } else {
                void createRemoteStorage();
              }
            }}
          >
            <ol className="owner-wizard-steps" aria-label="添加存储位置步骤">
              {["类型", "用途", "位置", "项目", "安全"].map((label, index) => {
                const step = index + 1;
                return (
                  <li
                    className={
                      step === storageWizardStep
                        ? "is-active"
                        : step < storageWizardStep
                          ? "is-done"
                          : ""
                    }
                    key={label}
                  >
                    <span>{step}</span>
                    {label}
                  </li>
                );
              })}
            </ol>

            {storageWizardStep === 1 ? (
              <div className="owner-wizard-choice-grid">
                <button
                  className={storageWizardType === "local" ? "is-selected" : ""}
                  type="button"
                  onClick={() => setStorageWizardType("local")}
                >
                  <strong>本机托管存储位置</strong>
                  <span>源文件保存在运行 Visto 的电脑或服务器目录内。</span>
                </button>
                <button
                  className={
                    storageWizardType === "webdav" ? "is-selected" : ""
                  }
                  type="button"
                  onClick={() => setStorageWizardType("webdav")}
                >
                  <strong>WebDAV</strong>
                  <span>接入 NAS、网盘或支持 WebDAV 的团队存储。</span>
                </button>
                <button
                  className={storageWizardType === "s3" ? "is-selected" : ""}
                  type="button"
                  onClick={() => setStorageWizardType("s3")}
                >
                  <strong>S3 兼容</strong>
                  <span>接入 MinIO、云对象存储或其他 S3 兼容服务。</span>
                </button>
              </div>
            ) : null}

            {storageWizardStep === 2 ? (
              <div className="owner-wizard-choice-grid">
                {(
                  [
                    [
                      "upload",
                      "作为项目上传位置",
                      "项目媒体上传的新文件会优先保存到这里。",
                    ],
                    [
                      "archive",
                      "作为归档位置",
                      "用于后续交付、备份或迁移，不作为默认上传入口。",
                    ],
                    [
                      "review_upload",
                      "作为评论附件位置",
                      "用于审阅回传、评论附件和访客上传附件。",
                    ],
                  ] as Array<[StorageWizardUsage, string, string]>
                ).map(([value, title, description]) => (
                  <button
                    className={
                      storageWizardUsage === value ? "is-selected" : ""
                    }
                    type="button"
                    key={value}
                    onClick={() => setStorageWizardUsage(value)}
                  >
                    <strong>{title}</strong>
                    <span>{description}</span>
                  </button>
                ))}
              </div>
            ) : null}

            {storageWizardStep === 3 ? (
              storageWizardType === "local" ? (
                !hostManagement ? (
                  <div className="owner-storage-wizard-locked">
                    <strong>本机路径只能在运行 Visto 的电脑上添加</strong>
                    <p>
                      你现在通过远程页面访问。为了避免暴露主机目录结构，请回到桌面控制台选择本机托管位置。
                    </p>
                  </div>
                ) : (
                  <div className="owner-storage-form owner-wizard-form-grid">
                    <label>
                      <span>名称</span>
                      <input
                        value={bucketForm.displayName}
                        onChange={(event) =>
                          setBucketForm((current) => ({
                            ...current,
                            displayName: event.target.value,
                          }))
                        }
                        placeholder="例如 本机上传位置"
                      />
                    </label>
                    <label>
                      <span>主机目录</span>
                      <input
                        ref={bucketPathInputRef}
                        value={bucketForm.localPath}
                        aria-invalid={bucketPathError ? "true" : undefined}
                        aria-describedby={
                          bucketPathError
                            ? "owner-bucket-path-error"
                            : undefined
                        }
                        onChange={(event) => {
                          setBucketPathError(null);
                          setBucketForm((current) => ({
                            ...current,
                            localPath: event.target.value,
                          }));
                        }}
                        placeholder="例如 D:\Visto\Uploads"
                      />
                      {bucketPathError ? (
                        <small
                          className="owner-field-error"
                          id="owner-bucket-path-error"
                        >
                          {bucketPathError}
                        </small>
                      ) : null}
                    </label>
                  </div>
                )
              ) : (
                <div className="owner-storage-form owner-wizard-form-grid">
                  <label>
                    <span>连接名称</span>
                    <input
                      value={remoteForm.name}
                      onChange={(event) =>
                        setRemoteForm((current) => ({
                          ...current,
                          name: event.target.value,
                        }))
                      }
                      placeholder={
                        storageWizardType === "webdav"
                          ? "例如 团队 WebDAV"
                          : "例如 团队 S3 存储"
                      }
                    />
                  </label>
                  <label>
                    <span>连接地址</span>
                    <input
                      value={remoteForm.endpoint}
                      onChange={(event) =>
                        setRemoteForm((current) => ({
                          ...current,
                          endpoint: event.target.value,
                        }))
                      }
                      placeholder={
                        storageWizardType === "webdav"
                          ? "https://example.com/dav"
                          : "https://s3.example.com"
                      }
                    />
                  </label>
                  <label>
                    <span>
                      {storageWizardType === "webdav" ? "用户名" : "Access Key"}
                    </span>
                    <input
                      autoComplete="off"
                      value={remoteForm.username}
                      onChange={(event) =>
                        setRemoteForm((current) => ({
                          ...current,
                          username: event.target.value,
                        }))
                      }
                    />
                  </label>
                  <label>
                    <span>
                      {storageWizardType === "webdav" ? "密码" : "Secret Key"}
                    </span>
                    <input
                      type="password"
                      autoComplete="new-password"
                      value={remoteForm.password}
                      onChange={(event) =>
                        setRemoteForm((current) => ({
                          ...current,
                          password: event.target.value,
                        }))
                      }
                    />
                  </label>
                  {storageWizardType === "s3" ? (
                    <>
                      <label>
                        <span>区域</span>
                        <input
                          value={remoteForm.region}
                          onChange={(event) =>
                            setRemoteForm((current) => ({
                              ...current,
                              region: event.target.value,
                            }))
                          }
                        />
                      </label>
                      <label>
                        <span>桶名称</span>
                        <input
                          value={remoteForm.bucket}
                          onChange={(event) =>
                            setRemoteForm((current) => ({
                              ...current,
                              bucket: event.target.value,
                            }))
                          }
                        />
                      </label>
                    </>
                  ) : null}
                  <label>
                    <span>默认路径前缀</span>
                    <input
                      value={remoteForm.basePath}
                      onChange={(event) =>
                        setRemoteForm((current) => ({
                          ...current,
                          basePath: event.target.value,
                        }))
                      }
                      placeholder="可留空"
                    />
                  </label>
                  <label className="provider-inline-check owner-bucket-check">
                    <input
                      type="checkbox"
                      checked={remoteForm.allowPrivateNetwork}
                      onChange={(event) =>
                        setRemoteForm((current) => ({
                          ...current,
                          allowPrivateNetwork: event.target.checked,
                        }))
                      }
                    />
                    <span>允许连接局域网或内网地址</span>
                  </label>
                </div>
              )
            ) : null}

            {storageWizardStep === 4 ? (
              <div className="owner-wizard-choice-grid">
                <button
                  className={
                    storageWizardAvailability === "all" ? "is-selected" : ""
                  }
                  type="button"
                  onClick={() => setStorageWizardAvailability("all")}
                >
                  <strong>开放给项目主管选择</strong>
                  <span>
                    项目主管可以在项目设置中把这个位置选为上传或使用范围。
                  </span>
                </button>
                <button
                  className={
                    storageWizardAvailability === "none" ? "is-selected" : ""
                  }
                  type="button"
                  onClick={() => setStorageWizardAvailability("none")}
                >
                  <strong>暂不开放给项目</strong>
                  <span>先保存连接或位置，稍后再从存储详情里开放给项目。</span>
                </button>
              </div>
            ) : null}

            {storageWizardStep === 5 ? (
              <div className="owner-storage-form owner-wizard-form-grid owner-wizard-security-grid">
                {isLocalStorageWizard ? (
                  <>
                    <label>
                      <span>上传安全策略</span>
                      <select
                        value={bucketForm.uploadSecurityPolicy}
                        onChange={(event) =>
                          setBucketForm((current) => ({
                            ...current,
                            uploadSecurityPolicy: event.target
                              .value as UploadSecurityPolicy,
                          }))
                        }
                      >
                        <option value="quick">快速</option>
                        <option value="standard">标准</option>
                        <option value="enhanced">增强</option>
                      </select>
                      <small className="owner-policy-note">
                        {bucketSecurityPolicyDescription(
                          bucketForm.uploadSecurityPolicy,
                        )}
                      </small>
                    </label>
                    <label>
                      <span>容量上限（GB）</span>
                      <input
                        inputMode="decimal"
                        value={bucketForm.quotaGb}
                        onChange={(event) =>
                          setBucketForm((current) => ({
                            ...current,
                            quotaGb: event.target.value,
                          }))
                        }
                        placeholder="不填则不限"
                      />
                      <small className="owner-policy-note">
                        容量用于本机托管位置的上传保护。
                      </small>
                    </label>
                  </>
                ) : (
                  <div className="owner-remote-security-note">
                    <strong>远程存储暂不执行完整上传扫描</strong>
                    <span>
                      WebDAV/S3 会保留连接、路径、项目授权和审计记录；
                      恶意文件扫描、隔离与容量保护目前只对本机托管上传桶完整生效。
                    </span>
                  </div>
                )}
              </div>
            ) : null}

            <footer className="owner-wizard-actions">
              <button
                className="secondary-button"
                type="button"
                disabled={storageWizardStep === 1}
                onClick={() =>
                  setStorageWizardStep((current) => Math.max(1, current - 1))
                }
              >
                上一步
              </button>
              <button
                className="primary-button"
                type="submit"
                disabled={
                  !canAdvanceStorageWizard ||
                  (storageWizardStep === 5 &&
                    (busyKey === "bucket:create" ||
                      busyKey === "remote:create"))
                }
              >
                {storageWizardStep < 5
                  ? "下一步"
                  : busyKey === "bucket:create" || busyKey === "remote:create"
                    ? "创建中..."
                    : "创建存储位置"}
              </button>
            </footer>
          </form>
        )}
      </section>

      <section className="owner-storage-control owner-upload-check-control">
        <div className="section-heading">
          <div>
            <p className="eyebrow">QUARANTINE</p>
            <h3>隔离与异常</h3>
          </div>
          <div className="owner-section-actions">
            <select
              value={uploadCheckFilter}
              onChange={(event) =>
                setUploadCheckFilter(event.target.value as UploadCheckStatus)
              }
              aria-label="上传检查状态"
            >
              <option value="quarantined">已隔离</option>
              <option value="rejected">已拒绝</option>
              <option value="ready">已放行</option>
            </select>
            <button
              className="secondary-button"
              type="button"
              disabled={loadingUploadChecks}
              onClick={() => void handleRefreshUploadChecks()}
            >
              <RefreshCw size={15} />
              {loadingUploadChecks ? "刷新中…" : "刷新状态"}
            </button>
          </div>
        </div>

        <p className="owner-context-note">隔离文件无法用于审阅、分享或下载。</p>
        <div className="owner-exception-summary">
          <div>
            <span>待处理隔离</span>
            <strong>{quarantineTotal}</strong>
            <small>需要 Owner 决定放行或拒绝</small>
          </div>
          <div>
            <span>当前筛选</span>
            <strong>{uploadCheckTotal}</strong>
            <small>{uploadCheckStatusLabel(uploadCheckFilter)}</small>
          </div>
          <div>
            <span>影响范围</span>
            <strong>{quarantineTotal > 0 ? "会阻塞交付" : "无阻塞"}</strong>
          </div>
        </div>

        {loadingUploadChecks ? (
          <OwnerLoading label="正在读取隔离与异常记录" />
        ) : uploadChecks.length === 0 ? (
          <div className="activity-empty compact">
            <strong>
              {uploadCheckFilter === "quarantined"
                ? "当前没有隔离文件"
                : uploadCheckFilter === "rejected"
                  ? "当前没有已拒绝文件"
                  : "当前没有人工放行记录"}
            </strong>
          </div>
        ) : (
          <div className="owner-upload-check-list">
            {uploadChecks.map((item) => {
              const check = item.uploadCheck;
              const canHandle = check.status === "quarantined";
              return (
                <article className="owner-upload-check-row" key={check.id}>
                  <div className="owner-upload-check-main">
                    <strong>{item.sourceFilename || item.assetName}</strong>
                    <span>
                      {item.projectName ?? "未绑定项目"} ·{" "}
                      {bucketSecurityPolicyLabel(check.uploadSecurityPolicy)} ·{" "}
                      {formatBytes(item.sourceSizeBytes)}
                    </span>
                    <small>
                      {uploadCheckResultLabel(check.resultCode)} ·{" "}
                      {item.uploadedByName ?? "未知上传者"} ·{" "}
                      {formatDateTime(check.updatedAt)}
                    </small>
                    {check.message ? <p>{check.message}</p> : null}
                  </div>
                  <div className="owner-upload-check-impact">
                    <div>
                      <span>原因</span>
                      <strong>
                        {uploadCheckResultLabel(check.resultCode)}
                      </strong>
                    </div>
                    <div>
                      <span>影响</span>
                      <strong>{uploadCheckImpactLabel(check.status)}</strong>
                    </div>
                    <div>
                      <span>下一步</span>
                      <strong>{uploadCheckNextStepLabel(check.status)}</strong>
                    </div>
                  </div>
                  <span
                    className={`owner-state-label is-${check.status}`}
                    title={check.id}
                  >
                    {uploadCheckStatusLabel(check.status)}
                  </span>
                  {canHandle ? (
                    <div className="owner-upload-check-actions">
                      <input
                        value={uploadCheckMessages[check.id] ?? ""}
                        onChange={(event) =>
                          setUploadCheckMessages((current) => ({
                            ...current,
                            [check.id]: event.target.value,
                          }))
                        }
                        placeholder="处理说明，可选"
                      />
                      <button
                        className="secondary-button"
                        type="button"
                        disabled={
                          busyKey === `upload-check:${check.id}:release`
                        }
                        onClick={() =>
                          void handleUploadCheckAction(item, "release")
                        }
                      >
                        {busyKey === `upload-check:${check.id}:release`
                          ? "放行中…"
                          : "人工放行"}
                      </button>
                      <button
                        className="secondary-button danger"
                        type="button"
                        disabled={busyKey === `upload-check:${check.id}:reject`}
                        onClick={() =>
                          void handleUploadCheckAction(item, "reject")
                        }
                      >
                        {busyKey === `upload-check:${check.id}:reject`
                          ? "拒绝中…"
                          : "拒绝"}
                      </button>
                    </div>
                  ) : null}
                </article>
              );
            })}
          </div>
        )}
      </section>
    </div>
  );
}

function OwnerDiagnostics({ system }: { system: SystemInfo }) {
  const [jobs, setJobs] = useState<Job[]>([]);
  const [loading, setLoading] = useState(true);
  const [error, setError] = useState<string | null>(null);

  async function refresh() {
    setLoading(true);
    setError(null);
    try {
      const items = await listJobs();
      setJobs(
        [...items]
          .sort(
            (left, right) =>
              new Date(right.updatedAt).getTime() -
              new Date(left.updatedAt).getTime(),
          )
          .slice(0, 12),
      );
    } catch (loadError) {
      setError(
        loadError instanceof Error ? loadError.message : "无法读取后台任务",
      );
    } finally {
      setLoading(false);
    }
  }

  useEffect(() => {
    void refresh();
  }, []);

  const activeJobs = jobs.filter((item) =>
    ["queued", "leased", "running"].includes(item.status),
  ).length;
  const failedJobs = jobs.filter((item) => item.status === "failed").length;
  const hostManaged = system.access.hostManagement;

  return (
    <div className="owner-diagnostics">
      <div className="owner-fact-strip" aria-label="系统摘要">
        <div>
          <span>运行模式</span>
          <strong>{systemModeLabel(system.mode)}</strong>
        </div>
        <div>
          <span>数据库</span>
          <strong>{system.database.toUpperCase()}</strong>
        </div>
        <div>
          <span>待处理任务</span>
          <strong>{activeJobs}</strong>
        </div>
        <div>
          <span>失败任务</span>
          <strong>{failedJobs}</strong>
        </div>
      </div>

      <section className="owner-runtime-grid">
        <div>
          <Server size={18} />
          <span>服务版本</span>
          <strong>{system.version}</strong>
          <small>API {system.apiVersion}</small>
        </div>
        <div>
          <Database size={18} />
          <span>访问表面</span>
          <strong>
            {system.access.surface === "host" ? "主机控制台" : "远程 Web"}
          </strong>
          <small>
            {system.access.hostManagement
              ? "允许主机级来源管理"
              : "主机级操作已隔离"}
          </small>
        </div>
        <div>
          <Wrench size={18} />
          <span>媒体引擎</span>
          <strong>
            {system.media.ffmpegAvailable && system.media.ffprobeAvailable
              ? "可用"
              : "需要检查"}
          </strong>
          <small>
            FFmpeg {system.media.ffmpegAvailable ? "可用" : "不可用"} · FFprobe{" "}
            {system.media.ffprobeAvailable ? "可用" : "不可用"}
          </small>
          <small>
            视频编码：
            {videoAccelerationLabel(system.media.videoAccelerationMode)} ·{" "}
            {system.media.videoEncoder}
            {system.media.videoFallbackEncoder
              ? ` · 失败回退 ${system.media.videoFallbackEncoder}`
              : ""}
          </small>
        </div>
      </section>

      <section className="owner-maintenance-panel" aria-label="系统维护">
        <div className="section-heading">
          <div>
            <p className="eyebrow">SYSTEM MAINTENANCE</p>
            <h3>系统维护</h3>
            <p>
              这里用于判断当前服务是否健康、是否需要处理失败任务，以及应在哪里完成备份和更新。
            </p>
          </div>
        </div>
        <div className="owner-maintenance-grid">
          <article>
            <span>当前运行版本</span>
            <strong>{system.version}</strong>
            <p>更新前先在部署主机创建备份；更新失败时使用同一备份恢复。</p>
          </article>
          <article>
            <span>部署维护入口</span>
            <strong>{hostManaged ? "本机主机控制台" : "部署主机终端"}</strong>
            <p>
              {hostManaged
                ? "本机可以管理受信任的主机资源。远程访问仍不会获得任意命令权限。"
                : "备份、恢复、更新和诊断包仅能在 Docker 或 Windows Server 主机上执行。"}
            </p>
          </article>
          <article className={failedJobs > 0 ? "is-warning" : "is-ready"}>
            <span>处理建议</span>
            <strong>
              {failedJobs > 0 ? `${failedJobs} 个任务待处理` : "没有待处理失败"}
            </strong>
            <p>
              {failedJobs > 0
                ? "先查看下方失败任务的原因并重试；若持续失败，再从部署主机导出诊断包。"
                : "定期在部署主机创建备份，并在更新前确认服务版本和可用磁盘空间。"}
            </p>
          </article>
        </div>
        <div className="owner-maintenance-boundary">
          <ShieldCheck size={17} />
          <p>
            为保护服务器文件和网络，远程网页不能直接执行备份、恢复、更新或任意系统命令。这些操作请使用部署包随附的维护脚本，并会保留本机审计记录。
          </p>
        </div>
      </section>

      <OwnerUpdatePanel />

      <section className="owner-jobs-section">
        <div className="section-heading">
          <div>
            <p className="eyebrow">BACKGROUND JOBS</p>
            <h3>最近后台任务</h3>
          </div>
          <button
            className="secondary-button"
            type="button"
            disabled={loading}
            onClick={() => void refresh()}
          >
            <RefreshCw size={15} />
            {loading ? "刷新中…" : "刷新"}
          </button>
        </div>
        {error ? (
          <p className="catalog-error" role="alert">
            {error}
          </p>
        ) : loading ? (
          <OwnerLoading label="正在读取后台任务" />
        ) : jobs.length === 0 ? (
          <div className="activity-empty compact">
            <strong>没有后台任务</strong>
          </div>
        ) : (
          <div className="owner-job-list">
            {jobs.map((job) => (
              <article className="owner-job-row" key={job.id}>
                <span className={`owner-job-state is-${job.status}`} />
                <div>
                  <strong>{jobTypeLabel(job.type)}</strong>
                  <span>
                    {jobSubjectLabel(job.subject.type)} ·{" "}
                    {shortIdentifier(job.subject.id)} · 优先级 {job.priority}
                  </span>
                  {job.status === "failed" && job.error ? (
                    <p>{job.error.message}</p>
                  ) : null}
                </div>
                <span className="owner-state-label">
                  {jobStatusLabel(job.status)}
                </span>
                <time dateTime={job.updatedAt}>
                  {formatDateTime(job.updatedAt)}
                </time>
              </article>
            ))}
          </div>
        )}
      </section>
    </div>
  );
}

// the free Server has no web-installed update. This panel only shows
// facts and the commands an administrator runs on the deployment host.
function OwnerUpdatePanel() {
  const [status, setStatus] = useState<SystemUpdateStatus | null>(null);
  const [loading, setLoading] = useState(true);
  const [checking, setChecking] = useState(false);
  const [error, setError] = useState<string | null>(null);
  const [checkedNotice, setCheckedNotice] = useState<string | null>(null);
  const [copyState, setCopyState] = useState<{
    id: string;
    copied: boolean;
  } | null>(null);

  async function load(check: boolean) {
    if (check) {
      setChecking(true);
    } else {
      setLoading(true);
    }
    setError(null);
    setCheckedNotice(null);
    try {
      const next = await getSystemUpdateStatus(check);
      setStatus(next);
      // A deployment without update sources answers with the same text every
      // time, so an explicit check needs its own acknowledgement.
      if (check) {
        setCheckedNotice(
          next.check.checkedAt
            ? `已重新检查，检查时间 ${formatReleaseDate(next.check.checkedAt)}。`
            : "已重新检查，本次没有访问任何公共更新源。",
        );
      }
    } catch (loadError) {
      setError(
        loadError instanceof Error ? loadError.message : "无法读取更新状态",
      );
    } finally {
      setChecking(false);
      setLoading(false);
    }
  }

  useEffect(() => {
    void load(false);
  }, []);

  async function handleCopy(command: SystemUpdateCommand) {
    setCopyState({ id: command.id, copied: await copyText(command.command) });
  }

  const view = updateStatusView(status);

  return (
    <section className="owner-update-panel" aria-label="版本与更新">
      <div className="section-heading">
        <div>
          <p className="eyebrow">UPDATES</p>
          <h3>版本与更新</h3>
          <p>
            这里只显示版本、更新说明和需要在部署主机执行的命令。Visto
            不会从网页下载更新包、安装更新或执行主机命令。
          </p>
        </div>
        <button
          className="secondary-button"
          type="button"
          disabled={loading || checking}
          onClick={() => void load(true)}
        >
          <RefreshCw size={15} />
          {checking ? "检查中…" : "检查更新"}
        </button>
      </div>

      {error ? (
        <p className="catalog-error" role="alert">
          {error}
        </p>
      ) : null}

      {checkedNotice ? (
        <p className="owner-update-notice" role="status">
          {checkedNotice}
        </p>
      ) : null}

      {loading || !status ? (
        <OwnerLoading label="正在读取更新状态" />
      ) : (
        <>
          <div className="owner-update-grid">
            <article>
              <span>当前版本</span>
              <strong>{status.currentVersion}</strong>
              <p>{deploymentLabel(status.deployment)}</p>
            </article>
            <article className={`is-${view.latestTone}`}>
              <span>最新版本</span>
              <strong>{view.latestVersion}</strong>
              <p>{view.latestHint}</p>
            </article>
            <article>
              <span>更新方式</span>
              <strong>
                {status.policy.hostAdminRequired ? "部署主机执行" : "不可用"}
              </strong>
              <p>后台静默更新与网页一键安装都不属于免费 Server。</p>
            </article>
          </div>

          {view.checkSummary ? (
            <p className="owner-update-check">
              {view.checkSummary}
              {status.check.checkedAt
                ? ` 检查时间 ${formatReleaseDate(status.check.checkedAt)}`
                : ""}
            </p>
          ) : null}

          {status.latest ? (
            <div className="owner-update-release">
              <div className="owner-update-release-head">
                <strong>{status.latest.version}</strong>
                <span className={`owner-update-badge is-${view.latestTone}`}>
                  {status.latest.state === "up_to_date"
                    ? "已是最新"
                    : status.latest.state === "update_available"
                      ? "可更新"
                      : "无法判断"}
                </span>
              </div>
              {status.latest.publishedAt ? (
                <small>
                  发布于 {formatReleaseDate(status.latest.publishedAt)}
                </small>
              ) : null}
            </div>
          ) : null}

          {view.commandGroups.map((group) => (
            <div className="owner-update-commands" key={group.platform}>
              <h4>{group.label}</h4>
              {group.commands.map((command) => (
                <article key={command.id}>
                  <div className="owner-update-command-head">
                    <strong>{command.label}</strong>
                    <button
                      className="secondary-button"
                      type="button"
                      onClick={() => void handleCopy(command)}
                    >
                      <Copy size={14} />
                      {copyState?.id === command.id
                        ? copyState.copied
                          ? "已复制"
                          : "复制失败"
                        : "复制"}
                    </button>
                  </div>
                  <p>{command.description}</p>
                  <pre>{command.command}</pre>
                </article>
              ))}
            </div>
          ))}

          <div className="owner-update-offline">
            <h4>离线更新</h4>
            <p>{status.offline.summary}</p>
            <ol>
              {status.offline.steps.map((step) => (
                <li key={step}>{step}</li>
              ))}
            </ol>
          </div>

          <div className="owner-update-notes">
            <ShieldCheck size={17} />
            <ul>
              {status.securityNotes.map((note) => (
                <li key={note}>{note}</li>
              ))}
            </ul>
          </div>
        </>
      )}
    </section>
  );
}

function OwnerLoading({ label }: { label: string }) {
  return (
    <div className="activity-loading" aria-label={label} aria-live="polite">
      <span />
      <span />
      <span />
    </div>
  );
}

function ownerSectionNavLabel(section: OwnerSection, t: Translator) {
  switch (section) {
    case "accounts":
      return t("owner.accounts");
    case "network":
      return t("owner.network");
    case "storage":
      return t("owner.storage");
    case "encoding":
      return t("owner.encoding");
    case "notifications":
      return t("owner.notifications");
    case "activity":
      return t("owner.activity");
    case "diagnostics":
      return t("owner.diagnostics");
  }
}

function ownerSectionNavDescription(section: OwnerSection, t: Translator) {
  switch (section) {
    case "accounts":
      return t("owner.accountsDescription");
    case "network":
      return t("owner.networkDescription");
    case "storage":
      return t("owner.storageDescription");
    case "encoding":
      return t("owner.encodingDescription");
    case "notifications":
      return t("owner.notificationsDescription");
    case "activity":
      return t("owner.activityDescription");
    case "diagnostics":
      return t("owner.diagnosticsDescription");
  }
}

function ownerSectionTitle(section: OwnerSection, t: Translator) {
  switch (section) {
    case "accounts":
      return t("owner.accountsTitle");
    case "network":
      return t("owner.networkTitle");
    case "storage":
      return t("owner.storageTitle");
    case "encoding":
      return t("owner.encodingTitle");
    case "notifications":
      return t("owner.notificationsTitle");
    case "activity":
      return t("owner.activityTitle");
    case "diagnostics":
      return t("owner.diagnosticsTitle");
  }
}

function ownerSectionDescription(section: OwnerSection, t: Translator) {
  switch (section) {
    case "accounts":
      return t("owner.accountsDetail");
    case "network":
      return t("owner.networkDetail");
    case "storage":
      return t("owner.storageDetail");
    case "encoding":
      return t("owner.encodingDetail");
    case "notifications":
      return t("owner.notificationsDetail");
    case "activity":
      return t("owner.activityDetail");
    case "diagnostics":
      return t("owner.diagnosticsDetail");
  }
}

function parseQuotaGb(value: string): number | null | "invalid" {
  const normalized = value.trim();
  if (normalized === "") {
    return null;
  }
  const parsed = Number(normalized);
  if (!Number.isFinite(parsed) || parsed < 0) {
    return "invalid";
  }
  return Math.round(parsed * 1024 * 1024 * 1024);
}

function formatQuotaGbForInput(value: number | null): string {
  if (value == null) {
    return "";
  }
  const gb = value / 1024 / 1024 / 1024;
  return Number.isInteger(gb) ? String(gb) : gb.toFixed(2);
}

function bucketPurposeLabel(purpose: LocalManagedBucketPurpose) {
  switch (purpose) {
    case "upload":
      return "上传落点";
    case "source_archive":
      return "归档保存";
    case "review_upload":
      return "审阅回传";
  }
}

function wizardUsageToBucketPurpose(
  usage: StorageWizardUsage,
): LocalManagedBucketPurpose {
  switch (usage) {
    case "review_upload":
      return "review_upload";
    case "archive":
      return "source_archive";
    case "upload":
      return "upload";
  }
}

function bucketSecurityPolicyLabel(policy: UploadSecurityPolicy) {
  switch (policy) {
    case "quick":
      return "快速策略";
    case "standard":
      return "标准策略";
    case "enhanced":
      return "增强策略";
  }
}

function bucketSecurityPolicyDescription(policy: UploadSecurityPolicy) {
  switch (policy) {
    case "quick":
      return "保留路径、权限、大小和审计等基础防护；耗时恶意软件扫描可记录为未扫描。";
    case "standard":
      return "默认策略；基础检查和媒体探测通过后进入处理，扫描结果后续影响分享与下载。";
    case "enhanced":
      return "面向公网或访客上传；扫描和检查通过前不应进入可分享、可下载或可审阅状态。";
  }
}

function bucketStatusLabel(status: LocalManagedBucket["status"]) {
  switch (status) {
    case "active":
      return "可用";
    case "disabled":
      return "已停用";
    case "error":
      return "异常";
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
      return "已放行";
    case "quarantined":
      return "已隔离";
    case "rejected":
      return "已拒绝";
    default:
      return status;
  }
}

function uploadCheckResultLabel(value: string | null) {
  switch (value) {
    case "type_checks_passed":
      return "类型检查通过";
    case "malware_not_scanned":
      return "未执行恶意软件扫描";
    case "malware_clean":
      return "扫描通过";
    case "malware_scan_unavailable":
      return "扫描器不可用";
    case "malware_scan_failed":
      return "扫描失败";
    case "malware_detected":
      return "发现威胁";
    case "manual_released":
      return "Owner 人工放行";
    case "manual_rejected":
      return "Owner 人工拒绝";
    default:
      return value ?? "暂无结果";
  }
}

function uploadCheckImpactLabel(status: string) {
  switch (status) {
    case "quarantined":
      return "阻止审阅、分享和下载";
    case "rejected":
      return "继续阻止交付";
    case "ready":
      return "已可进入后续处理";
    case "checking":
      return "等待检查完成";
    case "processing":
      return "等待媒体处理";
    default:
      return "需要查看记录";
  }
}

function uploadCheckNextStepLabel(status: string) {
  switch (status) {
    case "quarantined":
      return "填写备注后放行或拒绝";
    case "rejected":
      return "需要时由 Owner 重新放行";
    case "ready":
      return "无需处理";
    case "checking":
    case "processing":
      return "稍后刷新状态";
    default:
      return "确认原因";
  }
}

function formatBytes(value: number) {
  if (!Number.isFinite(value) || value <= 0) {
    return "0 B";
  }
  const units = ["B", "KB", "MB", "GB", "TB"];
  let size = value;
  let unitIndex = 0;
  while (size >= 1024 && unitIndex < units.length - 1) {
    size /= 1024;
    unitIndex += 1;
  }
  return `${size >= 10 || unitIndex === 0 ? size.toFixed(0) : size.toFixed(1)} ${
    units[unitIndex]
  }`;
}

function storageProviderDisplayName(name: string) {
  if (
    name === "Review Studio Managed Uploads" ||
    name.toLowerCase().includes("managed asset")
  ) {
    return "系统版本文件";
  }
  return name;
}

function storageRootDisplayName(name: string) {
  if (
    name === "Core managed storage" ||
    name.toLowerCase().includes("managed storage") ||
    name.toLowerCase().includes("managed asset")
  ) {
    return "系统内部位置";
  }
  return name;
}

function storageKindLabel(kind: string) {
  switch (kind) {
    case "local":
      return "本地存储";
    case "webdav":
      return "WebDAV";
    case "s3":
      return "S3";
    default:
      return kind;
  }
}

function storageStatusLabel(status: string) {
  switch (status) {
    case "active":
      return "连接正常";
    case "offline":
      return "离线";
    case "error":
      return "连接异常";
    case "disabled":
      return "已停用";
    default:
      return status;
  }
}

function providerEffectiveStatus(provider: StorageProvider) {
  if (provider.status === "active" && provider.lastTestStatus === "failed") {
    return "error";
  }
  return provider.status;
}

function isRemoteStorageProvider(
  provider: StorageProvider | null | undefined,
): provider is StorageProvider {
  return provider?.kind === "webdav" || provider?.kind === "s3";
}

function canGrantUploadToRemote(
  grant: ProjectStorageGrant,
  provider: StorageProvider | undefined,
) {
  return (
    grant.status === "active" &&
    grant.authorizedRootId !== null &&
    grant.rootStatus === "available" &&
    provider !== undefined &&
    isRemoteStorageProvider(provider) &&
    providerEffectiveStatus(provider) === "active"
  );
}

function providerNeedsAttention(provider: StorageProvider) {
  return ["error", "offline"].includes(providerEffectiveStatus(provider));
}

function storageStatusTone(status: string) {
  switch (status) {
    case "active":
    case "ready":
      return "success";
    case "error":
    case "offline":
    case "rejected":
      return "danger";
    case "disabled":
    case "quarantined":
      return "warning";
    default:
      return "muted";
  }
}

function systemModeLabel(mode: string) {
  switch (mode) {
    case "local":
      return "本地";
    case "desktop":
      return "桌面";
    case "docker":
      return "容器";
    case "cloud":
      return "云端";
    default:
      return mode;
  }
}

function videoAccelerationLabel(mode: string) {
  switch (mode) {
    case "software":
      return "软件编码";
    case "nvenc":
      return "NVIDIA NVENC";
    case "qsv":
      return "Intel Quick Sync";
    case "amf":
      return "AMD AMF";
    case "videotoolbox":
      return "Apple VideoToolbox";
    default:
      return mode || "软件编码";
  }
}

function ownerRemoteGrantDescription(
  grant: ProjectStorageGrant,
  provider: StorageProvider | undefined,
) {
  const uploadReady = provider
    ? canGrantUploadToRemote(grant, provider)
    : false;
  if (uploadReady) {
    return "已开放给项目，可用于上传、预览或归档。";
  }
  return "可用于预览或归档；完成配置后可作为上传位置。";
}

function RemoteTrafficPolicyCard({
  provider,
  grant,
}: {
  provider: StorageProvider;
  grant: ProjectStorageGrant | undefined;
}) {
  const uploadReady = grant ? canGrantUploadToRemote(grant, provider) : false;
  const storageLabel = storageKindLabel(provider.kind);
  return (
    <div className="remote-traffic-card">
      <div>
        <span>上传流量</span>
        <strong>
          {uploadReady ? "经 Visto 主机转发" : "开放后经主机转发"}
        </strong>
        <small>上传到 {storageLabel} 会占用 Visto 主机带宽。</small>
      </div>
    </div>
  );
}

function StorageDeletionPanel({
  location,
  impact,
  loading,
  error,
  busy,
  onDisable,
  onDelete,
}: {
  location: OwnerStorageLocation;
  impact: StorageDeleteImpact | null;
  loading: boolean;
  error: string | null;
  busy: boolean;
  onDisable: () => void;
  onDelete: () => void;
}) {
  const canShowDisable =
    location.bucket?.status === "active" || location.grant?.status === "active";
  const impactItems = impact ? storageImpactCountItems(impact) : [];
  return (
    <section className="owner-storage-danger-panel">
      <header>
        <div>
          <p className="eyebrow">DELETE SAFETY</p>
          <h4>删除与停用</h4>
        </div>
        {impact?.canDelete ? (
          <span className="owner-state-label is-active">可删除</span>
        ) : (
          <span className="owner-state-label is-disabled">需先处理引用</span>
        )}
      </header>
      <p>
        {loading
          ? "正在检查项目、媒体、审阅和任务引用…"
          : impact?.canDelete
            ? "没有发现会断链的引用，可以只删除 Visto 元数据；真实文件不会被物理删除。"
            : "这个存储位置仍有引用。建议先停用，让它从项目可选范围移除；需要彻底删除时先迁移或清理引用。"}
      </p>
      {error ? (
        <p className="inline-feedback is-error" role="alert">
          {error}
        </p>
      ) : null}
      {impact ? (
        <>
          <div className="owner-storage-impact-grid">
            {impactItems.map((item) => (
              <div key={item.key}>
                <span>{item.label}</span>
                <strong>{item.value}</strong>
              </div>
            ))}
          </div>
          {impact.blockingReasons.length > 0 ? (
            <div className="owner-storage-blocking-list">
              <strong>删除前需要处理</strong>
              {impact.blockingReasons.map((reason) => (
                <span key={reason}>{reason}</span>
              ))}
            </div>
          ) : null}
        </>
      ) : null}
      <footer>
        {canShowDisable ? (
          <button
            className="secondary-button"
            type="button"
            onClick={onDisable}
            disabled={busy}
          >
            停用存储位置
          </button>
        ) : null}
        <button
          className="secondary-button danger"
          type="button"
          onClick={onDelete}
          disabled={busy || loading || !impact?.canDelete}
        >
          {busy ? "删除中…" : "删除空存储"}
        </button>
      </footer>
    </section>
  );
}

function storageImpactCountItems(impact: StorageDeleteImpact) {
  return [
    {
      key: "roots",
      label:
        impact.targetType === "provider" ? "授权目录/位置" : "当前授权目录",
      value: impact.counts.authorizedRoots,
    },
    {
      key: "grants",
      label: "项目授权",
      value: impact.counts.projectGrants,
    },
    {
      key: "selections",
      label: "项目选择",
      value: impact.counts.projectSelections,
    },
    {
      key: "objects",
      label: "文件对象",
      value: impact.counts.storageObjects,
    },
    {
      key: "versions",
      label: "资产版本",
      value: impact.counts.assetVersions,
    },
    {
      key: "renditions",
      label: "预览文件",
      value: impact.counts.renditions,
    },
    {
      key: "reviews",
      label: "未关闭审阅",
      value: impact.counts.reviewSessions,
    },
    {
      key: "jobs",
      label: "处理中任务",
      value: impact.counts.pendingJobs,
    },
    {
      key: "copy",
      label: "复制任务",
      value: impact.counts.storageCopyTasks,
    },
    {
      key: "checks",
      label: "上传检查",
      value: impact.counts.uploadChecks,
    },
  ];
}

function jobStatusLabel(status: Job["status"]) {
  switch (status) {
    case "queued":
      return "排队中";
    case "leased":
    case "running":
      return "处理中";
    case "succeeded":
      return "已完成";
    case "failed":
      return "失败";
    case "cancel_requested":
      return "正在取消";
    case "cancelled":
      return "已取消";
  }
}

function jobTypeLabel(type: string) {
  const labels: Record<string, string> = {
    directory_scan: "目录扫描",
    media_probe: "媒体探测",
    rendition_generate: "预览生成",
    storage_copy: "存储复制",
    "media.process_asset_version": "处理媒体版本",
    "media.generate_video_renditions": "生成视频预览",
    "media.generate_video_enhancements": "生成视频增强",
    "media.probe_root": "媒体探测",
  };
  return labels[type] ?? type.replaceAll("_", " ");
}

function jobSubjectLabel(type: string) {
  const labels: Record<string, string> = {
    storageObject: "媒体对象",
    authorizedRoot: "存储位置",
    asset: "媒体",
    assetVersion: "媒体版本",
  };
  return labels[type] ?? type;
}

function formatDateTime(value: string) {
  return new Intl.DateTimeFormat("zh-CN", {
    month: "2-digit",
    day: "2-digit",
    hour: "2-digit",
    minute: "2-digit",
  }).format(new Date(value));
}

function shortIdentifier(value: string) {
  return value.length > 12 ? `${value.slice(0, 8)}…` : value;
}

function isAbort(error: unknown) {
  return error instanceof DOMException && error.name === "AbortError";
}
