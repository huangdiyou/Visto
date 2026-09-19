import { type FormEvent, useEffect, useMemo, useState } from "react";
import type {
  AuditLog,
  Membership,
  Project,
  ProjectMember,
  ProjectMemberInput,
  ProjectStorageGrant,
  ProjectStorageSelection,
  SessionInfo,
} from "@review-studio/contracts";
import {
  Activity,
  Archive,
  Crown,
  HardDrive,
  KeyRound,
  RotateCcw,
  Save,
  ShieldCheck,
  Trash2,
  UserPlus,
  UsersRound,
} from "lucide-react";
import {
  addProjectMember,
  archiveProject,
  createProjectGuest,
  deleteProject,
  listProjectMembers,
  removeProjectMember,
  restoreProject,
  transferProject,
  updateProject,
  updateProjectMember,
} from "./api/catalog";
import { listProjectActivity } from "./api/activity";
import { listMembers } from "./api/members";
import {
  listAvailableProjectStorageGrants,
  listProjectStorageSelections,
  selectProjectStorage,
} from "./api/storage";

export type ProjectSettingsSection =
  | "general"
  | "access"
  | "storage"
  | "activity"
  | "danger";
type Notice = { tone: "success" | "error"; message: string } | null;
type ProjectStoragePurpose = ProjectStorageSelection["purpose"];
type AddMode = "member" | "guest";

interface ProjectMemberDraft {
  userId: string;
  roleKey: "supervisor" | "member";
  permissions: Record<string, boolean>;
  expiresAt: string;
}

interface ProjectGuestDraft {
  displayName: string;
  email: string;
  permissions: Record<string, boolean>;
  expiresAt: string;
}

interface ProjectTransferDraft {
  newPrimaryUserId: string;
  formerOwnerAction: "keep_supervisor" | "demote_member" | "leave";
}

const SETTINGS_SECTIONS: Array<{
  id: ProjectSettingsSection;
  label: string;
  description: string;
  icon: typeof Activity;
}> = [
  {
    id: "general",
    label: "基本信息",
    description: "名称、说明和当前状态",
    icon: KeyRound,
  },
  {
    id: "access",
    label: "成员与权限",
    description: "项目主管、项目成员和临时账户",
    icon: UsersRound,
  },
  {
    id: "storage",
    label: "项目存储",
    description: "上传、预览和归档落点",
    icon: HardDrive,
  },
  {
    id: "activity",
    label: "项目活动",
    description: "成员与设置变更记录",
    icon: Activity,
  },
  {
    id: "danger",
    label: "危险操作",
    description: "归档、恢复和删除项目",
    icon: ShieldCheck,
  },
];

const PROJECT_PERMISSION_OPTIONS = [
  { id: "project.read", label: "查看项目" },
  { id: "project.manage", label: "管理项目" },
  { id: "project.members.manage", label: "管理成员" },
  { id: "assets.add", label: "添加媒体" },
  { id: "assets.upload", label: "上传版本" },
  { id: "assets.remove", label: "删除媒体" },
  { id: "reviews.create", label: "创建审阅" },
  { id: "reviews.comment", label: "发表评论" },
  { id: "reviews.decide", label: "提交结论" },
  { id: "reviews.delete", label: "删除审阅" },
  { id: "shares.create", label: "创建分享" },
] as const;

const PROJECT_STORAGE_PURPOSES: Array<{
  id: ProjectStoragePurpose;
  label: string;
  description: string;
}> = [
  {
    id: "upload",
    label: "上传存储",
    description: "项目成员上传新媒体和新版本时使用。",
  },
  {
    id: "default_rendition",
    label: "预览存储",
    description: "代理视频、海报、缩略图和其他预览产物使用。",
  },
  {
    id: "archive",
    label: "归档存储",
    description: "项目完成后的长期保留位置。",
  },
];

const EMPTY_STORAGE_SELECTIONS: Record<ProjectStoragePurpose, string> = {
  upload: "",
  default_rendition: "",
  archive: "",
};

