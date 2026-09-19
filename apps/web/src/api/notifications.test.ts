import { afterEach, describe, expect, it, vi } from "vitest";
import {
  createNotificationChannel,
  updateNotificationPreferences,
} from "./notifications";

describe("notification api", () => {
  afterEach(() => {
    vi.restoreAllMocks();
  });

  it("sends channel payload as one JSON object", async () => {
    const fetchMock = vi.spyOn(globalThis, "fetch").mockResolvedValue(
      jsonResponse({
        id: "channel-1",
        workspaceId: "workspace-1",
        kind: "feishu",
        name: "提醒",
        status: "disabled",
        smtpAuthConfigured: false,
        webhookHost: "open.feishu.cn",
        allowPrivateNetwork: false,
        revision: 1,
        createdAt: "2026-06-12T00:00:00Z",
        updatedAt: "2026-06-12T00:00:00Z",
        lastTestAt: null,
        lastTestStatus: null,
        lastErrorCode: null,
        lastErrorMessage: null,
      }),
    );

    await createNotificationChannel({
      kind: "feishu",
      name: "提醒",
      webhookUrl: "https://open.feishu.cn/open-apis/bot/v2/hook/test",
    });

    const firstCall = fetchMock.mock.calls[0];
    expect(firstCall).toBeDefined();
    const init = firstCall![1];
    expect(JSON.parse(String(init?.body))).toMatchObject({
      kind: "feishu",
      name: "提醒",
    });
    expect(typeof JSON.parse(String(init?.body))).toBe("object");
  });

  it("sends preference payload as one JSON object", async () => {
    const fetchMock = vi.spyOn(globalThis, "fetch").mockResolvedValue(
      jsonResponse({
        userId: "user-1",
        emailEnabled: true,
        feishuEnabled: false,
        wechatWorkEnabled: true,
        updatedAt: "2026-06-12T00:00:00Z",
      }),
    );

    await updateNotificationPreferences({
      emailEnabled: true,
      feishuEnabled: false,
      wechatWorkEnabled: true,
    });

    const firstCall = fetchMock.mock.calls[0];
    expect(firstCall).toBeDefined();
    const init = firstCall![1];
    expect(JSON.parse(String(init?.body))).toEqual({
      emailEnabled: true,
      feishuEnabled: false,
      wechatWorkEnabled: true,
    });
  });
});

function jsonResponse(body: unknown): Response {
  return new Response(JSON.stringify(body), {
    status: 200,
    headers: { "Content-Type": "application/json" },
  });
}
