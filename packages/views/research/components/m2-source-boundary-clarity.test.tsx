// @vitest-environment jsdom

/**
 * LRM-1282 — source / human-boundary clarity: purpose-first titles, five-state
 * matrix, expected outcomes from second 0 of loading, auto-fit single column
 * in a 360px drawer, no invented "pending" fact fillers.
 */
import { render, screen } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { describe, expect, it, vi } from "vitest";
import type {
  HumanBoundaryModel,
  SourceStrategyModel,
} from "../lib/m2-visibility";
import { HumanBoundaryCard } from "./human-boundary-card";
import { SourceStrategyStrip } from "./source-strategy-strip";

const M2 = {
  strategy_title: "这次用了哪些信源",
  strategy_label: "信源策略",
  strategy_hint: "让你知道结论参考了什么。",
  strategy_empty_title: "尚未收集到可展示的调研依据",
  strategy_empty_body: "开始调研后会出现来源。",
  strategy_loading: "正在补充调研依据",
  strategy_partial: "已收集部分信息，调研仍在补充",
  strategy_ready_status: "调研依据已整理",
  strategy_ready_live: "调研依据已整理",
  strategy_error: "未能加载调研依据",
  strategy_expect_1: "来源层级：通用参考与领域证据",
  strategy_expect_2: "采用原因：为何采信这些来源",
  strategy_expect_3: "样例链接：可点开核对的原文出处",
  strategy_sample_count: "{{count}} 条样例",
  layer_general: "通用参考",
  layer_domain: "领域证据",
  why_label: "为何查这里：",
  boundary_primary_title: "人和 Agent 各自负责什么",
  boundary_title: "人机边界",
  boundary_hint: "明确 Agent 可协助的工作。",
  boundary_chip: "交付前确认",
  boundary_empty_title: "尚未形成可展示的分工结论",
  boundary_empty_body: "交付阶段会出现分工。",
  boundary_loading: "正在明确分工与约束",
  boundary_partial: "已收集部分信息，调研仍在补充",
  boundary_ready_status: "协作分工已明确",
  boundary_ready_live: "协作分工已明确",
  boundary_error: "未能加载协作分工",
  boundary_expect_1: "Agent 的能力边界",
  boundary_expect_2: "需要人确认的事项",
  boundary_expect_3: "人 / Agent 如何配合",
  boundary_matrix_label: "人 / Agent 如何配合",
  ai_ceiling: "Agent 的能力边界",
  must_human: "需要人确认的事项",
  col_human: "人做",
  col_ai: "AI 做",
};

vi.mock("../../i18n/use-t", () => ({
  useT: () => ({
    t: (
      fn: (dict: Record<string, unknown>) => unknown,
      vars?: Record<string, unknown>,
    ) => {
      const raw = fn({ m2: M2, session_page: { retry: "重试" } });
      if (typeof raw === "string" && vars?.count != null) {
        return raw.replace("{{count}}", String(vars.count));
      }
      return raw;
    },
  }),
}));

const EMPTY_STRATEGY: SourceStrategyModel = {
  chips: [],
  whyLine: "",
  empty: true,
};

const READY_STRATEGY: SourceStrategyModel = {
  chips: [
    {
      id: "docs",
      label: "docs",
      layer: "general",
      why: "权威基线",
      samples: [{ id: "s1", title: "RFC", url: "https://example.com/rfc" }],
    },
    {
      id: "market",
      label: "marketplace",
      layer: "domain",
      samples: [],
    },
  ],
  whyLine: "先通用后领域",
  empty: false,
};

const EMPTY_BOUNDARY: HumanBoundaryModel = {
  aiCeiling: "",
  mustHuman: "",
  matrix: [],
  empty: true,
};

const READY_BOUNDARY: HumanBoundaryModel = {
  aiCeiling: "不能做持牌建议",
  mustHuman: "合规终审",
  matrix: [{ human: "签署", ai: "草稿检索" }],
  empty: false,
};

const PARTIAL_BOUNDARY: HumanBoundaryModel = {
  aiCeiling: "可起草摘要",
  mustHuman: "",
  matrix: [],
  empty: false,
};

