import { useCallback, useEffect, useMemo, useRef, useState } from "react";
import {
  Check,
  CircleAlert,
  FileUp,
  LoaderCircle,
  RotateCcw,
  Trash2,
  X,
} from "lucide-react";
import { getProjectUploadTarget, uploadAsset } from "./api/storage";

export type UploadTaskStatus =
  | "queued"
  | "validating"
  | "uploading"
  | "storing"
  | "success"
  | "attention"
  | "failed"
  | "cancelled";

export interface UploadTask {
  id: string;
  projectId: string;
  projectName: string;
  file: File;
  status: UploadTaskStatus;
  loaded: number;
  total: number;
  percent: number;
  speedBytesPerSecond: number | null;
  error: string | null;
  createdAt: number;
}

export interface UploadQueueController {
  tasks: UploadTask[];
  completedByProject: Record<string, number>;
  enqueue: (
    projectId: string,
    projectName: string,
    files: FileList | File[],
  ) => void;
  cancel: (taskId: string) => void;
  retry: (taskId: string) => void;
  clearCompleted: () => void;
}

const maxConcurrentUploads = 2;

export function useUploadQueue(): UploadQueueController {
  const [tasks, setTasks] = useState<UploadTask[]>([]);
  const [completedByProject, setCompletedByProject] = useState<
    Record<string, number>
  >({});
  const activeTaskIds = useRef(new Set<string>());
  const controllers = useRef(new Map<string, AbortController>());
  const speedSamples = useRef(
    new Map<string, { loaded: number; measuredAt: number }>(),
  );
  const nextTaskId = useRef(1);

  const updateTask = useCallback(
    (taskId: string, update: Partial<UploadTask>) => {
      setTasks((current) =>
        current.map((task) =>
          task.id === taskId ? { ...task, ...update } : task,
        ),
      );
    },
    [],
  );

  const runTask = useCallback(
    async (task: UploadTask) => {
      const controller = new AbortController();
      controllers.current.set(task.id, controller);
      updateTask(task.id, {
        status: "validating",
        error: null,
        loaded: 0,
        total: task.file.size,
        percent: 0,
        speedBytesPerSecond: null,
      });
      try {
        await getProjectUploadTarget(
          task.projectId,
          { sizeBytes: task.file.size },
          controller.signal,
        );
        const startedAt = Date.now();
        speedSamples.current.set(task.id, { loaded: 0, measuredAt: startedAt });
        updateTask(task.id, { status: "uploading" });
        const version = await uploadAsset(task.file, {
          projectId: task.projectId,
          signal: controller.signal,
          onProgress: (progress) => {
            const now = Date.now();
            const previous = speedSamples.current.get(task.id) ?? {
              loaded: 0,
              measuredAt: startedAt,
            };
            const elapsed = now - previous.measuredAt;
            let speedBytesPerSecond: number | null = null;
            if (elapsed >= 250 || progress.percent === 100) {
              speedBytesPerSecond = Math.max(
                0,
                ((progress.loaded - previous.loaded) * 1000) /
                  Math.max(elapsed, 1),
              );
              speedSamples.current.set(task.id, {
                loaded: progress.loaded,
                measuredAt: now,
              });
            }
            updateTask(task.id, {
              status:
                progress.percent === 100 ||
                (progress.total !== null && progress.loaded >= progress.total)
                  ? "storing"
                  : "uploading",
              loaded: progress.loaded,
              total: progress.total ?? task.file.size,
              percent: progress.percent ?? 0,
              ...(speedBytesPerSecond === null ? {} : { speedBytesPerSecond }),
            });
          },
        });
        const checkStatus = version.uploadCheck?.status;
        updateTask(task.id, {
          status:
            checkStatus === "quarantined" || checkStatus === "rejected"
              ? "attention"
              : "success",
          loaded: task.file.size,
          total: task.file.size,
          percent: 100,
          speedBytesPerSecond: null,
          error:
            checkStatus === "quarantined" || checkStatus === "rejected"
              ? (version.uploadCheck?.message ?? "上传完成，需要 Owner 处理")
              : null,
        });
        setCompletedByProject((current) => ({
          ...current,
          [task.projectId]: (current[task.projectId] ?? 0) + 1,
        }));
      } catch (error) {
        if (controller.signal.aborted) {
          updateTask(task.id, {
            status: "cancelled",
            speedBytesPerSecond: null,
            error: null,
          });
        } else {
          updateTask(task.id, {
            status: "failed",
            speedBytesPerSecond: null,
            error: uploadErrorMessage(error),
          });
        }
      } finally {
        controllers.current.delete(task.id);
        speedSamples.current.delete(task.id);
      }
    },
    [updateTask],
  );

  useEffect(() => {
    const available = maxConcurrentUploads - activeTaskIds.current.size;
    if (available <= 0) return;
    const pending = tasks
      .filter(
        (task) =>
          task.status === "queued" && !activeTaskIds.current.has(task.id),
      )
      .slice(0, available);
    for (const task of pending) {
      activeTaskIds.current.add(task.id);
      void runTask(task).finally(() => {
        activeTaskIds.current.delete(task.id);
        setTasks((current) => [...current]);
      });
    }
  }, [runTask, tasks]);

  const enqueue = useCallback(
    (projectId: string, projectName: string, files: FileList | File[]) => {
      const additions = Array.from(files)
        .filter((file) => file.name)
        .map<UploadTask>((file) => ({
          id: `upload-${Date.now()}-${nextTaskId.current++}`,
          projectId,
          projectName,
          file,
          status: "queued",
          loaded: 0,
          total: file.size,
          percent: 0,
          speedBytesPerSecond: null,
          error: null,
          createdAt: Date.now(),
        }));
      if (additions.length > 0) {
        setTasks((current) => [...additions, ...current]);
      }
    },
    [],
  );

  const cancel = useCallback(
    (taskId: string) => {
      const controller = controllers.current.get(taskId);
      if (controller) {
        controller.abort();
        return;
      }
      updateTask(taskId, { status: "cancelled", error: null });
    },
    [updateTask],
  );

  const retry = useCallback(
    (taskId: string) => {
      updateTask(taskId, {
        status: "queued",
        loaded: 0,
        percent: 0,
        speedBytesPerSecond: null,
        error: null,
      });
    },
    [updateTask],
  );

  const clearCompleted = useCallback(() => {
    setTasks((current) =>
      current.filter(
        (task) => task.status !== "success" && task.status !== "cancelled",
      ),
    );
  }, []);

  return {
    tasks,
    completedByProject,
    enqueue,
    cancel,
    retry,
    clearCompleted,
  };
}

