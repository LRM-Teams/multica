// @vitest-environment jsdom

import { describe, it, expect, vi } from "vitest";
import { render, screen } from "@testing-library/react";
import type { AgentRuntime } from "@multica/core/types";
import { I18nProvider } from "@multica/core/i18n/react";
import enCommon from "../../locales/en/common.json";
import enRuntimes from "../../locales/en/runtimes.json";
import type { RuntimeMachine } from "./runtime-machines";
import { MachineCodeAgentsSection } from "./machine-code-agents-section";

const TEST_RESOURCES = {
  en: { common: enCommon, runtimes: enRuntimes },
};

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
    updateTargetVersion: null,
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
  return render(
    <I18nProvider locale="en" resources={TEST_RESOURCES}>
      <MachineCodeAgentsSection machine={machine} />
    </I18nProvider>,
  );
}

describe("MachineCodeAgentsSection", () => {
  it("renders installed + supported-not-installed inventory with install guide", () => {
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
    // Short count only — no long summary strip under the title.
    expect(
      screen.getByText(/Installed 1 of \d+ supported runtimes/i),
    ).toBeInTheDocument();
    expect(
      screen.queryByText(/Available to agents on this computer/i),
    ).not.toBeInTheDocument();
    expect(
      screen.queryByText(/Install to make them selectable/i),
    ).not.toBeInTheDocument();
    expect(screen.getByText(/Installed · v1\.4\.2/)).toBeInTheDocument();
    expect(screen.getByTestId("provider-logo-cursor")).toBeInTheDocument();
    expect(screen.getByText("Grok Build")).toBeInTheDocument();
    // Recommend catalog still listed as supported · not installed.
    expect(
      screen.getAllByText("Supported · not installed").length,
    ).toBeGreaterThan(0);
    expect(
      screen.getAllByText("View install guide").length,
    ).toBeGreaterThan(0);
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

    expect(
      screen.getByRole("heading", { name: "Runtimes on this computer" }),
    ).toBeInTheDocument();
    expect(screen.getByText("kimi")).toBeInTheDocument();
    expect(screen.getByTestId("provider-logo-kimi")).toBeInTheDocument();
    expect(screen.getByText(/Installed · v9\.9\.9/)).toBeInTheDocument();
  });

  it("keeps the section mounted and shows the empty copy when nothing is installed", () => {
    renderSection(makeMachine([]));

    expect(
      screen.getByRole("heading", { name: "Runtimes on this computer" }),
    ).toBeInTheDocument();
    // Empty install still shows the recommend catalog (supported · not installed).
    expect(
      screen.getByText(/Installed 0 of \d+ supported runtimes/i),
    ).toBeInTheDocument();
    expect(
      screen.queryByText(/Available to agents on this computer/i),
    ).not.toBeInTheDocument();
    expect(
      screen.getAllByText("Supported · not installed").length,
    ).toBeGreaterThan(0);
  });
});
