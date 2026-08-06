import { describe, expect, it } from "vitest";
import {
  effectiveGlowTier,
  glowTierFromLevel,
  honorPulseDurationSeconds,
  resolveHonorNameStyleForSurface,
} from "@multica/ui/lib/honor-glow";

describe("honor glow tiers", () => {
  it("maps high levels to upper tiers", () => {
    expect(glowTierFromLevel(50)).toBe(7);
  });

  it("caps dense inline surfaces at tier V while profiles show the full tier", () => {
    expect(effectiveGlowTier(40, "inline")).toBe(5);
    expect(effectiveGlowTier(40, "profile")).toBe(5);
    expect(effectiveGlowTier(50, "inline")).toBe(5);
    expect(effectiveGlowTier(50, "profile")).toBe(7);
  });

  it("downgrades animated styles on inline surfaces", () => {
    expect(resolveHonorNameStyleForSurface("animated_glow", "inline")).toBe("glow");
    expect(resolveHonorNameStyleForSurface("shimmer", "inline")).toBe("gold");
  });

  it("uses slow breathing intervals instead of rapid flashing", () => {
    expect(honorPulseDurationSeconds(1)).toBe(0);
    expect(honorPulseDurationSeconds(3)).toBeGreaterThanOrEqual(5);
    expect(honorPulseDurationSeconds(7)).toBeGreaterThanOrEqual(4.5);
  });
});
