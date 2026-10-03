export interface ApiErrorResponse {
  error: {
    code: string;
    message: string;
    requestId?: string;
    details?: Record<string, unknown>;
  };
}

export interface SystemInfo {
  name: string;
  version: string;
  apiVersion: string;
  mode: "local" | "desktop" | "docker" | "cloud" | string;
  database: "sqlite" | "postgres" | string;
  media: {
    ffmpegAvailable: boolean;
    ffprobeAvailable: boolean;
    videoAccelerationMode: string;
    videoEncoder: string;
    videoFallbackEncoder: string;
    videoHardwareAcceleration: boolean;
  };
  access: {
    surface: "host" | "remote" | string;
    hostManagement: boolean;
    remoteWorkspace: boolean;
  };
}

/**
 * the Owner update surface is read-only. The free Server never
 * downloads, installs, or executes an update from the web app.
 */
export interface SystemUpdateStatus {
  channel: string;
  currentVersion: string;
  deployment:
    | "docker"
    | "windows-server"
    | "linux-server"
    | "macos-server"
    | "source"
    | string;
  checked: boolean;
  policy: {
    webInstallSupported: boolean;
    silentUpdate: boolean;
    hostAdminRequired: boolean;
    downloadsPackage: boolean;
    executesHostCommands: boolean;
  };
  sources: string[];
  check: {
    status:
      | "unchecked"
      | "not_configured"
      | "unavailable"
      | "available"
      | string;
    checkedAt: string | null;
    message: string;
  };
  latest: SystemUpdateRelease | null;
  commands: SystemUpdateCommand[];
  offline: {
    summary: string;
    steps: string[];
  };
  securityNotes: string[];
}

/**
 * The free tier version announcement. It carries no artifact list and no
 * download location: the channel is unsigned, so a location taken from it could
 * be rewritten by anyone who can rewrite the source.
 */
export interface SystemUpdateRelease {
  version: string;
  publishedAt: string;
  source: string;
  /**
   * Version guard verdict. Deliberately a three-way state rather than a
   * boolean, so "cannot compare" is never rendered as "已是最新".
   */
  state: "update_available" | "up_to_date" | "unknown";
}

export interface SystemUpdateCommand {
  id: string;
  label: string;
  platform: string;
  command: string;
  description: string;
}

export interface SetupStatus {
  setupRequired: boolean;
  hostClaimRequired: boolean;
}

export interface SetupInput {
  workspaceName: string;
  ownerName: string;
  ownerEmail?: string;
  password: string;
  locale: string;
  timezone: string;
  /**
   * The first-run answer to "may an Owner add host directories from the web?".
   * Optional: an omitted field keeps the documented default (enabled), so it is
   * never read as a refusal. The answer is recorded once and cannot be changed
   * from the web afterwards — see SystemHostAccess.
   */
  allowWebHostPaths?: boolean;
}

export interface SessionInfo {
  user: {
    id: string;
    email: string | null;
    displayName: string;
    locale: string;
  };
  workspace: {
    id: string;
    name: string;
    teamName: string;
    timezone: string;
  };
  role: "owner" | "admin" | "member" | "guest" | string;
  expiresAt: string;
}

export interface UpdateProfileInput {
  displayName: string;
  locale?: string;
}

export interface Membership {
  id: string;
  userId: string;
  email: string | null;
  displayName: string;
  role: "owner" | "admin" | "member" | "guest";
  status: "active" | "disabled";
  revision: number;
  joinedAt: string | null;
  createdAt: string;
  updatedAt: string;
  disabledAt: string | null;
}

export interface Invitation {
  id: string;
  email: string;
  role: "admin" | "member" | "guest";
  tokenPrefix: string;
  status: "pending" | "accepted" | "revoked" | "expired";
  expiresAt: string;
  sendCount: number;
  lastSentAt: string;
  acceptedAt: string | null;
  revokedAt: string | null;
  createdAt: string;
  updatedAt: string;
}

export interface InvitationSecret {
  invitation: Invitation;
  url: string;
}

export interface InvitationPreview {
  workspaceName: string;
  email: string;
  role: Invitation["role"];
  expiresAt: string;
}

export type ProjectStatus = "active" | "archived";

export interface Project {
  id: string;
  workspaceId: string;
  primaryOwnerUserId: string | null;
  name: string;
  description: string | null;
  status: ProjectStatus;
  cover: null;
  assetCount: number;
  collectionCount: number;
  revision: number;
  createdAt: string;
  updatedAt: string;
  archivedAt: string | null;
}

export interface ProjectOverview {
  projectId: string;
  generatedAt: string;
  reviews: ProjectOverviewList<ProjectOverviewReview>;
  openFeedback: ProjectOverviewList<ProjectOverviewFeedback>;
  recentVersions: ProjectOverviewList<ProjectOverviewVersion>;
  failedTasks: ProjectOverviewList<ProjectOverviewFailedTask>;
}

