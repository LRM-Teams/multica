import { cn } from "@multica/ui/lib/utils";

/**
 * LRM-254 A1 — Slack-style channel landmark: a text-level `#` glyph beside
 * the channel name. Not an avatar slot, not a filled tile.
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

export function ChannelHashLandmark({
  size = "sm",
  className,
}: {
  size?: ChannelHashLandmarkSize;
  className?: string;
}) {
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
