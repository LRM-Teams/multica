import { fireEvent, render, screen, within } from "@testing-library/react";
import { describe, expect, it, vi } from "vitest";
import { ExplorationRail } from "./exploration-rail";
import type { ExplorationDimension } from "../lib/m2-visibility";

const M2 = {
  rail_title: "探索轨迹",
  rail_hint: "查看已验证的方向、暂未得到结论的尝试与最终采用的线索",
  rail_error: "暂时无法整理探索轨迹，请稍后重试。",
  rail_error_title: "暂时无法整理探索轨迹",
  rail_error_body: "请稍后重试；不会显示技术错误详情。",
  rail_loading: "正在整理已验证与待补证据的方向",
  rail_loading_hint: "完成后会显示每条方向得到的结果",
  rail_empty_title: "还没有探索轨迹",
  rail_empty_body: "开始调研后，这里会记录验证过的方向。",
  rail_empty_expect_verified: "已验证方向",
  rail_empty_expect_gap: "待补证据的问题",
  rail_empty_expect_reuse: "可复用发现",
  rail_ready_live: "探索轨迹已就绪",
  rail_summary_pending: "仍在验证中",
  rail_summary_verified: "已验证 {{count}} 个方向",
  rail_summary_adopted: "采纳 {{count}} 条发现",
  rail_summary_dead: "{{count}} 条未产出结论",
  rail_summary_joiner: " · ",
  rail_completed_banner: "本次调研已完成",
  rail_completed_directions: "{{count}} 个方向",
  rail_completed_findings: "{{count}} 条发现",
  rail_result_prefix: "结果：",
  rail_result_open: "正在收集证据，得到稳定结论后会显示在这里。",
  rail_result_covered_fallback: "已形成可用结论",
  rail_result_gap: "现有证据不足，暂不纳入最终结论。",
  rail_result_dead: "这条探索暂未得到可用结论。",
  rail_result_dead_reason: "原因：{{reason}}",
  rail_next_expand_covered: "{{count}} 个问题 · 展开查看支撑这一结论的线索",
  rail_next_expand_gap: "{{count}} 个问题 · 展开查看缺少哪类证据",
  rail_next_expand_dead: "{{count}} 个问题 · 可收起此分支",
  rail_question_count: "{{count}} 个问题",
  rail_collapse: "收起",
  required: "必选",
  status: {
    open: "正在验证",
    covered: "已采纳",
    gap: "待补证据",
    dead: "未产出结论",
  },
};

vi.mock("../../i18n/use-t", () => ({
  useT: () => ({
    t: (
      picker: (keys: Record<string, unknown>) => unknown,
      vars?: Record<string, unknown>,
    ) => {
      const keys = { m2: M2, session_page: { retry: "重试" } };
      const out = picker(keys as never);
      if (typeof out === "string" && vars) {
        return out.replace(/\{\{(\w+)\}\}/g, (_, k: string) =>
          String(vars[k] ?? ""),
        );
      }
      return out;
    },
  }),
}));

const sampleDimensions: ExplorationDimension[] = [
  {
    family: "market",
    title: "市场",
    status: "open",
    questions: [{ id: "q1", title: "问题一", nodeType: "question" }],
    findingSummary: "摘要",
  },
];

describe("ExplorationRail accessible name (LRM-1172)", () => {
  it("exposes complementary name via aria-labelledby on empty mode", () => {
    render(<ExplorationRail dimensions={[]} sessionStatus="drafting" />);
    const rail = screen.getByTestId("exploration-rail");
    const labelId = rail.getAttribute("aria-labelledby");
    expect(labelId).toBeTruthy();
    const label = document.getElementById(labelId as string);
    expect(label).not.toBeNull();
    expect(label).toHaveTextContent("探索轨迹");
    expect(screen.getByRole("complementary", { name: "探索轨迹" })).toBe(rail);
  });

  it("keeps the same accessible name across loading / error / ready modes", () => {
    const { rerender } = render(
      <ExplorationRail dimensions={[]} sessionStatus="running" />,
    );
    expect(
      screen.getByRole("complementary", { name: "探索轨迹" }),
    ).toBeTruthy();

    rerender(
      <ExplorationRail
        dimensions={[]}
        sessionStatus="running"
        error="boom"
        onRetry={() => {}}
      />,
    );
    expect(
      screen.getByRole("complementary", { name: "探索轨迹" }),
    ).toBeTruthy();

    rerender(<ExplorationRail dimensions={sampleDimensions} />);
    expect(
      screen.getByRole("complementary", { name: "探索轨迹" }),
    ).toBeTruthy();
  });
});

/**
 * LRM-1252 — pending 摘要曾是 `text-[11px] text-muted-foreground/80`，
 * 落在 `bg-card/90` 上亮色实测 3.93:1（WCAG AA 4.5 FAIL）。
 */
