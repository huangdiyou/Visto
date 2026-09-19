import { describe, expect, it } from "vitest";
import type {
  SystemUpdateCommand,
  SystemUpdateStatus,
} from "@review-studio/contracts";
import {
  deploymentLabel,
  groupUpdateCommands,
  updateStatusView,
} from "./update-status";

function status(
  overrides: Partial<SystemUpdateStatus> = {},
): SystemUpdateStatus {
  return {
    channel: "stable",
    currentVersion: "1.0.0",
    deployment: "docker",
    checked: false,
    policy: {
      webInstallSupported: false,
      silentUpdate: false,
      hostAdminRequired: true,
      downloadsPackage: false,
      executesHostCommands: false,
    },
    sources: [],
    check: { status: "not_configured", checkedAt: null, message: "未配置" },
    latest: null,
    commands: [],
    offline: { summary: "离线更新", steps: ["复制文件"] },
    securityNotes: ["不会下载更新包"],
    ...overrides,
  };
}

function command(id: string, platform: string): SystemUpdateCommand {
  return { id, label: id, platform, command: id, description: "" };
}

describe("update status view", () => {
  it("stays neutral before a check has happened", () => {
    const view = updateStatusView(status());
    expect(view.latestVersion).toBe("未检查");
    expect(view.latestTone).toBe("neutral");
    expect(view.showsRelease).toBe(false);
    expect(view.commandGroups).toEqual([]);
  });

  it("marks the deployment up to date when the manifest matches", () => {
    const view = updateStatusView(
      status({
        latest: {
          version: "1.0.0",
          publishedAt: "2026-09-02T00:00:00Z",
          minimumSupportedVersion: "1.0.0",
          releaseNotes: ["no change"],
          source: "https://updates.test/server/stable",
          upToDate: true,
          artifacts: [],
        },
      }),
    );
    expect(view.latestTone).toBe("ready");
    expect(view.releaseNotes).toEqual(["no change"]);
    expect(view.showsRelease).toBe(true);
  });

  it("asks for a host update when the manifest is newer", () => {
    const view = updateStatusView(
      status({
        currentVersion: "1.0.0",
        latest: {
          version: "1.0.1",
          publishedAt: "2026-09-02T00:00:00Z",
          minimumSupportedVersion: "1.0.0",
          releaseNotes: [],
          source: "https://updates.test/server/stable",
          upToDate: false,
          artifacts: [],
        },
      }),
    );
    expect(view.latestTone).toBe("attention");
    expect(view.latestHint).toContain("1.0.1");
  });

  it("explains an unavailable source without showing a version", () => {
    const view = updateStatusView(
      status({
        check: { status: "unavailable", checkedAt: null, message: "源不可用" },
      }),
    );
    expect(view.latestVersion).toBe("未检查");
    expect(view.latestHint).toContain("离线");
  });
});

describe("update command grouping", () => {
  it("keeps the order commands were sent in and labels each platform", () => {
    const groups = groupUpdateCommands([
      command("backup", "docker"),
      command("update", "docker"),
      command("windows", "docker-windows"),
    ]);
    expect(groups.map((group) => group.platform)).toEqual([
      "docker",
      "docker-windows",
    ]);
    expect(groups[0]?.commands.map((item) => item.id)).toEqual([
      "backup",
      "update",
    ]);
    expect(groups[1]?.label).toBe("Docker · Windows 主机");
  });

  it("labels the native Server command platforms", () => {
    const groups = groupUpdateCommands([
      command("linux-server-apply", "linux-server"),
      command("macos-server-rollback", "macos-server"),
    ]);
    expect(groups.map((group) => group.label)).toEqual([
      "Linux Server 主机",
      "macOS Server 主机",
    ]);
  });

  it("labels known deployments", () => {
    expect(deploymentLabel("docker")).toBe("Docker Compose");
    expect(deploymentLabel("windows-server")).toBe("Windows Server");
    expect(deploymentLabel("linux-server")).toBe("Linux Server");
    expect(deploymentLabel("macos-server")).toBe("macOS Server");
    expect(deploymentLabel("mystery")).toBe("mystery");
  });
});
