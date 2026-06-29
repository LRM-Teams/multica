"use client";

import { Hash } from "lucide-react";
import type { ChannelMemberBrief } from "@multica/core/types";
import { agentColor } from "../../common/agent-color";

/**
 * WeChat-style composite group avatar: member tiles packed into a single round
 * circle. Shows up to 9 members in an adaptive grid (1 → full, 2-4 → 2 cols,
 * 5-9 → 3 cols), partial rows centered so any count reads evenly. Members
 * beyond 9 are dropped. Recomputes whenever the member list changes (joins /
 * leaves), since it derives purely from `members`.
 */
const MAX_TILES = 9;

function firstLetter(name: string): string {
  const c = name.trim().charAt(0);
  return c ? c.toUpperCase() : "?";
}

function memberLabel(member: ChannelMemberBrief): string {
  return member.display_name?.trim() || member.name?.trim() || "?";
}

export function ChannelGroupAvatar({
  members,
  size = 36,
}: {
  members: ChannelMemberBrief[];
  size?: number;
}) {
  if (members.length === 0) {
    return (
      <span
        style={{ width: size, height: size }}
        className="flex shrink-0 items-center justify-center rounded-full bg-muted text-muted-foreground"
      >
        <Hash style={{ width: size * 0.45, height: size * 0.45 }} />
      </span>
    );
  }

  const shown = members.slice(0, MAX_TILES);
  const cols = shown.length === 1 ? 1 : shown.length <= 4 ? 2 : 3;
  const tile = size / cols;

  return (
    <span
      style={{ width: size, height: size }}
      className="flex shrink-0 flex-wrap content-center items-center justify-center overflow-hidden rounded-full bg-background"
    >
      {shown.map((m) => {
        const color = m.member_type === "agent" ? agentColor(m.member_id) : undefined;
        const label = memberLabel(m);
        return (
          <span
            key={`${m.member_type}:${m.member_id}`}
            title={label}
            style={{
              width: tile,
              height: tile,
              fontSize: Math.max(7, Math.round(tile * 0.42)),
              backgroundColor: color?.bg,
              color: color?.fg,
            }}
            className={
              color
                ? "flex items-center justify-center font-medium"
                : "flex items-center justify-center bg-muted font-medium text-muted-foreground"
            }
          >
            {firstLetter(label)}
          </span>
        );
      })}
    </span>
  );
}
