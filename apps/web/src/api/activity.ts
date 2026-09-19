import type {
  AuditLog,
  ItemList,
  Notification,
  NotificationList,
} from "@review-studio/contracts";
import { requestJSON, requestVoid } from "./client";

export interface AuditLogFilter {
  actorId?: string;
  action?: string;
  resourceType?: string;
  resourceId?: string;
}

export async function listAuditLogs(
  filter: AuditLogFilter = {},
  signal?: AbortSignal,
): Promise<AuditLog[]> {
  const params = new URLSearchParams();
  if (filter.actorId) params.set("actorId", filter.actorId);
  if (filter.action) params.set("action", filter.action);
  if (filter.resourceType) params.set("resourceType", filter.resourceType);
  if (filter.resourceId) params.set("resourceId", filter.resourceId);
  const suffix = params.size > 0 ? `?${params.toString()}` : "";
  const response = await requestJSON<ItemList<AuditLog>>(
    `/api/v1/audit-logs${suffix}`,
    signal ? { signal } : {},
  );
  return response.items;
}

export async function listProjectActivity(
  projectId: string,
  signal?: AbortSignal,
): Promise<AuditLog[]> {
  const response = await requestJSON<ItemList<AuditLog>>(
    `/api/v1/projects/${encodeURIComponent(projectId)}/activity?limit=50`,
    signal ? { signal } : {},
  );
  return response.items;
}

export function listNotifications(
  signal?: AbortSignal,
): Promise<NotificationList> {
  return requestJSON<NotificationList>(
    "/api/v1/notifications",
    signal ? { signal } : {},
  );
}

export function markNotificationRead(
  item: Notification,
): Promise<Notification> {
  return requestJSON<Notification>(
    `/api/v1/notifications/${encodeURIComponent(item.id)}/read`,
    { method: "POST" },
  );
}

export function markAllNotificationsRead(): Promise<void> {
  return requestVoid("/api/v1/notifications/read-all", { method: "POST" });
}
