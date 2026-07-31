export type HonorGlowTier = 0 | 1 | 2 | 3 | 4 | 5 | 6 | 7;

export type HonorDisplaySurface = "inline" | "profile";

/** Max glow tier shown in message lists and compact identity rows. */
export const INLINE_GLOW_CAP: HonorGlowTier = 5;

export function glowTierFromLevel(level: number): HonorGlowTier {
  if (level <= 5) return 1;
  if (level <= 12) return 2;
  if (level <= 22) return 3;
  if (level <= 35) return 4;
  if (level <= 45) return 5;
  if (level < 50) return 6;
  return 7;
}

export function effectiveGlowTier(level: number, surface: HonorDisplaySurface): HonorGlowTier {
  const tier = glowTierFromLevel(level);
  if (surface === "inline") {
    return Math.min(tier, INLINE_GLOW_CAP) as HonorGlowTier;
  }
  return tier;
}

/** Downgrade animated name styles on inline surfaces (anti-harsh). */
export function resolveHonorNameStyleForSurface(
  nameStyle: string | undefined,
  surface: HonorDisplaySurface,
): string {
  const style = nameStyle ?? "default";
  if (surface !== "inline") return style;
  switch (style) {
    case "animated_glow":
      return "glow";
    case "animated_prismatic":
      return "prismatic";
    case "shimmer":
      return "gold";
    default:
      return style;
  }
}

export function honorPulseDurationSeconds(glowTier: HonorGlowTier): number {
  if (glowTier <= 1) return 0;
  if (glowTier === 2) return 6.4;
  if (glowTier === 3) return 6;
  if (glowTier === 4) return 5.6;
  if (glowTier === 5) return 5.2;
  return 4.8;
}
