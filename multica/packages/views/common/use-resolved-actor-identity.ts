"use client";

import { useQuery } from "@tanstack/react-query";
import { useWorkspaceId } from "@multica/core/hooks";
import { memberProfileOptions } from "@multica/core/workspace/queries";
import { useActorName } from "@multica/core/workspace/hooks";
import { resolvePublicFileUrl } from "@multica/core/workspace/avatar-url";
import {
  directoryActorDisplayName,
  profileActorDisplayName,
  toDirectoryActorType,
  toMemberProfileType,
} from "@multica/core/workspace/resolved-actor-name";

export type ResolvedActorIdentity = {
  /** Live display name, or null while pending / on hard miss. */
  displayName: string | null;
  /** Face URL from directory or member-profile; null when unknown. */
  avatarUrl: string | null;
};

/**
 * LRM-391 / LRM-281 / LRM-238: resolve actor identity for read-only surfaces.
 *
 * ListAgents filters by visibility (channel/private) and hides group managers
 * (LRM-233), so `useActorName` alone can return "Unknown Agent". Visibility
 * must not erase display names on comments / Activity / avatars — only invite,
 * discovery, and @mention candidates (LRM-240).
 *
 * Prefer the workspace directory; on miss hit GET /member-profiles (always
 * returns identity, including identity_only for private agents).
 */
export function useResolvedActorIdentity(
  actorId: string | undefined,
  mentionType: "agent" | "member" | null,
): ResolvedActorIdentity {
  const { getActorName, getActorAvatarUrl } = useActorName();
  const fromDirectory =
    actorId && mentionType
      ? directoryActorDisplayName(getActorName, mentionType, actorId)
      : null;
  const directoryAvatar =
    actorId && mentionType && typeof getActorAvatarUrl === "function"
      ? getActorAvatarUrl(mentionType, actorId)
      : null;

  const directoryAvatarResolved = resolvePublicFileUrl(directoryAvatar);
  // AC#5 / LRM-391: directory may have a name but no face (ListAgents thin).
  // Still hit member-profiles for avatar — do not leave Presence/Working blank.
  const needsProfile = !fromDirectory || !directoryAvatarResolved;

  const wsId = useWorkspaceId();
  const profileType = mentionType ? toMemberProfileType(mentionType) : "user";
  const { data: profile } = useQuery({
    ...memberProfileOptions(wsId, profileType, actorId ?? ""),
    enabled: !!wsId && !!actorId && mentionType != null && needsProfile,
  });

  if (!actorId || !mentionType) {
    return { displayName: null, avatarUrl: null };
  }
  const profileName = profileActorDisplayName(profile);
  const profileAvatar = resolvePublicFileUrl(profile?.avatar_url);
  return {
    displayName: fromDirectory ?? profileName,
    avatarUrl: directoryAvatarResolved ?? profileAvatar,
  };
}

/**
 * Resolve a timeline / comment `actor_type` string onto mention types used by
 * {@link useResolvedActorIdentity}. Returns null for system / unknown.
 */
export function mentionTypeFromActorType(
  actorType: string | null | undefined,
): "agent" | "member" | null {
  return toDirectoryActorType(actorType ?? undefined);
}

/**
 * Label for read-only chrome: real name when known, else stable id placeholder.
 * Never returns the "Unknown Agent" / "Unknown" directory sentinels.
 */
export function resolvedActorLabel(
  identity: ResolvedActorIdentity,
  actorId: string | undefined,
): string {
  return identity.displayName ?? actorId ?? "";
}
