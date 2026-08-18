// @vitest-environment jsdom

import { act, fireEvent, render, screen } from "@testing-library/react";
import { beforeEach, describe, expect, it, vi } from "vitest";
import type { Agent } from "@multica/core/types";
import { AgentProfileActions } from "./agent-profile-actions";
import { pickStoppableDmTask, type StoppableAgentTask } from "./agent-profile-stoppable-task";

const mocks = vi.hoisted(() => ({
  openDM: vi.fn(),
  isPending: false,
  archiveAgent: vi.fn(async (..._args: unknown[]) => ({})),
  startAgent: vi.fn(async (..._args: unknown[]) => ({ status: "starting" })),
  stopAgent: vi.fn(async (..._args: unknown[]) => ({ status: "stopping" })),
  invalidateQueries: vi.fn(),
  toastSuccess: vi.fn(),
  toastError: vi.fn(),
  presence: "offline" as "online" | "offline",
}));

vi.mock("../../common/use-open-dm", () => ({
  useOpenDM: () => ({ openDM: mocks.openDM, isPending: mocks.isPending }),
}));

vi.mock("@multica/core/api", () => ({
  api: {
    archiveAgent: (...args: unknown[]) => mocks.archiveAgent(...args),
    startAgent: (...args: unknown[]) => mocks.startAgent(...args),
    stopAgent: (...args: unknown[]) => mocks.stopAgent(...args),
  },
}));

vi.mock("@tanstack/react-query", () => ({
  useQueryClient: () => ({ invalidateQueries: mocks.invalidateQueries }),
}));

vi.mock("sonner", () => ({
  toast: {
    success: (...args: unknown[]) => mocks.toastSuccess(...args),
    error: (...args: unknown[]) => mocks.toastError(...args),
  },
}));

vi.mock("./agent-restart-modal", () => ({
  AgentRestartModal: ({ open }: { open: boolean }) =>
    open ? <div data-testid="agent-restart-modal">Restart and reset choices</div> : null,
}));

vi.mock("../../i18n/use-t", () => ({
  useT: () => ({
    t: (selector: (r: typeof RESOURCES) => string, vars?: Record<string, unknown>) => {
      const template = selector(RESOURCES);
      if (typeof template !== "string") return String(template);
      return template.replace(/\{\{(\w+)\}\}/g, (_, key: string) =>
        String(vars?.[key] ?? `{{${key}}}`),
      );
    },
  }),
}));

const RESOURCES = {
  delete_confirm: {
    title: "Delete agent?",
    description: "Delete {{name}}",
    cancel: "Cancel",
    confirm: "Confirm delete",
    confirming: "Deleting…",
  },
  side_panel: {
    actions_section: "Actions",
    message_button: "Message",
    message_opening: "Opening…",
    actions_delete: "Delete",
    actions_start_agent: "Start Agent",
    actions_start_success: "Agent start requested",
    actions_start_failed: "Failed to start agent",
    actions_stop_agent: "Stop Agent",
    actions_stop_agent_success: "Agent stopped",
    actions_stop_agent_failed: "Failed to stop agent",
    agent_deleted_toast: "Deleted",
    delete_failed_toast: "Delete failed",
  },
  restart_modal: {
    trigger: "Restart…",
  },
};

const agent = {
  id: "agent-1",
  workspace_id: "ws-1",
  workspace_role: "member",
  runtime_id: "runtime-1",
  name: "atlas",
  display_name: "Atlas",
  description: "desc",
  instructions: "",
  avatar_url: null,
  runtime_mode: "local",
  runtime_config: {},
  custom_args: [],
  status: "idle",
  runtime_status: "online",
  runtime_last_seen_at: new Date().toISOString(),
  max_concurrent_tasks: 1,
  model: "auto",
  thinking_level: "",
  managed_role: undefined,
  owner_id: "user-owner",
  skills: [],
  created_at: "2026-01-01T00:00:00Z",
  updated_at: "2026-01-01T00:00:00Z",
  archived_at: null,
  archived_by: null,
} as Agent;

