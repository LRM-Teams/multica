// @vitest-environment jsdom

import { describe, it, expect, vi } from "vitest";
import type { ReactElement } from "react";
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

vi.mock("@multica/core/api", () => ({
  api: {
    initiateMachineUpgrade: vi.fn(),
    getMachineUpgrade: vi.fn(),
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
import { MachineDaemonUpgrade } from "./machine-daemon-upgrade";
import { MachineDangerZone } from "./machine-danger-zone";

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
    daemonTargetVersion: null,
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

function wrap(ui: ReactElement) {
  const qc = new QueryClient({ defaultOptions: { queries: { retry: false } } });
  return render(
    <QueryClientProvider client={qc}>
      <I18nProvider locale="en" resources={TEST_RESOURCES}>
        {ui}
      </I18nProvider>
    </QueryClientProvider>,
  );
}

describe("MachineHeaderOps (LRM-1085 / v5)", () => {
  const now = Date.parse("2026-08-01T00:00:05Z");

  it("shows Restart outline only; no empty ⋯, Upgrade, or Delete in header", () => {
    wrap(<MachineHeaderOps machine={makeMachine()} now={now} />);
    expect(screen.getByTestId("machine-header-ops")).toBeInTheDocument();
    expect(screen.getByTestId("machine-header-restart")).toBeInTheDocument();
    expect(screen.queryByTestId("machine-actions-menu-trigger")).not.toBeInTheDocument();
    expect(screen.queryByTestId("machine-header-upgrade")).not.toBeInTheDocument();
    expect(screen.queryByText("Actions")).not.toBeInTheDocument();
  });

  it("keeps blocked Restart reason on the Restart title tooltip (no ⋯ shell)", () => {
    wrap(
      <MachineHeaderOps
        machine={makeMachine({
          runtimes: [makeRuntime({ metadata: { launched_by: "desktop" } })],
        })}
        now={now}
      />,
    );
    const restart = screen.getByTestId("machine-header-restart");
    expect(restart).toBeDisabled();
    expect(restart).toHaveAttribute("title", expect.stringMatching(/Desktop managed/i));
    expect(screen.queryByTestId("machine-actions-menu-trigger")).not.toBeInTheDocument();
  });
});

describe("MachineDaemonUpgrade (LRM-1071 / v5)", () => {
  it("shows version-only when up to date", () => {
    const runtime = makeRuntime();
    wrap(
      <MachineDaemonUpgrade
        runtime={runtime}
        cliVersion="0.3.94"
        daemonTargetVersion={null}
        updateError={null}
        isOnline
        canUpdate
      />,
    );
    expect(screen.getByTestId("machine-basics-daemon-version")).toHaveTextContent(
      "0.3.94",
    );
    expect(screen.queryByTestId("machine-daemon-upgrade-btn")).not.toBeInTheDocument();
  });

  it("ignores a failed machine upgrade whose target is older than the running version", () => {
    const runtime = makeRuntime({
      current_version: "0.4.18",
      runtime_health: "ok",
      machine_upgrade: {
        id: "machine-upgrade-1",
        daemon_id: "daemon-1",
        request_id: "request-1",
        requested_target: "v0.4.17",
        resolved_target: "v0.4.17",
        phase: "failed",
        error_message: "prepare journal: candidate version v0.4.13 is not staged",
        created_at: "2026-08-07T00:00:00Z",
        updated_at: "2026-08-07T00:00:01Z",
      },
    });

    wrap(
      <MachineDaemonUpgrade
        runtime={runtime}
        cliVersion="0.4.18"
        daemonTargetVersion="v0.4.17"
        updateError="prepare journal: candidate version v0.4.13 is not staged"
        isOnline
        canUpdate
      />,
    );

    expect(screen.getByTestId("machine-basics-daemon-version")).toHaveTextContent(
      "0.4.18",
    );
    expect(screen.queryByTestId("machine-basics-daemon-target")).not.toBeInTheDocument();
    expect(screen.queryByTestId("machine-daemon-upgrade-fail")).not.toBeInTheDocument();
    expect(screen.queryByTestId("machine-daemon-upgrade-error")).not.toBeInTheDocument();
  });

  it("shows outline Upgrade when an update is available", () => {
    const runtime = makeRuntime({
      runtime_health: "update_available",
      target_version: null,
    });
    wrap(
      <MachineDaemonUpgrade
        runtime={runtime}
        cliVersion="0.3.94"
        daemonTargetVersion="1.5.0"
        updateError={null}
        isOnline
        canUpdate
      />,
    );
    expect(screen.getByTestId("machine-daemon-upgrade-btn")).toHaveTextContent(
      /Upgrade to 1\.5\.0/,
    );
    expect(screen.getByTestId("machine-basics-daemon-version")).toHaveTextContent(
      "0.3.94",
    );
  });

  it("does not read an individual runtime target", () => {
    const runtime = makeRuntime({
      runtime_health: "update_available",
      target_version: "v0.4.14",
    });
    wrap(
      <MachineDaemonUpgrade
        runtime={runtime}
        cliVersion="0.4.13"
        daemonTargetVersion={null}
        updateError="drain_timeout"
        isOnline
        canUpdate
      />,
    );
    expect(screen.queryByTestId("machine-daemon-upgrade-btn")).not.toBeInTheDocument();
  });

  it("projects a sibling's queued machine upgrade as active", () => {
    const runtime = makeRuntime({
      runtime_health: "ok",
      target_version: null,
      machine_upgrade: {
        id: "machine-upgrade-1",
        daemon_id: "daemon-1",
        request_id: "request-1",
        requested_target: "0.4.0",
        phase: "queued",
        created_at: "2026-08-06T00:00:00Z",
        updated_at: "2026-08-06T00:00:00Z",
      },
    });
    wrap(
      <MachineDaemonUpgrade
        runtime={runtime}
        cliVersion="0.3.99"
        daemonTargetVersion={null}
        updateError={null}
        isOnline
        canUpdate
      />,
    );
    expect(screen.getByTestId("machine-daemon-upgrade")).toHaveAttribute("data-state", "active");
    expect(screen.getByTestId("machine-basics-daemon-target")).toHaveTextContent("0.4.0");
	    expect(screen.getByTestId("machine-daemon-upgrade-progress")).toHaveTextContent("Waiting for daemon to accept update…");
  });

  it("keeps rollback recovery active until every sibling has attested", () => {
    const runtime = makeRuntime({
      runtime_health: "ok",
      target_version: null,
      machine_upgrade: {
        id: "machine-upgrade-1",
        daemon_id: "daemon-1",
        request_id: "request-1",
        requested_target: "0.4.0",
        resolved_target: "0.4.0",
        phase: "rollback_pending",
        error_message: "candidate failed; restoring the previous version",
        created_at: "2026-08-06T00:00:00Z",
        updated_at: "2026-08-06T00:00:00Z",
      },
    });
    wrap(
      <MachineDaemonUpgrade
        runtime={runtime}
        cliVersion="0.3.99"
        daemonTargetVersion={null}
        updateError={null}
        isOnline
        canUpdate
      />,
    );
    expect(screen.getByTestId("machine-daemon-upgrade")).toHaveAttribute("data-state", "active");
    expect(screen.getByTestId("machine-basics-daemon-target")).toHaveTextContent("0.4.0");
  });

  it("labels a successor handoff as restarting instead of downloading", () => {
    const runtime = makeRuntime({
      runtime_health: "ok",
      target_version: null,
      machine_upgrade: {
        id: "machine-upgrade-1",
        daemon_id: "daemon-1",
        request_id: "request-1",
        requested_target: "0.4.0",
        resolved_target: "0.4.0",
        phase: "handoff",
        created_at: "2026-08-06T00:00:00Z",
        updated_at: "2026-08-06T00:00:00Z",
      },
    });
    wrap(
      <MachineDaemonUpgrade
        runtime={runtime}
        cliVersion="0.3.99"
        daemonTargetVersion={null}
        updateError={null}
        isOnline={false}
        canUpdate
      />,
    );
    expect(screen.getByTestId("machine-daemon-upgrade-progress")).toHaveTextContent(
      "Restarting to switch version…",
    );
  });

  it("hides stale handoff chrome once the running version matches the target", () => {
    const runtime = makeRuntime({
      runtime_health: "ok",
      machine_upgrade: {
        id: "machine-upgrade-1",
        daemon_id: "daemon-1",
        request_id: "request-1",
        requested_target: "v0.4.19",
        resolved_target: "v0.4.19",
        phase: "handoff",
        created_at: "2026-08-07T00:00:00Z",
        updated_at: "2026-08-07T00:00:01Z",
      },
    });
    wrap(
      <MachineDaemonUpgrade
        runtime={runtime}
        cliVersion="0.4.19"
        daemonTargetVersion="v0.4.19"
        updateError={null}
        isOnline
        canUpdate
      />,
    );

    expect(screen.getByTestId("machine-basics-daemon-version")).toHaveTextContent(
      "0.4.19",
    );
    expect(screen.queryByTestId("machine-basics-daemon-target")).not.toBeInTheDocument();
    expect(screen.queryByTestId("machine-daemon-upgrade-progress")).not.toBeInTheDocument();
  });
});

describe("MachineDangerZone (LRM-1071 / v5)", () => {
  it("hosts Delete computer (not the header)", () => {
    wrap(<MachineDangerZone machine={makeMachine()} />);
    expect(screen.getByTestId("machine-danger-zone")).toBeInTheDocument();
    expect(screen.getByTestId("machine-danger-delete")).toHaveTextContent(
      /Delete computer/i,
    );
  });

  it("shows delete for pending cloud computers owned by the caller", () => {
    wrap(
      <MachineDangerZone
        machine={makeMachine({
          daemonId: null,
          runtimes: [],
          pendingCloud: true,
          sandboxInstanceId: "sb-1",
          ownerUserId: "user-mine",
          title: "cloud-pending",
        })}
      />,
    );
    expect(screen.getByTestId("machine-danger-zone")).toBeInTheDocument();
    expect(screen.getByTestId("machine-danger-delete")).not.toBeDisabled();
    expect(screen.getByText(/cloud computer, its container/i)).toBeTruthy();
  });
});
