// @vitest-environment jsdom

import { describe, it, expect, vi } from "vitest";
import { render, screen } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
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

vi.mock("@multica/core/api", () => ({
  api: {
    initiateUpdate: vi.fn(),
    getUpdateResult: vi.fn(),
    initiateRestart: vi.fn(),
    getRestart: vi.fn(),
  },
  ApiError: class ApiError extends Error {
    status: number;
    body: unknown;
    constructor(status: number, body: unknown) {
      super("api");
      this.status = status;
      this.body = body;
    }
  },
}));

import { MachineHeaderOps } from "./machine-header-ops";

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
  const now = Date.now();
  return render(
    <QueryClientProvider client={qc}>
      <I18nProvider locale="en" resources={TEST_RESOURCES}>
        <MachineHeaderOps machine={machine} now={now} />
      </I18nProvider>
    </QueryClientProvider>,
  );
}

describe("MachineHeaderOps (LRM-1036)", () => {
  it("puts ops in the header slot with ⋯ menu; no Actions card title", () => {
    renderOps(makeMachine());
    expect(screen.getByTestId("machine-header-ops")).toBeInTheDocument();
    expect(screen.queryByText("Actions")).not.toBeInTheDocument();
    expect(screen.getByTestId("machine-actions-menu-trigger")).toBeInTheDocument();
    // Up-to-date: no primary Upgrade button
    expect(screen.queryByTestId("machine-header-upgrade")).not.toBeInTheDocument();
  });

  it("shows Upgrade only when an update is available", () => {
    renderOps(
      makeMachine({
        updateTargetVersion: "1.5.0",
        runtimes: [
          makeRuntime({
            runtime_health: "update_available",
            target_version: "1.5.0",
          }),
        ],
      }),
    );
    expect(screen.getByTestId("machine-header-upgrade")).toHaveTextContent(
      /Upgrade daemon to v1\.5\.0/i,
    );
  });

  it("shows short disable reason inside ⋯ for desktop-managed restart", async () => {
    const user = userEvent.setup();
    renderOps(
      makeMachine({
        runtimes: [
          makeRuntime({
            metadata: { launched_by: "desktop" },
          }),
        ],
      }),
    );
    await user.click(screen.getByTestId("machine-actions-menu-trigger"));
    const reason = await screen.findByTestId("machine-ops-restart-reason");
    expect(reason).toHaveTextContent(/Desktop managed/i);
    const item = screen.getByTestId("machine-actions-restart");
    expect(
      item.getAttribute("data-disabled") !== null ||
        item.getAttribute("aria-disabled") === "true" ||
        (item as HTMLButtonElement).disabled,
    ).toBe(true);
  });
});
