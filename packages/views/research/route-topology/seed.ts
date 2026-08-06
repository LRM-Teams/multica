/**
 * Stable deterministic hashing for the route engine (LRM-1487 / 实现-11).
 *
 * All "organic" variation in the layout MUST come from a stable seed derived
 * from `(run seed, nodeId/edgeId)` — never from `Math.random()`. Replaying an
 * identical topology with an identical seed must reproduce identical geometry.
 */
import type { Point } from "./types";

const FNV_OFFSET = 0x811c9dc5;
const FNV_PRIME = 0x01000193;

/** FNV-1a 32-bit hash of a string → unsigned 32-bit integer. */
export function fnv1a(input: string): number {
  let hash = FNV_OFFSET >>> 0;
  for (let i = 0; i < input.length; i += 1) {
    hash ^= input.charCodeAt(i);
    hash = Math.imul(hash, FNV_PRIME) >>> 0;
  }
  return hash >>> 0;
}

/** Combine a list of already-hashed 32-bit values into one stable hash. */
export function combineHashes(parts: number[]): number {
  let h = FNV_OFFSET >>> 0;
  for (const p of parts) {
    h ^= p >>> 0;
    h = Math.imul(h, FNV_PRIME) >>> 0;
  }
  return h >>> 0;
}

const PI2 = Math.PI * 2;

/**
 * Stable scalar in [0, 1) for a logical unit, keyed by a seed string.
 * `runSeed` is the top-level stable seed passed to the engine.
 */
export function stable01(unitKey: string, runSeed = ""): number {
  return fnv1a(`${runSeed}\u0001${unitKey}`) / 0x100000000;
}

/**
 * Stable integer in [min, max] (inclusive both ends) keyed by a logical unit.
 */
export function stableInt(
  unitKey: string,
  min: number,
  max: number,
  runSeed = "",
): number {
  const span = max - min + 1;
  return min + (fnv1a(`${runSeed}\u0007${unitKey}`) % span);
}

/**
 * Stable angle in radians within [minDeg, maxDeg], hashed per unit. Used for
 * branch tangent selection in the 24–52° band required by the spec.
 */
export function stableAngleDeg(
  unitKey: string,
  minDeg: number,
  maxDeg: number,
  runSeed = "",
): number {
  const t = stable01(`ang:${unitKey}`, runSeed);
  return minDeg + t * (maxDeg - minDeg);
}

/** Stable phase in [0, 2π) for an S-curve / oscillation. */
export function stablePhase(unitKey: string, runSeed = ""): number {
  return stable01(`phase:${unitKey}`, runSeed) * PI2;
}

/** Stable unit vector at a given angle. */
export function unitAtDeg(deg: number): Point {
  const rad = (deg * Math.PI) / 180;
  return { x: Math.cos(rad), y: Math.sin(rad) };
}
