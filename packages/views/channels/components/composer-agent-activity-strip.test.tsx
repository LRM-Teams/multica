import { render, screen } from "@testing-library/react";
import { beforeEach, describe, expect, it, vi } from "vitest";
import { ComposerAgentActivityStrip } from "./composer-agent-activity-strip";

const projectionState = vi.hoisted(() => ({
  view: null as null | { label: string; textClass: string; dotClass: string },
}));

vi.mock("@multica/core/hooks", () => ({
  useWorkspaceId: () => "ws-1",
}));

vi.mock("../../agents/use-agent-live-status", () => ({
  useAgentActivityProjection: () => projectionState.view,
}));

describe("ComposerAgentActivityStrip", () => {
  beforeEach(() => {
    projectionState.view = null;
  });

  it("renders compact Thinking above the composer when projection is visible", () => {
    projectionState.view = {
      label: "Thinking...",
      textClass: "text-foreground",
      dotClass: "bg-brand",
    };

    render(<ComposerAgentActivityStrip agentId="agent-1" />);

    const strip = screen.getByTestId("composer-agent-activity-strip");
    expect(strip).toHaveTextContent("Thinking...");
    expect(screen.queryByText(/Working|Idle/i)).toBeNull();
  });

  it("hides when idle / no observation (no empty chrome)", () => {
    projectionState.view = null;
    const { container } = render(<ComposerAgentActivityStrip agentId="agent-1" />);
    expect(container).toBeEmptyDOMElement();
    expect(screen.queryByTestId("composer-agent-activity-strip")).toBeNull();
  });

  it("rejects Working as compact label", () => {
    projectionState.view = {
      label: "Working",
      textClass: "text-foreground",
      dotClass: "bg-brand",
    };
    const { container } = render(<ComposerAgentActivityStrip agentId="agent-1" />);
    expect(container).toBeEmptyDOMElement();
  });
});
