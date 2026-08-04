/** LRM-1348 harness stub — the product avatar drags in navigation, panel
 *  contexts and presence queries. The focus path never touches it. */
export function ActorAvatar({
  actorId,
  name,
  size = 22,
}: {
  actorType?: string;
  actorId: string;
  name?: string;
  avatarUrlHint?: string | null;
  size?: number;
  showStatusDot?: boolean;
  profileLink?: boolean;
}) {
  return (
    <span
      data-testid={`face-${actorId}`}
      className="inline-flex shrink-0 items-center justify-center rounded-full bg-muted text-[9px] font-semibold text-muted-foreground"
      style={{ width: size, height: size }}
    >
      {(name ?? actorId).slice(0, 1)}
    </span>
  );
}
