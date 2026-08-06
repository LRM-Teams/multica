/**
 * Research V6 · 10k-node performance gate tests (LRM-1485 / FE-09).
 *
 * Repeatable CI assertions over the gate harness in `./performance-gate`,
 * honoring the rewritten design contract (PR #2415):
 *   - AC1 → single slice request ≤ 500 nodes; cumulative cache bounded + evictable.
 *   - AC2 → pan/expand recomputes only affected roots; unaffected nodes keep
 *           identity and exact position.
 *   - AC3 → thresholds fail with concrete metrics (not a bare timeout).
 *   - viewport-performance §3 → desktop 120/180 DOM≤220, mid 72/96, narrow 32/48.
 *   - §7 → 10k, initial slice, 100/20-node delta, scroll window, motion ≤64,
 *           off-screen direct-final, 200% zoom coverage is folded into the
 *           visible-DOM gate (dense expansion must stay within DOM budget).
 *   - Reduced Motion → all animation durations 0.
 */

import { describe, expect, it } from "vitest";
import {
  CACHE_NODE_BUDGET,
  MOTION_QUEUE_PEAK,
  PER_PAGE_MAX,
  VIEWPORT_BUDGETS,
  foldToBudget,
  formatMetric,
  formatReport,
  type Report,
  runAllGates,
  runLayoutScopeGate,
  runMotionGate,
  runScrollWindowGate,
  runSliceTransportGate,
  runTwentyNodeDeltaGate,
  runVisibleBudgetGate,
  coalesceMotionIntents,
  timed,
} from "./performance-gate";

describe("Research V6 10k performance gate (LRM-1485) · AC1 slice transport", () => {
  it("every wire slice request is ≤ 500 nodes and the browser never fetches 10k", async () => {
    const gate = await runSliceTransportGate({ totalNodes: 10_000 });
    expect(gate.perPageNodes).toBeGreaterThan(0);
    expect(gate.perPageNodes).toBeLessThanOrEqual(PER_PAGE_MAX);
    const metric = gate.report.metrics.find((m) => m.key === "perPageNodes");
    expect(metric?.pass).toBe(true);
  });

  it("cumulative retained cache stays within the node budget and actually evicts", async () => {
    const gate = await runSliceTransportGate({ totalNodes: 10_000 });
    expect(gate.retainedUniqueNodes).toBeLessThanOrEqual(CACHE_NODE_BUDGET);
    expect(gate.evictions).toBeGreaterThan(0);
  });

  it("an evicted page re-downloads only that page, never the whole graph", async () => {
    const gate = await runSliceTransportGate({ totalNodes: 10_000 });
    // The cache must actually evict while walking the full 10k under a
    // 1500-node budget.
    expect(gate.evictions).toBeGreaterThan(0);
    // Re-reading the long-evicted first page costs at most ONE wire request.
    expect(gate.wireRequests).toBeLessThanOrEqual(1);
    expect(gate.perPageNodes).toBeLessThanOrEqual(PER_PAGE_MAX);
  });
});

describe("Research V6 10k performance gate (LRM-1485) · AC2 scoped layout + 20-node delta", () => {
  it("pan/expand recomputes only the affected cluster; unaffected nodes keep exact position", () => {
    const gate = runLayoutScopeGate({ nodeCount: 10_000, clusters: 40 });
    const clusterSize = gate.affectedRegionSize;
    const expected = 10_000 - clusterSize;
    expect(gate.unaffectedStableCount).toBe(expected);
    expect(gate.unaffectedNodeIds).toHaveLength(expected);
    const rep = gate.report.metrics.find((m) => m.key === "affectedRegion");
    expect(rep?.value).toBe(clusterSize);
    expect(clusterSize).toBe(10_000 / 40);
  });

  it("unaffected node identity is stable across repeated recomputes", () => {
    const gate = runLayoutScopeGate({ nodeCount: 10_000, clusters: 40, iterations: 8 });
    const expected = 10_000 - gate.affectedRegionSize;
    expect(new Set(gate.unaffectedNodeIds).size).toBe(gate.unaffectedNodeIds.length);
    expect(gate.unaffectedStableCount).toBe(expected * 8);
  });

  it("a real 20-node canvas delta recomputes only the affected cluster + new nodes", () => {
    const gate = runTwentyNodeDeltaGate({ nodeCount: 10_000, clusters: 40, newNodes: 20 });
    const regionSize = 10_000 / 40;
    // Untouched = everything not in cluster-0 and not a new node.
    expect(gate.untouchedStableCount).toBe(10_000 - regionSize);
    // Touched-or-moved = cluster-0 region (250) + the 20 new nodes (270); the
    // affected set is (nodeCount + newNodes) minus the untouched count.
    const touched = (10_000 + 20) - gate.untouchedStableCount;
    expect(touched).toBe(regionSize + 20);
    // Identity: every untouched node is still present in the rendered view.
    expect(gate.renderedNodeIdentityCount).toBe(10_000 - regionSize);
  });
});

