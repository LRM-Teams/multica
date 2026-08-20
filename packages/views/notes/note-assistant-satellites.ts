export type NoteAssistantSatelliteId = "period_brief" | "worker" | "highlights";

export const NOTE_ASSISTANT_SATELLITE_IDS: readonly NoteAssistantSatelliteId[] = [
  "period_brief",
  "worker",
  "highlights",
];

/** Fan from just-above to just-left of the main FAB. Wide span keeps
 * satellites apart; short radius keeps them close to the hub. */
const SATELLITE_ARC_START_DEG = 78;
const SATELLITE_ARC_END_DEG = 192;
export const NOTE_ASSISTANT_SATELLITE_RADIUS_PX = 44;

/**
 * Offset of a satellite center relative to the main FAB center.
 * index 0 sits highest; the last item sits furthest left.
 */
export function noteAssistantSatelliteOffset(
  index: number,
  count: number,
  radiusPx = NOTE_ASSISTANT_SATELLITE_RADIUS_PX,
): { x: number; y: number } {
  if (count <= 0) return { x: 0, y: 0 };
  const t = count === 1 ? 0.5 : index / (count - 1);
  const deg = SATELLITE_ARC_START_DEG + (SATELLITE_ARC_END_DEG - SATELLITE_ARC_START_DEG) * t;
  const rad = (deg * Math.PI) / 180;
  return {
    x: Math.cos(rad) * radiusPx,
    y: -Math.sin(rad) * radiusPx,
  };
}