export function ProjectSettingsWorkspace({
  project,
  initialMembers,
  session,
  onProjectUpdated,
  onProjectDeleted,
  onMembersChanged,
  initialSection = "general",
}: {
  project: Project;
  initialMembers: ProjectMember[];
  session: SessionInfo;
  onProjectUpdated: (project: Project) => void;
  onProjectDeleted: (projectId: string) => void;
  onMembersChanged: (members: ProjectMember[]) => void;
  initialSection?: ProjectSettingsSection;
}) {
  const [section, setSection] =
    useState<ProjectSettingsSection>(initialSection);
  const [members, setMembers] = useState<ProjectMember[]>(initialMembers);
  const [workspaceMembers, setWorkspaceMembers] = useState<Membership[]>([]);
  const [grants, setGrants] = useState<ProjectStorageGrant[]>([]);
  const [selections, setSelections] = useState<ProjectStorageSelection[]>([]);
  const [selectionDrafts, setSelectionDrafts] = useState<
    Record<ProjectStoragePurpose, string>
  >(EMPTY_STORAGE_SELECTIONS);
  const [activity, setActivity] = useState<AuditLog[]>([]);
  const [loading, setLoading] = useState(true);
  const [busy, setBusy] = useState(false);
  const [notice, setNotice] = useState<Notice>(null);
  const [name, setName] = useState(project.name);
  const [description, setDescription] = useState(project.description ?? "");
  const [addMode, setAddMode] = useState<AddMode>("member");
  const [memberDraft, setMemberDraft] =
    useState<ProjectMemberDraft>(defaultMemberDraft());
  const [guestDraft, setGuestDraft] =
    useState<ProjectGuestDraft>(defaultGuestDraft());
  const [transferDraft, setTransferDraft] = useState<ProjectTransferDraft>({
    newPrimaryUserId: "",
    formerOwnerAction: "keep_supervisor",
  });
  const [deleteConfirmation, setDeleteConfirmation] = useState("");

  const currentMember = useMemo(
    () =>
      members.find(
        (item) => item.userId === session.user.id && item.status === "active",
      ) ?? null,
    [members, session.user.id],
  );
  const currentPermissions = currentMember
    ? permissionsForMember(currentMember)
    : {};
  const isOwner = session.role === "owner";
  const canManageProject =
    isOwner || Boolean(currentPermissions["project.manage"]);

  useEffect(() => {
    setMembers(initialMembers);
  }, [initialMembers]);

  useEffect(() => {
    setName(project.name);
    setDescription(project.description ?? "");
    setDeleteConfirmation("");
  }, [project.id, project.name, project.description]);

  useEffect(() => {
    setSection(initialSection);
  }, [initialSection]);

  useEffect(() => {
    const controller = new AbortController();
    setLoading(true);
    setNotice(null);
    Promise.all([
      listProjectMembers(project.id, controller.signal),
      listMembers(controller.signal).catch(() => []),
      canManageProject
        ? listAvailableProjectStorageGrants(project.id, controller.signal)
        : Promise.resolve([]),
      listProjectStorageSelections(project.id, controller.signal),
      listProjectActivity(project.id, controller.signal),
    ])
      .then(
        ([
          memberItems,
          accountItems,
          grantItems,
          selectionItems,
          activityItems,
        ]) => {
          setMembers(memberItems);
          onMembersChanged(memberItems);
          setWorkspaceMembers(accountItems);
          setGrants(grantItems);
          setSelections(selectionItems);
          setSelectionDrafts(selectionValues(selectionItems));
          setActivity(activityItems);
        },
      )
      .catch((error: unknown) => {
        if (!isAbort(error)) {
          setNotice({
            tone: "error",
            message: errorMessage(error, "无法读取项目设置"),
          });
        }
      })
      .finally(() => setLoading(false));
    return () => controller.abort();
  }, [canManageProject, project.id]);
  const canManageMembers =
    isOwner || Boolean(currentPermissions["project.members.manage"]);
  const canTransfer = isOwner || project.primaryOwnerUserId === session.user.id;
  const activeMembers = members.filter((item) => item.status === "active");
  const primaryMember = members.find(
    (item) => item.userId === project.primaryOwnerUserId,
  );
  const activeUserIds = new Set(
    members
      .filter((item) => item.status !== "removed")
      .map((item) => item.userId),
  );
  const visibleAccounts = workspaceMembers.filter(
    (item) => item.status === "active" && (isOwner || item.role !== "owner"),
  );
  const addableMembers = visibleAccounts.filter(
    (item) => !activeUserIds.has(item.userId),
  );
  const transferCandidates = visibleAccounts.filter(
    (item) => item.userId !== project.primaryOwnerUserId,
  );

  useEffect(() => {
    setMemberDraft((current) => ({
      ...current,
      userId:
        current.userId &&
        addableMembers.some((item) => item.userId === current.userId)
          ? current.userId
          : (addableMembers[0]?.userId ?? ""),
    }));
    setTransferDraft((current) => ({
      ...current,
      newPrimaryUserId:
        current.newPrimaryUserId &&
        transferCandidates.some(
          (item) => item.userId === current.newPrimaryUserId,
        )
          ? current.newPrimaryUserId
          : (transferCandidates[0]?.userId ?? ""),
    }));
  }, [project.id, workspaceMembers, members]);

  async function reloadMembers() {
    const items = await listProjectMembers(project.id);
    setMembers(items);
    onMembersChanged(items);
  }

  async function reloadActivity() {
    setActivity(await listProjectActivity(project.id));
  }

  async function saveGeneral(event: FormEvent<HTMLFormElement>) {
    event.preventDefault();
    if (!name.trim()) {
      setNotice({ tone: "error", message: "项目名称不能为空" });
      return;
    }
    setBusy(true);
    setNotice(null);
    try {
      const updated = await updateProject(project.id, {
        name: name.trim(),
        description: description.trim() || null,
        revision: project.revision,
      });
      onProjectUpdated(updated);
      setNotice({ tone: "success", message: "项目基本信息已保存" });
      await reloadActivity();
    } catch (error) {
      setNotice({
        tone: "error",
        message: errorMessage(error, "无法保存项目基本信息"),
      });
    } finally {
      setBusy(false);
    }
  }

  async function addMember(event: FormEvent<HTMLFormElement>) {
    event.preventDefault();
    if (!memberDraft.userId) {
      setNotice({ tone: "error", message: "请选择要加入项目的账号" });
      return;
    }
    setBusy(true);
    setNotice(null);
    try {
      await addProjectMember(project.id, {
        userId: memberDraft.userId,
        roleKey: memberDraft.roleKey,
        permissions: normalizedPermissions(memberDraft.permissions),
        expiresAt: dateToISO(memberDraft.expiresAt),
      });
      await Promise.all([reloadMembers(), reloadActivity()]);
      setMemberDraft(defaultMemberDraft());
      setNotice({ tone: "success", message: "成员已加入当前项目" });
    } catch (error) {
      setNotice({
        tone: "error",
        message: errorMessage(error, "无法添加项目成员"),
      });
    } finally {
      setBusy(false);
    }
  }

  async function createGuest(event: FormEvent<HTMLFormElement>) {
    event.preventDefault();
    if (!guestDraft.displayName.trim()) {
      setNotice({ tone: "error", message: "请填写临时账户显示名称" });
      return;
    }
    setBusy(true);
    setNotice(null);
    try {
      await createProjectGuest(project.id, {
        displayName: guestDraft.displayName.trim(),
        email: nullableText(guestDraft.email),
        permissions: normalizedPermissions(guestDraft.permissions),
        expiresAt: dateToISO(guestDraft.expiresAt),
      });
      await Promise.all([reloadMembers(), reloadActivity()]);
      setGuestDraft(defaultGuestDraft());
      setNotice({
        tone: "success",
        message: "项目临时账户已创建。",
      });
    } catch (error) {
      setNotice({
        tone: "error",
        message: errorMessage(error, "无法创建临时账户"),
      });
    } finally {
      setBusy(false);
    }
  }

  async function saveMember(item: ProjectMember, input: ProjectMemberInput) {
    setBusy(true);
    setNotice(null);
    try {
      await updateProjectMember(item, input);
      await Promise.all([reloadMembers(), reloadActivity()]);
      setNotice({ tone: "success", message: "成员角色与权限已保存" });
    } catch (error) {
      setNotice({
        tone: "error",
        message: errorMessage(error, "无法保存成员权限"),
      });
    } finally {
      setBusy(false);
    }
  }

  async function removeMember(item: ProjectMember) {
    setBusy(true);
    setNotice(null);
    try {
      await removeProjectMember(item);
      await Promise.all([reloadMembers(), reloadActivity()]);
      setNotice({ tone: "success", message: "成员已从当前项目移除" });
    } catch (error) {
      setNotice({
        tone: "error",
        message: errorMessage(error, "无法移除项目成员"),
      });
    } finally {
      setBusy(false);
    }
  }

  async function submitTransfer(event: FormEvent<HTMLFormElement>) {
    event.preventDefault();
    if (!transferDraft.newPrimaryUserId) {
      setNotice({ tone: "error", message: "请选择新的项目主负责人" });
      return;
    }
    setBusy(true);
    setNotice(null);
    try {
      await transferProject(project.id, transferDraft);
      await Promise.all([reloadMembers(), reloadActivity()]);
      setNotice({ tone: "success", message: "项目主负责人已转交" });
    } catch (error) {
      setNotice({
        tone: "error",
        message: errorMessage(error, "无法转交项目主负责人"),
      });
    } finally {
      setBusy(false);
    }
  }

  async function saveStorage(purpose: ProjectStoragePurpose) {
    const grantId = selectionDrafts[purpose];
    if (!grantId) {
      setNotice({ tone: "error", message: "请选择项目要使用的存储" });
      return;
    }
    setBusy(true);
    setNotice(null);
    try {
      const updated = await selectProjectStorage(project.id, purpose, {
        grantId,
      });
      setSelections((current) => [
        updated,
        ...current.filter((item) => item.purpose !== purpose),
      ]);
      setNotice({ tone: "success", message: "项目存储选择已保存" });
      await reloadActivity();
    } catch (error) {
      setNotice({
        tone: "error",
        message: errorMessage(error, "无法保存项目存储"),
      });
    } finally {
      setBusy(false);
    }
  }

  async function toggleArchive() {
    setBusy(true);
    setNotice(null);
    try {
      const updated =
        project.status === "active"
          ? await archiveProject(project)
          : await restoreProject(project);
      onProjectUpdated(updated);
      setNotice({
        tone: "success",
        message:
          updated.status === "archived" ? "项目已归档" : "项目已恢复为进行中",
      });
      await reloadActivity();
    } catch (error) {
      setNotice({
        tone: "error",
        message: errorMessage(error, "无法更新项目状态"),
      });
    } finally {
      setBusy(false);
    }
  }

  async function confirmDelete() {
    if (deleteConfirmation !== project.name) {
      setNotice({ tone: "error", message: "请输入完整项目名称以确认删除" });
      return;
    }
    setBusy(true);
    setNotice(null);
    try {
      await deleteProject(project);
      onProjectDeleted(project.id);
    } catch (error) {
      setNotice({
        tone: "error",
        message: errorMessage(error, "无法删除项目"),
      });
      setBusy(false);
    }
  }

  const selectedSection =
    SETTINGS_SECTIONS.find((item) => item.id === section) ??
    SETTINGS_SECTIONS[0]!;

  return (
    <section className="project-settings-workspace">
      <header className="studio-section-heading project-settings-heading">
        <div>
          <p className="studio-kicker">PROJECT GOVERNANCE</p>
          <h2>项目设置</h2>
        </div>
      </header>

      <div className="project-governance-strip">
        <div>
          <span>主负责人</span>
          <strong>{primaryMember?.displayName ?? "未指定"}</strong>
        </div>
        <div>
          <span>有效成员</span>
          <strong>{activeMembers.length}</strong>
        </div>
        <div>
          <span>存储策略</span>
          <strong>{selections.length}/3</strong>
        </div>
        <div>
          <span>项目状态</span>
          <strong>{project.status === "active" ? "进行中" : "已归档"}</strong>
        </div>
      </div>

      {notice ? (
        <p
          className={`project-settings-notice is-${notice.tone}`}
          role={notice.tone === "error" ? "alert" : "status"}
        >
          {notice.message}
        </p>
      ) : null}

      <div className="project-settings-shell">
        <nav className="project-settings-nav" aria-label="项目设置分区">
          {SETTINGS_SECTIONS.map((item) => {
            const Icon = item.icon;
            return (
              <button
                className={item.id === section ? "is-active" : ""}
                type="button"
                key={item.id}
                onClick={() => {
                  setSection(item.id);
                  setNotice(null);
                }}
              >
                <Icon size={16} aria-hidden="true" />
                <span>
                  <strong>{item.label}</strong>
                  <small>{item.description}</small>
                </span>
              </button>
            );
          })}
        </nav>

        <div className="project-settings-content">
          <header className="project-settings-section-header">
            <p>{selectedSection.description}</p>
            <h3>{selectedSection.label}</h3>
          </header>

          {loading ? <SettingsLoading /> : null}

          {!loading && section === "general" ? (
            <form className="project-general-form" onSubmit={saveGeneral}>
              <label className="field">
                <span>项目名称</span>
                <input
                  value={name}
                  maxLength={120}
                  disabled={busy || !canManageProject}
                  onChange={(event) => setName(event.target.value)}
                />
              </label>
              <label className="field">
                <span>项目说明</span>
                <textarea
                  value={description}
                  rows={5}
                  maxLength={2000}
                  disabled={busy || !canManageProject}
                  placeholder="说明项目目标、交付范围或协作约定"
                  onChange={(event) => setDescription(event.target.value)}
                />
              </label>
              <div className="project-setting-facts">
                <span>创建于 {formatDateTime(project.createdAt)}</span>
                <span>最近更新 {formatDateTime(project.updatedAt)}</span>
              </div>
              {canManageProject ? (
                <div className="project-settings-actions">
                  <button
                    className="primary-button"
                    type="submit"
                    disabled={busy}
                  >
                    <Save size={15} />
                    {busy ? "保存中…" : "保存基本信息"}
                  </button>
                </div>
              ) : (
                <ReadOnlyNote />
              )}
            </form>
          ) : null}

          {!loading && section === "access" ? (
            <div className="project-access-settings">
              <div className="project-member-directory">
                {members.map((item) => (
                  <ProjectMemberRow
                    key={item.id}
                    item={item}
                    busy={busy}
                    canManage={canManageMembers}
                    onSave={saveMember}
                    onRemove={removeMember}
                  />
                ))}
              </div>

              {canManageMembers ? (
                <div className="project-add-member">
                  <div className="project-setting-subhead">
                    <div>
                      <strong>加入项目</strong>
                      <span>账号和临时账户仅可访问当前项目。</span>
                    </div>
                    <div className="project-setting-segmented">
                      <button
                        className={addMode === "member" ? "is-active" : ""}
                        type="button"
                        onClick={() => setAddMode("member")}
                      >
                        已有账号
                      </button>
                      <button
                        className={addMode === "guest" ? "is-active" : ""}
                        type="button"
                        onClick={() => setAddMode("guest")}
                      >
                        临时账户
                      </button>
                    </div>
                  </div>

                  {addMode === "member" ? (
                    <form className="project-access-form" onSubmit={addMember}>
                      <div className="project-access-form-grid">
                        <label className="field">
                          <span>账号</span>
                          <select
                            value={memberDraft.userId}
                            disabled={busy || addableMembers.length === 0}
                            onChange={(event) =>
                              setMemberDraft({
                                ...memberDraft,
                                userId: event.target.value,
                              })
                            }
                          >
                            {addableMembers.length === 0 ? (
                              <option value="">没有可添加账号</option>
                            ) : null}
                            {addableMembers.map((item) => (
                              <option key={item.userId} value={item.userId}>
                                {item.displayName} ·{" "}
                                {item.email ?? workspaceRoleLabel(item.role)}
                              </option>
                            ))}
                          </select>
                        </label>
                        <label className="field">
                          <span>项目角色</span>
                          <select
                            value={memberDraft.roleKey}
                            disabled={busy}
                            onChange={(event) => {
                              const roleKey = event.target
                                .value as ProjectMemberDraft["roleKey"];
                              setMemberDraft({
                                ...memberDraft,
                                roleKey,
                                permissions: defaultPermissions(roleKey),
                              });
                            }}
                          >
                            <option value="member">项目成员</option>
                            <option value="supervisor">项目主管</option>
                          </select>
                        </label>
                        <label className="field">
                          <span>有效期</span>
                          <input
                            type="date"
                            value={memberDraft.expiresAt}
                            disabled={busy}
                            onChange={(event) =>
                              setMemberDraft({
                                ...memberDraft,
                                expiresAt: event.target.value,
                              })
                            }
                          />
                        </label>
                      </div>
                      <PermissionGrid
                        value={memberDraft.permissions}
                        disabled={busy}
                        onChange={(permissions) =>
                          setMemberDraft({ ...memberDraft, permissions })
                        }
                      />
                      <button
                        className="primary-button"
                        type="submit"
                        disabled={busy || !memberDraft.userId}
                      >
                        <UserPlus size={15} />
                        {busy ? "添加中…" : "添加成员"}
                      </button>
                    </form>
                  ) : (
                    <form
                      className="project-access-form"
                      onSubmit={createGuest}
                    >
                      <div className="project-access-form-grid">
                        <label className="field">
                          <span>显示名称</span>
                          <input
                            value={guestDraft.displayName}
                            maxLength={80}
                            disabled={busy}
                            placeholder="例如 客户审阅"
                            onChange={(event) =>
                              setGuestDraft({
                                ...guestDraft,
                                displayName: event.target.value,
                              })
                            }
                          />
                        </label>
                        <label className="field">
                          <span>邮箱备注</span>
                          <input
                            type="email"
                            value={guestDraft.email}
                            disabled={busy}
                            placeholder="可留空"
                            onChange={(event) =>
                              setGuestDraft({
                                ...guestDraft,
                                email: event.target.value,
                              })
                            }
                          />
                        </label>
                        <label className="field">
                          <span>有效期</span>
                          <input
                            type="date"
                            value={guestDraft.expiresAt}
                            disabled={busy}
                            onChange={(event) =>
                              setGuestDraft({
                                ...guestDraft,
                                expiresAt: event.target.value,
                              })
                            }
                          />
                        </label>
                      </div>
                      <PermissionGrid
                        value={guestDraft.permissions}
                        disabled={busy}
                        onChange={(permissions) =>
                          setGuestDraft({ ...guestDraft, permissions })
                        }
                      />
                      <button
                        className="primary-button"
                        type="submit"
                        disabled={busy || !guestDraft.displayName.trim()}
                      >
                        <UserPlus size={15} />
                        {busy ? "创建中…" : "创建临时账户"}
                      </button>
                    </form>
                  )}
                </div>
              ) : (
                <ReadOnlyNote />
              )}

              {canTransfer ? (
                <form
                  className="project-transfer-settings"
                  onSubmit={submitTransfer}
                >
                  <div className="project-setting-subhead">
                    <div>
                      <strong>转交主负责人</strong>
                      <span>新负责人会自动加入项目，原负责人按选择处理。</span>
                    </div>
                    <Crown size={18} aria-hidden="true" />
                  </div>
                  <div className="project-access-form-grid">
                    <label className="field">
                      <span>新的主负责人</span>
                      <select
                        value={transferDraft.newPrimaryUserId}
                        disabled={busy || transferCandidates.length === 0}
                        onChange={(event) =>
                          setTransferDraft({
                            ...transferDraft,
                            newPrimaryUserId: event.target.value,
                          })
                        }
                      >
                        {transferCandidates.length === 0 ? (
                          <option value="">没有可选择账号</option>
                        ) : null}
                        {transferCandidates.map((item) => (
                          <option key={item.userId} value={item.userId}>
                            {item.displayName} ·{" "}
                            {item.email ?? workspaceRoleLabel(item.role)}
                          </option>
                        ))}
                      </select>
                    </label>
                    <label className="field">
                      <span>原主负责人</span>
                      <select
                        value={transferDraft.formerOwnerAction}
                        disabled={busy}
                        onChange={(event) =>
                          setTransferDraft({
                            ...transferDraft,
                            formerOwnerAction: event.target
                              .value as ProjectTransferDraft["formerOwnerAction"],
                          })
                        }
                      >
                        <option value="keep_supervisor">保留为项目主管</option>
                        <option value="demote_member">降为项目成员</option>
                        <option value="leave">退出项目</option>
                      </select>
                    </label>
                  </div>
                  <button
                    className="secondary-button"
                    type="submit"
                    disabled={busy || !transferDraft.newPrimaryUserId}
                  >
                    {busy ? "转交中…" : "转交项目"}
                  </button>
                </form>
              ) : null}
            </div>
          ) : null}

          {!loading && section === "storage" ? (
            <div className="project-storage-settings">
              {PROJECT_STORAGE_PURPOSES.map((purpose) => {
                const current = selections.find(
                  (item) => item.purpose === purpose.id,
                );
                const availableGrants = storageGrantsForPurpose(
                  grants,
                  purpose.id,
                );
                const draftGrantId = selectionDrafts[purpose.id];
                const draftGrantUnavailable =
                  draftGrantId !== "" &&
                  !availableGrants.some((grant) => grant.id === draftGrantId);
                const selectedDraftGrant = availableGrants.find(
                  (grant) => grant.id === draftGrantId,
                );
                return (
                  <article
                    className="project-storage-setting-row"
                    key={purpose.id}
                  >
                    <div>
                      <strong>{purpose.label}</strong>
                      <span>{purpose.description}</span>
                      <small>
                        {current
                          ? `当前：${storageGrantLabel(current.grant)}`
                          : "尚未选择"}
                      </small>
                    </div>
                    <label className="field">
                      <span>可用存储</span>
                      <select
                        value={draftGrantId}
                        disabled={
                          busy ||
                          !canManageProject ||
                          project.status === "archived" ||
                          availableGrants.length === 0
                        }
                        onChange={(event) =>
                          setSelectionDrafts({
                            ...selectionDrafts,
                            [purpose.id]: event.target.value,
                          })
                        }
                      >
                        {availableGrants.length === 0 ? (
                          <option value="">
                            {purpose.id === "upload"
                              ? "Owner 尚未开放上传存储"
                              : "Owner 尚未开放存储"}
                          </option>
                        ) : (
                          <option value="">请选择</option>
                        )}
                        {draftGrantUnavailable ? (
                          <option value={draftGrantId}>当前存储不可用</option>
                        ) : null}
                        {availableGrants.map((grant) => (
                          <option key={grant.id} value={grant.id}>
                            {storageGrantLabel(grant)}
                          </option>
                        ))}
                      </select>
                      {purpose.id === "upload" &&
                      availableGrants.length === 0 ? (
                        <small>
                          需要 Owner 先在存储与上传安全中创建并开放上传位置。
                        </small>
                      ) : null}
                      {purpose.id === "upload" &&
                      selectedDraftGrant &&
                      projectStorageTrafficNote(selectedDraftGrant) ? (
                        <small>
                          {projectStorageTrafficNote(selectedDraftGrant)}
                        </small>
                      ) : null}
                    </label>
                    <button
                      className="secondary-button"
                      type="button"
                      disabled={
                        busy ||
                        !canManageProject ||
                        project.status === "archived" ||
                        availableGrants.length === 0 ||
                        !selectionDrafts[purpose.id] ||
                        selectionDrafts[purpose.id] === current?.grantId
                      }
                      onClick={() => void saveStorage(purpose.id)}
                    >
                      保存
                    </button>
                  </article>
                );
              })}
              {!canManageProject ? <ReadOnlyNote /> : null}
            </div>
          ) : null}

          {!loading && section === "activity" ? (
            <div className="project-activity-settings">
              {activity.length === 0 ? (
                <div className="project-setting-empty">
                  <Activity size={20} />
                  <strong>还没有项目活动</strong>
                </div>
              ) : (
                <ol className="project-activity-list">
                  {activity.map((item) => (
                    <li key={item.id}>
                      <span
                        className="project-activity-mark"
                        aria-hidden="true"
                      />
                      <div>
                        <strong>{activityLabel(item.action)}</strong>
                        <span>
                          {item.actorName || actorTypeLabel(item.actorType)} ·{" "}
                          {formatDateTime(item.occurredAt)}
                        </span>
                      </div>
                    </li>
                  ))}
                </ol>
              )}
            </div>
          ) : null}

          {!loading && section === "danger" ? (
            <div className="project-danger-settings">
              {canManageProject ? (
                <>
                  <section>
                    <div>
                      <strong>
                        {project.status === "active" ? "归档项目" : "恢复项目"}
                      </strong>
                      <span>
                        {project.status === "active"
                          ? "归档后保留项目数据，但暂停项目内写入和存储调整。"
                          : "恢复后项目重新进入进行中状态。"}
                      </span>
                    </div>
                    <button
                      className="secondary-button"
                      type="button"
                      disabled={busy}
                      onClick={() => void toggleArchive()}
                    >
                      {project.status === "active" ? (
                        <Archive size={15} />
                      ) : (
                        <RotateCcw size={15} />
                      )}
                      {project.status === "active" ? "归档项目" : "恢复项目"}
                    </button>
                  </section>
                  <section className="is-destructive">
                    <div>
                      <strong>删除项目</strong>
                      <span>
                        项目会从项目列表移除，集合一并关闭；媒体原文件不会立即删除。
                      </span>
                    </div>
                    <label className="field">
                      <span>输入“{project.name}”确认</span>
                      <input
                        value={deleteConfirmation}
                        disabled={busy}
                        onChange={(event) =>
                          setDeleteConfirmation(event.target.value)
                        }
                      />
                    </label>
                    <button
                      className="danger-button"
                      type="button"
                      disabled={busy || deleteConfirmation !== project.name}
                      onClick={() => void confirmDelete()}
                    >
                      <Trash2 size={15} />
                      {busy ? "删除中…" : "删除项目"}
                    </button>
                  </section>
                </>
              ) : (
                <ReadOnlyNote />
              )}
            </div>
          ) : null}
        </div>
      </div>
    </section>
  );
}

