/** UI mapping for team_knowledge kinds (LRM-1001 / freeze LRM-999). */
export type WikiTab = "all" | "topic" | "decision" | "goal";

/** BE kinds that back the member Wiki MVP. */
export type WikiBackendKind = "context" | "decision";

export function tabToApiKind(tab: WikiTab): WikiBackendKind | "goal" | undefined {
  if (tab === "topic") return "context";
  if (tab === "decision") return "decision";
  if (tab === "goal") return "goal";
  return undefined;
}

export function isWikiPageKind(kind: string): boolean {
  return kind === "context" || kind === "decision" || kind === "goal";
}

export const WIKI_EDGE_TYPES = [
  "derived_from",
  "about",
  "shared_to",
  "supersedes",
  "owned_by",
] as const;

export type WikiEdgeType = (typeof WIKI_EDGE_TYPES)[number];

export function isWikiEdgeType(value: string): value is WikiEdgeType {
  return (WIKI_EDGE_TYPES as readonly string[]).includes(value);
}
