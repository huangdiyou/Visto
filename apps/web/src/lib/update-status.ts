import type {
  SystemUpdateCommand,
  SystemUpdateStatus,
} from "@review-studio/contracts";

export type UpdateTone = "ready" | "attention" | "neutral";

export interface UpdateCommandGroup {
  platform: string;
  label: string;
  commands: SystemUpdateCommand[];
}

export interface UpdateStatusView {
  latestVersion: string;
  latestTone: UpdateTone;
  latestHint: string;
  checkSummary: string;
  commandGroups: UpdateCommandGroup[];
  showsRelease: boolean;
}

const platformLabels: Record<string, string> = {
  docker: "Docker · Linux 主机",
  "docker-windows": "Docker · Windows 主机",
  "windows-server": "Windows Server 主机",
  "linux-server": "Linux Server 主机",
  "macos-server": "macOS Server 主机",
  source: "源码部署",
};

const deploymentLabels: Record<string, string> = {
  docker: "Docker Compose",
  "windows-server": "Windows Server",
  "linux-server": "Linux Server",
  "macos-server": "macOS Server",
  source: "源码部署",
};

export function deploymentLabel(deployment: string): string {
  return deploymentLabels[deployment] ?? deployment;
}

export function updateCommandPlatformLabel(platform: string): string {
  return platformLabels[platform] ?? platform;
}

export function formatReleaseDate(value: string): string {
  const date = new Date(value);
  if (Number.isNaN(date.getTime())) {
    return value;
  }
  return new Intl.DateTimeFormat("zh-CN", {
    year: "numeric",
    month: "2-digit",
    day: "2-digit",
  }).format(date);
}

// The update page never offers a browser-side install. Everything the view
// exposes is either a fact about the running version or a command an
// administrator runs on the deployment host.
export function updateStatusView(
  status: SystemUpdateStatus | null,
): UpdateStatusView {
  if (!status) {
    return {
      latestVersion: "—",
      latestTone: "neutral",
      latestHint: "正在读取更新状态。",
      checkSummary: "",
      commandGroups: [],
      showsRelease: false,
    };
  }

  const latest = status.latest;
  let latestTone: UpdateTone = "neutral";
  let latestVersion = "未检查";
  let latestHint = "点按“检查更新”读取官方版本清单，或直接使用离线更新包。";
  if (latest) {
    latestVersion = latest.version;
    switch (latest.state) {
      case "up_to_date":
        latestTone = "ready";
        latestHint = `当前版本 ${status.currentVersion} 已是最新。`;
        break;
      case "update_available":
        latestTone = "attention";
        latestHint = `当前版本 ${status.currentVersion}，可在部署主机更新到 ${latest.version}。`;
        break;
      default:
        // The published or running version could not be compared. The page must
        // not claim the instance is current, and must not prompt an update it
        // cannot justify.
        latestTone = "neutral";
        latestHint = `无法判断是否有新版本（当前版本 ${status.currentVersion}，更新源报告 ${latest.version}）。请对照官方发布页确认。`;
        break;
    }
  } else if (status.check.status === "unavailable") {
    latestHint = "更新源暂时不可用，可改用官方发布页或离线更新包。";
  } else if (status.check.status === "not_configured") {
    latestHint = "当前部署未配置公共更新源，请按下方离线流程更新。";
  }

  return {
    latestVersion,
    latestTone,
    latestHint,
    checkSummary: status.check.message,
    commandGroups: groupUpdateCommands(status.commands),
    showsRelease: latest !== null,
  };
}

export function groupUpdateCommands(
  commands: SystemUpdateCommand[],
): UpdateCommandGroup[] {
  const groups: UpdateCommandGroup[] = [];
  for (const command of commands) {
    let group = groups.find((item) => item.platform === command.platform);
    if (!group) {
      group = {
        platform: command.platform,
        label: updateCommandPlatformLabel(command.platform),
        commands: [],
      };
      groups.push(group);
    }
    group.commands.push(command);
  }
  return groups;
}
