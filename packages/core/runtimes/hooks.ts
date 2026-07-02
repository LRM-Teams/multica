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