export interface ProjectOverviewList<T> {
  total: number;
  items: T[];
}

export interface ProjectOverviewReview {
  id: string;
  name: string;
  status: ReviewSessionStatus;
  dueAt: string | null;
  responsibleName: string;
  assetName: string;
  versionNumber: number;
  itemCount: number;
  openFeedbackCount: number;
  updatedAt: string;
}

export interface ProjectOverviewFeedback {
  id: string;
  reviewId: string;
  reviewName: string;
  assetId: string;
  assetName: string;
  assetVersionId: string;
  versionNumber: number;
  authorName: string;
  body: string;
  annotationKind: string;
  timeStartUs: number | null;
  timeEndUs: number | null;
  updatedAt: string;
}

export interface ProjectOverviewVersion {
  id: string;
  assetId: string;
  assetName: string;
  versionNumber: number;
  sourceFilename: string;
  processingStatus: AssetVersion["processingStatus"];
  actorName: string;
  createdAt: string;
}

export interface ProjectOverviewFailedTask {
  id: string;
  type: string;
  assetId: string;
  assetName: string;
  errorCode: string;
  errorMessage: string;
  updatedAt: string;
}

export type ProjectMemberRole =
  | "primary_owner"
  | "supervisor"
  | "member"
  | "guest";

export type ProjectMemberStatus = "active" | "disabled" | "expired" | "removed";

export interface ProjectMember {
  id: string;
  workspaceId: string;
  projectId: string;
  userId: string;
  email: string | null;
  displayName: string;
  roleKey: ProjectMemberRole;
  status: ProjectMemberStatus;
  permissions: Record<string, boolean>;
  expiresAt: string | null;
  joinedAt: string | null;
  removedAt: string | null;
  revision: number;
  createdAt: string;
  updatedAt: string;
}

export interface ProjectMemberInput {
  userId: string;
  roleKey: Exclude<ProjectMemberRole, "primary_owner">;
  status?: Exclude<ProjectMemberStatus, "removed">;
  permissions: Record<string, boolean>;
  expiresAt: string | null;
  revision?: number;
}

export interface ProjectGuestInput {
  email?: string | null;
  displayName: string;
  locale?: string;
  permissions: Record<string, boolean>;
  expiresAt: string | null;
}

export interface ProjectTransferInput {
  newPrimaryUserId: string;
  formerOwnerAction: "keep_supervisor" | "demote_member" | "leave";
}

export interface ProjectAsset {
  id: string;
  workspaceId: string;
  projectId: string;
  projectName: string;
  assetId: string;
  assetName: string;
  assetType: string;
  status: "active" | "trashed";
  addedBy: string;
  trashedBy: string | null;
  trashedAt: string | null;
  trashExpiresAt: string | null;
  createdAt: string;
  updatedAt: string;
}

export interface ProjectAssetInput {
  assetId: string;
}

export interface MoveProjectAssetInput {
  targetProjectId: string;
}

export interface ProjectStorageGrant {
  id: string;
  workspaceId: string;
  storageProviderId: string;
  authorizedRootId: string | null;
  providerName: string;
  providerKind: "local" | "webdav" | "s3" | string;
  providerStatus: string;
  rootName: string | null;
  rootStatus: string | null;
  localManagedBucketId: string | null;
  bucketPurpose: "upload" | "source_archive" | "review_upload" | null;
  status: "active" | "disabled";
  createdAt: string;
  updatedAt: string;
}

export interface ProjectStorageGrantInput {
  storageProviderId: string;
  authorizedRootId?: string | null;
  status: "active" | "disabled";
}

export interface ProjectStorageSelection {
  id: string;
  workspaceId: string;
  projectId: string;
  grantId: string;
  purpose: "upload" | "default_rendition" | "archive";
  selectedBy: string;
  createdAt: string;
  updatedAt: string;
  grant: ProjectStorageGrant;
}

export interface ProjectStorageSelectionInput {
  grantId: string;
}

export interface ProjectUploadTarget {
  projectId: string;
  uploadTarget: "local_managed_bucket" | "remote_storage";
  uploadSecurityPolicy: "quick" | "standard" | "enhanced" | string;
  storageProviderId: string;
  authorizedRootId: string;
  localManagedBucketId: string | null;
  quotaBytes: number | null;
  usedBytesEstimate: number;
  quotaAvailableBytes: number | null;
  maxSingleFileBytes: number;
}

export interface WorkspaceRegistrationSettings {
  workspaceId: string;
  workspaceName: string;
  teamName: string;
  registrationEnabled: boolean;
  emailVerificationRequired: boolean;
  defaultWorkspaceRole: "member";
  revision: number;
  updatedBy: string | null;
  updatedAt: string;
}

