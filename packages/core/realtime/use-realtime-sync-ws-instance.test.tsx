/**
 * @vitest-environment jsdom
 */
import { QueryClient, QueryClientProvider, type InfiniteData } from "@tanstack/react-query";
import { renderHook } from "@testing-library/react";
import type { ReactNode } from "react";
import { describe, expect, it, vi, beforeEach } from "vitest";
import type { WSClient } from "../api/ws-client";
import { channelKeys } from "../channels/queries";
import { runnerActivityKeys } from "../agents/queries";
import { researchKeys } from "../research/queries";
import { voiceCallKeys } from "../voice-calls/queries";
import type {
  ChannelMessage,
  ChannelMessagesPage,
  ChannelThreadMessagesPage,
  RunnerActivityResponse,
} from "../types";
import { useRealtimeSync, type RealtimeSyncStores } from "./use-realtime-sync";

vi.mock("../platform/workspace-storage", () => ({
  getCurrentWsId: () => "ws-1",
  getCurrentSlug: () => "test-ws",
}));

vi.mock("../paths", () => ({
  useHasOnboarded: () => true,
  resolvePostAuthDestination: () => "/",
}));

function createMockWs(): WSClient {
  return {
    on: vi.fn(() => () => {}),
    onAny: vi.fn(() => () => {}),
    onReconnect: vi.fn(() => () => {}),
  } as unknown as WSClient;
}

function createStores(): RealtimeSyncStores {
  return {
    authStore: Object.assign(() => ({}), {
      getState: () => ({ user: { id: "u1" } }),
      subscribe: () => () => {},
      setState: () => {},
      destroy: () => {},
    }),
  } as unknown as RealtimeSyncStores;
}

function createWrapper(qc: QueryClient) {
  // Named function (not arrow) so react/display-name lint rule passes —
  // anonymous render-fn components break that rule even in test files.
  return function Wrapper({ children }: { children: ReactNode }) {
    return <QueryClientProvider client={qc}>{children}</QueryClientProvider>;
  };
}

