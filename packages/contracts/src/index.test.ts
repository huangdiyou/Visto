import { describe, expect, it } from "vitest";
import type { SystemInfo } from "./index";

describe("SystemInfo contract", () => {
  it("accepts the local runtime shape", () => {
    const info: SystemInfo = {
      name: "Review Studio Core",
      version: "dev",
      apiVersion: "v1",
      mode: "local",
      database: "sqlite",
      media: {
        ffmpegAvailable: true,
        ffprobeAvailable: true,
        videoAccelerationMode: "software",
        videoEncoder: "libx264",
        videoFallbackEncoder: "",
        videoHardwareAcceleration: false,
      },
      access: {
        surface: "host",
        hostManagement: true,
        remoteWorkspace: true,
      },
    };

    expect(info.mode).toBe("local");
  });
});
