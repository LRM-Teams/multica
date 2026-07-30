import type { ResearchFleetMember, ResearchMessage } from "@multica/core/types";

function metaString(meta: unknown, key: string): string | null {
  if (!meta || typeof meta !== "object") return null;
  const value = (meta as Record<string, unknown>)[key];
  return typeof value === "string" && value.trim() ? value : null;
}

/** Resolve the speaking agent — never treat target_agent_id as the speaker. */
export function speakerMemberForMessage(
  message: ResearchMessage,
  members: ResearchFleetMember[],
): ResearchFleetMember | undefined {
  if (message.sender_type === "user") return undefined;
  const actorFromMeta = metaString(message.meta, "actor_agent_id");
  const agentId =
    actorFromMeta ||
    (message.sender_type === "agent" || message.sender_type === "system"
      ? message.sender_id
      : null);
  if (!agentId) return undefined;
  return members.find((m) => m.agent_id === agentId);
}