function ProjectMemberRow({
  item,
  busy,
  canManage,
  onSave,
  onRemove,
}: {
  item: ProjectMember;
  busy: boolean;
  canManage: boolean;
  onSave: (item: ProjectMember, input: ProjectMemberInput) => Promise<void>;
  onRemove: (item: ProjectMember) => Promise<void>;
}) {
  const editable = canManage && item.roleKey !== "primary_owner";
  const [expanded, setExpanded] = useState(false);
  const [roleKey, setRoleKey] = useState<ProjectMemberInput["roleKey"]>(
    item.roleKey === "primary_owner" ? "supervisor" : item.roleKey,
  );
  const [status, setStatus] = useState<
    NonNullable<ProjectMemberInput["status"]>
  >(coerceStatus(item));
  const [expiresAt, setExpiresAt] = useState(dateInputValue(item.expiresAt));
  const [permissions, setPermissions] = useState(() =>
    permissionsForMember(item),
  );

  useEffect(() => {
    setRoleKey(item.roleKey === "primary_owner" ? "supervisor" : item.roleKey);
    setStatus(coerceStatus(item));
    setExpiresAt(dateInputValue(item.expiresAt));
    setPermissions(permissionsForMember(item));
  }, [item]);

  const changed =
    roleKey !==
      (item.roleKey === "primary_owner" ? "supervisor" : item.roleKey) ||
    status !== coerceStatus(item) ||
    expiresAt !== dateInputValue(item.expiresAt) ||
    permissionSignature(permissions) !==
      permissionSignature(permissionsForMember(item));

  return (
    <article className={`project-settings-member${expanded ? " is-open" : ""}`}>
      <div className="project-settings-member-summary">
        <span className="project-settings-avatar" aria-hidden="true">
          {initials(item.displayName)}
        </span>
        <div>
          <strong>{item.displayName}</strong>
          <span>
            {projectRoleLabel(item.roleKey)} ·{" "}
            {projectMemberStatusLabel(item.status)}
            {item.expiresAt ? ` · 至 ${formatDate(item.expiresAt)}` : ""}
          </span>
        </div>
        <small>{item.email ?? "无邮箱"}</small>
        {editable ? (
          <button
            className="text-button"
            type="button"
            onClick={() => setExpanded((current) => !current)}
          >
            {expanded ? "收起" : "管理"}
          </button>
        ) : (
          <span className="project-settings-owner-badge">受保护</span>
        )}
      </div>

      {expanded && editable ? (
        <div className="project-settings-member-editor">
          <div className="project-access-form-grid">
            <label className="field">
              <span>项目角色</span>
              <select
                value={roleKey}
                disabled={busy}
                onChange={(event) => {
                  const nextRole = event.target
                    .value as ProjectMemberInput["roleKey"];
                  setRoleKey(nextRole);
                  setPermissions(defaultPermissions(nextRole));
                }}
              >
                <option value="supervisor">项目主管</option>
                <option value="member">项目成员</option>
                <option value="guest">临时账户</option>
              </select>
            </label>
            <label className="field">
              <span>状态</span>
              <select
                value={status}
                disabled={busy}
                onChange={(event) =>
                  setStatus(
                    event.target.value as NonNullable<
                      ProjectMemberInput["status"]
                    >,
                  )
                }
              >
                <option value="active">有效</option>
                <option value="disabled">停用</option>
                <option value="expired">过期</option>
              </select>
            </label>
            <label className="field">
              <span>有效期</span>
              <input
                type="date"
                value={expiresAt}
                disabled={busy}
                onChange={(event) => setExpiresAt(event.target.value)}
              />
            </label>
          </div>
          <PermissionGrid
            value={permissions}
            disabled={busy}
            onChange={setPermissions}
          />
          <div className="project-settings-member-actions">
            <button
              className="secondary-button"
              type="button"
              disabled={busy || !changed}
              onClick={() =>
                void onSave(item, {
                  userId: item.userId,
                  roleKey,
                  status,
                  permissions: normalizedPermissions(permissions),
                  expiresAt: dateToISO(expiresAt),
                })
              }
            >
              保存权限
            </button>
            <button
              className="text-danger-button"
              type="button"
              disabled={busy}
              onClick={() => void onRemove(item)}
            >
              从项目移除
            </button>
          </div>
        </div>
      ) : null}
    </article>
  );
}

