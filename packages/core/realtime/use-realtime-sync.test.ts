import { QueryClient, type InfiniteData } from "@tanstack/react-query";
import { afterEach, describe, expect, it, vi } from "vitest";
import { setApiInstance } from "../api";
import type { ApiClient } from "../api/client";
import { channelKeys } from "../channels/queries";
import { chatKeys } from "../chat/queries";
import { inboxKeys } from "../inbox/queries";
import { issueKeys } from "../issues/queries";
import { notificationPreferenceKeys } from "../notification-preferences/queries";
import { workspaceKeys } from "../workspace/queries";
import { voiceCallKeys } from "../voice-calls/queries";
import type {
  ChatDonePayload,
  ChatMessage,
  ChatPendingTask,
  ChatMessagesPage,
  Channel,
  ChannelMessage,
  InboxItem,
  Workspace,
} from "../types";
import {
  applyChatDoneToCache,
  applyWorkspaceUpdatedToCache,
  handleChannelMessageNotification,
  handleInboxNew,
  invalidateChatMessageQueries,
  invalidateVoiceCallFromRealtime,
  resolveInboxSourceSlug,
  shouldNotifyChannelMessage,
} from "./use-realtime-sync";

const sessionId = "session-1";
const taskId = "task-1";
const messagesKey = chatKeys.messages(sessionId);
const pendingKey = chatKeys.pendingTask(sessionId);

function createQueryClient() {
  return new QueryClient({
    defaultOptions: {
      queries: { retry: false },
    },
  });
}

function userMessage(): ChatMessage {
  return {
    id: "msg-user",
    chat_session_id: sessionId,
    role: "user",
    content: "hello",
    task_id: null,
    created_at: "2026-05-13T05:00:00Z",
  };
}

function donePayload(overrides: Partial<ChatDonePayload> = {}): ChatDonePayload {
  return {
    chat_session_id: sessionId,
    task_id: taskId,
    message_id: "msg-assistant",
    content: "done",
    elapsed_ms: 1234,
    created_at: "2026-05-13T05:00:02Z",
    ...overrides,
  };
}

describe("applyChatDoneToCache", () => {
  it("writes the assistant message before clearing pending task", () => {
    const qc = createQueryClient();
    qc.setQueryData<ChatMessage[]>(messagesKey, [userMessage()]);
    qc.setQueryData<ChatPendingTask>(pendingKey, {
      task_id: taskId,
      status: "running",
    });

    const setQueryData = vi.spyOn(qc, "setQueryData");

    applyChatDoneToCache(qc, donePayload());

    expect(setQueryData.mock.calls[0]?.[0]).toEqual(messagesKey);
    expect(setQueryData.mock.calls[2]?.[0]).toEqual(pendingKey);
    expect(qc.getQueryData<ChatPendingTask>(pendingKey)).toEqual({});
    expect(qc.getQueryData<ChatMessage[]>(messagesKey)).toEqual([
      userMessage(),
      {
        id: "msg-assistant",
        chat_session_id: sessionId,
        role: "assistant",
        content: "done",
        task_id: taskId,
        created_at: "2026-05-13T05:00:02Z",
        elapsed_ms: 1234,
      },
    ]);
  });

  it("does not duplicate a replayed chat done event", () => {
    const qc = createQueryClient();
    const assistant: ChatMessage = {
      id: "msg-assistant",
      chat_session_id: sessionId,
      role: "assistant",
      content: "done",
      task_id: taskId,
      created_at: "2026-05-13T05:00:02Z",
      elapsed_ms: 1234,
    };
    qc.setQueryData<ChatMessage[]>(messagesKey, [userMessage(), assistant]);
    qc.setQueryData<ChatPendingTask>(pendingKey, {
      task_id: taskId,
      status: "running",
    });

    applyChatDoneToCache(qc, donePayload());

    expect(qc.getQueryData<ChatMessage[]>(messagesKey)).toEqual([
      userMessage(),
      assistant,
    ]);
    expect(qc.getQueryData<ChatPendingTask>(pendingKey)).toEqual({});
  });

  it("falls back to invalidation-only when older servers omit message fields", () => {
    const qc = createQueryClient();
    qc.setQueryData<ChatMessage[]>(messagesKey, [userMessage()]);
    qc.setQueryData<ChatPendingTask>(pendingKey, {
      task_id: taskId,
      status: "running",
    });

    applyChatDoneToCache(
      qc,
      donePayload({ message_id: undefined, content: undefined }),
    );

    expect(qc.getQueryData<ChatMessage[]>(messagesKey)).toEqual([
      userMessage(),
    ]);
    expect(qc.getQueryData<ChatPendingTask>(pendingKey)).toEqual({});
  });

  it("does not clear a newer queued follow-up when the interrupted run finishes", () => {
    const qc = createQueryClient();
    qc.setQueryData<ChatMessage[]>(messagesKey, [userMessage()]);
    qc.setQueryData<ChatPendingTask>(pendingKey, {
      task_id: "task-followup",
      status: "queued",
    });

    applyChatDoneToCache(qc, donePayload({ task_id: taskId }));

    expect(qc.getQueryData<ChatPendingTask>(pendingKey)).toEqual({
      task_id: "task-followup",
      status: "queued",
    });
  });
});

