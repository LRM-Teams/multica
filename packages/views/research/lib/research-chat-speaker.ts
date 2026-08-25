import type { ResearchPresenceMap } from "@multica/core/research";
import type { ResearchFleetMember, ResearchMessage } from "@multica/core/types";

function metaString(meta: unknown, key: string): string | null {
  if (!meta || typeof meta !== "object") return null;
  const value = (meta as Record<string, unknown>)[key];
  return typeof value === "string" && value.trim() ? value : null;
}

export interface ResearchChatSpeaker {
  agentId: string;
  name: string | null;
  role: string | null;
}

function nonEmpty(value: string | null | undefined): string | null {
  const trimmed = value?.trim();
  return trimmed ? trimmed : null;
}

/** Resolve the speaking agent — never treat target_agent_id as the speaker. */
export function researchChatSpeakerForMessage(
  message: ResearchMessage,
  members: ResearchFleetMember[],
  presence: ResearchPresenceMap = {},
): ResearchChatSpeaker | null {
  if (message.sender_type === "user") return null;
  const actorFromMeta = metaString(message.meta, "actor_agent_id");
  const agentId =
    actorFromMeta ||
    (message.sender_type === "agent" || message.sender_type === "system"
      ? message.sender_id
      : null);
  if (!agentId) return null;

  const member = members.find((candidate) => candidate.agent_id === agentId);
  const liveIdentity = presence[agentId];
  return {
    agentId,
    name:
      nonEmpty(liveIdentity?.name) ??
      nonEmpty(member?.display_name) ??
      nonEmpty(member?.name),
    role: nonEmpty(liveIdentity?.role) ?? nonEmpty(member?.role),
  };
}
