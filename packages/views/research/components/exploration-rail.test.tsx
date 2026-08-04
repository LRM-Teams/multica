import { render, screen } from "@testing-library/react";
import { describe, expect, it, vi } from "vitest";
import { ExplorationRail } from "./exploration-rail";
import type { ExplorationDimension } from "../lib/m2-visibility";

vi.mock("../../i18n/use-t", () => ({
  useT: () => ({
    t: (picker: (keys: Record<string, unknown>) => unknown) => {
      const keys = {
        m2: {
          rail_title: "探索轨迹",
          rail_hint: "hint",
          rail_error: "error",
          rail_loading: "loading",
          rail_empty_title: "empty",
          rail_empty_body: "empty body",
          rail_summary_pending: "pending",
          rail_question_count: "1",
          required: "必选",
          status: {
            open: "开放",
            covered: "已覆盖",
            gap: "缺口",
            dead: "终止",
          },
        },
        session_page: { retry: "重试" },
      };
      return picker(keys as never);
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

  it("renders the pending summary with solid muted text", () => {
    render(<ExplorationRail dimensions={pendingDimensions} />);
    const pending = screen.getByText("pending");
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