function PermissionGrid({
  value,
  disabled,
  onChange,
}: {
  value: Record<string, boolean>;
  disabled: boolean;
  onChange: (value: Record<string, boolean>) => void;
}) {
  return (
    <fieldset className="project-settings-permissions">
      <legend>逐项权限</legend>
      {PROJECT_PERMISSION_OPTIONS.map((permission) => (
        <label key={permission.id}>
          <input
            type="checkbox"
            checked={Boolean(value[permission.id])}
            disabled={disabled}
            onChange={(event) =>
              onChange({ ...value, [permission.id]: event.target.checked })
            }
          />
          <span>{permission.label}</span>
        </label>
      ))}
    </fieldset>
  );
}

function SettingsLoading() {
  return (
    <div className="project-settings-loading" aria-label="正在加载项目设置">
      <span />
      <span />
      <span />
    </div>
  );
}

function ReadOnlyNote() {
  return (
    <p className="project-settings-readonly">
      当前账号可以查看这部分设置，但没有修改权限。
    </p>
  );
}

function defaultMemberDraft(): ProjectMemberDraft {
  return {
    userId: "",
    roleKey: "member",
    permissions: defaultPermissions("member"),
    expiresAt: "",
  };
}

function defaultGuestDraft(): ProjectGuestDraft {
  return {
    displayName: "",
    email: "",
    permissions: defaultPermissions("guest"),
    expiresAt: defaultExpiryDate(7),
  };
}

