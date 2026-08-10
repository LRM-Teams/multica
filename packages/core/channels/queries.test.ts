import { beforeEach, describe, expect, it, vi } from "vitest";
import { QueryClient, type InfiniteData } from "@tanstack/react-query";
import type { ChannelMessage, ChannelMessagesPage } from "../types";

const listChannelMessagesPageMock = vi.fn().mockResolvedValue({
  messages: [],
  limit: 50,
  has_more: false,
});
vi.mock("../api", () => ({
  api: {
    listChannelMessagesPage: (...args: unknown[]) =>
      listChannelMessagesPageMock(...args),
  },
}));

import {
  CHANNEL_MESSAGES_VIRTUOSO_BASE_INDEX,
  channelKeys,
  channelMessagesFirstItemIndex,
  channelMessagesPageOptions,
  findChannelMessageMatchIndex,
  flattenChannelMessagePages,
  normalizeChannelMessages,
  patchChannelMessageReactionInCache,
  upsertChannelMessageInCache,
} from "./queries";

function page(ids: string[], extra: Partial<ChannelMessagesPage> = {}): ChannelMessagesPage {
  return {
    messages: ids.map((id) => ({ id }) as ChannelMessage),
    limit: 50,
    has_more: false,
    ...extra,
  };
}

describe("channelMessagesPageOptions — around_seq (task #340)", () => {
  beforeEach(() => listChannelMessagesPageMock.mockClear());

  it("default mode: initialPageParam is null and queryFn walks OLDER via before", async () => {
    const opts = channelMessagesPageOptions("c1");
    expect(opts.initialPageParam).toBeNull();

    await opts.queryFn!({ pageParam: null } as any);
    expect(listChannelMessagesPageMock).toHaveBeenCalledWith("c1", {
      before: null,
      limit: 50,
    });
  });

  it("around mode: initialPageParam is {around}, and queryFn dispatches around vs before", async () => {
    const opts = channelMessagesPageOptions("c1", { aroundSeq: 42 });
    expect(opts.initialPageParam).toEqual({ around: 42 });

    // page 0 — the around window
    await opts.queryFn!({ pageParam: { around: 42 } } as any);
    expect(listChannelMessagesPageMock).toHaveBeenLastCalledWith("c1", {
      around: 42,
      limit: 50,
    });

    // an older page uses a before cursor, never mistaken for around
    const cursor = { seq: 30, created_at: "t", id: "older-1" };
    await opts.queryFn!({ pageParam: cursor } as any);
    expect(listChannelMessagesPageMock).toHaveBeenLastCalledWith("c1", {
      before: cursor,
      limit: 50,
    });
  });

  it("getNextPageParam returns the older cursor only while has_more", () => {
    const opts = channelMessagesPageOptions("c1", { aroundSeq: 42 });
    const cursor = { seq: 5, created_at: "t", id: "x" };
    const more = opts.getNextPageParam!(page(["a"], { has_more: true, next_cursor: cursor }) as any, [], null, []);
    expect(more).toEqual(cursor);
    const none = opts.getNextPageParam!(page(["a"], { has_more: false, next_cursor: null }) as any, [], null, []);
    expect(none).toBeUndefined();
  });
});

describe("flatten + firstItemIndex", () => {
  it("flattens pages (reverse → ascending) with page0 as the newest window", () => {
    const data = {
      pages: [page(["a"]), page(["b", "c"])],
      pageParams: [],
    } as unknown as InfiniteData<ChannelMessagesPage>;
    // page0=[a] is the latest window; page1=[b,c] is the older page appended
    // behind it → ascending order is older-first: b, c, a.
    expect(flattenChannelMessagePages(data).map((m) => m.id)).toEqual(["b", "c", "a"]);
  });

  it("firstItemIndex = BASE - (messages older than page0)", () => {
    const data = {
      pages: [page(["a"]), page(["b", "c"])],
      pageParams: [],
    } as unknown as InfiniteData<ChannelMessagesPage>;
    expect(channelMessagesFirstItemIndex(data, true)).toBe(
      CHANNEL_MESSAGES_VIRTUOSO_BASE_INDEX - 2,
    );
    expect(channelMessagesFirstItemIndex(data, false)).toBe(0);
  });
});

