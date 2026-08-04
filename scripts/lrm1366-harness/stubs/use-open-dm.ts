/**
 * LRM-1366 harness stub — the real hook needs a NavigationAdapter and workspace
 * paths to push a route. The picker is only rendered here so the empty-state CTA
 * and the header `+` keep their shipped markup.
 */
import type { DMItem } from "@multica/core/dm";

export function useOpenDM(): {
  openDM: (body: unknown) => Promise<DMItem | null>;
  isPending: boolean;
} {
  return { openDM: async () => null, isPending: false };
}
