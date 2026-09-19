import type {
  ItemList,
  NotificationChannel,
  NotificationChannelInput,
  NotificationChannelTestReport,
  NotificationDelivery,
  NotificationPreferences,
} from "@review-studio/contracts";
import { requestJSON, requestVoid } from "./client";

export async function listNotificationChannels(
  signal?: AbortSignal,
): Promise<NotificationChannel[]> {
  const response = await requestJSON<ItemList<NotificationChannel>>(
    "/api/v1/notification-channels",
    signal ? { signal } : {},
  );
  return response.items;
}

export function createNotificationChannel(
  input: NotificationChannelInput,
): Promise<NotificationChannel> {
  return requestJSON<NotificationChannel>("/api/v1/notification-channels", {
    method: "POST",
    body: input,
  });
}

export function updateNotificationChannel(
  channelId: string,
  input: NotificationChannelInput,
): Promise<NotificationChannel> {
  return requestJSON<NotificationChannel>(
    `/api/v1/notification-channels/${encodeURIComponent(channelId)}`,
    { method: "PATCH", body: input },
  );
}

export function deleteNotificationChannel(
  channel: NotificationChannel,
): Promise<void> {
  return requestVoid(
    `/api/v1/notification-channels/${encodeURIComponent(channel.id)}`,
    { method: "DELETE", body: { revision: channel.revision } },
  );
}

export function testNotificationChannel(
  channel: NotificationChannel,
): Promise<NotificationChannelTestReport> {
  return requestJSON<NotificationChannelTestReport>(
    `/api/v1/notification-channels/${encodeURIComponent(channel.id)}/test`,
    { method: "POST" },
  );
}

export function getNotificationPreferences(
  signal?: AbortSignal,
): Promise<NotificationPreferences> {
  return requestJSON<NotificationPreferences>(
    "/api/v1/notification-preferences/me",
    signal ? { signal } : {},
  );
}

export function updateNotificationPreferences(
  input: Pick<
    NotificationPreferences,
    "emailEnabled" | "feishuEnabled" | "wechatWorkEnabled"
  >,
): Promise<NotificationPreferences> {
  return requestJSON<NotificationPreferences>(
    "/api/v1/notification-preferences/me",
    { method: "PUT", body: input },
  );
}

export async function listNotificationDeliveries(
  signal?: AbortSignal,
): Promise<NotificationDelivery[]> {
  const response = await requestJSON<ItemList<NotificationDelivery>>(
    "/api/v1/notification-deliveries",
    signal ? { signal } : {},
  );
  return response.items;
}

export function retryNotificationDelivery(
  delivery: NotificationDelivery,
): Promise<NotificationDelivery> {
  return requestJSON<NotificationDelivery>(
    `/api/v1/notification-deliveries/${encodeURIComponent(delivery.id)}/retry`,
    { method: "POST" },
  );
}
