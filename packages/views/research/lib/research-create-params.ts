/**
 * LRM-838 — create-time research levers (depth / source weights / language).
 * depth_tier is a first-class create API field; weights + language also ride a
 * parseable goal trailer so session detail can round-trip them without a BE
 * schema change (LRM-835 will harden validation later).
 */

export type ResearchCreateDepthTier = "shallow" | "standard" | "deep";
export type ResearchCreateLanguage = "zh" | "en";

export type ResearchSourceWeightKey = "primary" | "secondary" | "community";

export type ResearchSourceWeights = Record<ResearchSourceWeightKey, number>;

export type ResearchCreateParams = {
  depth_tier: ResearchCreateDepthTier;
  language: ResearchCreateLanguage;
  source_weights: ResearchSourceWeights;
};

export const SOURCE_WEIGHT_KEYS: ResearchSourceWeightKey[] = [
  "primary",
  "secondary",
  "community",
];

export const DEPTH_TIERS: ResearchCreateDepthTier[] = [
  "shallow",
  "standard",
  "deep",
];

export const CREATE_LANGUAGES: ResearchCreateLanguage[] = ["zh", "en"];

export const DEFAULT_SOURCE_WEIGHTS: ResearchSourceWeights = {
  primary: 0.85,
  secondary: 0.65,
  community: 0.4,
};

/** Inclusive bounds for weight sliders (LRM-835 will surface out-of-range errors). */
export const SOURCE_WEIGHT_MIN = 0;
export const SOURCE_WEIGHT_MAX = 1;

const TRAILER_RE =
  /【调研参数\s+depth=(shallow|standard|deep)\s+lang=(zh|en)\s+primary=([0-9.]+)\s+secondary=([0-9.]+)\s+community=([0-9.]+)】\s*$/u;

export function defaultCreateLanguage(uiLanguage?: string): ResearchCreateLanguage {
  return (uiLanguage ?? "").toLowerCase().startsWith("zh") ? "zh" : "en";
}

export function defaultCreateParams(uiLanguage?: string): ResearchCreateParams {
  return {
    depth_tier: "standard",
    language: defaultCreateLanguage(uiLanguage),
    source_weights: { ...DEFAULT_SOURCE_WEIGHTS },
  };
}

export function clampSourceWeight(value: number): number {
  if (!Number.isFinite(value)) return DEFAULT_SOURCE_WEIGHTS.primary;
  const clamped = Math.min(SOURCE_WEIGHT_MAX, Math.max(SOURCE_WEIGHT_MIN, value));
  return Math.round(clamped * 100) / 100;
}

export function normalizeCreateParams(
  partial: Partial<ResearchCreateParams> | null | undefined,
  uiLanguage?: string,
): ResearchCreateParams {
  const base = defaultCreateParams(uiLanguage);
  const depth =
    partial?.depth_tier && DEPTH_TIERS.includes(partial.depth_tier)
      ? partial.depth_tier
      : base.depth_tier;
  const language =
    partial?.language && CREATE_LANGUAGES.includes(partial.language)
      ? partial.language
      : base.language;
  const weights = partial?.source_weights ?? base.source_weights;
  return {
    depth_tier: depth,
    language,
    source_weights: {
      primary: clampSourceWeight(weights.primary ?? base.source_weights.primary),
      secondary: clampSourceWeight(
        weights.secondary ?? base.source_weights.secondary,
      ),
      community: clampSourceWeight(
        weights.community ?? base.source_weights.community,
      ),
    },
  };
}

export function formatCreateParamsTrailer(params: ResearchCreateParams): string {
  const p = normalizeCreateParams(params);
  const { primary, secondary, community } = p.source_weights;
  return `【调研参数 depth=${p.depth_tier} lang=${p.language} primary=${primary.toFixed(2)} secondary=${secondary.toFixed(2)} community=${community.toFixed(2)}】`;
}

/** Append (or replace) the create-params trailer on a goal string. */
export function appendCreateParamsToGoal(
  goal: string,
  params: ResearchCreateParams,
): string {
  const stripped = stripCreateParamsTrailer(goal).trimEnd();
  const trailer = formatCreateParamsTrailer(params);
  return stripped ? `${stripped}\n\n${trailer}` : trailer;
}

export function stripCreateParamsTrailer(goal: string): string {
  return goal.replace(TRAILER_RE, "").replace(/\s+$/u, "");
}

export function parseCreateParamsFromGoal(
  goal: string | null | undefined,
): ResearchCreateParams | null {
  if (!goal) return null;
  const match = goal.match(TRAILER_RE);
  if (!match) return null;
  return normalizeCreateParams({
    depth_tier: match[1] as ResearchCreateDepthTier,
    language: match[2] as ResearchCreateLanguage,
    source_weights: {
      primary: Number(match[3]),
      secondary: Number(match[4]),
      community: Number(match[5]),
    },
  });
}

/**
 * Resolve params for session detail: prefer trailer, fall back to session
 * depth_tier + defaults (so older sessions still show a depth chip).
 */
export function resolveSessionCreateParams(input: {
  goal?: string | null;
  depth_tier?: string | null;
  uiLanguage?: string;
}): ResearchCreateParams {
  const fromGoal = parseCreateParamsFromGoal(input.goal);
  if (fromGoal) {
    const depth =
      input.depth_tier &&
      DEPTH_TIERS.includes(input.depth_tier as ResearchCreateDepthTier)
        ? (input.depth_tier as ResearchCreateDepthTier)
        : fromGoal.depth_tier;
    return { ...fromGoal, depth_tier: depth };
  }
  const base = defaultCreateParams(input.uiLanguage);
  if (
    input.depth_tier &&
    DEPTH_TIERS.includes(input.depth_tier as ResearchCreateDepthTier)
  ) {
    return {
      ...base,
      depth_tier: input.depth_tier as ResearchCreateDepthTier,
    };
  }
  return base;
}

export function createParamsSummaryLine(
  params: ResearchCreateParams,
  labels: {
    depth: string;
    language: string;
    weights: string;
  },
): string {
  const p = normalizeCreateParams(params);
  const w = p.source_weights;
  return `${labels.depth} · ${labels.language} · ${labels.weights} ${w.primary.toFixed(2)}/${w.secondary.toFixed(2)}/${w.community.toFixed(2)}`;
}
