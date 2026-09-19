import type {
  AssetVersion,
  AuthorizedRoot,
  CreateLocalManagedBucketInput,
  CreateStorageCopyTaskInput,
  DirectoryScan,
  ItemList,
  Job,
  JobEnvelope,
  JobListEnvelope,
  LocalManagedBucket,
  MediaLibraryPage,
  MediaLibraryAsset,
  MediaLibraryQuery,
  MediaProbe,
  MoveProjectAssetInput,
  ProjectAsset,
  ProjectAssetInput,
  ProjectStorageGrant,
  ProjectStorageGrantInput,
  ProjectStorageSelection,
  ProjectStorageSelectionInput,
  ProjectUploadTarget,
  RegisterProviderRootInput,
  Rendition,
  RegisterAuthorizedRootInput,
  StorageObject,
  StorageConnectionReport,
  StorageDeleteImpact,
  StorageCopyTask,
  StorageCopyTaskEnvelope,
  StorageProvider,
  StorageProviderInput,
  UpdateMediaLibraryAssetInput,
  UpdateAuthorizedRootInput,
  UpdateLocalManagedBucketInput,
  UploadCheck,
  UploadCheckActionInput,
  UploadCheckListPage,
  UploadCheckQuery,
} from "@review-studio/contracts";
import {
  requestFormJSONWithProgress,
  requestJSON,
  requestVoid,
  type UploadProgress,
} from "./client";

export async function listAuthorizedRoots(
  signal?: AbortSignal,
): Promise<AuthorizedRoot[]> {
  const response = await requestJSON<ItemList<AuthorizedRoot>>(
    "/api/v1/authorized-roots",
    signal ? { signal } : {},
  );
  return response.items;
}

export async function listStorageProviders(
  signal?: AbortSignal,
): Promise<StorageProvider[]> {
  const response = await requestJSON<ItemList<StorageProvider>>(
    "/api/v1/storage-providers",
    signal ? { signal } : {},
  );
  return response.items;
}

export function createStorageProvider(
  input: StorageProviderInput,
): Promise<StorageProvider> {
  return requestJSON<StorageProvider>("/api/v1/storage-providers", {
    method: "POST",
    body: input,
  });
}

export function updateStorageProvider(
  provider: StorageProvider,
  input: StorageProviderInput,
): Promise<StorageProvider> {
  return requestJSON<StorageProvider>(
    `/api/v1/storage-providers/${provider.id}`,
    { method: "PATCH", body: { ...input, revision: provider.revision } },
  );
}

export function deleteStorageProvider(
  provider: StorageProvider,
): Promise<void> {
  return requestVoid(`/api/v1/storage-providers/${provider.id}`, {
    method: "DELETE",
    body: { revision: provider.revision },
  });
}

export function getStorageProviderDeleteImpact(
  providerId: string,
  signal?: AbortSignal,
): Promise<StorageDeleteImpact> {
  return requestJSON<StorageDeleteImpact>(
    `/api/v1/storage-providers/${providerId}/delete-impact`,
    signal ? { signal } : {},
  );
}

export function updateMediaAssetName(
  asset: MediaLibraryAsset,
  name: string,
): Promise<MediaLibraryAsset> {
  const body: UpdateMediaLibraryAssetInput = {
    name,
    revision: asset.revision,
  };
  return requestJSON<MediaLibraryAsset>(`/api/v1/assets/${asset.id}`, {
    method: "PATCH",
    body,
  });
}

export function testStorageProvider(
  providerId: string,
): Promise<StorageConnectionReport> {
  return requestJSON<StorageConnectionReport>(
    `/api/v1/storage-providers/${providerId}/test`,
    { method: "POST" },
  );
}

export function registerProviderRoot(
  providerId: string,
  input: RegisterProviderRootInput,
): Promise<AuthorizedRoot> {
  return requestJSON<AuthorizedRoot>(
    `/api/v1/storage-providers/${providerId}/roots`,
    { method: "POST", body: input },
  );
}

export function createStorageCopyTask(
  input: CreateStorageCopyTaskInput,
): Promise<StorageCopyTaskEnvelope> {
  return requestJSON<StorageCopyTaskEnvelope>("/api/v1/storage-copy-tasks", {
    method: "POST",
    body: input,
  });
}

export function getStorageCopyTask(taskId: string): Promise<StorageCopyTask> {
  return requestJSON<StorageCopyTask>(`/api/v1/storage-copy-tasks/${taskId}`);
}

