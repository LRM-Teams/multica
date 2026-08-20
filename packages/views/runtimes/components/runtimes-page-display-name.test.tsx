// @vitest-environment jsdom
//
// Rename is a single entry point on the hero title (no second Basics
// "Display name" pencil for the same display_name field).

import { describe, it, expect, vi } from "vitest";
import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { fireEvent, render, screen, waitFor } from "@testing-library/react";
import type { Agent, AgentRuntime } from "@multica/core/types";
import { I18nProvider } from "@multica/core/i18n/react";
import enCommon from "../../locales/en/common.json";
import enRuntimes from "../../locales/en/runtimes.json";
import enAgents from "../../locales/en/agents.json";
import type { RuntimeMachine } from "./runtime-machines";

const TEST_RESOURCES = {
  en: { common: enCommon, runtimes: enRuntimes, agents: enAgents },
};

const { refetchWorkspaces, bulkUpdateAgentRuntimeConfig, bulkAgentLifecycle } =
  vi.hoisted(() => ({
    refetchWorkspaces: vi.fn(),
    bulkUpdateAgentRuntimeConfig: vi.fn().mockResolvedValue({
      updated_agent_ids: ["agent-1", "agent-2"],
    }),
    bulkAgentLifecycle: vi.fn().mockResolvedValue({
      results: [
        { agent_id: "agent-1", accepted: true, status: "starting" },
        { agent_id: "agent-2", accepted: true, status: "starting" },
      ],
    }),
  }));

vi.mock("@multica/core/api", () => ({
  api: {
    bulkUpdateAgentRuntimeConfig,
    bulkAgentLifecycle,
  },
}));

vi.mock("@multica/core/auth", () => ({
  useAuthStore: (sel: (s: { user: { id: string } }) => unknown) =>
    sel({ user: { id: "user-me" } }),
}));

vi.mock("@multica/core/agents", async () => {
  const actual =
    await vi.importActual<typeof import("@multica/core/agents")>(
      "@multica/core/agents",
    );
  return {
    ...actual,
    useWorkspaceAgentPresence: () => ({ byAgent: new Map() }),
  };
});

vi.mock("@multica/core/workspace/hooks", () => ({
  useActorName: () => ({ getActorName: () => "Me" }),
}));

vi.mock("@multica/core/agents/stores", () => ({
  useAgentPanelStore: (sel: (s: { open: (id: string) => void }) => unknown) =>
    sel({ open: vi.fn() }),
}));

vi.mock("@multica/core/runtimes/mutations", () => ({
  useRuntimeAgentWorkspaces: () => ({
    data: undefined,
    isFetching: false,
    refetch: refetchWorkspaces,
  }),
  useDeleteRuntimeAgentWorkspace: () => ({
    isPending: false,
    mutate: vi.fn(),
  }),
  useDeleteRuntimesByDaemon: () => ({
    isPending: false,
    mutateAsync: vi.fn(),
  }),
  useRemoveAgentsByDaemon: () => ({
    isPending: false,
    mutateAsync: vi.fn(),
  }),
  useUpdateRuntime: () => ({
    mutate: vi.fn(),
    isPending: false,
  }),
}));

vi.mock("@multica/core/workspace/queries", () => ({
  agentListOptions: () => ({ queryKey: ["agents"] }),
  memberListOptions: () => ({
    queryKey: ["members"],
    queryFn: async () => [],
  }),
  workspaceKeys: {
    agents: (wsId: string) => ["workspaces", wsId, "agents"],
  },
}));

vi.mock("@tanstack/react-query", async () => {
  const actual =
    await vi.importActual<typeof import("@tanstack/react-query")>(
      "@tanstack/react-query",
    );
  return {
    ...actual,
    useQuery: vi.fn(() => ({ data: [], isLoading: false })),
  };
});

vi.mock("@multica/core/paths", () => ({
  useWorkspacePaths: () => ({
    agentDetail: (id: string) => `/agents/${id}`,
  }),
}));

