// @vitest-environment jsdom

import { describe, it, expect, vi, beforeEach } from "vitest";
import { render, screen, fireEvent } from "@testing-library/react";
import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { I18nProvider } from "@multica/core/i18n/react";
import type { AgentRuntime } from "@multica/core/types";
import enCommon from "../../locales/en/common.json";
import enRuntimes from "../../locales/en/runtimes.json";
import type { RuntimeMachine } from "./runtime-machines";

const mutate = vi.fn();

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
      { user_id: "user-other", role: "member", user: { name: "Other" } },
    ],
  }),
}));

vi.mock("@multica/core/runtimes/mutations", () => ({
  useUpdateRuntime: () => ({ mutate, isPending: false }),
}));

vi.mock("./provider-logo", () => ({
  ProviderLogo: () => <span data-testid="provider-logo" />,
}));

import { MachineSharingSection } from "./machine-sharing-section";

const TEST_RESOURCES = { en: { common: enCommon, runtimes: enRuntimes } };

function makeRuntime(
  overrides: Partial<AgentRuntime> & Pick<AgentRuntime, "id" | "provider">,
): AgentRuntime {
  return {
    workspace_id: "ws-1",
    daemon_id: "daemon-1",
    name: `${overrides.provider} (host)`,
    runtime_mode: "local",
    launch_header: "",
    status: "online",
    device_info: "host",
    metadata: {},
    current_version: "1.0.0",
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

function makeMachine(runtimes: AgentRuntime[]): RuntimeMachine {
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
    onlineCount: runtimes.length,
    issueCount: 0,
    runningCount: 0,
    queuedCount: 0,
    providerNames: runtimes.map((r) => r.provider),
    lastSeenAt: "2026-08-01T00:00:00Z",
  };
}

function renderSection(machine: RuntimeMachine) {
  const qc = new QueryClient({ defaultOptions: { queries: { retry: false } } });
  return render(
    <QueryClientProvider client={qc}>
      <I18nProvider locale="en" resources={TEST_RESOURCES}>
        <MachineSharingSection machine={machine} />
      </I18nProvider>
    </QueryClientProvider>,
  );
}

describe("MachineSharingSection", () => {
  beforeEach(() => {
    mutate.mockReset();
  });

  it("lists tool name + visibility without repeating owner prefix", () => {
    renderSection(
      makeMachine([
        makeRuntime({ id: "rt-c", provider: "cursor", visibility: "public" }),
        makeRuntime({ id: "rt-p", provider: "pi", visibility: "private" }),
      ]),
    );

    expect(screen.getByText("Sharing")).toBeInTheDocument();
    expect(screen.getByText("Cursor")).toBeInTheDocument();
    expect(screen.getByText("Pi")).toBeInTheDocument();
    expect(screen.queryByText(/Me's/i)).not.toBeInTheDocument();
    expect(screen.getByTestId("machine-sharing-toggle-rt-c")).toHaveTextContent(
      "Make private",
    );
  });

  it("shows the lock reason once at section level, not per row", () => {
    renderSection(
      makeMachine([
        makeRuntime({
          id: "rt-a",
          provider: "grok",
          owner_id: "user-other",
          visibility: "private",
        }),
        makeRuntime({
          id: "rt-b",
          provider: "pi",
          owner_id: "user-other",
          visibility: "public",
        }),
      ]),
    );

    expect(screen.getAllByTestId("machine-sharing-locked-reason")).toHaveLength(
      1,
    );
    expect(screen.getByTestId("machine-sharing-locked-reason")).toHaveTextContent(
      /Owner only/i,
    );
    expect(screen.getByTestId("machine-sharing-toggle-rt-a")).toBeDisabled();
    expect(screen.getByTestId("machine-sharing-toggle-rt-b")).toBeDisabled();
  });

  it("hides the lock reason when the viewer can edit", () => {
    renderSection(
      makeMachine([
        makeRuntime({ id: "rt-c", provider: "cursor", visibility: "private" }),
      ]),
    );
    expect(
      screen.queryByTestId("machine-sharing-locked-reason"),
    ).not.toBeInTheDocument();
  });

  it("flips visibility when editable", () => {
    renderSection(
      makeMachine([
        makeRuntime({ id: "rt-c", provider: "cursor", visibility: "private" }),
      ]),
    );

    fireEvent.click(screen.getByTestId("machine-sharing-toggle-rt-c"));
    expect(mutate).toHaveBeenCalledWith(
      { runtimeId: "rt-c", patch: { visibility: "public" } },
      expect.any(Object),
    );
  });
});
