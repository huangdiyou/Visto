import type {
  Collection,
  CollectionItem,
  CreateCollectionInput,
  CreateProjectInput,
  ItemList,
  ProjectMember,
  ProjectMemberInput,
  Project,
  ProjectOverview,
  ProjectGuestInput,
  ProjectTransferInput,
  UpdateCollectionInput,
  UpdateProjectInput,
} from "@review-studio/contracts";
import { requestJSON, requestVoid } from "./client";

export async function listProjects(signal?: AbortSignal): Promise<Project[]> {
  const options = signal ? { signal } : {};
  const response = await requestJSON<ItemList<Project>>(
    "/api/v1/projects",
    options,
  );
  return response.items;
}

export function createProject(input: CreateProjectInput): Promise<Project> {
  return requestJSON<Project>("/api/v1/projects", {
    method: "POST",
    body: input,
  });
}

export function updateProject(
  projectId: string,
  input: UpdateProjectInput,
): Promise<Project> {
  return requestJSON<Project>(`/api/v1/projects/${projectId}`, {
    method: "PATCH",
    body: input,
  });
}

export function archiveProject(project: Project): Promise<Project> {
  return requestJSON<Project>(`/api/v1/projects/${project.id}/archive`, {
    method: "POST",
    body: { revision: project.revision },
  });
}

export function restoreProject(project: Project): Promise<Project> {
  return requestJSON<Project>(`/api/v1/projects/${project.id}/restore`, {
    method: "POST",
    body: { revision: project.revision },
  });
}

export function deleteProject(project: Project): Promise<void> {
  return requestVoid(`/api/v1/projects/${project.id}`, {
    method: "DELETE",
    body: { revision: project.revision },
  });
}

export function getProjectOverview(
  projectId: string,
  signal?: AbortSignal,
): Promise<ProjectOverview> {
  return requestJSON<ProjectOverview>(
    `/api/v1/projects/${encodeURIComponent(projectId)}/overview`,
    signal ? { signal } : {},
  );
}

export async function listProjectMembers(
  projectId: string,
  signal?: AbortSignal,
): Promise<ProjectMember[]> {
  const options = signal ? { signal } : {};
  const response = await requestJSON<ItemList<ProjectMember>>(
    `/api/v1/projects/${projectId}/members`,
    options,
  );
  return response.items;
}

export function addProjectMember(
  projectId: string,
  input: ProjectMemberInput,
): Promise<ProjectMember> {
  return requestJSON<ProjectMember>(`/api/v1/projects/${projectId}/members`, {
    method: "POST",
    body: input,
  });
}

export function createProjectGuest(
  projectId: string,
  input: ProjectGuestInput,
): Promise<ProjectMember> {
  return requestJSON<ProjectMember>(`/api/v1/projects/${projectId}/guests`, {
    method: "POST",
    body: input,
  });
}

export function updateProjectMember(
  item: ProjectMember,
  input: ProjectMemberInput,
): Promise<ProjectMember> {
  return requestJSON<ProjectMember>(
    `/api/v1/projects/${item.projectId}/members/${item.id}`,
    { method: "PATCH", body: { ...input, revision: item.revision } },
  );
}

export function removeProjectMember(item: ProjectMember): Promise<void> {
  return requestVoid(`/api/v1/projects/${item.projectId}/members/${item.id}`, {
    method: "DELETE",
    body: { revision: item.revision },
  });
}

export function transferProject(
  projectId: string,
  input: ProjectTransferInput,
): Promise<ProjectMember> {
  return requestJSON<ProjectMember>(`/api/v1/projects/${projectId}/transfer`, {
    method: "POST",
    body: input,
  });
}

export async function listCollections(
  projectId: string,
  signal?: AbortSignal,
): Promise<Collection[]> {
  const options = signal ? { signal } : {};
  const response = await requestJSON<ItemList<Collection>>(
    `/api/v1/projects/${projectId}/collections`,
    options,
  );
  return response.items;
}

export function createCollection(
  projectId: string,
  input: CreateCollectionInput,
): Promise<Collection> {
  return requestJSON<Collection>(`/api/v1/projects/${projectId}/collections`, {
    method: "POST",
    body: input,
  });
}

export function updateCollection(
  collectionId: string,
  input: UpdateCollectionInput,
): Promise<Collection> {
  return requestJSON<Collection>(`/api/v1/collections/${collectionId}`, {
    method: "PATCH",
    body: input,
  });
}

export function deleteCollection(collection: Collection): Promise<void> {
  return requestVoid(`/api/v1/collections/${collection.id}`, {
    method: "DELETE",
    body: { revision: collection.revision },
  });
}

export async function listCollectionItems(
  collectionId: string,
  signal?: AbortSignal,
): Promise<CollectionItem[]> {
  const options = signal ? { signal } : {};
  const response = await requestJSON<ItemList<CollectionItem>>(
    `/api/v1/collections/${collectionId}/items`,
    options,
  );
  return response.items;
}

export function reorderCollectionItems(
  collectionId: string,
  itemIds: string[],
): Promise<void> {
  return requestVoid(`/api/v1/collections/${collectionId}/items/order`, {
    method: "PATCH",
    body: { itemIds },
  });
}
