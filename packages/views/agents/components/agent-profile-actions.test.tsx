// @vitest-environment jsdom

import { fireEvent, render, screen, waitFor } from "@testing-library/react";
import { beforeEach, describe, expect, it, vi } from "vitest";
import type { Agent, ChannelActiveTask } from "@multica/core/types";
import type { DMItem } from "@multica/core/dm";
import { AgentProfileActions } from "./agent-profile-actions";
import { pickStoppableDmTask } from "./agent-profile-stoppable-task";

const mocks = vi.hoisted(() => ({
  openDM: vi.fn(),
  isPending: false,
  archiveAgent: vi.fn(async (..._args: unknown[]) => ({})),
  cancelChannelInboxEvent: vi.fn(async (..._args: unknown[]) => ({})),
  invalidateQueries: vi.fn(),
  toastSuccess: vi.fn(),
  toastError: vi.fn(),
  dms: [] as DMItem[],
  activeTasks: [] as ChannelActiveTask[],
}));

vi.mock("../../common/use-open-dm", () => ({
  useOpenDM: () => ({ openDM: mocks.openDM, isPending: mocks.isPending }),
}));

vi.mock("@multica/core/api", () => ({
  api: {
    archiveAgent: (...args: unknown[]) => mocks.archiveAgent(...args),
    cancelChannelInboxEvent: (...args: unknown[]) =>
      mocks.cancelChannelInboxEvent(...args),
  },
}));

vi.mock("@multica/core/hooks", () => ({
  useWorkspaceId: () => "ws-1",
}));

vi.mock("@multica/core/dm", () => ({
  dmKeys: { list: (wsId: string) => ["dm", wsId, "list"] },
  dmListOptions: () => ({
    queryKey: ["dm", "ws-1", "list"],
    queryFn: async () => mocks.dms,
  }),
}));

vi.mock("@multica/core/channels", () => ({
  activeChannelTasksKeys: {
    all: (channelId: string) => ["channel-active-tasks", channelId],
  },
  activeChannelTasksOptions: (channelId: string) => ({
    queryKey: ["channel-active-tasks", channelId],
    queryFn: async () => mocks.activeTasks,
    enabled: !!channelId,
  }),
}));

vi.mock("@tanstack/react-query", () => ({
  useQueryClient: () => ({ invalidateQueries: mocks.invalidateQueries }),
  useQuery: (options: {
    queryKey: unknown[];
    queryFn?: () => Promise<unknown>;
    enabled?: boolean;
  }) => {
    const key0 = String(options.queryKey[0] ?? "");
    if (key0 === "dm") {
      return { data: mocks.dms };
    }
    if (options.enabled === false || key0 !== "channel-active-tasks") {
      return { data: [] };
    }
    return { data: mocks.activeTasks };
  },
}));

vi.mock("sonner", () => ({
  toast: {
    success: (...args: unknown[]) => mocks.toastSuccess(...args),
    error: (...args: unknown[]) => mocks.toastError(...args),
  },
}));

// #633: the three-tier restart modal has its own test; stub it here so this
// suite only exercises the actions stack (the modal mounts a real lifecycle
// hook we don't need to wire for these assertions).
vi.mock("./agent-restart-modal", () => ({
  AgentRestartModal: () => null,
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
  side_panel: {
    actions_section: "Actions",
    message_button: "Message",
    message_opening: "Opening…",
    actions_stop: "Stop",
    actions_stop_aria: "Stop {{name}}'s current task",
    actions_stop_success: "Stopped {{name}}",
    actions_stop_failed: "Failed to stop agent task",
    actions_delete: "Delete",
    delete_dialog_title: "Delete agent?",
    delete_dialog_description: "Delete {{name}}",
    delete_dialog_cancel: "Cancel",
    delete_dialog_confirm: "Confirm delete",
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
  runtime_id: "runtime-1",
  name: "atlas",
  display_name: "Atlas",
  description: "desc",
  instructions: "",
  avatar_url: null,
  runtime_mode: "local",
  runtime_config: {},
  custom_args: [],
  visibility: "workspace",
  status: "idle",
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

const dm = {
  id: "dm-1",
  peer: { type: "agent", id: "agent-1", name: "Atlas" },
} as DMItem;

function runningTask(over: Partial<ChannelActiveTask> = {}): ChannelActiveTask {
  return {
    agent_id: "agent-1",
    agent_name: "Atlas",
    task_id: "t1",
    status: "running",
    kind: "reply",
    reason: "mention",
    inbox_event_id: "inbox-1",
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
            inbox_event_id: "inbox-other",
          }),
          runningTask({
            status: "queued",
            task_id: "queued",
            inbox_event_id: "inbox-queued",
          }),
          runningTask({
            status: "failed",
            outcome: "failed",
            task_id: "failed",
            inbox_event_id: "inbox-failed",
          }),
          runningTask({ task_id: "run", inbox_event_id: "inbox-run" }),
        ],
        "agent-1",
      )?.task_id,
    ).toBe("run");
  });

  it("returns null when idle", () => {
    expect(pickStoppableDmTask([], "agent-1")).toBeNull();
  });
});

