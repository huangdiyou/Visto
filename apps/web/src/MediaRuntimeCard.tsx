import { useEffect, useState } from "react";
import type { SystemInfo } from "@review-studio/contracts";
import { getSystemInfo } from "./api/system";
import { listJobs, retryJob } from "./api/jobs";
import { copyText } from "./lib/clipboard";
import {
  isRuntimeError,
  mediaProgramsAvailable,
  runtimeCommands,
} from "./lib/media-runtime";

export function MediaRuntimeCard({
  system,
  setup = false,
  english = false,
}: {
  system: SystemInfo;
  setup?: boolean;
  english?: boolean;
}) {
  const [current, setCurrent] = useState(system);
  const [checking, setChecking] = useState(false);
  const [retrying, setRetrying] = useState(false);
  const [checked, setChecked] = useState(false);
  const [notice, setNotice] = useState<string | null>(null);
  const [error, setError] = useState<string | null>(null);
  const [pending, setPending] = useState<string[]>([]);
  const text = (zh: string, en: string) => (english ? en : zh);
  const available = mediaProgramsAvailable(current);
  const commands = runtimeCommands(current);
  useEffect(() => {
    const controller = new AbortController();
    setCurrent(system);
    setChecked(false);
    void getSystemInfo(controller.signal)
      .then(setCurrent)
      .catch(() => {
        if (!controller.signal.aborted)
          setError(
            text(
              "无法读取最新组件状态，请重新检查。",
              "Could not load current component status. Check again.",
            ),
          );
      });
    return () => controller.abort();
  }, [system]);

  async function check() {
    setChecking(true);
    setError(null);
    setNotice(null);
    setChecked(false);
    setPending([]);
    try {
      const fresh = await getSystemInfo();
      setCurrent(fresh);
      if (!setup) {
        const jobs = await listJobs(undefined, true);
        setPending(
          jobs
            .filter(
              (j) =>
                j.status === "failed" &&
                j.type.startsWith("media.") &&
                isRuntimeError(j.error),
            )
            .map((j) => j.id),
        );
      }
      setChecked(true);
      setNotice(
        mediaProgramsAvailable(fresh)
          ? setup
            ? text(
                "已检测到两个程序。创建工作空间后，可在 Owner 设置中检查编码能力。",
                "Both programs were found. After setup, verify encoding in Owner settings.",
              )
            : text(
                "已检测到两个程序。请在下方确认编码能力；配置已修复的失败任务现在可以重试。",
                "Both programs were found. Verify encoding below; repaired jobs can now be retried.",
              )
          : text(
              "仍缺少视频组件。请在服务器上完成配置并重启对应 Visto 实例，再重新检查。",
              "Video components are still missing. Configure them on the server, restart this Visto instance, then check again.",
            ),
      );
    } catch (e) {
      setError(
        e instanceof Error
          ? e.message
          : text("无法检查视频组件", "Could not check video components"),
      );
    } finally {
      setChecking(false);
    }
  }
  async function copy(command: string) {
    setError(null);
    setNotice(null);
    try {
      if (!(await copyText(command))) throw new Error("copy failed");
      setNotice(
        text(
          "命令已复制，请在运行 Visto 的服务器上执行。",
          "Command copied. Run it on the Visto server.",
        ),
      );
    } catch {
      setError(
        text(
          "无法复制，请手动选择命令。",
          "Could not copy. Select the command manually.",
        ),
      );
    }
  }
  async function retry() {
    setRetrying(true);
    setError(null);
    setNotice(null);
    const remaining: string[] = [];
    let count = 0;
    try {
      // Recheck at action time, including after another browser changed the state.
      const fresh = await getSystemInfo();
      setCurrent(fresh);
      if (!mediaProgramsAvailable(fresh))
        throw new Error(
          text(
            "组件仍不可用，请完成配置后再重试。",
            "Components are unavailable. Finish configuration before retrying.",
          ),
        );
      for (const id of pending) {
        try {
          await retryJob(id);
          count++;
        } catch {
          remaining.push(id);
        }
      }
      setPending(remaining);
      setNotice(
        text(`已重新排队 ${count} 个任务。`, `${count} jobs queued again.`),
      );
      if (remaining.length)
        setError(
          text(
            `${remaining.length} 个任务未能重新排队，请重新检查或回项目媒体查看原因。`,
            `${remaining.length} jobs could not be queued. Check again or inspect project media.`,
          ),
        );
    } catch (e) {
      setError(
        e instanceof Error
          ? e.message
          : text("无法重试任务", "Could not retry jobs"),
      );
    } finally {
      setRetrying(false);
    }
  }
  return (
    <section
      className="media-runtime-help"
      aria-label={text("视频处理组件", "Video components")}
    >
      <h3>
        {text(
          setup ? "视频预览准备" : "视频处理组件",
          setup ? "Video preview preparation" : "Video components",
        )}
      </h3>
      <p role="status">
        {available
          ? text(
              "已检测到视频组件程序。完整处理能力以本机能力检查和实际任务结果为准。",
              "Video programs were found. Capability checks and actual jobs confirm processing support.",
            )
          : text(
              "服务器缺少视频处理组件，暂时无法生成封面和视频预览。原文件已保留。",
              "The server is missing video components. Covers and video previews are unavailable. Original files are retained.",
            )}
      </p>
      <p>
        FFmpeg{" "}
        {current.media.ffmpegAvailable
          ? text("已检测到", "found")
          : text("未检测到", "missing")}{" "}
        · ffprobe{" "}
        {current.media.ffprobeAvailable
          ? text("已检测到", "found")
          : text("未检测到", "missing")}
      </p>
      {setup && !available ? (
        <p>
          {text(
            "可以继续创建工作空间；完成组件配置后才能处理视频。",
            "You can create the workspace now; video processing requires the components.",
          )}
        </p>
      ) : null}
      {!available ? (
        <>
          <p>
            {text(
              "请服务器管理员在运行 Visto 的电脑上处理。先检查已有组件，兼容就复用；缺失或不兼容，再按安装指引下载审核版或使用离线包。",
              "Ask the server administrator to check existing components first and reuse a compatible installation. Otherwise, follow the guide for the reviewed runtime or an offline package.",
            )}
          </p>
          {commands ? (
            <details>
              <summary>
                {text(
                  "已有 FFmpeg：检查并选择",
                  "Existing FFmpeg: inspect and select",
                )}
              </summary>
              <p>
                {text(
                  "在当前实例的安装文件夹打开终端，先运行只读能力检查。两个程序必须同时可用。",
                  "Open a terminal in this instance’s installation folder and run the read-only capability check. Both programs are required.",
                )}
              </p>
              <code>{commands.inspect}</code>
              <button
                type="button"
                className="secondary-button"
                onClick={() => void copy(commands.inspect)}
              >
                {text("复制检查命令", "Copy check command")}
              </button>
              <p>
                {text(
                  "检查通过后，由管理员执行下面的选择命令，再重启同一个 Visto 实例。CLI 与服务必须使用相同的 VISTO_MEDIA_RUNTIME_ROOT；自定义部署请按手册核对。此命令只选择已有组件，不下载组件。",
                  "After inspection passes, an administrator can select the runtime below and restart the same Visto instance. CLI and service must share VISTO_MEDIA_RUNTIME_ROOT; consult the guide for custom deployments. This selects existing components without downloading.",
                )}
              </p>
              <code>{commands.select}</code>
              <button
                type="button"
                className="secondary-button"
                onClick={() => void copy(commands.select)}
              >
                {text("复制配置命令", "Copy configuration command")}
              </button>
            </details>
          ) : (
            <p>
              {text(
                current.deployment === "docker"
                  ? "Docker 应使用含视频组件的配套镜像，请按 Docker 指引核对镜像与服务配置。"
                  : "此部署没有可确认的主机命令，请按安装指引选择服务器平台。",
                current.deployment === "docker"
                  ? "Use the Docker image containing the video components. Check the image and configuration in the Docker guide."
                  : "No host command is available for this deployment. Choose your server platform in the installation guide.",
              )}
            </p>
          )}
          <p>
            {text(
              "使用已有路径时，目录内需要同时有 ffmpeg 和 ffprobe。离线安装需使用同平台的审核包及配套签名清单，具体步骤见指引。",
              "A custom directory must contain both ffmpeg and ffprobe. Offline installation requires a reviewed package and signed manifest for the server platform; see the guide.",
            )}
          </p>
        </>
      ) : null}
      <div className="media-runtime-actions">
        <button
          type="button"
          className="secondary-button"
          disabled={checking || retrying}
          onClick={() => void check()}
        >
          {checking
            ? text("正在检查…", "Checking…")
            : text("重新检查", "Check again")}
        </button>
        <a
          href={
            english
              ? "https://visto.akahxh.top/docs/en/runtime.html"
              : "https://visto.akahxh.top/docs/runtime.html"
          }
          target="_blank"
          rel="noreferrer"
        >
          {text("安装与离线配置指引", "Installation and offline guide")}
        </a>
        {!setup && checked && available && pending.length > 0 ? (
          <button
            className="primary-button"
            type="button"
            disabled={checking || retrying}
            onClick={() => void retry()}
          >
            {retrying
              ? text("正在重新排队…", "Queuing…")
              : text(
                  `重试最近受影响任务（${pending.length}）`,
                  `Retry affected jobs (${pending.length})`,
                )}
          </button>
        ) : null}
      </div>
      {!setup && checked && available && pending.length === 0 ? (
        <p>
          {text(
            "最近任务中没有因缺少组件而失败的任务。更早的失败任务可回项目媒体中重试。",
            "No component-related failures were found in recent jobs. Retry older failures from project media.",
          )}
        </p>
      ) : null}
      {notice ? <p role="status">{notice}</p> : null}
      {error ? <p role="alert">{error}</p> : null}
    </section>
  );
}
