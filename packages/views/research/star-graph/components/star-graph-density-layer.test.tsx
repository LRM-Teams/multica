// @vitest-environment jsdom
import { render, screen } from "@testing-library/react";
import { describe, expect, it } from "vitest";
import {
  dominantExecutionStatus,
  StarGraphDensityLayer,
} from "./star-graph-density-layer";

const bins = [
  {
    id: "branch-a:running",
    bounds: { x: 20, y: 30, width: 180, height: 120 },
    total: 24,
    executionCounts: { running: 18, pending: 6 },
  },
];

describe("StarGraphDensityLayer", () => {
  it("renders projection bins only in the far semantic zoom", () => {
    const { rerender } = render(
      <StarGraphDensityLayer bins={bins} zoom={0.4} />,
    );
    expect(screen.getByTestId("star-graph-density-branch-a:running")).toBeTruthy();
    rerender(<StarGraphDensityLayer bins={bins} zoom={0.8} />);
    expect(screen.queryByTestId("star-graph-density-layer")).toBeNull();
  });

  it("uses a non-colour execution channel for failure and ties", () => {
    expect(dominantExecutionStatus({ failed: 4, running: 1 })).toBe("failed");
    expect(dominantExecutionStatus({ failed: 2, running: 2 })).toBe("mixed");
  });
});