describe("normalizeChannelMessages", () => {
  const base = {
    channel_id: "c1",
    workspace_id: "w1",
    type: "agent" as const,
    author_id: "agent-1",
    author_name: "前端工程师",
    content: "hi",
    created_at: "2026-07-21T10:00:00Z",
  };

  it("skips sparse undefined holes without throwing", () => {
    const normalized = normalizeChannelMessages([
      undefined as unknown as ChannelMessage,
      { ...base, id: "m1" } as ChannelMessage,
      null as unknown as ChannelMessage,
    ]);
    expect(normalized).toHaveLength(1);
  });

  it("keeps soft-deleted tombstones in the read model", () => {
    const normalized = normalizeChannelMessages([
      {
        ...base,
        id: "m1",
        deleted_at: "2026-07-22T04:00:00Z",
        content: "",
      } as ChannelMessage,
    ]);
    expect(normalized).toHaveLength(1);
    expect(normalized[0]?.deleted_at).toBe("2026-07-22T04:00:00Z");
  });
});

describe("findChannelMessageMatchIndex (optimistic ACK)", () => {
  it("matches an optimistic temp id when the ACK only carries client_message_id", () => {
    const optimistic = {
      id: "client-1",
      client_message_id: "client-1",
    } as ChannelMessage;
    const ack = {
      id: "server-1",
      client_message_id: "client-1",
    } as ChannelMessage;
    expect(findChannelMessageMatchIndex([optimistic], ack)).toBe(0);
  });

  it("matches a pending bubble by author+content when ACK omits client_message_id", () => {
    const optimistic = {
      id: "client-9",
      client_message_id: "client-9",
      author_id: "u1",
      content: "hi",
      local_send_status: "pending",
      thread_root_message_id: "root-1",
    } as ChannelMessage;
    const ack = {
      id: "server-9",
      author_id: "u1",
      content: "hi",
      client_message_id: null,
      thread_root_message_id: "root-1",
    } as ChannelMessage;
    expect(findChannelMessageMatchIndex([optimistic], ack)).toBe(0);
  });
});

describe("patchChannelMessageReactionInCache (#689 perf audit)", () => {
  function messageWithReactions(id: string, reactions: ChannelMessage["reactions"]): ChannelMessage {
    return { id, channel_id: "c1", reactions } as ChannelMessage;
  }

  it("appends a reaction to the matching message in the flat list cache, leaving others untouched", () => {
    const qc = new QueryClient();
    qc.setQueryData(channelKeys.messages("c1"), [
      messageWithReactions("m1", []),
      messageWithReactions("m2", [{ id: "r-existing", channel_id: "c1", message_id: "m2", actor_type: "member", actor_id: "u2", emoji: "👍", created_at: "t" }]),
    ]);

    patchChannelMessageReactionInCache(qc, "c1", "m1", (reactions) => [
      ...(reactions ?? []),
      { id: "r1", channel_id: "c1", message_id: "m1", actor_type: "member", actor_id: "u1", emoji: "🎉", created_at: "t" },
    ]);

    const cached = qc.getQueryData<ChannelMessage[]>(channelKeys.messages("c1")) ?? [];
    expect(cached[0]?.reactions).toEqual([
      { id: "r1", channel_id: "c1", message_id: "m1", actor_type: "member", actor_id: "u1", emoji: "🎉", created_at: "t" },
    ]);
    // m2 is a different object reference than what setQueryData originally
    // stored only if map() re-wraps it — the CONTENT must be untouched.
    expect(cached[1]?.reactions).toHaveLength(1);
    expect(cached[1]?.reactions?.[0]?.id).toBe("r-existing");
  });

  it("removes a reaction by (emoji, actor_type, actor_id) triple", () => {
    const qc = new QueryClient();
    qc.setQueryData(channelKeys.messages("c1"), [
      messageWithReactions("m1", [
        { id: "r1", channel_id: "c1", message_id: "m1", actor_type: "member", actor_id: "u1", emoji: "🎉", created_at: "t" },
        { id: "r2", channel_id: "c1", message_id: "m1", actor_type: "agent", actor_id: "a1", emoji: "🎉", created_at: "t" },
      ]),
    ]);

    patchChannelMessageReactionInCache(qc, "c1", "m1", (reactions) =>
      reactions?.filter((r) => !(r.emoji === "🎉" && r.actor_type === "member" && r.actor_id === "u1")),
    );

    const cached = qc.getQueryData<ChannelMessage[]>(channelKeys.messages("c1")) ?? [];
    expect(cached[0]?.reactions).toEqual([
      { id: "r2", channel_id: "c1", message_id: "m1", actor_type: "agent", actor_id: "a1", emoji: "🎉", created_at: "t" },
    ]);
  });

  it("patches the message inside every loaded page of the infinite cache", () => {
    const qc = new QueryClient();
    const data = {
      pages: [page(["a"]), page(["b", "c"])],
      pageParams: [],
    } as unknown as InfiniteData<ChannelMessagesPage>;
    qc.setQueryData(channelKeys.messagesPage("c1"), data);

    patchChannelMessageReactionInCache(qc, "c1", "b", () => [
      { id: "r1", channel_id: "c1", message_id: "b", actor_type: "member", actor_id: "u1", emoji: "👍", created_at: "t" },
    ]);

    const cached = qc.getQueryData<InfiniteData<ChannelMessagesPage>>(channelKeys.messagesPage("c1"));
    const patched = cached?.pages.flatMap((p) => p.messages).find((m) => m.id === "b");
    expect(patched?.reactions).toHaveLength(1);
    // Untouched siblings keep no reactions field.
    const sibling = cached?.pages.flatMap((p) => p.messages).find((m) => m.id === "c");
    expect(sibling?.reactions ?? undefined).toBeUndefined();
  });

  it("is a no-op when neither cache has the channel loaded (message never opened yet)", () => {
    const qc = new QueryClient();
    expect(() =>
      patchChannelMessageReactionInCache(qc, "never-opened", "m1", (r) => r),
    ).not.toThrow();
    expect(qc.getQueryData(channelKeys.messages("never-opened"))).toBeUndefined();
  });
});

