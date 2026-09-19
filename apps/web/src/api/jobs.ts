import type { ItemList, Job } from "@review-studio/contracts";
import { requestJSON } from "./client";

export function getJob(jobId: string, signal?: AbortSignal): Promise<Job> {
  return requestJSON<Job>(`/api/v1/jobs/${jobId}`, signal ? { signal } : {});
}

export async function listJobs(signal?: AbortSignal): Promise<Job[]> {
  const response = await requestJSON<ItemList<Job>>(
    "/api/v1/jobs",
    signal ? { signal } : {},
  );
  return response.items;
}

export function cancelJob(jobId: string): Promise<Job> {
  return requestJSON<Job>(`/api/v1/jobs/${jobId}/cancel`, {
    method: "POST",
  });
}

export function retryJob(jobId: string): Promise<Job> {
  return requestJSON<Job>(`/api/v1/jobs/${jobId}/retry`, {
    method: "POST",
  });
}
