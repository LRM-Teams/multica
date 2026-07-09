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
  flattenChannelMessagePages,
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
