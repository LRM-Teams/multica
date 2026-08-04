/**
 * LRM-1335 — canvas-reorg-motion pure function tests.
 *
 * AC coverage:
 *  #1: classifyCanvasDelta three-state (23px no trigger / ≥24px reorg / appended)
 *  #4: timing constants single ≤320ms, total ≤900ms, stagger cap ≤6
 *  #5: CSS selectors only target .react-flow__node in [data-reorg], no .react-flow__viewport
 */

import { describe, expect, it } from "vitest";
import {
  type CanvasNodeSnapshot,
  REORG_DISPLACEMENT_THRESHOLD_PX,
  REORG_LANE_STAGGER_CAP,
  REORG_SINGLE_ELEMENT_MAX_MS,
  REORG_TOTAL_BUDGET_MS,
  P1_START_MS,
  P1_DURATION_MS,
  P2_START_MS,
  P2_DURATION_MS,
  P3_START_MS,
  P3_DURATION_MS,
  P4_START_MS,
  P4_DURATION_MS,
  classifyCanvasDelta,
  reorgTransitionCss,
  buildNodeSnapshotMap,
  gutterGrowthStyle,
  gutterGrowthTargetStyle,
  REORG_LANE_STAGGER_MS,
} from "./canvas-reorg-motion";

// ─── classifyCanvasDelta ─────────────────────────────────────────────────────

describe("classifyCanvasDelta", () => {
  const makeSnap = (x: number, y: number, lane = 0, nodeType = "probe", status = "done"): CanvasNodeSnapshot => ({
    x, y, lane, nodeType, status,
  });

  it("returns 'none' when both maps are empty", () => {
    const result = classifyCanvasDelta(new Map(), new Map());
    expect(result.kind).toBe("none");
    expect(result.movedIds).toHaveLength(0);
    expect(result.addedIds).toHaveLength(0);
    expect(result.removedIds).toHaveLength(0);
  });

  it("returns 'none' when positions differ less than threshold (23px)", () => {
    const prev = new Map([["a", makeSnap(100, 100)]]);
    // 23px diagonal: ~16.26 per axis
    const next = new Map([["a", makeSnap(116.26, 116.26)]]);
    // sqrt(16.26^2 + 16.26^2) ≈ 22.99 < 24
    const result = classifyCanvasDelta(prev, next);
    expect(result.kind).toBe("none");
  });

  it("returns 'none' when displacement is exactly below threshold (23.99px)", () => {
    const prev = new Map([["a", makeSnap(0, 0)]]);
    // 23.99px straight horizontal
    const next = new Map([["a", makeSnap(23.99, 0)]]);
    const result = classifyCanvasDelta(prev, next);
    expect(result.kind).toBe("none");
  });

  it("returns 'reorg' when displacement reaches threshold (24px)", () => {
    const prev = new Map([["a", makeSnap(0, 0)]]);
    const next = new Map([["a", makeSnap(24, 0)]]);
    const result = classifyCanvasDelta(prev, next);
    expect(result.kind).toBe("reorg");
    expect(result.movedIds).toContain("a");
  });

  it("returns 'reorg' when displacement exceeds threshold (100px)", () => {
    const prev = new Map([["a", makeSnap(0, 0)]]);
    const next = new Map([["a", makeSnap(80, 60)]]);
    // sqrt(80^2 + 60^2) = 100
    const result = classifyCanvasDelta(prev, next);
    expect(result.kind).toBe("reorg");
    expect(result.movedIds).toContain("a");
  });

  it("returns 'reorg' when lane changes (even if position stays)", () => {
    const prev = new Map([["a", makeSnap(100, 100, 0)]]);
    const next = new Map([["a", makeSnap(100, 100, 1)]]);
    const result = classifyCanvasDelta(prev, next);
    expect(result.kind).toBe("reorg");
    expect(result.movedIds).toContain("a");
  });

  it("returns 'reorg' when node_type changes", () => {
    const prev = new Map([["a", makeSnap(100, 100, 0, "probe")]]);
    const next = new Map([["a", makeSnap(100, 100, 0, "finding")]]);
    const result = classifyCanvasDelta(prev, next);
    expect(result.kind).toBe("reorg");
    expect(result.movedIds).toContain("a");
  });

  it("returns 'reorg' when status changes", () => {
    const prev = new Map([["a", makeSnap(100, 100, 0, "probe", "running")]]);
    const next = new Map([["a", makeSnap(100, 100, 0, "probe", "done")]]);
    const result = classifyCanvasDelta(prev, next);
    expect(result.kind).toBe("reorg");
    expect(result.movedIds).toContain("a");
  });

  it("returns 'appended' when only new nodes added (no moves)", () => {
    const prev = new Map([["a", makeSnap(100, 100)]]);
    const next = new Map([
      ["a", makeSnap(100, 100)],
      ["b", makeSnap(200, 200)],
      ["c", makeSnap(300, 300)],
    ]);
    const result = classifyCanvasDelta(prev, next);
    expect(result.kind).toBe("appended");
    expect(result.addedIds).toEqual(expect.arrayContaining(["b", "c"]));
    expect(result.movedIds).toHaveLength(0);
  });

  it("returns 'reorg' when nodes are added AND existing ones move", () => {
    const prev = new Map([["a", makeSnap(0, 0)]]);
    const next = new Map([
      ["a", makeSnap(50, 50)],
      ["b", makeSnap(200, 200)],
    ]);
    const result = classifyCanvasDelta(prev, next);
    expect(result.kind).toBe("reorg");
    expect(result.movedIds).toContain("a");
    expect(result.addedIds).toContain("b");
  });

  it("returns 'reorg' when nodes are removed", () => {
    const prev = new Map([
      ["a", makeSnap(100, 100)],
      ["b", makeSnap(200, 200)],
    ]);
    const next = new Map([["a", makeSnap(100, 100)]]);
    const result = classifyCanvasDelta(prev, next);
    expect(result.kind).toBe("reorg");
    expect(result.removedIds).toContain("b");
  });

  it("correctly handles mix of add, move, and remove", () => {
    const prev = new Map([
      ["stay", makeSnap(10, 10)],
      ["move", makeSnap(0, 0)],
      ["gone", makeSnap(50, 50)],
    ]);
    const next = new Map([
      ["stay", makeSnap(10, 10)],
      ["move", makeSnap(30, 0)], // 30px > 24 threshold
      ["new1", makeSnap(200, 200)],
    ]);
    const result = classifyCanvasDelta(prev, next);
    expect(result.kind).toBe("reorg");
    expect(result.movedIds).toContain("move");
    expect(result.addedIds).toContain("new1");
    expect(result.removedIds).toContain("gone");
    expect(result.movedIds).not.toContain("stay");
  });
});

