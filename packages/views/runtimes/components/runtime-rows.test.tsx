// @vitest-environment jsdom

import { describe, it, expect, vi, beforeEach } from "vitest";
import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { fireEvent, render, screen } from "@testing-library/react";
import type { AgentRuntime } from "@multica/core/types";
import { I18nProvider } from "@multica/core/i18n/react";
import enCommon from "../../locales/en/common.json";
import enRuntimes from "../../locales/en/runtimes.json";
import enAgents from "../../locales/en/agents.json";

const TEST_RESOURCES = {
  en: { common: enCommon, runtimes: enRuntimes, agents: enAgents },
};

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

vi.mock("@multica/core/hooks", () => ({
  useWorkspaceId: () => "ws-1",
}));

vi.mock("@multica/core/workspace/queries", () => ({
  agentListOptions: () => ({ queryKey: ["agents"] }),
  memberListOptions: () => ({ queryKey: ["members"] }),
}));

vi.mock("@multica/core/agents", () => ({
  agentTaskSnapshotOptions: () => ({ queryKey: ["snapshot"] }),
  deriveWorkload: (c: { runningCount: number; queuedCount: number }) =>
    c.runningCount > 0 ? "working" : c.queuedCount > 0 ? "queued" : "idle",
}));

vi.mock("@multica/core/runtimes", () => ({
  deriveRuntimeHealth: (rt: AgentRuntime) =>
    rt.status === "online" ? "online" : "offline",
  deriveRuntimeHealthPresentation: (rt: AgentRuntime) =>
    rt.runtime_health ?? "ok",
}));

vi.mock("@multica/core/auth", () => ({
  useAuthStore: (sel: (s: { user: { id: string } }) => unknown) =>
    sel({ user: { id: "user-me" } }),
}));

const mockPush = vi.fn();
vi.mock("../../navigation", () => ({
  useNavigation: () => ({ push: mockPush }),
}));

vi.mock("@multica/core/paths", () => ({
  useWorkspaceSlug: () => "acme",
  paths: {
    workspace: () => ({
      computers: () => "/acme/computers",
      computerDetail: (id: string) => `/acme/computers/${id}`,
    }),
  },
}));

// The workload icon config pulls in the presence subsystem; the row only
// needs an icon component + text class per state.
vi.mock("../../agents/presence", () => ({
  workloadConfig: {
    idle: { icon: () => null, textClass: "text-muted-foreground" },
    working: { icon: () => null, textClass: "text-primary" },
    queued: { icon: () => null, textClass: "text-warning" },
  },
}));

vi.mock("../../common/actor-avatar", () => ({ ActorAvatar: () => null }));
vi.mock("./provider-logo", () => ({ ProviderLogo: () => null }));
vi.mock("./delete-runtime-dialog", () => ({ DeleteRuntimeDialog: () => null }));
vi.mock("sonner", () => ({
  toast: { error: vi.fn(), success: vi.fn() },
}));

import { RuntimeRows } from "./runtime-list";

function makeRuntime(overrides: Partial<AgentRuntime> = {}): AgentRuntime {
  return {
    id: "rt-1",
    workspace_id: "ws-1",
    daemon_id: null,
    name: "Wendy (host-01)",
    runtime_mode: "local",
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

function renderRows(runtimes: AgentRuntime[]) {
  const qc = new QueryClient({ defaultOptions: { queries: { retry: false } } });
  const now = Date.now();
  return render(
    <I18nProvider locale="en" resources={TEST_RESOURCES}>
      <QueryClientProvider client={qc}>
        <RuntimeRows runtimes={runtimes} now={now} />
      </QueryClientProvider>
    </I18nProvider>,
  );
}

describe("RuntimeRows (LRM-745 row cards)", () => {
  beforeEach(() => vi.clearAllMocks());

  it("renders one row per runtime with the base name (host suffix stripped)", () => {
    renderRows([
      makeRuntime({ id: "rt-1", name: "Wendy (host-01)" }),
      makeRuntime({ id: "rt-2", name: "Morgan (host-01)" }),
    ]);
    expect(screen.getByText("Wendy")).toBeInTheDocument();
    expect(screen.getByText("Morgan")).toBeInTheDocument();
    expect(screen.queryByText(/host-01/)).not.toBeInTheDocument();
  });

  it("navigates to the runtime detail route on row click", () => {
    renderRows([makeRuntime({ id: "rt-9" })]);
    fireEvent.click(screen.getByText("Wendy"));
    expect(mockPush).toHaveBeenCalledWith("/acme/computers");
  });

  it("shows the delete kebab only for runtimes the current user owns", () => {
    renderRows([
      makeRuntime({ id: "rt-mine", name: "Mine (h)", owner_id: "user-me" }),
      makeRuntime({ id: "rt-theirs", name: "Theirs (h)", owner_id: "user-other" }),
    ]);
    expect(screen.getAllByLabelText("Row actions")).toHaveLength(1);
  });

  it("surfaces an incremental update badge instead of the idle status", () => {
    renderRows([
      makeRuntime({ id: "rt-1", runtime_health: "update_available" }),
    ]);
    expect(screen.getByText("Update available")).toBeInTheDocument();
    expect(screen.queryByText("Idle")).not.toBeInTheDocument();
  });

  it("shows connectivity for offline runtimes, including last seen", () => {
    renderRows([
      makeRuntime({
        id: "rt-1",
        status: "offline",
        last_seen_at: new Date(Date.now() - 4 * 60_000).toISOString(),
      }),
    ]);
    expect(screen.getByText(/Offline/)).toBeInTheDocument();
    expect(screen.getByText(/ago/)).toBeInTheDocument();
  });

  it("renders the empty hint when the machine has no runtimes", () => {
    renderRows([]);
    expect(
      screen.getByText("No runtimes on this machine yet."),
    ).toBeInTheDocument();
  });
});
