import { cn } from "@multica/ui/lib/utils";
import { resolvePublicFileUrl } from "@multica/core/workspace/avatar-url";

/**
 * LRM-254 A1 — Slack-style channel landmark: a text-level `#` glyph beside
 * the channel name. Not an avatar slot, not a filled tile.
 *
 * LRM-724 — once the channel has a custom `avatar_url`, the landmark slot
 * renders that image instead of the `#` glyph (same footprint, rounded to
 * match the tile language). Absent/null keeps the `#` glyph exactly.
 *
 * Sizes match the frozen design gate (design-channel-avatar-slack.html):
 * - `sm` ≈ 15px — sidebar / settings
 * - `lg` ≈ 18px — header / details hero
 */
export type ChannelHashLandmarkSize = "sm" | "lg";

const SIZE_CLASS: Record<ChannelHashLandmarkSize, string> = {
  sm: "w-5 text-[15px] leading-none text-ink-2",
  lg: "w-6 text-[18px] leading-none text-ink",
};

const IMG_SIZE_CLASS: Record<ChannelHashLandmarkSize, string> = {
  sm: "size-5",
  lg: "size-6",
};

export function ChannelHashLandmark({
  size = "sm",
  avatarUrl,
  className,
}: {
  size?: ChannelHashLandmarkSize;
  /** Custom channel icon; falls back to the `#` glyph when absent. */
  avatarUrl?: string | null;
  className?: string;
}) {
  const src = resolvePublicFileUrl(avatarUrl);
  if (src) {
    return (
      <img
        src={src}
        alt=""
        data-testid="channel-avatar-image"
        data-size={size}
        className={cn(
          "shrink-0 rounded-md object-cover",
          IMG_SIZE_CLASS[size],
          className,
        )}
      />
    );
  }
  return (
    <span
      aria-hidden="true"
      data-testid="channel-hash-landmark"
      data-size={size}
      className={cn(
        "inline-flex shrink-0 items-center justify-center font-bold",
        SIZE_CLASS[size],
        className,
      )}
    >
      #
    </span>
  );
}
