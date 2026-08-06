// @vitest-environment jsdom

import { fireEvent, render, screen } from "@testing-library/react";
import { beforeEach, describe, expect, it, vi } from "vitest";
import type { Agent } from "@multica/core/types";
import { AgentProfileActions } from "./agent-profile-actions";
import { pickStoppableDmTask } from "./agent-profile-stoppable-task";
import type { ChannelActiveTask } from "@multica/core/types";

const mocks = vi.hoisted(() => ({
  openDM: vi.fn(),
  isPending: false,
  archiveAgent: vi.fn(async (..._args: unknown[]) => ({})),
  invalidateQueries: vi.fn(),
  toastSuccess: vi.fn(),
  toastError: vi.fn(),
  // Defaults to "not resolved yet" so every existing test (which doesn't
  // care about the restart preflight) sees the trigger enabled, exactly
  // like before this query existed — see `restartBlocked` in the component.
  restartPreflight: { data: undefined, isSuccess: false } as {
    data: unknown;
    isSuccess: boolean;
  },
}));

vi.mock("../../common/use-open-dm", () => ({
  useOpenDM: () => ({ openDM: mocks.openDM, isPending: mocks.isPending }),
}));

vi.mock("@multica/core/api", () => ({
  api: {
    archiveAgent: (...args: unknown[]) => mocks.archiveAgent(...args),
  },
}));

vi.mock("@tanstack/react-query", () => ({
  useQueryClient: () => ({ invalidateQueries: mocks.invalidateQueries }),
  useQuery: () => mocks.restartPreflight,
}));

