import type { NotePage } from "@multica/core/types";

export type NoteDropPosition = "before" | "after" | "inside";

export function isNoteDescendant(pages: readonly Pick<NotePage, "id" | "parent_id">[], ancestorId: string, targetId: string) {
  const byId = new Map(pages.map((page) => [page.id, page]));
  const seen = new Set<string>();
  let current = byId.get(targetId);
  while (current?.parent_id) {
    if (current.parent_id === ancestorId) return true;
    if (seen.has(current.parent_id)) return false;
    seen.add(current.parent_id);
    current = byId.get(current.parent_id);
  }
  return false;
}

export function noteCanDropOnTarget(
  dragged: Pick<NotePage, "id" | "can_manage_shares"> | null | undefined,
  target: Pick<NotePage, "id" | "parent_id">,
  position: NoteDropPosition,
  pages: readonly Pick<NotePage, "id" | "parent_id">[],
) {
  if (!dragged?.can_manage_shares || dragged.id === target.id) return false;
  if (position === "inside" && isNoteDescendant(pages, dragged.id, target.id)) return false;
  if (position !== "inside" && target.parent_id && isNoteDescendant(pages, dragged.id, target.parent_id)) return false;
  return true;
}
