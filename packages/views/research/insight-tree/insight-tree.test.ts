// @vitest-environment node
/**
 * LRM-1470 — Insight 组合树体验（AC1/AC2/AC3）。
 * 全部为纯函数测试，无 DOM；数据来自严格按后端计划构造的 contract fixture。
 */
import { describe, expect, it } from "vitest";
import { selectInsightDerivationNodes, validateInputEdges } from "./insight-derivation-contract";
import { insightDerivationFixture } from "./insight-derivation-fixture";
import {
  planExpanded,
  planSummary,
  preserveViewContext,
} from "./insight-tree-layout";
import { computeStalePaths } from "./insight-tree-stale";

const fixture = insightDerivationFixture;

describe("contract 适配层（后端为准，前端不推断）", () => {
  it("fixture 通过 canonical 字段合法性校验（level/inputIds/freshness）", () => {
    const sel = selectInsightDerivationNodes(fixture);
    expect(sel.ok).toBe(true);
  });

  it("level 必须是单调递增的 1 + max(input level)，无效输入被拒绝", () => {
    const bad = [
      ...fixture,
      {
        id: "x1",
        level: 0,
        inputIds: ["c1"],
        freshness: "fresh" as const,
        conclusion: "坏节点：引用了与自己同级甚至更高级的节点",
      },
    ];
    const invalid = validateInputEdges(bad as typeof fixture);
    expect(invalid.invalid.length).toBeGreaterThan(0);
    expect(invalid.invalid[0]).toContain("non-monotonic-level");
  });

  it("缺少 canonical 字段的节点被标记为合约缺口而非静默编造", () => {
    const broken = [
      ...fixture,
      { id: "y1", level: "latest" as unknown as number, inputIds: [], freshness: "fresh" as const, conclusion: "bad" },
    ];
    const sel = selectInsightDerivationNodes(broken as typeof fixture);
    expect(sel.ok).toBe(false);
    expect(sel.invalidIds).toContain("y1");
  });
});

describe("AC1 — 摘要/展开切换与节点数显著下降", () => {
  it("展开态完整呈现 ≥3 层 Insight DAG，全部节点可见", () => {
    const expanded = planExpanded(fixture);
    // 3 级以上：level 0..3
    const levels = new Set(fixture.map((f) => f.level));
    expect(levels.has(0)).toBe(true);
    expect(levels.has(1)).toBe(true);
    expect(levels.has(2)).toBe(true);
    expect(levels.has(3)).toBe(true);
    expect(expanded.length).toBe(fixture.length);
  });

  it("摘要态折叠后可见节点数显著小于展开态", () => {
    const expandedCount = planExpanded(fixture).length;
    const summary = planSummary(fixture);
    expect(summary.visibleNodeCount).toBeLessThan(expandedCount);
    // 折叠后节点数明显下降：至少节省 40%。
    expect(summary.collapsedCount).toBeGreaterThanOrEqual(Math.round(expandedCount * 0.4));
    // 折叠的子树被如实标为「显示分组」，绝不冒充真实 Insight。
    expect(summary.collapsed.length).toBeGreaterThan(0);
  });

  it("摘要态保留顶层 + stale 受影响路径的可见性（失效传播可辨）", () => {
    // pin stale 路径（c2 → i1 → r1）后，摘要应能把受影响的祖先链保持可见。
    const summary = planSummary(fixture, new Set(["r1", "i1", "c2"]));
    const visibleIds = new Set(
      summary.viewNodes.filter((v) => v.kind === "node").map((v) => v.node.id),
    );
    expect(visibleIds.has("c2")).toBe(true);
    expect(visibleIds.has("i1")).toBe(true);
    expect(visibleIds.has("r1")).toBe(true);
  });
});

describe("AC2 — 展开/合并保持选择、相机与上下文", () => {
  const ctx = {
    viewportCenter: { x: 420, y: 260 },
    zoom: 0.85,
    selectedId: "r1",
    pinnedIds: new Set<string>(["r1"]),
  };

  it("摘要→展开再合并后选择、相机、缩放保持不变", () => {
    const summary = planSummary(fixture, ctx.pinnedIds);
    expect(summary.viewNodes.some((v) => v.kind === "node" && v.node.id === "r1")).toBe(true);

    const afterExpand = preserveViewContext(ctx, { selectedId: "r1" });
    const afterCollapse = preserveViewContext(afterExpand, {});
    expect(afterCollapse.viewportCenter).toEqual({ x: 420, y: 260 });
    expect(afterCollapse.zoom).toBe(0.85);
    expect(afterCollapse.selectedId).toBe("r1");
  });

  it("展开与合并后的可见集合是稳定纯函数（无跳位/重叠的布局前提）", () => {
    const a = planExpanded(fixture);
    const b = planExpanded(fixture);
    expect(a.map((v) => (v.kind === "node" ? v.node.id : v.groupId))).toEqual(
      b.map((v) => (v.kind === "node" ? v.node.id : v.groupId)),
    );
  });
});

describe("AC3 — stale 从输入到祖先的传播与重新整合入口", () => {
  it("c2 superseded 后，i1 → r1 → m1 整条祖先路径受影响且可辨认", () => {
    const paths = computeStalePaths(fixture);
    const byId = new Map(paths.affected.map((p) => [p.nodeId, p.affect]));
    expect(byId.get("c2")).toBe("direct");
    expect(byId.get("i1")).toBe("direct");      // 直接输入 c2 已 stale
    expect(byId.get("r1")).toBe("direct");      // 输入 i1 已 stale
    expect(byId.get("m1")).toBe("direct");      // 输入 r1 已 stale（或 inherited）
    // 受影响路径非空。
    expect(paths.affectedCount).toBeGreaterThanOrEqual(4);
    // 无关分支（r2/i3/c4/c6）不受影响：不被误判为 affected。
    expect(byId.has("r2")).toBe(false);
    expect(byId.has("i3")).toBe(false);
    expect(byId.has("c4")).toBe(false);
  });

  it("受影响路径按 level 升序稳定（UI 由下而上描边）", () => {
    const paths = computeStalePaths(fixture);
    const levels = paths.affected.map((p) => p.level);
    for (let i = 1; i < levels.length; i++) {
      expect(levels[i]!).toBeGreaterThanOrEqual(levels[i - 1]!);
    }
  });

  it("给出最小范围的重新整合入口（不要整图全量重算）", () => {
    const paths = computeStalePaths(fixture);
    expect(paths.reIntegrationTargets.length).toBeGreaterThan(0);
    // 每个目标都指向一个受影响的高层 Insight，并带上其 stale 输入范围。
    for (const t of paths.reIntegrationTargets) {
      expect(t.insightId.length).toBeGreaterThan(0);
      expect(t.staleInputIds.length).toBeGreaterThan(0);
    }
    // 独立受影响分量至少覆盖 m1（最高层）作为一个入口候选。
    const topIds = paths.reIntegrationTargets.map((t) => t.insightId);
    expect(topIds).toContain("m1");
  });
});
