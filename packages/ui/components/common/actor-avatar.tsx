"use client";

import { useState, useEffect } from "react";
import { Users } from "lucide-react";
import { cn } from "@multica/ui/lib/utils";
import { MulticaIcon } from "./multica-icon";

interface ActorAvatarProps {
  name: string;
  initials: string;
  avatarUrl?: string | null;
  /**
   * Retained for call-site symmetry with isSystem/isSquad. No longer drives a
   * fallback glyph (#451 retired the bot); agents get a pool photo upstream via
   * getActorAvatarUrl and fall through to initials only if that image fails.
   */
  isAgent?: boolean;
  isSystem?: boolean;
  isSquad?: boolean;
  size?: number;
  className?: string;
}

function ActorAvatar({
  name,
  initials,
  avatarUrl,
  isSystem,
  isSquad,
  size = 20,
  className,
}: ActorAvatarProps) {
  const [imgError, setImgError] = useState(false);

  useEffect(() => {
    setImgError(false);
  }, [avatarUrl]);

  const showFallback = !avatarUrl || imgError;

  return (
    <div
      data-slot="avatar"
      className={cn(
        "inline-flex shrink-0 items-center justify-center font-medium overflow-hidden",
        // Squads (a group, non-human) get a square tile so they don't read as
        // a single person; everyone else stays round.
        isSquad ? "rounded-md" : "rounded-full",
        // One restrained, uniform fallback for everyone — no per-actor hash
        // colors, no bot glyph (#451, Frank: retire the robot / random colors).
        // Agents render a deterministic photo from the shared pool via
        // getActorAvatarUrl; this text fallback only shows if that image itself
        // fails to load, in which case initials read better than a glyph.
        showFallback && "bg-muted text-muted-foreground",
        className
      )}
      style={{
        width: size,
        height: size,
        fontSize: size * 0.45,
      }}
      title={name}
    >
      {avatarUrl && !imgError ? (
        <img
          src={avatarUrl}
          alt={name}
          className="h-full w-full object-cover"
          onError={() => setImgError(true)}
        />
      ) : isSystem ? (
        <MulticaIcon noSpin style={{ width: size * 0.55, height: size * 0.55 }} />
      ) : isSquad ? (
        <Users style={{ width: size * 0.55, height: size * 0.55 }} />
      ) : (
        initials
      )}
    </div>
  );
}

export { ActorAvatar, type ActorAvatarProps };
