import { describe, expect, it } from "vitest";
import { noteCanDropOnTarget } from "./note-drag";

function page(id: string, parent: string | null = null, canManage = true) {
  return { id, parent_id: parent, can_manage_shares: canManage };
}

describe("noteCanDropOnTarget", () => {
  it("allows dropping an owned note inside a shared note", () => {
    const mine = page("mine");
    const shared = page("shared", null, false);
    expect(noteCanDropOnTarget(mine, shared, "inside", [mine, shared])).toBe(true);
  });

  it("rejects dragging a note the viewer does not own", () => {
    const shared = page("shared", null, false);
    const mine = page("mine");
    expect(noteCanDropOnTarget(shared, mine, "inside", [shared, mine])).toBe(false);
  });

  it("rejects dropping a note onto one of its descendants", () => {
    const mine = page("mine");
    const child = page("child", "mine");
    expect(noteCanDropOnTarget(mine, child, "inside", [mine, child])).toBe(false);
  });
});
