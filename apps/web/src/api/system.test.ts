import { afterEach, describe, expect, it, vi } from "vitest";
import { formatRuntimeLabel } from "../lib/format";
import {
  getHostAccess,
  getMediaEncodingSettings,
  getSystemUpdateStatus,
  reprobeMediaEncodingSettings,
} from "./system";

describe("formatRuntimeLabel", () => {
  it("joins the deployment mode and database", () => {
    expect(formatRuntimeLabel("local", "sqlite")).toBe("本地运行 · SQLite");
  });

  it("keeps unknown values readable", () => {
    expect(formatRuntimeLabel("edge", "duckdb")).toBe("edge · duckdb");
  });
});

describe("getSystemUpdateStatus", () => {
  afterEach(() => {
    vi.unstubAllGlobals();
    vi.restoreAllMocks();
  });

  async function requestURLFor(check: boolean) {
    const fetchMock = vi.fn().mockResolvedValue(
      new Response(JSON.stringify({ channel: "stable" }), {
        status: 200,
        headers: { "Content-Type": "application/json" },
      }),
    );
    vi.stubGlobal("fetch", fetchMock);
    await getSystemUpdateStatus(check);
    return String(fetchMock.mock.calls[0]?.[0]);
  }

  // A page load must not contact a public platform; only an explicit check may.
  it("does not ask Core to fetch the manifest by default", async () => {
    expect(await requestURLFor(false)).toContain(
      "/api/v1/system/update-status",
    );
    expect(await requestURLFor(false)).not.toContain("check=1");
  });

  it("asks Core to fetch the manifest when the Owner checks", async () => {
    expect(await requestURLFor(true)).toContain(
      "/api/v1/system/update-status?check=1",
    );
  });
});

describe("getHostAccess", () => {
  afterEach(() => {
    vi.unstubAllGlobals();
    vi.restoreAllMocks();
  });

  it("reads the read-only host access marker with GET", async () => {
    const fetchMock = vi.fn().mockResolvedValue(
      new Response(
        JSON.stringify({
          allowWebHostPaths: true,
          effectiveAllowWebHostPaths: true,
          environmentForced: false,
          revision: 1,
          updatedBy: "owner-1",
          updatedAt: "2026-09-21T00:00:00Z",
        }),
        { status: 200, headers: { "Content-Type": "application/json" } },
      ),
    );
    vi.stubGlobal("fetch", fetchMock);

    const settings = await getHostAccess();

    expect(String(fetchMock.mock.calls[0]?.[0])).toContain(
      "/api/v1/system/host-access",
    );
    // A read must not carry a write method: the switch is not flippable here.
    const init = fetchMock.mock.calls[0]?.[1] as RequestInit | undefined;
    expect((init?.method ?? "GET").toUpperCase()).toBe("GET");
    expect(init?.body).toBeUndefined();
    expect(settings.effectiveAllowWebHostPaths).toBe(true);
  });
});

describe("media encoding endpoints", () => {
  afterEach(() => {
    vi.unstubAllGlobals();
    vi.restoreAllMocks();
  });

  function stubJSON(body: unknown) {
    const fetchMock = vi.fn().mockResolvedValue(
      new Response(JSON.stringify(body), {
        status: 200,
        headers: { "Content-Type": "application/json" },
      }),
    );
    vi.stubGlobal("fetch", fetchMock);
    return fetchMock;
  }

  // The re-probe is a write: it starts FFmpeg processes, so it must never be
  // reachable as a plain GET.
  it("starts a re-probe with POST on the reprobe route", async () => {
    const fetchMock = stubJSON({ started: true });

    const result = await reprobeMediaEncodingSettings();

    expect(String(fetchMock.mock.calls[0]?.[0])).toContain(
      "/api/v1/system/media-encoding/reprobe",
    );
    const init = fetchMock.mock.calls[0]?.[1] as RequestInit | undefined;
    expect(init?.method?.toUpperCase()).toBe("POST");
    expect(result.started).toBe(true);
  });

  it("reads the encoding settings with GET", async () => {
    const fetchMock = stubJSON({ preferredEncoder: "" });

    await getMediaEncodingSettings();

    expect(String(fetchMock.mock.calls[0]?.[0])).toContain(
      "/api/v1/system/media-encoding",
    );
    const init = fetchMock.mock.calls[0]?.[1] as RequestInit | undefined;
    expect((init?.method ?? "GET").toUpperCase()).toBe("GET");
  });
});
