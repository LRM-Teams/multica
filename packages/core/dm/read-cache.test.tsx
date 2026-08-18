/**
 * @vitest-environment jsdom
 *
 * Verifies that marking a conversation read optimistically clears the unified
 * conversations list. Legacy chat sessions are no longer returned from /api/dm.
 */
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import { act, renderHook } from "@testing-library/react";
import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import type { ReactNode } from "react";

import { setApiInstance } from "../api";
import type { ApiClient } from "../api/client";
import { useMarkChannelRead } from "../channels/mutations";
import { useMarkChatSessionRead } from "../chat/mutations";
import { dmKeys } from "./queries";
import type { DMItem } from "./types";
import { conversationKeys } from "../conversations";
import type { ConversationListResponse } from "../conversations";

vi.mock("../hooks", () => ({
  useWorkspaceId: () => "ws-1",
}));

vi.mock("../logger", async (importOriginal) => {
  const actual = await importOriginal<typeof import("../logger")>();
  return {
    ...actual,
    createLogger: () => ({ info: vi.fn(), debug: vi.fn(), error: vi.fn() }),
  };
});

const WS_ID = "ws-1";

function makeDmItem(overrides: Partial<DMItem>): DMItem {
  return {
    id: "dm-1",
    source: "dm_channel",
    peer: { type: "user", id: "user-1", name: "Alice" },
    unread: 0,
    updated_at: "2025-01-01T00:00:00Z",
    ...overrides,
  };
}

function createWrapper(qc: QueryClient) {
  return function Wrapper({ children }: { children: ReactNode }) {
    return <QueryClientProvider client={qc}>{children}</QueryClientProvider>;
  };
}

function deferred<T>() {
  let resolve!: (value: T) => void;
  let reject!: (reason?: unknown) => void;
  const promise = new Promise<T>((res, rej) => {
    resolve = res;
    reject = rej;
  });
  return { promise, resolve, reject };
}

