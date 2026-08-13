/**
 * @ picker scope for group channels (Parker / Alice #1984, Raft-aligned).
 *
 * - **Group**: workspace members + workspace agents (and any channel-only
 *   co-members) so the user can pick people not yet in the channel. Delivery
 *   still requires membership — BE returns `undelivered_mentions` for invite.
 * - **Viewer**: the current human is never a group @ target. @yourself is
 *   not a useful notify/wake, and listing the sender first is noise.
 * - **DM / private-style narrow**: stay on channel members only (fail-closed).
 */

export function buildGroupMentionAllowedActorIds(args: {
  workspaceUserIds: readonly string[];
  workspaceAgentIds: readonly string[];
  channelMemberIds: readonly string[];
  /** Current human user id. Always dropped from the allowlist. */
  viewerUserId: string | null | undefined;
}): Set<string> {
  const out = new Set<string>();
  for (const id of args.workspaceUserIds) {
    if (id) out.add(id);
  }
  for (const id of args.workspaceAgentIds) {
    if (id) out.add(id);
  }
  // Channel co-members (e.g. a teammate's agent not in personal list).
  for (const id of args.channelMemberIds) {
    if (id) out.add(id);
  }
  if (args.viewerUserId) out.delete(args.viewerUserId);
  return out;
}

export function inviteableUndeliveredMentions(
  undelivered: readonly {
    type: string;
    id: string;
    handle?: string;
    label?: string;
    actions?: readonly string[];
  }[] | null | undefined,
): Array<{
  member_type: "user" | "agent";
  member_id: string;
  display: string;
}> {
  if (!undelivered?.length) return [];
  const out: Array<{
    member_type: "user" | "agent";
    member_id: string;
    display: string;
  }> = [];
  const seen = new Set<string>();
  for (const u of undelivered) {
    if (!u.id) continue;
    let canInvite = false;
    for (const a of u.actions ?? []) {
      if (a === "invite") {
        canInvite = true;
        break;
      }
    }
    if (!canInvite) continue;
    const member_type = u.type === "agent" ? "agent" : "user";
    const key = `${member_type}:${u.id}`;
    if (seen.has(key)) continue;
    seen.add(key);
    out.push({
      member_type,
      member_id: u.id,
      display: u.label || u.handle || u.id,
    });
  }
  return out;
}
