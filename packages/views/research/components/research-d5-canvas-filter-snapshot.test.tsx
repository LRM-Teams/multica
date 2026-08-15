import { render, screen } from "@testing-library/react";
import { beforeEach, describe, expect, it, vi } from "vitest";
import { useResearchCanvasStore } from "@multica/core/research";
import { ResearchD5CanvasFilter } from "./research-d5-canvas-filter";

vi.mock("../../i18n/use-t", () => ({
  useT: () => ({
    t: () => "Filter",
  }),
}));

describe("ResearchD5CanvasFilter store snapshot", () => {
  beforeEach(() => {
    useResearchCanvasStore.setState({ filterBySession: {} });
  });

  it("renders a session that has no persisted filter without an update loop", () => {
    render(
      <ResearchD5CanvasFilter
        sessionId="new-session"
        options={{ statuses: [], tiers: [], rounds: [], clusters: [] }}
      />,
    );

    expect(screen.getByTestId("research-d5-filter-trigger")).toBeTruthy();
  });
});
