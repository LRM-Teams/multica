import { useEffect, useRef, useState } from "react";

/**
 * Drives an infinite-query message list to load older pages until a jump
 * target (search hit / quote back-reference) is present in the loaded pages,
 * or history is exhausted.
 *
 * The channel/DM message viewport can only scroll to and highlight a message
 * it has actually loaded (`highlightIndex < 0` is a no-op). When a search
 * result or quote jump points at a message that lives in an older, not-yet
 * -fetched page, the jump would otherwise silently fail. This hook fetches
 * earlier pages one at a time until the target loads, then reports `found`
 * so the existing highlight effect can position it. If it runs out of pages
 * without finding the target it reports `exhausted` so the caller can surface
 * a visible "not found" state instead of failing silently.
 *
 * It concludes at most once per target id (found or exhausted) so a target
 * that can't be located does not re-trigger an endless fetch loop.
 */
export type EnsureMessageLoadedStatus = "idle" | "searching" | "found" | "exhausted";

export function useEnsureMessageLoaded(params: {
  /** Message id to bring into the loaded window; null/undefined = nothing to do. */
  targetId: string | null | undefined;
  /** Whether `targetId` is already present in the currently loaded pages. */
  targetLoaded: boolean;
  /** Whether an older page can still be fetched (infinite query `hasNextPage`). */
  hasOlder: boolean;
  /** Whether an older page is currently being fetched (`isFetchingNextPage`). */
  isFetchingOlder: boolean;
  /** Fetch the next older page (`fetchNextPage`). */
  fetchOlder: () => void;
}): EnsureMessageLoadedStatus {
  const { targetId, targetLoaded, hasOlder, isFetchingOlder, fetchOlder } = params;
  const [status, setStatus] = useState<EnsureMessageLoadedStatus>("idle");
  // The target id we've already reached a terminal conclusion for (found or
  // exhausted). Prevents re-driving the fetch loop for a target we can't find.
  const concludedForRef = useRef<string | null>(null);

  useEffect(() => {
    if (!targetId) {
      setStatus("idle");
      concludedForRef.current = null;
      return;
    }
    if (targetLoaded) {
      setStatus("found");
      concludedForRef.current = targetId;
      return;
    }
    // Target not in the loaded window.
    if (concludedForRef.current === targetId) {
      // Already exhausted history for this target — don't loop.
      return;
    }
    if (hasOlder) {
      setStatus("searching");
      if (!isFetchingOlder) fetchOlder();
      // Wait for the fetched page to change `targetLoaded`/`hasOlder`, which
      // re-runs this effect.
      return;
    }
    // No older pages left and the target never appeared.
    setStatus("exhausted");
    concludedForRef.current = targetId;
  }, [targetId, targetLoaded, hasOlder, isFetchingOlder, fetchOlder]);

  return status;
}