function defaultPermissions(
  role: ProjectMemberInput["roleKey"],
): Record<string, boolean> {
  const all = role === "supervisor";
  const memberDefaults = new Set([
    "project.read",
    "assets.add",
    "assets.upload",
    "assets.remove",
    "reviews.create",
    "reviews.comment",
    "reviews.decide",
    "reviews.delete",
    "shares.create",
  ]);
  const guestDefaults = new Set([
    "project.read",
    "reviews.comment",
    "reviews.decide",
  ]);
  return Object.fromEntries(
    PROJECT_PERMISSION_OPTIONS.map((item) => [
      item.id,
      all ||
        (role === "member" && memberDefaults.has(item.id)) ||
        (role === "guest" && guestDefaults.has(item.id)),
    ]),
  );
}

function permissionsForMember(item: ProjectMember): Record<string, boolean> {
  return {
    ...defaultPermissions(
      item.roleKey === "primary_owner" ? "supervisor" : item.roleKey,
    ),
    ...item.permissions,
  };
}

function normalizedPermissions(
  permissions: Record<string, boolean>,
): Record<string, boolean> {
  return Object.fromEntries(
    PROJECT_PERMISSION_OPTIONS.map((item) => [
      item.id,
      Boolean(permissions[item.id]),
    ]),
  );
}