function runningTask(over: Partial<StoppableAgentTask> = {}): StoppableAgentTask {
  return {
    agent_id: "agent-1",
    task_id: "task-1",
    status: "running",
    ...over,
  };
}

describe("pickStoppableDmTask (LRM-589)", () => {
  it("prefers running over queued and skips terminal / other agents", () => {
    expect(
      pickStoppableDmTask(
        [
          runningTask({
            agent_id: "other",
            task_id: "other",
          }),
          runningTask({
            status: "queued",
            task_id: "queued",
          }),
          runningTask({
            status: "failed",
            outcome: "failed",
            task_id: "failed",
          }),
          runningTask({ task_id: "run" }),
        ],
        "agent-1",
      )?.task_id,
    ).toBe("run");
  });

  it("returns null when idle", () => {
    expect(pickStoppableDmTask([], "agent-1")).toBeNull();
  });
});

describe("AgentProfileActions", () => {
  beforeEach(() => {
    mocks.openDM.mockReset();
    mocks.archiveAgent.mockReset().mockResolvedValue({});
    mocks.startAgent.mockReset().mockResolvedValue({ status: "starting" });
    mocks.stopAgent.mockReset().mockResolvedValue({ status: "stopping" });
    mocks.toastSuccess.mockReset();
    mocks.toastError.mockReset();
    mocks.invalidateQueries.mockReset();
    mocks.isPending = false;
    mocks.presence = "offline";
  });

  it("uses Runner presence, not work status, to offer Start", async () => {
    render(<AgentProfileActions agent={agent} canManage presence={mocks.presence} />);
    await act(async () => fireEvent.click(screen.getByRole("button", { name: "Start Agent" })));
    expect(mocks.startAgent).toHaveBeenCalledWith("agent-1");
    expect(mocks.stopAgent).not.toHaveBeenCalled();
  });

  it("uses active Runner presence to offer Stop", async () => {
    mocks.presence = "online";
    render(
      <AgentProfileActions
        agent={{ ...agent, status: "offline" }}
        canManage
        presence={mocks.presence}
      />,
    );
    await act(async () => fireEvent.click(screen.getByRole("button", { name: "Stop Agent" })));
    expect(mocks.stopAgent).toHaveBeenCalledWith("agent-1");
    expect(mocks.startAgent).not.toHaveBeenCalled();
  });

  it("renders Message as primary action and opens DM", () => {
    render(<AgentProfileActions agent={agent} canManage presence={mocks.presence} />);
    fireEvent.click(screen.getByTestId("agent-profile-action-message"));
    expect(mocks.openDM).toHaveBeenCalledWith({ peer_type: "agent", peer_id: "agent-1" });
  });

  it("keeps Restart and reset choices separate from the Start/Stop lifecycle action", () => {
    render(<AgentProfileActions agent={agent} canManage presence={mocks.presence} />);
    expect(screen.getByTestId("agent-profile-action-message")).toBeInTheDocument();
    expect(screen.getByTestId("agent-profile-action-start")).toBeInTheDocument();
    fireEvent.click(screen.getByTestId("agent-profile-action-restart"));
    expect(screen.getByTestId("agent-restart-modal")).toHaveTextContent("Restart and reset choices");
    expect(screen.getByTestId("agent-profile-action-delete")).toBeInTheDocument();
    expect(screen.queryByTestId("agent-profile-action-stop")).not.toBeInTheDocument();
    expect(screen.queryByText("Stop all")).not.toBeInTheDocument();
  });

  it("hides Delete when canManage is false; keeps Message", () => {
    render(<AgentProfileActions agent={agent} canManage={false} presence={mocks.presence} />);
    expect(screen.queryByTestId("agent-profile-action-delete")).not.toBeInTheDocument();
    expect(screen.getByTestId("agent-profile-action-message")).toBeInTheDocument();
  });

  it("Delete is the only solid destructive in a border-t danger zone (LRM-593 lock A)", () => {
    render(<AgentProfileActions agent={agent} canManage presence={mocks.presence} />);
    const del = screen.getByTestId("agent-profile-action-delete");
    expect(del.className).toMatch(/text-white/);
    expect(del.parentElement?.className).toMatch(/border-t/);
  });
});
