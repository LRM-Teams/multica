import type { MotionDirective } from "./directives";
import { MOTION_GLOW_MAX_ACTIVE } from "./tokens";

/**
 * Slice F · cap concurrent transition glow (D5 spec §9 / tokens §3.3).
 * Static markers (tombstone, restart, regoal) are unchanged.
 */
export function capTransitionGlowDirectives(
  directives: ReadonlyMap<string, MotionDirective | null>,
): Map<string, MotionDirective | null> {
  const capped = new Map(directives);
  let activeGlow = 0;

  for (const [entityId, directive] of capped) {
    if (!directive || directive.glowDisabled) continue;
    if (activeGlow >= MOTION_GLOW_MAX_ACTIVE) {
      capped.set(entityId, { ...directive, glowDisabled: true });
      continue;
    }
    activeGlow += 1;
  }

  return capped;
}
