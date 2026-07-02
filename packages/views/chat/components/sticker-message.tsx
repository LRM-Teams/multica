"use client";

import { useState } from "react";
import { resolvePublicFileUrl } from "@multica/core/workspace/avatar-url";
import { cn } from "@multica/ui/lib/utils";

/**
 * Best-effort emoji stand-ins for the well-known sticker ids documented in the
 * stickers skill. Used only when the sticker image asset can't be loaded — the
 * deployment hasn't shipped `GET /api/stickers/{id}` yet, the network failed,
 * or the id is unknown to this server — so a sticker reply degrades to a
 * friendly glyph instead of ever falling back to the raw JSON body.
 */
const STICKER_FALLBACK_EMOJI: Record<string, string> = {
  hi: "👋",
  ok: "👌",
  "got-it": "🫡",
  "nod-yes": "✅",
  "thumbs-up": "👍",
  impressive: "🤩",
  perfect: "💯",
  thanks: "🙏",
  "heart-hands": "🫶",
  applause: "👏",
  "on-it": "💪",
  huaji: "😏",
};

export interface StickerMessageProps {
  /** Sticker id from a `{"parts":[{"type":"sticker","sticker_id":...}]}` reply. */
  id: string;
  className?: string;
}

/**
 * Render a single sticker referenced by id. Loads the image from the server's
 * public sticker endpoint; on any load error it swaps to a labelled emoji /
 * id chip. It never renders the raw structured-parts JSON — that is the whole
 * point of LRM-84.
 */
export function StickerMessage({ id, className }: StickerMessageProps) {
  const [failed, setFailed] = useState(false);

  // Relative `/api/...` path resolved against the API base the same way
  // avatars/attachments are, so it works on web (Next proxy), desktop and
  // self-host. Falls back to the relative path when the base isn't set yet.
  const src = resolvePublicFileUrl(`/api/stickers/${id}`) ?? `/api/stickers/${id}`;

  if (failed) {
    const emoji = STICKER_FALLBACK_EMOJI[id];
    return (
      <span
        role="img"
        aria-label={id}
        title={id}
        data-testid="sticker-fallback"
        className={cn(
          "inline-flex items-center gap-1.5 rounded-2xl bg-muted px-3 py-2 leading-none",
          className,
        )}
      >
        <span className="text-3xl">{emoji ?? "🖼️"}</span>
        {!emoji && (
          <span className="text-xs text-muted-foreground">{id}</span>
        )}
      </span>
    );
  }

  return (
    <img
      src={src}
      alt={id}
      title={id}
      draggable={false}
      data-testid="sticker-image"
      onError={() => setFailed(true)}
      className={cn(
        "inline-block h-24 w-24 select-none object-contain",
        className,
      )}
    />
  );
}
