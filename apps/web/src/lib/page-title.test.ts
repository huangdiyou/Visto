import { describe, expect, it } from "vitest";
import {
  buildInvitationTitle,
  buildPublicShareTitle,
  buildStudioTitle,
} from "./page-title";

describe("page title helpers", () => {
  it("builds project route titles with project and team context", () => {
    expect(
      buildStudioTitle({
        route: {
          area: "project",
          projectId: "project-1",
          section: "reviews",
        },
        projectName: "纪录片",
        teamName: "Visto 制作组",
      }),
    ).toBe("纪录片 · 审阅 - Visto 制作组 · Visto");
  });

  it("builds owner section and overlay titles", () => {
    expect(
      buildStudioTitle({
        route: { area: "owner", section: "storage" },
        teamName: "武将杯",
      }),
    ).toBe("Owner 设置 · 存储与上传安全 - 武将杯 · Visto");
    expect(
      buildStudioTitle({
        route: { area: "projects" },
        teamName: "武将杯",
        overlay: "notifications",
      }),
    ).toBe("通知 - 武将杯 · Visto");
  });

  it("builds localized studio titles", () => {
    expect(
      buildStudioTitle({
        route: {
          area: "project",
          projectId: "project-1",
          section: "overview",
        },
        projectName: "Documentary",
        teamName: "Visto Team",
        locale: "en-US",
      }),
    ).toBe("Documentary · Overview - Visto Team · Visto");
    expect(
      buildStudioTitle({
        route: { area: "owner", section: "diagnostics" },
        teamName: "Visto Team",
        locale: "en-US",
      }),
    ).toBe("Owner settings · Diagnostics - Visto Team · Visto");
  });

  it("builds public entry titles", () => {
    expect(buildInvitationTitle("武将杯")).toBe("加入 武将杯 - Visto");
    expect(
      buildPublicShareTitle({
        shareName: "客户终审",
        teamName: "武将杯",
      }),
    ).toBe("客户终审 - 武将杯 · Visto");
  });
});