describe("AgentProfileActions (LRM-468 / LRM-589)", () => {
  beforeEach(() => {
    mocks.openDM.mockReset();
    mocks.archiveAgent.mockReset().mockResolvedValue({});
    mocks.cancelChannelInboxEvent.mockReset().mockResolvedValue({});
    mocks.toastSuccess.mockReset();
    mocks.toastError.mockReset();
    mocks.invalidateQueries.mockReset();
    mocks.isPending = false;
    mocks.dms = [];
    mocks.activeTasks = [];
  });

  it("renders Message as primary action and opens DM", () => {
    render(<AgentProfileActions agent={agent} canManage />);
    fireEvent.click(screen.getByTestId("agent-profile-action-message"));
    expect(mocks.openDM).toHaveBeenCalledWith({ peer_type: "agent", peer_id: "agent-1" });
  });

  it("renders the #633 Restart entry for a manager; Copy diagnostic / Report stay out of scope", () => {
    render(<AgentProfileActions agent={agent} canManage />);
    // #633 reinstates a Restart entry (opens the three-tier restart modal).
    expect(screen.getByTestId("agent-profile-action-restart")).toBeInTheDocument();
    // The other LRM-468 items remain out of scope.
    expect(screen.queryByTestId("agent-profile-action-copy")).not.toBeInTheDocument();
    expect(screen.queryByTestId("agent-profile-action-report")).not.toBeInTheDocument();
    expect(screen.queryByText("Copy diagnostic info")).not.toBeInTheDocument();
    expect(screen.queryByText("Report issue")).not.toBeInTheDocument();
  });

  it("hides the Restart entry for a non-manager", () => {
    render(<AgentProfileActions agent={agent} canManage={false} />);
    expect(screen.queryByTestId("agent-profile-action-restart")).not.toBeInTheDocument();
  });

  it("hides Delete when canManage is false; keeps Message", () => {
    render(<AgentProfileActions agent={agent} canManage={false} />);
    expect(screen.queryByTestId("agent-profile-action-delete")).not.toBeInTheDocument();
    expect(screen.getByTestId("agent-profile-action-message")).toBeInTheDocument();
  });

  it("isolates Delete in a danger bottom zone for managers", () => {
    render(<AgentProfileActions agent={agent} canManage />);
    const del = screen.getByTestId("agent-profile-action-delete");
    expect(del.className).toMatch(/text-destructive/);
    expect(del.parentElement?.className).toMatch(/border-t/);
  });

  it("Delete confirms then deactivates via archiveAgent (LRM-448: Delete, not Archive)", async () => {
    render(<AgentProfileActions agent={agent} canManage />);
    // Button is labeled Delete (never "Archive agent") — LRM-448 AC#2.
    expect(screen.getByTestId("agent-profile-action-delete")).toHaveTextContent("Delete");
    expect(screen.queryByText("Archive agent")).not.toBeInTheDocument();
    fireEvent.click(screen.getByTestId("agent-profile-action-delete"));
    fireEvent.click(screen.getByRole("button", { name: "Confirm delete" }));
    await waitFor(() => {
      expect(mocks.archiveAgent).toHaveBeenCalledWith("agent-1");
    });
    expect(mocks.toastSuccess).toHaveBeenCalledWith("Deleted");
  });

  it("hides Stop when the agent has no live DM task", () => {
    mocks.dms = [dm];
    mocks.activeTasks = [];
    render(<AgentProfileActions agent={agent} canManage />);
    expect(screen.queryByTestId("agent-profile-action-stop")).not.toBeInTheDocument();
    expect(screen.queryByText("Stop all")).not.toBeInTheDocument();
  });

  it("shows Stop (not Stop all) for a live DM task and cancels inbox", async () => {
    mocks.dms = [dm];
    mocks.activeTasks = [runningTask()];
    render(<AgentProfileActions agent={agent} canManage />);
    const stop = screen.getByTestId("agent-profile-action-stop");
    expect(stop).toHaveAttribute("aria-label", "Stop Atlas's current task");
    expect(screen.queryByText("Stop all")).not.toBeInTheDocument();
    fireEvent.click(stop);
    await waitFor(() => {
      expect(mocks.cancelChannelInboxEvent).toHaveBeenCalledWith("dm-1", "inbox-1");
    });
    expect(mocks.toastSuccess).toHaveBeenCalledWith("Stopped Atlas");
  });

  it("keeps Stop out of the Delete danger zone", () => {
    mocks.dms = [dm];
    mocks.activeTasks = [runningTask()];
    render(<AgentProfileActions agent={agent} canManage />);
    const stop = screen.getByTestId("agent-profile-action-stop");
    expect(stop.parentElement?.className).not.toMatch(/border-t/);
  });
});
