/**
 * LRM-1475 AC2 — the 8-state matrix + conflict priority.
 */
import { describe, expect, it } from "vitest";
import {
  NODE_CARD_STATES,
  resolveCardState,
  stateVisualFor,
} from "./node-state-matrix";

describe("node-state-matrix (AC2)", () => {
  it("exposes exactly the 8 required states", () => {
    expect(NODE_CARD_STATES).toEqual([
      "default",
      "selected",
      "loading",
      "running",
      "failed",
      "stale",
      "unknown",
      "terminal",
    ]);
  });

  it("each state has a semantic visual pair (no hex/palette classes)", () => {
    for (const s of NODE_CARD_STATES) {
      const v = stateVisualFor(s);
      expect(v.state).toBe(s);
      expect(v.borderClass).toMatch(/^(border|ring)/);
      expect(v.borderClass).not.toMatch(/#[0-9a-f]{3,6}/i);
      expect(v.borderClass).not.toMatch(/-(50|500|600|700|800)-\d$/);
      for (const cls of [v.borderClass, v.accentBarClass ?? "", v.shellClass ?? ""]) {
        expect(cls.toLowerCase()).not.toMatch(/(sky|emerald|amber|violet|cyan|indigo)/);
      }
    }
  });

  it("no state relies on color alone — every state has a text label", () => {
    for (const s of NODE_CARD_STATES) {
      expect(stateVisualFor(s).label.length).toBeGreaterThan(0);
    }
  });

  it("conflict priority: failed > running > stale > selected > default", () => {
    expect(resolveCardState(["default"])).toBe("default");
    expect(resolveCardState(["running", "default"])).toBe("running");
    expect(resolveCardState(["failed", "running"])).toBe("failed");
    expect(resolveCardState(["stale", "selected", "running"])).toBe("running");
    expect(resolveCardState(["stale", "selected"])).toBe("stale");
    expect(resolveCardState(["selected", "default"])).toBe("selected");
    expect(resolveCardState(["failed", "stale", "selected", "running"])).toBe("failed");
  });

  it("empty signals resolve to default", () => {
    expect(resolveCardState([])).toBe("default");
  });
});
