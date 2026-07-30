import { describe, expect, it } from "vitest";
import {
  effectiveGlowTier,
  glowTierFromLevel,
  resolveHonorNameStyleForSurface,
} from "@multica/ui/lib/honor-glow";

describe("honor glow tiers", () => {
  it("maps high levels to upper tiers", () => {
    expect(glowTierFromLevel(50)).toBe(7);
  });

  it("caps inline surfaces at tier III", () => {
    expect(effectiveGlowTier(40, "inline")).toBe(3);
    expect(effectiveGlowTier(40, "profile")).toBe(5);
  });

  it("downgrades animated styles on inline surfaces", () => {
    expect(resolveHonorNameStyleForSurface("animated_glow", "inline")).toBe("glow");
    expect(resolveHonorNameStyleForSurface("shimmer", "inline")).toBe("gold");
  });
});