// ─── Timing constants (AC #4) ────────────────────────────────────────────────

describe("timing constants", () => {
  it("single element duration ≤ 320ms", () => {
    expect(REORG_SINGLE_ELEMENT_MAX_MS).toBeLessThanOrEqual(320);
  });

  it("total budget ≤ 900ms", () => {
    expect(REORG_TOTAL_BUDGET_MS).toBeLessThanOrEqual(900);
  });

  it("lane stagger cap ≤ 6", () => {
    expect(REORG_LANE_STAGGER_CAP).toBeLessThanOrEqual(6);
  });

  it("displacement threshold is exactly 24px", () => {
    expect(REORG_DISPLACEMENT_THRESHOLD_PX).toBe(24);
  });

  it("P2 node reposition duration ≤ single element max", () => {
    expect(P2_DURATION_MS).toBeLessThanOrEqual(REORG_SINGLE_ELEMENT_MAX_MS);
  });

  it("no phase end exceeds total budget", () => {
    expect(P1_START_MS + P1_DURATION_MS).toBeLessThanOrEqual(REORG_TOTAL_BUDGET_MS);
    expect(P2_START_MS + P2_DURATION_MS).toBeLessThanOrEqual(REORG_TOTAL_BUDGET_MS);
    expect(P3_START_MS + P3_DURATION_MS).toBeLessThanOrEqual(REORG_TOTAL_BUDGET_MS);
    expect(P4_START_MS + P4_DURATION_MS).toBeLessThanOrEqual(REORG_TOTAL_BUDGET_MS);
  });

  it("max stagger (cap × per-lane) does not exceed budget when added to P3 end", () => {
    const maxStagger = REORG_LANE_STAGGER_CAP * REORG_LANE_STAGGER_MS;
    expect(P3_START_MS + P3_DURATION_MS + maxStagger).toBeLessThanOrEqual(REORG_TOTAL_BUDGET_MS);
  });
});

