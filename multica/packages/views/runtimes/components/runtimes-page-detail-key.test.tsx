// @vitest-environment jsdom

import { describe, it, expect, vi } from "vitest";
import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { fireEvent, render, screen } from "@testing-library/react";
import type { AgentRuntime } from "@multica/core/types";
import { I18nProvider } from "@multica/core/i18n/react";
import enCommon from "../../locales/en/common.json";
import enRuntimes from "../../locales/en/runtimes.json";
import enAgents from "../../locales/en/agents.json";
import type { RuntimeMachine } from "./runtime-machines";

const TEST_RESOURCES = {
  en: { common: enCommon, runtimes: enRuntimes, agents: enAgents },
};


const { FakeMachineHeaderOps } = vi.hoisted(() => {
  // eslint-disable-next-line @typescript-eslint/no-require-imports
  const React = require("react") as typeof import("react");
  function FakeMachineHeaderOps() {
    const [open, setOpen] = React.useState(false);
    return React.createElement(
      React.Fragment,
      null,
      React.createElement(
        "button",
        {
          type: "button",
          "data-testid": "delete-computer-button",
          onClick: () => setOpen(true),
        },
        "Delete",
      ),
      open
        ? React.createElement(
            "div",
            { "data-testid": "delete-computer-dialog" },
            "Permanently delete this computer?",
          )
        : null,
    );
  }
  return { FakeMachineHeaderOps };
});

vi.mock("@multica/core/auth", () => ({
  useAuthStore: (sel: (s: { user: { id: string } }) => unknown) =>
    sel({ user: { id: "user-me" } }),
}));

vi.mock("@multica/core/workspace/hooks", () => ({
  useActorName: () => ({
    getActorName: () => "Me",
  }),
}));

vi.mock("@multica/core/agents/stores", () => ({
  useAgentPanelStore: (sel: (s: { open: () => void }) => unknown) =>
    sel({ open: () => {} }),
}));

vi.mock("@multica/core/runtimes/mutations", () => ({
  useRuntimeAgentWorkspaces: () => ({ data: [], isFetching: false }),
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
}));

vi.mock("@multica/core/workspace/queries", () => ({
  agentListOptions: () => ({ queryKey: ["agents"] }),
  memberListOptions: () => ({
    queryKey: ["members"],
    queryFn: async () => [],
  }),
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

vi.mock("@multica/core/runtimes", () => ({
  deriveRuntimeHealth: () => "online",
  deriveRuntimeHealthPresentation: () => "ok",
}));

vi.mock("./machine-name-editor", () => ({
  MachineNameEditor: ({ machine }: { machine: RuntimeMachine }) => (
    <span data-testid="machine-title">{machine.title}</span>
  ),
}));

vi.mock("./machine-code-agents-section", () => ({
  MachineCodeAgentsSection: () => null,
}));


vi.mock("./machine-header-ops", () => ({
  MachineHeaderOps: FakeMachineHeaderOps,
}));

vi.mock("./machine-sharing-section", () => ({
  MachineSharingSection: () => null,
}));

vi.mock("../../common/actor-avatar", () => ({ ActorAvatar: () => null }));
vi.mock("../../common/actor-identity-row", () => ({
  ActorIdentityRow: () => null,
}));
vi.mock("../../navigation/app-link", () => ({
  AppLink: ({ children }: { children: React.ReactNode }) => <>{children}</>,
}));
vi.mock("sonner", () => ({
  toast: { error: vi.fn(), success: vi.fn() },
}));

import { ComputersMachineDetail } from "./runtimes-page";

function makeMachine(id: string, title: string): RuntimeMachine {
  const runtime: AgentRuntime = {
    id: `${id}-rt`,
    workspace_id: "ws-1",
    daemon_id: `${id}-daemon`,
    name: title,
    display_name: title,
    runtime_mode: "local",
    provider: "claude",
    launch_header: "",
    status: "online",
    device_info: title,
    metadata: {},
    current_version: "1.0.0",
    update_state: "idle",
    runtime_health: "ok",
    owner_id: "user-me",
    visibility: "private",
    last_seen_at: "2026-08-01T00:00:00Z",
    created_at: "2026-01-01T00:00:00Z",
    updated_at: "2026-01-01T00:00:00Z",
  };
  return {
    id,
    daemonId: `${id}-daemon`,
    title,
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
    updateTargetVersion: null,
    runtimes: [runtime],
    onlineCount: 1,
    issueCount: 0,
    runningCount: 0,
    queuedCount: 0,
    providerNames: ["claude"],
    lastSeenAt: "2026-08-01T00:00:00Z",
  };
}

function renderDetail(machine: RuntimeMachine) {
  const qc = new QueryClient({
    defaultOptions: { queries: { retry: false } },
  });
  return render(
    <QueryClientProvider client={qc}>
      <I18nProvider locale="en" resources={TEST_RESOURCES}>
        <ComputersMachineDetail
          machine={machine}
          agents={[]}
          snapshot={[]}
          now={Date.parse("2026-08-01T00:00:05Z")}
          wsId="ws-1"
          isMobile={false}
          onBack={() => {}}
          headerActions={null}
          showBack={false}
          showListActions={false}
        />
      </I18nProvider>
    </QueryClientProvider>,
  );
}

describe("ComputersMachineDetail — machine id remount", () => {
  it("closes the delete dialog when switching machines (key={machine.id})", () => {
    const machineA = makeMachine("machine-a", "Box Alpha");
    const machineB = makeMachine("machine-b", "Box Beta");
    const { rerender } = renderDetail(machineA);

    expect(
      screen.getAllByTestId("machine-title").every((el) => el.textContent === "Box Alpha"),
    ).toBe(true);

    fireEvent.click(screen.getByTestId("delete-computer-button"));
    expect(screen.getByTestId("delete-computer-dialog")).toBeInTheDocument();
    expect(
      screen.getByText("Permanently delete this computer?"),
    ).toBeInTheDocument();

    const qc = new QueryClient({
      defaultOptions: { queries: { retry: false } },
    });
    rerender(
      <QueryClientProvider client={qc}>
        <I18nProvider locale="en" resources={TEST_RESOURCES}>
          <ComputersMachineDetail
            machine={machineB}
            agents={[]}
            snapshot={[]}
            now={Date.parse("2026-08-01T00:00:05Z")}
            wsId="ws-1"
            isMobile={false}
            onBack={() => {}}
            headerActions={null}
            showBack={false}
            showListActions={false}
          />
        </I18nProvider>
      </QueryClientProvider>,
    );

    expect(screen.queryByTestId("delete-computer-dialog")).toBeNull();
    expect(
      screen.getAllByTestId("machine-title").every((el) => el.textContent === "Box Beta"),
    ).toBe(true);
  });
});
