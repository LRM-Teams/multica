import type { RuntimeAgentWorkspace } from "@multica/core/types";

export type WorkspaceRowStatus = "active" | "archived" | "orphaned";

const WORKSPACE_UUID_PREFIX =
  /^[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12}\//i;

/** Derive ACTIVE / ARCHIVED / ORPHANED from API orphan + agent_id (LRM-1095). */
export function workspaceRowStatus(
  ws: Pick<RuntimeAgentWorkspace, "orphan" | "agent_id">,
): WorkspaceRowStatus {
  if (!ws.orphan) return "active";
  if (ws.agent_id) return "archived";
  return "orphaned";
}

/** Prefer agent_name; orphan dirs fall back to truncated dir_name. */
export function workspaceDisplayName(
  ws: Pick<RuntimeAgentWorkspace, "agent_name" | "dir_name">,
): string {
  const name = ws.agent_name?.trim();
  if (name) return name;
  return truncateOrphanDirName(ws.dir_name);
}

/** First 8 + last 4 when dir_name is a long UUID-like slug. */
export function truncateOrphanDirName(dirName: string): string {
  const trimmed = dirName.trim();
  if (trimmed.length <= 14) return trimmed;
  return `${trimmed.slice(0, 8)}…${trimmed.slice(-4)}`;
}

/** Strip leading workspace UUID from rel_path for the path line. */
export function workspaceDisplayPath(relPath: string): string {
  const trimmed = relPath.trim();
  return trimmed.replace(WORKSPACE_UUID_PREFIX, "");
}
