/**
 * @vitest-environment jsdom
 */
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import { act, renderHook } from "@testing-library/react";
import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import type { ReactNode } from "react";

import { setApiInstance } from "../api";
import type { ApiClient } from "../api/client";
import type { NotePage, NotePageListResponse } from "../types";
import { useUpdateNotePage } from "./mutations";
import { noteKeys } from "./queries";

vi.mock("../hooks", () => ({
  useWorkspaceId: () => "ws-1",
}));

const WS_ID = "ws-1";
const NOTE_ID = "note-1";

function makeNote(overrides: Partial<NotePage> = {}): NotePage {
  return {
    id: NOTE_ID,
    workspace_id: WS_ID,
    parent_id: null,
    owner_user_id: "user-1",
    title: "Untitled",
    content: "",
    sort_key: "0001",
    share_user_ids: [],
    can_manage_shares: true,
    created_at: "2026-08-01T00:00:00.000Z",
    updated_at: "2026-08-01T00:00:00.000Z",
    deleted_at: null,
    ...overrides,
  };
}

function createWrapper(qc: QueryClient) {
  return function Wrapper({ children }: { children: ReactNode }) {
    return <QueryClientProvider client={qc}>{children}</QueryClientProvider>;
  };
}

describe("useUpdateNotePage", () => {
  let qc: QueryClient;

  beforeEach(() => {
    qc = new QueryClient({
      defaultOptions: { queries: { retry: false }, mutations: { retry: false } },
    });
    const seed = makeNote();
    qc.setQueryData(noteKeys.detail(WS_ID, NOTE_ID), seed);
    qc.setQueryData<NotePageListResponse>(noteKeys.list(WS_ID), { pages: [seed] });
  });

  afterEach(() => {
    qc.clear();
    vi.restoreAllMocks();
  });

  it("does not let a slower older save overwrite a newer content write (lost keystrokes)", async () => {
    // Repro: type "a", autosave starts; type "b", second autosave starts;
    // second response lands first, then the first (stale) response lands last.
    // Without an ordering guard the cache regresses to "a" and the editor
    // syncs that stale defaultValue back into the document.
    let resolveOlder!: (page: NotePage) => void;
    let resolveNewer!: (page: NotePage) => void;
    const olderSave = new Promise<NotePage>((resolve) => {
      resolveOlder = resolve;
    });
    const newerSave = new Promise<NotePage>((resolve) => {
      resolveNewer = resolve;
    });

    const updateNotePage = vi
      .fn<(id: string, data: { content?: string }) => Promise<NotePage>>()
      .mockImplementationOnce(() => olderSave)
      .mockImplementationOnce(() => newerSave);

    setApiInstance({ updateNotePage } as unknown as ApiClient);

    const { result } = renderHook(() => useUpdateNotePage(), {
      wrapper: createWrapper(qc),
    });

    let olderDone!: Promise<NotePage>;
    let newerDone!: Promise<NotePage>;
    await act(async () => {
      olderDone = result.current.mutateAsync({ id: NOTE_ID, data: { content: "a" } });
      newerDone = result.current.mutateAsync({ id: NOTE_ID, data: { content: "ab" } });
    });

    await act(async () => {
      resolveNewer(
        makeNote({
          content: "ab",
          updated_at: "2026-08-01T00:00:02.000Z",
        }),
      );
      await newerDone;
    });

    expect(qc.getQueryData<NotePage>(noteKeys.detail(WS_ID, NOTE_ID))?.content).toBe("ab");

    await act(async () => {
      resolveOlder(
        makeNote({
          content: "a",
          updated_at: "2026-08-01T00:00:01.000Z",
        }),
      );
      await olderDone;
    });

    expect(qc.getQueryData<NotePage>(noteKeys.detail(WS_ID, NOTE_ID))?.content).toBe("ab");
    expect(qc.getQueryData<NotePageListResponse>(noteKeys.list(WS_ID))?.pages[0]?.content).toBe("ab");
  });

  it("does not roll back a newer write when an older overlapping save fails", async () => {
    let rejectOlder!: (error: Error) => void;
    let resolveNewer!: (page: NotePage) => void;
    const olderSave = new Promise<NotePage>((_resolve, reject) => {
      rejectOlder = reject;
    });
    const newerSave = new Promise<NotePage>((resolve) => {
      resolveNewer = resolve;
    });

    const updateNotePage = vi
      .fn<(id: string, data: { content?: string }) => Promise<NotePage>>()
      .mockImplementationOnce(() => olderSave)
      .mockImplementationOnce(() => newerSave);

    setApiInstance({ updateNotePage } as unknown as ApiClient);

    const { result } = renderHook(() => useUpdateNotePage(), {
      wrapper: createWrapper(qc),
    });

    let olderDone!: Promise<NotePage>;
    let newerDone!: Promise<NotePage>;
    await act(async () => {
      olderDone = result.current.mutateAsync({ id: NOTE_ID, data: { content: "a" } }).catch((error: unknown) => error as NotePage);
      newerDone = result.current.mutateAsync({ id: NOTE_ID, data: { content: "ab" } });
    });

    await act(async () => {
      resolveNewer(makeNote({ content: "ab", updated_at: "2026-08-01T00:00:02.000Z" }));
      await newerDone;
    });

    await act(async () => {
      rejectOlder(new Error("network"));
      await olderDone;
    });

    expect(qc.getQueryData<NotePage>(noteKeys.detail(WS_ID, NOTE_ID))?.content).toBe("ab");
  });
});
