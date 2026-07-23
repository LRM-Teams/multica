"use client";

import { useMemo } from "react";
import { useQuery, type QueryClient } from "@tanstack/react-query";
import { memberPresenceOptions, workspaceKeys } from "./queries";
import type { MemberPresenceEntry, MemberPresenceResponse } from "../types/workspace";

/**
 * Workspace-wide human online set (LRM-462). Prefer this over per-avatar
 * fetches — one query + WS patches keep every member dot in sync.
 */
export function useWorkspaceMemberPresenceMap(wsId: string | undefined): {
  onlineUserIds: Set<string>;
  loading: boolean;
} {
  const { data, isPending, isError } = useQuery({
    ...memberPresenceOptions(wsId ?? ""),
    enabled: !!wsId,
  });

  const onlineUserIds = useMemo(() => {
    const set = new Set<string>();
    for (const m of data?.members ?? []) {
      if (m.status === "online") set.add(m.user_id);
    }
    return set;
  }, [data]);

  return {
    onlineUserIds,
    loading: isPending && !isError,
  };
}

/** Single-member online lookup for avatar dots. Missing = offline. */
export function useMemberOnline(
  wsId: string | undefined,
  userId: string | undefined,
): boolean | "loading" {
  const { onlineUserIds, loading } = useWorkspaceMemberPresenceMap(wsId);
  if (!wsId || !userId) return "loading";
  if (loading) return "loading";
  return onlineUserIds.has(userId);
}

/**
 * Apply a member:presence WS event into the react-query cache.
 * Exported for useRealtimeSync and unit tests.
 */
export function applyMemberPresenceEvent(
  qc: QueryClient,
  wsId: string,
  entry: MemberPresenceEntry,
) {
  if (!wsId || !entry.user_id) return;
  qc.setQueryData<MemberPresenceResponse>(
    workspaceKeys.memberPresence(wsId),
    (prev) => {
      const members = [...(prev?.members ?? [])];
      const idx = members.findIndex((m) => m.user_id === entry.user_id);
      if (entry.status === "online") {
        const next: MemberPresenceEntry = {
          user_id: entry.user_id,
          status: "online",
          observed_at: entry.observed_at,
        };
        if (idx >= 0) members[idx] = next;
        else members.push(next);
      } else if (idx >= 0) {
        members.splice(idx, 1);
      }
      return { members };
    },
  );
}