vi.mock("./machine-name-editor", () => ({
  MachineNameEditor: ({
    variant,
  }: {
    machine: RuntimeMachine;
    wsId: string;
    variant?: string;
  }) => <span data-testid={`machine-name-editor-${variant ?? "default"}`} />,
}));

vi.mock("./machine-code-agents-section", () => ({
  MachineCodeAgentsSection: () => null,
}));

vi.mock("./machine-header-ops", () => ({
  MachineHeaderOps: () => null,
}));

vi.mock("./machine-daemon-upgrade", () => ({
  MachineDaemonUpgrade: () => null,
}));

vi.mock("./machine-danger-zone", () => ({
  MachineDangerZone: () => null,
}));

vi.mock("../../common/actor-avatar", () => ({ ActorAvatar: () => null }));
vi.mock("../../agents/components/agent-activity-list-item", () => ({
  AgentActivityListItem: ({ agentId }: { agentId: string }) => (
    <div data-testid={`agent-row-${agentId}`} />
  ),
}));
vi.mock("../../agents/components/bulk-runtime-config-dialog", () => ({
  BulkRuntimeConfigDialog: ({
    agents,
    onSave,
  }: {
    agents: Agent[];
    onSave: (config: {
      runtime_id: string;
      model: string;
      thinking_level: string;
    }) => Promise<void>;
  }) => (
    <div data-testid="bulk-runtime-config-dialog">
      {agents.length}
      <button
        type="button"
        onClick={() =>
          void onSave({
            runtime_id: "runtime-1",
            model: "target-model",
            thinking_level: "high",
          })
        }
      >
        Apply bulk config
      </button>
    </div>
  ),
}));
vi.mock("sonner", () => ({
  toast: { error: vi.fn(), success: vi.fn() },
}));

import { ComputersMachineDetail } from "./runtimes-page";

function makeMachine(): RuntimeMachine {
  const runtime: AgentRuntime = {
    id: "runtime-1",
    workspace_id: "ws-1",
    daemon_id: "daemon-1",
    name: "Claude (box)",
    display_name: "",
    runtime_mode: "local",
    provider: "claude",
    launch_header: "",
    status: "online",
    device_info: "box",
    metadata: {},
    current_version: "1.0.0",
    update_state: "idle",
    runtime_health: "ok",
    owner_id: "user-me",
    last_seen_at: "2026-08-01T00:00:00Z",
    created_at: "2026-01-01T00:00:00Z",
    updated_at: "2026-01-01T00:00:00Z",
  };
  return {
    id: "local:daemon-1",
    daemonId: "daemon-1",
    title: "box",
    subtitle: null,
    deviceInfo: null,
    deviceName: null,
    cliVersion: "1.0.0",
    mode: "local",
    section: "local",
    isCurrent: false,
    health: "online",
    runtimeHealth: "ok",
    updateError: null,
    daemonTargetVersion: null,
    runtimes: [runtime],
    onlineCount: 1,
    issueCount: 0,
    runningCount: 0,
    queuedCount: 0,
    providerNames: ["claude"],
    lastSeenAt: "2026-08-01T00:00:00Z",
  };
}

