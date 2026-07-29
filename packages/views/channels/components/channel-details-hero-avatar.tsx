import { resolvePublicFileUrl } from "@multica/core/workspace/avatar-url";

/** LRM-494 — circular # glyph avatar for the Slack channel-details hero.
 *  LRM-724 — shows the uploaded channel icon when one is set. */
export function ChannelDetailsHeroAvatar({
  name,
  avatarUrl,
}: {
  name: string;
  avatarUrl?: string | null;
}) {
  const src = resolvePublicFileUrl(avatarUrl);
  if (src) {
    return (
      <img
        src={src}
        alt=""
        data-testid="channel-details-hero-avatar"
        className="size-16 shrink-0 rounded-full border border-border object-cover"
      />
    );
  }
  const glyph = (name.trim().charAt(0) || "#").toUpperCase();
  return (
    <span
      data-testid="channel-details-hero-avatar"
      className="flex size-16 shrink-0 items-center justify-center rounded-full border border-border bg-muted text-xl font-bold text-foreground"
      aria-hidden="true"
    >
      {glyph}
    </span>
  );
}
