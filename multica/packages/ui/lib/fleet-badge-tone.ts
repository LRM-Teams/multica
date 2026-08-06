export type FleetBadgeTone =
  | "neutral"
  | "gold"
  | "cyan"
  | "violet"
  | "amber"
  | "emerald"
  | "sky"
  | "orange";

export function fleetClassBadgeTone(classId: string): FleetBadgeTone {
  switch (classId) {
    case "dreadnought":
      return "gold";
    case "battleship":
      return "orange";
    case "cruiser":
      return "sky";
    case "frigate":
      return "emerald";
    case "corvette":
      return "violet";
    default:
      return "neutral";
  }
}
