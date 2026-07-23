import type { Agent } from "../types";

/**
 * Whether an agent may appear in the invite / discover directory for a
 * specific group channel (LRM-399 / LRM-240).
 *
 * Mirrors backend ListAgents(`channel_id`) + group-manager exclusion:
 * - group managers (贝克汉姆) never appear in invite — they join via hire/create
 * - `visibility=channel` agents only when `home_channel_id` equals the target
 * - missing home on a channel agent is an explicit reject (no silent remap)
 *
 * Callers still pass `channel_id` to ListAgents; this is defense-in-depth so a
 * stale workspace-wide cache cannot leak other groups' channel agents.
 */
export function isAgentInviteDiscoverableInChannel(
  agent: Pick<Agent, "visibility" | "home_channel_id" | "managed_role" | "archived_at">,
  channelId: string | null | undefined,
): boolean {
  if (agent.archived_at) return false;
  if (agent.managed_role === "group_manager") return false;
  if (!channelId) {
    // No channel context → same as workspace directory: channel agents stay hidden.
    return agent.visibility !== "channel";
  }
  if (agent.visibility !== "channel") return true;
  const home = agent.home_channel_id?.trim() ?? "";
  if (!home) return false;
  return home === channelId;
}