export async function listProjectStorageGrants(
  signal?: AbortSignal,
): Promise<ProjectStorageGrant[]> {
  const response = await requestJSON<ItemList<ProjectStorageGrant>>(
    "/api/v1/project-storage-grants",
    signal ? { signal } : {},
  );
  return response.items;
}

export async function listProjectCreationStorageGrants(
  signal?: AbortSignal,
): Promise<ProjectStorageGrant[]> {
  const response = await requestJSON<ItemList<ProjectStorageGrant>>(
    "/api/v1/project-storage-grants/available",
    signal ? { signal } : {},
  );
  return response.items;
}

export function setProjectStorageGrant(
  input: ProjectStorageGrantInput,
): Promise<ProjectStorageGrant> {
  return requestJSON<ProjectStorageGrant>("/api/v1/project-storage-grants", {
    method: "PUT",
    body: input,
  });
}

export async function listLocalManagedBuckets(
  signal?: AbortSignal,
): Promise<LocalManagedBucket[]> {
  const response = await requestJSON<ItemList<LocalManagedBucket>>(
    "/api/v1/local-managed-buckets",
    signal ? { signal } : {},
  );
  return response.items;
}

export function createLocalManagedBucket(
  input: CreateLocalManagedBucketInput,
): Promise<LocalManagedBucket> {
  return requestJSON<LocalManagedBucket>("/api/v1/local-managed-buckets", {
    method: "POST",
    body: input,
  });
}

export function updateLocalManagedBucket(
  bucket: LocalManagedBucket,
  input: Omit<UpdateLocalManagedBucketInput, "revision">,
): Promise<LocalManagedBucket> {
  return requestJSON<LocalManagedBucket>(
    `/api/v1/local-managed-buckets/${bucket.id}`,
    { method: "PATCH", body: { ...input, revision: bucket.revision } },
  );
}

export function getLocalManagedBucketDeleteImpact(
  bucketId: string,
  signal?: AbortSignal,
): Promise<StorageDeleteImpact> {
  return requestJSON<StorageDeleteImpact>(
    `/api/v1/local-managed-buckets/${bucketId}/delete-impact`,
    signal ? { signal } : {},
  );
}

export function deleteLocalManagedBucket(
  bucket: LocalManagedBucket,
): Promise<void> {
  return requestVoid(`/api/v1/local-managed-buckets/${bucket.id}`, {
    method: "DELETE",
    body: { revision: bucket.revision },
  });
}

export function listUploadChecks(
  query: UploadCheckQuery = {},
  signal?: AbortSignal,
): Promise<UploadCheckListPage> {
  const parameters = new URLSearchParams();
  for (const [key, value] of Object.entries(query)) {
    if (value !== undefined && value !== "" && value !== "all") {
      parameters.set(key, String(value));
    }
  }
  const suffix = parameters.size > 0 ? `?${parameters.toString()}` : "";
  return requestJSON<UploadCheckListPage>(
    `/api/v1/upload-checks${suffix}`,
    signal ? { signal } : {},
  );
}

export function releaseUploadCheck(
  checkId: string,
  input: UploadCheckActionInput = {},
): Promise<UploadCheck> {
  return requestJSON<UploadCheck>(`/api/v1/upload-checks/${checkId}/release`, {
    method: "POST",
    body: input,
  });
}

export function rejectUploadCheck(
  checkId: string,
  input: UploadCheckActionInput = {},
): Promise<UploadCheck> {
  return requestJSON<UploadCheck>(`/api/v1/upload-checks/${checkId}/reject`, {
    method: "POST",
    body: input,
  });
}

export async function listAvailableProjectStorageGrants(
  projectId: string,
  signal?: AbortSignal,
): Promise<ProjectStorageGrant[]> {
  const response = await requestJSON<ItemList<ProjectStorageGrant>>(
    `/api/v1/projects/${projectId}/storage-grants`,
    signal ? { signal } : {},
  );
  return response.items;
}

export async function listProjectStorageSelections(
  projectId: string,
  signal?: AbortSignal,
): Promise<ProjectStorageSelection[]> {
  const response = await requestJSON<ItemList<ProjectStorageSelection>>(
    `/api/v1/projects/${projectId}/storage-selections`,
    signal ? { signal } : {},
  );
  return response.items;
}

