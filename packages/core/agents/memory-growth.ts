import type {
  AgentMemoryGrowth,
  MemoryGrowthNextProgress,
  MemoryGrowthSegment,
  MemoryGrowthTierId,
} from "../types/memory-growth";

/** Mirrors server/internal/memorygrowth (LRM-303). */
export const MEMORY_GROWTH_DEFAULT_BASE = 3;
export const MEMORY_GROWTH_DEFAULT_RATIO = 2;

const TIER_ORDER: readonly MemoryGrowthTierId[] = [
  "bronze",
  "silver",
  "gold",
  "platinum",
] as const;

const TIER_LABELS: Record<MemoryGrowthTierId, string> = {
  bronze: "Bronze",
  silver: "Silver",
  gold: "Gold",
  platinum: "Platinum",
};

/**
 * Client-side tier math matching LRM-303. Used for unit tests and explicit
 * design-preview mocks — production profile surfaces prefer the server field.
 */
export function computeMemoryGrowth(
  totalWrites: number,
  base = MEMORY_GROWTH_DEFAULT_BASE,
  ratio = MEMORY_GROWTH_DEFAULT_RATIO,
): AgentMemoryGrowth | null {
  if (totalWrites <= 0) return null;
  const safeBase = base > 0 ? base : MEMORY_GROWTH_DEFAULT_BASE;
  const safeRatio = ratio > 0 ? ratio : MEMORY_GROWTH_DEFAULT_RATIO;
  const thresholds = tierThresholds(safeBase, safeRatio);
  const tierIdx = currentTierIndex(totalWrites, thresholds);

  const segments: MemoryGrowthSegment[] = TIER_ORDER.map((tier, i) => {
    let status: MemoryGrowthSegment["status"] = "upcoming";
    if (totalWrites >= thresholds[i]) {
      status = "complete";
    } else if (i === tierIdx) {
      status = "current";
    }
    return {
      tier,
      tier_label: TIER_LABELS[tier],
      status,
    };
  });

  const tier = TIER_ORDER[tierIdx];
  return {
    total_writes: totalWrites,
    tier,
    tier_label: TIER_LABELS[tier],
    segments,
    next: nextProgress(totalWrites, thresholds),
  };
}

function tierThresholds(base: number, ratio: number): number[] {
  return TIER_ORDER.map((_, i) => {
    let t = base;
    for (let j = 0; j < i; j++) t *= ratio;
    return t;
  });
}

function currentTierIndex(total: number, thresholds: number[]): number {
  let idx = 0;
  for (let i = 0; i < TIER_ORDER.length - 1; i++) {
    if (total >= thresholds[i]) idx = i + 1;
  }
  return Math.min(idx, TIER_ORDER.length - 1);
}

function nextProgress(
  total: number,
  thresholds: number[],
): MemoryGrowthNextProgress | null {
  for (let i = 0; i < thresholds.length; i++) {
    const required = thresholds[i];
    if (total < required) {
      let tierIdx = i + 1;
      if (tierIdx >= TIER_ORDER.length) tierIdx = TIER_ORDER.length - 1;
      const tier = TIER_ORDER[tierIdx];
      return {
        tier,
        tier_label: TIER_LABELS[tier],
        current: total,
        required,
      };
    }
  }
  return null;
}
