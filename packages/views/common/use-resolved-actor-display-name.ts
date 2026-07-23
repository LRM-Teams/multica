"use client";

import { useQuery } from "@tanstack/react-query";
import { useWorkspaceId } from "@multica/core/hooks";
import { memberProfileOptions } from "@multica/core/workspace/queries";
import { useActorName } from "@multica/core/workspace/hooks";
import {
  directoryActorDisplayName,
  profileActorDisplayName,
  toMemberProfileType,
} from "@multica/core/workspace/resolved-actor-name";

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
 */
export function useResolvedActorDisplayName(
  actorId: string | undefined,
  mentionType: "agent" | "member" | null,
): string | null {
  const { getActorName } = useActorName();
  const fromDirectory =
    actorId && mentionType
      ? directoryActorDisplayName(getActorName, mentionType, actorId)
      : null;

  const wsId = useWorkspaceId();
  const profileType = mentionType ? toMemberProfileType(mentionType) : "user";
  const { data: profile } = useQuery({
    ...memberProfileOptions(wsId, profileType, actorId ?? ""),
    enabled: !!wsId && !!actorId && mentionType != null && !fromDirectory,
  });

  if (!actorId || !mentionType) return null;
  if (fromDirectory) return fromDirectory;
  return profileActorDisplayName(profile);
}
