/**
 * @ picker scope for group channels (Parker / Alice #1984, Raft-aligned).
 *
 * - **Group**: workspace members + workspace agents (and any channel-only
 *   co-members) so the user can pick people not yet in the channel. Delivery
 *   still requires membership — BE returns `undelivered_mentions` for invite.
 * - **DM / private-style narrow**: stay on channel members only (fail-closed).
 */

export function buildGroupMentionAllowedActorIds(args: {
  workspaceUserIds: readonly string[];
  workspaceAgentIds: readonly string[];
  channelMemberIds: readonly string[];
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
  return out;
}

export function inviteableUndeliveredMentions(
  undelivered: readonly {
    type: string;
    id: string;
    actions?: readonly string[];
  }[] | null | undefined,
): Array<{ member_type: "user" | "agent"; member_id: string }> {
  if (!undelivered?.length) return [];
  const out: Array<{ member_type: "user" | "agent"; member_id: string }> = [];
  const seen = new Set<string>();
  for (const u of undelivered) {
    if (!u.id || !u.actions?.includes("invite")) continue;
    const member_type = u.type === "agent" ? "agent" : "user";
    const key = `${member_type}:${u.id}`;
    if (seen.has(key)) continue;
    seen.add(key);
    out.push({ member_type, member_id: u.id });
  }
  return out;
}
