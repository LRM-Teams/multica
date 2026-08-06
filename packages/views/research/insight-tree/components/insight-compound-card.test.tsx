import { describe, expect, it, vi } from "vitest";
import { fireEvent, render, screen } from "@testing-library/react";
import { InsightCompoundCard } from "./insight-compound-card";
import { insightDerivationFixture } from "../insight-derivation-fixture";

const m1 = insightDerivationFixture.find((n) => n.id === "m1")!;
const c2 = insightDerivationFixture.find((n) => n.id === "c2")!;

describe("InsightCompoundCard（LRM-1476）", () => {
  it("渲染层级徽标、输入数量、证据覆盖、结论文案", () => {
    render(
      <InsightCompoundCard
        node={m1}
        expandable
        labels={{ inputsLabel: "输入", evidenceLabel: "证据覆盖" }}
      />,
    );
    // Level 3 insight
    expect(screen.getByTestId("level-badge").textContent).toContain("Level 3");
    expect(screen.getByTestId("input-count").textContent).toContain("2");
    expect(screen.getByTestId("evidence-coverage").textContent).toContain("10 输入");
    expect(screen.getByTestId("conclusion").textContent).toContain("跨市场选型决策");
  });

  it("fresh Claim 使用 success 语义，不显示失效徽标", () => {
    const { container } = render(<InsightCompoundCard node={c2} />);
    // c2 是 stale fixture，但本卡只读传入的事实；这里不传 stale → 视为未受影响。
    expect(container.querySelector('[data-testid="stale-badge"]')).toBeNull();
  });

  it("stale（direct）显示失效徽标并标注 direct", () => {
    const { container } = render(
      <InsightCompoundCard node={m1} stale={{ stale: true, affect: "direct", reason: "input_superseded" }} />,
    );
    const badge = container.querySelector('[data-testid="stale-badge"]')!;
    expect(badge).toBeTruthy();
    expect(badge.getAttribute("data-affect")).toBe("direct");
  });

  it("inherited 失效与 direct 可分辨", () => {
    const { container } = render(
      <InsightCompoundCard node={m1} stale={{ stale: true, affect: "inherited" }} />,
    );
    const badge = container.querySelector('[data-testid="stale-badge"]')!;
    expect(badge.getAttribute("data-affect")).toBe("inherited");
  });

  it("展开开关触发 onToggleExpand，且不触发 onSelect", () => {
    const onToggle = vi.fn();
    const onSelect = vi.fn();
    render(
      <InsightCompoundCard
        node={m1}
        expandable
        onToggleExpand={onToggle}
        onSelect={onSelect}
      />,
    );
    fireEvent.click(screen.getByTestId("expand-toggle"));
    expect(onToggle).toHaveBeenCalledTimes(1);
    expect(onSelect).not.toHaveBeenCalled();
  });

  it("卡主体点击触发 onSelect", () => {
    const onSelect = vi.fn();
    render(<InsightCompoundCard node={c2} onSelect={onSelect} />);
    fireEvent.click(screen.getByTestId("insight-compound-card"));
    expect(onSelect).toHaveBeenCalledTimes(1);
  });

  it("Claim（level 0）不渲染展开开关，即使 expandable", () => {
    const { container } = render(<InsightCompoundCard node={c2} expandable />);
    expect(container.querySelector('[data-testid="expand-toggle"]')).toBeNull();
  });
});
