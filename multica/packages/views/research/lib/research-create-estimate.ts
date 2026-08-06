/**
 * LRM-839 — create-time duration / cost-tier estimates.
 *
 * Static tier table until a BE estimate API exists. Lookup is sync so the
 * params panel can refresh in the same paint (AC ≤200ms, no submit).
 * Missing / unresolvable data → unknown; estimate never blocks create.
 */

import {
  clampSourceWeight,
  isValidCreateLanguage,
  isValidDepthTier,
  type ResearchCreateDepthTier,
  type ResearchCreateLanguage,
  type ResearchCreateParams,
  type ResearchCreateParamsDraft,
  type ResearchSourceWeights,
} from "./research-create-params";

export type ResearchCostTier = "low" | "medium" | "high";

export type ResearchCreateEstimate = {
  /** Inclusive lower bound, minutes. */
  duration_min: number;
  /** Inclusive upper bound, minutes. */
  duration_max: number;
  cost_tier: ResearchCostTier;
};

export type ResearchCreateEstimateResult =
  | { status: "ready"; estimate: ResearchCreateEstimate }
  | { status: "unknown"; reason: "invalid_params" | "no_data" };

type DepthBase = {
  duration_min: number;
  duration_max: number;
  cost_tier: ResearchCostTier;
};

/** Static FE table — depth is the primary driver. */
const DEPTH_BASE: Record<ResearchCreateDepthTier, DepthBase> = {
  shallow: { duration_min: 8, duration_max: 20, cost_tier: "low" },
  standard: { duration_min: 20, duration_max: 45, cost_tier: "medium" },
  deep: { duration_min: 45, duration_max: 120, cost_tier: "high" },
};

const COST_ORDER: ResearchCostTier[] = ["low", "medium", "high"];

function bumpCostTier(tier: ResearchCostTier, steps: number): ResearchCostTier {
  const idx = Math.min(
    COST_ORDER.length - 1,
    Math.max(0, COST_ORDER.indexOf(tier) + steps),
  );
  return COST_ORDER[idx]!;
}

function meanWeight(weights: ResearchSourceWeights): number {
  return (weights.primary + weights.secondary + weights.community) / 3;
}

function scaleRange(
  min: number,
  max: number,
  factor: number,
): { duration_min: number; duration_max: number } {
  const duration_min = Math.max(1, Math.round(min * factor));
  const duration_max = Math.max(duration_min, Math.round(max * factor));
  return { duration_min, duration_max };
}

/**
 * Pure static lookup for valid create params. Returns null only when the
 * table has no row (reserved for future API / incomplete tables).
 */
export function lookupStaticCreateEstimate(
  params: ResearchCreateParams,
): ResearchCreateEstimate | null {
  const base = DEPTH_BASE[params.depth_tier];
  if (!base) return null;

  let factor = 1;
  const avg = meanWeight(params.source_weights);
  // Heavier source mix → more gather/verify work.
  if (avg >= 0.8) factor += 0.18;
  else if (avg >= 0.65) factor += 0.08;
  else if (avg <= 0.35) factor -= 0.08;

  // English report slightly wider search / synthesis window.
  if (params.language === "en") factor += 0.06;

  const { duration_min, duration_max } = scaleRange(
    base.duration_min,
    base.duration_max,
    factor,
  );

  let cost_tier = base.cost_tier;
  if (avg >= 0.85 && params.depth_tier !== "deep") {
    cost_tier = bumpCostTier(cost_tier, 1);
  } else if (avg <= 0.3 && params.depth_tier !== "shallow") {
    cost_tier = bumpCostTier(cost_tier, -1);
  }

  return { duration_min, duration_max, cost_tier };
}

export type ResolveCreateEstimateOptions = {
  /**
   * Optional lookup (API or test double). Default = static table.
   * Returning null yields unknown / no_data without blocking create.
   */
  lookup?: (params: ResearchCreateParams) => ResearchCreateEstimate | null;
};

/**
 * Resolve an estimate from a create draft. Invalid depth / non-finite weights
 * → unknown. Out-of-range weights are clamped for preview only so linkage
 * still updates while the user is mid-edit (LRM-835 still blocks Done/Start).
 */
export function resolveCreateEstimate(
  draft: Partial<ResearchCreateParamsDraft> | null | undefined,
  options?: ResolveCreateEstimateOptions,
): ResearchCreateEstimateResult {
  const depth = draft?.depth_tier;
  if (!isValidDepthTier(depth)) {
    return { status: "unknown", reason: "invalid_params" };
  }

  const weights = draft?.source_weights;
  if (
    !weights ||
    !Number.isFinite(weights.primary) ||
    !Number.isFinite(weights.secondary) ||
    !Number.isFinite(weights.community)
  ) {
    return { status: "unknown", reason: "invalid_params" };
  }

  const language: ResearchCreateLanguage = isValidCreateLanguage(draft?.language)
    ? draft.language
    : "zh";

  const params: ResearchCreateParams = {
    depth_tier: depth,
    language,
    source_weights: {
      primary: clampSourceWeight(weights.primary),
      secondary: clampSourceWeight(weights.secondary),
      community: clampSourceWeight(weights.community),
    },
  };

  const lookup = options?.lookup ?? lookupStaticCreateEstimate;
  const estimate = lookup(params);
  if (!estimate) {
    return { status: "unknown", reason: "no_data" };
  }
  return { status: "ready", estimate };
}
