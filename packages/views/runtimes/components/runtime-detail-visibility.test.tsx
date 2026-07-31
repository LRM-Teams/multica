// @vitest-environment jsdom

import { describe, it, expect, vi, beforeEach } from "vitest";
import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { render, screen, fireEvent, waitFor, within } from "@testing-library/react";
import type { AgentRuntime } from "@multica/core/types";
import { I18nProvider } from "@multica/core/i18n/react";
import enCommon from "../../locales/en/common.json";
import enRuntimes from "../../locales/en/runtimes.json";
import enAgents from "../../locales/en/agents.json";

const TEST_RESOURCES = {
  en: { common: enCommon, runtimes: enRuntimes, agents: enAgents },
};

const { mockUpdateRuntime, mockQueryData } = vi.hoisted(() => ({
  mockUpdateRuntime: vi.fn(),
  mockQueryData: { value: [] as unknown[] },
}));

vi.mock("@multica/core/hooks", () => ({
  useWorkspaceId: () => "ws-1",
}));

vi.mock("@multica/core/api", () => ({
  api: {
    updateRuntime: (...args: unknown[]) => mockUpdateRuntime(...args),
    deleteRuntime: vi.fn(),
    archiveAgentsAndDeleteRuntime: vi.fn(),
  },
  ApiError: class ApiError extends Error {},
}));

vi.mock("sonner", () => ({
  toast: { error: vi.fn(), success: vi.fn() },
}));

// Pull the bits we want to test directly from the detail file. They aren't
// exported, so we exercise them through RuntimeDetail's DiagnosticsCard.
// Easier path: import the inner components by re-exporting them from a
// shared module. They live in the same file as RuntimeDetail; rather than
// touching the prod file just to ease testing, we test by rendering
// `RuntimeDetail` with a runtime fixture and asserting on the visibility
// UI. To avoid pulling in the entire detail page (which would need
// presence maps, member lists, paths, agents queries, etc.) we stub the
// heavy queries below.
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

vi.mock("@multica/core/runtimes", () => ({
  deriveRuntimeHealth: () => "online",
  runtimeCurrentVersion: () => "0.3.0",
  runtimeLaunchedBy: () => null,
  runtimeTargetVersion: () => null,
}));

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
  useUpdateRuntime: () => ({
    mutate: (
      args: { runtimeId: string; patch: Record<string, unknown> },
      opts?: { onSuccess?: () => void; onError?: () => void },
    ) => {
      mockUpdateRuntime(args.runtimeId, args.patch);
      opts?.onSuccess?.();
    },
    isPending: false,
  }),
  useDeleteRuntime: () => ({ mutate: vi.fn(), isPending: false, mutateAsync: vi.fn() }),
  useArchiveAgentsAndDeleteRuntime: () => ({
    mutate: vi.fn(),
    isPending: false,
    mutateAsync: vi.fn(),
  }),
  useRuntimeAgentWorkspaces: () => ({
    data: undefined,
    isFetching: false,
  }),
  useDeleteRuntimeAgentWorkspace: () => ({
    mutate: vi.fn(),
    isPending: false,
  }),
}));

// Stubbing ProviderLogo / UsageSection / UpdateSection avoids dragging in
// chart libs and additional query keys we don't care about here.
vi.mock("./provider-logo", () => ({ ProviderLogo: () => null }));
vi.mock("./update-section", () => ({ UpdateSection: () => null }));
vi.mock("./usage-section", () => ({ UsageSection: () => null }));
vi.mock("./shared", () => ({ HealthBadge: () => null }));
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

function privateChoice(root: HTMLElement = document.body) {
  return within(root).getAllByRole("button", { name: "Private" })[0]!;
}

function publicChoice(root: HTMLElement = document.body) {
  return within(root).getAllByRole("button", { name: "Public" })[0]!;
}

describe("RuntimeDetail visibility section", () => {
  beforeEach(() => {
    vi.clearAllMocks();
    mockQueryData.value = [];
  });

  it("shows owner-editable visibility choices when the caller owns the runtime", () => {
    const { container, unmount } = renderDetail(makeRuntime({ owner_id: "user-me" }));
    expect(within(container).queryByText("Visibility")).not.toBeNull();
    expect(privateChoice(container)).toBeTruthy();
    expect(publicChoice(container)).toBeTruthy();
    unmount();
  });

  it("flips visibility to public when the owner clicks the Public choice", async () => {
    const { container, unmount } = renderDetail(
      makeRuntime({ owner_id: "user-me", visibility: "private" }),
    );
    fireEvent.click(publicChoice(container));
    await waitFor(() =>
      expect(mockUpdateRuntime).toHaveBeenCalledWith("rt-1", { visibility: "public" }),
    );
    unmount();
  });

  it("renders a read-only visibility chip when the caller cannot edit", () => {
    const { container, unmount } = renderDetail(
      makeRuntime({ owner_id: "someone-else", visibility: "public" }),
    );
    expect(within(container).getAllByText("Public").length).toBeGreaterThan(0);
    expect(
      within(container).queryAllByRole("button", { name: "Private" }),
    ).toHaveLength(0);
    unmount();
  });

  it("lets an admin manage another runtime but hides its delete action", () => {
    mockQueryData.value = [
      { user_id: "user-me", role: "admin", display_name: "Admin" },
    ];
    const { container, unmount } = renderDetail(
      makeRuntime({ owner_id: "someone-else", visibility: "private" }),
    );

    expect(privateChoice(container)).toBeTruthy();
    expect(publicChoice(container)).toBeTruthy();
    expect(
      screen.queryByRole("button", { name: /delete runtime/i }),
    ).toBeNull();
    unmount();
  });
});
