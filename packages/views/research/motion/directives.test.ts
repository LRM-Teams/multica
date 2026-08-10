/**
 * LRM-1477 — Directive builder tests (AC1/AC3 presentation layer).
 *
 * Asserts the per-verb scoped class + inline style + persistent static marker,
 * including the reduced-motion (Rule ④) and low-performance (Rule ⑤) paths and
 * that the emitted CSS contains the required keyframes (Rule ② DOM contrast).
 */
import { describe, expect, it } from "vitest";
import {
  buildMotionDirective,
  semanticMotionCss,
  verbClass,
  markerClass,
  type MotionDirectiveOptions,
} from "./directives";

const baseOpts: MotionDirectiveOptions = {
  reducedMotion: false,
  lowPerformance: false,
  animationDelayMs: 0,
  durationMs: 300,
};

const markerOpts = (marker: MotionDirectiveOptions["marker"]): MotionDirectiveOptions => ({
  ...baseOpts,
  marker,
});

describe("buildMotionDirective — AC1 deterministic per-verb output", () => {
  it("maps each displacement verb to its own animationName + scoped class", () => {
    const cases: Array<[string, string]> = [
      ["appear", "research-motion-appear"],
      ["merge", "research-motion-merge"],
      ["conflict", "research-motion-conflict"],
      ["escalate", "research-motion-escalate"],
      ["stale", "research-motion-stale"],
      ["revise", "research-motion-revise"],
      ["reappear", "research-motion-fade-in"],
      ["camera", "research-motion-camera"],
    ];
    for (const [verb, animation] of cases) {
      const d = buildMotionDirective(verb as never, "t1", baseOpts);
      expect(d.style.animationName).toBe(animation);
      expect(d.className).toBe(`research-motion-${verb} txn-t1`);
      expect(d.dataVerb).toBe(verb);
    }
  });

  it("maps the D5 lifecycle verbs to their own keyframes (LRM-1537 §2)", () => {
    const cases: Array<[string, string]> = [
      ["retire", "research-motion-retire"],
      ["restart", "research-motion-restart"],
      ["regoal", "research-motion-regoal"],
    ];
    for (const [verb, animation] of cases) {
      const d = buildMotionDirective(verb as never, "t1", baseOpts);
      expect(d.style.animationName).toBe(animation);
      expect(d.dataVerb).toBe(verb);
    }
  });

  it("retire ends at the stale opacity (grey-out, never disappears) — ⑤", () => {
    const d = buildMotionDirective("retire", "t1", baseOpts);
    expect(d.style.opacity).toBe("0.55");
  });

  it("applies displacement CSS vars only to the verbs that move", () => {
    const appear = buildMotionDirective("appear", "t1", baseOpts);
    expect(appear.style["--motion-rise-px"]).toBe("8px");

    const conflict = buildMotionDirective("conflict", "t1", baseOpts);
    expect(conflict.style["--motion-gap-px"]).toBe("12px");

    const escalate = buildMotionDirective("escalate", "t1", baseOpts);
    expect(escalate.style["--motion-rise-px"]).toBe("8px");
  });

  it("halves displacement scale under low-performance (Rule ⑤)", () => {
    const appear = buildMotionDirective("appear", "t1", {
      ...baseOpts,
      lowPerformance: true,
    });
    expect(appear.style["--motion-rise-px"]).toBe("4px");
  });

  it("keeps glow enabled unless low-performance disables it", () => {
    expect(buildMotionDirective("escalate", "t1", baseOpts).glowDisabled).toBe(false);
    expect(
      buildMotionDirective("escalate", "t1", { ...baseOpts, lowPerformance: true })
        .glowDisabled,
    ).toBe(true);
  });
});

describe("Static marker (Rule ②) — carried through reduced-motion", () => {
  it("supplies the conflict border marker when a semantic marker is given", () => {
    const d = buildMotionDirective("conflict", "t1", markerOpts("conflict-border"));
    expect(d.markerClass).toContain("research-motion-marker-conflict-border");
  });

  it("carries the semantic marker even when reduced-motion collapses the verb (Rule ④)", () => {
    const reduced = buildMotionDirective("reappear", "t1", {
      ...markerOpts("conflict-border"),
      reducedMotion: true,
    });
    // All displacement collapses to a uniform fade…
    expect(reduced.style.animationName).toBe("research-motion-fade-in");
    // …but the persistent conflict marker is retained.
    expect(reduced.markerClass).toContain("research-motion-marker-conflict-border");
  });

  it("emits no marker class when the semantic marker is none", () => {
    const d = buildMotionDirective("appear", "t1", markerOpts("none"));
    expect(d.markerClass).toBeNull();
  });
});

describe("markerClass / verbClass helpers", () => {
  it("only emits a marker class for a real static marker", () => {
    expect(markerClass("none")).toBeNull();
    expect(markerClass("stale-grey")).toBe("research-motion-marker research-motion-marker-stale-grey");
  });

  it("scopes every verb class by its transition id", () => {
    expect(verbClass("merge", "txn-7")).toBe("research-motion-merge txn-txn-7");
  });
});

describe("semanticMotionCss — Rule ② DOM contrast keyframes", () => {
  it("contains every required keyframe definition", () => {
    const css = semanticMotionCss();
    const required = [
      "research-motion-appear",
      "research-motion-merge",
      "research-motion-conflict",
      "research-motion-escalate",
      "research-motion-stale",
      "research-motion-revise",
      "research-motion-fade-in",
      "research-motion-camera",
      // D5 lifecycle keyframes (LRM-1537 §2):
      "research-motion-retire",
      "research-motion-restart",
      "research-motion-regoal",
      "@media (prefers-reduced-motion: reduce)",
    ];
    for (const name of required) {
      expect(css).toContain(name);
    }
  });

  it("eliminates the hardcoded brand hex in research-motion-revise (Rule ② zero-hardcoded-hex)", () => {
    const css = semanticMotionCss();
    // The old rgba(59,130,246) must be replaced by the semantic --brand token.
    expect(css).not.toContain("rgba(59,130,246");
    expect(css).not.toMatch(/#[0-9a-fA-F]{3,8}\b/);
    expect(css).toContain("color-mix(in oklch, var(--brand)");
  });

  it("guards all classes under the scoped root so styles never leak", () => {
    const css = semanticMotionCss();
    expect(css).toContain(".research-semantic-motion");
    expect(css).toContain("will-change: transform, opacity;");
  });
});