describe("useMarkChannelRead — dm_channel optimistic clear", () => {
  let qc: QueryClient;
  let markChannelRead: ReturnType<typeof vi.fn<(id: string) => Promise<void>>>;

  beforeEach(() => {
    qc = new QueryClient({ defaultOptions: { queries: { retry: false }, mutations: { retry: false } } });
    markChannelRead = vi.fn().mockResolvedValue(undefined);
    setApiInstance({ markChannelRead } as unknown as ApiClient);
  });

  afterEach(() => {
    qc.clear();
    vi.restoreAllMocks();
  });

  it("clears unread and manually_unread optimistically for the matching dm_channel item", async () => {
    const dmItem = makeDmItem({ id: "ch-1", source: "dm_channel", unread: 3, manually_unread: true });
    const other = makeDmItem({ id: "ch-2", source: "dm_channel", unread: 1 });
    qc.setQueryData<{ pages: ConversationListResponse[]; pageParams: unknown[] }>(
      conversationKeys.list(WS_ID),
      {
        pages: [{ items: [{ kind: "dm", dm: dmItem }, { kind: "dm", dm: other }], next_cursor: undefined }],
        pageParams: [null],
      },
    );

    const { result } = renderHook(() => useMarkChannelRead(), { wrapper: createWrapper(qc) });

    await act(async () => {
      result.current.mutate("ch-1");
      // Let the async cache cancellation finish before asserting.
      await new Promise((resolve) => setTimeout(resolve, 0));
    });

    const conversation = qc.getQueryData<{ pages: ConversationListResponse[] }>(
      conversationKeys.list(WS_ID),
    );
    expect(conversation?.pages[0]?.items[0]?.dm).toMatchObject({
      unread: 0,
      manually_unread: false,
    });
    expect(conversation?.pages[0]?.items[1]?.dm).toMatchObject({ unread: 1 });
  });

  it("refetches the unified list when marking a conversation read fails", async () => {
    markChannelRead.mockRejectedValue(new Error("network"));
    const dmItem = makeDmItem({ id: "ch-1", source: "dm_channel", unread: 2, manually_unread: true });
    qc.setQueryData<{ pages: ConversationListResponse[]; pageParams: unknown[] }>(
      conversationKeys.list(WS_ID),
      {
        pages: [{ items: [{ kind: "dm", dm: dmItem }] }],
        pageParams: [null],
      },
    );

    const { result } = renderHook(() => useMarkChannelRead(), { wrapper: createWrapper(qc) });
    const invalidateQueries = vi.spyOn(qc, "invalidateQueries");

    await act(async () => {
      result.current.mutate("ch-1");
      await new Promise((r) => setTimeout(r, 0));
    });

    expect(invalidateQueries).toHaveBeenCalledWith({ queryKey: conversationKeys.list(WS_ID) });
  });

  it("clears a group channel in the unified cache", async () => {
    const request = deferred<void>();
    markChannelRead.mockImplementation(() => request.promise);
    const channel = {
      id: "channel-1",
      workspace_id: WS_ID,
      name: "general",
      kind: "group" as const,
      description: null,
      lark_chat_id: null,
      created_by: "user-1",
      created_at: "2025-01-01T00:00:00Z",
      updated_at: "2025-01-01T00:00:00Z",
      unread_count: 2,
      real_unread_count: 2,
      manually_unread: true,
    };
    qc.setQueryData<{ pages: ConversationListResponse[]; pageParams: unknown[] }>(
      conversationKeys.list(WS_ID),
      { pages: [{ items: [{ kind: "channel", channel }] }], pageParams: [null] },
    );

    const { result } = renderHook(() => useMarkChannelRead(), { wrapper: createWrapper(qc) });
    await act(async () => {
      result.current.mutate(channel.id);
      await Promise.resolve();
    });
    expect(qc.getQueryData<{ pages: ConversationListResponse[] }>(conversationKeys.list(WS_ID))
      ?.pages[0]?.items[0]?.channel).toMatchObject({ unread_count: 0, manually_unread: false });

    await act(async () => {
      request.reject(new Error("network"));
      await Promise.resolve();
    });
    expect(qc.getQueryData<{ pages: ConversationListResponse[] }>(conversationKeys.list(WS_ID))
      ?.pages[0]?.items[0]?.channel).toMatchObject({ unread_count: 0, manually_unread: false });
  });

  it("keeps the unified cache clear while concurrent reads settle", async () => {
    const first = deferred<void>();
    const second = deferred<void>();
    markChannelRead
      .mockImplementationOnce(() => first.promise)
      .mockImplementationOnce(() => second.promise);
    const dmItem = makeDmItem({ id: "ch-1", unread: 2, manually_unread: true });
    qc.setQueryData<{ pages: ConversationListResponse[]; pageParams: unknown[] }>(
      conversationKeys.list(WS_ID),
      { pages: [{ items: [{ kind: "dm", dm: dmItem }] }], pageParams: [null] },
    );

    const { result } = renderHook(() => useMarkChannelRead(), { wrapper: createWrapper(qc) });
    act(() => {
      result.current.mutate("ch-1");
      result.current.mutate("ch-1");
    });
    await act(async () => {
      await new Promise((resolve) => setTimeout(resolve, 0));
    });
    first.resolve();
    second.resolve();
    await act(async () => {
      await new Promise((resolve) => setTimeout(resolve, 0));
    });
    expect(qc.getQueryData<{ pages: ConversationListResponse[] }>(conversationKeys.list(WS_ID))
      ?.pages[0]?.items[0]?.dm).toMatchObject({
      unread: 0,
      manually_unread: false,
    });
  });
});

describe("useMarkChatSessionRead — DM list isolation", () => {
  let qc: QueryClient;
  let markChatSessionRead: ReturnType<typeof vi.fn<(id: string) => Promise<void>>>;

  beforeEach(() => {
    qc = new QueryClient({ defaultOptions: { queries: { retry: false }, mutations: { retry: false } } });
    markChatSessionRead = vi.fn().mockResolvedValue(undefined);
    setApiInstance({ markChatSessionRead } as unknown as ApiClient);
  });

  afterEach(() => {
    qc.clear();
    vi.restoreAllMocks();
  });

  it("does not mutate visible dm_channel rows while reading a legacy chat session", async () => {
    const dmItem = makeDmItem({ id: "ch-1", source: "dm_channel", unread: 1, manually_unread: true });
    const other = makeDmItem({ id: "ch-2", source: "dm_channel", unread: 0 });
    qc.setQueryData(dmKeys.list(WS_ID), [dmItem, other]);
    qc.setQueryData(["chat", "sessions", WS_ID], []);

    const { result } = renderHook(() => useMarkChatSessionRead(), { wrapper: createWrapper(qc) });

    await act(async () => {
      result.current.mutate("sess-1");
      await Promise.resolve();
    });

    const cached = qc.getQueryData<DMItem[]>(dmKeys.list(WS_ID));
    expect(cached?.find((d) => d.id === "ch-1")).toMatchObject({ unread: 1, manually_unread: true });
    expect(cached?.find((d) => d.id === "ch-2")).toMatchObject({ unread: 0 });
  });
});
