// @vitest-environment jsdom

import { describe, it, expect, vi } from "vitest";
import { render, screen } from "@testing-library/react";
import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { I18nProvider } from "@multica/core/i18n/react";
import type { AgentRuntime } from "@multica/core/types";
import enCommon from "../../locales/en/common.json";
import enRuntimes from "../../locales/en/runtimes.json";
import type { RuntimeMachine } from "./runtime-machines";

vi.mock("@multica/core/auth", () => ({
  useAuthStore: (sel: (s: { user: { id: string } | null }) => unknown) =>
    sel({ user: { id: "user-mine" } }),
}));

vi.mock("@multica/core/hooks", () => ({
  useWorkspaceId: () => "ws-1",
}));

vi.mock("@multica/core/workspace/queries", () => ({
  memberListOptions: () => ({
    queryKey: ["members"],
    queryFn: async () => [
      { user_id: "user-mine", role: "member", user: { name: "Me" } },
    ],
  }),
}));

vi.mock("./update-section", () => ({
  UpdateSection: () => <div data-testid="update-section">UpdateSection</div>,
}));

vi.mock("./restart-section", () => ({
  RestartSection: ({ canRestart }: { canRestart: boolean }) => (
    <button type="button" disabled={!canRestart} data-testid="restart-section">
      Restart
    </button>
  ),
}));

vi.mock("./delete-computer-dialog", () => ({
  MachineDeleteControl: () => (
    <button type="button" data-testid="delete-computer-button">
      Delete computer
    </button>
  ),
}));

import { MachineOpsSection } from "./machine-ops-section";

const TEST_RESOURCES = { en: { common: enCommon, runtimes: enRuntimes } };

function makeRuntime(overrides: Partial<AgentRuntime> = {}): AgentRuntime {
  return {
    id: "rt-1",
    workspace_id: "ws-1",
    daemon_id: "daemon-1",
    name: "Cursor (host)",
    runtime_mode: "local",
    provider: "cursor",
    launch_header: "",
    status: "online",
    device_info: "host",
    metadata: {},
    current_version: "0.3.94",
    update_state: "idle",
    runtime_health: "ok",
    owner_id: "user-mine",
    visibility: "private",
    last_seen_at: "2026-08-01T00:00:00Z",
    created_at: "2026-01-01T00:00:00Z",
    updated_at: "2026-01-01T00:00:00Z",
    ...overrides,
  };
}

function makeMachine(
  overrides: Partial<RuntimeMachine> & { runtimes?: AgentRuntime[] } = {},
): RuntimeMachine {
  const runtimes = overrides.runtimes ?? [makeRuntime()];
  return {
    id: "m1",
    daemonId: "daemon-1",
    title: "s144",
    subtitle: null,
    deviceInfo: null,
    deviceName: "ubuntu",
    cliVersion: "0.3.94",
    mode: "local",
    section: "remote",
    isCurrent: false,
    health: "online",
    runtimeHealth: "ok",
    updateError: null,
    updateTargetVersion: null,
    runtimes,
    onlineCount: 1,
    issueCount: 0,
    runningCount: 0,
    queuedCount: 0,
    providerNames: ["cursor"],
    lastSeenAt: "2026-08-01T00:00:00Z",
    ...overrides,
  };
}

function renderOps(machine: RuntimeMachine) {
  const qc = new QueryClient({ defaultOptions: { queries: { retry: false } } });
  // Sample once outside JSX — react-doctor flags Date.now() in JSX for SSR
  // hydration mismatch; this file is RTL-only and never SSR'd.
  const now = Date.now();
  return render(
    <QueryClientProvider client={qc}>
      <I18nProvider locale="en" resources={TEST_RESOURCES}>
        <MachineOpsSection machine={machine} now={now} />
      </I18nProvider>
    </QueryClientProvider>,
  );
}

describe("MachineOpsSection", () => {
  it("renders a dedicated Actions zone with upgrade, restart, delete", () => {
    renderOps(makeMachine());
    expect(screen.getByTestId("machine-ops-section")).toBeInTheDocument();
    expect(screen.getByText("Actions")).toBeInTheDocument();
    expect(screen.getByTestId("update-section")).toBeInTheDocument();
    expect(screen.getByTestId("restart-section")).toBeEnabled();
    expect(screen.getByTestId("delete-computer-button")).toBeInTheDocument();
  });

  it("shows always-visible reason when restart is blocked (desktop-managed)", () => {
    renderOps(
      makeMachine({
        runtimes: [
          makeRuntime({
            metadata: { launched_by: "desktop" },
          }),
        ],
      }),
    );
    expect(screen.getByTestId("restart-section")).toBeDisabled();
    expect(screen.getByTestId("machine-ops-restart-reason")).toHaveTextContent(
      /desktop app/i,
    );
  });
});
