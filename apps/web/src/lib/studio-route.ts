export type ProjectSection = "overview" | "media" | "reviews" | "settings";
export type OwnerSection =
  | "accounts"
  | "network"
  | "storage"
  | "notifications"
  | "activity"
  | "diagnostics";

export type StudioRoute =
  | { area: "projects" }
  | {
      area: "project";
      projectId: string;
      section: ProjectSection;
      reviewId?: string;
      threadId?: string;
      assetId?: string;
    }
  | { area: "owner"; section: OwnerSection };

const projectSections = new Set<ProjectSection>([
  "overview",
  "media",
  "reviews",
  "settings",
]);
const ownerSections = new Set<OwnerSection>([
  "accounts",
  "network",
  "storage",
  "notifications",
  "activity",
  "diagnostics",
]);

export function parseStudioRoute(hash: string): StudioRoute {
  const path = hash.replace(/^#\/?/, "");
  const parts = path.split("/").filter(Boolean);

  if (parts[0] === "projects" && parts[1]) {
    const section = projectSections.has(parts[2] as ProjectSection)
      ? (parts[2] as ProjectSection)
      : "overview";
    return {
      area: "project",
      projectId: decodeURIComponent(parts[1]),
      section,
      ...(section === "reviews" && parts[3]
        ? { reviewId: decodeURIComponent(parts[3]) }
        : {}),
      ...(section === "reviews" && parts[4]
        ? { threadId: decodeURIComponent(parts[4]) }
        : {}),
      ...(section === "media" && parts[3]
        ? { assetId: decodeURIComponent(parts[3]) }
        : {}),
    };
  }

  if (parts[0] === "sources") {
    return { area: "projects" };
  }

  if (parts[0] === "owner") {
    return {
      area: "owner",
      section: ownerSections.has(parts[1] as OwnerSection)
        ? (parts[1] as OwnerSection)
        : "accounts",
    };
  }

  return { area: "projects" };
}

export function studioRouteToHash(route: StudioRoute): string {
  if (route.area === "project") {
    let value = `#/projects/${encodeURIComponent(route.projectId)}/${route.section}`;
    if (route.section === "reviews" && route.reviewId) {
      value += `/${encodeURIComponent(route.reviewId)}`;
      if (route.threadId) {
        value += `/${encodeURIComponent(route.threadId)}`;
      }
    } else if (route.section === "media" && route.assetId) {
      value += `/${encodeURIComponent(route.assetId)}`;
    }
    return value;
  }
  if (route.area === "owner") {
    return `#/owner/${route.section}`;
  }
  return "#/projects";
}
