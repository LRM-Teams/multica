// @vitest-environment jsdom

import { describe, it, expect, vi, beforeEach } from "vitest";
import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { render, screen, fireEvent } from "@testing-library/react";
import type { Agent, AgentRuntime } from "@multica/core/types";
import { I18nProvider } from "@multica/core/i18n/react";
import enCommon from "../../locales/en/common.json";
import enRuntimes from "../../locales/en/runtimes.json";
import enAgents from "../../locales/en/agents.json";

const TEST_RESOURCES = {
  en: { common: enCommon, runtimes: enRuntimes, agents: enAgents },
};

// ApiError mirrors the production export. The dialog's parseActiveAgentsConflict
// uses an `instanceof` check, so the class identity must match the one the
// mocked api throws. vi.hoisted is required because vi.mock is hoisted above
// imports — a top-level class declaration would not be visible to the mock
// factory at hoist time.
const { ApiError, apiDeleteRuntime, navPush } = vi.hoisted(() => {
  class ApiError extends Error {
    status: number;
    body: unknown;
    constructor(message: string, status: number, body: unknown) {
      super(message);
      this.status = status;
      this.body = body;
    }
  }
  return {
    ApiError,
    apiDeleteRuntime: vi.fn(),
    navPush: vi.fn(),
  };
});

vi.mock("@multica/core/api", () => ({
  api: {
    deleteRuntime: (...args: unknown[]) => apiDeleteRuntime(...args),
    listAgents: vi.fn(),
    listMembers: vi.fn(),
  },
  ApiError,
}));

vi.mock("@multica/core/runtimes/mutations", () => ({
  useDeleteRuntime: () => ({
    isPending: false,
    mutate: vi.fn(),
    mutateAsync: (...args: unknown[]) => apiDeleteRuntime(...args),
  }),
}));

vi.mock("@tanstack/react-query", async () => {
  const actual =
    await vi.importActual<typeof import("@tanstack/react-query")>(
      "@tanstack/react-query",
    );
  return {
    ...actual,
    // The dialog reads agentListOptions through useQuery. The default
    // returns an empty list — individual tests override mockUseQuery to
    // return populated agents when they want the blocked-by-agents step.
    useQuery: vi.fn(() => ({ data: [], isLoading: false })),
  };
});

vi.mock("@multica/core/agents", () => ({
  // Empty presence map keeps the cell renderers honest without dragging in
  // the full presence pipeline.
  useWorkspaceAgentPresence: () => ({ byAgent: new Map(), loading: false }),
  useAgentPresence: () => "online",
  useRunnerActivity: () => ({ data: undefined }),
  useRunnerActivitySummary: () => ({ data: undefined }),
}));

vi.mock("@multica/core/hooks", () => ({
  useWorkspaceId: () => "ws-1",
}));

vi.mock("@multica/core/paths", () => ({
  useCurrentWorkspace: () => ({ id: "ws-1" }),
  useWorkspacePaths: () => ({
    agentDetail: (id: string) => `/agents/${id}`,
  }),
}));

vi.mock("../../navigation/app-link", () => ({
  AppLink: ({
    href,
    children,
    className,
  }: {
    href: string;
    children: React.ReactNode;
    className?: string;
  }) => (
    <a href={href} className={className}>
      {children}
    </a>
  ),
}));

vi.mock("../../navigation", () => ({
  useNavigation: () => ({ push: navPush, replace: vi.fn() }),
}));

vi.mock("../../common/actor-avatar", () => ({ ActorAvatar: () => null }));
vi.mock("./provider-logo", () => ({
  ProviderLogo: () => null,
  knownProviderLabel: (p: string) => p,
}));
vi.mock("../../agents/presence", () => ({
  toLiveAvailability: (availability: string) => availability,
  formatPresenceStatus: (presence: string) =>
    presence === "online" ? "Online" : "Offline",
  presenceStatusVisual: () => ({ textClass: "" }),
  presenceStatusDotClass: () => "bg-success",
  availabilityConfig: {
    online: { dotClass: "", textClass: "" },
    offline: { dotClass: "", textClass: "" },
  },
  workloadConfig: {
    working: { icon: () => null, textClass: "" },
    queued: { icon: () => null, textClass: "" },
    idle: { icon: () => null, textClass: "" },
  },
}));

vi.mock("sonner", () => ({
  toast: { error: vi.fn(), success: vi.fn() },
}));

import { useQuery } from "@tanstack/react-query";
import { DeleteRuntimeDialog } from "./delete-runtime-dialog";

const mockedUseQuery = vi.mocked(useQuery);

