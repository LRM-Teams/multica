"use client";

import { Hash } from "lucide-react";
import { resolveActorDisplayName } from "@multica/core/identity";
import type { ChannelMemberBrief } from "@multica/core/types";
import { ActorAvatar } from "@multica/ui/components/common/actor-avatar";
import { resolvePublicFileUrl } from "@multica/core/workspace/avatar-url";

/**
 * WeChat-style composite group avatar: up to 4 members' real, persisted
 * avatars tiled into a single round circle — 1 → full circle, 2 → a
 * centered pair, 3-4 → a 2x2 grid. Members (human or agent) beyond the
 * first 4, in `channel.members`' own order, are dropped — a stable,
 * deterministic selection, not a random sample. Recomputes whenever the
 * member list changes (joins/leaves), since it derives purely from
 * `members`.
 *
 * Each tile renders that member's own persisted `avatar_url`; a missing or
 * failed-to-load image falls back to that member's initials (never a random
 * or synthesized avatar — see `ActorAvatar`'s own fallback). Only an empty
 * channel (no members at all) falls back to the neutral `#` glyph.
 */
const MAX_TILES = 4;

function firstLetter(name: string): string {
  const c = name.trim().charAt(0);
  return c ? c.toUpperCase() : "?";
}

function memberLabel(member: ChannelMemberBrief): string {
  return resolveActorDisplayName(member, "?");
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
  const cols = shown.length === 1 ? 1 : 2;
  const tile = size / cols;

  return (
    <span
      style={{ width: size, height: size }}
      className="flex shrink-0 flex-wrap content-center items-center justify-center overflow-hidden rounded-full bg-background"
    >
      {shown.map((m) => {
        const label = memberLabel(m);
        return (
          <ActorAvatar
            key={`${m.member_type}:${m.member_id}`}
            name={label}
            initials={firstLetter(label)}
            avatarUrl={resolvePublicFileUrl(m.avatar_url)}
            size={tile}
            className="rounded-none"
          />
        );
      })}
    </span>
  );
}