export interface WorkspaceRegistrationSettingsInput {
  teamName: string;
  registrationEnabled: boolean;
  emailVerificationRequired: boolean;
  revision: number;
}

/**
 * Deployment-wide network access policy.
 *
 * `requireRemoteHTTPS` defaults to `false` so a fresh self-hosted install does
 * not block LAN HTTP before TLS is configured. Off means "HTTP or HTTPS is
 * accepted", never "HTTPS is disabled". Loopback always accepts plaintext.
 */
export interface SystemNetworkSettings {
  /** The stored Owner decision. */
  requireRemoteHTTPS: boolean;
  /** What Core enforces right now, including the deployment override. */
  effectiveRequireRemoteHTTPS: boolean;
  /** `REVIEW_STUDIO_REQUIRE_HTTPS=1` pins enforcement on; the toggle is disabled. */
  environmentForced: boolean;
  /** False when the current page is on remote plaintext HTTP, which cannot enable enforcement. */
  currentRequestSecure: boolean;
  revision: number;
  updatedBy: string | null;
  updatedAt: string;
}

export interface SystemNetworkSettingsInput {
  requireRemoteHTTPS: boolean;
  revision: number;
}

/**
 * The host access switch, read-only from the web on purpose. It decides whether
 * an Owner web session may reach the host management surface — which includes
 * adding any local directory as a storage location. A setting that grants
 * host-level reach must not be flippable by the session that benefits from it,
 * so there is no update counterpart and no PUT/PATCH route. It can be changed in
 * exactly two places: the first-run wizard, which requires the host management
 * token, and the deployment configuration (`VISTO_ALLOW_WEB_HOST_PATHS`).
 */
export interface SystemHostAccess {
  /** What the first-run wizard recorded. */
  allowWebHostPaths: boolean;
  /** What the host management guard consults right now, override included. */
  effectiveAllowWebHostPaths: boolean;
  /** `VISTO_ALLOW_WEB_HOST_PATHS` pins the value; the wizard's answer is inert. */
  environmentForced: boolean;
  revision: number;
  updatedBy: string | null;
  updatedAt: string;
}

/**
 * H.264 encoder selection for the managed media runtime, Owner-editable. Core
 * probes which encoders this host can actually run — a compiled-in encoder is not
 * an available one — and the Owner may override the choice or return to automatic.
 *
 * `preferredEncoder` is `""` for "follow the probe". `effectiveEncoder` is what
 * the next job will use, resolved server-side so the page never re-derives the
 * order: the breaker's current choice, then the Owner's, then Core's configuration.
 */
export interface SystemMediaEncodingSettings {
  /** The stored Owner decision; `""` means automatic. */
  preferredEncoder: string;
  /** What the next job will actually use. */
  effectiveEncoder: string;
  /** What the breaker moved the instance to; `""` when it has no opinion. */
  activeEncoder: string;
  /** The last probe sweep, most preferred first. */
  detectedEncoders: string[];
  detectedAt: string | null;
  /** Consecutive failures of the current encoder; resets on any success. */
  failureCount: number;
  /** Set while the breaker has moved the instance off an encoder. */
  trippedEncoder: string | null;
  trippedReason: string | null;
  trippedAt: string | null;
  revision: number;
  updatedBy: string | null;
  updatedAt: string;
}

export interface SystemMediaEncodingSettingsInput {
  /** `""` returns to automatic selection. */
  preferredEncoder: string;
  revision: number;
}

/**
 * What a manual re-probe answers with. The sweep itself runs in the background,
 * so this only confirms it started; the outcome is read back from
 * SystemMediaEncodingSettings.detectedEncoders / detectedAt.
 */
export interface SystemMediaEncodingReprobeResult {
  started: boolean;
}

export interface PublicRegistrationInfo {
  workspaceName: string;
  teamName: string;
  registrationEnabled: boolean;
  emailVerificationRequired: boolean;
  defaultWorkspaceRole: "member";
}

export interface PublicRegistrationInput {
  email: string;
  displayName: string;
  password: string;
  locale?: string;
}

export interface Collection {
  id: string;
  workspaceId: string;
  projectId: string;
  name: string;
  description: string | null;
  kind: "delivery" | "playlist" | "portfolio_section";
  position: number;
  itemCount: number;
  revision: number;
  createdAt: string;
  updatedAt: string;
}

export interface CollectionItem {
  id: string;
  collectionId: string;
  assetId: string;
  assetName: string;
  assetType: string;
  pinnedVersionId: string | null;
  position: number;
  caption: string | null;
  createdAt: string;
}

export interface ItemList<T> {
  items: T[];
}

