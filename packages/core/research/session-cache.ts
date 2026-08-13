import type { QueryClient, QueryKey } from "@tanstack/react-query";
import { researchKeys } from "./queries";

/** Every D5 query family whose facts belong exclusively to one session. */
export function researchSessionQueryPrefixes(
  wsId: string,
  sessionId: string,
): readonly QueryKey[] {
  return [
    researchKeys.snapshot(wsId, sessionId),
    researchKeys.presence(wsId, sessionId),
    researchKeys.productRounds(wsId, sessionId),
    ["research", wsId, "graph-typed", sessionId],
    researchKeys.graphTypedInfinite(wsId, sessionId),
    // Capability probe and V5/V6 canvas adapter caches are session-scoped even
    // though their family is separate from the legacy `research` prefix.
    ["research-canvas", wsId, sessionId],
  ];
}

/**
 * Cancels in-flight reads before removing all session-owned server state.
 * Cancellation is required: otherwise a late response can repopulate a cache
 * after the session was successfully deleted.
 */
export async function evictResearchSessionQueries(
  queryClient: QueryClient,
  wsId: string,
  sessionId: string,
): Promise<void> {
  const prefixes = researchSessionQueryPrefixes(wsId, sessionId);
  await Promise.all(
    prefixes.map((queryKey) =>
      queryClient.cancelQueries({ queryKey, exact: false }),
    ),
  );
  for (const queryKey of prefixes) {
    queryClient.removeQueries({ queryKey, exact: false });
  }
}
