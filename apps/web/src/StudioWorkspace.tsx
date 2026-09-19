import {
  lazy,
  Suspense,
  useEffect,
  useRef,
  useState,
  type FormEvent,
} from "react";
import type {
  Project,
  ProjectMember,
  ProjectStorageGrant,
  SessionInfo,
  SystemInfo,
} from "@review-studio/contracts";
import {
  Bell,
  ChevronDown,
  CircleUserRound,
  Film,
  FolderKanban,
  LogOut,
  Menu,
  Plus,
  Settings,
  SlidersHorizontal,
  UsersRound,
  X,
} from "lucide-react";
import { createProject, listProjectMembers, listProjects } from "./api/catalog";
import { listNotifications } from "./api/activity";
import { listProjectCreationStorageGrants } from "./api/storage";
import { VistoMark, PRODUCT_NAME_EN } from "./brand";
import { ProjectOverviewPanel } from "./ProjectOverview";
import type { ProjectSettingsSection } from "./ProjectSettingsWorkspace";
import type { ReviewCreationSeed } from "./ReviewWorkspace";
import {
  parseStudioRoute,
  type ProjectSection,
  type StudioRoute,
  studioRouteToHash,
} from "./lib/studio-route";
import {
  buildStudioTitle,
  setDocumentTitle,
  teamDisplayName,
} from "./lib/page-title";
import { useI18n } from "./lib/i18n-react";
import type { PersonalSettingsSection } from "./PersonalSettings";
import { nullableDescription, validateCatalogDraft } from "./lib/catalog";
import { UploadCenter, useUploadQueue } from "./UploadCenter";

const projectTabIds: ProjectSection[] = [
  "overview",
  "media",
  "reviews",
  "settings",
];

const lastProjectKey = "visto:last-project";
const legacyLastProjectKey = "framewise:last-project";
const MediaWorkspace = lazy(() =>
  import("./MediaWorkspace").then((module) => ({
    default: module.MediaWorkspace,
  })),
);
const ProjectReviewsWorkspace = lazy(() =>
  import("./ReviewWorkspace").then((module) => ({
    default: module.ProjectReviewsWorkspace,
  })),
);
const ProjectSettingsWorkspace = lazy(() =>
  import("./ProjectSettingsWorkspace").then((module) => ({
    default: module.ProjectSettingsWorkspace,
  })),
);
const NotificationCenter = lazy(() =>
  import("./NotificationCenter").then((module) => ({
    default: module.NotificationCenter,
  })),
);
const PersonalSettings = lazy(() =>
  import("./PersonalSettings").then((module) => ({
    default: module.PersonalSettings,
  })),
);
const OwnerSettingsWorkspace = lazy(() =>
  import("./OwnerSettingsWorkspace").then((module) => ({
    default: module.OwnerSettingsWorkspace,
  })),
);

