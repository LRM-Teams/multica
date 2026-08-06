/**
 * LRM-1475 — NodeCardShell: structure, semantic-token-only classes, zoom
 * density (AC3: 40% / 100% / 160%) and the clickable-card rule.
 */
import { describe, expect, it, vi } from "vitest";
import { fireEvent, render, screen } from "@testing-library/react";
import { NodeCardShell } from "./node-card-shell";

function shell(overrides: Partial<Parameters<typeof NodeCardShell>[0]> = {}) {
  return (
    <NodeCardShell
      family="evidence"
      state="default"
      title="衍射实验"
      typeLabel="观测"
      summary="实验观测摘要"
      importance={2}
      {...overrides}
    />
  );
}

describe("NodeCardShell — base structure", () => {
  it("renders accent bar, kind icon, type badge, title, summary, state badge", () => {
    const { container } = render(shell());
    expect(container.querySelector('[data-testid="node-accent-bar"]')).toBeTruthy();
    expect(container.querySelector('[data-testid="node-kind-icon"]')).toBeTruthy();
    expect(screen.getByTestId("node-type-badge")).toHaveTextContent("观测");
    expect(screen.getByTestId("node-title")).toHaveTextContent("衍射实验");
    expect(screen.getByTestId("node-summary")).toHaveTextContent("实验观测摘要");
    expect(screen.getByTestId("node-state-badge")).toBeTruthy();
  });

  it("renders importance stars as fill/half/outline (not color-only)", () => {
    const { container } = render(shell({ importance: 3 }));
    const stars = container.querySelectorAll('[data-testid="node-importance"] svg');
    expect(stars.length).toBe(3);
    for (const s of stars) {
      expect(s.classList.contains("fill-warning")).toBe(true);
    }
  });

  it("whole card is a clickable button when onOpen is provided", () => {
    const onOpen = vi.fn();
    const { container } = render(shell({ onOpen }));
    const card = container.querySelector('[data-testid="node-card"]');
    expect(card?.getAttribute("role")).toBe("button");
    fireEvent.click(card as HTMLElement);
    expect(onOpen).toHaveBeenCalledTimes(1);
  });
});

describe("NodeCardShell — zoom density (AC3)", () => {
  it("all families use semantic tokens only (no hex / palette-*-500)", () => {
    for (const family of ["structure", "execution", "evidence", "cognition", "collaboration", "governance", "generic"] as const) {
      const { container } = render(shell({ family }));
      const card = container.querySelector('[data-testid="node-card"]');
      const cls = card?.getAttribute("class") ?? "";
      expect(cls).not.toMatch(/#[0-9a-f]{3,6}/i);
      for (const rail of ["sky", "emerald", "amber", "violet", "cyan", "indigo"]) {
        expect(cls.toLowerCase()).not.toContain(rail);
      }
    }
  });

  it("40% zoom hides summary, importance and footer (degraded layout)", () => {
    const { container } = render(shell({ zoom: 0.4, meta: <span>FOOTER</span> }));
    expect(container.querySelector('[data-testid="node-summary"]')).toBeNull();
    expect(container.querySelector('[data-testid="node-importance"]')).toBeNull();
    expect(container.querySelector('[data-testid="node-chevron"]')).toBeNull();
    // kind badge + title still present (legible at low zoom)
    expect(screen.getByTestId("node-type-badge")).toBeTruthy();
    expect(screen.getByTestId("node-title")).toBeTruthy();
  });

  it("100% zoom shows summary + footer meta, no expanded meta", () => {
    const { container } = render(shell({ zoom: 1, meta: <span>META</span> }));
    expect(container.querySelector('[data-testid="node-summary"]')).toBeTruthy();
    expect(screen.getByText("META")).toBeTruthy();
    expect(container.querySelector('[data-testid="node-expanded-meta"]')).toBeNull();
  });

  it("160% zoom additionally expands detail meta (with legend)", () => {
    const { container } = render(shell({ zoom: 1.6, legend: <span>LEGEND</span> }));
    expect(container.querySelector('[data-testid="node-expanded-meta"]')).toBeTruthy();
    expect(container.querySelector('[data-testid="node-legend"]')).toBeTruthy();
  });
});

describe("NodeCardShell — card-face rows (owner/objective/action/counts)", () => {
  it("renders owner, objective and current action as separate rows", () => {
    const { container } = render(
      shell({
        owner: "张三",
        objective: "核验 3 个来源",
        currentAction: "正在执行 · 检索",
      }),
    );
    expect(container.querySelector('[data-testid="node-owner"]')).toBeTruthy();
    expect(container.querySelector('[data-testid="node-objective"]')).toBeTruthy();
    expect(container.querySelector('[data-testid="node-current-action"]')).toBeTruthy();
    expect(screen.getByText("核验 3 个来源")).toBeTruthy();
  });

  it("renders the counts row with resolved/progress/risk chips", () => {
    const { container } = render(
      shell({ resolvedCount: 2, progressCount: 1, riskCount: 1 }),
    );
    expect(container.querySelector('[data-testid="node-progress-counts"]')).toBeTruthy();
  });

  it("renders neutral 暂无新进展 when all counts are zero/absent", () => {
    const { container } = render(shell({ resolvedCount: 0, progressCount: 0, riskCount: 0 }));
    expect(container.querySelector('[data-testid="node-progress-none"]')).toBeTruthy();
  });

  it("hides card-face rows at 40% zoom (degraded density)", () => {
    const { container } = render(
      shell({ zoom: 0.4, owner: "张三", objective: "目标", currentAction: "动作" }),
    );
    expect(container.querySelector('[data-testid="node-owner"]')).toBeNull();
    expect(container.querySelector('[data-testid="node-objective"]')).toBeNull();
    expect(container.querySelector('[data-testid="node-current-action"]')).toBeNull();
  });
});