export interface CreateProjectInput {
  name: string;
  description: string | null;
  storageGrantId: string;
}

export interface UpdateProjectInput {
  name: string;
  description: string | null;
  revision: number;
}

export interface CreateCollectionInput {
  name: string;
  description: string | null;
}

export interface UpdateCollectionInput extends CreateCollectionInput {
  revision: number;
}

export interface ScanSummary {
  discovered: number;
  new: number;
  modified: number;
  moved: number;
  missing: number;
  recovered: number;
  unchanged: number;
}

export interface AuthorizedRoot {
  id: string;
  workspaceId: string;
  storageProviderId: string;
  displayName: string;
  displayPath: string;
  mode: "referenced" | "managed";
  scanEnabled: boolean;
  status: "available" | "unavailable" | string;
  revision: number;
  createdAt: string;
  updatedAt: string;
  lastScanAt: string | null;
  lastScanStatus: "running" | "succeeded" | "failed" | null;
  lastScanSummary: ScanSummary | null;
}

export type LocalManagedBucketPurpose =
  | "upload"
  | "source_archive"
  | "review_upload";

export type UploadSecurityPolicy = "quick" | "standard" | "enhanced";

export interface LocalManagedBucket {
  id: string;
  workspaceId: string;
  authorizedRootId: string;
  storageProviderId: string;
  displayName: string;
  displayPath: string;
  purpose: LocalManagedBucketPurpose;
  quotaBytes: number | null;
  usedBytesEstimate: number | null;
  uploadSecurityPolicy: UploadSecurityPolicy;
  projectAvailable: boolean;
  status: "active" | "disabled" | "error";
  createdBy: string | null;
  revision: number;
  createdAt: string;
  updatedAt: string;
}

export interface StorageDeleteImpact {
  targetType: "provider" | "local_managed_bucket" | string;
  providerId: string;
  providerName: string;
  providerKind: string;
  authorizedRootId: string | null;
  bucketId: string | null;
  bucketName: string | null;
  canDelete: boolean;
  canDisable: boolean;
  counts: {
    authorizedRoots: number;
    projectGrants: number;
    projectSelections: number;
    storageObjects: number;
    assetVersions: number;
    renditions: number;
    reviewSessions: number;
    pendingJobs: number;
    storageCopyTasks: number;
    uploadChecks: number;
  };
  blockingReasons: string[];
}

export interface CreateLocalManagedBucketInput {
  displayName: string;
  localPath: string;
  purpose: LocalManagedBucketPurpose;
  quotaBytes?: number | null;
  uploadSecurityPolicy: UploadSecurityPolicy;
  projectAvailable: boolean;
}

export interface UpdateLocalManagedBucketInput {
  displayName: string;
  purpose: LocalManagedBucketPurpose;
  quotaBytes?: number | null;
  uploadSecurityPolicy: UploadSecurityPolicy;
  projectAvailable: boolean;
  status: "active" | "disabled" | "error";
  revision: number;
}

export type StorageProviderKind = "webdav" | "s3";

export interface StorageProvider {
  id: string;
  workspaceId: string;
  kind: StorageProviderKind | string;
  name: string;
  status: "offline" | "active" | "error" | "disabled" | string;
  endpoint: string;
  basePath: string;
  region?: string;
  bucket?: string;
  pathStyle: boolean;
  allowPrivateNetwork: boolean;
  capabilities: Record<string, boolean>;
  revision: number;
  createdAt: string;
  updatedAt: string;
  lastTestAt: string | null;
  lastTestStatus: "succeeded" | "failed" | null;
  lastErrorCode: string | null;
  lastErrorMessage: string | null;
}

export interface StorageProviderInput {
  kind: StorageProviderKind;
  name: string;
  endpoint: string;
  basePath: string;
  region?: string;
  bucket?: string;
  pathStyle?: boolean;
  allowPrivateNetwork: boolean;
  username?: string;
  password?: string;
  accessKeyId?: string;
  secretAccessKey?: string;
}

export interface StorageConnectionReport {
  providerId: string;
  status: "succeeded" | "failed" | string;
  capabilities: Record<string, boolean>;
  warnings: string[];
  latencyMs: number;
  testedAt: string;
}

export interface RegisterProviderRootInput {
  displayName: string;
  basePath: string;
  mode: "referenced" | "managed";
  scanEnabled: boolean;
}

export interface StorageCopyTask {
  id: string;
  workspaceId: string;
  sourceStorageObjectId: string;
  sourceRootId: string;
  sourceObjectKey: string;
  targetRootId: string;
  targetObjectKey: string;
  targetStorageObjectId: string | null;
  jobId: string | null;
  status: "queued" | "running" | "succeeded" | "failed" | "cancelled" | string;
  totalBytes: number;
  copiedBytes: number;
  contentHash: string | null;
  contentHashAlgorithm: string | null;
  errorCode: string | null;
  errorMessage: string | null;
  revision: number;
  createdAt: string;
  updatedAt: string;
  completedAt: string | null;
}