describe("Research V6 10k performance gate (LRM-1485) · visible-DOM budget (PR #2415 §3/§7)", () => {
  it("exposes the fixed design-contract budgets: desktop 120/180 DOM≤220, mid 72/96, narrow 32/48", () => {
    const desktop = VIEWPORT_BUDGETS.find((b) => b.key === "desktop")!;
    expect(desktop.softLimit).toBe(120);
    expect(desktop.hardLimit).toBe(180);
    expect(desktop.domBudget).toBe(220);
    const mid = VIEWPORT_BUDGETS.find((b) => b.key === "mid")!;
    expect(mid.softLimit).toBe(72);
    expect(mid.hardLimit).toBe(96);
    const narrow = VIEWPORT_BUDGETS.find((b) => b.key === "narrow")!;
    expect(narrow.softLimit).toBe(32);
    expect(narrow.hardLimit).toBe(48);
  });

  it("first screen mounts ≤ soft limit and DOM never exceeds the budget at any breakpoint", () => {
    const gate = runVisibleBudgetGate({ nodeCount: 10_000, clusters: 40 });
    for (const v of gate.perViewport) {
      expect(v.firstScreenCanonical).toBeLessThanOrEqual(v.softLimit);
      expect(v.oneScreenDomBudget).toBeLessThanOrEqual(v.domBudget);
    }
    expect(gate.maxMountedDom).toBeLessThanOrEqual(gate.maxMountedDomBudget);
    expect(gate.maxMountedDomBudget).toBe(220);
    expect(gate.report.passedMetricCount).toBe(gate.report.totalMetricCount);
  });

  it("an over-budget dense expansion folds into Display Groups to stay within DOM budget (200% zoom analog)", () => {
    const desktop = VIEWPORT_BUDGETS.find((b) => b.key === "desktop")!;
    // A 200%-zoom analog: a dense expansion bringing 400 candidate cards.
    const folded = foldToBudget(400, desktop);
    expect(folded.cards).toBeLessThanOrEqual(desktop.hardLimit);
    expect(folded.cards).toBe(180);
    expect(folded.dom).toBeLessThanOrEqual(desktop.domBudget);
  });
});

describe("Research V6 10k performance gate (LRM-1485) · scroll window + overscan (§7)", () => {
  it("mounted DOM stays bounded at any scroll offset through the whole 10k", () => {
    const gate = runScrollWindowGate({ totalItems: 10_000, windowSize: 40, overscan: 8, probeOffsets: 200 });
    expect(gate.maxMounted).toBeLessThanOrEqual(48);
    expect(gate.windowPeak).toBeLessThanOrEqual(48);
    // The vast majority of the trajectory is never mounted at once.
    expect(gate.neverMountedFraction).toBeGreaterThan(0.9);
    expect(gate.report.passedMetricCount).toBe(gate.report.totalMetricCount);
  });
});

