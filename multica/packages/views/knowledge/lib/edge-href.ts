import type { WorkspacePaths } from "@multica/core/paths";

/** Resolve a knowledge-edge endpoint to an in-app href when possible. */
export function edgeTargetHref(
  paths: WorkspacePaths,
  kind: string,
  id: string,
): string | null {
  if (!id) return null;
  switch (kind) {
    case "issue":
      return paths.issueDetail(id);
    case "channel":
      return paths.channelDetail(id);
    case "project":
      return paths.projectDetail(id);
    case "agent":
      return paths.agentDetail(id);
    case "member":
      return paths.memberDetail(id);
    case "team_knowledge":
      return paths.wikiDetail(id);
    default:
      return null;
  }
}

/** Pick the "other" side of an edge relative to the open page. */
export function edgeCounterpart(
  pageId: string,
  edge: { from_kind: string; from_id: string; to_kind: string; to_id: string },
): { kind: string; id: string } {
  if (edge.from_kind === "team_knowledge" && edge.from_id === pageId) {
    return { kind: edge.to_kind, id: edge.to_id };
  }
  if (edge.to_kind === "team_knowledge" && edge.to_id === pageId) {
    return { kind: edge.from_kind, id: edge.from_id };
  }
  // Prefer non-page end when undirected listing returns both orientations.
  if (edge.from_kind === "team_knowledge") {
    return { kind: edge.to_kind, id: edge.to_id };
  }
  return { kind: edge.from_kind, id: edge.from_id };
}
