import { describe, expect, it } from "vitest";

import type { SystemMediaEncodingSettings } from "@review-studio/contracts";
import { ApiError } from "../api/client";
import { createTranslator } from "./i18n";
import {
  mediaEncodingOptions,
  mediaEncodingReprobeErrorMessage,
  mediaEncodingSaveErrorMessage,
  mediaEncodingViewState,
  mediaEncoderLabel,
} from "./media-encoding";

function settings(
  overrides: Partial<SystemMediaEncodingSettings> = {},
): SystemMediaEncodingSettings {
  return {
    preferredEncoder: "",
    effectiveEncoder: "h264_videotoolbox",
    activeEncoder: "",
    detectedEncoders: ["h264_videotoolbox", "libopenh264"],
    detectedAt: "2026-09-24T09:00:00Z",
    failureCount: 0,
    trippedEncoder: null,
    trippedReason: null,
    trippedAt: null,
    revision: 1,
    updatedBy: null,
    updatedAt: "2026-09-24T09:00:00Z",
    ...overrides,
  };
}

describe("mediaEncodingViewState", () => {
  it("describes an automatic instance", () => {
    const state = mediaEncodingViewState(settings());
    expect(state.automatic).toBe(true);
    expect(state.tripped).toBe(false);
    expect(state.summary).toContain("自动选择");
    expect(state.summary).toContain("VideoToolbox");
  });

  it("describes an explicit choice", () => {
    const state = mediaEncodingViewState(
      settings({
        preferredEncoder: "libopenh264",
        effectiveEncoder: "libopenh264",
      }),
    );
    expect(state.automatic).toBe(false);
    expect(state.summary).toContain("已指定");
    expect(state.summary).toContain("OpenH264");
  });

  it("explains a trip, because the Owner did not ask for that encoder", () => {
    const state = mediaEncodingViewState(
      settings({
        effectiveEncoder: "libopenh264",
        trippedEncoder: "h264_videotoolbox",
        trippedReason: "no device",
      }),
    );
    expect(state.tripped).toBe(true);
    expect(state.summary).toContain("已自动切换");
    expect(state.summary).toContain("VideoToolbox");
  });

  it("reports an instance that has never been probed", () => {
    const state = mediaEncodingViewState(
      settings({ detectedEncoders: [], detectedAt: null }),
    );
    expect(state.notProbed).toBe(true);
  });

  it("only shows the failure count while a cycle is open", () => {
    expect(
      mediaEncodingViewState(settings({ failureCount: 2 })).showFailureCount,
    ).toBe(true);
    expect(
      mediaEncodingViewState(settings({ failureCount: 0 })).showFailureCount,
    ).toBe(false);
    expect(
      mediaEncodingViewState(
        settings({ failureCount: 0, trippedEncoder: "h264_videotoolbox" }),
      ).showFailureCount,
    ).toBe(false);
  });

  it("returns an empty state before the settings load", () => {
    const state = mediaEncodingViewState(null);
    expect(state.effectiveLabel).toBe("");
    expect(state.summary).toBe("");
  });
});

describe("mediaEncodingOptions", () => {
  it("lists what the probe found", () => {
    expect(mediaEncodingOptions(settings())).toEqual([
      "h264_videotoolbox",
      "libopenh264",
    ]);
  });

  it("keeps a stored choice the probe did not report, so saving cannot drop it", () => {
    expect(
      mediaEncodingOptions(settings({ preferredEncoder: "libx264" })),
    ).toEqual(["h264_videotoolbox", "libopenh264", "libx264"]);
  });

  it("does not duplicate a stored choice the probe already reported", () => {
    expect(
      mediaEncodingOptions(settings({ preferredEncoder: "libopenh264" })),
    ).toEqual(["h264_videotoolbox", "libopenh264"]);
  });

  it("returns nothing before the settings load", () => {
    expect(mediaEncodingOptions(null)).toEqual([]);
  });
});

describe("mediaEncoderLabel", () => {
  it("falls back to the raw name so an unlabelled value is still visible", () => {
    expect(mediaEncoderLabel("h264_mystery")).toBe("h264_mystery");
  });
});

describe("mediaEncodingSaveErrorMessage", () => {
  const t = createTranslator("zh-CN");

  it("explains a revision conflict as a safe retry", () => {
    const error = new ApiError(
      "system_settings.revision_conflict",
      "conflict",
      409,
    );
    expect(mediaEncodingSaveErrorMessage(error, t)).toContain("已刷新");
  });

  it("keeps an unexpected message verbatim", () => {
    expect(mediaEncodingSaveErrorMessage(new Error("boom"), t)).toBe("boom");
  });

  it("falls back to the localized line for a non-Error", () => {
    expect(mediaEncodingSaveErrorMessage("nope", t)).toBe(
      t("owner.encodingSaveFailed"),
    );
  });
});

describe("mediaEncodingReprobeErrorMessage", () => {
  const t = createTranslator("zh-CN");

  it("names both refusals an Owner can act on", () => {
    expect(
      mediaEncodingReprobeErrorMessage(
        new ApiError("system_settings.probe_in_progress", "busy", 409),
        t,
      ),
    ).toContain("正在进行");
    expect(
      mediaEncodingReprobeErrorMessage(
        new ApiError("system_settings.probe_unavailable", "no", 503),
        t,
      ),
    ).toContain("不支持");
  });

  it("falls back to the localized line for a non-Error", () => {
    expect(mediaEncodingReprobeErrorMessage(undefined, t)).toBe(
      t("owner.encodingReprobeFailed"),
    );
  });
});

it("excludes stale VAAPI from save options", () => {
  expect(
    mediaEncodingOptions(
      settings({
        preferredEncoder: "h264_vaapi",
        detectedEncoders: ["h264_vaapi", "libopenh264"],
      }),
    ),
  ).toEqual(["libopenh264"]);
});
it("explains saved but unapplied settings", () => {
  const error = new ApiError(
    "system_settings.encoder_apply_failed",
    "failed",
    503,
  );
  expect(
    mediaEncodingSaveErrorMessage(error, createTranslator("zh-CN")),
  ).toContain("未能生效");
});
