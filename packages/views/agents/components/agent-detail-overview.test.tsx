// @vitest-environment jsdom

import { cleanup, render, screen } from "@testing-library/react";
import { afterEach, describe, expect, it, vi } from "vitest";
import { I18nProvider } from "@multica/core/i18n/react";
import type { Agent, AgentTask } from "@multica/core/types";
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
  ActorIdentityRow: () => <span>Radar agent</span>,
}));

import { AgentDetailOverview } from "./agent-detail-overview";

const agent: Agent = {
  id: "agent-1",
  workspace_id: "workspace-1",
  workspace_role: "member",
  runtime_id: "runtime-1",
  name: "radar-agent",
  display_name: "Radar agent",
  description: "",
  instructions: "",
  avatar_url: null,
  runtime_mode: "local",
  runtime_config: {},
  custom_args: [],
  visibility: "workspace",
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

function radarTask(status: AgentTask["status"]): AgentTask {
  return {
    id: `radar-${status}`,
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
    kind: "agent_radar",
  };
}

function renderOverview(task: AgentTask) {
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
        canManage={false}
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

describe("AgentDetailOverview radar execution log", () => {
  it("labels a queued radar scan without a spinning icon", () => {
    renderOverview(radarTask("queued"));

    const row = screen.getByText("Proactive scan · Queued").closest("li");
    expect(row).not.toBeNull();
    expect(row?.querySelector("svg")).not.toHaveClass("animate-spin");
    expect(screen.queryByText("Task run")).not.toBeInTheDocument();
  });

  it("labels a running radar scan with a spinning icon", () => {
    renderOverview(radarTask("running"));

    const row = screen.getByText("Proactive scan · In progress").closest("li");
    expect(row).not.toBeNull();
    expect(row?.querySelector("svg")).toHaveClass("animate-spin");
  });

  it("labels a dispatched radar scan as in progress", () => {
    renderOverview(radarTask("dispatched"));

    expect(screen.getByText("Proactive scan · In progress")).toBeInTheDocument();
  });

  it("keeps a radar scan waiting on a local directory in the queued state", () => {
    renderOverview(radarTask("waiting_local_directory"));

    const row = screen.getByText("Proactive scan · Queued").closest("li");
    expect(row?.querySelector("svg")).not.toHaveClass("animate-spin");
  });

  it("labels a completed radar scan", () => {
    renderOverview(radarTask("completed"));

    expect(screen.getByText("Proactive scan · Completed")).toBeInTheDocument();
  });

  it("labels a failed radar scan", () => {
    renderOverview(radarTask("failed"));

    expect(screen.getByText("Proactive scan · Failed")).toBeInTheDocument();
  });

  it("labels a cancelled radar scan", () => {
    renderOverview(radarTask("cancelled"));

    expect(screen.getByText("Proactive scan · Cancelled")).toBeInTheDocument();
  });

  it("keeps the existing title rules for ordinary tasks", () => {
    renderOverview({
      ...radarTask("queued"),
      kind: "direct",
      trigger_summary: "Review the API change",
    });

    expect(screen.getByText("Review the API change")).toBeInTheDocument();
    expect(screen.queryByText(/Proactive scan/)).not.toBeInTheDocument();
  });
});