describe("invalidateChatMessageQueries", () => {
  it("invalidates both legacy and paged chat message caches", () => {
    const qc = createQueryClient();
    const invalidate = vi.spyOn(qc, "invalidateQueries");

    invalidateChatMessageQueries(qc, sessionId);

    expect(invalidate).toHaveBeenCalledWith({ queryKey: chatKeys.messages(sessionId) });
    expect(invalidate).toHaveBeenCalledWith({ queryKey: chatKeys.messagesPage(sessionId) });
  });
});

describe("invalidateVoiceCallFromRealtime", () => {
  it("invalidates only the call identified by the event payload", () => {
    const qc = createQueryClient();
    const invalidate = vi.spyOn(qc, "invalidateQueries");

    invalidateVoiceCallFromRealtime(qc, {
      workspace_id: "workspace-1",
      call_id: "call-1",
    });

    expect(invalidate).toHaveBeenCalledOnce();
    expect(invalidate).toHaveBeenCalledWith({
      queryKey: voiceCallKeys.detail("workspace-1", "call-1"),
    });
  });

  it("ignores malformed payloads instead of invalidating a broad cache", () => {
    const qc = createQueryClient();
    const invalidate = vi.spyOn(qc, "invalidateQueries");

    invalidateVoiceCallFromRealtime(qc, {
      workspace_id: "",
      call_id: "call-1",
    });
    invalidateVoiceCallFromRealtime(qc, {
      workspace_id: "workspace-1",
      call_id: "",
    });

    expect(invalidate).not.toHaveBeenCalled();
  });
});

describe("applyWorkspaceUpdatedToCache", () => {
  const wsId = "ws-1";

  function workspace(overrides: Partial<Workspace> = {}): Workspace {
    return {
      id: wsId,
      name: "Test",
      slug: "test",
      description: null,
      context: null,
      settings: {},
      issue_prefix: "TES",
      avatar_url: null,
      created_at: "2026-05-18T00:00:00Z",
      updated_at: "2026-05-18T00:00:00Z",
      ...overrides,
    };
  }

  it("invalidates issue cache when issue_prefix changes", () => {
    const qc = createQueryClient();
    qc.setQueryData<Workspace[]>(workspaceKeys.list(), [
      workspace({ issue_prefix: "TES" }),
    ]);
    const invalidate = vi.spyOn(qc, "invalidateQueries");

    applyWorkspaceUpdatedToCache(qc, {
      workspace: workspace({ issue_prefix: "NEW" }),
    });

    expect(invalidate).toHaveBeenCalledWith({
      queryKey: issueKeys.all(wsId),
    });
    expect(invalidate).toHaveBeenCalledWith({
      queryKey: workspaceKeys.list(),
    });
  });

  it("does not invalidate issue cache when only non-prefix fields change", () => {
    const qc = createQueryClient();
    qc.setQueryData<Workspace[]>(workspaceKeys.list(), [
      workspace({ issue_prefix: "TES", name: "Old name" }),
    ]);
    const invalidate = vi.spyOn(qc, "invalidateQueries");

    applyWorkspaceUpdatedToCache(qc, {
      workspace: workspace({ issue_prefix: "TES", name: "New name" }),
    });

    expect(invalidate).not.toHaveBeenCalledWith({
      queryKey: issueKeys.all(wsId),
    });
    expect(invalidate).toHaveBeenCalledWith({
      queryKey: workspaceKeys.list(),
    });
  });

  it("invalidates issue cache when the workspace isn't in the cached list yet", () => {
    // Conservative: a workspace appearing for the first time may correspond
    // to issue queries that were primed without ever seeing the (possibly
    // changing) prefix. Erring on the side of refresh keeps identifiers
    // accurate at minimal cost.
    const qc = createQueryClient();
    const invalidate = vi.spyOn(qc, "invalidateQueries");

    applyWorkspaceUpdatedToCache(qc, {
      workspace: workspace({ issue_prefix: "NEW" }),
    });

    expect(invalidate).toHaveBeenCalledWith({
      queryKey: issueKeys.all(wsId),
    });
  });
});