describe("ExplorationRail text contrast (LRM-1252)", () => {
  const pendingDimensions: ExplorationDimension[] = [
    {
      family: "cost",
      title: "成本",
      status: "open",
      questions: [],
    },
  ];

  it("renders the open result with solid muted text", () => {
    render(<ExplorationRail dimensions={pendingDimensions} />);
    const pending = screen.getByText(/正在收集证据/);
    expect(pending.className).toContain("text-muted-foreground");
    expect(pending.className).not.toMatch(/text-muted-foreground\/\d/);
    expect(pending.className).not.toMatch(/\bopacity-\d/);
  });

  it("guard: no text-bearing node uses alpha-dimmed muted text or an opacity ancestor", () => {
    const { container } = render(
      <ExplorationRail dimensions={[...pendingDimensions, ...sampleDimensions]} />,
    );
    const offenders: string[] = [];
    for (const el of container.querySelectorAll<HTMLElement>("*")) {
      const ownText = [...el.childNodes]
        .filter((n) => n.nodeType === 3)
        .map((n) => n.textContent ?? "")
        .join("")
        .trim();
      if (!ownText) continue;
      if (el.closest('[aria-hidden="true"]')) continue;
      const chain: string[] = [];
      let cur: HTMLElement | null = el;
      while (cur && cur !== container) {
        chain.push(cur.className || "");
        cur = cur.parentElement;
      }
      const classes = chain.join(" ");
      if (/text-muted-foreground\/[5-8]\d/.test(classes)) {
        offenders.push(`${ownText}: dimmed muted text (${classes})`);
      }
      if (/\bopacity-\d/.test(classes)) {
        offenders.push(`${ownText}: opacity ancestor (${classes})`);
      }
    }
    expect(offenders).toEqual([]);
  });
});

/** LRM-1287 / LRM-1281 — readable exploration story (no internal codes). */
describe("ExplorationRail readable story (LRM-1287)", () => {
  it("shows purpose hint and empty expected outputs", () => {
    render(<ExplorationRail dimensions={[]} sessionStatus="drafting" />);
    expect(
      screen.getByText("查看已验证的方向、暂未得到结论的尝试与最终采用的线索"),
    ).toBeTruthy();
    const empty = screen.getByTestId("exploration-rail-empty");
    expect(within(empty).getByText("还没有探索轨迹")).toBeTruthy();
    expect(within(empty).getByText("已验证方向")).toBeTruthy();
    expect(within(empty).getByText("待补证据的问题")).toBeTruthy();
    expect(within(empty).getByText("可复用发现")).toBeTruthy();
  });

  it("never renders raw error codes in DOM / aria / title", () => {
    const { container } = render(
      <ExplorationRail
        dimensions={[]}
        error="inbox_task_failed: dimension boom"
        onRetry={() => {}}
      />,
    );
    expect(container.textContent).not.toMatch(/inbox_task_failed/);
    expect(container.textContent).not.toMatch(/dimension boom/);
    expect(screen.getByRole("alert").textContent).toContain(
      "暂时无法整理探索轨迹",
    );
    expect(screen.getByRole("button", { name: "重试" })).toBeTruthy();
    for (const el of container.querySelectorAll("[title],[aria-label]")) {
      const title = el.getAttribute("title") ?? "";
      const label = el.getAttribute("aria-label") ?? "";
      expect(title + label).not.toMatch(/inbox_task_failed/);
    }
  });

  it("maps status badges and never invents branch retry", () => {
    const dims: ExplorationDimension[] = [
      {
        family: "a",
        title: "方向甲",
        status: "covered",
        findingSummary: "可用结论",
        questions: [{ id: "q1", title: "支撑问题", nodeType: "question" }],
      },
      {
        family: "b",
        title: "方向乙",
        status: "dead",
        findingSummary: "证据不支持假设",
        questions: [{ id: "q2", title: "排除问题", nodeType: "question" }],
      },
      {
        family: "c",
        title: "方向丙",
        status: "gap",
        questions: [],
      },
    ];
    render(
      <ExplorationRail
        dimensions={dims}
        selectedFamily="b"
        onSelectFamily={() => {}}
      />,
    );

    expect(screen.getByText("已采纳")).toBeTruthy();
    expect(screen.getByText("未产出结论")).toBeTruthy();
    expect(screen.getByText("待补证据")).toBeTruthy();
    expect(screen.getByText(/已验证 3 个方向/)).toBeTruthy();
    expect(screen.getByText(/采纳 1 条发现/)).toBeTruthy();
    expect(screen.getByText(/1 条未产出结论/)).toBeTruthy();

    expect(screen.queryByText("重试这条探索")).toBeNull();
    expect(
      screen.getByText((_c, el) =>
        Boolean(
          el?.getAttribute("data-testid") === "exploration-result-body" &&
            el.textContent?.includes("这条探索暂未得到可用结论。"),
        ),
      ),
    ).toBeTruthy();
    expect(
      screen.getByText((_c, el) =>
        Boolean(
          el?.getAttribute("data-testid") === "exploration-result-body" &&
            el.textContent?.includes("原因：证据不支持假设"),
        ),
      ),
    ).toBeTruthy();

    const collapse = screen.getByTestId("exploration-rail-collapse");
    expect(collapse).toHaveTextContent("收起");
    fireEvent.click(collapse);
    expect(screen.queryByTestId("exploration-rail-collapse")).toBeNull();
  });

  it("shows completed banner without hiding dead/gap cards", () => {
    const dims: ExplorationDimension[] = [
      {
        family: "a",
        title: "采纳方向",
        status: "covered",
        findingSummary: "结论",
        questions: [],
      },
      {
        family: "b",
        title: "缺口方向",
        status: "gap",
        questions: [],
      },
    ];
    render(
      <ExplorationRail dimensions={dims} sessionStatus="completed" />,
    );
    const summary = screen.getByTestId("exploration-rail-summary");
    expect(within(summary).getByText("本次调研已完成")).toBeTruthy();
    expect(screen.getByText("待补证据")).toBeTruthy();
    expect(screen.getByText("已采纳")).toBeTruthy();
  });
});
