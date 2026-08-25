import type { ReactElement } from "react";
import { render, screen } from "@testing-library/react";
import { beforeEach, describe, expect, it, vi } from "vitest";
import { I18nProvider } from "@multica/core/i18n/react";
import enChannels from "../../locales/en/channels.json";
import { ComposerAgentActivityStrip } from "./composer-agent-activity-strip";

const state = vi.hoisted(() => ({ items: [] as Array<{ agent_id: string; summary: { label: string; activityKind: string; detailKind: string } }> }));
vi.mock("@multica/core/hooks", () => ({ useWorkspaceId: () => "ws-1" }));
vi.mock("@multica/core/agents", () => ({ useRunnerActivitySummaries: () => ({ data: { items: state.items } }) }));
function renderStrip(ui: ReactElement) {
  return render(<I18nProvider resources={{ en: { channels: enChannels } }} locale="en">{ui}</I18nProvider>);
}
const summary = (agent_id: string, label: string, activityKind: string, detailKind: string) => ({ agent_id, summary: { label, activityKind, detailKind } });

describe("ComposerAgentActivityStrip", () => {
  beforeEach(() => { state.items = []; });

  it("renders a compact fact-projected verb and hides presence-only facts", () => {
    state.items = [summary("think", "Thinking...", "thinking", "thinking_started"), summary("online", "Online", "online", "idle")];
    renderStrip(<ComposerAgentActivityStrip agents={[{ agentId: "online", name: "OnlineBot" }, { agentId: "think", name: "Thinker" }]} />);
    expect(screen.getByTestId("composer-agent-activity-strip")).toHaveTextContent("Thinker Thinking...");
    expect(screen.queryByText(/OnlineBot/)).toBeNull();
  });

  it("renders no empty chrome without live facts", () => {
    const { container } = renderStrip(<ComposerAgentActivityStrip agentId="agent-1" />);
    expect(container).toBeEmptyDOMElement();
  });

  it("sorts and groups verbs, then collapses overflow", () => {
    state.items = [
      summary("r1", "Running command...", "working", "running_command"),
      summary("r2", "Running command...", "working", "running_command"),
      summary("t1", "Thinking...", "thinking", "thinking_started"),
      summary("t2", "Thinking...", "thinking", "thinking_started"),
      summary("read", "Reading history...", "working", "reading_history"),
      summary("send", "Sending message...", "working", "sending_message"),
      summary("check", "Checking messages...", "working", "checking_messages"),
    ];
    renderStrip(<ComposerAgentActivityStrip agents={state.items.map((item) => ({ agentId: item.agent_id, name: item.agent_id }))} />);
    const rows = screen.getAllByTestId("composer-agent-activity-row");
    expect(rows).toHaveLength(2);
    expect(rows[0]).toHaveTextContent("t1, t2 Thinking...");
    expect(rows[1]).toHaveTextContent("r1, r2 Running command...");
    expect(screen.getByTestId("composer-agent-activity-more")).toHaveTextContent("3");
  });
});