describe("applyChatDoneToCache paged messages", () => {
  it("patches page zero and skips older pages without duplicating replayed events", () => {
    const qc = createQueryClient();
    const older = userMessage();
    const latest: ChatMessage = {
      id: "msg-latest",
      chat_session_id: sessionId,
      role: "user",
      content: "latest",
      task_id: null,
      created_at: "2026-05-13T05:00:01Z",
    };
    qc.setQueryData<InfiniteData<ChatMessagesPage>>(chatKeys.messagesPage(sessionId), {
      pages: [
        { messages: [latest], limit: 1, has_more: true, next_cursor: { created_at: latest.created_at, id: latest.id } },
        { messages: [older], limit: 1, has_more: false, next_cursor: null },
      ],
      pageParams: [null, { created_at: latest.created_at, id: latest.id }],
    });

    applyChatDoneToCache(qc, donePayload());
    applyChatDoneToCache(qc, donePayload());

    const paged = qc.getQueryData<InfiniteData<ChatMessagesPage>>(chatKeys.messagesPage(sessionId));

    expect(paged?.pages[0]?.messages.map((m) => m.id)).toEqual(["msg-latest", "msg-assistant"]);
    expect(paged?.pages[1]?.messages.map((m) => m.id)).toEqual(["msg-user"]);
  });
});
describe("resolveInboxSourceSlug", () => {
  function workspace(overrides: Partial<Workspace> = {}): Workspace {
    return {
      id: "ws-a",
      name: "Workspace A",
      slug: "workspace-a",
      description: null,
      context: null,
      settings: {},
      issue_prefix: "WSA",
      avatar_url: null,
      created_at: "2026-05-18T00:00:00Z",
      updated_at: "2026-05-18T00:00:00Z",
      ...overrides,
    };
  }

  it("resolves the inbox item's source workspace, not another cached one", async () => {
    // Regression for #3766: an `inbox:new` from workspace A arriving while
    // workspace B is active must resolve A's slug for notification routing.
    const qc = createQueryClient();
    qc.setQueryData<Workspace[]>(workspaceKeys.list(), [
      workspace({ id: "ws-b", slug: "workspace-b", name: "Workspace B" }),
      workspace(),
    ]);

    await expect(resolveInboxSourceSlug(qc, "ws-a")).resolves.toBe("workspace-a");
  });

  it("returns null instead of falling back when the workspace is unknown", async () => {
    const qc = createQueryClient();
    qc.setQueryData<Workspace[]>(workspaceKeys.list(), [
      workspace({ id: "ws-b", slug: "workspace-b" }),
    ]);

    await expect(resolveInboxSourceSlug(qc, "ws-a")).resolves.toBeNull();
  });

  it("returns null for an empty workspace id without touching the cache", async () => {
    const qc = createQueryClient();
    const ensure = vi.spyOn(qc, "ensureQueryData");

    await expect(resolveInboxSourceSlug(qc, "")).resolves.toBeNull();
    expect(ensure).not.toHaveBeenCalled();
  });

  it("returns null when the workspace list cannot be fetched", async () => {
    const qc = createQueryClient();
    vi.spyOn(qc, "ensureQueryData").mockRejectedValueOnce(new Error("network down"));

    await expect(resolveInboxSourceSlug(qc, "ws-a")).resolves.toBeNull();
  });
});