describe("upsertChannelMessageInCache (LRM-271/273 ACK)", () => {
  it("drops the legacy wire avatar before a WS message enters the cache", () => {
    const incoming = {
      id: "server-avatar",
      channel_id: "c1",
      workspace_id: "w1",
      seq: 9,
      type: "agent",
      author_id: "agent-1",
      author_name: "Ronan",
      author_avatar_url: "/legacy/ws.png",
      reply_to: {
        id: "reply-1",
        type: "agent",
        author_id: "agent-1",
        author_name: "Ronan",
        author_avatar_url: "/legacy/reply.png",
        content: "reply",
        created_at: "2026-07-21T09:59:00Z",
      },
      quote: {
        messageId: "quote-1",
        status: "active",
        snapshot: {
          type: "agent",
          authorId: "agent-1",
          authorName: "Ronan",
          authorAvatarUrl: "/legacy/quote.png",
          content: "quote",
          createdAt: "2026-07-21T09:58:00Z",
        },
      },
      content: "hello",
      source: "multica",
      external_message_id: null,
      client_message_id: null,
      created_at: "2026-07-21T10:00:00Z",
    } as unknown as ChannelMessage;
    const qc = new QueryClient();
    qc.setQueryData(channelKeys.messagesPage("c1"), {
      pages: [{ messages: [], limit: 50, has_more: false }],
      pageParams: [null],
    });

    upsertChannelMessageInCache(qc, incoming);

    const cached = qc.getQueryData<{
      pages: Array<{ messages: Array<Record<string, unknown>> }>;
    }>(channelKeys.messagesPage("c1"));
    const message = cached?.pages[0]?.messages[0];
    expect(message).not.toHaveProperty("author_avatar_url");
    expect(message?.reply_to).not.toHaveProperty("author_avatar_url");
    expect((message?.quote as { snapshot: Record<string, unknown> }).snapshot).not.toHaveProperty(
      "authorAvatarUrl",
    );
  });

  it("strips leaked local_send_status and preserves client_message_id for stable list keys", () => {
    const qc = new QueryClient();
    const optimistic = {
      id: "client-1",
      channel_id: "c1",
      workspace_id: "w1",
      seq: 1,
      type: "user",
      author_id: "u1",
      author_name: "Alice",
      content: "hello",
      source: "multica",
      external_message_id: null,
      client_message_id: "client-1",
      created_at: "2026-07-22T05:00:00Z",
      local_send_status: "pending",
    } as ChannelMessage;
    qc.setQueryData(channelKeys.messages("c1"), [optimistic]);

    upsertChannelMessageInCache(qc, {
      ...optimistic,
      id: "server-1",
      seq: 42,
      client_message_id: null,
      // Simulate a buggy merge that left the client-only flag on the ACK.
      local_send_status: "pending",
    });

    const cached = qc.getQueryData<ChannelMessage[]>(channelKeys.messages("c1")) ?? [];
    expect(cached).toHaveLength(1);
    expect(cached[0]?.id).toBe("server-1");
    expect(cached[0]?.client_message_id).toBe("client-1");
    expect(cached[0]?.local_send_status ?? null).toBeNull();
  });
});