describe("ComputersMachineDetail — display name rename entry", () => {
  it("keeps a single rename control on the hero title, not a Basics Display name row", () => {
    const client = new QueryClient({
      defaultOptions: { queries: { retry: false } },
    });
    render(
      <QueryClientProvider client={client}>
        <I18nProvider locale="en" resources={TEST_RESOURCES}>
          <ComputersMachineDetail
            machine={makeMachine()}
            agents={[]}
            snapshot={[]}
            now={Date.parse("2026-08-01T00:00:05Z")}
            wsId="ws-1"
            isMobile={false}
            actions={null}
            onBack={() => {}}
            headerActions={null}
            showBack={false}
            showListActions={false}
          />
        </I18nProvider>
      </QueryClientProvider>,
    );

    expect(screen.queryByText("Display name")).toBeNull();
    expect(screen.getByTestId("machine-name-editor-title")).toBeInTheDocument();
    expect(screen.queryByTestId("machine-name-editor-basics")).toBeNull();
  });

  it("shows the full Computer ID with a copy control (never truncated alone)", () => {
    const client = new QueryClient({
      defaultOptions: { queries: { retry: false } },
    });
    const machine = makeMachine();
    render(
      <QueryClientProvider client={client}>
        <I18nProvider locale="en" resources={TEST_RESOURCES}>
          <ComputersMachineDetail
            machine={machine}
            agents={[]}
            snapshot={[]}
            now={Date.parse("2026-08-01T00:00:05Z")}
            wsId="ws-1"
            isMobile={false}
            actions={null}
            onBack={() => {}}
            headerActions={null}
            showBack={false}
            showListActions={false}
          />
        </I18nProvider>
      </QueryClientProvider>,
    );

    expect(screen.getByTestId("machine-basics-computer-id")).toHaveTextContent(
      "daemon-1",
    );
    expect(screen.getByTestId("machine-basics-computer-id-copy")).toBeInTheDocument();
    // Truncated short form must not be the only representation.
    expect(screen.queryByText(/daemon-1…|…daemon/)).toBeNull();
  });
});

describe("ComputersMachineDetail — Agent Workspace scan", () => {
  it("refetches on every Rescan click after the initial scan", () => {
    refetchWorkspaces.mockClear();
    const client = new QueryClient({
      defaultOptions: { queries: { retry: false } },
    });
    render(
      <QueryClientProvider client={client}>
        <I18nProvider locale="en" resources={TEST_RESOURCES}>
          <ComputersMachineDetail
            machine={makeMachine()}
            agents={[]}
            snapshot={[]}
            now={Date.parse("2026-08-01T00:00:05Z")}
            wsId="ws-1"
            isMobile={false}
            actions={null}
            onBack={() => {}}
            headerActions={null}
            showBack={false}
            showListActions={false}
          />
        </I18nProvider>
      </QueryClientProvider>,
    );

    const rescan = screen.getByTestId("machine-scan-workspaces");
    fireEvent.click(rescan);
    fireEvent.click(rescan);
    fireEvent.click(rescan);

    expect(refetchWorkspaces).toHaveBeenCalledTimes(2);
  });
});

