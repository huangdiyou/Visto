import { describe, expect, it } from "vitest";
import { isRuntimeError, runtimeCommands } from "./media-runtime";

describe("video runtime recovery", () => {
  it("recognizes old wrapped missing-tool errors without misdiagnosing damaged media", () => {
    expect(
      isRuntimeError({
        code: "job.execution_failed",
        message: "media probe failed: ffprobe is unavailable",
      }),
    ).toBe(true);
    expect(isRuntimeError({ code: "probe.unavailable" })).toBe(true);
    expect(isRuntimeError({ code: "rendition.processor_unavailable" })).toBe(
      true,
    );
    expect(
      isRuntimeError({
        code: "probe.failed",
        message: "Invalid data found when processing input",
      }),
    ).toBe(false);
    expect(
      isRuntimeError({
        code: "probe.failed",
        message: "media probe failed: timeout",
      }),
    ).toBe(false);
  });
  it("uses the server platform and never emits native commands for Docker or unknown servers", () => {
    expect(
      runtimeCommands({ platform: "windows", mode: "local" })?.inspect,
    ).toContain(".\\bin\\visto-server.exe");
    expect(
      runtimeCommands({ platform: "linux", mode: "local" })?.select,
    ).toContain("--media-runtime system");
    expect(
      runtimeCommands({ platform: "darwin", mode: "local" })?.inspect,
    ).toContain("./bin/visto-server");
    expect(
      runtimeCommands({
        platform: "linux",
        mode: "local",
        deployment: "docker",
      }),
    ).toBeNull();
    expect(runtimeCommands({ mode: "local" })).toBeNull();
  });
});
