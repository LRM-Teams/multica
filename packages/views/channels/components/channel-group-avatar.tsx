"use client";

import { Hash } from "lucide-react";
import type { ChannelMemberBrief } from "@multica/core/types";
import { ActorAvatar } from "../../common/actor-avatar";

/**
 * Multi-party DM collage only (LRM-254): up to 4 members' avatars tiled into
 * a round circle — 1 → full circle, 2 → centered pair, 3–4 → 2×2 grid.
 * Group **channels** use `ChannelHashMark` (text `#` + name), not this.
 *
 * Members beyond the first 4 (stable `members` order) are dropped.
 * LRM-224: each tile is identity-first ActorAvatar; empty → `#` fallback.
 */
const MAX_TILES = 4;

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
  const cols = shown.length === 1 ? 1 : 2;
  const tile = size / cols;

  return (
    <span
      style={{ width: size, height: size }}
      className="flex shrink-0 flex-wrap content-center items-center justify-center overflow-hidden rounded-full bg-background"
    >
      {shown.map((m) => (
        <ActorAvatar
          key={`${m.member_type}:${m.member_id}`}
          actorType={m.member_type === "agent" ? "agent" : "member"}
          actorId={m.member_id}
          size={tile}
          className="rounded-none"
          avatarUrlHint={m.avatar_url}
          profileLink={false}
        />
      ))}
    </span>
  );
}
