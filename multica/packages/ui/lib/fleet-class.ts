import type { FleetClassId } from "@multica/core/types/agent-fleet";

export const FLEET_CLASS_ORDER: FleetClassId[] = [
  "reserve",
  "corvette",
  "frigate",
  "cruiser",
  "battleship",
  "dreadnought",
];

export function fleetClassTone(classId: string): string {
  switch (classId) {
    case "dreadnought":
      return "text-amber-300";
    case "battleship":
      return "text-orange-300";
    case "cruiser":
      return "text-sky-300";
    case "frigate":
      return "text-emerald-300";
    case "corvette":
      return "text-violet-300";
    default:
      return "text-muted-foreground";
  }
}

export function fleetRankPennantClass(fleetRank: number): string {
  switch (fleetRank) {
    case 1:
      return "bg-amber-400 text-amber-950";
    case 2:
      return "bg-slate-300 text-slate-900";
    case 3:
      return "bg-orange-400 text-orange-950";
    default:
      return "bg-muted text-muted-foreground";
  }
}
