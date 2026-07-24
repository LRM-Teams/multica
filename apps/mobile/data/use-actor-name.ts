import { useQuery } from "@tanstack/react-query";
import { useWorkspaceStore } from "@/data/workspace-store";
import { memberListOptions, memberProfileOptions } from "@/data/queries/members";
import { agentListOptions } from "@/data/queries/agents";
import {
  isDirectoryActorMiss,
  profileActorDisplayName,
  toDirectoryActorType,
  toMemberProfileType,
} from "@multica/core/workspace/resolved-actor-name";
import { resolveActorDisplayName } from "@multica/core/identity";

/**
 * Resolve actor (member / agent) name + avatar URL from the
 * workspace lists. Mirrors packages/core/workspace/hooks.ts useActorName.
 *
 * LRM-391: when ListAgents omits channel/private / group-manager agents,
 * fall through to GET /member-profiles so read-only chrome never shows
 * "Unknown Agent".
 *
 * Returns synchronous lookup helpers — they read whatever is in the TQ
 * cache. If the lists haven't loaded yet, lookups return null/initials
 * fallback; the row will re-render once data arrives.
 */
export function useActorLookup() {
  const wsId = useWorkspaceStore((s) => s.currentWorkspaceId);
  const { data: members = [] } = useQuery(memberListOptions(wsId));
  const { data: agents = [] } = useQuery(agentListOptions(wsId));

  const getName = (
    type: "member" | "agent" | "squad" | null | undefined,
    id: string | null | undefined,
  ): string => {
    if (!type || !id) return "System";
    if (type === "member") {
      const m = members.find((m) => m.user_id === id);
      return resolveActorDisplayName(m, "Unknown");
    }
    if (type === "agent") {
      const a = agents.find((a) => a.id === id);
      return resolveActorDisplayName(a, "Unknown Agent");
    }
    return "Unsupported assignee (squad)";
  };

  const getAvatarUrl = (
    type: "member" | "agent" | "squad" | null | undefined,
    id: string | null | undefined,
  ): string | null => {
    if (!type || !id) return null;
    if (type === "member") {
      return members.find((m) => m.user_id === id)?.avatar_url ?? null;
    }
    if (type === "agent") {
      return agents.find((a) => a.id === id)?.avatar_url ?? null;
    }
    return null;
  };

  return { getName, getAvatarUrl };
}

/**
 * LRM-391: comment / activity author chrome — directory first, then
 * member-profile. Never returns the Unknown* sentinel once a profile loads;
 * while pending uses the stable actor id.
 */
export function useResolvedActorName(
  type: "member" | "agent" | "squad" | "system" | null | undefined,
  id: string | null | undefined,
): { name: string; avatarUrl: string | null } {
  const { getName, getAvatarUrl } = useActorLookup();
  const wsId = useWorkspaceStore((s) => s.currentWorkspaceId);
  const mentionType = toDirectoryActorType(type ?? undefined);
  const directoryName = type && id ? getName(type === "system" ? null : type, id) : "";
  const directoryMiss =
    !!mentionType && !!id && isDirectoryActorMiss(directoryName);

  const { data: profile } = useQuery({
    ...memberProfileOptions(
      wsId,
      mentionType ? toMemberProfileType(mentionType) : null,
      id,
    ),
    enabled: !!wsId && directoryMiss,
  });

  if (!type || !id) {
    return { name: "System", avatarUrl: null };
  }
  if (type === "squad" || type === "system") {
    return { name: getName(type === "system" ? null : type, id), avatarUrl: getAvatarUrl(type === "system" ? null : type, id) };
  }
  if (!directoryMiss) {
    return { name: directoryName, avatarUrl: getAvatarUrl(type, id) };
  }
  const profileName = profileActorDisplayName(profile);
  return {
    name: profileName ?? id,
    avatarUrl: profile?.avatar_url ?? null,
  };
}

export function getInitials(name: string): string {
  return name
    .split(" ")
    .map((w) => w[0])
    .filter(Boolean)
    .join("")
    .toUpperCase()
    .slice(0, 2);
}
