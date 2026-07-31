import { useMemo } from "react";
import { useQuery } from "@tanstack/react-query";
import { useAuthStore } from "../auth";
import { runtimeListOptions } from "./queries";
import {
  runtimeCanStartSelfUpdate,
  runtimeHasHealthAttention,
} from "./runtime-health-state";

/**
 * Returns true if the current user has any local runtime needing health attention.
 * Accepts wsId as parameter so callers outside WorkspaceIdProvider can use it safely.
 */
export function useMyRuntimeHealthAttention(wsId: string | undefined): boolean {
  const userId = useAuthStore((s) => s.user?.id);
  const { data: runtimes } = useQuery({
    ...runtimeListOptions(wsId ?? ""),
    enabled: !!wsId,
  });

  if (!runtimes || !userId) return false;

  return runtimes.some((runtime) =>
    runtimeHasHealthAttention(runtime, userId),
  );
}

/**
 * Backward-compatible alias for callers that still only need a boolean badge.
 */
export const useMyRuntimesNeedUpdate = useMyRuntimeHealthAttention;

/**
 * Count of the current user's local runtimes needing health attention (task
 * #9, 2026-07-31) — same underlying predicate as `useMyRuntimeHealthAttention`
 * (`runtimeHasHealthAttention`), so the sidebar badge and its popover count
 * never disagree about which runtimes qualify (e.g. both correctly exclude
 * sandbox daemons and desktop-managed runtimes).
 */
export function useMyAttentionRuntimeCount(wsId: string | undefined): number {
  const userId = useAuthStore((s) => s.user?.id);
  const { data: runtimes } = useQuery({
    ...runtimeListOptions(wsId ?? ""),
    enabled: !!wsId,
  });

  return useMemo(() => {
    if (!runtimes || !userId) return 0;
    let count = 0;
    for (const runtime of runtimes) {
      if (runtimeHasHealthAttention(runtime, userId)) count += 1;
    }
    return count;
  }, [runtimes, userId]);
}

/**
 * Returns runtime IDs that can start an update from the current user.
 * Accepts wsId as parameter so callers outside WorkspaceIdProvider can use it safely.
 */
export function useUpdatableRuntimeIds(wsId: string | undefined): Set<string> {
  const userId = useAuthStore((s) => s.user?.id);
  const { data: runtimes } = useQuery({
    ...runtimeListOptions(wsId ?? ""),
    enabled: !!wsId,
  });

  return useMemo(() => {
    if (!runtimes || !userId) return new Set<string>();
    const ids = new Set<string>();
    for (const runtime of runtimes) {
      if (runtimeCanStartSelfUpdate(runtime, userId)) {
        ids.add(runtime.id);
      }
    }
    return ids;
  }, [runtimes, userId]);
}
