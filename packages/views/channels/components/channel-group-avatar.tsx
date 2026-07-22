"use client";

import { Hash } from "lucide-react";
import type { ChannelMemberBrief } from "@multica/core/types";
import { ActorAvatar } from "../../common/actor-avatar";

/**
 * WeChat-style composite group avatar: up to 4 members' real, persisted
 * avatars tiled into a single round circle — 1 → full circle, 2 → a
 * centered pair, 3-4 → a 2x2 grid. Members (human or agent) beyond the
 * first 4, in `channel.members`' own order, are dropped — a stable,
 * deterministic selection, not a random sample. Recomputes whenever the
 * member list changes (joins/leaves), since it derives purely from
 * `members`.
 *
 * LRM-224: each tile is the identity-first ActorAvatar (sticky cache +
 * directory); `avatar_url` on the member brief only seeds. Missing / failed
 * image → colored glyph (never whole-word text). Empty channel → `#`.
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
