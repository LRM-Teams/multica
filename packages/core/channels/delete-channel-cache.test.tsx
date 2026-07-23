/**
 * @vitest-environment jsdom
 *
 * LRM-485 — permanent Delete must clear BOTH the active channel list and the
 * Archived sidebar cache. Invalidating only `channelKeys.list` left ghost
 * entries under Archived (N) after hard-deleting an already-archived channel
 * (Delete ≠ Archive / soft-delete residue).
 */
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import { act, renderHook, waitFor } from "@testing-library/react";
import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import type { ReactNode } from "react";

import { setApiInstance } from "../api";
import type { ApiClient } from "../api/client";
import type { Channel } from "../types";
import { channelKeys } from "./queries";
import { useDeleteChannel } from "./mutations";

vi.mock("../hooks", () => ({
  useWorkspaceId: () => "ws-1",
}));

function createWrapper(qc: QueryClient) {
  return function Wrapper({ children }: { children: ReactNode }) {
    return <QueryClientProvider client={qc}>{children}</QueryClientProvider>;
  };
}

function channel(overrides: Partial<Channel> = {}): Channel {
  return {
    id: "chan-1",
    workspace_id: "ws-1",
    name: "斗地主人类沟通群",
    description: null,
    lark_chat_id: null,
    created_by: "user-1",
    created_at: "2026-07-01T00:00:00Z",
    updated_at: "2026-07-01T00:00:00Z",
    kind: "group",
    system_key: null,
    archived_at: null,
    archived_by: null,
    pinned_at: null,
    muted_at: null,
    muted: false,
    unread_count: 0,
    real_unread_count: 0,
    mention_unread_count: 0,
    manually_unread: false,
    members: [],
    ...overrides,
  };
}

describe("useDeleteChannel cache (LRM-485)", () => {
  let qc: QueryClient;
  let deleteChannel: ReturnType<typeof vi.fn>;

  beforeEach(() => {
    qc = new QueryClient({ defaultOptions: { queries: { retry: false } } });
    deleteChannel = vi.fn().mockResolvedValue(undefined);
    setApiInstance({ deleteChannel } as unknown as ApiClient);
  });

  afterEach(() => {
    setApiInstance(undefined as unknown as ApiClient);
    vi.clearAllMocks();
  });

  it("drops the channel from active and archived lists on success", async () => {
    const active = channel({ id: "keep-active", archived_at: null });
    const archivedTarget = channel({
      id: "archived-gone",
      name: "斗地主人类沟通群",
      archived_at: "2026-07-23T10:00:00Z",
    });
    const archivedOther = channel({
      id: "archived-keep",
      name: "other",
      archived_at: "2026-07-20T00:00:00Z",
    });
    qc.setQueryData(channelKeys.list("ws-1"), [active, archivedTarget]);
    qc.setQueryData(channelKeys.archivedList("ws-1"), [archivedTarget, archivedOther]);

    const invalidateSpy = vi.spyOn(qc, "invalidateQueries");
    const { result } = renderHook(() => useDeleteChannel(), {
      wrapper: createWrapper(qc),
    });

    await act(async () => {
      await result.current.mutateAsync("archived-gone");
    });

    await waitFor(() => expect(deleteChannel).toHaveBeenCalledWith("archived-gone"));

    expect(qc.getQueryData(channelKeys.list("ws-1"))).toEqual([active]);
    expect(qc.getQueryData(channelKeys.archivedList("ws-1"))).toEqual([archivedOther]);
    expect(invalidateSpy).toHaveBeenCalledWith({
      queryKey: channelKeys.all("ws-1"),
    });
  });

  it("does not call archiveChannel (hard delete only)", async () => {
    const archiveChannel = vi.fn();
    setApiInstance({ deleteChannel, archiveChannel } as unknown as ApiClient);

    const { result } = renderHook(() => useDeleteChannel(), {
      wrapper: createWrapper(qc),
    });

    await act(async () => {
      await result.current.mutateAsync("chan-1");
    });

    expect(deleteChannel).toHaveBeenCalledWith("chan-1");
    expect(archiveChannel).not.toHaveBeenCalled();
  });
});
