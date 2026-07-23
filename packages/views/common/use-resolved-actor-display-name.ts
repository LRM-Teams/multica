"use client";

import {
  useResolvedActorIdentity,
} from "./use-resolved-actor-identity";

/**
 * LRM-281 / LRM-238: resolve display names from the live actor directory
 * (`useActorName` / ListAgents+members) or a dedicated member-profile fetch
 * (DB). Never use emit-time actor_name / target_name as a silent fallback —
 * ListAgents hides group managers (LRM-233), so denormalized params would
 * paper over an incomplete directory.
 *
 * Prefer the directory (same cache the rest of chat uses). Only when that
 * returns the honest unknown sentinel do we hit `GET /member-profiles`.
 *
 * Returns null while pending / on hard miss — callers must not invent copy
 * from emit-time params (use a typed id placeholder if a label is required).
 *
 * LRM-391: same path also powers comment / Activity author chrome via
 * {@link useResolvedActorIdentity} (name + avatar).
 */
export function useResolvedActorDisplayName(
  actorId: string | undefined,
  mentionType: "agent" | "member" | null,
): string | null {
  return useResolvedActorIdentity(actorId, mentionType).displayName;
}
