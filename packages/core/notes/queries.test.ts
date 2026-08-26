import { describe, expect, it, vi } from "vitest";
import { QueryClient } from "@tanstack/react-query";
import type { NotePage, NotePageListResponse } from "../types";
import { applyNoteShareSeen, noteKeys, noteNeedsShareSeen } from "./queries";

const WS_ID = "ws-1";
const PAGE_ID = "page-1";

function makePage(overrides: Partial<NotePage> = {}): NotePage {
  return {
    id: PAGE_ID,
    workspace_id: WS_ID,
    parent_id: null,
    owner_user_id: "owner-1",
    title: "Shared",
    content: "",
    sort_key: "0001",
    share_user_ids: ["user-2"],
    can_manage_shares: false,
    share_unread: true,
    created_at: "2026-08-01T00:00:00.000Z",
    updated_at: "2026-08-01T00:00:00.000Z",
    deleted_at: null,
    ...overrides,
  };
}

describe("noteNeedsShareSeen", () => {
  it("is true only for unread shared pages", () => {
    expect(noteNeedsShareSeen(makePage({ share_unread: true }))).toBe(true);
    expect(noteNeedsShareSeen(makePage({ share_unread: false }))).toBe(false);
    expect(noteNeedsShareSeen(undefined)).toBe(false);
  });
});

describe("applyNoteShareSeen", () => {
  it("clears the list unread flag and decrements the sidebar count", () => {
    const qc = new QueryClient();
    const invalidate = vi.spyOn(qc, "invalidateQueries");
    qc.setQueryData<NotePageListResponse>(noteKeys.list(WS_ID), { pages: [makePage()] });
    qc.setQueryData(noteKeys.shareUnreadCount(WS_ID), { count: 2 });

    applyNoteShareSeen(qc, WS_ID, PAGE_ID);

    expect(qc.getQueryData<NotePageListResponse>(noteKeys.list(WS_ID))?.pages[0]?.share_unread).toBe(false);
    expect(qc.getQueryData<{ count: number }>(noteKeys.shareUnreadCount(WS_ID))?.count).toBe(1);
    expect(invalidate).toHaveBeenCalledWith({ queryKey: noteKeys.shareUnreadCount(WS_ID) });
  });

  it("does not touch an already-read page or the count", () => {
    const qc = new QueryClient();
    const invalidate = vi.spyOn(qc, "invalidateQueries");
    const setData = vi.spyOn(qc, "setQueryData");
    qc.setQueryData<NotePageListResponse>(noteKeys.list(WS_ID), { pages: [makePage({ share_unread: false })] });
    qc.setQueryData(noteKeys.shareUnreadCount(WS_ID), { count: 3 });
    setData.mockClear();

    applyNoteShareSeen(qc, WS_ID, PAGE_ID);

    expect(qc.getQueryData<{ count: number }>(noteKeys.shareUnreadCount(WS_ID))?.count).toBe(3);
    expect(invalidate).not.toHaveBeenCalled();
    expect(setData).not.toHaveBeenCalled();
  });

  it("invalidates the count when the opened page is not in the list cache", () => {
    const qc = new QueryClient();
    const invalidate = vi.spyOn(qc, "invalidateQueries");
    qc.setQueryData(noteKeys.shareUnreadCount(WS_ID), { count: 1 });

    applyNoteShareSeen(qc, WS_ID, PAGE_ID);

    expect(invalidate).toHaveBeenCalledWith({ queryKey: noteKeys.shareUnreadCount(WS_ID) });
  });
});
