import type { ChannelGoalSubgoal, ChannelGoalSubgoalStatus, ChannelMember } from "@multica/core/types";

export const GOAL_SUBGOAL_PANEL_ID = "channel-goal-subgoal-panel";

export const EMPTY_SUBGOALS: ChannelGoalSubgoal[] = [];

const OPEN_STATUSES: ChannelGoalSubgoalStatus[] = ["captured", "in_progress", "waiting"];

export function isOpenStatus(status: ChannelGoalSubgoalStatus): boolean {
  return OPEN_STATUSES.includes(status);
}

export function countOpenSubgoals(subgoals: ChannelGoalSubgoal[]): number {
  let count = 0;
  for (const item of subgoals) {
    if (isOpenStatus(item.status)) count += 1;
  }
  return count;
}

export function memberLabel(member: ChannelMember | undefined, fallbackId: string): string {
  return member?.display_name || member?.name || fallbackId.slice(0, 8);
}

export function toActorType(memberType: ChannelMember["member_type"]): "agent" | "member" {
  return memberType === "agent" ? "agent" : "member";
}

export function buildMemberByKey(members: ChannelMember[]): Map<string, ChannelMember> {
  const map = new Map<string, ChannelMember>();
  for (const member of members) {
    map.set(`${toActorType(member.member_type)}:${member.member_id}`, member);
    if (member.member_type === "user") map.set(`member:${member.member_id}`, member);
  }
  return map;
}

export function buildTitleById(subgoals: ChannelGoalSubgoal[]): Map<string, string> {
  return new Map(subgoals.map((item) => [item.id, item.title]));
}

export function lines(value: string): string[] {
  return [
    ...new Set(
      value.split("\n").flatMap((item) => {
        const trimmed = item.trim();
        return trimmed ? [trimmed] : [];
      }),
    ),
  ];
}

export function mutationMessage(error: unknown, fallback: string, stale: string): string {
  if (error && typeof error === "object" && "status" in error && error.status === 409) {
    const message = "message" in error && typeof error.message === "string" ? error.message : "";
    if (message.toLowerCase().includes("depend")) return message;
    return stale;
  }
  return fallback;
}

/** Deep-link to the channel message a subgoal was captured from (`?message=`). */
export function subgoalSourceMessageHref(
  channelId: string,
  sourceMessageId: string | undefined | null,
  channelDetail: (id: string) => string,
): string | null {
  if (!sourceMessageId) return null;
  return `${channelDetail(channelId)}?message=${encodeURIComponent(sourceMessageId)}`;
}
