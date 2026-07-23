import type { Agent } from "../types";

/**
 * Invite-panel filter aligned with ListAgents(?channel_id=) / canInviteAgentToChannel
 * (LRM-370 / LRM-399): channel-visibility agents are only inviteable into their
 * home group. Workspace/private agents pass through; callers still exclude
 * agents already in the channel roster.
 *
 * Prefer fetching with `channel_id` so the server hides out-of-home channel
 * agents (and group managers). This client guard keeps the panel consistent if
 * a stale workspace-wide list is ever reused.
 */
export function filterAgentsForChannelInvite(
  agents: readonly Agent[],
  channelId: string,
): Agent[] {
  const home = channelId.trim();
  if (!home) return [];
  return agents.filter((a) => {
    if (a.archived_at) return false;
    if (a.visibility === "channel") {
      return !!a.home_channel_id && a.home_channel_id === home;
    }
    return true;
  });
}
