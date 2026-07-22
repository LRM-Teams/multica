"use client";

import { cn } from "@multica/ui/lib/utils";

/**
 * LRM-254 / Slack A1 — channel landmark is a **text-level `#`**, not an
 * avatar slot or purple tile. Sidebar ≈ 15px muted; header / details ≈ 18px
 * ink. Same glyph shape; only size/weight/color shift between surfaces.
 */
export type ChannelHashMarkSize = "sidebar" | "header" | "details";

const sizeClass: Record<ChannelHashMarkSize, string> = {
  sidebar: "w-5 text-[15px] font-bold leading-none text-muted-foreground",
  header: "w-6 text-lg font-bold leading-none text-foreground",
  details: "w-7 text-xl font-bold leading-none text-foreground",
};

export function ChannelHashMark({
  size = "sidebar",
  className,
}: {
  size?: ChannelHashMarkSize;
  className?: string;
}) {
  return (
    <span
      aria-hidden
      className={cn(
        "inline-flex shrink-0 items-center justify-center text-center",
        sizeClass[size],
        className,
      )}
    >
      #
    </span>
  );
}
