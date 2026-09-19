import { describe, expect, it } from "vitest";
import type { ProjectOverview } from "@review-studio/contracts";
import { formatOverviewLocation, getProjectPulse } from "./project-overview";

function overview(overrides: Partial<ProjectOverview> = {}): ProjectOverview {
  return {
    projectId: "project-1",
    generatedAt: "2026-06-13T08:00:00Z",
    reviews: { total: 0, items: [] },
    openFeedback: { total: 0, items: [] },
    recentVersions: { total: 0, items: [] },
    failedTasks: { total: 0, items: [] },
    ...overrides,
  };
}

describe("project overview helpers", () => {
  it("prioritizes failed tasks over feedback and reviews", () => {
    const pulse = getProjectPulse(
      overview({
        failedTasks: { total: 2, items: [] },
        openFeedback: { total: 8, items: [] },
        reviews: { total: 4, items: [] },
      }),
    );

    expect(pulse.target).toBe("media");
    expect(pulse.tone).toBe("danger");
    expect(pulse.title).toContain("2");
  });

  it("uses feedback as the next actionable signal", () => {
    const pulse = getProjectPulse(
      overview({ openFeedback: { total: 3, items: [] } }),
    );

    expect(pulse.target).toBe("reviews");
    expect(pulse.title).toContain("3");
  });

  it("formats time points and visual annotations", () => {
    expect(formatOverviewLocation("time_point", 75_000_000, null)).toBe("1:15");
    expect(formatOverviewLocation("region", null, null)).toBe("画面区域");
  });
});
