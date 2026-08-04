// @vitest-environment jsdom

import { describe, it, expect, vi } from "vitest";
import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { fireEvent, render, screen } from "@testing-library/react";
import type { Agent, AgentRuntime, AgentTask } from "@multica/core/types";
import { I18nProvider } from "@multica/core/i18n/react";
import enCommon from "../../locales/en/common.json";
import enRuntimes from "../../locales/en/runtimes.json";
import enAgents from "../../locales/en/agents.json";
import type { RuntimeMachine } from "./runtime-machines";

const TEST_RESOURCES = {
  en: { common: enCommon, runtimes: enRuntimes, agents: enAgents },
};

const openAgentPanel = vi.fn();

vi.mock("@multica/core/auth", () => ({
  useAuthStore: (sel: (s: { user: { id: string } }) => unknown) =>
    sel({ user: { id: "user-me" } }),
}));

vi.mock("@multica/core/workspace/hooks", () => ({
  useActorName: () => ({
    getActorName: () => "jianghp3's helper",
  }),
}));


const presenceWorkload = vi.hoisted(() => ({ current: "idle" as string }));

vi.mock("@multica/core/agents", async () => {
  const actual = await vi.importActual<typeof import("@multica/core/agents")>(
    "@multica/core/agents",
  );
  return {
    ...actual,
    useWorkspacePresenceMap: () => ({
      byAgent: new Map([
        [
          "agent-1",
          {
            agent_id: "agent-1",
            availability: "online",
            workload: presenceWorkload.current,
          },
        ],
      ]),
      loading: false,
    }),
  };
});

vi.mock("@multica/core/agents/stores", () => ({
  useAgentPanelStore: (sel: (s: { open: (id: string) => void }) => unknown) =>
    sel({ open: openAgentPanel }),
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
  MachineHeaderOps: () => null,
}));

vi.mock("./machine-daemon-upgrade", () => ({
  MachineDaemonUpgrade: () => null,
}));

vi.mock("./machine-danger-zone", () => ({
  MachineDangerZone: () => null,
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

function makeMachine(runtimeOverrides: Partial<AgentRuntime> = {}): RuntimeMachine {
  const runtime: AgentRuntime = {
    id: "runtime-1",
    workspace_id: "ws-1",
    daemon_id: "daemon-1",
    name: "Box",
    display_name: "Box",
    runtime_mode: "local",
    provider: "cursor",
    launch_header: "",
    status: "online",
    device_info: "Box",
    metadata: {},
    current_version: "1.0.0",
    update_state: "idle",
    runtime_health: "ok",
    owner_id: "user-me",
    last_seen_at: "2026-08-01T00:00:00Z",
    created_at: "2026-01-01T00:00:00Z",
    updated_at: "2026-01-01T00:00:00Z",
    ...runtimeOverrides,
  };
  return {
    id: "machine-1",
    daemonId: "daemon-1",
    title: "Box",
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
    providerNames: ["cursor"],
    lastSeenAt: "2026-08-01T00:00:00Z",
  };
}

function makeAgent(overrides: Partial<Agent> = {}): Agent {
  return {
    id: "agent-1",
    workspace_id: "ws-1",
    runtime_id: "runtime-1",
    name: "jianghp3-helper",
    display_name: "jianghp3's helper",
    description: "",
    instructions: "",
    avatar_url: null,
    runtime_mode: "local",
    runtime_config: {},
    custom_args: [],
    status: "idle",
    max_concurrent_tasks: 1,
    model: "auto",
    owner_id: "user-me",
    skills: [],
    created_at: "2026-01-01T00:00:00Z",
    updated_at: "2026-01-01T00:00:00Z",
    archived_at: null,
    archived_by: null,
    ...overrides,
  } as Agent;
}

function renderDetail(
  machine: RuntimeMachine,
  agents: Agent[],
  snapshot: AgentTask[] = [],
) {
  const qc = new QueryClient({
    defaultOptions: { queries: { retry: false } },
  });
  return render(
    <QueryClientProvider client={qc}>
      <I18nProvider locale="en" resources={TEST_RESOURCES}>
        <ComputersMachineDetail
          machine={machine}
          agents={agents}
          snapshot={snapshot}
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

// task #7 follow-up (08-02): the desktop Agent list is a flat one-line-per-
// agent list, not a 4-column table, and never shows a duration.
describe("ComputersMachineDetail — desktop agent list", () => {
  it("shows name · runtime · activity on one line, opens the panel on click", () => {
    presenceWorkload.current = "idle";
    renderDetail(makeMachine(), [makeAgent()]);

    const row = screen.getByRole("button", { name: /jianghp3's helper/ });
    expect(row).toHaveTextContent("Cursor");
    expect(row).toHaveTextContent("Idle");

    fireEvent.click(row);
    expect(openAgentPanel).toHaveBeenCalledWith("agent-1");
  });

  it("shows Working (Activity band) while a task is active", () => {
    presenceWorkload.current = "working";
    renderDetail(makeMachine(), [makeAgent()]);

    expect(
      screen.getByRole("button", { name: /jianghp3's helper/ }),
    ).toHaveTextContent("Working");
    presenceWorkload.current = "idle";
  });

  it("never shows a duration (no table headers, no relative-time text)", () => {
    renderDetail(makeMachine(), [makeAgent()]);

    // The old 4-column table headers must be gone.
    expect(screen.queryByText("Host runtime")).toBeNull();
    expect(screen.queryByText("Code agent")).toBeNull();
    expect(screen.queryByRole("table")).toBeNull();
    // Frank 08-02: no duration text alongside the activity word.
    expect(screen.queryByText(/ago$/)).toBeNull();
  });
});

// Task #81 (Iris/Parker/Dax/Wren, 08-02): pin is only the daemon's locally-
// recorded MULTICA_PINNED_VERSION intent — the server doesn't enforce it
// against a server-initiated update yet. Label ("Version pin") and value
// ("vX.Y") must both read as a fact, never a promise ("locked"/"pinned"/
// "won't be upgraded") — a lock-flavored label next to a factual value would
// undercut the value's own restraint. Only "has a non-blank pin" renders
// anything; the other cases (no pin, missing field, empty/whitespace value)
// must add nothing to the page.
describe("ComputersMachineDetail — Basics pinned-version row (task #81)", () => {
  it("shows the pinned-version row when the primary runtime has one", () => {
    renderDetail(makeMachine({ pinned_version: "0.3.85" }), []);
    expect(screen.getByText("Version pin")).toBeInTheDocument();
    expect(screen.getByTestId("machine-basics-pinned-version")).toHaveTextContent(
      "v0.3.85",
    );
  });

  it("renders nothing when pinned_version is null", () => {
    renderDetail(makeMachine({ pinned_version: null }), []);
    expect(screen.queryByTestId("machine-basics-pinned-version")).toBeNull();
    expect(screen.queryByText("Version pin")).toBeNull();
  });

  it("renders nothing when pinned_version is absent (older backend)", () => {
    renderDetail(makeMachine(), []);
    expect(screen.queryByTestId("machine-basics-pinned-version")).toBeNull();
  });

  it("renders nothing when pinned_version is an empty string", () => {
    renderDetail(makeMachine({ pinned_version: "" }), []);
    expect(screen.queryByTestId("machine-basics-pinned-version")).toBeNull();
  });

  it("renders nothing when pinned_version is whitespace-only", () => {
    renderDetail(makeMachine({ pinned_version: "   " }), []);
    expect(screen.queryByTestId("machine-basics-pinned-version")).toBeNull();
  });
});
