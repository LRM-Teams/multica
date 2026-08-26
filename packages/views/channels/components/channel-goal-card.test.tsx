// @vitest-environment jsdom

import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { screen, waitFor } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { beforeEach, describe, expect, it, vi } from "vitest";
import type { ChannelGoal, WorkGraphDetail, WorkGraphNode } from "@multica/core/types";
import { renderWithI18n } from "../../test/i18n";
import { ChannelGoalCard } from "./channel-goal-card";

const goal: ChannelGoal = {
  id: "goal-1",
  workspace_id: "workspace-1",
  channel_id: "channel-1",
  title: "Ship the goal graph",
  objective: "Show live execution progress",
  success_criteria: ["Graph is visible"],
  status: "active",
  version: 1,
  progress_summary: "Implementation started",
  current_step: "Build the graph",
  blocker: "",
  evidence_refs: ["https://github.com/LRM-Teams/minecraft/pull/13"],
  completed_criteria: [],
  created_by_type: "user",
  created_by_id: "user-1",
  updated_by_type: "agent",
  updated_by_id: "manager-1",
  created_at: "2026-08-11T00:00:00Z",
  updated_at: "2026-08-11T00:00:00Z",
  work_graph: {
    id: "graph-1",
    version: 2,
    status: "active",
    total: 3,
    completed: 1,
    running: 1,
    waiting: 1,
    stale: 0,
  },
  coordination: {
    git_repository_bound: false,
    agent_member_count: 3,
    channel_issue_total: 0,
    channel_project_issue_total: 0,
    project_issue_total: 0,
    open_project_issue_total: 0,
    in_review_project_issue_total: 0,
    subgoal_total: 0,
    open_subgoal_total: 0,
    execution_admission: "project_required",
  },
};

function graphNode(id: string, patch: Partial<WorkGraphNode>): WorkGraphNode {
  return {
    id,
    issue_id: `issue-${id}`,
    role: "worker",
    context_policy: "bounded",
    execution_status: "queued",
    validity_status: "valid",
    review_status: "unreviewed",
    completion_authority: "kernel_evidence",
    effective_completion: "pending",
    objective: `Node ${id}`,
    completion_contract: [],
    based_on_graph_version: 2,
    ...patch,
  };
}

const graph: WorkGraphDetail = {
  id: "graph-1",
  workspace_id: "workspace-1",
  anchor_kind: "channel_goal",
  anchor_id: "goal-1",
  status: "active",
  current_version: 2,
  admission_decision: "GRAPH",
  nodes: [
    graphNode("done", { effective_completion: "satisfied" }),
    graphNode("work", { execution_status: "running" }),
    graphNode("error", { execution_status: "failed", role: "verifier" }),
  ],
  edges: [
    { id: "done-work", from_node_id: "done", to_node_id: "work", edge_type: "depends_on", required: true },
    { id: "work-error", from_node_id: "work", to_node_id: "error", edge_type: "depends_on", required: true },
  ],
};

const state = vi.hoisted(() => ({
  graphQuery: vi.fn(async () => graph),
  bootstrapMutate: vi.fn(),
  goal: null as ChannelGoal | null,
}));

state.goal = goal;

vi.mock("@multica/core/channels", async (importOriginal) => ({
  ...(await importOriginal<typeof import("@multica/core/channels")>()),
  channelGoalOptions: (channelId: string) => ({
    queryKey: ["channel-goal", channelId],
    queryFn: async () => ({ goal: state.goal }),
  }),
  channelGoalProcessesOptions: (channelId: string) => ({
    queryKey: ["channel-goal", channelId, "processes"],
    queryFn: async () => ({ processes: [] }),
  }),
  channelGoalSubgoalsOptions: (channelId: string) => ({
    queryKey: ["channel-goal", channelId, "subgoals"],
    queryFn: async () => ({ subgoals: [] }),
  }),
  channelMembersOptions: (channelId: string) => ({
    queryKey: ["channel-members", channelId],
    queryFn: async () => [],
  }),
  workGraphOptions: (graphId: string | undefined) => ({
    queryKey: ["channel-goal", "work-graph", graphId ?? "none"],
    queryFn: state.graphQuery,
  }),
  useCreateChannelGoal: () => ({ mutate: vi.fn(), isPending: false }),
  useUpdateChannelGoal: () => ({ mutate: vi.fn(), isPending: false }),
  useBootstrapChannelGoalControlPlane: () => ({
    mutate: state.bootstrapMutate,
    isPending: false,
  }),
}));