export function UploadCenter({ queue }: { queue: UploadQueueController }) {
  const [open, setOpen] = useState(false);
  const rootRef = useRef<HTMLDivElement>(null);
  const activeCount = queue.tasks.filter(isPendingUploadTask).length;
  const failedCount = queue.tasks.filter(
    (task) => task.status === "failed" || task.status === "attention",
  ).length;
  const completedCount = queue.tasks.filter(
    (task) => task.status === "success",
  ).length;
  const orderedTasks = useMemo(
    () => [...queue.tasks].sort((a, b) => b.createdAt - a.createdAt),
    [queue.tasks],
  );

  useEffect(() => {
    if (!open) return;
    function close(event: PointerEvent) {
      if (!rootRef.current?.contains(event.target as Node)) setOpen(false);
    }
    function closeOnEscape(event: KeyboardEvent) {
      if (event.key === "Escape") setOpen(false);
    }
    window.addEventListener("pointerdown", close);
    window.addEventListener("keydown", closeOnEscape);
    return () => {
      window.removeEventListener("pointerdown", close);
      window.removeEventListener("keydown", closeOnEscape);
    };
  }, [open]);

  if (queue.tasks.length === 0) return null;

  const indicatorLabel =
    activeCount > 0
      ? `${activeCount} 个文件正在上传`
      : failedCount > 0
        ? `${failedCount} 个上传需要处理`
        : `${completedCount} 个文件上传完成`;

  return (
    <div className="upload-center" ref={rootRef}>
      <button
        className={`upload-center-trigger${activeCount > 0 ? " is-active" : ""}${failedCount > 0 ? " is-error" : ""}`}
        type="button"
        aria-label={indicatorLabel}
        aria-expanded={open}
        title="上传任务"
        onClick={() => setOpen((current) => !current)}
      >
        {activeCount > 0 ? (
          <LoaderCircle className="is-spinning" size={19} />
        ) : failedCount > 0 ? (
          <CircleAlert size={19} />
        ) : (
          <Check size={19} />
        )}
        {activeCount > 0 ? (
          <span className="upload-center-count">{activeCount}</span>
        ) : null}
      </button>

      {open ? (
        <section className="upload-center-panel" aria-label="上传任务">
          <header>
            <div>
              <strong>上传任务</strong>
              <span>
                {uploadSummary(activeCount, completedCount, failedCount)}
              </span>
            </div>
            <button
              className="studio-icon-button"
              type="button"
              title="清除已完成"
              aria-label="清除已完成的上传任务"
              disabled={completedCount === 0}
              onClick={queue.clearCompleted}
            >
              <Trash2 size={16} />
            </button>
          </header>
          <div className="upload-center-list">
            {orderedTasks.map((task) => (
              <UploadTaskRow
                key={task.id}
                task={task}
                onCancel={() => queue.cancel(task.id)}
                onRetry={() => queue.retry(task.id)}
              />
            ))}
          </div>
        </section>
      ) : null}
    </div>
  );
}