// Fresh heartbeat so local+online runtimes still count as self-healing under
// deriveRuntimeHealth (stale/missing last_seen reads as Offline — LRM-437).
const FRESH_LAST_SEEN = new Date().toISOString();

function makeRuntime(overrides: Partial<AgentRuntime> = {}): AgentRuntime {
  return {
    id: "rt-1",
    workspace_id: "ws-1",
    daemon_id: null,
    name: "Cloud Runtime",
    runtime_mode: "cloud",
    provider: "claude",
    launch_header: "",
    status: "online",
    device_info: "",
    metadata: {},
    current_version: null,
    update_state: "idle",
    runtime_health: "ok",
    owner_id: "user-me",
    last_seen_at: null,
    created_at: "2026-01-01T00:00:00Z",
    updated_at: "2026-01-01T00:00:00Z",
    ...overrides,
  };
}

function makeLiveLocalRuntime(
  overrides: Partial<AgentRuntime> = {},
): AgentRuntime {
  return makeRuntime({
    runtime_mode: "local",
    status: "online",
    last_seen_at: FRESH_LAST_SEEN,
    ...overrides,
  });
}

function makeAgent(id: string, overrides: Partial<Agent> = {}): Agent {
  const name = overrides.name ?? `Agent ${id}`;
  return {
    id,
    workspace_id: "ws-1",
    workspace_role: "member",
    runtime_id: "rt-1",
    description: "",
    instructions: "",
    avatar_url: null,
    runtime_mode: "cloud",
    runtime_config: {},
    custom_args: [],
    status: "idle",
    max_concurrent_tasks: 1,
    model: "claude-sonnet-4-5",
    owner_id: "user-me",
    skills: [],
    created_at: "2026-01-01T00:00:00Z",
    updated_at: "2026-01-01T00:00:00Z",
    archived_at: null,
    archived_by: null,
    ...overrides,
    name,
    display_name: overrides.display_name ?? name,
  };
}

function renderDialog(opts: {
  runtime?: AgentRuntime;
  cachedAgents?: Agent[];
  onDeleted?: () => void;
} = {}) {
  const qc = new QueryClient({ defaultOptions: { queries: { retry: false } } });
  const onOpenChange = vi.fn();
  const onDeleted = opts.onDeleted ?? vi.fn();

  mockedUseQuery.mockImplementation(((queryArg: unknown) => {
    const q = queryArg as { queryKey?: readonly unknown[] };
    const key = q?.queryKey ?? [];
    const tail = key[key.length - 1];
    if (tail === "agents") {
      return { data: opts.cachedAgents ?? [], isLoading: false } as unknown as ReturnType<typeof useQuery>;
    }
    return { data: [], isLoading: false } as unknown as ReturnType<typeof useQuery>;
  }) as unknown as typeof useQuery);

  const tree = (runtime: AgentRuntime) => (
    <I18nProvider locale="en" resources={TEST_RESOURCES}>
      <QueryClientProvider client={qc}>
        <DeleteRuntimeDialog
          open
          onOpenChange={onOpenChange}
          runtime={runtime}
          wsId="ws-1"
          onDeleted={onDeleted}
        />
      </QueryClientProvider>
    </I18nProvider>
  );

  const initialRuntime = opts.runtime ?? makeRuntime();
  const utils = render(tree(initialRuntime));
  // Simulates the parent (list/detail page) re-rendering this same open
  // dialog with a fresh `runtime` prop, as it would after the runtime-list
  // query refetches from a poll-triggered invalidation — without remounting
  // the dialog.
  const rerenderWithRuntime = (next: AgentRuntime) => utils.rerender(tree(next));

  return { ...utils, onOpenChange, onDeleted, qc, rerenderWithRuntime };
}

