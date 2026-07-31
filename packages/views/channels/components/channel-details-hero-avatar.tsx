import { Camera, Loader2 } from "lucide-react";
import { resolvePublicFileUrl } from "@multica/core/workspace/avatar-url";
import { cn } from "@multica/ui/lib/utils";

/** LRM-494 — circular # glyph avatar for the Slack channel-details hero.
 *  LRM-724 — shows the uploaded channel icon when one is set.
 *  LRM-860 — when editable, whole circle is clickable with camera wash. */
export function ChannelDetailsHeroAvatar({
  name,
  avatarUrl,
  editable = false,
  busy = false,
  onClick,
  changeAriaLabel,
}: {
  name: string;
  avatarUrl?: string | null;
  editable?: boolean;
  busy?: boolean;
  onClick?: () => void;
  changeAriaLabel?: string;
}) {
  const src = resolvePublicFileUrl(avatarUrl);
  const glyph = (name.trim().charAt(0) || "#").toUpperCase();
  const face = src ? (
    <img
      src={src}
      alt=""
      data-testid="channel-details-hero-avatar"
      className="size-16 shrink-0 rounded-full border border-border object-cover"
    />
  ) : (
    <span
      data-testid="channel-details-hero-avatar"
      className="flex size-16 shrink-0 items-center justify-center rounded-full border border-border bg-muted text-xl font-bold text-foreground"
      aria-hidden="true"
    >
      {glyph}
    </span>
  );

  if (!editable) return face;

  return (
    <button
      type="button"
      onClick={onClick}
      disabled={busy}
      aria-label={changeAriaLabel}
      data-testid="channel-details-avatar-change"
      className={cn(
        "group relative size-16 shrink-0 rounded-full outline-none",
        "focus-visible:ring-2 focus-visible:ring-ring",
        "disabled:cursor-not-allowed",
      )}
    >
      {face}
      <span
        className={cn(
          "pointer-events-none absolute inset-0 flex items-center justify-center rounded-full bg-black/40",
          "opacity-0 transition-opacity duration-150 motion-reduce:transition-none",
          "group-hover:opacity-100 group-focus-visible:opacity-100",
          "group-disabled:opacity-0",
          busy && "opacity-100",
        )}
      >
        {busy ? (
          <Loader2 className="size-5 animate-spin text-white" />
        ) : (
          <Camera className="size-5 text-white" />
        )}
      </span>
    </button>
  );
}
