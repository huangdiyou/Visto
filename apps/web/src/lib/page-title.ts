import { PRODUCT_NAME_EN } from "../brand";
import type { OwnerSection, ProjectSection, StudioRoute } from "./studio-route";
import { normalizeLocale, type AppLocale } from "./i18n";

const projectSectionLabels: Record<
  AppLocale,
  Record<ProjectSection, string>
> = {
  "zh-CN": {
    overview: "概览",
    media: "媒体",
    reviews: "审阅",
    settings: "项目设置",
  },
  "en-US": {
    overview: "Overview",
    media: "Media",
    reviews: "Reviews",
    settings: "Project settings",
  },
};

const ownerSectionLabels: Record<AppLocale, Record<OwnerSection, string>> = {
  "zh-CN": {
    accounts: "账号",
    network: "网络与安全",
    storage: "存储与上传安全",
    notifications: "通知",
    activity: "活动",
    diagnostics: "诊断",
  },
  "en-US": {
    accounts: "Accounts",
    network: "Network and security",
    storage: "Storage and upload safety",
    notifications: "Notifications",
    activity: "Activity",
    diagnostics: "Diagnostics",
  },
};

const studioLabels: Record<
  AppLocale,
  {
    notifications: string;
    ownerSettings: string;
    personalSettings: string;
    projectFallback: string;
    projects: string;
  }
> = {
  "zh-CN": {
    notifications: "通知",
    ownerSettings: "Owner 设置",
    personalSettings: "我的设置",
    projectFallback: "项目",
    projects: "项目",
  },
  "en-US": {
    notifications: "Notifications",
    ownerSettings: "Owner settings",
    personalSettings: "My settings",
    projectFallback: "Project",
    projects: "Projects",
  },
};

export function teamDisplayName(
  value: { teamName?: string | null; name?: string | null } | null | undefined,
) {
  return clean(value?.teamName) || clean(value?.name) || PRODUCT_NAME_EN;
}

export function buildStudioTitle(input: {
  route: StudioRoute;
  teamName: string;
  projectName?: string | null;
  overlay?: "notifications" | "personal" | null;
  locale?: string | null;
}) {
  const locale = normalizeLocale(input.locale);
  const labels = studioLabels[locale];
  if (input.overlay === "notifications") {
    return withTeam(labels.notifications, input.teamName, locale);
  }
  if (input.overlay === "personal") {
    return withTeam(labels.personalSettings, input.teamName, locale);
  }
  if (input.route.area === "projects") {
    return withTeam(labels.projects, input.teamName, locale);
  }
  if (input.route.area === "owner") {
    return withTeam(
      `${labels.ownerSettings} · ${ownerSectionLabels[locale][input.route.section]}`,
      input.teamName,
      locale,
    );
  }
  const projectName = clean(input.projectName) || labels.projectFallback;
  return withTeam(
    `${projectName} · ${projectSectionLabels[locale][input.route.section]}`,
    input.teamName,
    locale,
  );
}

export function buildInvitationTitle(teamName?: string | null) {
  const value = clean(teamName);
  return value ? `加入 ${value} - ${PRODUCT_NAME_EN}` : PRODUCT_NAME_EN;
}

export function buildPublicShareTitle(input: {
  shareName?: string | null;
  teamName?: string | null;
}) {
  const shareName = clean(input.shareName);
  if (!shareName) {
    return PRODUCT_NAME_EN;
  }
  return withTeam(shareName, input.teamName);
}

export function setDocumentTitle(title: string) {
  document.title = clean(title) || PRODUCT_NAME_EN;
}

function withTeam(
  subject: string,
  teamName?: string | null,
  locale: AppLocale = "zh-CN",
) {
  const cleanSubject = clean(subject);
  const cleanTeam = clean(teamName);
  if (!cleanSubject) {
    return PRODUCT_NAME_EN;
  }
  if (!cleanTeam || cleanTeam === PRODUCT_NAME_EN) {
    return `${cleanSubject} · ${PRODUCT_NAME_EN}`;
  }
  if (locale === "en-US") {
    return `${cleanSubject} - ${cleanTeam} · ${PRODUCT_NAME_EN}`;
  }
  return `${cleanSubject} - ${cleanTeam} · ${PRODUCT_NAME_EN}`;
}

function clean(value: string | null | undefined) {
  return value?.trim() ?? "";
}
