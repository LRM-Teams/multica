"use client";

import { Hash } from "lucide-react";
import type { ChannelMemberBrief } from "@multica/core/types";
import { ActorAvatar } from "../../common/actor-avatar";

/**
 * Multi-person DM member collage only (LRM-254 A1). Group channels use
 * {@link ChannelHashLandmark} (`#` + name) — never this collage — so the
 * landmark does not "drift" when the roster changes.
 *
 * Up to 4 members' real avatars tiled into a round circle — 1 → full,
 * 2 → centered pair, 3–4 → 2×2. Members beyond the first 4 (stable order)
 * are dropped. Recomputes on join/leave.
 *
 * LRM-224: each tile is identity-first ActorAvatar. Empty roster → `#`
 * placeholder (DM edge case only; channels must not call this).
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
