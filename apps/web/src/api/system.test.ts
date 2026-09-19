import { afterEach, describe, expect, it, vi } from "vitest";
import { formatRuntimeLabel } from "../lib/format";
import { getSystemUpdateStatus } from "./system";

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