export interface CreateStorageCopyTaskInput {
  sourceStorageObjectId?: string;
  sourceAssetId?: string;
  sourceAssetVersionId?: string;
  targetRootId: string;
  targetObjectKey: string;
}

export interface StorageCopyTaskEnvelope {
  task: StorageCopyTask;
  job: Job;
}

export interface StorageObject {
  id: string;
  authorizedRootId: string;
  objectKey: string;
  status: "available" | "missing";
  sizeBytes: number;
  modifiedAt: string;
  mimeType: string;
  firstDiscoveredAt: string;
  lastSeenAt: string | null;
  missingSince: string | null;
}

export interface DirectoryScan {
  id: string;
  rootId: string;
  status: "succeeded" | "failed" | string;
  summary: ScanSummary;
  startedAt: string;
  completedAt: string;
}

export interface RegisterAuthorizedRootInput {
  displayName: string;
  localPath: string;
  mode: "referenced" | "managed";
  scanEnabled: boolean;
}

export interface UpdateAuthorizedRootInput {
  displayName: string;
  scanEnabled: boolean;
  revision: number;
}

export interface MediaProbe {
  storageObjectId: string;
  status: "succeeded" | "failed";
  mediaType:
    | "video"
    | "image"
    | "audio"
    | "pdf"
    | "design"
    | "document"
    | "other";
  formatName: string | null;
  formatLongName: string | null;
  durationUs: number | null;
  bitRate: number | null;
  width: number | null;
  height: number | null;
  rotationDegrees: number | null;
  frameRate: number | null;
  videoCodec: string | null;
  audioCodec: string | null;
  errorCode: string | null;
  errorMessage: string | null;
  probedAt: string;
}

export interface MediaProbeBatch {
  rootId: string;
  available: number;
  attempted: number;
  succeeded: number;
  failed: number;
  skipped: number;
  remaining: number;
  items: MediaProbe[];
}

export interface Rendition {
  id: string;
  sourceStorageObjectId: string;
  kind:
    | "thumbnail"
    | "screen_preview"
    | "poster"
    | "proxy"
    | "hls"
    | "storyboard";
  profileKey: string;
  profileVersion: number;
  sourceFingerprint: string;
  status: "processing" | "ready" | "failed" | "stale";
  mimeType: string | null;
  width: number | null;
  height: number | null;
  durationUs: number | null;
  sizeBytes: number | null;
  metadataJson: string;
  errorCode: string | null;
  errorMessage: string | null;
  contentUrl: string | null;
  updatedAt: string;
  completedAt: string | null;
}

export type MediaLibraryState =
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

export interface MediaLibraryItem {
  object: StorageObject;
  asset: MediaLibraryAsset | null;
  probe: MediaProbe | null;
  renditions: Rendition[];
  jobs: Job[];
  uploadCheck: UploadCheck | null;
}

export interface MediaLibraryAsset {
  id: string;
  projectId: string | null;
  projectName: string | null;
  name: string;
  type: MediaProbe["mediaType"];
  revision: number;
  versionId: string;
  versionNumber: number;
}

export interface UpdateMediaLibraryAssetInput {
  name: string;
  revision: number;
}

export interface AssetVersion {
  id: string;
  assetId: string;
  versionNumber: number;
  label: string | null;
  note: string | null;
  processingStatus: "pending" | "processing" | "ready" | "failed" | "partial";
  processingStage:
    | "ingested"
    | "processing"
    | "preview_ready"
    | "optimizing"
    | "complete"
    | "enhancement_failed"
    | "failed";
  sourceFilename: string;
  sourceMime: string | null;
  sourceSizeBytes: number;
  sourceFingerprint: string;
  storageObjectId: string;
  authorizedRootId: string;
  storageObjectKey: string;
  storageObjectStatus: "available" | "missing" | string;
  createdAt: string;
  isCurrent: boolean;
  probe: MediaProbe | null;
  renditions: Rendition[];
  uploadCheck: UploadCheck | null;
}

export type UploadCheckStatus =
  | "uploaded"
  | "checking"
  | "processing"
  | "ready"
  | "quarantined"
  | "rejected";

export interface UploadCheck {
  id: string;
  projectId: string | null;
  assetId: string;
  assetVersionId: string;
  storageObjectId: string;
  sourceType: "user" | "visitor" | "system" | string;
  uploadSecurityPolicy: UploadSecurityPolicy;
  status: UploadCheckStatus | string;
  resultCode: string | null;
  message: string | null;
  createdAt: string;
  updatedAt: string;
  completedAt: string | null;
  quarantinedAt: string | null;
  rejectedAt: string | null;
}

