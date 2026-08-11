import type { TypedGraphNode } from "@multica/core/research";

export interface D5FilterOptions {
  statuses: readonly string[];
  tiers: readonly string[];
  rounds: readonly string[];
  clusters: readonly string[];
}

const TIER_ORDER = ["xxl", "xl", "l", "m", "s"];

function sortTiers(values: Iterable<string>): string[] {
  const list = [...values];
  return list.sort(
    (a, b) =>
      (TIER_ORDER.indexOf(a) === -1 ? 99 : TIER_ORDER.indexOf(a)) -
      (TIER_ORDER.indexOf(b) === -1 ? 99 : TIER_ORDER.indexOf(b)),
  );
}

/** Derive display-only filter facets from the canonical typed graph. */
export function buildD5FilterOptions(
  nodes: readonly TypedGraphNode[],
): D5FilterOptions {
  const statuses = new Set<string>();
  const tiers = new Set<string>();
  const rounds = new Set<string>();
  const clusters = new Set<string>();

  for (const node of nodes) {
    const status = (node.status || "").trim();
    if (status) statuses.add(status);

    const level = (node.level || "").trim().toLowerCase();
    if (level) tiers.add(level);

    if (node.round != null && node.round > 0) {
      rounds.add(String(node.round));
    }

    const cluster = (node.cluster_id || "").trim();
    if (cluster) clusters.add(cluster);
  }

  return {
    statuses: [...statuses].sort(),
    tiers: sortTiers(tiers),
    rounds: [...rounds].sort((a, b) => Number(a) - Number(b)),
    clusters: [...clusters].sort(),
  };
}
