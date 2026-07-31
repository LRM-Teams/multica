// @vitest-environment jsdom

import { describe, it, expect, vi, beforeEach, afterEach } from "vitest";
import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { render, within } from "@testing-library/react";
import type { AgentRuntime } from "@multica/core/types";
import { I18nProvider } from "@multica/core/i18n/react";
import enCommon from "../../locales/en/common.json";
import enRuntimes from "../../locales/en/runtimes.json";
import enAgents from "../../locales/en/agents.json";

const TEST_RESOURCES = {
  en: { common: enCommon, runtimes: enRuntimes, agents: enAgents },
};

const { mockQueryData } = vi.hoisted(() => ({
  mockQueryData: { value: [] as unknown[] },
}));

vi.mock("@multica/core/hooks", () => ({
  useWorkspaceId: () => "ws-1",
}));

vi.mock("@multica/core/api", () => ({
  api: {
    updateRuntime: vi.fn(),
    deleteRuntime: vi.fn(),
    archiveAgentsAndDeleteRuntime: vi.fn(),
    initiateRestart: vi.fn(),
    getRestart: vi.fn(),
  },
  ApiError: class ApiError extends Error {},
}));

vi.mock("sonner", () => ({
  toast: { error: vi.fn(), success: vi.fn() },
}));

vi.mock("@tanstack/react-query", async () => {
  const actual =
    await vi.importActual<typeof import("@tanstack/react-query")>(
      "@tanstack/react-query",
    );
  return {
    ...actual,
    useQuery: vi.fn(() => ({ data: mockQueryData.value, isLoading: false })),
  };
});

vi.mock("@multica/core/auth", () => ({
  useAuthStore: (sel: (s: { user: { id: string } }) => unknown) =>
    sel({ user: { id: "user-me" } }),
}));

// Deliberately real: `deriveRuntimeHealth`/`isSandboxRuntime` are exactly
// the derivation this test exists to exercise, not a value to fake out.
vi.mock("@multica/core/runtimes", async () => {
  const actual =
    await vi.importActual<typeof import("@multica/core/runtimes")>(
      "@multica/core/runtimes",
    );
  return {
    ...actual,
    runtimeCurrentVersion: () => "0.3.0",
    runtimeLaunchedBy: () => null,
    runtimeTargetVersion: () => null,
  };
});

vi.mock("@multica/core/agents", () => ({
  useWorkspacePresenceMap: () => ({ byAgent: new Map() }),
}));

vi.mock("@multica/core/paths", () => ({
  useWorkspacePaths: () => ({
    runtimes: () => "/runtimes",
    agentDetail: () => "/agents",
  }),
}));

vi.mock("@multica/core/runtimes/mutations", () => ({
  useUpdateRuntime: () => ({ mutate: vi.fn(), isPending: false }),
  useDeleteRuntime: () => ({ mutate: vi.fn(), isPending: false, mutateAsync: vi.fn() }),
  useArchiveAgentsAndDeleteRuntime: () => ({
    mutate: vi.fn(),
    isPending: false,
    mutateAsync: vi.fn(),
  }),
  useRuntimeAgentWorkspaces: () => ({ data: undefined, isFetching: false }),
  useDeleteRuntimeAgentWorkspace: () => ({ mutate: vi.fn(), isPending: false }),
}));

vi.mock("./provider-logo", () => ({ ProviderLogo: () => null }));
vi.mock("./usage-section", () => ({ UsageSection: () => null }));
vi.mock("./shared", () => ({ HealthBadge: () => null }));
// Update section is unrelated to this regression — stub it so the test
// only exercises the isOnline plumbing this file cares about.
vi.mock("./update-section", () => ({ UpdateSection: () => null }));
vi.mock("../../agents/presence", () => ({
  availabilityConfig: { offline: { dotClass: "", textClass: "" } },
  workloadConfig: { idle: { icon: () => null, textClass: "" } },
}));
vi.mock("../../common/actor-avatar", () => ({ ActorAvatar: () => null }));
vi.mock("../../navigation/app-link", () => ({ AppLink: () => null }));
vi.mock("../../navigation/context", () => ({
  useNavigation: () => ({ push: vi.fn(), replace: vi.fn() }),
}));

import { RuntimeDetail } from "./runtime-detail";

function makeRuntime(overrides: Partial<AgentRuntime>): AgentRuntime {
  return {
    id: "rt-1",
    workspace_id: "ws-1",
    daemon_id: null,
    name: "Local Runtime",
    runtime_mode: "local",
    provider: "claude",
    launch_header: "",
    status: "online",
    device_info: "host.local",
    metadata: {},
    current_version: null,
    update_state: "idle",
    runtime_health: "ok",
    owner_id: "user-me",
    visibility: "private",
    last_seen_at: "2026-04-27T11:59:50Z",
    created_at: "2026-04-01T00:00:00Z",
    updated_at: "2026-04-01T00:00:00Z",
    ...overrides,
  };
}

function renderDetail(runtime: AgentRuntime) {
  const qc = new QueryClient({ defaultOptions: { queries: { retry: false } } });
  const view = render(
    <I18nProvider locale="en" resources={TEST_RESOURCES}>
      <QueryClientProvider client={qc}>
        <RuntimeDetail runtime={runtime} />
      </QueryClientProvider>
    </I18nProvider>,
  );
  return { ...view, unmount: () => view.unmount() };
}

describe("RuntimeDetail diagnostics — isOnline follows derived health, not raw status", () => {
  const fixedNow = Date.parse("2026-04-27T20:00:00Z");

  beforeEach(() => {
    vi.clearAllMocks();
    mockQueryData.value = [];
    vi.useFakeTimers();
    vi.setSystemTime(fixedNow);
  });

  afterEach(() => {
    vi.useRealTimers();
  });

  it("disables the Restart action when status=online but the heartbeat is 8 hours stale", () => {
    // Same shape as the real bug: the server's `status` column hasn't
    // flipped to offline yet, but `last_seen_at` is far past the
    // recently_lost/offline windows deriveRuntimeHealth applies.
    const eightHoursAgo = new Date(fixedNow - 8 * 60 * 60 * 1000).toISOString();
    const { container, unmount } = renderDetail(
      makeRuntime({ status: "online", last_seen_at: eightHoursAgo }),
    );
    const restartButton = within(container).getByRole("button", {
      name: /restart/i,
    });
    expect(restartButton).toBeDisabled();
    unmount();
  });

  it("enables the Restart action when status=online and the heartbeat is fresh", () => {
    const { container, unmount } = renderDetail(
      makeRuntime({
        status: "online",
        last_seen_at: new Date(fixedNow - 5_000).toISOString(),
      }),
    );
    const restartButton = within(container).getByRole("button", {
      name: /restart/i,
    });
    expect(restartButton).not.toBeDisabled();
    unmount();
  });
});