describe("handleInboxNew", () => {
  function workspace(overrides: Partial<Workspace> = {}): Workspace {
    return {
      id: "ws-a",
      name: "Workspace A",
      slug: "workspace-a",
      description: null,
      context: null,
      settings: {},
      issue_prefix: "WSA",
      avatar_url: null,
      created_at: "2026-05-18T00:00:00Z",
      updated_at: "2026-05-18T00:00:00Z",
      ...overrides,
    };
  }

  function inboxItem(overrides: Partial<InboxItem> = {}): InboxItem {
    return {
      id: "item-1",
      workspace_id: "ws-a",
      recipient_type: "member",
      recipient_id: "member-1",
      actor_type: "member",
      actor_id: "member-2",
      type: "mentioned",
      severity: "info",
      issue_id: "issue-1",
      title: "Mentioned you",
      body: "in a comment",
      issue_status: null,
      read: false,
      archived: false,
      created_at: "2026-05-18T00:00:00Z",
      details: null,
      ...overrides,
    };
  }

  function stubDesktopAPI() {
    const showNotification = vi.fn();
    (globalThis as Record<string, unknown>).desktopAPI = { showNotification };
    return showNotification;
  }

  afterEach(() => {
    delete (globalThis as Record<string, unknown>).desktopAPI;
  });

  it("still shows the banner when the slug can't be resolved, with an empty slug so the click is a no-op", async () => {
    const qc = createQueryClient();
    // Workspace list is cached but doesn't contain the item's workspace.
    qc.setQueryData<Workspace[]>(workspaceKeys.list(), [
      workspace({ id: "ws-b", slug: "workspace-b" }),
    ]);
    qc.setQueryData(notificationPreferenceKeys.all("ws-a"), {
      preferences: { system_notifications: "all" },
    });
    const showNotification = stubDesktopAPI();

    await handleInboxNew(qc, inboxItem());

    expect(showNotification).toHaveBeenCalledWith({
      slug: "",
      itemId: "item-1",
      issueKey: "issue-1",
      title: "Mentioned you",
      body: "in a comment",
      url: "/",
    });
  });

  it("invalidates the ITEM's workspace inbox cache and resolves its slug, not the active workspace's", async () => {
    const qc = createQueryClient();
    qc.setQueryData<Workspace[]>(workspaceKeys.list(), [
      workspace({ id: "ws-b", slug: "workspace-b" }),
      workspace(),
    ]);
    qc.setQueryData(notificationPreferenceKeys.all("ws-a"), {
      preferences: { system_notifications: "all" },
    });
    const invalidate = vi.spyOn(qc, "invalidateQueries");
    const showNotification = stubDesktopAPI();

    await handleInboxNew(qc, inboxItem());

    expect(invalidate).toHaveBeenCalledWith({
      queryKey: inboxKeys.list("ws-a"),
    });
    expect(showNotification).toHaveBeenCalledWith(
      expect.objectContaining({ slug: "workspace-a" }),
    );
  });

  it("honors the SOURCE workspace's mute preference", async () => {
    const qc = createQueryClient();
    qc.setQueryData<Workspace[]>(workspaceKeys.list(), [workspace()]);
    qc.setQueryData(notificationPreferenceKeys.all("ws-a"), {
      preferences: { system_notifications: "muted" },
    });
    const showNotification = stubDesktopAPI();

    await handleInboxNew(qc, inboxItem());

    expect(showNotification).not.toHaveBeenCalled();
  });

  // The tests below exercise the COLD-cache mute path (source preference not
  // yet cached), where the request — not just the query key — must be scoped
  // to the source workspace (#3766 follow-up). They install a fake API so the
  // outgoing call's workspace argument is observable.
  afterEach(() => {
    setApiInstance(undefined as unknown as ApiClient);
  });

  it("fetches the SOURCE workspace's preference using its slug when the cache is cold", async () => {
    const qc = createQueryClient();
    qc.setQueryData<Workspace[]>(workspaceKeys.list(), [
      workspace({ id: "ws-b", slug: "workspace-b", name: "Workspace B" }),
      workspace(),
    ]);
    // No cached preference for ws-a → the handler must fetch, and the fetch
    // must target the source workspace's slug, not the active workspace's.
    const getNotificationPreferences = vi
      .fn()
      .mockResolvedValue({ preferences: { system_notifications: "all" } });
    setApiInstance({ getNotificationPreferences } as unknown as ApiClient);
    const showNotification = stubDesktopAPI();

    await handleInboxNew(qc, inboxItem());

    expect(getNotificationPreferences).toHaveBeenCalledWith("workspace-a");
    expect(showNotification).toHaveBeenCalledWith(
      expect.objectContaining({ slug: "workspace-a" }),
    );
  });

  it("suppresses the banner when the SOURCE workspace is muted on a cold cache", async () => {
    const qc = createQueryClient();
    qc.setQueryData<Workspace[]>(workspaceKeys.list(), [workspace()]);
    const getNotificationPreferences = vi
      .fn()
      .mockResolvedValue({ preferences: { system_notifications: "muted" } });
    setApiInstance({ getNotificationPreferences } as unknown as ApiClient);
    const showNotification = stubDesktopAPI();

    await handleInboxNew(qc, inboxItem());

    expect(getNotificationPreferences).toHaveBeenCalledWith("workspace-a");
    expect(showNotification).not.toHaveBeenCalled();
  });

  it("never fetches the active workspace's preference when the source slug can't be resolved", async () => {
    const qc = createQueryClient();
    // Item's workspace is absent from the cached list → slug unresolvable.
    qc.setQueryData<Workspace[]>(workspaceKeys.list(), [
      workspace({ id: "ws-b", slug: "workspace-b" }),
    ]);
    const getNotificationPreferences = vi
      .fn()
      .mockResolvedValue({ preferences: { system_notifications: "muted" } });
    setApiInstance({ getNotificationPreferences } as unknown as ApiClient);
    const showNotification = stubDesktopAPI();

    await handleInboxNew(qc, inboxItem());

    // Must NOT fall back to the active workspace's preference — that both
    // mis-mutes and pollutes the source workspace's cache key (#3766).
    expect(getNotificationPreferences).not.toHaveBeenCalled();
    expect(showNotification).toHaveBeenCalledWith(
      expect.objectContaining({ slug: "" }),
    );
  });

  // --- Web path: no desktopAPI → the browser Notification API ---
  // Same focus/mute gating as desktop, but the desktop bridge is absent and a
  // granted browser Notification stub is installed on `window`.
  let webBanners: { title: string; options?: NotificationOptions }[] = [];
  class FakeNotification {
    static permission: NotificationPermission = "granted";
    onclick: (() => void) | null = null;
    close = vi.fn();
    constructor(
      public title: string,
      public options?: NotificationOptions,
    ) {
      webBanners.push({ title, options });
    }
  }
  function installBrowserNotification(
    permission: NotificationPermission = "granted",
  ) {
    webBanners = [];
    FakeNotification.permission = permission;
    (globalThis as Record<string, unknown>).window = {
      Notification: FakeNotification,
      focus: vi.fn(),
    };
  }

  afterEach(() => {
    delete (globalThis as Record<string, unknown>).window;
  });

  it("shows a browser banner on web (no desktopAPI) when granted and not muted", async () => {
    const qc = createQueryClient();
    qc.setQueryData<Workspace[]>(workspaceKeys.list(), [workspace()]);
    qc.setQueryData(notificationPreferenceKeys.all("ws-a"), {
      preferences: { system_notifications: "all" },
    });
    installBrowserNotification("granted");

    await handleInboxNew(qc, inboxItem());

    expect(webBanners).toHaveLength(1);
    expect(webBanners[0]?.title).toBe("Mentioned you");
  });

  it("shows no browser banner when the SOURCE workspace is muted", async () => {
    const qc = createQueryClient();
    qc.setQueryData<Workspace[]>(workspaceKeys.list(), [workspace()]);
    qc.setQueryData(notificationPreferenceKeys.all("ws-a"), {
      preferences: { system_notifications: "muted" },
    });
    installBrowserNotification("granted");

    await handleInboxNew(qc, inboxItem());

    expect(webBanners).toHaveLength(0);
  });

  it("shows no browser banner when permission is not granted", async () => {
    const qc = createQueryClient();
    qc.setQueryData<Workspace[]>(workspaceKeys.list(), [workspace()]);
    qc.setQueryData(notificationPreferenceKeys.all("ws-a"), {
      preferences: { system_notifications: "all" },
    });
    installBrowserNotification("default");

    await handleInboxNew(qc, inboxItem());

    expect(webBanners).toHaveLength(0);
  });

  function channel(overrides: Partial<Channel> = {}): Channel {
    return {
      id: "channel-1",
      workspace_id: "ws-a",
      name: "general",
      kind: "group",
      description: null,
      lark_chat_id: null,
      created_by: "member-1",
      created_at: "2026-05-18T00:00:00Z",
      updated_at: "2026-05-18T00:00:00Z",
      ...overrides,
    };
  }

  function channelMessage(overrides: Partial<ChannelMessage> = {}): ChannelMessage {
    const seq = overrides.seq ?? 1;
    return {
      id: "message-1",
      channel_id: "channel-1",
      workspace_id: "ws-a",
      type: "agent",
      author_id: "agent-1",
      author_name: "Agent Bot",
      content: "I finished the work",
      source: "multica",
      external_message_id: null,
      client_message_id: null,
      created_at: "2026-05-18T00:00:00Z",
      ...overrides,
      seq,
    };
  }

  describe("shouldNotifyChannelMessage (LRM-411)", () => {
    it("notifies ambient unmuted traffic and self-suppresses", () => {
      expect(
        shouldNotifyChannelMessage(channelMessage(), {
          myUserId: "member-1",
          channelMuted: false,
        }),
      ).toBe(true);
      expect(
        shouldNotifyChannelMessage(
          channelMessage({ type: "user", author_id: "member-1" }),
          { myUserId: "member-1", channelMuted: false },
        ),
      ).toBe(false);
    });

    it("muted ambient is silent; muted @me still rings", () => {
      expect(
        shouldNotifyChannelMessage(channelMessage(), {
          myUserId: "member-1",
          channelMuted: true,
        }),
      ).toBe(false);
      expect(
        shouldNotifyChannelMessage(
          channelMessage({
            content: "hey [@Alice](mention://member/member-1)",
          }),
          { myUserId: "member-1", channelMuted: true },
        ),
      ).toBe(true);
    });

    it("never notifies type=system channel events (LRM-414)", () => {
      expect(
        shouldNotifyChannelMessage(
          channelMessage({ type: "system", content: "Alice joined" }),
          { myUserId: "member-1", channelMuted: false },
        ),
      ).toBe(false);
    });
  });

  it("shows a group-channel browser banner for ordinary unmuted messages (LRM-411)", async () => {
    const qc = createQueryClient();
    qc.setQueryData<Workspace[]>(workspaceKeys.list(), [workspace()]);
    qc.setQueryData(notificationPreferenceKeys.all("ws-a"), {
      preferences: { system_notifications: "all" },
    });
    qc.setQueryData<Channel[]>(channelKeys.list("ws-a"), [channel()]);
    installBrowserNotification("granted");

    await handleChannelMessageNotification(qc, channelMessage(), "member-1");

    expect(webBanners).toHaveLength(1);
    expect(webBanners[0]?.title).toBe("#general");
    expect(webBanners[0]?.options).toMatchObject({
      body: "Agent Bot: I finished the work",
      tag: "channel-1",
      data: expect.objectContaining({
        channelId: "channel-1",
        itemId: "message-1",
        url: "/workspace-a/channels/channel-1?message=message-1",
      }),
    });
  });

  it("still shows a group-channel banner for @mentions when unmuted", async () => {
    const qc = createQueryClient();
    qc.setQueryData<Workspace[]>(workspaceKeys.list(), [workspace()]);
    qc.setQueryData(notificationPreferenceKeys.all("ws-a"), {
      preferences: { system_notifications: "all" },
    });
    qc.setQueryData<Channel[]>(channelKeys.list("ws-a"), [channel()]);
    installBrowserNotification("granted");

    await handleChannelMessageNotification(
      qc,
      channelMessage({
        content: "hey [@Alice](mention://member/member-1) please look",
      }),
      "member-1",
    );

    expect(webBanners).toHaveLength(1);
    expect(webBanners[0]?.title).toBe("#general");
    expect(webBanners[0]?.options).toMatchObject({
      body: "Agent Bot: hey [@Alice](mention://member/member-1) please look",
      tag: "channel-1",
      data: expect.objectContaining({
        channelId: "channel-1",
        url: "/workspace-a/channels/channel-1?message=message-1",
      }),
    });
  });

  it("routes DM channel banners to the same channel detail path", async () => {
    const qc = createQueryClient();
    qc.setQueryData<Workspace[]>(workspaceKeys.list(), [workspace()]);
    qc.setQueryData(notificationPreferenceKeys.all("ws-a"), {
      preferences: { system_notifications: "all" },
    });
    qc.setQueryData<Channel[]>(channelKeys.list("ws-a"), [channel({ kind: "dm" })]);
    installBrowserNotification("granted");

    await handleChannelMessageNotification(qc, channelMessage(), "member-1");

    expect(webBanners[0]?.title).toBe("Agent Bot");
    expect(webBanners[0]?.options).toMatchObject({
      tag: "channel-1",
      data: expect.objectContaining({
        dmId: "channel-1",
        url: "/workspace-a/channels/channel-1?message=message-1",
      }),
    });
  });

  it("deep-links thread-reply banners into the thread panel (LRM-736)", async () => {
    const qc = createQueryClient();
    qc.setQueryData<Workspace[]>(workspaceKeys.list(), [workspace()]);
    qc.setQueryData(notificationPreferenceKeys.all("ws-a"), {
      preferences: { system_notifications: "all" },
    });
    qc.setQueryData<Channel[]>(channelKeys.list("ws-a"), [channel()]);
    installBrowserNotification("granted");

    await handleChannelMessageNotification(
      qc,
      channelMessage({ id: "reply-1", thread_root_message_id: "root-1" }),
      "member-1",
    );

    expect(webBanners[0]?.options).toMatchObject({
      tag: "channel-1",
      data: expect.objectContaining({
        channelId: "channel-1",
        itemId: "reply-1",
        threadId: "root-1",
        url: "/workspace-a/channels/channel-1?thread=root-1&message=reply-1",
      }),
    });
  });

  it("suppresses ambient banners for muted channels but still rings on @mention", async () => {
    const qc = createQueryClient();
    qc.setQueryData<Workspace[]>(workspaceKeys.list(), [workspace()]);
    qc.setQueryData(notificationPreferenceKeys.all("ws-a"), {
      preferences: { system_notifications: "all" },
    });
    qc.setQueryData<Channel[]>(channelKeys.list("ws-a"), [channel({ muted: true })]);
    installBrowserNotification("granted");

    await handleChannelMessageNotification(qc, channelMessage(), "member-1");
    expect(webBanners).toHaveLength(0);

    await handleChannelMessageNotification(
      qc,
      channelMessage({
        content: "hey [@Alice](mention://member/member-1)",
      }),
      "member-1",
    );
    expect(webBanners).toHaveLength(1);
    expect(webBanners[0]?.title).toBe("#general");
  });

  it("suppresses self-sent user messages even when unmuted", async () => {
    const qc = createQueryClient();
    qc.setQueryData<Workspace[]>(workspaceKeys.list(), [workspace()]);
    qc.setQueryData(notificationPreferenceKeys.all("ws-a"), {
      preferences: { system_notifications: "all" },
    });
    qc.setQueryData<Channel[]>(channelKeys.list("ws-a"), [channel()]);
    installBrowserNotification("granted");

    await handleChannelMessageNotification(
      qc,
      channelMessage({ type: "user", author_id: "member-1" }),
      "member-1",
    );

    expect(webBanners).toHaveLength(0);
  });

  it("suppresses banners for muted DMs unless @mentioned", async () => {
    const qc = createQueryClient();
    qc.setQueryData<Workspace[]>(workspaceKeys.list(), [workspace()]);
    qc.setQueryData(notificationPreferenceKeys.all("ws-a"), {
      preferences: { system_notifications: "all" },
    });
    qc.setQueryData<Channel[]>(channelKeys.list("ws-a"), [
      channel({ kind: "dm", muted: true }),
    ]);
    installBrowserNotification("granted");

    await handleChannelMessageNotification(qc, channelMessage(), "member-1");
    expect(webBanners).toHaveLength(0);

    await handleChannelMessageNotification(
      qc,
      channelMessage({
        content: "ping [@Alice](mention://member/member-1)",
      }),
      "member-1",
    );
    expect(webBanners).toHaveLength(1);
    expect(webBanners[0]?.options).toMatchObject({
      data: expect.objectContaining({ dmId: "channel-1" }),
    });
  });

  it("suppresses banners for channel message edits and deletes", async () => {
    const qc = createQueryClient();
    qc.setQueryData<Workspace[]>(workspaceKeys.list(), [workspace()]);
    qc.setQueryData(notificationPreferenceKeys.all("ws-a"), {
      preferences: { system_notifications: "all" },
    });
    qc.setQueryData<Channel[]>(channelKeys.list("ws-a"), [channel()]);
    installBrowserNotification("granted");

    await handleChannelMessageNotification(
      qc,
      channelMessage({ edited_at: "2026-05-18T00:01:00Z" }),
      "member-1",
    );
    await handleChannelMessageNotification(
      qc,
      channelMessage({ deleted_at: "2026-05-18T00:02:00Z" }),
      "member-1",
    );

    expect(webBanners).toHaveLength(0);
  });
});
