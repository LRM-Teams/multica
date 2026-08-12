import { api } from "../api";

const UUID =
  "[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12}";

const ISSUE_MENTION_RE = new RegExp(
  `\\[(?:\\\\.|[^\\]])+\\]\\(mention://issue/(${UUID})\\)`,
  "gi",
);
const AGENT_MENTION_RE = new RegExp(
  `\\[@?(?:\\\\.|[^\\]])+\\]\\(mention://agent/(${UUID})\\)`,
  "gi",
);
const RUN_MENTION_RE = new RegExp(
  `\\[(?:\\\\.|[^\\]])+\\]\\(mention://run/(${UUID})\\)`,
  "gi",
);

function extractMentionIds(content: string, re: RegExp): string[] {
  if (!content) return [];
  const ids = new Set<string>();
  for (const match of content.matchAll(re)) {
    const id = match[1]?.toLowerCase();
    if (id) ids.add(id);
  }
  return [...ids];
}

/** Extract unique issue UUIDs from note markdown `mention://issue/<uuid>` links. */
export function extractIssueIdsFromNoteMarkdown(content: string): string[] {
  return extractMentionIds(content, ISSUE_MENTION_RE);
}

/** Extract unique agent UUIDs from note markdown `mention://agent/<uuid>` links. */
export function extractAgentIdsFromNoteMarkdown(content: string): string[] {
  return extractMentionIds(content, AGENT_MENTION_RE);
}

/** Extract unique run UUIDs from note markdown `mention://run/<uuid>` links. */
export function extractRunIdsFromNoteMarkdown(content: string): string[] {
  return extractMentionIds(content, RUN_MENTION_RE);
}

async function syncRefs<TCreate>(
  pageId: string,
  desired: Set<string>,
  listedIds: string[],
  createOne: (id: string) => Promise<TCreate>,
  deleteOne: (id: string) => Promise<void>,
): Promise<{ added: number; removed: number; errors: string[] }> {
  const existing = new Set(listedIds.map((id) => id.toLowerCase()).filter(Boolean));
  const toAdd = [...desired].filter((id) => !existing.has(id));
  const toRemove = [...existing].filter((id) => !desired.has(id));
  const errors: string[] = [];

  await Promise.all([
    ...toAdd.map(async (id) => {
      try {
        await createOne(id);
      } catch (error) {
        errors.push(error instanceof Error ? error.message : `failed to link ${id}`);
      }
    }),
    ...toRemove.map(async (id) => {
      try {
        await deleteOne(id);
      } catch (error) {
        errors.push(error instanceof Error ? error.message : `failed to unlink ${id}`);
      }
    }),
  ]);

  return { added: toAdd.length, removed: toRemove.length, errors };
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
  return syncRefs(
    pageId,
    desired,
    listed.refs.map((ref) => ref.id || ref.issue_id || ""),
    (issueId) => api.createNotePageIssueRef(pageId, { issue_id: issueId }),
    (issueId) => api.deleteNotePageIssueRef(pageId, issueId),
  );
}

/** Sync agent mentions → `note_page_agent_ref` (S2-R1). */
export async function syncNotePageAgentRefsFromContent(
  pageId: string,
  content: string,
): Promise<{ added: number; removed: number; errors: string[] }> {
  const desired = new Set(extractAgentIdsFromNoteMarkdown(content));
  const listed = await api.listNotePageAgentRefs(pageId);
  return syncRefs(
    pageId,
    desired,
    listed.refs.map((ref) => ref.id || ""),
    (agentId) => api.createNotePageAgentRef(pageId, { agent_id: agentId }),
    (agentId) => api.deleteNotePageAgentRef(pageId, agentId),
  );
}

/** Sync run mentions → `note_page_run_ref` (S2-R1). */
export async function syncNotePageRunRefsFromContent(
  pageId: string,
  content: string,
): Promise<{ added: number; removed: number; errors: string[] }> {
  const desired = new Set(extractRunIdsFromNoteMarkdown(content));
  const listed = await api.listNotePageRunRefs(pageId);
  return syncRefs(
    pageId,
    desired,
    listed.refs.map((ref) => ref.id || ""),
    (runId) => api.createNotePageRunRef(pageId, { run_id: runId }),
    (runId) => api.deleteNotePageRunRef(pageId, runId),
  );
}

/** Sync issue + agent + run mention associations after a note save. */
export async function syncNotePageRefsFromContent(
  pageId: string,
  content: string,
): Promise<{ added: number; removed: number; errors: string[] }> {
  const [issues, agents, runs] = await Promise.all([
    syncNotePageIssueRefsFromContent(pageId, content),
    syncNotePageAgentRefsFromContent(pageId, content),
    syncNotePageRunRefsFromContent(pageId, content),
  ]);
  return {
    added: issues.added + agents.added + runs.added,
    removed: issues.removed + agents.removed + runs.removed,
    errors: [...issues.errors, ...agents.errors, ...runs.errors],
  };
}
