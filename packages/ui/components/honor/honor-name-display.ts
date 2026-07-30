import type { CSSProperties } from "react";
import {
  effectiveGlowTier,
  honorPulseDurationSeconds,
  resolveHonorNameStyleForSurface,
  type HonorDisplaySurface,
} from "../../lib/honor-glow";

export function honorNameClassName(nameStyle: string | undefined): string {
  switch (nameStyle) {
    case "member":
      return "honor-name honor-name--member";
    case "gold":
      return "honor-name honor-name--gold";
    case "founding":
      return "honor-name honor-name--founding";
    case "prismatic":
      return "honor-name honor-name--prismatic";
    case "glow":
      return "honor-name honor-name--glow";
    case "shimmer":
      return "honor-name honor-name--shimmer";
    case "animated_prismatic":
      return "honor-name honor-name--animated-prismatic";
    case "animated_glow":
      return "honor-name honor-name--animated-glow";
    default:
      return "honor-name honor-name--default";
  }
}

export function honorNameDisplayProps(opts: {
  nameStyle?: string;
  level?: number;
  surface?: HonorDisplaySurface;
}): {
  className: string;
  "data-honor-glow-tier"?: string;
  style?: CSSProperties;
} {
  const surface = opts.surface ?? "inline";
  const level = opts.level ?? 1;
  const resolvedStyle = resolveHonorNameStyleForSurface(opts.nameStyle, surface);
  const glowTier = effectiveGlowTier(level, surface);
  const baseClass = honorNameClassName(resolvedStyle);

  if (glowTier <= 0) {
    return { className: baseClass };
  }

  return {
    className: `${baseClass} honor-name-glow`.trim(),
    "data-honor-glow-tier": String(glowTier),
    style: {
      "--honor-pulse-duration": `${honorPulseDurationSeconds(glowTier)}s`,
    } as CSSProperties,
  };
}
