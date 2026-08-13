import { render, screen } from "@testing-library/react";
import { describe, expect, it, vi } from "vitest";
import { ResearchRunGateBlockers } from "./research-run-gate-blockers";

describe("ResearchRunGateBlockers accessible name", () => {
  it("uses the localized visible title in normal and degraded modes", () => {
    const props = {
      blockers: [],
      onLocate: vi.fn(),
      title: "交付阻断项",
      degradedTitle: "阻断状态暂不可用",
      degradedBody: "请稍后重试。",
    };
    const { rerender } = render(
      <ResearchRunGateBlockers {...props} degraded />,
    );
    expect(screen.getByTestId("research-run-gates")).toHaveAccessibleName(
      "阻断状态暂不可用",
    );

    rerender(
      <ResearchRunGateBlockers
        {...props}
        degraded={false}
        blockers={[{ id: "g1", label: "缺少来源", targetNodeId: "n1" }]}
      />,
    );
    expect(screen.getByTestId("research-run-gates")).toHaveAccessibleName(
      "交付阻断项",
    );
  });
});
