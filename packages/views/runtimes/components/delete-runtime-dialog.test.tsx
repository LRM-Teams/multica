// @vitest-environment jsdom

import { describe, it, expect, vi, beforeEach } from "vitest";
import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { render, screen, fireEvent, waitFor } from "@testing-library/react";
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
const { ApiError, apiDeleteRuntime } = vi.hoisted(() => {
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
  useWorkspacePresenceMap: () => ({ byAgent: new Map(), loading: false }),
}));

vi.mock("@multica/core/paths", () => ({
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

vi.mock("../../common/actor-avatar", () => ({ ActorAvatar: () => null }));
vi.mock("../../agents/presence", () => ({
  availabilityConfig: {
    online: { dotClass: "", textClass: "" },
    unstable: { dotClass: "", textClass: "" },
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
    visibility: "private",
    last_seen_at: null,
    created_at: "2026-01-01T00:00:00Z",
    updated_at: "2026-01-01T00:00:00Z",
    ...overrides,
  };
}

function makeAgent(id: string, overrides: Partial<Agent> = {}): Agent {
  const name = overrides.name ?? `Agent ${id}`;
  return {
    id,
    workspace_id: "ws-1",
    runtime_id: "rt-1",
    description: "",
    instructions: "",
    avatar_url: null,
    runtime_mode: "cloud",
    runtime_config: {},
    custom_args: [],
    visibility: "private",
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

  const utils = render(
    <I18nProvider locale="en" resources={TEST_RESOURCES}>
      <QueryClientProvider client={qc}>
        <DeleteRuntimeDialog
          open
          onOpenChange={onOpenChange}
          runtime={opts.runtime ?? makeRuntime()}
          wsId="ws-1"
          onDeleted={onDeleted}
        />
      </QueryClientProvider>
    </I18nProvider>,
  );
  return { ...utils, onOpenChange, onDeleted };
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

  it("calls the strict DELETE and reports success", async () => {
    apiDeleteRuntime.mockResolvedValueOnce(undefined);
    const { onDeleted } = renderDialog({ cachedAgents: [] });

    fireEvent.click(screen.getByRole("button", { name: "Delete runtime" }));
    await waitFor(() => expect(apiDeleteRuntime).toHaveBeenCalledWith("rt-1"));
    await waitFor(() => expect(onDeleted).toHaveBeenCalled());
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
    // Never offers to archive/cascade — only a way out (Close) and a link
    // per agent to go handle it there.
    expect(
      screen.queryByRole("button", { name: /archive/i }),
    ).not.toBeInTheDocument();
    expect(screen.queryByRole("checkbox")).not.toBeInTheDocument();
    const alphaLink = screen.getByText("Alpha").closest("a");
    expect(alphaLink).toHaveAttribute("href", "/agents/a-1");
    expect(screen.getByRole("button", { name: "Close" })).toBeInTheDocument();
  });

  it("shows the stop-daemon guidance for a self-healing local runtime with no bound agents", () => {
    renderDialog({
      runtime: makeRuntime({ runtime_mode: "local", status: "online" }),
      cachedAgents: [],
    });

    expect(screen.getByText("Stop the daemon first")).toBeInTheDocument();
    expect(screen.getByText("multica daemon stop")).toBeInTheDocument();
    expect(
      screen.queryByRole("button", { name: "Delete runtime" }),
    ).not.toBeInTheDocument();
  });

  it("prioritizes the agents-blocking step over the stop-daemon step when both apply", () => {
    renderDialog({
      runtime: makeRuntime({ runtime_mode: "local", status: "online" }),
      cachedAgents: [makeAgent("a-1", { name: "Alpha" })],
    });

    expect(
      screen.getByText(/1 agent is still on this runtime/),
    ).toBeInTheDocument();
    expect(screen.queryByText("Stop the daemon first")).not.toBeInTheDocument();
  });

  it("falls back to the agents-blocking step when the strict DELETE returns runtime_has_active_agents", async () => {
    const fresh = makeAgent("a-9", { name: "FreshAgent" });
    apiDeleteRuntime.mockRejectedValueOnce(
      new ApiError("conflict", 409, {
        code: "runtime_has_active_agents",
        active_agents: [fresh],
      }),
    );

    renderDialog({ cachedAgents: [] });

    fireEvent.click(screen.getByRole("button", { name: "Delete runtime" }));

    await waitFor(() =>
      expect(
        screen.getByText(/1 agent is still on this runtime/),
      ).toBeInTheDocument(),
    );
    expect(screen.getByText("FreshAgent")).toBeInTheDocument();
    expect(
      screen.getByText(/Active agents were added since you opened this dialog/),
    ).toBeInTheDocument();
  });
});