export interface UploadCheckListItem {
  uploadCheck: UploadCheck;
  assetName: string;
  sourceFilename: string;
  sourceSizeBytes: number;
  projectName: string | null;
  uploadedByName: string | null;
}

export interface UploadCheckListPage {
  items: UploadCheckListItem[];
  total: number;
  page: number;
  pageSize: number;
}

export interface UploadCheckQuery {
  status?: UploadCheckStatus | "all";
  projectId?: string;
  page?: number;
  pageSize?: number;
}

export interface UploadCheckActionInput {
  message?: string;
}

export type ReviewSessionStatus =
  | "draft"
  | "open"
  | "changes_requested"
  | "approved"
  | "closed";

export interface ReviewParticipant {
  id: string;
  userId: string | null;
  displayName: string;
  role: "reviewer" | "observer";
}

export interface ReviewItem {
  id: string;
  assetId: string;
  assetVersionId: string;
  assetName: string;
  versionNumber: number;
  position: number;
  status: "pending" | "in_review" | "approved" | "changes_requested";
  createdAt: string;
  updatedAt: string;
}

export interface ReviewSession {
  id: string;
  workspaceId: string;
  projectId: string;
  projectName: string;
  collectionId: string | null;
  name: string;
  status: ReviewSessionStatus;
  dueAt: string | null;
  templateId: string | null;
  templateRevision: number | null;
  responsibleUserId: string | null;
  responsibleName: string;
  allowDownload: boolean;
  decisionRule: ReviewDecisionRule;
  revision: number;
  createdAt: string;
  updatedAt: string;
  closedAt: string | null;
  items: ReviewItem[];
  participants: ReviewParticipant[];
}

export interface ReviewParticipantInput {
  userId: string | null;
  displayName: string;
  role: "reviewer" | "observer";
}

export type ReviewDecisionRule =
  | "any_reviewer"
  | "all_reviewers"
  | "responsible_only";

export interface ReviewTemplate {
  id: string;
  workspaceId: string;
  name: string;
  description: string | null;
  participantRoles: Array<"reviewer" | "observer">;
  allowDownload: boolean;
  dueDays: number | null;
  decisionRule: ReviewDecisionRule;
  revision: number;
  createdAt: string;
  updatedAt: string;
}

export interface ReviewTemplateInput {
  name: string;
  description: string | null;
  participantRoles: Array<"reviewer" | "observer">;
  allowDownload: boolean;
  dueDays: number | null;
  decisionRule: ReviewDecisionRule;
}

export interface CreateReviewSessionInput {
  projectId: string;
  collectionId: string | null;
  name: string;
  dueAt: string | null;
  templateId: string | null;
  responsibleUserId: string | null;
  allowDownload: boolean;
  decisionRule: ReviewDecisionRule;
  participants: ReviewParticipantInput[];
  items: Array<{
    assetId: string;
    assetVersionId: string;
  }>;
}

export interface UpdateReviewSessionInput {
  name: string;
  dueAt: string | null;
  responsibleUserId: string | null;
  participants: ReviewParticipantInput[];
  revision: number;
}

export type ReviewDecisionValue = "approved" | "changes_requested" | "rejected";

export interface ReviewDecision {
  id: string;
  reviewSessionId: string;
  reviewItemId: string | null;
  actor: CommentAuthor;
  decision: ReviewDecisionValue;
  note: string | null;
  createdAt: string;
}

export interface CreateReviewDecisionInput {
  reviewItemId: string | null;
  decision: ReviewDecisionValue;
  note: string;
}

export type ShareStatus = "active" | "expired" | "revoked";

export interface ShareLink {
  id: string;
  shareId: string;
  tokenPrefix: string;
  status: "active" | "revoked";
  createdAt: string;
  lastUsedAt: string | null;
  revokedAt: string | null;
}

export interface Share {
  id: string;
  reviewSessionId: string | null;
  reviewSessionName: string | null;
  name: string;
  status: ShareStatus;
  allowComment: boolean;
  allowDownload: boolean;
  requireNickname: boolean;
  expiresAt: string | null;
  passwordProtected: boolean;
  revision: number;
  createdAt: string;
  updatedAt: string;
  revokedAt: string | null;
  links: ShareLink[];
}

export interface AccessEvent {
  id: string;
  shareId: string;
  shareLinkId: string | null;
  visitorId: string | null;
  visitorSessionId: string | null;
  visitorName: string;
  eventType:
    | "opened"
    | "verified"
    | "identified"
    | "viewed"
    | "downloaded"
    | "commented"
    | "decision_submitted"
    | string;
  resourceType: string | null;
  resourceId: string | null;
  userAgentSummary: string;
  occurredAt: string;
}