vi.mock("@multica/core/agents", () => ({
  agentLifecyclePreflightOptions: (agentId: string, enabled: boolean) => ({
    agentId,
    enabled,
  }),
  agentLifecycleActionState: (
    preflight: { actions?: Record<string, { supported: boolean; disabled_reason?: string | null }> } | null | undefined,
    kind: string,
  ) => preflight?.actions?.[kind] ?? { supported: false, disabled_reason: "unavailable" },
  resolveLifecycleDisabledReasonKey: (reason: string | null | undefined) =>
    reason ?? "unavailable",
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
    agent_deleted_toast: "Deleted",
    delete_failed_toast: "Delete failed",
  },
  restart_modal: {
    trigger: "Restart…",
    disabled_reason: {
      unsupported_runtime_capability: "Requires daemon v0.3.95 or newer.",
      unavailable: "This action isn't available right now.",
      no_force_capability: "This code agent can't force-restart yet.",
    },
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

describe("AgentProfileActions (LRM-468 / LRM-909)", () => {
  beforeEach(() => {
    mocks.openDM.mockReset();
    mocks.archiveAgent.mockReset().mockResolvedValue({});
    mocks.toastSuccess.mockReset();
    mocks.toastError.mockReset();
    mocks.invalidateQueries.mockReset();
    mocks.isPending = false;
    mocks.restartPreflight = { data: undefined, isSuccess: false };
  });

  it("renders Message as primary action and opens DM", () => {
    render(<AgentProfileActions agent={agent} canManage forceRestartSupported />);
    fireEvent.click(screen.getByTestId("agent-profile-action-message"));
    expect(mocks.openDM).toHaveBeenCalledWith({ peer_type: "agent", peer_id: "agent-1" });
  });

  it("renders Message → Restart… → Delete and never Stop (LRM-909)", () => {
    render(<AgentProfileActions agent={agent} canManage forceRestartSupported />);
    expect(screen.getByTestId("agent-profile-action-message")).toBeInTheDocument();
    expect(screen.getByTestId("agent-profile-action-restart")).toBeInTheDocument();
    expect(screen.getByTestId("agent-profile-action-delete")).toBeInTheDocument();
    expect(screen.queryByTestId("agent-profile-action-stop")).not.toBeInTheDocument();
    expect(screen.queryByText("Stop")).not.toBeInTheDocument();
    expect(screen.queryByText("Stop all")).not.toBeInTheDocument();
  });

  it("renders the #633 Restart entry for a manager; Copy diagnostic / Report stay out of scope", () => {
    render(<AgentProfileActions agent={agent} canManage forceRestartSupported />);
    expect(screen.getByTestId("agent-profile-action-restart")).toBeInTheDocument();
    expect(screen.queryByTestId("agent-profile-action-copy")).not.toBeInTheDocument();
    expect(screen.queryByTestId("agent-profile-action-report")).not.toBeInTheDocument();
    expect(screen.queryByText("Copy diagnostic info")).not.toBeInTheDocument();
    expect(screen.queryByText("Report issue")).not.toBeInTheDocument();
  });

  it("hides the Restart entry for a non-manager", () => {
    render(<AgentProfileActions agent={agent} canManage={false} forceRestartSupported />);
    expect(screen.queryByTestId("agent-profile-action-restart")).not.toBeInTheDocument();
  });

  // Frank, 2026-08-01: restart only makes sense while the computer can
  // actually receive it — hide it offline, it comes back on its own once
  // the computer reconnects (derived health, not the raw status column).
  it("hides the Restart entry when the bound computer is offline", () => {
    const offlineAgent = { ...agent, runtime_status: "offline" } as Agent;
    render(<AgentProfileActions agent={offlineAgent} canManage forceRestartSupported />);
    expect(screen.queryByTestId("agent-profile-action-restart")).not.toBeInTheDocument();
  });

  it("hides the Restart entry when status says online but the heartbeat is stale (#10)", () => {
    const staleAgent = {
      ...agent,
      runtime_status: "online",
      runtime_last_seen_at: new Date(Date.now() - 10 * 60_000).toISOString(),
    } as Agent;
    render(<AgentProfileActions agent={staleAgent} canManage forceRestartSupported />);
    expect(screen.queryByTestId("agent-profile-action-restart")).not.toBeInTheDocument();
  });

  // task #26 / Parker: canManage still sees Restart when force_restart is
  // false — disabled with human copy, never a missing entry.
  it("shows Restart disabled with reason when the runtime lacks force_restart", () => {
    render(<AgentProfileActions agent={agent} canManage forceRestartSupported={false} />);
    const trigger = screen.getByTestId("agent-profile-action-restart");
    expect(trigger).toBeDisabled();
    expect(screen.getByTestId("agent-profile-action-restart-reason")).toHaveTextContent(
      /can.t force-restart|force-restart/i,
    );
  });

  it("shows the Restart entry when the runtime supports forced restart", () => {
    render(<AgentProfileActions agent={agent} canManage forceRestartSupported={true} />);
    expect(screen.getByTestId("agent-profile-action-restart")).toBeInTheDocument();
  });

  // The provider-level gate (forceRestartSupported) only says the provider
  // CAN be force-restarted in principle — the daemon it's actually running
  // on might still be too old (pre-agent_lifecycle_actions_v1). This is
  // that second, daemon-side gate: button visible, but disabled with a
  // standing reason instead of a click that silently no-ops.
  it("disables the trigger with a standing reason when the daemon preflight says unsupported", () => {
    mocks.restartPreflight = {
      isSuccess: true,
      data: {
        actions: {
          restart: { supported: false, disabled_reason: "unsupported_runtime_capability" },
        },
      },
    };
    render(<AgentProfileActions agent={agent} canManage forceRestartSupported />);
    const trigger = screen.getByTestId("agent-profile-action-restart");
    expect(trigger).toBeDisabled();
    expect(screen.getByTestId("agent-profile-action-restart-reason")).toHaveTextContent(
      "Requires daemon v0.3.95 or newer.",
    );
  });

  it("keeps the trigger enabled with no reason once the preflight confirms restart is supported", () => {
    mocks.restartPreflight = {
      isSuccess: true,
      data: { actions: { restart: { supported: true } } },
    };
    render(<AgentProfileActions agent={agent} canManage forceRestartSupported />);
    const trigger = screen.getByTestId("agent-profile-action-restart");
    expect(trigger).not.toBeDisabled();
    expect(screen.queryByTestId("agent-profile-action-restart-reason")).not.toBeInTheDocument();
  });

  it("doesn't flash a disabled reason before the preflight resolves", () => {
    mocks.restartPreflight = { isSuccess: false, data: undefined };
    render(<AgentProfileActions agent={agent} canManage forceRestartSupported />);
    const trigger = screen.getByTestId("agent-profile-action-restart");
    expect(trigger).not.toBeDisabled();
    expect(screen.queryByTestId("agent-profile-action-restart-reason")).not.toBeInTheDocument();
  });

  it("hides Delete when canManage is false; keeps Message", () => {
    render(<AgentProfileActions agent={agent} canManage={false} forceRestartSupported />);
    expect(screen.queryByTestId("agent-profile-action-delete")).not.toBeInTheDocument();
    expect(screen.getByTestId("agent-profile-action-message")).toBeInTheDocument();
  });

  it("Delete is the only solid destructive in a border-t danger zone (LRM-593 lock A)", () => {
    render(<AgentProfileActions agent={agent} canManage forceRestartSupported />);
    const del = screen.getByTestId("agent-profile-action-delete");
    expect(del.className).toMatch(/text-white/);
    expect(del.parentElement?.className).toMatch(/border-t/);
  });

});
