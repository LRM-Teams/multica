import { describe, expect, it } from "vitest";
import {
  NODE_ENTER_DURATION_MS,
  NODE_ENTER_STAGGER_CAP,
  NODE_ENTER_STAGGER_MS,
  nodeEnterDelayStyle,
  nodeEnterMotionCss,
  nodeEnterStaggerDelayMs,
} from "./node-enter-motion";

describe("node-enter-motion (LRM-827)", () => {
  it("keeps per-node duration at or under 300ms", () => {
    expect(NODE_ENTER_DURATION_MS).toBeLessThanOrEqual(300);
    expect(NODE_ENTER_DURATION_MS).toBeGreaterThan(0);
  });

  it("staggers new nodes with a capped rhythm", () => {
    expect(nodeEnterStaggerDelayMs(0)).toBe(0);
    expect(nodeEnterStaggerDelayMs(1)).toBe(NODE_ENTER_STAGGER_MS);
    expect(nodeEnterStaggerDelayMs(3)).toBe(3 * NODE_ENTER_STAGGER_MS);
    expect(nodeEnterStaggerDelayMs(NODE_ENTER_STAGGER_CAP + 5)).toBe(
      NODE_ENTER_STAGGER_CAP * NODE_ENTER_STAGGER_MS,
    );
  });

  it("exposes delay via CSS variable for shell animation", () => {
    expect(nodeEnterDelayStyle(80)["--research-node-enter-delay"]).toBe("80ms");
  });

  it("ships reduced-motion kill-switch in shared CSS", () => {
    const css = nodeEnterMotionCss();
    expect(css).toContain("prefers-reduced-motion: reduce");
    expect(css).toContain("animation: none");
    expect(css).toContain(`${NODE_ENTER_DURATION_MS}ms`);
    expect(css).toContain("translateY");
  });
});
