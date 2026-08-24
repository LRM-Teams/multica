/**
 * @vitest-environment happy-dom
 */
import { fireEvent, screen } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { describe, expect, it, vi } from "vitest";
import { renderWithI18n } from "../test/i18n";
import { NoteTrashDock, noteCanDropOnTrash } from "./note-trash-dock";

describe("noteCanDropOnTrash", () => {
  it("allows only pages the user can manage", () => {
    expect(noteCanDropOnTrash({ can_manage_shares: true })).toBe(true);
    expect(noteCanDropOnTrash({ can_manage_shares: false })).toBe(false);
    expect(noteCanDropOnTrash(null)).toBe(false);
  });
});

describe("NoteTrashDock", () => {
  it("toggles the trash view on click", async () => {
    const user = userEvent.setup();
    const onToggle = vi.fn();
    renderWithI18n(
      <NoteTrashDock selected={false} count={2} canDrop={false} onToggle={onToggle} onDrop={vi.fn()} />,
    );
    await user.click(screen.getByRole("button", { name: /Trash/ }));
    expect(onToggle).toHaveBeenCalledTimes(1);
  });

  it("moves a dragged note into trash without opening the trash view", () => {
    const onToggle = vi.fn();
    const onDrop = vi.fn();
    renderWithI18n(
      <NoteTrashDock selected={false} count={0} canDrop onToggle={onToggle} onDrop={onDrop} />,
    );
    const dock = screen.getByRole("button", { name: /Trash/ });
    fireEvent.dragOver(dock);
    fireEvent.drop(dock);
    fireEvent.click(dock);
    expect(onDrop).toHaveBeenCalledTimes(1);
    expect(onToggle).not.toHaveBeenCalled();
  });

  it("ignores a drop when the current drag cannot go to trash", () => {
    const onDrop = vi.fn();
    renderWithI18n(
      <NoteTrashDock selected={false} count={0} canDrop={false} onToggle={vi.fn()} onDrop={onDrop} />,
    );
    fireEvent.drop(screen.getByRole("button", { name: /Trash/ }));
    expect(onDrop).not.toHaveBeenCalled();
  });
});