describe("DeleteRuntimeDialog", () => {
  beforeEach(() => {
    vi.clearAllMocks();
  });

  it("renders the final confirm prompt when no agents are bound and the runtime isn't a self-healing local daemon", () => {
    renderDialog({ cachedAgents: [] });

    expect(screen.getByText("Delete Runtime?")).toBeInTheDocument();
    expect(screen.getByText("Delete runtime")).toBeInTheDocument();
    expect(screen.queryByText(/still on this runtime/)).not.toBeInTheDocument();
  });


  it("blocks deletion and lists bound agents with a link to each, instead of offering to cascade-archive them", () => {
    renderDialog({
      cachedAgents: [
        makeAgent("a-1", { name: "Alpha" }),
        makeAgent("a-2", { name: "Beta" }),
      ],
    });

    expect(
      screen.getByText(/2 agents are still on this runtime/),
    ).toBeInTheDocument();
    expect(screen.getByText("Alpha")).toBeInTheDocument();
    expect(screen.getByText("Beta")).toBeInTheDocument();
    // Never offers to archive/cascade — only a way out (Close) and a row
    // per agent (AgentActivityListItem) that navigates to handle it there.
    expect(
      screen.queryByRole("button", { name: /archive/i }),
    ).not.toBeInTheDocument();
    expect(screen.queryByRole("checkbox")).not.toBeInTheDocument();
    const alphaRow = screen
      .getAllByTestId("agent-activity-list-item")
      .find((el) => el.getAttribute("data-agent-id") === "a-1");
    expect(alphaRow).toBeTruthy();
    fireEvent.click(alphaRow!);
    expect(navPush).toHaveBeenCalledWith("/agents/a-1");
    expect(screen.getByRole("button", { name: "Close" })).toBeInTheDocument();
  });

  it("shows the stop-daemon guidance for a self-healing local runtime with no bound agents", () => {
    renderDialog({
      runtime: makeLiveLocalRuntime(),
      cachedAgents: [],
    });

    expect(screen.getByText("Stop the Computer first")).toBeInTheDocument();
    expect(screen.getByText("multica daemon stop")).toBeInTheDocument();
    expect(
      screen.queryByRole("button", { name: "Delete runtime" }),
    ).not.toBeInTheDocument();
  });

  it("allows final confirm when status is still online but heartbeat is stale (UI Offline) — LRM-437", () => {
    // Frank's Mac: Health column Offline, AGENTS —, raw status still "online"
    // because the daemon never reported disconnect. Must not trap in stop-daemon.
    const staleIso = new Date(Date.now() - 30 * 60_000).toISOString();
    renderDialog({
      runtime: makeRuntime({
        runtime_mode: "local",
        status: "online",
        last_seen_at: staleIso,
        name: "Claude (FrankAns-MacBook-Pro.local)",
      }),
      cachedAgents: [],
    });

    expect(screen.getByText("Delete Runtime?")).toBeInTheDocument();
    expect(
      screen.getByRole("button", { name: "Delete runtime" }),
    ).toBeInTheDocument();
    expect(screen.queryByText("Stop the Computer first")).not.toBeInTheDocument();
  });

  it("resolves the stop-daemon step's device label from the runtime's hostname suffix, not its provider-branded name", () => {
    renderDialog({
      runtime: makeLiveLocalRuntime({
        name: "Claude (build-server-01)",
      }),
      cachedAgents: [],
    });

    expect(screen.getByText("build-server-01")).toBeInTheDocument();
    expect(screen.queryByText("Claude (build-server-01)")).not.toBeInTheDocument();
  });

  it("falls back to device_info's leading segment when the runtime name has no hostname suffix", () => {
    renderDialog({
      runtime: makeLiveLocalRuntime({
        name: "Claude",
        device_info: "host.local · 2.1.121 (Claude Code)",
      }),
      cachedAgents: [],
    });

    expect(screen.getByText("host.local")).toBeInTheDocument();
  });

  it("polls for a runtime-list refresh while the stop-daemon step is showing, and auto-advances to the final confirm once the same open dialog receives an offline runtime", async () => {
    vi.useFakeTimers();
    try {
      const online = makeLiveLocalRuntime();
      const { qc, rerenderWithRuntime } = renderDialog({
        runtime: online,
        cachedAgents: [],
      });
      const invalidateSpy = vi.spyOn(qc, "invalidateQueries");

      expect(screen.getByText("Stop the Computer first")).toBeInTheDocument();
      expect(invalidateSpy).not.toHaveBeenCalled();

      await vi.advanceTimersByTimeAsync(4_000);
      expect(invalidateSpy).toHaveBeenCalledWith({
        queryKey: ["runtimes", "ws-1"],
      });

      // Simulates the poll-triggered refetch resolving to "offline" and the
      // parent re-rendering this SAME open dialog with the fresh runtime —
      // it must advance without the user reopening anything.
      rerenderWithRuntime({ ...online, status: "offline" });

      expect(screen.queryByText("Stop the Computer first")).not.toBeInTheDocument();
      expect(
        screen.getByRole("button", { name: "Delete runtime" }),
      ).toBeInTheDocument();
    } finally {
      vi.useRealTimers();
    }
  });

  it("prioritizes the agents-blocking step over the stop-daemon step when both apply", () => {
    renderDialog({
      runtime: makeLiveLocalRuntime(),
      cachedAgents: [makeAgent("a-1", { name: "Alpha" })],
    });

    expect(
      screen.getByText(/1 agent is still on this runtime/),
    ).toBeInTheDocument();
    expect(screen.queryByText("Stop the Computer first")).not.toBeInTheDocument();
  });

});