function permissionSignature(permissions: Record<string, boolean>): string {
  return PROJECT_PERMISSION_OPTIONS.map((item) =>
    permissions[item.id] ? "1" : "0",
  ).join("");
}

function selectionValues(
  selections: ProjectStorageSelection[],
): Record<ProjectStoragePurpose, string> {
  const values = { ...EMPTY_STORAGE_SELECTIONS };
  for (const item of selections) values[item.purpose] = item.grantId;
  return values;
}

function coerceStatus(
  item: ProjectMember,
): NonNullable<ProjectMemberInput["status"]> {
  return item.status === "removed" ? "disabled" : item.status;
}

function projectRoleLabel(role: ProjectMember["roleKey"]) {
  switch (role) {
    case "primary_owner":
      return "主负责人";
    case "supervisor":
      return "项目主管";
    case "member":
      return "项目成员";
    case "guest":
      return "临时账户";
  }
}

function workspaceRoleLabel(role: Membership["role"]) {
  switch (role) {
    case "owner":
      return "Owner";
    case "admin":
      return "项目主管";
    case "member":
      return "普通账户";
    case "guest":
      return "临时账户";
  }
}

function projectMemberStatusLabel(status: ProjectMember["status"]) {
  switch (status) {
    case "active":
      return "有效";
    case "disabled":
      return "停用";
    case "expired":
      return "已过期";
    case "removed":
      return "已移除";
  }
}