// ─── CSS selector assertions (AC #5) ─────────────────────────────────────────

describe("reorgTransitionCss", () => {
  const css = reorgTransitionCss();

  it("targets .react-flow__node within [data-reorg=\"running\"]", () => {
    expect(css).toContain('[data-reorg="running"] .react-flow__node');
  });

  it("does NOT target .react-flow__viewport", () => {
    expect(css).not.toContain(".react-flow__viewport");
  });

  it("all selectors are scoped under [data-reorg]", () => {
    // Every CSS rule line that is a selector should begin with [data-reorg
    const selectorLines = css
      .split("\n")
      .filter((line) => line.trim() && !line.trim().startsWith("//") && line.includes("{") || line.includes("}"))
      .filter((line) => !line.trim().startsWith("}") && !line.trim().startsWith("//"));
    const ruleStarters = selectorLines.filter(
      (line) => line.includes("{") && !line.trim().startsWith("}"),
    );
    for (const rule of ruleStarters) {
      expect(rule.trim()).toMatch(/^\[data-reorg/);
    }
  });

  it("includes transition property with correct duration", () => {
    expect(css).toContain(`${P2_DURATION_MS}ms`);
  });
});

// ─── buildNodeSnapshotMap ────────────────────────────────────────────────────

describe("buildNodeSnapshotMap", () => {
  it("excludes gitGutter nodes", () => {
    const nodes = [
      { id: "gutter1", position: { x: 0, y: 0 }, type: "gitGutter", data: { gitLane: 0 } },
      { id: "research1", position: { x: 10, y: 20 }, type: "research", data: { research: { node_type: "probe", status: "done" }, gitLane: 1 } },
    ];
    const map = buildNodeSnapshotMap(nodes);
    expect(map.has("gutter1")).toBe(false);
    expect(map.has("research1")).toBe(true);
    expect(map.get("research1")).toEqual({
      x: 10,
      y: 20,
      lane: 1,
      nodeType: "probe",
      status: "done",
    });
  });

  it("handles missing research data gracefully", () => {
    const nodes = [
      { id: "n1", position: { x: 5, y: 5 }, type: "research", data: { gitLane: 0 } },
    ];
    const map = buildNodeSnapshotMap(nodes);
    expect(map.get("n1")?.nodeType).toBe("unknown");
    expect(map.get("n1")?.status).toBeUndefined();
  });
});

// ─── gutterGrowthStyle ───────────────────────────────────────────────────────

describe("gutterGrowthStyle", () => {
  it("returns dashoffset equal to pathLength (fully hidden)", () => {
    const style = gutterGrowthStyle(500, 0);
    expect(style.strokeDasharray).toBe(500);
    expect(style.strokeDashoffset).toBe(500);
  });

  it("includes stagger delay based on lane index", () => {
    const style0 = gutterGrowthStyle(100, 0);
    const style3 = gutterGrowthStyle(100, 3);
    // Lane 0: delay = P3_START_MS + 0
    expect(style0.transition).toContain(`${P3_START_MS}ms`);
    // Lane 3: delay = P3_START_MS + 3*40 = P3_START_MS + 120
    expect(style3.transition).toContain(`${P3_START_MS + 3 * REORG_LANE_STAGGER_MS}ms`);
  });

  it("caps stagger at REORG_LANE_STAGGER_CAP", () => {
    const style10 = gutterGrowthStyle(100, 10);
    const styleCap = gutterGrowthStyle(100, REORG_LANE_STAGGER_CAP);
    // Both should have the same delay since 10 > cap (6)
    expect(style10.transition).toBe(styleCap.transition);
  });
});

describe("gutterGrowthTargetStyle", () => {
  it("returns dashoffset 0 (fully drawn)", () => {
    const style = gutterGrowthTargetStyle(500, 0);
    expect(style.strokeDasharray).toBe(500);
    expect(style.strokeDashoffset).toBe(0);
  });
});
