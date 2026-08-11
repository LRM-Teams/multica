// @vitest-environment jsdom

import { describe, it, expect, vi } from "vitest";
import { render, screen } from "@testing-library/react";
import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import type { AgentRuntime } from "@multica/core/types";
import { I18nProvider } from "@multica/core/i18n/react";
import enCommon from "../../locales/en/common.json";
import enRuntimes from "../../locales/en/runtimes.json";
import type { RuntimeMachine } from "./runtime-machines";
import { MachineCodeAgentsSection } from "./machine-code-agents-section";

const TEST_RESOURCES = {
  en: { common: enCommon, runtimes: enRuntimes },
};

vi.mock("@multica/core/auth", () => ({
  useAuthStore: (sel: (s: { user: { id: string } | null }) => unknown) =>
    sel({ user: { id: "user-1" } }),
}));

vi.mock("@multica/core/hooks", () => ({
  useWorkspaceId: () => "ws-1",
}));

vi.mock("@multica/core/workspace/queries", () => ({
  memberListOptions: () => ({
    queryKey: ["members"],
    queryFn: async () => [
      { user_id: "user-1", role: "member", user: { name: "Me" } },
    ],
  }),
}));

vi.mock("@multica/core/runtimes/mutations", () => ({
  useUpdateRuntime: () => ({ mutate: vi.fn(), isPending: false }),
}));

vi.mock("./provider-logo", async (importOriginal) => {
  const actual = await importOriginal<typeof import("./provider-logo")>();
  return {
    ...actual,
    ProviderLogo: ({ provider }: { provider: string }) => (
      <span data-testid={`provider-logo-${provider}`} />
    ),
  };
});

function makeRuntime(
  partial: Partial<AgentRuntime> & { provider: string },
): AgentRuntime {
  return {
    id: `rt-${partial.provider}`,
    workspace_id: "ws-1",
    daemon_id: "daemon-1",
    name: partial.provider,
    runtime_mode: "local",
    launch_header: "",
    status: "online",
    device_info: "box",
    metadata: {},
    current_version: null,
    update_state: "idle",
    runtime_health: "ok",
    owner_id: "user-1",
    visibility: "private",
    last_seen_at: null,
    created_at: "2026-01-01T00:00:00Z",
    updated_at: "2026-01-01T00:00:00Z",
    ...partial,
  };
}

function makeMachine(runtimes: AgentRuntime[]): RuntimeMachine {
  return {
    id: "machine-1",
    daemonId: "daemon-1",
    title: "Test box",
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
    daemonTargetVersion: null,
    runtimes,
    onlineCount: runtimes.length,
    issueCount: 0,
    runningCount: 0,
    queuedCount: 0,
    providerNames: [...new Set(runtimes.map((r) => r.provider))],
    lastSeenAt: "2026-08-01T00:00:00Z",
  };
}

function renderSection(machine: RuntimeMachine) {
  const qc = new QueryClient({ defaultOptions: { queries: { retry: false } } });
  return render(
    <QueryClientProvider client={qc}>
      <I18nProvider locale="en" resources={TEST_RESOURCES}>
        <MachineCodeAgentsSection machine={machine} />
      </I18nProvider>
    </QueryClientProvider>,
  );
}

describe("MachineCodeAgentsSection", () => {
  it("renders A2 status: green-dot version, no Installed/Supported copy (LRM-1108)", () => {
    renderSection(
      makeMachine([
        makeRuntime({
          provider: "cursor",
          metadata: { version: "1.4.2" },
        }),
      ]),
    );

    expect(
      screen.getByRole("heading", { name: "Runtimes on this computer" }),
    ).toBeInTheDocument();
    // A2.1: no section-header installed/supported count.
    expect(screen.queryByText(/^\d+ \/ \d+$/)).not.toBeInTheDocument();
    expect(screen.getByText("v1.4.2")).toBeInTheDocument();
    expect(screen.queryByText(/Installed ·/)).not.toBeInTheDocument();
    expect(screen.queryByText("Installed")).not.toBeInTheDocument();
    expect(
      screen.queryByText("Supported · not installed"),
    ).not.toBeInTheDocument();
    expect(screen.getByTestId("provider-logo-cursor")).toBeInTheDocument();
    expect(screen.getByText("Grok Build")).toBeInTheDocument();
    expect(
      screen.getAllByText("View install guide").length,
    ).toBeGreaterThan(0);
  });

  it("puts Make public / Make private on installed runtime cards (LRM-1071)", () => {
    renderSection(
      makeMachine([
        makeRuntime({
          provider: "cursor",
          metadata: { version: "1.4.2" },
          visibility: "private",
        }),
      ]),
    );
    expect(screen.getByTestId("machine-sharing-toggle-rt-cursor")).toHaveTextContent(
      /Make public/i,
    );
    // No inline Private/Public next to the version (A2).
    const card = screen.getByTestId("machine-runtime-card-cursor");
    expect(card.textContent).not.toMatch(/·\s*Private|·\s*Public/);
  });

  it("renders an off-catalog installed provider with its raw id as the label", () => {
    renderSection(
      makeMachine([
        makeRuntime({
          provider: "kimi",
          metadata: { version: "9.9.9" },
        }),
      ]),
    );

    expect(screen.getByText("kimi")).toBeInTheDocument();
    expect(screen.getByTestId("provider-logo-kimi")).toBeInTheDocument();
    expect(screen.getByText("v9.9.9")).toBeInTheDocument();
  });

  it("keeps the section mounted without a header count when nothing is installed", () => {
    renderSection(makeMachine([]));

    expect(
      screen.getByRole("heading", { name: "Runtimes on this computer" }),
    ).toBeInTheDocument();
    expect(screen.queryByText(/^\d+ \/ \d+$/)).not.toBeInTheDocument();
    expect(
      screen.queryByText("Supported · not installed"),
    ).not.toBeInTheDocument();
    expect(
      screen.getAllByText("View install guide").length,
    ).toBeGreaterThan(0);
  });
});