function activityLabel(action: string) {
  const labels: Record<string, string> = {
    "project.created": "创建了项目",
    "project.updated": "更新了项目基本信息",
    "project.archived": "归档了项目",
    "project.restored": "恢复了项目",
    "project.member_added": "添加了项目成员",
    "project.member_updated": "更新了成员权限",
    "project.member_removed": "移除了项目成员",
    "project.guest_created": "创建了临时账户",
    "project.primary_owner_transferred": "转交了项目主负责人",
    "project_storage.selected": "更新了项目存储",
  };
  return labels[action] ?? action.replaceAll(".", " / ");
}

function storageGrantsForPurpose(
  grants: ProjectStorageGrant[],
  purpose: ProjectStoragePurpose,
) {
  return grants.filter((grant) => {
    const activeLocalTarget =
      grant.status === "active" &&
      grant.providerStatus === "active" &&
      grant.rootStatus === "available" &&
      grant.authorizedRootId !== null &&
      grant.localManagedBucketId !== null;
    if (activeLocalTarget) {
      return purpose !== "upload" || grant.bucketPurpose === "upload";
    }
    return isRemoteUploadGrant(grant);
  });
}

function isRemoteUploadGrant(grant: ProjectStorageGrant) {
  return (
    grant.status === "active" &&
    grant.authorizedRootId !== null &&
    grant.rootStatus === "available" &&
    grant.providerStatus === "active" &&
    (grant.providerKind === "webdav" || grant.providerKind === "s3")
  );
}

