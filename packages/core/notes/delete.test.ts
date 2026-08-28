import { describe, expect, it } from "vitest";
import { collectNoteIdsRemovedOnDelete } from "./delete";

function page(id: string, owner: string, parent: string | null = null) {
  return { id, parent_id: parent, owner_user_id: owner };
}

describe("collectNoteIdsRemovedOnDelete", () => {
  it("removes the whole visible subtree when the viewer owns the root", () => {
    const pages = [
      page("parent", "a"),
      page("owned-child", "a", "parent"),
      page("other-child", "b", "parent"),
    ];
    expect([...collectNoteIdsRemovedOnDelete(pages, "parent", true)].sort()).toEqual([
      "other-child",
      "owned-child",
      "parent",
    ]);
  });

  it("keeps the viewer's own pages when dismissing a shared root", () => {
    const pages = [
      page("parent", "a"),
      page("owned-child", "a", "parent"),
      page("other-child", "b", "parent"),
    ];
    expect([...collectNoteIdsRemovedOnDelete(pages, "parent", false)].sort()).toEqual([
      "owned-child",
      "parent",
    ]);
  });
});
