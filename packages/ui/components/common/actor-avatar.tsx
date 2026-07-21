"use client";

import { useState, useEffect } from "react";
import { Users } from "lucide-react";
import { cn } from "@multica/ui/lib/utils";
import { avatarGlyph, avatarToneClass } from "@multica/ui/lib/avatar-fallback";
import { MulticaIcon } from "./multica-icon";

interface ActorAvatarProps {
  name: string;
  initials: string;
  avatarUrl?: string | null;
  /**
   * Retained for call-site symmetry with isSystem/isSquad. No longer drives a
   * fallback glyph (#451 retired the bot); agents render their persisted avatar
   * URL and fall through to initials only when it is missing or fails to load.
   */
  isAgent?: boolean;
  isSystem?: boolean;
  isSquad?: boolean;
  size?: number;
  className?: string;
  /**
   * Seed for the stable fallback tone palette (LRM-201). Defaults to `name`.
   * Prefer an actor id when available so rename does not recolor the disc.
   */
  toneSeed?: string;
}

function ActorAvatar({
  name,
  initials,
  avatarUrl,
  isSystem,
  isSquad,
  size = 20,
  className,
  toneSeed,
}: ActorAvatarProps) {
  const [imgError, setImgError] = useState(false);

  useEffect(() => {
    setImgError(false);
  }, [avatarUrl]);

  const showFallback = !avatarUrl || imgError;
  // Single glyph always — callers may still pass two-letter initials for
  // legacy surfaces; LRM-201 forbids gray double-letter fake faces.
  const fallbackGlyph = avatarGlyph(name) || avatarGlyph(initials) || "?";
  const tone = avatarToneClass(toneSeed || name || initials || "?");

  return (
    <div
      data-slot="avatar"
      data-fallback={showFallback ? "true" : undefined}
      className={cn(
        "inline-flex shrink-0 items-center justify-center font-medium overflow-hidden",
        // Squads (a group, non-human) get a square tile so they don't read as
        // a single person; everyone else stays round.
        isSquad ? "rounded-md" : "rounded-full",
        // Missing / failed image → stable tone palette (LRM-201). Callers may
        // still pass an explicit tone via className; tw-merge keeps the last bg-*.
        showFallback && !isSystem && !isSquad && tone,
        className,
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
        fallbackGlyph
      )}
    </div>
  );
}

export { ActorAvatar, type ActorAvatarProps };