function actorTypeLabel(actorType: string) {
  switch (actorType) {
    case "system":
      return "系统";
    case "visitor":
      return "匿名访客";
    case "node":
      return "处理节点";
    default:
      return "未知账号";
  }
}

function storageGrantLabel(grant: ProjectStorageGrant) {
  return `${grant.providerName}${grant.rootName ? ` · ${grant.rootName}` : ""}`;
}

function projectStorageTrafficNote(grant: ProjectStorageGrant) {
  if (grant.providerKind === "webdav" || grant.providerKind === "s3") {
    return "远程上传会占用 Visto 主机带宽。";
  }
  return "";
}

function defaultExpiryDate(days: number): string {
  const value = new Date();
  value.setDate(value.getDate() + days);
  return value.toISOString().slice(0, 10);
}

function dateInputValue(value: string | null): string {
  return value ? value.slice(0, 10) : "";
}

function dateToISO(value: string): string | null {
  return value ? new Date(`${value}T23:59:59`).toISOString() : null;
}

function nullableText(value: string): string | null {
  const trimmed = value.trim();
  return trimmed ? trimmed : null;
}

function initials(value: string): string {
  return value.trim().slice(0, 2).toUpperCase() || "?";
}

function formatDate(value: string): string {
  return new Intl.DateTimeFormat("zh-CN", {
    year: "numeric",
    month: "short",
    day: "numeric",
  }).format(new Date(value));
}

function formatDateTime(value: string): string {
  return new Intl.DateTimeFormat("zh-CN", {
    month: "short",
    day: "numeric",
    hour: "2-digit",
    minute: "2-digit",
  }).format(new Date(value));
}

function isAbort(error: unknown): boolean {
  return error instanceof DOMException && error.name === "AbortError";
}

function errorMessage(error: unknown, fallback: string): string {
  return error instanceof Error ? error.message : fallback;
}
