/**
 * LRM-1497 · D5 star-graph quick navigation — keyword search + locate stepping.
 *
 * The AC ("支持按标题、Agent、结论和证据关键词搜索，结果可逐个定位并保持画布语境" and
 * "200 节点场景下可在 10 秒内通过搜索定位指定成果") needs ordered, field-tagged keyword
 * matches that a consumer can step through one-by-one while the canvas keeps
 * its context (selection, viewport, filter).
 *
 * `matchesResearchCanvasFilter` (canvas-store) is a boolean show/hide predicate
 * — it answers "is this node visible under a query?". This module answers the
 * complementary question: "WHICH nodes match this query, in what field, and
 * with a local snippet" — i.e. the ordered result set + a next/prev locate
 * cursor. It consumes the SAME `ResearchCanvasFilterableNode` shape the canvas
 * filter already uses, so a canvas can pass the identical canonical/filterable
 * node list to both.
 *
 * Pure client-side presentation logic over the canonical node list (server-
 * owned via React Query) — never mutates nodes, never fabricates fields, never
 * owns state. Selection / viewport / filter stay client state owned by
 * `canvas-store`, untouched here, so locating a result keeps canvas context by
 * construction.
 *
 * Performance: a single linear scan with case-folded substring match is O(n)
 * with no allocations per node beyond the returned matches — trivially under
 * the 10 s budget for a 200-node scene.
 */

import type { ResearchCanvasFilterableNode } from "./canvas-store";

/** Where a query matched, plus a bounded local snippet for context. */
export interface StarSearchMatch {
  nodeId: string;
  field: "title" | "conclusion" | "evidence" | "agent";
  /** The exact matched substring (same casing as the node field). */
  matched: string;
  /** "保持画布语境": a short window around the first hit on the field. */
  snippet: string;
}

const EMPTY_MATCHES: StarSearchMatch[] = [];

/** AC field precedence for each node: title → conclusion → evidence → agent. */
const FIELD_RESOLVERS: Array<{
  field: StarSearchMatch["field"];
  pick: (node: ResearchCanvasFilterableNode) => string | null | undefined;
}> = [
  { field: "title", pick: (n) => n.title },
  {
    field: "conclusion",
    pick: (n) => (n.summary ?? n.conclusion) || null,
  },
  { field: "evidence", pick: (n) => toEvidenceText(n.evidence) },
  {
    field: "agent",
    pick: (n) => (n.actor_agent_id ?? n.agent) || null,
  },
];

function toEvidenceText(evidence: unknown): string | null {
  if (evidence == null) return null;
  if (typeof evidence === "string") return evidence || null;
  try {
    return JSON.stringify(evidence);
  } catch {
    return null;
  }
}

/**
 * In-order keyword search across title / conclusion / evidence / agent.
 *
 * Returns one `StarSearchMatch` per node (first matching field wins, to keep
 * the result set bounded and step-through friendly). Order follows the input
 * node order (stable, deterministic). A blank / trimmed-empty query matches
 * nothing (the caller treats "no search" separately, mirroring `isBlankFilter`).
 */
export function searchStarGraphNodes(
  nodes: readonly ResearchCanvasFilterableNode[],
  query: string,
): StarSearchMatch[] {
  const q = (query || "").trim().toLowerCase();
  if (!q) return EMPTY_MATCHES;

  const matches: StarSearchMatch[] = [];
  for (const node of nodes) {
    for (const { field, pick } of FIELD_RESOLVERS) {
      const text = pick(node);
      if (text == null || text === "") continue;
      const str = String(text);
      const idx = str.toLowerCase().indexOf(q);
      if (idx < 0) continue;
      matches.push({
        nodeId: node.id,
        field,
        matched: str.slice(idx, idx + q.length),
        snippet: snippetAround(str, idx, q.length),
      });
      break; // first matching field per node
    }
  }
  return matches;
}

const SNIPPET_CONTEXT = 24;

/** Return a ~2×context window around `[from, from+len)` in `text`. */
function snippetAround(text: string, from: number, len: number): string {
  const start = Math.max(0, from - SNIPPET_CONTEXT);
  const end = Math.min(text.length, from + len + SNIPPET_CONTEXT);
  const prefix = start > 0 ? "…" : "";
  const suffix = end < text.length ? "…" : "";
  return `${prefix}${text.slice(start, end)}${suffix}`;
}

/**
 * Location cursor: step through an ordered list of node ids (e.g. the matched
 * results from `searchStarGraphNodes`, or any ordered visible id list), reading
 * the "current" location and returning the next one in `direction` while
 * "keeping canvas context" (it only returns an id to select/focus; it never
 * resets viewport/filter).
 *
 *  - currentId == null → returns the first (next) or last (prev) node.
 *  - When current is not in the set, starts at the first result.
 *  - Wraps around at both ends (cyclic stepping keeps the user in the result
 *    set).
 */
export function searchLocateCursor(
  nodeIds: readonly string[],
  currentId: string | null,
  direction: "next" | "prev",
): string | null {
  if (nodeIds.length === 0) return null;
  if (currentId == null) {
    const at = direction === "next" ? 0 : nodeIds.length - 1;
    return nodeIds[at] ?? null;
  }
  const idx = nodeIds.indexOf(currentId);
  if (idx < 0) {
    return nodeIds[0] ?? null;
  }
  const step =
    direction === "next"
      ? (idx + 1) % nodeIds.length
      : (idx - 1 + nodeIds.length) % nodeIds.length;
  return nodeIds[step] ?? null;
}

/** Convenience: reduce search matches to an ordered locate id list. */
export function matchNodeIds(matches: readonly StarSearchMatch[]): string[] {
  return matches.map((m) => m.nodeId);
}
