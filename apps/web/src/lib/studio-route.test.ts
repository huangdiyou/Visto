import { describe, expect, it } from "vitest";
import { parseStudioRoute, studioRouteToHash } from "./studio-route";

describe("studio routes", () => {
  it("falls back to the project list for empty or unknown routes", () => {
    expect(parseStudioRoute("")).toEqual({ area: "projects" });
    expect(parseStudioRoute("#/unknown")).toEqual({ area: "projects" });
  });

  it("retires the legacy sources route", () => {
    expect(parseStudioRoute("#/sources")).toEqual({ area: "projects" });
    expect(parseStudioRoute("#/sources/legacy")).toEqual({
      area: "projects",
    });
  });

  it("parses project routes and defaults invalid sections to overview", () => {
    expect(parseStudioRoute("#/projects/project-1/reviews")).toEqual({
      area: "project",
      projectId: "project-1",
      section: "reviews",
    });
    expect(parseStudioRoute("#/projects/project-1/other")).toEqual({
      area: "project",
      projectId: "project-1",
      section: "overview",
    });
  });

  it("round trips project identifiers safely", () => {
    const hash = studioRouteToHash({
      area: "project",
      projectId: "project / 1",
      section: "settings",
    });
    expect(parseStudioRoute(hash)).toEqual({
      area: "project",
      projectId: "project / 1",
      section: "settings",
    });
  });

  it("supports deep links to project review threads", () => {
    const route = {
      area: "project" as const,
      projectId: "project / 1",
      section: "reviews" as const,
      reviewId: "review / 1",
      threadId: "thread / 1",
    };

    expect(parseStudioRoute(studioRouteToHash(route))).toEqual(route);
  });

  it("supports deep links to project media details", () => {
    const route = {
      area: "project" as const,
      projectId: "project / 1",
      section: "media" as const,
      assetId: "asset / 1",
    };

    expect(parseStudioRoute(studioRouteToHash(route))).toEqual(route);
  });

  it("parses Owner settings sections and defaults to accounts", () => {
    expect(parseStudioRoute("#/owner")).toEqual({
      area: "owner",
      section: "accounts",
    });
    expect(parseStudioRoute("#/owner/activity")).toEqual({
      area: "owner",
      section: "activity",
    });
    expect(studioRouteToHash({ area: "owner", section: "diagnostics" })).toBe(
      "#/owner/diagnostics",
    );
  });
});