vi.mock("@multica/ui/hooks/use-mobile", () => ({
  useIsMobile: () => false,
}));

function renderCard() {
  const queryClient = new QueryClient({
    defaultOptions: { queries: { retry: false } },
  });
  return renderWithI18n(
    <QueryClientProvider client={queryClient}>
      <ChannelGoalCard channelId="channel-1" canManage />
    </QueryClientProvider>,
  );
}

describe("ChannelGoalCard work graph", () => {
  beforeEach(() => {
    state.graphQuery.mockClear();
    state.bootstrapMutate.mockClear();
    state.goal = goal;
  });

  it("loads the graph only after expansion and renders live node states", async () => {
    const user = userEvent.setup();
    const view = renderCard();

    expect(await screen.findByText(goal.title)).toBeInTheDocument();
    expect(screen.getByTestId("channel-goal-control-plane-badge")).toHaveTextContent("Setup required");
    expect(state.graphQuery).not.toHaveBeenCalled();
    expect(screen.queryByTestId("goal-mini-graph")).not.toBeInTheDocument();

    await user.click(screen.getByRole("button", { name: "Expand goal" }));

    expect(await screen.findByTestId("channel-goal-control-plane")).toHaveTextContent("Delivery control plane");
    expect(await screen.findByTestId("goal-mini-graph")).toBeInTheDocument();
    expect(state.graphQuery).toHaveBeenCalledTimes(1);
    await waitFor(() => {
      expect(view.container.querySelector('[data-state="done"]')).toBeInTheDocument();
      expect(view.container.querySelector('[data-state="working"]')).toBeInTheDocument();
      expect(view.container.querySelector('[data-state="error"]')).toBeInTheDocument();
      // Verifier nodes render as dashed HTML chips (not SVG rects).
      expect(view.container.querySelector('[data-node-id="error"]')).toHaveClass("border-dashed");
    });
  });

  it("lets a channel manager recover a legacy goal with an explicit repository confirmation", async () => {
    const user = userEvent.setup();
    renderCard();

    await user.click(await screen.findByRole("button", { name: "Expand goal" }));
    await user.click(screen.getByRole("button", { name: "Set up delivery" }));

    expect(screen.getByRole("dialog", { name: "Set up delivery control plane" })).toBeInTheDocument();
    expect(screen.getByLabelText("Project title")).toHaveValue(goal.title);
    expect(screen.getByLabelText("GitHub repository URL")).toHaveValue(
      "https://github.com/LRM-Teams/minecraft",
    );

    await user.click(screen.getByRole("button", { name: "Create and bind" }));
    expect(state.bootstrapMutate).toHaveBeenCalledWith(
      {
        project_title: goal.title,
        repository_url: "https://github.com/LRM-Teams/minecraft",
        default_branch_hint: "",
      },
      expect.objectContaining({ onSuccess: expect.any(Function), onError: expect.any(Function) }),
    );
  });

  it("hides the empty state from viewers who cannot manage the Goal", async () => {
    state.goal = null;
    const queryClient = new QueryClient({
      defaultOptions: { queries: { retry: false } },
    });
    renderWithI18n(
      <QueryClientProvider client={queryClient}>
        <ChannelGoalCard channelId="channel-1" canManage={false} />
      </QueryClientProvider>,
    );
    await waitFor(() => {
      expect(screen.queryByTestId("channel-goal-loading")).not.toBeInTheDocument();
    });
    expect(screen.queryByRole("button", { name: "Set manually" })).not.toBeInTheDocument();
  });

  it("shows Set manually when a manager has no current Goal", async () => {
    state.goal = null;
    renderCard();
    expect(await screen.findByRole("button", { name: "Set manually" })).toBeInTheDocument();
    expect(screen.getByText(/State the overall goal in the group/)).toBeInTheDocument();
  });

  it("distinguishes missing Issues from missing Project or Git setup", async () => {
    state.goal = {
      ...goal,
      coordination: {
        ...goal.coordination!,
        project_id: "project-1",
        git_repository_bound: true,
        execution_admission: "issues_required",
      },
    };
    renderCard();

    expect(await screen.findByTestId("channel-goal-control-plane-badge")).toHaveTextContent(
      "Issues required",
    );
    expect(screen.queryByText("Setup required")).not.toBeInTheDocument();
  });
});