describe("Research V6 10k performance gate (LRM-1485) · motion budget (PR #2415)", () => {
  it("a 100-intent burst coalesces to ≤ 64 queued animations", () => {
    const gate = runMotionGate({ burst: 100, distinctRoots: 20 });
    expect(gate.queueLength).toBeLessThanOrEqual(MOTION_QUEUE_PEAK);
    expect(gate.rawIntents).toBe(100);
  });

  it("off-screen nodes never animate — they land directly on their final state", () => {
    const gate = runMotionGate({ burst: 100, distinctRoots: 20 });
    expect(gate.offscreenAnimateCount).toBe(0);
    const rawOff = gate.report.metrics.find((m) => m.key === "rawOffscreen");
    expect(rawOff!.value).toBeGreaterThan(0);
  });

  it("Reduced Motion drives every duration to 0 (direct terminal)", () => {
    // Direct unit-level check of the coalescer under Reduced Motion.
    const queue = coalesceMotionIntents(
      [
        { root: "r1", kind: "branch_spawned", nodeId: "a", onscreen: true, durationMs: 260 },
        { root: "r2", kind: "result_accepted", nodeId: "b", onscreen: true, durationMs: 180 },
        { root: "r3", kind: "integration_formed", nodeId: "c", onscreen: false, durationMs: 220 },
      ],
      true,
    );
    expect(queue.every((m) => m.durationMs === 0)).toBe(true);
    const gate = runMotionGate({ burst: 100 });
    expect(gate.reducedMotionMaxDuration).toBe(0);
  });

  it("motion queue respect the peak constant exposed to CI", () => {
    expect(MOTION_QUEUE_PEAK).toBe(64);
  });
});

describe("Research V6 10k performance gate (LRM-1485) · AC3 report shape", () => {
  it("gate report surfaces concrete metrics so a threshold failure is diagnostic, not just a timeout", async () => {
    const transport = await runSliceTransportGate({ totalNodes: 10_000 });
    const layout = runLayoutScopeGate({ nodeCount: 10_000, clusters: 40 });
    const delta = runTwentyNodeDeltaGate({ nodeCount: 10_000, clusters: 40 });
    const visible = runVisibleBudgetGate({ nodeCount: 10_000, clusters: 40 });
    const scroll = runScrollWindowGate({ totalItems: 10_000 });
    const motion = runMotionGate({ burst: 100 });
    const all = [...transport.report.metrics, ...layout.report.metrics, ...delta.report.metrics, ...visible.report.metrics, ...scroll.report.metrics, ...motion.report.metrics];
    const keys = all.map((m) => m.key);
    for (const required of [
      "perPageNodes",
      "retainedUniqueNodes",
      "evictions",
      "pageWalkMs",
      "unaffectedStable",
      "affectedRegion",
      "untouchedStable",
      "desktop-firstScreenCanonical",
      "desktop-mountedDom",
      "maxMountedDom",
      "maxMounted",
      "neverMountedFraction",
      "queueLength",
      "reducedMotionMaxDuration",
    ]) {
      expect(keys).toContain(required);
    }
    expect(formatReport(transport.report)).toMatch(/^slice-transport:/);
    expect(formatReport(layout.report)).toMatch(/^layout-scope:/);
    expect(formatReport(delta.report)).toMatch(/^twenty-node-delta:/);
    expect(formatReport(visible.report)).toMatch(/^visible-dom-budget:/);
    expect(formatReport(scroll.report)).toMatch(/^scroll-window:/);
    expect(formatReport(motion.report)).toMatch(/^motion-budget:/);
  });

  it("a wall-time bound exists and the fixture walk stays within it", async () => {
    const gate = await runSliceTransportGate({ totalNodes: 10_000 });
    const walk = gate.report.metrics.find((m) => m.key === "pageWalkMs");
    expect(walk?.pass).toBe(true);
    const measured = timed(() => runLayoutScopeGate({ nodeCount: 10_000, clusters: 40 }));
    expect(measured.value.unaffectedStableCount).toBeGreaterThan(0);
    expect(measured.elapsedMs).toBeGreaterThanOrEqual(0);
  });

  it("the aggregate runner emits one stable line per gate for CI dashboards", async () => {
    const { reports, passed, total } = await runAllGates({ clusters: 40 });
    expect(reports.length).toBe(6);
    expect(passed).toBeGreaterThan(0);
    expect(total).toBeGreaterThanOrEqual(passed);
  });

  it("report is a stable structured shape for CI", () => {
    const report: Report = {
      name: "slice-transport",
      metrics: [{ key: "perPageNodes", value: 500, threshold: 500, unit: "", pass: true }],
      passedMetricCount: 1,
      totalMetricCount: 1,
    };
    expect(report.passedMetricCount).toBe(1);
    expect(report.totalMetricCount).toBe(1);
    expect(formatReport(report)).toBe("slice-transport: perPageNodes=500 passed=1/1");
    expect(formatMetric(report.metrics[0]!)).toContain("perPageNodes=");
  });
});