export interface AuditLog {
  id: string;
  actorType: "user" | "visitor" | "system" | "node" | string;
  actorId: string | null;
  actorName: string;
  action: string;
  resourceType: string;
  resourceId: string;
  requestId: string;
  details: Record<string, string>;
  occurredAt: string;
}

export interface Notification {
  id: string;
  type: string;
  resourceType: string;
  resourceId: string;
  title: string;
  body: string;
  readAt: string | null;
  createdAt: string;
}

export interface NotificationList {
  items: Notification[];
  unreadCount: number;
}

export type NotificationChannelKind = "email" | "feishu" | "wechat_work";

export interface NotificationChannel {
  id: string;
  workspaceId: string;
  kind: NotificationChannelKind | string;
  name: string;
  status: "active" | "disabled" | "error" | string;
  smtpHost?: string;
  smtpPort?: number;
  smtpSecurity?: "starttls" | "tls" | "plain" | string;
  smtpFromAddress?: string;
  smtpFromName?: string;
  smtpAuthConfigured: boolean;
  testRecipient?: string;
  webhookHost?: string;
  allowPrivateNetwork: boolean;
  revision: number;
  createdAt: string;
  updatedAt: string;
  lastTestAt: string | null;
  lastTestStatus: "succeeded" | "failed" | null;
  lastErrorCode: string | null;
  lastErrorMessage: string | null;
}

export interface NotificationChannelInput {
  kind: NotificationChannelKind;
  name: string;
  smtpHost?: string;
  smtpPort?: number;
  smtpSecurity?: "starttls" | "tls" | "plain";
  smtpFromAddress?: string;
  smtpFromName?: string;
  smtpUsername?: string;
  smtpPassword?: string;
  testRecipient?: string;
  webhookUrl?: string;
  allowPrivateNetwork?: boolean;
  revision?: number;
}

export interface NotificationChannelTestReport {
  channelId: string;
  status: "succeeded" | "failed" | string;
  latencyMs: number;
  testedAt: string;
}

export interface NotificationPreferences {
  userId: string;
  emailEnabled: boolean;
  feishuEnabled: boolean;
  wechatWorkEnabled: boolean;
  updatedAt: string;
}

export interface NotificationDelivery {
  id: string;
  outboxId: string;
  notificationId: string | null;
  recipientUserId: string | null;
  channelId: string | null;
  channelKind: NotificationChannelKind | string;
  channelName: string;
  status: "queued" | "sending" | "succeeded" | "failed" | "skipped" | string;
  attempts: number;
  maxAttempts: number;
  lastErrorCode: string | null;
  lastError: string | null;
  createdAt: string;
  updatedAt: string;
  deliveredAt: string | null;
}

export interface CreateShareInput {
  reviewSessionId: string;
  name: string;
  allowComment: boolean;
  allowDownload: boolean;
  requireNickname: boolean;
  expiresAt: string | null;
  password: string | null;
  notifyUserIds?: string[];
}

export interface UpdateShareInput {
  name: string;
  allowComment: boolean;
  allowDownload: boolean;
  requireNickname: boolean;
  expiresAt: string | null;
  password?: string;
}

export interface ShareSecret {
  share?: Share;
  link: ShareLink;
  url: string;
  password?: string | null;
}

export interface ShareCredentialLink {
  linkId: string;
  url: string;
}

export interface ShareCredentials {
  shareId: string;
  password: string | null;
  links: ShareCredentialLink[];
}

export interface CreateVisitorCodeInput {
  displayName: string;
  expiresAt: string | null;
}

export interface VisitorCodeSecret {
  id: string;
  shareId: string;
  codePrefix: string;
  displayName: string;
  status: "active" | "revoked" | string;
  expiresAt: string | null;
  createdAt: string;
  lastUsedAt: string | null;
  revokedAt: string | null;
  code: string;
}

export interface PublicShareItem {
  id: string;
  assetName: string;
  versionNumber: number;
  mediaType: string;
  durationUs: number | null;
  width: number | null;
  height: number | null;
  sizeBytes: number;
  previewKind:
    | "thumbnail"
    | "screen_preview"
    | "poster"
    | "proxy"
    | "hls"
    | "storyboard"
    | null;
  previewUrl: string | null;
  thumbnailUrl: string | null;
  downloadUrl: string | null;
}

export type PublicVisitorIdentityMethod =
  | "anonymous"
  | "nickname"
  | "verification_code";

export interface PublicVisitor {
  displayName: string | null;
  identityMethod: PublicVisitorIdentityMethod | string;
  identified: boolean;
  verified: boolean;
}