describe("SourceStrategyStrip (LRM-1282)", () => {
  it("shows purpose-first title and expected outcomes while loading", () => {
    render(
      <SourceStrategyStrip model={EMPTY_STRATEGY} sessionStatus="running" />,
    );
    expect(
      screen.getByRole("heading", { level: 3, name: M2.strategy_title }),
    ).toBeTruthy();
    expect(screen.getByText(M2.strategy_label)).toBeTruthy();
    expect(screen.getByTestId("source-strategy-expected").textContent).toContain(
      M2.strategy_expect_1,
    );
    expect(screen.queryByTestId("source-strategy-loading")).toBeTruthy();
    // No semantic-less skeleton cards.
    expect(document.querySelector(".animate-pulse")).toBeNull();
  });

  it("keeps real chips in partial mode and does not invent why fillers", () => {
    render(
      <SourceStrategyStrip model={READY_STRATEGY} sessionStatus="running" />,
    );
    expect(screen.getByTestId("source-strategy-status").textContent).toBe(
      M2.strategy_partial,
    );
    expect(screen.getAllByTestId("source-strategy-card")).toHaveLength(2);
    expect(screen.getByText("权威基线")).toBeTruthy();
    // Domain chip without why must not render "pending" filler.
    expect(screen.queryByText(/待补充|pending|生成中/i)).toBeNull();
  });

  it("uses auto-fit card grid so a 360px drawer stays one column", () => {
    const { container } = render(
      <div style={{ width: 360 }}>
        <SourceStrategyStrip model={READY_STRATEGY} sessionStatus="done" />
      </div>,
    );
    const grid = container.querySelector(
      '[data-testid="source-strategy-cards"]',
    ) as HTMLElement;
    expect(grid.className).toContain("minmax(15rem,1fr)");
    expect(grid.className).not.toMatch(/md:grid-cols-3|lg:grid-cols-3/);
  });

  it("surfaces snapshot errors with optional retry that can disable", async () => {
    const onRetry = vi.fn();
    render(
      <SourceStrategyStrip
        model={EMPTY_STRATEGY}
        error="network down"
        onRetry={onRetry}
        retryPending
      />,
    );
    expect(screen.getByRole("alert").textContent).toContain("network down");
    const btn = screen.getByRole("button", { name: "重试" });
    expect(btn).toBeDisabled();
    await userEvent.click(btn);
    expect(onRetry).not.toHaveBeenCalled();
  });
});

describe("HumanBoundaryCard (LRM-1282)", () => {
  it("shows purpose-first title and expected outcomes while loading", () => {
    render(
      <HumanBoundaryCard model={EMPTY_BOUNDARY} sessionStatus="running" />,
    );
    expect(
      screen.getByRole("heading", {
        level: 3,
        name: M2.boundary_primary_title,
      }),
    ).toBeTruthy();
    expect(screen.getByText(M2.boundary_chip)).toBeTruthy();
    expect(screen.getByTestId("human-boundary-expected").textContent).toContain(
      M2.boundary_expect_1,
    );
    expect(document.querySelector(".animate-pulse")).toBeNull();
  });

  it("only renders facts that arrived in partial/ready", () => {
    render(
      <HumanBoundaryCard model={PARTIAL_BOUNDARY} sessionStatus="running" />,
    );
    expect(screen.getByTestId("human-boundary-status").textContent).toBe(
      M2.boundary_partial,
    );
    expect(screen.getAllByTestId("human-boundary-fact")).toHaveLength(1);
    expect(screen.queryByText(/待补充|Pending/i)).toBeNull();
  });

  it("renders matrix with minmax columns and no horizontal overflow class traps", () => {
    render(<HumanBoundaryCard model={READY_BOUNDARY} sessionStatus="done" />);
    const matrix = screen.getByTestId("human-boundary-matrix");
    expect(matrix.querySelector("li")?.className).toContain("minmax(0,1fr)");
    expect(screen.getByText(M2.boundary_matrix_label)).toBeTruthy();
  });

  it("embedded mode never shows loading shell", () => {
    render(
      <HumanBoundaryCard
        model={EMPTY_BOUNDARY}
        sessionStatus="running"
        embedded
      />,
    );
    expect(screen.queryByTestId("human-boundary-loading")).toBeNull();
    expect(screen.getByTestId("human-boundary-empty")).toBeTruthy();
  });
});
