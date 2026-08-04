import { cn } from "../../lib/utils";
import { fleetRankPennantClass } from "../../lib/fleet-class";

export function FleetRankBadge({
  classLabel,
  fleetRank,
  frozen = false,
}: {
  classLabel: string;
  fleetRank?: number;
  frozen?: boolean;
}) {
  return (
    <span
      data-testid="fleet-rank-badge"
      className={cn(
        "inline-flex items-center gap-1 rounded-full border border-border/60 bg-background/65 px-2.5 py-1 text-xs font-medium text-foreground backdrop-blur-sm",
        frozen && "opacity-60 grayscale",
      )}
      title={classLabel}
    >
      <span>{classLabel}</span>
      {fleetRank && fleetRank > 0 && fleetRank <= 3 ? (
        <span className="rounded px-1 text-[10px] font-semibold tabular-nums">#{fleetRank}</span>
      ) : null}
    </span>
  );
}

export function FleetRankPennantOverlay({ fleetRank }: { fleetRank: number }) {
  if (fleetRank <= 0 || fleetRank > 3) return null;
  return (
    <span
      className={cn(
        "pointer-events-none absolute -right-1 -top-1 flex size-4 items-center justify-center rounded-full text-[9px] font-bold tabular-nums shadow-sm",
        fleetRankPennantClass(fleetRank),
      )}
    >
      {fleetRank}
    </span>
  );
}