export interface PublicShare {
  name: string;
  reviewName: string;
  teamName: string;
  reviewStatus: ReviewSessionStatus;
  allowComment: boolean;
  allowDownload: boolean;
  requireNickname: boolean;
  expiresAt: string | null;
  visitor: PublicVisitor;
  items: PublicShareItem[];
}

export interface PublicShareEntry {
  status: "ready" | "password_required";
  share?: PublicShare;
}

export interface IdentifyPublicShareInput {
  method: PublicVisitorIdentityMethod;
  displayName?: string | null;
  code?: string | null;
}

export interface CommentAuthor {
  kind: "user" | "share_visitor" | "system" | string;
  displayName: string;
}

export interface ReviewAnnotation {
  kind: "time_point" | "time_range" | "point" | "region" | "drawing" | string;
  timeStartUs: number | null;
  timeEndUs: number | null;
  geometry: AnnotationGeometry | null;
  geometryVersion: number;
}

export interface PointAnnotationGeometry {
  shape: "point";
  x: number;
  y: number;
}

export interface RegionAnnotationGeometry {
  shape: "rect";
  x: number;
  y: number;
  width: number;
  height: number;
}

export interface DrawingPoint {
  x: number;
  y: number;
}

export type DrawingElement =
  | {
      tool: "brush";
      color: string;
      strokeWidth: number;
      points: DrawingPoint[];
    }
  | {
      tool: "arrow";
      color: string;
      strokeWidth: number;
      points: [DrawingPoint, DrawingPoint];
    }
  | {
      tool: "rect";
      color: string;
      strokeWidth: number;
      x: number;
      y: number;
      width: number;
      height: number;
    };

export interface DrawingAnnotationGeometry {
  shape: "drawing";
  elements: DrawingElement[];
}

export type AnnotationGeometry =
  | PointAnnotationGeometry
  | RegionAnnotationGeometry
  | DrawingAnnotationGeometry;

export interface ReviewComment {
  id: string;
  author: CommentAuthor;
  body: string;
  attachments: ReviewCommentAttachment[];
  editedAt: string | null;
  createdAt: string;
  canEdit: boolean;
  canDelete: boolean;
}

export interface ReviewCommentAttachment {
  id: string;
  originalFilename: string;
  mimeType: string;
  sizeBytes: number;
  width: number | null;
  height: number | null;
  url: string;
  createdAt: string;
}

export interface CommentThread {
  id: string;
  reviewItemId: string;
  assetVersionId: string;
  status: "open" | "resolved" | string;
  author: CommentAuthor;
  resolvedBy: CommentAuthor | null;
  resolvedAt: string | null;
  annotation: ReviewAnnotation;
  comments: ReviewComment[];
  revision: number;
  createdAt: string;
  updatedAt: string;
  canReply: boolean;
  canResolve: boolean;
  canReopen: boolean;
}

export interface CreateCommentThreadInput {
  body: string;
  attachmentIds?: string[];
  annotation:
    | {
        kind: "time_point" | "time_range";
        timeStartUs: number;
        timeEndUs: number | null;
        geometry: null;
        geometryVersion: 1;
      }
    | {
        kind: "point" | "region" | "drawing";
        timeStartUs: null;
        timeEndUs: null;
        geometry: AnnotationGeometry;
        geometryVersion: 1;
      };
}

export interface CreateCommentInput {
  body: string;
  attachmentIds?: string[];
}

export interface UpdateCommentInput {
  body: string;
}

export interface ThreadStateInput {
  revision: number;
}

export interface MediaLibraryPage {
  items: MediaLibraryItem[];
  total: number;
  page: number;
  pageSize: number;
}

export interface MediaLibraryQuery {
  search?: string;
  mediaType?: MediaProbe["mediaType"] | "all";
  state?: MediaLibraryState | "all";
  projectId?: string | "all" | "unassigned";
  modifiedFrom?: string;
  modifiedTo?: string;
  sort?:
    | "modified_desc"
    | "modified_asc"
    | "name_asc"
    | "name_desc"
    | "size_desc"
    | "size_asc";
  page?: number;
  pageSize?: number;
}

export type JobStatus =
  | "queued"
  | "leased"
  | "running"
  | "succeeded"
  | "failed"
  | "cancel_requested"
  | "cancelled";

export interface Job {
  id: string;
  type: string;
  status: JobStatus;
  priority: number;
  subject: {
    type: string;
    id: string;
  };
  progress: {
    current: number;
    total: number;
    unit: string;
  } | null;
  error: {
    code: string;
    message: string;
  } | null;
  attemptCount: number;
  maxAttempts: number;
  createdAt: string;
  updatedAt: string;
  startedAt: string | null;
  completedAt: string | null;
}

export interface JobEnvelope {
  job: Job;
}

export interface JobListEnvelope {
  jobs: Job[];
}
