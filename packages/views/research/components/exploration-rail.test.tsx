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
