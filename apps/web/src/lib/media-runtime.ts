import type {
  Job,
  MediaProbe,
  Rendition,
  SystemInfo,
} from "@review-studio/contracts";

export const runtimeMissingMessage =
  "服务器缺少视频处理组件，暂时无法生成预览。原文件已保留。";

export function isRuntimeError(
  error: { code?: string | null; message?: string | null } | null | undefined,
): boolean {
  if (!error) return false;
  if (
    [
      "probe.unavailable",
      "rendition.processor_unavailable",
      "media.runtime_unavailable",
    ].includes(error.code ?? "")
  )
    return true;
  // Older jobs wrapped the component error in job.execution_failed. Match only
  // the known missing-program text, never generic decoder/file errors.
  return /(?:ffprobe|ffmpeg) is unavailable/i.test(error.message ?? "");
}

export function runtimeIssue(item: {
  jobs: Job[];
  probe?: MediaProbe | undefined;
  failedRenditions: Rendition[];
}): boolean {
  return (
    item.jobs.some((j) => j.status === "failed" && isRuntimeError(j.error)) ||
    (item.probe?.status === "failed" &&
      isRuntimeError({
        code: item.probe.errorCode,
        message: item.probe.errorMessage,
      })) ||
    item.failedRenditions.some((r) =>
      isRuntimeError({ code: r.errorCode, message: r.errorMessage }),
    )
  );
}

export function mediaProgramsAvailable(system: SystemInfo): boolean {
  return system.media.ffmpegAvailable && system.media.ffprobeAvailable;
}

// Commands are for the SERVER installation folder, not the browser machine.
// Unknown platforms and containers get instructions instead of a guessed command.
export function runtimeCommands(
  system: Pick<SystemInfo, "platform" | "deployment" | "mode">,
) {
  if (system.deployment === "docker" || system.mode === "docker") return null;
  const binary =
    system.platform === "windows"
      ? "& .\\bin\\visto-server.exe"
      : ["linux", "darwin"].includes(system.platform ?? "")
        ? "./bin/visto-server"
        : null;
  if (!binary) return null;
  return {
    inspect: `${binary} --json media-runtime inspect --media-runtime system --non-interactive`,
    select: `${binary} --json media-runtime install --media-runtime system --non-interactive`,
  };
}
