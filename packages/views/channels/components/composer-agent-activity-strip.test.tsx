import { render, screen } from "@testing-library/react";
import { beforeEach, describe, expect, it, vi } from "vitest";
import { ComposerAgentActivityStrip } from "./composer-agent-activity-strip";

const summariesState = vi.hoisted(() => ({
  items: [] as Array<{
    agent_id: string;
    summary: { label: string; tone: string; visibility: string };
  }>,
}));

vi.mock("@multica/core/hooks", () => ({
  useWorkspaceId: () => "ws-1",
}));

vi.mock("@multica/core/agents", () => ({
  useRunnerActivitySummaries: () => ({ data: { items: summariesState.items } }),
}));

describe("ComposerAgentActivityStrip", () => {
  beforeEach(() => {
    summariesState.items = [];
  });

  it("renders compact Thinking above the composer when projection is visible", () => {
    summariesState.items = [
      {
        agent_id: "agent-1",
        summary: { label: "Thinking...", tone: "active", visibility: "visible" },
      },
    ];

    render(<ComposerAgentActivityStrip agentId="agent-1" />);

    const strip = screen.getByTestId("composer-agent-activity-strip");
    expect(strip).toHaveTextContent("Thinking...");
    expect(screen.queryByText(/Working|Idle/i)).toBeNull();
  });

  it("hides when idle / no observation (no empty chrome)", () => {
    const { container } = render(<ComposerAgentActivityStrip agentId="agent-1" />);
    expect(container).toBeEmptyDOMElement();
    expect(screen.queryByTestId("composer-agent-activity-strip")).toBeNull();
  });

  it("rejects Working as compact label", () => {
    summariesState.items = [
      {
        agent_id: "agent-1",
        summary: { label: "Working", tone: "active", visibility: "visible" },
      },
    ];
    const { container } = render(<ComposerAgentActivityStrip agentId="agent-1" />);
    expect(container).toBeEmptyDOMElement();
  });

  it("lists group agents with names and only the currently live verbs", () => {
    summariesState.items = [
      {
        agent_id: "agent-online",
        summary: { label: "Online", tone: "success", visibility: "visible" },
      },
      {
        agent_id: "agent-run",
        summary: { label: "Running command...", tone: "info", visibility: "visible" },
      },
      {
        agent_id: "agent-think",
        summary: { label: "Thinking...", tone: "active", visibility: "visible" },
      },
    ];

    render(
      <ComposerAgentActivityStrip
        agents={[
          { agentId: "agent-online", name: "OnlineBot" },
          { agentId: "agent-run", name: "Runner" },
          { agentId: "agent-think", name: "Thinker" },
        ]}
      />,
    );

    const rows = screen.getAllByTestId("composer-agent-activity-row");
    expect(rows).toHaveLength(2);
    expect(rows[0]).toHaveTextContent("Thinker Thinking...");
    expect(rows[1]).toHaveTextContent("Runner Running command...");
    expect(screen.queryByText(/OnlineBot/)).toBeNull();
  });
});
