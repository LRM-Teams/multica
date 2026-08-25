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
    actions_more_aria: "More actions",
    message_button: "Message",
    message_opening: "Opening…",
    actions_delete: "Delete",
    actions_start_agent: "Start Agent",
    actions_start_failed: "Failed to start agent",
    actions_stop_agent: "Stop Agent",
    actions_stop_agent_failed: "Failed to stop agent",
    agent_deleted_toast: "Deleted",
    delete_failed_toast: "Delete failed",
  },
  restart_modal: {
    trigger: "Restart/Reset",
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
    render(
      <AgentProfileActions agent={agent} canManage presence={mocks.presence} layout="icons" />,
    );
    fireEvent.click(screen.getByTestId("agent-profile-chrome-actions-menu"));
    await act(async () =>
      fireEvent.click(screen.getByTestId("agent-profile-chrome-action-start")),
    );
    expect(mocks.startAgent).toHaveBeenCalledWith("agent-1");
    expect(mocks.stopAgent).not.toHaveBeenCalled();
    expect(mocks.invalidateQueries).toHaveBeenCalledWith({
      queryKey: ["workspaces", "ws-1", "agent-presence"],
    });
    expect(mocks.toastSuccess).not.toHaveBeenCalled();
  });

  it("uses active Runner presence to offer Stop", async () => {
    mocks.presence = "online";
    render(
      <AgentProfileActions
        agent={{ ...agent, status: "offline" }}
        canManage
        presence={mocks.presence}
        layout="icons"
      />,
    );
    fireEvent.click(screen.getByTestId("agent-profile-chrome-actions-menu"));
    await act(async () =>
      fireEvent.click(screen.getByTestId("agent-profile-chrome-action-start")),
    );
    expect(mocks.stopAgent).toHaveBeenCalledWith("agent-1");
    expect(mocks.startAgent).not.toHaveBeenCalled();
    expect(mocks.toastSuccess).not.toHaveBeenCalled();
  });

  it("opens the DM from the chrome ⋯ menu's Message item, not the stack", () => {
    render(
      <AgentProfileActions agent={agent} canManage presence={mocks.presence} layout="icons" />,
    );
    fireEvent.click(screen.getByTestId("agent-profile-chrome-actions-menu"));
    fireEvent.click(screen.getByTestId("agent-profile-chrome-action-message"));
    expect(mocks.openDM).toHaveBeenCalledWith({ peer_type: "agent", peer_id: "agent-1" });
    expect(screen.queryByTestId("agent-profile-action-message")).not.toBeInTheDocument();
  });

  it("reduces the stack to Delete — lifecycle and restart live only in the chrome menu", () => {
    render(<AgentProfileActions agent={agent} canManage presence={mocks.presence} />);
    expect(screen.getByText("Actions")).toBeInTheDocument();
    expect(screen.getByRole("button", { name: "Delete" })).toHaveTextContent("Delete");
    expect(screen.getAllByTestId("agent-profile-action-delete")).toHaveLength(1);
    expect(screen.queryByTestId("agent-profile-action-message")).not.toBeInTheDocument();
    expect(screen.queryByTestId("agent-profile-action-start")).not.toBeInTheDocument();
    expect(screen.queryByTestId("agent-profile-action-restart")).not.toBeInTheDocument();
    expect(screen.queryByRole("button", { name: "Start Agent" })).not.toBeInTheDocument();
    expect(screen.queryByRole("button", { name: "Restart/Reset" })).not.toBeInTheDocument();
    expect(screen.queryByTestId("agent-profile-action-stop")).not.toBeInTheDocument();
    expect(screen.queryByText("Stop all")).not.toBeInTheDocument();
  });

  it("lists Message, Start/Stop, and Restart/Reset as labeled items in the chrome ⋯ menu, without Delete", () => {
    render(
      <AgentProfileActions agent={agent} canManage presence={mocks.presence} layout="icons" />,
    );
    // Only the ⋯ trigger sits in the chrome row until it's opened — no
    // outline icon buttons are rendered inline any more.
    const trigger = screen.getByTestId("agent-profile-chrome-actions-menu");
    expect(trigger).toHaveAttribute("aria-label", "More actions");
    expect(screen.queryByTestId("agent-profile-chrome-action-message")).not.toBeInTheDocument();

    fireEvent.click(trigger);

    const message = screen.getByTestId("agent-profile-chrome-action-message");
    const lifecycle = screen.getByTestId("agent-profile-chrome-action-start");
    const restart = screen.getByTestId("agent-profile-chrome-action-restart");
    // Menu items carry visible text labels — that's the point of the
    // Linear-style menu (unlike the old bare icon buttons).
    expect(message).toHaveTextContent("Message");
    expect(lifecycle).toHaveTextContent("Start Agent");
    expect(restart).toHaveTextContent("Restart/Reset");
    expect(screen.queryByTestId("agent-profile-action-delete")).not.toBeInTheDocument();
    expect(screen.queryByTestId("agent-profile-chrome-action-delete")).not.toBeInTheDocument();

    fireEvent.click(restart);
    expect(screen.getByTestId("agent-restart-modal")).toHaveTextContent("Restart and reset choices");
  });

  it("hides the stack when canManage is false", () => {
    render(<AgentProfileActions agent={agent} canManage={false} presence={mocks.presence} />);
    expect(screen.queryByTestId("agent-profile-actions")).not.toBeInTheDocument();
    expect(screen.queryByTestId("agent-profile-action-delete")).not.toBeInTheDocument();
    expect(screen.queryByTestId("agent-profile-action-message")).not.toBeInTheDocument();
  });

  it("keeps Delete as the only solid destructive stack action", () => {
    render(<AgentProfileActions agent={agent} canManage presence={mocks.presence} />);
    const del = screen.getByTestId("agent-profile-action-delete");
    expect(del.className).toMatch(/text-white/);
    expect(del.className).toMatch(/bg-destructive/);
  });
});
