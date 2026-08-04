// @vitest-environment jsdom

import { act, renderHook, waitFor } from "@testing-library/react";
import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { type ReactNode } from "react";
import { beforeEach, describe, expect, it, vi } from "vitest";
import type { Channel } from "../types";
import type { DMItem } from "../dm/types";
import { dmKeys } from "../dm/queries";
import { channelKeys } from "./queries";
import { useMarkChannelRead } from "./mutations";

/**
 * LRM-1296 (1260-P1) — opening a conversation marks it read on EVERY switch.
 * The old `onSuccess` invalidated `channels.list` + `dm.list`, so each switch
 * fired two extra full-list refetches (both server-side aggregate unread +
 * last_message enrichments) concurrently with the message page that blocks
 * first paint. The read receipt only zeroes THIS row's unread — patch it
 * instead of re-pulling both lists.
 */

const apiMock = vi.hoisted(() => ({ markChannelRead: vi.fn() }));
vi.mock("../api", () => ({ api: apiMock }));
vi.mock("../hooks", () => ({ useWorkspaceId: () => "ws-1" }));

function channel(overrides: Partial<Channel> = {}): Channel {
  return {
    id: "channel-1",
    workspace_id: "ws-1",
    kind: "group",
    name: "pr-frontend",
    description: null,
    lark_chat_id: null,
    created_by: "user-1",
    created_at: "2026-08-04T00:00:00Z",
    updated_at: "2026-08-04T00:00:00Z",
    unread_count: 7,
    real_unread_count: 7,
    mention_unread_count: 2,
    manually_unread: true,
    last_read_seq: 40,
    ...overrides,
  } as Channel;
}

function dm(overrides: Partial<DMItem> = {}): DMItem {
  return {
    id: "dm-1",
    source: "dm_channel",
    peer: { type: "user", id: "user-2", name: "Peer" } as DMItem["peer"],
    unread: 3,
    real_unread: 3,
    manually_unread: true,
    has_mention: true,
    updated_at: "2026-08-04T00:00:00Z",
    last_read_seq: 12,
    ...overrides,
  };
}

function harness() {
  const queryClient = new QueryClient({
    defaultOptions: { queries: { retry: false }, mutations: { retry: false } },
  });
  const refetches: string[] = [];
  queryClient.setQueryDefaults(channelKeys.list("ws-1"), {
    queryFn: () => {
      refetches.push("channels");
      return [] as Channel[];
    },
  });
  queryClient.setQueryDefaults(dmKeys.list("ws-1"), {
    queryFn: () => {
      refetches.push("dms");
      return [] as DMItem[];
    },
  });
  queryClient.setQueryData<Channel[]>(channelKeys.list("ws-1"), [
    channel(),
    channel({ id: "channel-2", name: "other", unread_count: 5, real_unread_count: 5 }),
  ]);
  queryClient.setQueryData<DMItem[]>(dmKeys.list("ws-1"), [
    dm(),
    dm({ id: "dm-2", unread: 4, real_unread: 4 }),
  ]);
  const wrapper = ({ children }: { children: ReactNode }) => (
    <QueryClientProvider client={queryClient}>{children}</QueryClientProvider>
  );
  return { queryClient, wrapper, refetches };
}

const channels = (qc: QueryClient) => qc.getQueryData<Channel[]>(channelKeys.list("ws-1"))!;
const dms = (qc: QueryClient) => qc.getQueryData<DMItem[]>(dmKeys.list("ws-1"))!;

describe("useMarkChannelRead — incremental unread (LRM-1296)", () => {
  beforeEach(() => {
    apiMock.markChannelRead.mockReset();
    apiMock.markChannelRead.mockResolvedValue({ ok: true, previous_last_read_seq: 40 });
  });

  it("zeroes the opened channel row's unread without refetching either list", async () => {
    const { queryClient, wrapper, refetches } = harness();
    const { result } = renderHook(() => useMarkChannelRead(), { wrapper });

    act(() => result.current.mutate("channel-1"));
    await waitFor(() => expect(result.current.isSuccess).toBe(true));

    const [opened, other] = channels(queryClient) as [Channel, Channel];
    expect(opened.unread_count).toBe(0);
    expect(opened.real_unread_count).toBe(0);
    expect(opened.mention_unread_count).toBe(0);
    expect(opened.manually_unread).toBe(false);
    // Entry-frozen divider cursor: the read receipt carries no new seq, so the
    // cached cursor must be left alone rather than guessed.
    expect(opened.last_read_seq).toBe(40);
    // Untouched sibling rows keep their badges.
    expect(other.unread_count).toBe(5);

    expect(queryClient.getQueryState(channelKeys.list("ws-1"))?.isInvalidated).toBe(false);
    expect(queryClient.getQueryState(dmKeys.list("ws-1"))?.isInvalidated).toBe(false);
    expect(refetches).toEqual([]);
  });

  it("zeroes the opened DM row's unread without refetching either list", async () => {
    const { queryClient, wrapper, refetches } = harness();
    const { result } = renderHook(() => useMarkChannelRead(), { wrapper });

    act(() => result.current.mutate("dm-1"));
    await waitFor(() => expect(result.current.isSuccess).toBe(true));

    const [opened, other] = dms(queryClient) as [DMItem, DMItem];
    expect(opened.unread).toBe(0);
    expect(opened.real_unread).toBe(0);
    expect(opened.manually_unread).toBe(false);
    expect(opened.has_mention).toBe(false);
    expect(opened.last_read_seq).toBe(12);
    expect(other.unread).toBe(4);
    expect(refetches).toEqual([]);
  });

  it("rolls both lists back when the read receipt fails", async () => {
    apiMock.markChannelRead.mockRejectedValue(new Error("network"));
    const { queryClient, wrapper } = harness();
    const { result } = renderHook(() => useMarkChannelRead(), { wrapper });

    act(() => result.current.mutate("channel-1"));
    await waitFor(() => expect(result.current.isError).toBe(true));

    expect(channels(queryClient)[0]!.unread_count).toBe(7);
    expect(channels(queryClient)[0]!.manually_unread).toBe(true);
    expect(dms(queryClient)[0]!.unread).toBe(3);
  });
});
