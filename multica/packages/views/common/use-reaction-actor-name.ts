"use client";

import { useCallback, useMemo } from "react";
import { useQueries } from "@tanstack/react-query";
import { useWorkspaceId } from "@multica/core/hooks";
import { memberProfileOptions } from "@multica/core/workspace/queries";
import { useActorName } from "@multica/core/workspace/hooks";
import {
  directoryActorDisplayName,
  profileActorDisplayName,
  toDirectoryActorType,
  toMemberProfileType,
} from "@multica/core/workspace/resolved-actor-name";

type ReactionActorRef = {
  actor_type: string;
  actor_id: string;
};

/**
 * LRM-364: ReactionBar needs a sync `getActorName(type, id) => string`. Group
 * managers are omitted from ListAgents (LRM-233), so the plain directory hook
 * returns "Unknown Agent". Prefetch member-profiles for directory misses among
 * the given reactions, then return a resolver that prefers directory → profile
 * → honest id placeholder (never the Unknown* sentinel).
 */
export function useReactionActorName(
  reactions: ReadonlyArray<ReactionActorRef>,
): (type: string, id: string) => string {
  const { getActorName } = useActorName();
  const wsId = useWorkspaceId();

  const uniqueActors = useMemo(() => {
    const map = new Map<string, { type: "agent" | "member"; id: string }>();
    for (const reaction of reactions) {
      const type = toDirectoryActorType(reaction.actor_type);
      if (!type || !reaction.actor_id) continue;
      const key = `${type}:${reaction.actor_id}`;
      if (!map.has(key)) map.set(key, { type, id: reaction.actor_id });
    }
    return Array.from(map.values());
  }, [reactions]);

  const directoryNames = useMemo(() => {
    const hits = new Map<string, string>();
    for (const actor of uniqueActors) {
      const name = directoryActorDisplayName(getActorName, actor.type, actor.id);
      if (name) hits.set(`${actor.type}:${actor.id}`, name);
    }
    return hits;
  }, [uniqueActors, getActorName]);

  const misses = useMemo(
    () => uniqueActors.filter((actor) => !directoryNames.has(`${actor.type}:${actor.id}`)),
    [uniqueActors, directoryNames],
  );

  const profileQueries = useQueries({
    queries: misses.map((actor) => ({
      ...memberProfileOptions(wsId, toMemberProfileType(actor.type), actor.id),
      enabled: !!wsId && !!actor.id,
    })),
  });

  const profileNames = useMemo(() => {
    const hits = new Map<string, string>();
    misses.forEach((actor, index) => {
      const name = profileActorDisplayName(profileQueries[index]?.data);
      if (name) hits.set(`${actor.type}:${actor.id}`, name);
    });
    return hits;
  }, [misses, profileQueries]);

  return useCallback(
    (type: string, id: string) => {
      const mentionType = toDirectoryActorType(type);
      if (!mentionType || !id) {
        // Unrecognized actor type — never invent "Unknown Agent".
        return id || type || "";
      }
      const key = `${mentionType}:${id}`;
      return directoryNames.get(key) ?? profileNames.get(key) ?? id;
    },
    [directoryNames, profileNames],
  );
}
