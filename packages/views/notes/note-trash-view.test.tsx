/**
 * @vitest-environment happy-dom
 */
import { screen } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { describe, expect, it, vi } from "vitest";
import type { NotePage } from "@multica/core/types";
import { renderWithI18n } from "../test/i18n";
import { NoteTrashView } from "./note-trash-view";

function page(overrides: Partial<NotePage> = {}): NotePage {
  return {
    id: "page-1",
    workspace_id: "ws-1",
    parent_id: null,
    owner_user_id: "user-1",
    title: "Old brief",
    content: "",
    sort_key: "a",
    share_user_ids: [],
    can_manage_shares: true,
    created_at: "2026-01-01T00:00:00Z",
    updated_at: "2026-01-01T00:00:00Z",
    deleted_at: "2026-08-01T00:00:00Z",
    ...overrides,
  };
}

describe("NoteTrashView", () => {
  it("hides Empty trash when the list is empty", () => {
    renderWithI18n(
      <NoteTrashView
        pages={[]}
        emptying={false}
        restoring={false}
        deleting={false}
        onEmpty={vi.fn()}
        onRestore={vi.fn()}
        onPermanentDelete={vi.fn()}
      />,
    );
    expect(screen.queryByRole("button", { name: "Empty trash" })).toBeNull();
    expect(screen.getByText("Trash is empty.")).toBeTruthy();
  });

  it("shows Empty trash when there are deleted pages", async () => {
    const user = userEvent.setup();
    const onEmpty = vi.fn();
    renderWithI18n(
      <NoteTrashView
        pages={[page()]}
        emptying={false}
        restoring={false}
        deleting={false}
        onEmpty={onEmpty}
        onRestore={vi.fn()}
        onPermanentDelete={vi.fn()}
      />,
    );
    await user.click(screen.getByRole("button", { name: "Empty trash" }));
    expect(onEmpty).toHaveBeenCalledTimes(1);
  });
});