function UploadTaskRow({
  task,
  onCancel,
  onRetry,
}: {
  task: UploadTask;
  onCancel: () => void;
  onRetry: () => void;
}) {
  const active = isActiveUploadTask(task);
  return (
    <article className={`upload-task-row is-${task.status}`}>
      <span className="upload-task-icon">
        <FileUp size={17} />
      </span>
      <div className="upload-task-content">
        <div className="upload-task-title">
          <strong title={task.file.name}>{task.file.name}</strong>
          <span>{uploadTaskStatusLabel(task)}</span>
        </div>
        <div className="upload-task-meta">
          <span>{task.projectName}</span>
          <span>{formatBytes(task.file.size)}</span>
          {task.status === "uploading" && task.speedBytesPerSecond ? (
            <span>{formatSpeed(task.speedBytesPerSecond)}</span>
          ) : null}
        </div>
        {active ? (
          <div
            className="upload-task-progress"
            role="progressbar"
            aria-valuemin={0}
            aria-valuemax={100}
            aria-valuenow={task.percent}
          >
            <span style={{ width: `${task.percent}%` }} />
          </div>
        ) : null}
        {task.error ? <p>{task.error}</p> : null}
      </div>
      {active || task.status === "queued" ? (
        <button
          className="upload-task-action"
          type="button"
          title="取消上传"
          aria-label={`取消上传 ${task.file.name}`}
          onClick={onCancel}
        >
          <X size={15} />
        </button>
      ) : task.status === "failed" || task.status === "cancelled" ? (
        <button
          className="upload-task-action"
          type="button"
          title="重新上传"
          aria-label={`重新上传 ${task.file.name}`}
          onClick={onRetry}
        >
          <RotateCcw size={15} />
        </button>
      ) : null}
    </article>
  );
}

export function isActiveUploadTask(task: UploadTask) {
  return (
    task.status === "validating" ||
    task.status === "uploading" ||
    task.status === "storing"
  );
}

export function isPendingUploadTask(task: UploadTask) {
  return task.status === "queued" || isActiveUploadTask(task);
}

export function uploadTaskStatusLabel(task: UploadTask) {
  switch (task.status) {
    case "queued":
      return "等待上传";
    case "validating":
      return "检查存储";
    case "uploading":
      return `${Math.max(task.percent, task.loaded > 0 ? 1 : 0)}%`;
    case "storing":
      return "写入存储";
    case "success":
      return "上传完成";
    case "attention":
      return "需要处理";
    case "failed":
      return "上传失败";
    default:
      return "已取消";
  }
}

function uploadSummary(active: number, completed: number, failed: number) {
  if (active > 0) return `${active} 个进行中 · ${completed} 个已完成`;
  if (failed > 0) return `${failed} 个需要处理 · ${completed} 个已完成`;
  return `${completed} 个已完成`;
}

function uploadErrorMessage(error: unknown) {
  return error instanceof Error ? error.message : "无法上传这个文件";
}

function formatSpeed(bytesPerSecond: number) {
  return `${formatBytes(bytesPerSecond)}/s`;
}

function formatBytes(bytes: number) {
  if (!Number.isFinite(bytes) || bytes <= 0) return "0 B";
  const units = ["B", "KB", "MB", "GB", "TB"];
  const index = Math.min(
    Math.floor(Math.log(bytes) / Math.log(1024)),
    units.length - 1,
  );
  const value = bytes / 1024 ** index;
  return `${value >= 100 || index === 0 ? value.toFixed(0) : value.toFixed(1)} ${units[index]}`;
}
