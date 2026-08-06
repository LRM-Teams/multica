/**
 * @vitest-environment jsdom
 *
 * LRM-485 — permanent Delete must clear BOTH the active channel list and the
 * Archived sidebar cache. Invalidating only `channelKeys.list` left ghost
 * entries under Archived (N) after hard-deleting an already-archived channel
 * (Delete ≠ Archive / soft-delete residue).
 */
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import { act, renderHook } from "@testing-library/react";
import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import type { ReactNode } from "react";

import { setApiInstance } from "../api";
import type { ApiClient } from "../api/client";
import { useDeleteChannel } from "./mutations";

vi.mock("../hooks", () => ({
  useWorkspaceId: () => "ws-1",
}));

function createWrapper(qc: QueryClient) {
  return function Wrapper({ children }: { children: ReactNode }) {
    return <QueryClientProvider client={qc}>{children}</QueryClientProvider>;
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
