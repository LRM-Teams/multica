/** LRM-494 — circular # glyph avatar for the Slack channel-details hero. */
export function ChannelDetailsHeroAvatar({ name }: { name: string }) {
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
