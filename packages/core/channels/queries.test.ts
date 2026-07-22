import { beforeEach, describe, expect, it, vi } from "vitest";
import type { InfiniteData } from "@tanstack/react-query";
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
  channelMessagesFirstItemIndex,
  channelMessagesPageOptions,
  enrichChannelMessagesPreservingAvatars,
  findChannelMessageMatchIndex,
  flattenChannelMessagePages,
  withPreservedAuthorAvatar,
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

describe("withPreservedAuthorAvatar (LRM-202 / LRM-218)", () => {
  const base = {
    id: "m2",
    channel_id: "c1",
    workspace_id: "w1",
    type: "agent" as const,
    author_id: "agent-1",
    author_name: "前端工程师",
    content: "hi",
    created_at: "2026-07-21T10:00:00Z",
  };

  it("keeps an incoming author_avatar_url", () => {
    const incoming = { ...base, author_avatar_url: "/uploads/new.png" } as ChannelMessage;
    const existing = { ...base, id: "m2", author_avatar_url: "/uploads/old.png" } as ChannelMessage;
    expect(withPreservedAuthorAvatar(incoming, existing, [existing]).author_avatar_url).toBe(
      "/uploads/new.png",
    );
  });

  it("preserves the cached row avatar when the WS payload omits it", () => {
    const incoming = { ...base, author_avatar_url: null } as ChannelMessage;
    const existing = { ...base, author_avatar_url: "/uploads/agent.png" } as ChannelMessage;
    expect(withPreservedAuthorAvatar(incoming, existing, [existing]).author_avatar_url).toBe(
      "/uploads/agent.png",
    );
  });

  it("backfills from an earlier same-author bubble so consecutive messages stay consistent", () => {
    const prior = {
      ...base,
      id: "m1",
      author_avatar_url: "/uploads/agent.png",
    } as ChannelMessage;
    const incoming = { ...base, id: "m2", author_avatar_url: null } as ChannelMessage;
    expect(withPreservedAuthorAvatar(incoming, undefined, [prior]).author_avatar_url).toBe(
      "/uploads/agent.png",
    );
  });
});

describe("enrichChannelMessagesPreservingAvatars (LRM-218)", () => {
  const base = {
    channel_id: "c1",
    workspace_id: "w1",
    type: "agent" as const,
    author_id: "agent-1",
    author_name: "前端工程师",
    content: "hi",
    created_at: "2026-07-21T10:00:00Z",
  };

  it("backfills avatars across a refetched list when later rows omit the URL", () => {
    const enriched = enrichChannelMessagesPreservingAvatars([
      { ...base, id: "m1", author_avatar_url: "/uploads/agent.png" } as ChannelMessage,
      { ...base, id: "m2", author_avatar_url: null } as ChannelMessage,
      { ...base, id: "m3", author_avatar_url: null } as ChannelMessage,
    ]);
    expect(enriched.map((m) => m.author_avatar_url)).toEqual([
      "/uploads/agent.png",
      "/uploads/agent.png",
      "/uploads/agent.png",
    ]);
  });

  it("skips sparse undefined holes without throwing", () => {
    const enriched = enrichChannelMessagesPreservingAvatars([
      undefined as unknown as ChannelMessage,
      { ...base, id: "m1", author_avatar_url: "/uploads/agent.png" } as ChannelMessage,
      null as unknown as ChannelMessage,
    ]);
    expect(enriched).toHaveLength(1);
    expect(enriched[0]?.author_avatar_url).toBe("/uploads/agent.png");
  });
});

describe("findChannelMessageMatchIndex (optimistic ACK)", () => {
  it("matches an optimistic temp id when the ACK only carries client_message_id", () => {
    const optimistic = {
      id: "client-1",
      client_message_id: "client-1",
      author_avatar_url: "/uploads/me.png",
    } as ChannelMessage;
    const ack = {
      id: "server-1",
      client_message_id: "client-1",
      author_avatar_url: null,
    } as ChannelMessage;
    expect(findChannelMessageMatchIndex([optimistic], ack)).toBe(0);
  });
});
