// @vitest-environment jsdom

import { describe, it, expect, vi } from "vitest";
import { render, screen, within } from "@testing-library/react";
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
  it("renders an off-catalog installed provider with its raw id as the label (not blank)", () => {
    renderSection(
      makeMachine([
        makeRuntime({
          provider: "kimi",
          metadata: { version: "9.9.9" },
        }),
      ]),
    );

    // Section stays mounted — heading is the lock that it did not early-return null.
    expect(
      screen.getByRole("heading", { name: "Runtimes" }),
    ).toBeInTheDocument();

    const installedHeading = screen.getByText("Installed");
    const card = installedHeading.closest("div.overflow-hidden") ?? document.body;
    // Off-catalog: knownProviderLabel misses → fall back to provider id.
    expect(within(card as HTMLElement).getByText("kimi")).toBeInTheDocument();
    expect(within(card as HTMLElement).getByText("kimi").textContent?.trim()).toBe(
      "kimi",
    );
    expect(screen.getByTestId("provider-logo-kimi")).toBeInTheDocument();
    expect(screen.getByText(/v9\.9\.9/)).toBeInTheDocument();
  });

  it("keeps the section mounted and shows the empty copy when nothing is installed", () => {
    renderSection(makeMachine([]));

    expect(
      screen.getByRole("heading", { name: "Runtimes" }),
    ).toBeInTheDocument();
    expect(screen.getByText("Installed")).toBeInTheDocument();
    expect(
      screen.getByText("No runtimes detected on this computer yet."),
    ).toBeInTheDocument();
    // Recommend catalog still listed under Not installed — section is not null.
    expect(screen.getByText("Not installed")).toBeInTheDocument();
  });
});
