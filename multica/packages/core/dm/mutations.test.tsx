/**
 * @vitest-environment jsdom
 */
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import { act, renderHook } from "@testing-library/react";
import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import type { ReactNode } from "react";

import { setApiInstance } from "../api";
import type { ApiClient } from "../api/client";
import { useCreateOrFindDM } from "./mutations";
import { dmKeys } from "./queries";
import type { CreateOrFindDMBody, DMItem } from "./types";

vi.mock("../hooks", () => ({
  useWorkspaceId: () => "ws-1",
}));

const WS_ID = "ws-1";

function makeDm(id: string, overrides: Partial<DMItem> = {}): DMItem {
  return {
    id,
    source: "dm_channel",
    mode: "direct",
    peer: { type: "agent", id: `peer-${id}`, name: `Agent ${id}` },
    unread: 0,
    updated_at: "2026-07-27T00:00:00Z",
    ...overrides,
  };
}

function createWrapper(qc: QueryClient) {
  return function Wrapper({ children }: { children: ReactNode }) {
    return <QueryClientProvider client={qc}>{children}</QueryClientProvider>;
  };
}

describe("useCreateOrFindDM — seeds the fresh DM before the invalidate refetch", () => {
  let qc: QueryClient;
  let createOrFindDM: ReturnType<typeof vi.fn<(b: CreateOrFindDMBody) => Promise<DMItem>>>;
  let listDMs: ReturnType<typeof vi.fn<() => Promise<DMItem[]>>>;

  beforeEach(() => {
    qc = new QueryClient({
      defaultOptions: { queries: { retry: false }, mutations: { retry: false } },
    });
    createOrFindDM = vi.fn();
    // The invalidate-driven refetch stays PENDING for the whole test, modelling
    // the real race: `useOpenDM` navigates (channels-page reads `dms` from this
    // cache) BEFORE the refetch returns. With the fix the returned DM is seeded
    // and resolvable now; without it (invalidate-only) the cache still lacks the
    // fresh DM at nav time → channels-page falls back to #general.
    listDMs = vi.fn(() => new Promise<DMItem[]>(() => {}));
    setApiInstance({ createOrFindDM, listDMs } as unknown as ApiClient);
  });

  afterEach(() => {
    qc.clear();
    vi.restoreAllMocks();
  });

  it("makes a freshly-created DM resolvable in the list cache immediately (no #general race)", async () => {
    qc.setQueryData<DMItem[]>(dmKeys.list(WS_ID), [makeDm("existing")]);
    const fresh = makeDm("fresh");
    createOrFindDM.mockResolvedValue(fresh);

    const { result } = renderHook(() => useCreateOrFindDM(), {
      wrapper: createWrapper(qc),
    });
    await act(async () => {
      await result.current.mutateAsync({ peer_type: "agent", peer_id: "peer-fresh" });
    });

    // Fresh DM present now, while the refetch is still pending → the subsequent
    // sync navigation to /channels/fresh resolves via `dms.find(id)` instead of
    // falling back to #general. The existing row is preserved.
    const list = qc.getQueryData<DMItem[]>(dmKeys.list(WS_ID));
    expect(list?.map((d) => d.id)).toContain("fresh");
    expect(list?.map((d) => d.id)).toContain("existing");
  });

  it("does not duplicate a DM already in the list (idempotent create-or-find)", async () => {
    const fresh = makeDm("fresh");
    qc.setQueryData<DMItem[]>(dmKeys.list(WS_ID), [fresh]);
    createOrFindDM.mockResolvedValue(fresh);

    const { result } = renderHook(() => useCreateOrFindDM(), {
      wrapper: createWrapper(qc),
    });
    await act(async () => {
      await result.current.mutateAsync({ peer_type: "agent", peer_id: "peer-fresh" });
    });

    expect(
      qc.getQueryData<DMItem[]>(dmKeys.list(WS_ID))?.filter((d) => d.id === "fresh"),
    ).toHaveLength(1);
  });

  it("seeds even when the list cache is empty/unset", async () => {
    const fresh = makeDm("fresh");
    createOrFindDM.mockResolvedValue(fresh);

    const { result } = renderHook(() => useCreateOrFindDM(), {
      wrapper: createWrapper(qc),
    });
    await act(async () => {
      await result.current.mutateAsync({ peer_type: "agent", peer_id: "peer-fresh" });
    });

    expect(qc.getQueryData<DMItem[]>(dmKeys.list(WS_ID))?.map((d) => d.id)).toEqual(["fresh"]);
  });
});
