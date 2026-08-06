/**
 * @vitest-environment jsdom
 *
 * Verifies that marking a channel-backed DM read optimistically clears its
 * unread state in the dmKeys.list cache. Legacy chat sessions are no longer
 * returned from /api/dm.
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
    qc.setQueryData(dmKeys.list(WS_ID), [dmItem, other]);

    const { result } = renderHook(() => useMarkChannelRead(), { wrapper: createWrapper(qc) });

    await act(async () => {
      result.current.mutate("ch-1");
      // Let the microtask queue flush onMutate before asserting.
      await Promise.resolve();
    });

    const cached = qc.getQueryData<DMItem[]>(dmKeys.list(WS_ID));
    expect(cached?.find((d) => d.id === "ch-1")).toMatchObject({ unread: 0, manually_unread: false });
    expect(cached?.find((d) => d.id === "ch-2")).toMatchObject({ unread: 1 });
  });

  it("rolls back the dm cache on mutation error", async () => {
    markChannelRead.mockRejectedValue(new Error("network"));
    const dmItem = makeDmItem({ id: "ch-1", source: "dm_channel", unread: 2, manually_unread: true });
    qc.setQueryData(dmKeys.list(WS_ID), [dmItem]);

    const { result } = renderHook(() => useMarkChannelRead(), { wrapper: createWrapper(qc) });

    await act(async () => {
      result.current.mutate("ch-1");
      await new Promise((r) => setTimeout(r, 0));
    });

    const cached = qc.getQueryData<DMItem[]>(dmKeys.list(WS_ID));
    expect(cached?.find((d) => d.id === "ch-1")).toMatchObject({ unread: 2, manually_unread: true });
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
