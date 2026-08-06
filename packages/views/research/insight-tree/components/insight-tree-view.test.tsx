import { describe, expect, it, vi } from "vitest";
import { fireEvent, render, screen, within } from "@testing-library/react";
import { insightDerivationFixture } from "../insight-derivation-fixture";
import { InsightTreeView } from "./insight-tree-view";
import { countExpandedVisible } from "./insight-tree-visibility";

const fixture = insightDerivationFixture;

function countCards(container: HTMLElement): number {
  return container.querySelectorAll('[data-testid="insight-compound-card"]').length;
}

describe("AC1 — 三层 DAG 逐层展开/折叠，折叠后 DOM 节点数显著下降", () => {
  it("展开态（展开全部）呈现完整 ≥3 层 DAG：12 个节点全部在 DOM", () => {
    const allExpanded = new Set(fixture.map((n) => n.id));
    const { container } = render(
      <InsightTreeView
        nodes={fixture}
        initialMode="expanded"
        initialExpanded={allExpanded}
      />,
    );
    expect(fixture.length).toBe(12);
    expect(countExpandedVisible(fixture, allExpanded)).toBe(12);
    expect(countCards(container)).toBe(12);
  });

  it("折叠顶部节点后 DOM 节点数显著下降（子树整体卸载）", () => {
    const allExpanded = new Set(fixture.map((n) => n.id));
    const { container, getByTestId } = render(
      <InsightTreeView
        nodes={fixture}
        initialMode="expanded"
        initialExpanded={allExpanded}
      />,
    );
    const fullCount = countCards(container);
    expect(fullCount).toBe(12);

    // 折叠 M1（顶层汇点）→ 其下 r1/r2/i1..i3/c1..c6 整棵子树卸载；
    // 仅剩顶层汇点 m1 与孤叶 c5（c5 未被任何输入引用，是另一汇点）两个根卡。
    const m1Card = container.querySelector('[data-node-id="m1"]')!;
    fireEvent.click(within(m1Card as HTMLElement).getByTestId("expand-toggle"));
    const collapsedCount = countCards(container);
    expect(collapsedCount).toBe(2);
    // 显著下降：至少省 40%。
    expect(collapsedCount).toBeLessThanOrEqual(Math.round(fullCount * 0.6));
    expect(getByTestId("visible-count").textContent).toContain(String(collapsedCount));
  });

  it("折叠全部后仅保留顶层结论，DOM 显著低于展开态", () => {
    const allExpanded = new Set(fixture.map((n) => n.id));
    const { container, getByTestId } = render(
      <InsightTreeView
        nodes={fixture}
        initialMode="expanded"
        initialExpanded={allExpanded}
      />,
    );
    fireEvent.click(getByTestId("collapse-all"));
    // 仅顶层汇点 m1 与孤叶 c5 保留（无展开 → 不渲染其它节点）。
    expect(countCards(container)).toBe(2);
  });

  it("摘要模式把子树折叠成显示分组，DOM 不高于展开态（无重复渲染）", () => {
    const allExpanded = new Set(fixture.map((n) => n.id));
    const { container } = render(
      <InsightTreeView
        nodes={fixture}
        initialMode="summary"
        initialExpanded={allExpanded}
      />,
    );
    const summaryCards = countCards(container);
    const expandedCount = countExpandedVisible(fixture, allExpanded);
    // 摘要模式不逐层递归重复渲染：卡数 ≤ 展开态节点数。
    expect(summaryCards).toBeLessThanOrEqual(expandedCount);
    // 摘要态含显示分组卡。
    expect(container.querySelector('[data-testid="display-group-card"]')).toBeTruthy();
  });

  it("点击显示分组可展开其成员", () => {
    const { container } = render(<InsightTreeView nodes={fixture} initialMode="summary" />);
    const before = countCards(container);
    const group = container.querySelector('[data-testid="display-group-card"]')!;
    fireEvent.click(group);
    const after = countCards(container);
    expect(after).toBeGreaterThan(before);
  });
});

