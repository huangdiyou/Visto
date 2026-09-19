import type { ApiErrorResponse } from "@review-studio/contracts";

export class ApiError extends Error {
  readonly code: string;
  readonly status: number;
  readonly requestId: string | undefined;

  constructor(
    code: string,
    message: string,
    status: number,
    requestId?: string,
  ) {
    super(message);
    this.name = "ApiError";
    this.code = code;
    this.status = status;
    this.requestId = requestId;
  }
}

interface RequestOptions {
  method?: "GET" | "POST" | "PUT" | "PATCH" | "DELETE";
  body?: unknown;
  signal?: AbortSignal;
}

export interface UploadProgress {
  loaded: number;
  total: number | null;
  percent: number | null;
}

export async function requestJSON<T>(
  path: string,
  options: RequestOptions = {},
): Promise<T> {
  const response = await performRequest(path, options);
  return (await response.json()) as T;
}

export async function requestVoid(
  path: string,
  options: RequestOptions = {},
): Promise<void> {
  await performRequest(path, options);
}

export async function requestFormJSON<T>(
  path: string,
  body: FormData,
  signal?: AbortSignal,
): Promise<T> {
  const response = await fetch(path, {
    method: "POST",
    headers: {
      Accept: "application/json",
      "X-Review-Studio-Request": "1",
    },
    body,
    ...(signal ? { signal } : {}),
  });
  if (!response.ok) {
    throw await apiErrorFromResponse(response);
  }
  return (await response.json()) as T;
}

export function requestFormJSONWithProgress<T>(
  path: string,
  body: FormData,
  options: {
    signal?: AbortSignal | undefined;
    onProgress?: ((progress: UploadProgress) => void) | undefined;
  } = {},
): Promise<T> {
  return new Promise<T>((resolve, reject) => {
    const request = new XMLHttpRequest();
    request.open("POST", path);
    request.setRequestHeader("Accept", "application/json");
    request.setRequestHeader("X-Review-Studio-Request", "1");
    request.responseType = "text";

    request.upload.onprogress = (event) => {
      const total = event.lengthComputable ? event.total : null;
      options.onProgress?.({
        loaded: event.loaded,
        total,
        percent:
          total && total > 0 ? Math.round((event.loaded / total) * 100) : null,
      });
    };

    request.onload = () => {
      const bodyText = request.responseText || "";
      const parsed = parseJSON(bodyText);
      if (request.status < 200 || request.status >= 300) {
        reject(
          apiErrorFromStatus(request.status, parsed as ApiErrorResponse | null),
        );
        return;
      }
      resolve(parsed as T);
    };
    request.onerror = () => {
      reject(
        new ApiError(
          "network.error",
          "网络连接中断，上传没有完成",
          request.status || 0,
        ),
      );
    };
    request.onabort = () => {
      reject(new DOMException("The upload was aborted.", "AbortError"));
    };

    if (options.signal) {
      if (options.signal.aborted) {
        request.abort();
        return;
      }
      options.signal.addEventListener("abort", () => request.abort(), {
        once: true,
      });
    }
    request.send(body);
  });
}

async function performRequest(
  path: string,
  options: RequestOptions,
): Promise<Response> {
  const method = options.method ?? "GET";
  const headers: Record<string, string> = {
    Accept: "application/json",
  };
  if (options.body !== undefined) {
    headers["Content-Type"] = "application/json";
  }
  if (method !== "GET") {
    headers["X-Review-Studio-Request"] = "1";
  }

  const request: RequestInit = { method, headers };
  if (options.body !== undefined) {
    request.body = JSON.stringify(options.body);
  }
  if (options.signal) {
    request.signal = options.signal;
  }

  const response = await fetch(path, request);
  if (!response.ok) {
    throw await apiErrorFromResponse(response);
  }
  return response;
}

async function apiErrorFromResponse(response: Response): Promise<ApiError> {
  const body = (await response
    .json()
    .catch(() => null)) as ApiErrorResponse | null;

  return apiErrorFromStatus(response.status, body);
}

function apiErrorFromStatus(
  status: number,
  body: ApiErrorResponse | null,
): ApiError {
  return new ApiError(
    body?.error.code ?? "internal.unexpected",
    body?.error.message ?? "服务暂时无法完成这个操作",
    status,
    body?.error.requestId,
  );
}

function parseJSON(value: string): unknown {
  if (!value) return null;
  try {
    return JSON.parse(value);
  } catch {
    return null;
  }
}