export function getProjectUploadTarget(
  projectId: string,
  options: { sizeBytes?: number } = {},
  signal?: AbortSignal,
): Promise<ProjectUploadTarget> {
  const params = new URLSearchParams();
  if (typeof options.sizeBytes === "number" && options.sizeBytes >= 0) {
    params.set("sizeBytes", String(Math.trunc(options.sizeBytes)));
  }
  const query = params.toString();
  return requestJSON<ProjectUploadTarget>(
    `/api/v1/projects/${projectId}/upload-target${query ? `?${query}` : ""}`,
    signal ? { signal } : {},
  );
}

export function selectProjectStorage(
  projectId: string,
  purpose: ProjectStorageSelection["purpose"],
  input: ProjectStorageSelectionInput,
): Promise<ProjectStorageSelection> {
  return requestJSON<ProjectStorageSelection>(
    `/api/v1/projects/${projectId}/storage-selections/${purpose}`,
    { method: "PUT", body: input },
  );
}

export function registerAuthorizedRoot(
  input: RegisterAuthorizedRootInput,
): Promise<AuthorizedRoot> {
  return requestJSON<AuthorizedRoot>("/api/v1/authorized-roots", {
    method: "POST",
    body: input,
  });
}

export function updateAuthorizedRoot(
  root: AuthorizedRoot,
  input: Omit<UpdateAuthorizedRootInput, "revision">,
): Promise<AuthorizedRoot> {
  return requestJSON<AuthorizedRoot>(`/api/v1/authorized-roots/${root.id}`, {
    method: "PATCH",
    body: { ...input, revision: root.revision },
  });
}

export function deleteAuthorizedRoot(root: AuthorizedRoot): Promise<void> {
  return requestVoid(`/api/v1/authorized-roots/${root.id}`, {
    method: "DELETE",
    body: { revision: root.revision },
  });
}

export function scanAuthorizedRoot(rootId: string): Promise<DirectoryScan> {
  return requestJSON<DirectoryScan>(`/api/v1/authorized-roots/${rootId}/scan`, {
    method: "POST",
  });
}

export async function listStorageObjects(
  rootId: string,
  signal?: AbortSignal,
): Promise<StorageObject[]> {
  const response = await requestJSON<ItemList<StorageObject>>(
    `/api/v1/authorized-roots/${rootId}/objects`,
    signal ? { signal } : {},
  );
  return response.items;
}

export function queryMediaLibrary(
  rootId: string,
  query: MediaLibraryQuery,
  signal?: AbortSignal,
): Promise<MediaLibraryPage> {
  const parameters = new URLSearchParams();
  for (const [key, value] of Object.entries(query)) {
    if (value !== undefined && value !== "" && value !== "all") {
      parameters.set(key, String(value));
    }
  }
  const suffix = parameters.size > 0 ? `?${parameters.toString()}` : "";
  return requestJSON<MediaLibraryPage>(
    `/api/v1/authorized-roots/${rootId}/library${suffix}`,
    signal ? { signal } : {},
  );
}

export function queryProjectMediaLibrary(
  projectId: string,
  query: MediaLibraryQuery,
  signal?: AbortSignal,
): Promise<MediaLibraryPage> {
  const parameters = new URLSearchParams();
  for (const [key, value] of Object.entries(query)) {
    if (
      key !== "projectId" &&
      value !== undefined &&
      value !== "" &&
      value !== "all"
    ) {
      parameters.set(key, String(value));
    }
  }
  const suffix = parameters.size > 0 ? `?${parameters.toString()}` : "";
  return requestJSON<MediaLibraryPage>(
    `/api/v1/projects/${projectId}/library${suffix}`,
    signal ? { signal } : {},
  );
}

export function queryProjectMediaCandidates(
  projectId: string,
  query: MediaLibraryQuery,
  signal?: AbortSignal,
): Promise<MediaLibraryPage> {
  const parameters = new URLSearchParams();
  for (const [key, value] of Object.entries(query)) {
    if (
      key !== "projectId" &&
      value !== undefined &&
      value !== "" &&
      value !== "all"
    ) {
      parameters.set(key, String(value));
    }
  }
  const suffix = parameters.size > 0 ? `?${parameters.toString()}` : "";
  return requestJSON<MediaLibraryPage>(
    `/api/v1/projects/${projectId}/asset-candidates${suffix}`,
    signal ? { signal } : {},
  );
}

export function assignMediaAssetProject(
  storageObjectId: string,
  projectId: string | null,
): Promise<MediaLibraryAsset> {
  return requestJSON<MediaLibraryAsset>(
    `/api/v1/storage-objects/${storageObjectId}/project`,
    { method: "PATCH", body: { projectId } },
  );
}

