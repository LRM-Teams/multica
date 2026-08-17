import { beforeEach, describe, expect, it, vi } from "vitest";
import { QueryClient } from "@tanstack/react-query";
import type { NotePage } from "@multica/core/types";
import { noteKeys } from "@multica/core/notes/queries";
import { createTopLevelNoteFromChatText } from "./create-note-from-chat";

const createNotePage = vi.fn();
const updateNotePage = vi.fn();

vi.mock("@multica/core/api", () => ({
  api: {
    createNotePage: (...args: unknown[]) => createNotePage(...args),
    updateNotePage: (...args: unknown[]) => updateNotePage(...args),
  },
}));

function makePage(overrides: Partial<NotePage> = {}): NotePage {
  return {
    id: "new-1",
    workspace_id: "ws-1",
    parent_id: null,
    owner_user_id: "user-1",
    title: "Ship it",
    content: "Ship it\n\nDetails.",
    sort_key: "0001",
    share_user_ids: [],
    can_manage_shares: true,
    created_at: "2026-08-14T00:00:00.000Z",
    updated_at: "2026-08-14T00:00:00.000Z",
    deleted_at: null,
    ...overrides,
  };
}

describe("createTopLevelNoteFromChatText", () => {
  beforeEach(() => {
    createNotePage.mockReset();
    updateNotePage.mockReset();
  });

  it("creates a page then writes the chat text as content", async () => {
    const qc = new QueryClient();
    const created = makePage({ content: "" });
    const updated = makePage();
    createNotePage.mockResolvedValue(created);
    updateNotePage.mockResolvedValue(updated);

    const result = await createTopLevelNoteFromChatText({
      title: "Ship it",
      content: "Ship it\n\nDetails.",
      wsId: "ws-1",
      queryClient: qc,
    });

    expect(createNotePage).toHaveBeenCalledWith({ title: "Ship it" });
    expect(updateNotePage).toHaveBeenCalledWith("new-1", {
      content: "Ship it\n\nDetails.",
    });
    expect(result).toEqual(updated);
    expect(qc.getQueryData(noteKeys.detail("ws-1", "new-1"))).toEqual(updated);
  });
});
