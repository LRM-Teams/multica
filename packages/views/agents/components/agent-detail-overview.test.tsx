// @vitest-environment jsdom

import { cleanup, fireEvent, render, screen } from "@testing-library/react";
import { afterEach, describe, expect, it, vi } from "vitest";
import { I18nProvider } from "@multica/core/i18n/react";
import type { Agent, AgentFleetRank, AgentTask } from "@multica/core/types";
import enAgents from "../../locales/en/agents.json";
import enCommon from "../../locales/en/common.json";

const mockTasks = vi.hoisted(() => ({ current: [] as AgentTask[] }));

vi.mock("@tanstack/react-query", () => ({
  useQuery: () => ({ data: mockTasks.current }),
}));

vi.mock("@multica/core/agents", () => ({
  agentTasksOptions: () => ({ queryKey: ["agent-tasks"] }),
}));

vi.mock("@multica/core/hooks", () => ({
  useWorkspaceId: () => "workspace-1",
}));

vi.mock("../../common/actor-avatar", () => ({
  ActorAvatar: () => <div data-testid="agent-avatar" />,
}));

vi.mock("../../common/actor-identity-row", () => ({
  ActorIdentityRow: () => <span>Agent</span>,
}));

vi.mock("./agent-open-dm-button", () => ({
  AgentOpenDmButton: () => (
    <button type="button" data-testid="agent-open-dm-button">
      Message
    </button>
  ),
}));

import { AgentDetailOverview } from "./agent-detail-overview";

const agent: Agent = {
  id: "agent-1",
  workspace_id: "workspace-1",
  workspace_role: "member",
  runtime_id: "runtime-1",
  name: "agent-1",
  display_name: "Agent",
  description: "",
  instructions: "",
  avatar_url: null,
  runtime_mode: "local",
  runtime_config: {},
  custom_args: [],
  status: "idle",
  max_concurrent_tasks: 1,
  model: "",
  owner_id: "user-1",
  skills: [],
  created_at: "2026-07-13T00:00:00Z",
  updated_at: "2026-07-13T00:00:00Z",
  archived_at: null,
  archived_by: null,
};

const fleet: AgentFleetRank = {
  agent_id: agent.id,
  fleet_score: 68.4,
  class_id: "cruiser",
  class_label: "Cruiser",
  fleet_rank: 3,
  fleet_size: 12,
  sample_tasks: 24,
  sample_sufficient: true,
  frozen: false,
  pillars: {
    delivery: 0.82,
    evolution: 0.48,
    growth: 0.61,
    efficiency: 0.73,
  },
};

function makeTask(status: AgentTask["status"]): AgentTask {
  return {
    id: `task-${status}`,
    agent_id: agent.id,
    runtime_id: agent.runtime_id,
    issue_id: "",
    status,
    priority: 0,
    dispatched_at: null,
    started_at: null,
    completed_at: null,
    result: null,
    error: null,
    created_at: "2026-07-13T00:00:00Z",
    kind: "direct",
    trigger_summary: "Review the API change",
  };
}

function renderOverview(
  task: AgentTask,
  onHonor = vi.fn(),
  fleetRank?: AgentFleetRank,
) {
  mockTasks.current = [task];
  return render(
    <I18nProvider
      locale="en"
      resources={{ en: { agents: enAgents, common: enCommon } }}
    >
      <AgentDetailOverview
        agent={agent}
        runtime={null}
        metric={{ runCount: 1, successRate: null, cost: null }}
        fleet={fleetRank}
        canManage={false}
        onHonor={onHonor}
        onEdit={vi.fn()}
        onDelete={vi.fn()}
      />
    </I18nProvider>,
  );
}

afterEach(() => {
  cleanup();
  mockTasks.current = [];
});

describe("AgentDetailOverview", () => {
  it("opens the complete honor view from the list detail", () => {
    const onHonor = vi.fn();
    renderOverview(makeTask("queued"), onHonor);

    fireEvent.click(screen.getByRole("button", { name: "Honor" }));

    expect(onHonor).toHaveBeenCalledOnce();
  });

  it("focuses and opens Honor from the fleet card surface", () => {
    const onHonor = vi.fn();
    renderOverview(makeTask("queued"), onHonor, fleet);

    const fleetCard = screen.getByRole("button", { name: "Fleet rank · Honor" });
    fleetCard.focus();

    expect(fleetCard).toHaveFocus();

    fireEvent.click(fleetCard);

    expect(onHonor).toHaveBeenCalledOnce();
  });

  it("exposes a Message DM entry on the detail header", () => {
    renderOverview(makeTask("queued"));

    expect(screen.getByTestId("agent-open-dm-button")).toBeInTheDocument();
  });

  it("keeps the existing title rules for ordinary tasks", () => {
    renderOverview(makeTask("queued"));

    expect(screen.getByText("Review the API change")).toBeInTheDocument();
  });
});