describe("ComputersMachineDetail — bulk runtime config", () => {
  it("opens one dialog and updates every selected agent", async () => {
    bulkUpdateAgentRuntimeConfig.mockClear();
    const client = new QueryClient({
      defaultOptions: { queries: { retry: false } },
    });
    const agents = [
      { id: "agent-1", runtime_id: "runtime-1", model: "opus" },
      { id: "agent-2", runtime_id: "runtime-1", model: "sonnet" },
    ] as Agent[];

    render(
      <QueryClientProvider client={client}>
        <I18nProvider locale="en" resources={TEST_RESOURCES}>
          <ComputersMachineDetail
            machine={makeMachine()}
            agents={agents}
            snapshot={[]}
            now={Date.parse("2026-08-01T00:00:05Z")}
            wsId="ws-1"
            isMobile={false}
            actions={null}
            onBack={() => {}}
            headerActions={null}
            showBack={false}
            showListActions={false}
          />
        </I18nProvider>
      </QueryClientProvider>,
    );

    fireEvent.click(screen.getByTestId("machine-agents-select"));
    expect(screen.queryByTestId("machine-agents-bulk-bar")).toBeNull();
    fireEvent.click(screen.getByTestId("machine-agents-select-all"));
    fireEvent.click(screen.getByTestId("machine-agents-bulk-config"));

    expect(screen.getByTestId("bulk-runtime-config-dialog")).toHaveTextContent(
      "2",
    );
    fireEvent.click(screen.getByRole("button", { name: "Apply bulk config" }));

    await waitFor(() =>
      expect(bulkUpdateAgentRuntimeConfig).toHaveBeenCalledTimes(1),
    );
    expect(bulkUpdateAgentRuntimeConfig).toHaveBeenCalledWith({
      agent_ids: ["agent-1", "agent-2"],
      runtime_id: "runtime-1",
      model: "target-model",
      thinking_level: "high",
    });
  });

  it("starts and stops every selected agent with one request per action", async () => {
    bulkAgentLifecycle.mockClear();
    const client = new QueryClient({
      defaultOptions: { queries: { retry: false } },
    });
    const agents = [
      { id: "agent-1", runtime_id: "runtime-1" },
      { id: "agent-2", runtime_id: "runtime-1" },
    ] as Agent[];

    render(
      <QueryClientProvider client={client}>
        <I18nProvider locale="en" resources={TEST_RESOURCES}>
          <ComputersMachineDetail
            machine={makeMachine()}
            agents={agents}
            snapshot={[]}
            now={Date.parse("2026-08-01T00:00:05Z")}
            wsId="ws-1"
            isMobile={false}
            actions={null}
            onBack={() => {}}
            headerActions={null}
            showBack={false}
            showListActions={false}
          />
        </I18nProvider>
      </QueryClientProvider>,
    );

    fireEvent.click(screen.getByTestId("machine-agents-select"));
    fireEvent.click(screen.getByTestId("machine-agents-select-all"));
    fireEvent.click(screen.getByTestId("machine-agents-bulk-start"));

    await waitFor(() => expect(bulkAgentLifecycle).toHaveBeenCalledTimes(1));
    expect(bulkAgentLifecycle).toHaveBeenCalledWith({
      agent_ids: ["agent-1", "agent-2"],
      action: "start",
    });

    bulkAgentLifecycle.mockClear();
    const stopButton = screen.getByTestId("machine-agents-bulk-stop");
    await waitFor(() => expect(stopButton).toBeEnabled());
    fireEvent.click(stopButton);
    await waitFor(() => expect(bulkAgentLifecycle).toHaveBeenCalledTimes(1));
    expect(bulkAgentLifecycle).toHaveBeenCalledWith({
      agent_ids: ["agent-1", "agent-2"],
      action: "stop",
    });
  });

  it("resets every selected agent in the chosen mode with one request", async () => {
    bulkAgentLifecycle.mockClear();
    const client = new QueryClient({
      defaultOptions: { queries: { retry: false } },
    });
    const agents = [
      { id: "agent-1", runtime_id: "runtime-1" },
      { id: "agent-2", runtime_id: "runtime-1" },
    ] as Agent[];

    render(
      <QueryClientProvider client={client}>
        <I18nProvider locale="en" resources={TEST_RESOURCES}>
          <ComputersMachineDetail
            machine={makeMachine()}
            agents={agents}
            snapshot={[]}
            now={Date.parse("2026-08-01T00:00:05Z")}
            wsId="ws-1"
            isMobile={false}
            actions={null}
            onBack={() => {}}
            headerActions={null}
            showBack={false}
            showListActions={false}
          />
        </I18nProvider>
      </QueryClientProvider>,
    );

    fireEvent.click(screen.getByTestId("machine-agents-select"));
    fireEvent.click(screen.getByTestId("machine-agents-select-all"));
    fireEvent.click(screen.getByTestId("machine-agents-bulk-restart"));
    fireEvent.click(screen.getByRole("radio", { name: /Reset session/ }));
    fireEvent.click(screen.getByRole("button", { name: "Apply to 2 agents" }));

    await waitFor(() => expect(bulkAgentLifecycle).toHaveBeenCalledTimes(1));
    expect(bulkAgentLifecycle).toHaveBeenCalledWith({
      agent_ids: ["agent-1", "agent-2"],
      action: "reset",
      mode: "session",
    });
  });
});
