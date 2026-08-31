import type { Channel } from "../types";

/**
 * Human Goal writes: channel creator or workspace owner/admin.
 *
 * Unlike archive/settings, this does not exclude the system #general
 * channel. CreateChannelGoal has no immutable-system-channel check, so
 * wiring the empty-state card to canArchive hid Goal setup on #general.
 */
export function canManageChannelGoal(
  channel: Pick<Channel, "created_by">,
  currentUserId: string | null | undefined,
  workspaceRole: string | null | undefined,
): boolean {
  if (!currentUserId) return false;
  return (
    channel.created_by === currentUserId ||
    workspaceRole === "owner" ||
    workspaceRole === "admin"
  );
}