export async function listProjectAssetTrash(
  projectId: string,
  signal?: AbortSignal,
): Promise<ProjectAsset[]> {
  const response = await requestJSON<ItemList<ProjectAsset>>(
    `/api/v1/projects/${projectId}/asset-trash`,
    signal ? { signal } : {},
  );
  return response.items;
}

export function copyAssetToProject(
  projectId: string,
  input: ProjectAssetInput,
): Promise<ProjectAsset> {
  return requestJSON<ProjectAsset>(`/api/v1/projects/${projectId}/assets`, {
    method: "POST",
    body: input,
  });
}

export function moveAssetToProject(
  sourceProjectId: string,
  assetId: string,
  input: MoveProjectAssetInput,
): Promise<ProjectAsset> {
  return requestJSON<ProjectAsset>(
    `/api/v1/projects/${sourceProjectId}/assets/${assetId}/move`,
    { method: "POST", body: input },
  );
}

export function trashAssetFromProject(
  projectId: string,
  assetId: string,
): Promise<ProjectAsset> {
  return requestJSON<ProjectAsset>(
    `/api/v1/projects/${projectId}/assets/${assetId}`,
    { method: "DELETE" },
  );
}

export function restoreProjectAsset(
  projectId: string,
  assetId: string,
): Promise<ProjectAsset> {
  return requestJSON<ProjectAsset>(
    `/api/v1/projects/${projectId}/asset-trash/${assetId}/restore`,
    { method: "POST" },
  );
}

export async function listAssetVersions(
  assetId: string,
  signal?: AbortSignal,
): Promise<AssetVersion[]> {
  const response = await requestJSON<ItemList<AssetVersion>>(
    `/api/v1/assets/${assetId}/versions`,
    signal ? { signal } : {},
  );
  return response.items;
}

export function setCurrentAssetVersion(
  asset: MediaLibraryAsset,
  versionId: string,
): Promise<MediaLibraryAsset> {
  return requestJSON<MediaLibraryAsset>(
    `/api/v1/assets/${asset.id}/current-version`,
    { method: "POST", body: { versionId, revision: asset.revision } },
  );
}

export function uploadAsset(
  file: File,
  options: {
    projectId?: string | null;
    signal?: AbortSignal;
    onProgress?: (progress: UploadProgress) => void;
  } = {},
): Promise<AssetVersion> {
  const body = new FormData();
  if (options.projectId) {
    body.append("projectId", options.projectId);
  }
  body.append("file", file, file.name);
  return requestFormJSONWithProgress<AssetVersion>("/api/v1/assets", body, {
    signal: options.signal,
    onProgress: options.onProgress,
  });
}

export function uploadAssetVersion(
  asset: MediaLibraryAsset,
  file: File,
  options: {
    signal?: AbortSignal;
    onProgress?: (progress: UploadProgress) => void;
  } = {},
): Promise<AssetVersion> {
  const body = new FormData();
  body.append("revision", String(asset.revision));
  body.append("file", file, file.name);
  return requestFormJSONWithProgress<AssetVersion>(
    `/api/v1/assets/${asset.id}/versions`,
    body,
    {
      signal: options.signal,
      onProgress: options.onProgress,
    },
  );
}

export async function listMediaProbes(
  rootId: string,
  signal?: AbortSignal,
): Promise<MediaProbe[]> {
  const response = await requestJSON<ItemList<MediaProbe>>(
    `/api/v1/authorized-roots/${rootId}/media-probes`,
    signal ? { signal } : {},
  );
  return response.items;
}

export async function probeAuthorizedRoot(rootId: string): Promise<Job> {
  const response = await requestJSON<JobEnvelope>(
    `/api/v1/authorized-roots/${rootId}/media-probes`,
    { method: "POST" },
  );
  return response.job;
}

export async function listRenditions(
  rootId: string,
  signal?: AbortSignal,
): Promise<Rendition[]> {
  const response = await requestJSON<ItemList<Rendition>>(
    `/api/v1/authorized-roots/${rootId}/renditions`,
    signal ? { signal } : {},
  );
  return response.items;
}

export async function generateRenditions(rootId: string): Promise<Job[]> {
  const response = await requestJSON<JobListEnvelope>(
    `/api/v1/authorized-roots/${rootId}/renditions`,
    { method: "POST" },
  );
  return response.jobs;
}

export async function generateVideoRenditions(rootId: string): Promise<Job[]> {
  const response = await requestJSON<JobListEnvelope>(
    `/api/v1/authorized-roots/${rootId}/video-renditions`,
    { method: "POST" },
  );
  return response.jobs;
}
