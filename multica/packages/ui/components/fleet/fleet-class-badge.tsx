import { cn } from "../../lib/utils";
import { fleetClassTone, fleetRankPennantClass } from "../../lib/fleet-class";
import { fleetClassBadgeTone } from "../../lib/fleet-badge-tone";
import { ActorBadgeFrame } from "../common/actor-badge-frame";
import { FleetClassIcon } from "./fleet-class-icons";

export function FleetRankBadge({
  classId,
  classLabel,
  fleetRank,
  frozen = false,
  compact = false,
  /** Chat-scale QQ-style medal; icon-only with optional rank ribbon. */
  medal = false,
}: {
  classId: string;
  classLabel: string;
  fleetRank?: number;
  frozen?: boolean;
  compact?: boolean;
  medal?: boolean;
}) {
  if (medal) {
    return (
      <span
        className={cn("inline-flex shrink-0", frozen && "opacity-60 grayscale")}
        title={classLabel}
      >
        <ActorBadgeFrame tone={fleetClassBadgeTone(classId)} rank={fleetRank}>
          <FleetClassIcon classId={classId} className="size-4" title={classLabel} />
        </ActorBadgeFrame>
      </span>
    );
  }

  return (
    <span
      className={cn(
        "inline-flex items-center gap-1 rounded-md border border-border/60 bg-muted/40 px-1.5 py-0.5 text-[11px] font-medium",
        frozen && "opacity-60 grayscale",
        compact && "px-1 py-0",
      )}
      title={classLabel}
    >
      <FleetClassIcon classId={classId} className={cn("size-4", fleetClassTone(classId))} title={classLabel} />
      {!compact ? <span className={fleetClassTone(classId)}>{classLabel}</span> : null}
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
