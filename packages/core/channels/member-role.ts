import type { ChannelMember, ChannelMemberRole } from "../types";

/**
 * Pure helpers for group-membership roles (group management FE). Kept UI-free so
 * the badge scheme and the management-menu gate are unit-testable. The server
 * (BE #1284) is the source of `member.role`; a missing value means the least
 * privilege ("member").
 */

export function channelMemberRole(
  member: Pick<ChannelMember, "role">,
): ChannelMemberRole {
  return member.role ?? "member";
}

/**
 * Elevated-only badge, or `null` for ordinary members. Rendered ONLY in
 * management surfaces (the member list / settings) — never in the message
 * stream (Parker's durable rule: role markers stay out of content scenes).
 *   owner   → group owner (always human)
 *   manager → group manager, for humans and agents alike
 *   member  → no badge
 *
 * #832: this used to split `manager` by member type ("群管" for agents,
 * "管理员" for humans). One role now carries one name (Iris): showing an agent
 * as a different role than a human holding the same role invented a
 * distinction the permission model does not have, and — the part that decided
 * it — the action and its result then read differently ("make admin" producing
 * a row labelled "group manager").
 *
 * Display only. This value is never an authorization input: menu visibility
 * reads backend capability, never a badge (#844 §14).
 */
export type ChannelMemberBadge = "owner" | "manager";

export function channelMemberBadge(
  member: Pick<ChannelMember, "role">,
): ChannelMemberBadge | null {
  switch (channelMemberRole(member)) {
    case "owner":
      return "owner";
    case "manager":
      return "manager";
    case "member":
      return null;
  }
}

/**
 * Whether a viewer may open the management menu on other members at all.
 * Owner and manager both qualify (#845); *which* items they get is decided by
 * `groupMemberActions` — a manager only ever acts on ordinary members, while
 * role changes and ownership transfer stay owner-only.
 *
 * This drives menu show/hide only. The server re-decides every mutation
 * (`DecideMemberManagement`, #844), so a menu shown in error yields a failed
 * action, never an escalation.
 */
export function canManageGroupMembers(viewerRole: ChannelMemberRole): boolean {
  return viewerRole === "owner" || viewerRole === "manager";
}

/**
 * The owner cannot be removed or leave while still owner — ownership must be
 * transferred first. Drives the disabled "remove" affordance + the
 * "transfer ownership first" copy.
 */
export function isRemovableGroupMember(
  member: Pick<ChannelMember, "role">,
): boolean {
  return channelMemberRole(member) !== "owner";
}
