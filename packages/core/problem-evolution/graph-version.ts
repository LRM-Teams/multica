/**
 * Graph-version ordering for problem-evolution runs.
 *
 * The server owns `graph_version` and bumps it inside every write that changes
 * the graph. Both WebSocket events and snapshot responses carry the version
 * they were produced at, which lets the client drop anything that is not newer
 * than what it already renders — the case that otherwise makes the canvas flap
 * back to an older graph when an invalidate arrives before a slower snapshot.
 */

export type GraphVersionDecision = "apply" | "discard" | "refetch";

/**
 * Decide what to do with an incoming payload carrying `incomingVersion` when
 * the canvas currently renders `renderedVersion`.
 *
 * A version far ahead of the local one means intermediate updates were missed;
 * there is no incremental backfill, so the caller refetches the snapshot.
 */
export function decideGraphVersion(
  renderedVersion: number,
  incomingVersion: number,
  options?: { maxSkew?: number },
): GraphVersionDecision {
  const maxSkew = options?.maxSkew ?? 1;
  if (!Number.isFinite(incomingVersion) || incomingVersion <= renderedVersion) {
    return "discard";
  }
  if (incomingVersion - renderedVersion > maxSkew) {
    return "refetch";
  }
  return "apply";
}

/** Whether a snapshot response should replace the currently rendered graph. */
export function shouldAcceptSnapshot(
  renderedVersion: number,
  snapshotVersion: number,
): boolean {
  return Number.isFinite(snapshotVersion) && snapshotVersion > renderedVersion;
}