describe("useRealtimeSync — ws instance change", () => {
  let qc: QueryClient;
  let stores: RealtimeSyncStores;
  let invalidateSpy: ReturnType<typeof vi.spyOn>;

  beforeEach(() => {
    qc = new QueryClient({ defaultOptions: { queries: { retry: false } } });
    stores = createStores();
    invalidateSpy = vi.spyOn(qc, "invalidateQueries");
  });

  it("skips invalidation on first non-null ws instance", () => {
    const ws = createMockWs();
    renderHook(() => useRealtimeSync(ws, stores), {
      wrapper: createWrapper(qc),
    });

    // The main effect calls invalidateQueries for its own setup, but the
    // ws-instance-change effect should NOT have fired invalidation.
    // The only invalidateQueries calls should come from the main effect's
    // event handlers, not from the instance-change effect.
    // We verify by checking that no call was made with workspaceKeys.list()
    // pattern from the instance-change path (it logs a specific message).
    // Simpler: count calls — first mount with a ws should not trigger the
    // workspace-scoped bulk invalidation.
    expect(invalidateSpy).not.toHaveBeenCalled();
  });

  it("does not invalidate when ws goes from instance to null", () => {
    const ws1 = createMockWs();
    const { rerender } = renderHook(
      ({ ws }) => useRealtimeSync(ws, stores),
      { initialProps: { ws: ws1 as WSClient | null }, wrapper: createWrapper(qc) },
    );

    invalidateSpy.mockClear();
    rerender({ ws: null });

    expect(invalidateSpy).not.toHaveBeenCalled();
  });

  it("invalidates exactly once when a new ws instance appears after null gap", () => {
    const ws1 = createMockWs();
    const { rerender } = renderHook(
      ({ ws }) => useRealtimeSync(ws, stores),
      { initialProps: { ws: ws1 as WSClient | null }, wrapper: createWrapper(qc) },
    );

    // Simulate workspace switch: ws -> null -> new ws
    invalidateSpy.mockClear();
    rerender({ ws: null });
    expect(invalidateSpy).not.toHaveBeenCalled();

    const ws2 = createMockWs();
    rerender({ ws: ws2 });

    // Should have called invalidateQueries for all workspace-scoped keys
    // (15 workspace-scoped + 6 per-issue prefixes + 1 channel-issues prefix
    // (#562) + 1 session-scoped chat predicate + 1 workspaceKeys.list() = 24
    // calls; squads key removed in LRM-582; autopilot key removed in LRM-1050)
    expect(invalidateSpy).toHaveBeenCalledTimes(24);

    // Assert the KEY, not just the count (Ronan): the reconnect resync must
    // invalidate the channel Tasks board prefix (#562) so tasks changed while
    // disconnected refetch on recovery. A count-only check stays green if this
    // key silently disappears while another invalidation is added — the exact
    // regression this guards against.
    const invalidatedKeys = invalidateSpy.mock.calls.map(
      (call: [{ queryKey?: unknown }, ...unknown[]]) => call[0].queryKey,
    );
    expect(invalidatedKeys).toContainEqual(channelKeys.issuesRoot());
    expect(invalidatedKeys).toContainEqual(runnerActivityKeys.root("ws-1"));
  });

  it("does not re-invalidate when rerendered with the same ws instance", () => {
    const ws1 = createMockWs();
    const { rerender } = renderHook(
      ({ ws }) => useRealtimeSync(ws, stores),
      { initialProps: { ws: ws1 as WSClient | null }, wrapper: createWrapper(qc) },
    );

    invalidateSpy.mockClear();
    // Rerender with same instance
    rerender({ ws: ws1 });

    expect(invalidateSpy).not.toHaveBeenCalled();
  });

  it("patches runner activity without invalidating agent queries", () => {
    vi.useFakeTimers();
    try {
      const ws = createMockWs();
      renderHook(() => useRealtimeSync(ws, stores), {
        wrapper: createWrapper(qc),
      });
      const activity: RunnerActivityResponse = {
        summary: { label: "Running command...", tone: "info", visibility: "visible" },
        timeline: [{
          id: "entry-1",
          occurred_at: "2026-08-10T00:00:00Z",
          title: "Running command...",
          tone: "info",
          body_kind: "text",
          body: "Safe narrative",
        }],
      };
      const key = runnerActivityKeys.all("ws-1", "agent-1");
      qc.setQueryData(key, { summary: null, timeline: [] });

      const activityRegistrations = (ws.on as ReturnType<typeof vi.fn>).mock.calls.filter(
        ([eventName]) => eventName === "agent:activity",
      );
      const activityHandler = activityRegistrations[0]?.[1] as
        | ((payload: unknown) => void)
        | undefined;
      const anyHandler = (ws.onAny as ReturnType<typeof vi.fn>).mock.calls[0]?.[0] as
        | ((message: { type: string; payload: unknown }) => void)
        | undefined;

      expect(activityRegistrations).toHaveLength(1);
      expect(activityHandler).toBeDefined();
      invalidateSpy.mockClear();
      activityHandler?.({ agent_id: "agent-1", activity });
      anyHandler?.({ type: "agent:activity", payload: { agent_id: "agent-1", activity } });
      vi.advanceTimersByTime(100);

      expect(qc.getQueryData(key)).toEqual(activity);
      expect(qc.getQueryState(key)?.isInvalidated).toBe(false);
      expect(invalidateSpy).not.toHaveBeenCalled();
    } finally {
      vi.useRealTimers();
    }
  });

  it("invalidates chat, pins, labels, invitations, and session-scoped chat queries on ws instance change", () => {
    const ws1 = createMockWs();
    const { rerender } = renderHook(
      ({ ws }) => useRealtimeSync(ws, stores),
      { initialProps: { ws: ws1 as WSClient | null }, wrapper: createWrapper(qc) },
    );

    invalidateSpy.mockClear();
    rerender({ ws: null });

    const ws2 = createMockWs();
    rerender({ ws: ws2 });

    const calls = invalidateSpy.mock.calls.map((call: [{ queryKey?: unknown }, ...unknown[]]) => call[0].queryKey);
    expect(calls).toContainEqual(["chat", "ws-1"]);
    expect(
      invalidateSpy.mock.calls.some(
        (call: [{ predicate?: unknown }, ...unknown[]]) => typeof call[0].predicate === "function",
      ),
    ).toBe(true);
    expect(calls).toContainEqual(["labels", "ws-1"]);
    expect(calls).toContainEqual(["workspaces", "ws-1", "invitations"]);
    expect(calls).toContainEqual(voiceCallKeys.all("ws-1"));
  });

  it("invalidates per-issue caches (no wsId in key) on ws instance change", () => {
    // These keys are not under the ["issues", wsId] prefix, so they need
    // their own invalidation on recovery — otherwise events missed while
    // disconnected leave them stale forever (staleTime: Infinity, #3953).
    const ws1 = createMockWs();
    const { rerender } = renderHook(
      ({ ws }) => useRealtimeSync(ws, stores),
      { initialProps: { ws: ws1 as WSClient | null }, wrapper: createWrapper(qc) },
    );

    invalidateSpy.mockClear();
    rerender({ ws: null });

    const ws2 = createMockWs();
    rerender({ ws: ws2 });

    const calls = invalidateSpy.mock.calls.map((call: [{ queryKey?: unknown }, ...unknown[]]) => call[0].queryKey);
    expect(calls).toContainEqual(["issues", "timeline"]);
    expect(calls).toContainEqual(["issues", "reactions"]);
    expect(calls).toContainEqual(["issues", "subscribers"]);
    expect(calls).toContainEqual(["issues", "usage"]);
    expect(calls).toContainEqual(["issues", "attachments"]);
    expect(calls).toContainEqual(["issues", "tasks"]);
  });

  it("upserts thread replies without invalidating the thread query (LRM-271/273)", () => {
    const ws = createMockWs();
    renderHook(() => useRealtimeSync(ws, stores), {
      wrapper: createWrapper(qc),
    });

    const channelMessageHandler = (ws.on as ReturnType<typeof vi.fn>).mock.calls.find(
      ([eventName]) => eventName === "channel:message",
    )?.[1] as ((payload: ChannelMessage) => void) | undefined;
    expect(channelMessageHandler).toBeDefined();

    qc.setQueryData<InfiniteData<ChannelMessagesPage>>(channelKeys.messagesPage("channel-1"), {
      pages: [
        {
          messages: [channelMessage("root-old")],
          limit: 50,
          has_more: false,
          next_cursor: null,
        },
      ],
      pageParams: [null],
    });
    qc.setQueryData(channelKeys.messageThread("channel-1", "root-old"), {
      messages: [channelMessage("root-old")],
    });

    channelMessageHandler?.(channelMessage("root-new"));

    expect(
      qc
        .getQueryData<InfiniteData<ChannelMessagesPage>>(channelKeys.messagesPage("channel-1"))
        ?.pages[0]?.messages.map((message) => message.id),
    ).toEqual(["root-old", "root-new"]);
    // A full canonical payload is immediately visible, and the normal
    // channel timeline React Query refresh follows so realtime data never
    // becomes the durable client-side truth.
    expect(invalidateSpy).toHaveBeenCalledWith({
      queryKey: channelKeys.messages("channel-1"),
    });
    expect(invalidateSpy).toHaveBeenCalledWith({
      queryKey: channelKeys.messagesPage("channel-1"),
    });

    const invalidateBeforeThreadReply = invalidateSpy.mock.calls.length;
    channelMessageHandler?.(channelMessage("reply-1", { thread_root_message_id: "root-old" }));

    expect(
      qc
        .getQueryData<InfiniteData<ChannelMessagesPage>>(channelKeys.messagesPage("channel-1"))
        ?.pages[0]?.messages.map((message) => message.id),
    ).toEqual(["root-old", "root-new"]);
    // Main timeline still refreshes for root reply-count metadata.
    expect(invalidateSpy.mock.calls.length).toBeGreaterThan(invalidateBeforeThreadReply);
    expect(invalidateSpy).toHaveBeenCalledWith({
      queryKey: channelKeys.messagesPage("channel-1"),
    });
    // Thread panel uses upsert only — no post-ACK invalidate that races/flashes.
    expect(invalidateSpy).not.toHaveBeenCalledWith({
      queryKey: channelKeys.messageThread("channel-1", "root-old"),
    });
    expect(
      (
        qc.getQueryData<{ messages: ChannelMessage[] }>(
          channelKeys.messageThread("channel-1", "root-old"),
        )?.messages ?? []
      ).map((message) => message.id),
    ).toEqual(["root-old", "reply-1"]);
  });

  it("upserts server-enriched voice messages without a second message event", () => {
    const ws = createMockWs();
    renderHook(() => useRealtimeSync(ws, stores), {
      wrapper: createWrapper(qc),
    });
    const updatedHandler = (ws.on as ReturnType<typeof vi.fn>).mock.calls.find(
      ([eventName]) => eventName === "channel:message_updated",
    )?.[1] as ((payload: ChannelMessage) => void) | undefined;
    expect(updatedHandler).toBeDefined();

    qc.setQueryData<ChannelMessage[]>(channelKeys.messages("channel-1"), [
      channelMessage("voice-1", {
        content: "",
        parts: [{
          type: "voice",
          attachment_id: "audio-1",
          transcription_status: "pending",
        }],
      }),
    ]);

    updatedHandler?.(channelMessage("voice-1", {
      content: "server transcript",
      parts: [
        { type: "text", text: "server transcript" },
        {
          type: "voice",
          attachment_id: "audio-1",
          transcription_status: "completed",
        },
      ],
    }));

    expect(qc.getQueryData<ChannelMessage[]>(channelKeys.messages("channel-1"))?.[0]).toMatchObject({
      id: "voice-1",
      content: "server transcript",
      parts: expect.arrayContaining([
        expect.objectContaining({ type: "voice", transcription_status: "completed" }),
      ]),
    });
  });

  it("removes deleted root messages without replies but keeps tombstone roots with replies", () => {
    const ws = createMockWs();
    renderHook(() => useRealtimeSync(ws, stores), {
      wrapper: createWrapper(qc),
    });

    const channelMessageHandler = (ws.on as ReturnType<typeof vi.fn>).mock.calls.find(
      ([eventName]) => eventName === "channel:message",
    )?.[1] as ((payload: ChannelMessage) => void) | undefined;
    expect(channelMessageHandler).toBeDefined();

    qc.setQueryData<InfiniteData<ChannelMessagesPage>>(channelKeys.messagesPage("channel-1"), {
      pages: [
        {
          messages: [channelMessage("root-old")],
          limit: 50,
          has_more: false,
          next_cursor: null,
        },
      ],
      pageParams: [null],
    });

    channelMessageHandler?.(
      channelMessage("root-old", { deleted_at: "2026-07-01T00:01:00Z", thread_reply_count: 0 }),
    );

    expect(
      qc
        .getQueryData<InfiniteData<ChannelMessagesPage>>(channelKeys.messagesPage("channel-1"))
        ?.pages[0]?.messages.map((message) => message.id),
    ).toEqual([]);

    qc.setQueryData<InfiniteData<ChannelMessagesPage>>(channelKeys.messagesPage("channel-1"), {
      pages: [
        {
          messages: [channelMessage("root-old")],
          limit: 50,
          has_more: false,
          next_cursor: null,
        },
      ],
      pageParams: [null],
    });

    channelMessageHandler?.(
      channelMessage("root-old", { deleted_at: "2026-07-01T00:02:00Z", thread_reply_count: 2 }),
    );

    expect(
      qc
        .getQueryData<InfiniteData<ChannelMessagesPage>>(channelKeys.messagesPage("channel-1"))
        ?.pages[0]?.messages.map((message) => [message.id, message.deleted_at]),
    ).toEqual([["root-old", "2026-07-01T00:02:00Z"]]);
  });

  it("upserts thread reply edits and deletes into the cached thread panel", () => {
    const ws = createMockWs();
    renderHook(() => useRealtimeSync(ws, stores), {
      wrapper: createWrapper(qc),
    });

    const channelMessageHandler = (ws.on as ReturnType<typeof vi.fn>).mock.calls.find(
      ([eventName]) => eventName === "channel:message",
    )?.[1] as ((payload: ChannelMessage) => void) | undefined;
    expect(channelMessageHandler).toBeDefined();

    qc.setQueryData<ChannelThreadMessagesPage>(channelKeys.messageThread("channel-1", "root-old"), {
      messages: [
        channelMessage("root-old", { thread_reply_count: 1 }),
        channelMessage("reply-1", { thread_root_message_id: "root-old", content: "old reply" }),
      ],
      next_cursor: null,
    });

    channelMessageHandler?.(
      channelMessage("reply-1", {
        thread_root_message_id: "root-old",
        content: "edited reply",
        edited_at: "2026-07-01T00:01:00Z",
      }),
    );

    expect(
      qc
        .getQueryData<ChannelThreadMessagesPage>(channelKeys.messageThread("channel-1", "root-old"))
        ?.messages.map((message) => [message.id, message.content, message.edited_at]),
    ).toEqual([
      ["root-old", "root-old", undefined],
      ["reply-1", "edited reply", "2026-07-01T00:01:00Z"],
    ]);

    channelMessageHandler?.(
      channelMessage("reply-1", {
        thread_root_message_id: "root-old",
        deleted_at: "2026-07-01T00:02:00Z",
        thread_reply_count: 0,
      }),
    );

    expect(
      qc
        .getQueryData<ChannelThreadMessagesPage>(channelKeys.messageThread("channel-1", "root-old"))
        ?.messages.map((message) => message.id),
    ).toEqual(["root-old"]);
  });

  it("invalidates the paged (and thread) channel message cache when a reaction changes", () => {
    const ws = createMockWs();
    renderHook(() => useRealtimeSync(ws, stores), {
      wrapper: createWrapper(qc),
    });

    const reactionAddedHandler = (ws.on as ReturnType<typeof vi.fn>).mock.calls.find(
      ([eventName]) => eventName === "channel_reaction:added",
    )?.[1] as ((payload: { channel_id: string; message_id: string }) => void) | undefined;
    const reactionRemovedHandler = (ws.on as ReturnType<typeof vi.fn>).mock.calls.find(
      ([eventName]) => eventName === "channel_reaction:removed",
    )?.[1] as ((payload: { channel_id: string; message_id: string }) => void) | undefined;
    expect(reactionAddedHandler).toBeDefined();
    expect(reactionRemovedHandler).toBeDefined();

    invalidateSpy.mockClear();
    reactionAddedHandler?.({ channel_id: "channel-1", message_id: "msg-1" });

    // Before the fix this only invalidated channelKeys.messages, which nothing
    // renders from — the UI reads channelKeys.messagesPage, so another user's
    // reaction never showed up live.
    expect(invalidateSpy).toHaveBeenCalledWith({ queryKey: channelKeys.messagesPage("channel-1") });
    expect(invalidateSpy).toHaveBeenCalledWith({ queryKey: ["channel-message-thread", "channel-1"] });

    invalidateSpy.mockClear();
    reactionRemovedHandler?.({ channel_id: "channel-1", message_id: "msg-1" });

    expect(invalidateSpy).toHaveBeenCalledWith({ queryKey: channelKeys.messagesPage("channel-1") });
    expect(invalidateSpy).toHaveBeenCalledWith({ queryKey: ["channel-message-thread", "channel-1"] });
  });

  it("does not seed a fake-fresh page cache for a channel that has never been opened", () => {
    const ws = createMockWs();
    renderHook(() => useRealtimeSync(ws, stores), {
      wrapper: createWrapper(qc),
    });

    const channelMessageHandler = (ws.on as ReturnType<typeof vi.fn>).mock.calls.find(
      ([eventName]) => eventName === "channel:message",
    )?.[1] as ((payload: ChannelMessage) => void) | undefined;

    channelMessageHandler?.(
      channelMessage("msg-in-unopened-channel", { channel_id: "channel-never-opened" }),
    );

    // Seeding a single-message page here would mark the query "fresh" under
    // staleTime: Infinity, so opening the channel later would skip the real
    // fetch and permanently show only the message(s) caught by this upsert.
    expect(
      qc.getQueryData<InfiniteData<ChannelMessagesPage>>(
        channelKeys.messagesPage("channel-never-opened"),
      ),
    ).toBeUndefined();
  });

  it("subscribes product-round events and cleans up the handler", () => {
    const productRoundUnsubscribe = vi.fn();
    const ws = {
      on: vi.fn((eventName: string) =>
        eventName === "research_session:product_round"
          ? productRoundUnsubscribe
          : () => {},
      ),
      onAny: vi.fn(() => () => {}),
      onReconnect: vi.fn(() => () => {}),
    } as unknown as WSClient;
    const { unmount } = renderHook(() => useRealtimeSync(ws, stores), {
      wrapper: createWrapper(qc),
    });

    const productRoundHandler = (ws.on as ReturnType<typeof vi.fn>).mock.calls.find(
      ([eventName]) => eventName === "research_session:product_round",
    )?.[1] as ((payload: unknown) => void) | undefined;
    expect(productRoundHandler).toBeDefined();

    productRoundHandler?.({
      session_id: "session-1",
      card: { id: "round-1", round_number: 1, decision: "continue" },
    });

    expect(qc.getQueryData(researchKeys.productRounds("ws-1", "session-1"))).toEqual({
      rounds: [expect.objectContaining({ id: "round-1", session_id: "session-1" })],
    });

    unmount();
    expect(productRoundUnsubscribe).toHaveBeenCalledTimes(1);
  });
});

function channelMessage(
  id: string,
  overrides: Partial<ChannelMessage> = {},
): ChannelMessage {
  const seq = overrides.seq ?? 1;
  return {
    id,
    channel_id: "channel-1",
    workspace_id: "ws-1",
    type: "user",
    author_id: "author-1",
    author_name: "Author",
    content: id,
    source: "multica",
    external_message_id: null,
    client_message_id: null,
    created_at: "2026-07-01T00:00:00Z",
    thread_root_message_id: null,
    thread_reply_count: 0,
    thread_last_reply_at: null,
    thread_unread_count: 0,
    thread_followed: false,
    reactions: [],
    ...overrides,
    seq,
  };
}