describe("AC2 — 选中、viewport 上下文与 pinned 在切换/折叠前后保持", () => {
  const viewport = { viewportCenter: { x: 420, y: 260 }, zoom: 0.85 };

  it("摘要↔展开切换保持选中、相机中心、缩放、pinned", () => {
    const { container, getByTestId } = render(
      <InsightTreeView
        nodes={fixture}
        initialMode="summary"
        initialSelectedId="r1"
        initialViewport={viewport}
      />,
    );
    // 先选 r1
    fireEvent.click(container.querySelector('[data-node-id="r1"]')!);

    const breadcrumbHas = (id: string) =>
      !!container.querySelector(`[data-breadcrumb-id="${id}"]`);

    // 记录切换前的相机上下文
    expect(getByTestId("viewport-context").textContent).toContain("0.85");
    expect(breadcrumbHas("r1")).toBe(true);

    // 切到展开
    fireEvent.click(getByTestId("mode-toggle"));
    expect(getByTestId("viewport-context").textContent).toContain("0.85");
    // 选择经 preserveViewContext 原样保留（面包屑仍反映 r1）
    expect(breadcrumbHas("r1")).toBe(true);

    // 切回摘要
    fireEvent.click(getByTestId("mode-toggle"));
    expect(getByTestId("viewport-context").textContent).toContain("0.85");
    expect(breadcrumbHas("r1")).toBe(true);
    // pinned 路径（r1 → i1 → c2）在摘要态仍可见
    expect(container.querySelector('[data-node-id="r1"]')).toBeTruthy();
    expect(container.querySelector('[data-node-id="i1"]')).toBeTruthy();
    expect(container.querySelector('[data-node-id="c2"]')).toBeTruthy();
  });

  it("折叠某节点不改变选中与相机上下文", () => {
    const allExpanded = new Set(fixture.map((n) => n.id));
    const { container, getByTestId } = render(
      <InsightTreeView
        nodes={fixture}
        initialMode="expanded"
        initialExpanded={allExpanded}
        initialSelectedId="m1"
        initialViewport={viewport}
      />,
    );
    const beforeViewport = getByTestId("viewport-context").textContent;
    const m1 = container.querySelector('[data-node-id="m1"]')!;
    fireEvent.click(within(m1 as HTMLElement).getByTestId("expand-toggle"));
    expect(getByTestId("viewport-context").textContent).toBe(beforeViewport);
    expect(container.querySelector('[data-breadcrumb-id="m1"]')).toBeTruthy();
  });

  it("选择变化后面包屑随层级更新", () => {
    const { container } = render(<InsightTreeView nodes={fixture} initialMode="expanded" />);
    fireEvent.click(container.querySelector('[data-node-id="m1"]')!); // 选择（非展开）
    expect(container.querySelector('[data-breadcrumb-id="m1"]')).toBeTruthy();
  });
});

describe("AC3 — direct/inherited stale 路径与最小重新整合入口", () => {
  it("受影响路径卡按 direct/inherited 标记，无关分支不受影响", () => {
    const allExpanded = new Set(fixture.map((n) => n.id));
    const { container } = render(
      <InsightTreeView
        nodes={fixture}
        initialMode="expanded"
        initialExpanded={allExpanded}
      />,
    );
    const affect = (id: string) =>
      container.querySelector(`[data-node-id="${id}"]`)?.getAttribute("data-affect");
    expect(affect("c2")).toBe("direct");
    // 无关分支不误判为受影响
    expect(container.querySelector('[data-node-id="r2"]')?.getAttribute("data-stale")).toBe("false");
    expect(container.querySelector('[data-node-id="i3"]')?.getAttribute("data-stale")).toBe("false");
    // 受影响路径摘要角标非空
    expect(screen.getByTestId("stale-path-summary").textContent).toMatch(/\d+/);
  });

  it("最小重新整合入口只出现在受影响分量，点击触发 onReintegrate", () => {
    const onReintegrate = vi.fn();
    const { container } = render(
      <InsightTreeView nodes={fixture} initialMode="summary" onReintegrate={onReintegrate} />,
    );
    const targets = container.querySelectorAll('[data-testid="reintegration-target"]');
    expect(targets.length).toBeGreaterThan(0);
    // 入口指向受影响高层 Insight
    const topInsight = targets[0]?.getAttribute("data-insight-id");
    expect(topInsight).toBeTruthy();
    fireEvent.click(
      within(targets[0] as HTMLElement).getByTestId("reintegrate-button"),
    );
    expect(onReintegrate).toHaveBeenCalledTimes(1);
  });
});

describe("摘要/展开切换控件", () => {
  it("mode-toggle 在摘要/展开间切换并标记 aria-pressed", () => {
    const { getByTestId } = render(<InsightTreeView nodes={fixture} initialMode="summary" />);
    const toggle = getByTestId("mode-toggle");
    expect(toggle.getAttribute("aria-pressed")).toBe("false");
    fireEvent.click(toggle);
    expect(toggle.getAttribute("aria-pressed")).toBe("true");
  });
});
