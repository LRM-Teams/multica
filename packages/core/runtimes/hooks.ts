import { useMemo } from "react";
import { useQuery } from "@tanstack/react-query";
import { useAuthStore } from "../auth";
import type { AgentRuntime } from "../types";
import { computerListOptions, runtimeListOptions } from "./queries";
import {
  runtimeCanStartSelfUpdate,
  runtimeHasHealthAttention,
} from "./runtime-health-state";

export const RUNTIME_ATTENTION_RUNTIME_QUERY = "attention_runtime";

/**
 * Ids of the runtimes this viewer may bind an agent to, or null when the
 * server did not say.
 *
 * Where an agent runs is chosen at two levels — machine, then provider — so
 * the Computer list is where the choice belongs: each Computer carries the
 * runtimes on it the caller may pick, already filtered server-side. That
 * filter (visibility, which exists only at the runtime level) now has exactly
 * one definition instead of one per picker.
 *
 * Null means an older server that omits the field; callers fall back to the
 * legacy client-side rule rather than offering nothing.
 */
export function useBindableRuntimeIds(wsId: string | undefined): Set<string> | null {
  const { data: computers } = useQuery({
    ...computerListOptions(wsId ?? ""),
    enabled: !!wsId,
  });
  return useMemo(() => {
    if (!computers) return null;
    let sawField = false;
    const ids = new Set<string>();
    for (const computer of computers) {
      if (!computer.runtimes) continue;
      sawField = true;
      for (const runtime of computer.runtimes) ids.add(runtime.id);
    }
    return sawField ? ids : null;
  }, [computers]);
}

export interface MyAttentionRuntimeSummary {
  count: number;
  firstRuntimeId: string | null;
}

/**
 * Machine key for attention aggregation. Prefer daemon_id (one computer may
 * host many provider runtimes); fall back to runtime id when daemon_id is
 * missing so orphan rows still count once.
 */
export function attentionMachineKey(runtime: AgentRuntime): string {
  const daemon = runtime.daemon_id?.trim();
  return daemon || `runtime:${runtime.id}`;
}

/**
 * Distinct machines owned by `userId` that need health attention.
 * Pure helper for tests and hooks (task #31 — owner-only, machine-level).
 */
export function summarizeMyAttentionMachines(
  runtimes: readonly AgentRuntime[] | null | undefined,
  userId: string | null | undefined,
): MyAttentionRuntimeSummary {
  if (!runtimes || !userId) return { count: 0, firstRuntimeId: null };
  const keys = new Set<string>();
  let firstRuntimeId: string | null = null;
  for (const runtime of runtimes) {
    if (!runtimeHasHealthAttention(runtime, userId)) continue;
    const machineKey = attentionMachineKey(runtime);
    if (keys.has(machineKey)) continue;
    keys.add(machineKey);
    firstRuntimeId ??= runtime.id;
  }
  return { count: keys.size, firstRuntimeId };
}

export function countMyAttentionMachines(
  runtimes: readonly AgentRuntime[] | null | undefined,
  userId: string | null | undefined,
): number {
  return summarizeMyAttentionMachines(runtimes, userId).count;
}

export function useMyAttentionRuntimeSummary(
  wsId: string | undefined,
): MyAttentionRuntimeSummary {
  const userId = useAuthStore((s) => s.user?.id);
  const { data: runtimes } = useQuery({
    ...runtimeListOptions(wsId ?? ""),
    enabled: !!wsId,
  });

  return useMemo(
    () => summarizeMyAttentionMachines(runtimes, userId),
    [runtimes, userId],
  );
}

/**
 * Returns true if the current user has any local runtime needing health attention.
 * Accepts wsId as parameter so callers outside WorkspaceIdProvider can use it safely.
 */
export function useMyRuntimeHealthAttention(wsId: string | undefined): boolean {
  return useMyAttentionRuntimeSummary(wsId).count > 0;
}

/**
 * Backward-compatible alias for callers that still only need a boolean badge.
 */
export const useMyRuntimesNeedUpdate = useMyRuntimeHealthAttention;

/**
 * Count of **machines** (daemon_id) owned by the current user that need health
 * attention (task #9 / #31). Uses `runtimeHasHealthAttention` per runtime
 * (owner=me, local, not sandbox/desktop-managed), then collapses same-daemon
 * rows so the sidebar "N machines have updates" matches product language —
 * never counts another user's computers.
 */
export function useMyAttentionRuntimeCount(wsId: string | undefined): number {
  return useMyAttentionRuntimeSummary(wsId).count;
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
