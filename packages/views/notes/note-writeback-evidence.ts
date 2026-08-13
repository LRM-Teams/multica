import { appendQueryParams, type useWorkspacePaths } from "@multica/core/paths";
import type { NoteWritebackEvidence } from "@multica/core/types";

export function evidenceHref(
  item: NoteWritebackEvidence,
  evidence: NoteWritebackEvidence[],
  paths: ReturnType<typeof useWorkspacePaths>,
): string | null {
  const type = item.type.trim().toLowerCase();
  const id = item.id.trim();
  if (!id) return null;
  if (type === "issue") return paths.issueDetail(id);
  if (type === "agent") return paths.agentDetail(id);
  if (type === "run" || type === "task" || type === "agent_task") {
    const agentId = evidence.find((entry) => entry.type.trim().toLowerCase() === "agent" && entry.id.trim())?.id.trim();
    if (agentId) return appendQueryParams(paths.agentDetail(agentId), { run: id });
    const issueId = evidence.find((entry) => entry.type.trim().toLowerCase() === "issue" && entry.id.trim())?.id.trim();
    return issueId ? paths.issueDetail(issueId) : paths.issues();
  }
  return null;
}