export function StudioWorkspace({
  system,
  session,
  onSessionUpdated,
  onLogout,
}: {
  system: SystemInfo;
  session: SessionInfo;
  onSessionUpdated: (session: SessionInfo) => void;
  onLogout: () => Promise<void>;
}) {
  const { locale, t } = useI18n();
  const [route, setRoute] = useState<StudioRoute>(() => {
    const initial = parseStudioRoute(window.location.hash);
    if (window.location.hash.replace(/^#\/?/, "").startsWith("sources")) {
      window.history.replaceState(null, "", studioRouteToHash(initial));
    }
    return initial;
  });
  const [projects, setProjects] = useState<Project[]>([]);
  const [members, setMembers] = useState<ProjectMember[]>([]);
  const [projectsLoading, setProjectsLoading] = useState(true);
  const [projectsError, setProjectsError] = useState<string | null>(null);
  const [unreadCount, setUnreadCount] = useState(0);
  const [reviewSeed, setReviewSeed] = useState<ReviewCreationSeed | null>(null);
  const [profileOpen, setProfileOpen] = useState(false);
  const [personalSettingsOpen, setPersonalSettingsOpen] = useState(false);
  const [personalSettingsSection, setPersonalSettingsSection] =
    useState<PersonalSettingsSection>("profile");
  const [notificationsOpen, setNotificationsOpen] = useState(false);
  const [projectCreateOpen, setProjectCreateOpen] = useState(false);
  const [mobileNavOpen, setMobileNavOpen] = useState(false);
  const [logoutError, setLogoutError] = useState<string | null>(null);
  const uploadQueue = useUploadQueue();
  const initialRouteResolved = useRef(Boolean(window.location.hash));

  useEffect(() => {
    const syncRoute = () => {
      setRoute(parseStudioRoute(window.location.hash));
      setMobileNavOpen(false);
    };
    window.addEventListener("hashchange", syncRoute);
    return () => window.removeEventListener("hashchange", syncRoute);
  }, []);

  useEffect(() => {
    const controller = new AbortController();
    listProjects(controller.signal)
      .then((items) => {
        setProjects(items);
        setProjectsError(null);
        setProjectsLoading(false);
      })
      .catch((error: unknown) => {
        if (!isAbort(error)) {
          setProjectsError(
            error instanceof Error ? error.message : "无法读取项目",
          );
          setProjectsLoading(false);
        }
      });
    return () => controller.abort();
  }, []);

  useEffect(() => {
    const controller = new AbortController();
    listNotifications(controller.signal)
      .then((result) => setUnreadCount(result.unreadCount))
      .catch(() => setUnreadCount(0));
    return () => controller.abort();
  }, []);

  useEffect(() => {
    if (projectsLoading || initialRouteResolved.current) {
      return;
    }
    initialRouteResolved.current = true;
    const previousProjectId =
      window.localStorage.getItem(lastProjectKey) ??
      window.localStorage.getItem(legacyLastProjectKey);
    const previousProject = projects.find(
      (project) => project.id === previousProjectId,
    );
    navigate(
      previousProject
        ? {
            area: "project",
            projectId: previousProject.id,
            section: "overview",
          }
        : { area: "projects" },
    );
  }, [projects, projectsLoading]);

  const selectedProject =
    route.area === "project"
      ? (projects.find((project) => project.id === route.projectId) ?? null)
      : null;

  useEffect(() => {
    if (!selectedProject) {
      setMembers([]);
      return;
    }
    window.localStorage.setItem(lastProjectKey, selectedProject.id);
    const controller = new AbortController();
    listProjectMembers(selectedProject.id, controller.signal)
      .then(setMembers)
      .catch(() => setMembers([]));
    return () => controller.abort();
  }, [selectedProject?.id]);

  function navigate(nextRoute: StudioRoute) {
    const hash = studioRouteToHash(nextRoute);
    setProfileOpen(false);
    setNotificationsOpen(false);
    setProjectCreateOpen(false);
    setMobileNavOpen(false);
    if (window.location.hash === hash) {
      setRoute(nextRoute);
      return;
    }
    window.location.hash = hash;
  }

  function openProject(
    project: Project,
    section: ProjectSection = "overview",
    target: {
      reviewId?: string;
      threadId?: string;
      assetId?: string;
    } = {},
  ) {
    navigate({ area: "project", projectId: project.id, section, ...target });
  }

  function createReviewFromMedia(seed: ReviewCreationSeed) {
    setReviewSeed(seed);
    const project = projects.find((item) => item.id === seed.projectId);
    if (project) {
      openProject(project, "reviews");
      return;
    }
    setProjectsError("这个媒体所属的项目当前不可见");
    navigate({ area: "projects" });
  }

  const showOwner = session.role === "owner";
  const teamName = teamDisplayName(session.workspace);
  const titleOverlay = notificationsOpen
    ? "notifications"
    : personalSettingsOpen
      ? "personal"
      : null;

  useEffect(() => {
    setDocumentTitle(
      buildStudioTitle({
        route,
        teamName,
        projectName: selectedProject?.name ?? null,
        overlay: titleOverlay,
        locale,
      }),
    );
  }, [locale, route, selectedProject?.name, teamName, titleOverlay]);

  return (
    <div className="studio-shell">
      <header className="studio-mobile-header">
        <StudioBrand teamName={teamName} />
        <button
          className="studio-icon-button"
          type="button"
          aria-label={mobileNavOpen ? "Close navigation" : "Open navigation"}
          aria-expanded={mobileNavOpen}
          onClick={() => setMobileNavOpen((current) => !current)}
        >
          {mobileNavOpen ? <X size={19} /> : <Menu size={19} />}
        </button>
      </header>

      <aside
        className={`studio-sidebar${mobileNavOpen ? " is-mobile-open" : ""}`}
      >
        <StudioBrand teamName={teamName} />

        <nav className="studio-global-nav" aria-label="Global navigation">
          <button
            className={
              route.area === "projects" || route.area === "project"
                ? "studio-nav-item is-active"
                : "studio-nav-item"
            }
            type="button"
            onClick={() => navigate({ area: "projects" })}
          >
            <FolderKanban size={18} />
            <span>{t("nav.projects")}</span>
          </button>
          {showOwner ? (
            <button
              className={
                route.area === "owner"
                  ? "studio-nav-item is-active"
                  : "studio-nav-item"
              }
              type="button"
              onClick={() => navigate({ area: "owner", section: "accounts" })}
            >
              <Settings size={18} />
              <span>{t("nav.ownerSettings")}</span>
            </button>
          ) : null}
        </nav>

        <div className="studio-sidebar-footer">
          <button
            className="studio-utility-button"
            type="button"
            aria-expanded={notificationsOpen}
            onClick={() => {
              setProfileOpen(false);
              setNotificationsOpen(true);
              setMobileNavOpen(false);
            }}
          >
            <span className="studio-utility-icon">
              <Bell size={18} />
              {unreadCount > 0 ? (
                <span className="studio-notification-count">
                  {Math.min(unreadCount, 99)}
                </span>
              ) : null}
            </span>
            <span>{t("nav.notifications")}</span>
          </button>

          <div className="studio-profile">
            <button
              className="studio-profile-trigger"
              type="button"
              aria-expanded={profileOpen}
              onClick={() => setProfileOpen((current) => !current)}
            >
              <span className="studio-avatar" aria-hidden="true">
                {initials(session.user.displayName)}
              </span>
              <span className="studio-profile-copy">
                <strong>{session.user.displayName}</strong>
                <span>{roleLabel(session.role, t)}</span>
              </span>
              <ChevronDown size={15} />
            </button>

            {profileOpen ? (
              <div className="studio-profile-menu">
                <div>
                  <strong>{session.user.displayName}</strong>
                  <span>{teamName}</span>
                </div>
                <button
                  type="button"
                  onClick={() => {
                    setProfileOpen(false);
                    setMobileNavOpen(false);
                    setPersonalSettingsSection("profile");
                    setPersonalSettingsOpen(true);
                  }}
                >
                  <CircleUserRound size={16} />
                  {t("profile.profile")}
                </button>
                <button
                  type="button"
                  onClick={() => {
                    setProfileOpen(false);
                    setMobileNavOpen(false);
                    setPersonalSettingsSection("preferences");
                    setPersonalSettingsOpen(true);
                  }}
                >
                  <SlidersHorizontal size={16} />
                  {t("profile.preferences")}
                </button>
                <button
                  type="button"
                  onClick={() => {
                    setLogoutError(null);
                    void onLogout().catch((error: unknown) => {
                      setLogoutError(
                        error instanceof Error
                          ? error.message
                          : t("profile.signOutFailed"),
                      );
                    });
                  }}
                >
                  <LogOut size={16} />
                  {t("profile.signOut")}
                </button>
                {logoutError ? (
                  <p className="studio-menu-error" role="alert">
                    {logoutError}
                  </p>
                ) : null}
              </div>
            ) : null}
          </div>
        </div>
      </aside>

      <main className="studio-main">
        {route.area === "project" && selectedProject ? (
          <ProjectSurface
            project={selectedProject}
            projects={projects}
            members={members}
            session={session}
            section={route.section}
            reviewSeed={reviewSeed}
            initialReviewId={route.reviewId ?? null}
            initialThreadId={route.threadId ?? null}
            initialAssetId={route.assetId ?? null}
            onOpenProject={openProject}
            onOpenProjects={() => navigate({ area: "projects" })}
            onProjectUpdated={(updated) =>
              setProjects((current) =>
                current.map((item) =>
                  item.id === updated.id ? updated : item,
                ),
              )
            }
            onProjectDeleted={(projectId) => {
              setProjects((current) =>
                current.filter((item) => item.id !== projectId),
              );
              navigate({ area: "projects" });
            }}
            onMembersChanged={setMembers}
            onReviewSeedConsumed={() => setReviewSeed(null)}
            onCreateReview={createReviewFromMedia}
            uploadRevision={
              uploadQueue.completedByProject[selectedProject.id] ?? 0
            }
            onQueueUploads={(files) =>
              uploadQueue.enqueue(
                selectedProject.id,
                selectedProject.name,
                files,
              )
            }
          />
        ) : route.area === "project" && projectsLoading ? (
          <StudioLoading label="正在打开项目" />
        ) : route.area === "project" ? (
          <MissingProject
            onOpenProjects={() => navigate({ area: "projects" })}
          />
        ) : route.area === "owner" && showOwner ? (
          <Suspense fallback={<StudioLoading label="正在打开 Owner 设置" />}>
            <OwnerSettingsWorkspace
              system={system}
              session={session}
              section={route.section}
              onSessionUpdated={onSessionUpdated}
              onSectionChange={(section) =>
                navigate({ area: "owner", section })
              }
            />
          </Suspense>
        ) : (
          <ProjectList
            projects={projects}
            loading={projectsLoading}
            error={projectsError}
            onOpenProject={openProject}
            canCreate={session.role === "owner" || session.role === "admin"}
            onCreate={() => setProjectCreateOpen(true)}
          />
        )}
      </main>

      <UploadCenter queue={uploadQueue} />

      <nav className="studio-mobile-nav" aria-label="Mobile navigation">
        <button
          className={
            route.area === "projects" || route.area === "project"
              ? "is-active"
              : ""
          }
          type="button"
          onClick={() => navigate({ area: "projects" })}
        >
          <FolderKanban size={19} />
          <span>{t("nav.projects")}</span>
        </button>
        <button
          type="button"
          aria-label={t("nav.notifications")}
          onClick={() => {
            setProfileOpen(false);
            setNotificationsOpen(true);
            setMobileNavOpen(false);
          }}
        >
          <Bell size={19} />
          <span>{t("nav.notifications")}</span>
        </button>
        <button
          type="button"
          onClick={() => {
            setMobileNavOpen(true);
            setProfileOpen(true);
          }}
        >
          <CircleUserRound size={19} />
          <span>{t("nav.mine")}</span>
        </button>
      </nav>

      <Suspense fallback={null}>
        <NotificationCenter
          open={notificationsOpen}
          onClose={() => setNotificationsOpen(false)}
          onUnreadCountChange={setUnreadCount}
          onOpenReview={(review) => {
            const project = projects.find(
              (candidate) => candidate.id === review.projectId,
            );
            if (project) {
              openProject(project, "reviews", { reviewId: review.id });
              return;
            }
            setProjectsError("通知对应的项目当前不可见");
            navigate({ area: "projects" });
          }}
        />
      </Suspense>
      <Suspense fallback={null}>
        <PersonalSettings
          open={personalSettingsOpen}
          initialSection={personalSettingsSection}
          session={session}
          onClose={() => setPersonalSettingsOpen(false)}
          onSessionUpdated={onSessionUpdated}
        />
      </Suspense>
      <CreateProjectDialog
        open={projectCreateOpen}
        onClose={() => setProjectCreateOpen(false)}
        onOpenStorageSettings={() => {
          setProjectCreateOpen(false);
          navigate({ area: "owner", section: "storage" });
        }}
        onCreated={(project) => {
          setProjects((current) => [project, ...current]);
          setProjectCreateOpen(false);
          openProject(project);
        }}
      />
    </div>
  );
}

function StudioBrand({ teamName }: { teamName: string }) {
  return (
    <div className="studio-brand" aria-label="Visto Studio">
      <span className="studio-brand-mark" aria-hidden="true">
        <VistoMark className="visto-mark" />
      </span>
      <span>
        <strong>{PRODUCT_NAME_EN}</strong>
        <small>{teamName}</small>
      </span>
    </div>
  );
}

function ProjectList({
  projects,
  loading,
  error,
  onOpenProject,
  canCreate,
  onCreate,
}: {
  projects: Project[];
  loading: boolean;
  error: string | null;
  onOpenProject: (project: Project) => void;
  canCreate: boolean;
  onCreate: () => void;
}) {
  const { t, formatRelativeDate } = useI18n();
  return (
    <div className="studio-page">
      <header className="studio-page-header">
        <div>
          <p className="studio-kicker">PROJECTS</p>
          <h1>{t("projectList.title")}</h1>
        </div>
        {canCreate ? (
          <button className="primary-button" type="button" onClick={onCreate}>
            <Plus size={17} />
            {t("projectList.create")}
          </button>
        ) : null}
      </header>

      {error ? (
        <p className="catalog-error" role="alert">
          {error}
        </p>
      ) : loading ? (
        <StudioLoading label={t("projectList.loading")} />
      ) : projects.length === 0 ? (
        <section className="studio-empty">
          <FolderKanban size={24} />
          <h2>
            {canCreate
              ? t("projectList.emptyForOwner")
              : t("projectList.emptyForMember")}
          </h2>
          <p>
            {canCreate
              ? t("projectList.emptyOwnerDetail")
              : t("projectList.emptyMemberDetail")}
          </p>
          {canCreate ? (
            <button className="primary-button" type="button" onClick={onCreate}>
              <Plus size={17} />
              {t("projectList.create")}
            </button>
          ) : null}
        </section>
      ) : (
        <section
          className="studio-project-list"
          aria-label={t("projectList.title")}
        >
          <div className="studio-list-heading">
            <span>{t("projectList.title")}</span>
            <span>{t("projectList.content")}</span>
            <span>{t("projectList.recentUpdate")}</span>
          </div>
          {projects.map((project, index) => (
            <button
              className="studio-project-row"
              type="button"
              key={project.id}
              onClick={() => onOpenProject(project)}
            >
              <span className="studio-project-index" aria-hidden="true">
                {`${index + 1}`.padStart(2, "0")}
              </span>
              <span className="studio-project-identity">
                <strong>{project.name}</strong>
                <small>
                  {project.description || t("project.noDescription")}
                </small>
              </span>
              <span className="studio-project-content">
                {t("project.assetCount", { count: project.assetCount })} ·{" "}
                {t("project.collectionCount", {
                  count: project.collectionCount,
                })}
              </span>
              <time dateTime={project.updatedAt}>
                {formatRelativeDate(project.updatedAt)}
              </time>
            </button>
          ))}
        </section>
      )}
    </div>
  );
}

function CreateProjectDialog({
  open,
  onClose,
  onOpenStorageSettings,
  onCreated,
}: {
  open: boolean;
  onClose: () => void;
  onOpenStorageSettings: () => void;
  onCreated: (project: Project) => void;
}) {
  const { t } = useI18n();
  const [name, setName] = useState("");
  const [description, setDescription] = useState("");
  const [storageGrants, setStorageGrants] = useState<ProjectStorageGrant[]>([]);
  const [storageGrantId, setStorageGrantId] = useState("");
  const [storageLoading, setStorageLoading] = useState(false);
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState<string | null>(null);

  useEffect(() => {
    if (!open) return;
    setName("");
    setDescription("");
    setStorageGrants([]);
    setStorageGrantId("");
    setStorageLoading(true);
    setError(null);
    const controller = new AbortController();
    void listProjectCreationStorageGrants(controller.signal)
      .then((items) => {
        setStorageGrants(items);
        setStorageGrantId(items.length === 1 ? (items[0]?.id ?? "") : "");
      })
      .catch((loadError) => {
        if (controller.signal.aborted) return;
        setError(
          loadError instanceof Error
            ? loadError.message
            : t("projectCreate.storageLoadFailed"),
        );
      })
      .finally(() => {
        if (!controller.signal.aborted) setStorageLoading(false);
      });
    return () => controller.abort();
  }, [open, t]);

  useEffect(() => {
    if (!open) return;
    const handleKeyDown = (event: KeyboardEvent) => {
      if (event.key === "Escape" && !busy) onClose();
    };
    window.addEventListener("keydown", handleKeyDown);
    return () => window.removeEventListener("keydown", handleKeyDown);
  }, [busy, onClose, open]);

  async function submit(event: FormEvent<HTMLFormElement>) {
    event.preventDefault();
    const validationError = validateCatalogDraft({ name, description });
    if (validationError) {
      setError(validationError);
      return;
    }
    if (!storageGrantId) {
      setError(t("projectCreate.storageRequired"));
      return;
    }
    setBusy(true);
    setError(null);
    try {
      const project = await createProject({
        name: name.trim(),
        description: nullableDescription(description),
        storageGrantId,
      });
      onCreated(project);
    } catch (createError) {
      setError(
        createError instanceof Error ? createError.message : "无法创建项目",
      );
    } finally {
      setBusy(false);
    }
  }

  if (!open) return null;

  return (
    <div className="studio-create-layer" role="presentation">
      <button
        className="studio-create-scrim"
        type="button"
        aria-label={t("projectCreate.close")}
        disabled={busy}
        onClick={onClose}
      />
      <form
        className="studio-create-dialog"
        role="dialog"
        aria-modal="true"
        aria-labelledby="studio-create-project-title"
        onSubmit={submit}
      >
        <header>
          <div>
            <span className="studio-notification-kicker">NEW PROJECT</span>
            <h2 id="studio-create-project-title">{t("projectCreate.title")}</h2>
          </div>
          <button
            className="studio-icon-button"
            type="button"
            aria-label={t("projectCreate.close")}
            disabled={busy}
            onClick={onClose}
          >
            <X size={18} />
          </button>
        </header>
        <div className="studio-create-fields">
          <label>
            <span>{t("projectCreate.name")}</span>
            <input
              value={name}
              maxLength={120}
              autoFocus
              placeholder={t("projectCreate.namePlaceholder")}
              disabled={busy}
              onChange={(event) => setName(event.target.value)}
            />
          </label>
          <label>
            <span>{t("projectCreate.storage")}</span>
            <select
              value={storageGrantId}
              disabled={busy || storageLoading || storageGrants.length === 0}
              onChange={(event) => setStorageGrantId(event.target.value)}
            >
              <option value="">
                {storageLoading
                  ? t("projectCreate.storageLoading")
                  : storageGrants.length === 0
                    ? t("projectCreate.storageEmpty")
                    : t("projectCreate.storagePlaceholder")}
              </option>
              {storageGrants.map((grant) => (
                <option key={grant.id} value={grant.id}>
                  {projectCreationStorageLabel(grant)}
                </option>
              ))}
            </select>
            {!storageLoading && storageGrants.length === 0 ? (
              <button
                className="text-button"
                type="button"
                disabled={busy}
                onClick={onOpenStorageSettings}
              >
                {t("projectCreate.addStorage")}
              </button>
            ) : null}
          </label>
          <label>
            <span>{t("projectCreate.description")}</span>
            <textarea
              value={description}
              maxLength={2000}
              rows={4}
              placeholder={t("projectCreate.descriptionPlaceholder")}
              disabled={busy}
              onChange={(event) => setDescription(event.target.value)}
            />
          </label>
        </div>
        {error ? (
          <p className="inline-feedback is-error" role="alert">
            {error}
          </p>
        ) : null}
        <footer>
          <button
            className="secondary-button"
            type="button"
            disabled={busy}
            onClick={onClose}
          >
            {t("common.cancel")}
          </button>
          <button
            className="primary-button"
            type="submit"
            disabled={busy || storageLoading || !storageGrantId}
          >
            {busy ? t("projectCreate.creating") : t("projectList.create")}
          </button>
        </footer>
      </form>
    </div>
  );
}

function projectCreationStorageLabel(grant: ProjectStorageGrant) {
  return `${grant.providerName}${grant.rootName ? ` · ${grant.rootName}` : ""}`;
}

function ProjectSurface({
  project,
  projects,
  members,
  session,
  section,
  reviewSeed,
  initialReviewId,
  initialThreadId,
  initialAssetId,
  onOpenProject,
  onOpenProjects,
  onProjectUpdated,
  onProjectDeleted,
  onMembersChanged,
  onReviewSeedConsumed,
  onCreateReview,
  uploadRevision,
  onQueueUploads,
}: {
  project: Project;
  projects: Project[];
  members: ProjectMember[];
  session: SessionInfo;
  section: ProjectSection;
  reviewSeed: ReviewCreationSeed | null;
  initialReviewId: string | null;
  initialThreadId: string | null;
  initialAssetId: string | null;
  onOpenProject: (
    project: Project,
    section?: ProjectSection,
    target?: {
      reviewId?: string;
      threadId?: string;
      assetId?: string;
    },
  ) => void;
  onOpenProjects: () => void;
  onProjectUpdated: (project: Project) => void;
  onProjectDeleted: (projectId: string) => void;
  onMembersChanged: (members: ProjectMember[]) => void;
  onReviewSeedConsumed: () => void;
  onCreateReview: (seed: ReviewCreationSeed) => void;
  uploadRevision: number;
  onQueueUploads: (files: FileList | File[]) => void;
}) {
  const { t, formatRelativeDate } = useI18n();
  const [settingsInitialSection, setSettingsInitialSection] =
    useState<ProjectSettingsSection>("general");
  const [projectSwitchOpen, setProjectSwitchOpen] = useState(false);
  const projectSwitchRef = useRef<HTMLDivElement>(null);
  const activeIndex = Math.max(
    projects.findIndex((candidate) => candidate.id === project.id),
    0,
  );
  const currentProjectMember = members.find(
    (member) => member.userId === session.user.id && member.status === "active",
  );
  const canCreateReviews =
    session.role === "owner" ||
    Boolean(currentProjectMember?.permissions["reviews.create"]);
  const canCreateShares =
    session.role === "owner" ||
    Boolean(currentProjectMember?.permissions["shares.create"]);
  const canManageMembers =
    session.role === "owner" ||
    Boolean(currentProjectMember?.permissions["project.members.manage"]);

  useEffect(() => {
    setProjectSwitchOpen(false);
  }, [project.id]);

  useEffect(() => {
    if (!projectSwitchOpen) return;
    function handlePointerDown(event: PointerEvent) {
      const target = event.target;
      if (
        target instanceof Node &&
        projectSwitchRef.current?.contains(target)
      ) {
        return;
      }
      setProjectSwitchOpen(false);
    }
    function handleKeyDown(event: KeyboardEvent) {
      if (event.key === "Escape") {
        setProjectSwitchOpen(false);
      }
    }
    window.addEventListener("pointerdown", handlePointerDown);
    window.addEventListener("keydown", handleKeyDown);
    return () => {
      window.removeEventListener("pointerdown", handlePointerDown);
      window.removeEventListener("keydown", handleKeyDown);
    };
  }, [projectSwitchOpen]);

  return (
    <div className="studio-project-surface">
      <header className="studio-project-header">
        <div className="studio-project-ribbon" aria-hidden="true">
          <span>{`${activeIndex + 1}`.padStart(2, "0")}</span>
          <i />
        </div>
        <div className="studio-project-title">
          <button
            className="studio-back-link"
            type="button"
            onClick={onOpenProjects}
          >
            {t("project.allProjects")}
          </button>
          <div className="studio-project-switch" ref={projectSwitchRef}>
            <button
              className="studio-project-switch-trigger"
              type="button"
              aria-label={t("project.switch")}
              aria-haspopup="listbox"
              aria-expanded={projectSwitchOpen}
              onClick={() => setProjectSwitchOpen((current) => !current)}
            >
              <span>{project.name}</span>
              <ChevronDown size={18} aria-hidden="true" />
            </button>
            {projectSwitchOpen ? (
              <div className="studio-project-switch-menu" role="listbox">
                {projects.map((candidate, index) => {
                  const selected = candidate.id === project.id;
                  return (
                    <button
                      className={selected ? "is-selected" : ""}
                      type="button"
                      role="option"
                      aria-selected={selected}
                      key={candidate.id}
                      onClick={() => {
                        setProjectSwitchOpen(false);
                        if (!selected) {
                          onOpenProject(candidate, section);
                        }
                      }}
                    >
                      <span className="studio-project-switch-number">
                        {`${index + 1}`.padStart(2, "0")}
                      </span>
                      <span className="studio-project-switch-option">
                        <strong>{candidate.name}</strong>
                        <small>
                          {candidate.status === "active"
                            ? t("project.active")
                            : t("project.archived")}{" "}
                          ·{" "}
                          {t("project.assetCount", {
                            count: candidate.assetCount,
                          })}
                        </small>
                      </span>
                    </button>
                  );
                })}
              </div>
            ) : null}
          </div>
          <div className="studio-project-meta">
            <span className={`studio-status is-${project.status}`}>
              {project.status === "active"
                ? t("project.active")
                : t("project.archived")}
            </span>
            <span>
              {t("project.assetCount", { count: project.assetCount })}
            </span>
            <span>
              {t("common.updatedAt", {
                date: formatRelativeDate(project.updatedAt),
              })}
            </span>
          </div>
        </div>

        <div className="studio-project-actions">
          <button
            className="secondary-button"
            type="button"
            onClick={() => onOpenProject(project, "media")}
          >
            <Plus size={16} />
            {t("project.addMedia")}
          </button>
          <button
            className="primary-button"
            type="button"
            onClick={() => onOpenProject(project, "reviews")}
          >
            <Film size={16} />
            {t("project.createReview")}
          </button>
          <MemberStack
            members={members}
            canManage={canManageMembers}
            onClick={() => {
              setSettingsInitialSection("access");
              onOpenProject(project, "settings");
            }}
          />
        </div>
      </header>

      <nav className="studio-project-tabs" aria-label="Project sections">
        {projectTabIds.map((tabId) => (
          <button
            className={tabId === section ? "is-active" : ""}
            type="button"
            key={tabId}
            aria-current={tabId === section ? "page" : undefined}
            onClick={() => {
              if (tabId === "settings") {
                setSettingsInitialSection("general");
              }
              onOpenProject(project, tabId);
            }}
          >
            {projectSectionLabel(tabId, t)}
          </button>
        ))}
      </nav>

      <div className="studio-project-body">
        {section === "media" ? (
          <Suspense fallback={<StudioLoading label="正在加载项目媒体" />}>
            <MediaWorkspace
              project={project}
              initialAssetId={initialAssetId}
              onCreateReview={onCreateReview}
              uploadRevision={uploadRevision}
              onQueueUploads={onQueueUploads}
            />
          </Suspense>
        ) : section === "reviews" ? (
          <Suspense fallback={<StudioLoading label="正在加载项目审阅" />}>
            <ProjectReviewsWorkspace
              project={project}
              members={members}
              canCreateReviews={canCreateReviews}
              canCreateShares={canCreateShares}
              seed={reviewSeed}
              initialReviewId={initialReviewId}
              initialThreadId={initialThreadId}
              onSeedConsumed={onReviewSeedConsumed}
              onOpenMedia={() => onOpenProject(project, "media")}
            />
          </Suspense>
        ) : section === "settings" ? (
          <Suspense fallback={<StudioLoading label="正在加载项目设置" />}>
            <ProjectSettingsWorkspace
              project={project}
              initialMembers={members}
              session={session}
              onProjectUpdated={onProjectUpdated}
              onProjectDeleted={onProjectDeleted}
              onMembersChanged={onMembersChanged}
              initialSection={settingsInitialSection}
            />
          </Suspense>
        ) : (
          <ProjectOverviewPanel
            project={project}
            onOpenReview={(reviewId, threadId) =>
              onOpenProject(project, "reviews", {
                reviewId,
                ...(threadId ? { threadId } : {}),
              })
            }
            onOpenReviews={() => onOpenProject(project, "reviews")}
            onOpenMedia={(assetId) =>
              onOpenProject(project, "media", {
                ...(assetId ? { assetId } : {}),
              })
            }
          />
        )}
      </div>
    </div>
  );
}

function MemberStack({
  members,
  canManage,
  onClick,
}: {
  members: ProjectMember[];
  canManage: boolean;
  onClick: () => void;
}) {
  const { t } = useI18n();
  const visibleMembers = members.slice(0, 3);
  return (
    <button
      className="studio-member-stack"
      type="button"
      aria-label={
        canManage ? t("project.manageMembers") : t("project.viewMembers")
      }
      onClick={onClick}
    >
      {visibleMembers.length > 0 ? (
        visibleMembers.map((member) => (
          <span
            className="studio-avatar"
            title={member.displayName}
            key={member.id}
          >
            {initials(member.displayName)}
          </span>
        ))
      ) : (
        <span className="studio-avatar">
          <UsersRound size={15} />
        </span>
      )}
      {members.length > visibleMembers.length ? (
        <span className="studio-member-more">
          +{members.length - visibleMembers.length}
        </span>
      ) : null}
    </button>
  );
}

function ProjectReviewSkeleton({ onOpen }: { onOpen: () => void }) {
  const { t } = useI18n();
  return (
    <section className="studio-work-section">
      <div className="studio-section-heading">
        <div>
          <p className="studio-kicker">PROJECT REVIEWS</p>
          <h2>{t("project.reviews")}</h2>
        </div>
        <button className="primary-button" type="button" onClick={onOpen}>
          <Film size={17} />
          {t("projectReview.title")}
        </button>
      </div>
      <div className="studio-migration-row">
        <SlidersHorizontal size={20} />
        <div>
          <strong>审阅工作面</strong>
        </div>
      </div>
    </section>
  );
}

function StudioLoading({ label }: { label: string }) {
  return (
    <div className="studio-loading" aria-label={label} aria-live="polite">
      <span />
      <span />
      <span />
    </div>
  );
}

function MissingProject({ onOpenProjects }: { onOpenProjects: () => void }) {
  const { t } = useI18n();
  return (
    <section className="studio-empty">
      <FolderKanban size={24} />
      <h1>{t("project.missingTitle")}</h1>
      <p>{t("project.missingDescription")}</p>
      <button className="primary-button" type="button" onClick={onOpenProjects}>
        {t("project.returnToList")}
      </button>
    </section>
  );
}

function roleLabel(role: string, t: ReturnType<typeof useI18n>["t"]) {
  switch (role) {
    case "owner":
      return "Owner";
    case "admin":
      return t("role.admin");
    case "member":
      return t("role.member");
    case "guest":
      return t("role.guest");
    default:
      return role;
  }
}

function projectSectionLabel(
  section: ProjectSection,
  t: ReturnType<typeof useI18n>["t"],
) {
  switch (section) {
    case "overview":
      return t("project.overview");
    case "media":
      return t("project.media");
    case "reviews":
      return t("project.reviews");
    case "settings":
      return t("project.settings");
  }
}

function initials(value: string) {
  return value.trim().slice(0, 2).toUpperCase() || "U";
}

function isAbort(error: unknown) {
  return error instanceof DOMException && error.name === "AbortError";
}
