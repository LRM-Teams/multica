import { describe, expect, it } from "vitest";
import { noteCanDropOnTarget } from "./note-drag";

function page(id: string, parent: string | null = null, canManage = true) {
  return { id, parent_id: parent, can_manage_shares: canManage };
}

describe("noteCanDropOnTarget", () => {
  it("allows dropping an owned note inside a shared note", () => {
    const pages = [page("mine"), page("shared", null, false)];
    expect(noteCanDropOnTarget(pages[0], pages[1], "inside", pages)).toBe(true);
  });

  it("rejects dragging a note the viewer does not own", () => {
    const pages = [page("shared", null, false), page("mine")];
    expect(noteCanDropOnTarget(pages[0], pages[1], "inside", pages)).toBe(false);
  });

  it("rejects dropping a note onto one of its descendants", () => {
    const pages = [page("mine"), page("child", "mine")];
    expect(noteCanDropOnTarget(pages[0], pages[1], "inside", pages)).toBe(false);
  });
});
