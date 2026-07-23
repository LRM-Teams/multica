/**
 * Mirrors server `agentVisibleInChannelContext` (LRM-240 / LRM-370 / LRM-399).
 *
 * Channel-visibility agents are discoverable only inside their home group —
 * workspace directory (no channel context) and other groups must not list them
 * for invite / discovery. Non-channel agents always pass this gate (private
 * filtering is separate).
 */
export function isAgentDiscoverableInChannelContext(
  agent: {
    visibility?: string | null;
    home_channel_id?: string | null;
  },
  listChannelId: string | null | undefined,
): boolean {
  if (agent.visibility !== "channel") return true;
  if (!listChannelId) return false;
  return !!agent.home_channel_id && agent.home_channel_id === listChannelId;
}
