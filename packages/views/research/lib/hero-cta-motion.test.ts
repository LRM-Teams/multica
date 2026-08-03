// @vitest-environment node
import { describe, expect, it } from "vitest";
import {
  HERO_COMPOSER_CARD_CLASS,
  HERO_CTA_DURATION_MS,
  HERO_CTA_PRIMARY_CLASS,
  HERO_CTA_SECONDARY_CLASS,
  HERO_CTA_TRANSITION_CLASS,
} from "./hero-cta-motion";

describe("hero-cta-motion (LRM-837)", () => {
  it("keeps hover/press duration at or under 200ms (moderate token)", () => {
    expect(HERO_CTA_DURATION_MS).toBeLessThanOrEqual(200);
    expect(HERO_CTA_TRANSITION_CLASS).toContain("--motion-duration-moderate");
    expect(HERO_CTA_TRANSITION_CLASS).toContain("--motion-ease-out");
  });

  it("honours prefers-reduced-motion on the shared transition", () => {
    expect(HERO_CTA_TRANSITION_CLASS).toContain("motion-reduce:transition-none");
  });

  it("primary CTA exposes hover, press, and keyboard focus (press not hover-gated)", () => {
    expect(HERO_CTA_PRIMARY_CLASS).toContain("hover:");
    expect(HERO_CTA_PRIMARY_CLASS).toContain("active:scale");
    expect(HERO_CTA_PRIMARY_CLASS).toContain("focus-visible:ring");
    // Narrow/touch uses :active directly — not nested under @media (hover).
    expect(HERO_CTA_PRIMARY_CLASS).not.toContain("@media");
    expect(HERO_CTA_PRIMARY_CLASS).not.toContain("hover:active:");
  });

  it("secondary CTA and composer card share the moderate timing token", () => {
    expect(HERO_CTA_SECONDARY_CLASS).toContain("--motion-duration-moderate");
    expect(HERO_CTA_SECONDARY_CLASS).toContain("focus-visible:ring");
    expect(HERO_CTA_SECONDARY_CLASS).toContain("active:scale");
    expect(HERO_COMPOSER_CARD_CLASS).toContain("--motion-duration-moderate");
    expect(HERO_COMPOSER_CARD_CLASS).toContain("focus-within:");
  });
});
