import { api } from "../api";

const ISSUE_MENTION_RE = /\[(?:\\.|[^\]])+\]\(mention:\/\/issue\/([0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12})\)/gi;

/** Extract unique issue UUIDs from note markdown `mention://issue/<uuid>` links. */
export function extractIssueIdsFromNoteMarkdown(content: string): string[] {
  if (!content) return [];
  const ids = new Set<string>();
  for (const match of content.matchAll(ISSUE_MENTION_RE)) {
    const id = match[1]?.toLowerCase();
    if (id) ids.add(id);
  }
  return [...ids];
}

/**
 * Diff note markdown issue mentions against `note_page_issue_ref` and sync.
 * Create/delete failures are collected so callers can toast without failing
 * the note content save.
 */
export async function syncNotePageIssueRefsFromContent(
  pageId: string,
  content: string,
): Promise<{ added: number; removed: number; errors: string[] }> {
  const desired = new Set(extractIssueIdsFromNoteMarkdown(content));
  const listed = await api.listNotePageIssueRefs(pageId);
  const existing = new Set(
    listed.refs
      .map((ref) => (ref.id || ref.issue_id || "").toLowerCase())
      .filter(Boolean),
  );

  const toAdd = [...desired].filter((id) => !existing.has(id));
  const toRemove = [...existing].filter((id) => !desired.has(id));
  const errors: string[] = [];

  await Promise.all([
    ...toAdd.map(async (issueId) => {
      try {
        await api.createNotePageIssueRef(pageId, { issue_id: issueId });
      } catch (error) {
        errors.push(error instanceof Error ? error.message : `failed to link ${issueId}`);
      }
    }),
    ...toRemove.map(async (issueId) => {
      try {
        await api.deleteNotePageIssueRef(pageId, issueId);
      } catch (error) {
        errors.push(error instanceof Error ? error.message : `failed to unlink ${issueId}`);
      }
    }),
  ]);

  return { added: toAdd.length, removed: toRemove.length, errors };
}
