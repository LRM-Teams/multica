import { render, screen } from "@testing-library/react";
import { describe, expect, it, vi } from "vitest";
import type { AgentTask } from "@multica/core/types";
import { ConversationAgentActivityLine } from "./conversation-agent-activity-line";

const snapshotMock = vi.fn<() => AgentTask[]>();
const liveStatusMock =
  vi.fn<() => { label: string; textClass: string; dotClass: string } | null>();

vi.mock("@multica/core/hooks", () => ({ useWorkspaceId: () => "ws-1" }));
vi.mock("@multica/core/agents", () => ({
  agentTaskSnapshotOptions: () => ({ queryKey: ["snapshot"], queryFn: async () => [] }),
}));
vi.mock("@tanstack/react-query", () => ({
  useQuery: () => ({ data: snapshotMock() }),
}));
vi.mock("../use-agent-live-status", () => ({
  useAgentLiveStatus: () => liveStatusMock(),
}));

function runningTask(agentId: string): AgentTask {
  // Only the fields pickPrimaryActiveTask reads matter; cast the rest.
  return {
    id: "task-1",
    agent_id: agentId,
    status: "running",
    started_at: "2026-07-17T10:00:00Z",
    dispatched_at: null,
    created_at: "2026-07-17T09:59:00Z",
  } as unknown as AgentTask;
}

describe("ConversationAgentActivityLine", () => {
  it("renders the live activity mark while the agent has an active task", () => {
    snapshotMock.mockReturnValue([runningTask("agent-1")]);
    liveStatusMock.mockReturnValue({
      label: "Running command…",
      textClass: "text-foreground",
      dotClass: "bg-muted-foreground/40",
    });

    render(<ConversationAgentActivityLine agentId="agent-1" />);

    const line = screen.getByTestId("conversation-agent-activity-line");
    expect(line).toHaveTextContent("Running command…");
    expect(screen.getByTestId("agent-live-status")).toBeInTheDocument();
  });

  it("hides entirely when the agent is idle (no active task)", () => {
    snapshotMock.mockReturnValue([]);
    // Idle agents still resolve to a presence word ("Idle") — the line must NOT
    // fall back to it; no active task means no line at all.
    liveStatusMock.mockReturnValue({
      label: "Idle",
      textClass: "text-muted-foreground",
      dotClass: "bg-muted-foreground/40",
    });

    render(<ConversationAgentActivityLine agentId="agent-1" />);

    expect(screen.queryByTestId("conversation-agent-activity-line")).toBeNull();
    expect(screen.queryByTestId("agent-live-status")).toBeNull();
  });

  it("hides while the status is still resolving even if a task is active", () => {
    snapshotMock.mockReturnValue([runningTask("agent-1")]);
    liveStatusMock.mockReturnValue(null);

    render(<ConversationAgentActivityLine agentId="agent-1" />);

    expect(screen.queryByTestId("conversation-agent-activity-line")).toBeNull();
  });

  it("hides the Activity Output label above the composer (LRM-202)", () => {
    snapshotMock.mockReturnValue([runningTask("agent-1")]);
    liveStatusMock.mockReturnValue({
      label: "Output",
      textClass: "text-foreground",
      dotClass: "bg-muted-foreground/40",
    });

    render(<ConversationAgentActivityLine agentId="agent-1" />);

    expect(screen.queryByTestId("conversation-agent-activity-line")).toBeNull();
    expect(screen.queryByText("Output")).toBeNull();
  });
});
