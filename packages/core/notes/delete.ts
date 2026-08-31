export type NoteDeletePage = {
  id: string;
  parent_id: string | null;
  owner_user_id: string;
};

// Owner delete removes the whole visible subtree from *this* view (other
// people's pages stay in the database, but this viewer loses the ancestor
// path). Sharee dismiss keeps pages they own.
export function collectNoteIdsRemovedOnDelete(
  pages: readonly NoteDeletePage[],
  rootId: string,
  viewerOwnsRoot: boolean,
): Set<string> {
  const root = pages.find((page) => page.id === rootId);
  const ids = new Set<string>([rootId]);
  let changed = true;
  while (changed) {
    changed = false;
    for (const page of pages) {
      if (!page.parent_id || !ids.has(page.parent_id) || ids.has(page.id)) continue;
      if (viewerOwnsRoot || page.owner_user_id === root?.owner_user_id) {
        ids.add(page.id);
        changed = true;
      }
    }
  }
  return ids;
}
