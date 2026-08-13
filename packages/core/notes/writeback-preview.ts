import type { NoteWriteback } from "../types";

/**
 * Preview the note markdown that would result from accepting a writeback.
 * Mirrors server applyNoteWritebackContent (S1-W1/W2).
 */
export function previewNoteWritebackContent(
  current: string,
  writeback: Pick<NoteWriteback, "action" | "content" | "target">,
): string | null {
  const action = writeback.action;
  const content = writeback.content ?? "";
  switch (action) {
    case "append": {
      const add = content.trim();
      if (!add) return null;
      const cur = current.replace(/\n+$/, "");
      return cur ? `${cur}\n\n${add}` : add;
    }
    case "replace_page": {
      if (!content.trim()) return null;
      return content;
    }
    case "patch": {
      const target = writeback.target?.trim();
      if (!target) return null;
      if (current.includes(target)) {
        return current.replace(target, content);
      }
      const loose = target.trim();
      if (loose && current.includes(loose)) {
        return current.replace(loose, content);
      }
      return null;
    }
    default:
      return null;
  }
}

export function writebackHasOpenableEvidence(
  evidence: Array<{ type?: string; id?: string }> | undefined,
): boolean {
  if (!evidence?.length) return false;
  return evidence.some((item) => {
    const type = (item.type ?? "").trim().toLowerCase();
    const id = (item.id ?? "").trim();
    if (!id) return false;
    return type === "issue" || type === "agent" || type === "run" || type === "task" || type === "agent_task";
  });
}
