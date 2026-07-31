import type { CSSProperties } from "react";
import {
  effectiveGlowTier,
  honorPulseDurationSeconds,
  resolveHonorNameStyleForSurface,
  type HonorDisplaySurface,
} from "./honor-glow";

export function honorNameClassName(nameStyle: string | undefined): string {
  switch (nameStyle) {
    case "ice":
    case "member":
    case "emerald":
    case "sapphire":
    case "gold":
    case "coral":
    case "amethyst":
    case "founding":
    case "prismatic":
    case "aurora":
    case "glow":
    case "solar":
    case "shimmer":
    case "nebula":
    case "cyber":
    case "animated_prismatic":
    case "plasma":
    case "animated_glow":
    case "eclipse":
    case "nova":
    case "quantum":
    case "celestial":
    case "mythic":
    case "transcendent":
      return `honor-name honor-name--${nameStyle.replaceAll("_", "-")}`;
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
  "data-honor-surface": HonorDisplaySurface;
  style?: CSSProperties;
} {
  const surface = opts.surface ?? "inline";
  const level = opts.level ?? 1;
  const resolvedStyle = resolveHonorNameStyleForSurface(opts.nameStyle, surface);
  const glowTier = effectiveGlowTier(level, surface);
  const baseClass = honorNameClassName(resolvedStyle);

  if (glowTier <= 0) {
    return { className: baseClass, "data-honor-surface": surface };
  }

  return {
    className: `${baseClass} honor-name-glow`.trim(),
    "data-honor-glow-tier": String(glowTier),
    "data-honor-surface": surface,
    style: {
      "--honor-pulse-duration": `${honorPulseDurationSeconds(glowTier)}s`,
    } as CSSProperties,
  };
}
