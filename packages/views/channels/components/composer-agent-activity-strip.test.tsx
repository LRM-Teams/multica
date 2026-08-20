import type { ReactElement } from "react";
import { render, screen } from "@testing-library/react";
import { beforeEach, describe, expect, it, vi } from "vitest";
import { I18nProvider } from "@multica/core/i18n/react";
import enChannels from "../../locales/en/channels.json";
import { ComposerAgentActivityStrip } from "./composer-agent-activity-strip";

const TEST_RESOURCES = { en: { channels: enChannels } };

function renderStrip(ui: ReactElement) {
  return render(
    <I18nProvider resources={TEST_RESOURCES} locale="en">
      {ui}
    </I18nProvider>,
  );
}

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

    renderStrip(<ComposerAgentActivityStrip agentId="agent-1" />);

    const strip = screen.getByTestId("composer-agent-activity-strip");
    expect(strip).toHaveTextContent("Thinking...");
    expect(screen.queryByText(/Working|Idle/i)).toBeNull();
  });

  it("hides when idle / no observation (no empty chrome)", () => {
    const { container } = renderStrip(<ComposerAgentActivityStrip agentId="agent-1" />);
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
    const { container } = renderStrip(<ComposerAgentActivityStrip agentId="agent-1" />);
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

    renderStrip(
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
  it("merges same-verb agents onto one line and collapses the tail", () => {
    const verbs: Array<[string, string, string]> = [
      ["leo", "Running command...", "info"],
      ["owen", "Running command...", "info"],
      ["里维", "Thinking...", "active"],
      ["阿泰", "Thinking...", "active"],
      ["dante", "Reading history...", "info"],
      ["kevin", "Checking messages...", "info"],
      ["Wendy", "Sending message...", "info"],
    ];
    summariesState.items = verbs.map(([name, label, tone]) => ({
      agent_id: name,
      summary: { label, tone, visibility: "visible" },
    }));

    renderStrip(
      <ComposerAgentActivityStrip
        agents={verbs.map(([name]) => ({ agentId: name, name }))}
      />,
    );

    const rows = screen.getAllByTestId("composer-agent-activity-row");
    expect(rows).toHaveLength(2);
    expect(rows[0]).toHaveTextContent("里维, 阿泰 Thinking...");
    expect(rows[1]).toHaveTextContent("leo, owen Running command...");
    expect(screen.getByTestId("composer-agent-activity-more")).toHaveTextContent("3");
  });

  it("has no overflow tail when every verb fits", () => {
    summariesState.items = [
      {
        agent_id: "agent-think",
        summary: { label: "Thinking...", tone: "active", visibility: "visible" },
      },
    ];

    renderStrip(
      <ComposerAgentActivityStrip
        agents={[
          { agentId: "agent-think", name: "Thinker" },
          { agentId: "agent-quiet", name: "Quiet" },
        ]}
      />,
    );

    expect(screen.queryByTestId("composer-agent-activity-more")).toBeNull();
  });
});
