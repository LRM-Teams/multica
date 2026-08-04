/**
 * LRM-1366 harness stub — the product avatar drags navigation contexts and
 * presence queries into the tree. Row/skeleton geometry is what the shots
 * measure, so the stub keeps the exact same box size.
 */
export function ActorAvatar({
  actorId,
  size = 40,
}: {
  actorType?: string;
  actorId: string;
  size?: number;
  showStatusDot?: boolean;
  profileLink?: boolean;
}) {
  return (
    <span
      data-testid={`face-${actorId}`}
      className="inline-flex shrink-0 items-center justify-center rounded-full bg-sidebar-accent text-[10px] font-semibold text-muted-foreground"
      style={{ width: size, height: size }}
    >
      {actorId.slice(0, 1)}
    </span>
  );
}
