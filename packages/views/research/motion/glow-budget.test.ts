import { describe, expect, it } from "vitest";
import type { MotionDirective } from "./directives";
import { capTransitionGlowDirectives } from "./glow-budget";

function glowDirective(id: string): MotionDirective {
  return {
    className: `motion-${id}`,
    style: {},
    markerClass: null,
    dataVerb: "appear",
    glowDisabled: false,
  };
}

describe("capTransitionGlowDirectives", () => {
  it("keeps glow on the first entity only (D5 §9)", () => {
    const input = new Map<string, MotionDirective | null>([
      ["a", glowDirective("a")],
      ["b", glowDirective("b")],
      ["c", { ...glowDirective("c"), glowDisabled: true }],
    ]);

    const capped = capTransitionGlowDirectives(input);
    expect(capped.get("a")?.glowDisabled).toBe(false);
    expect(capped.get("b")?.glowDisabled).toBe(true);
    expect(capped.get("c")?.glowDisabled).toBe(true);
  });
});
