// @vitest-environment jsdom
import { render, screen } from "@testing-library/react";
import { describe, it, expect } from "vitest";
import { I18nProvider } from "@multica/core/i18n/react";
import type { AgentRuntime } from "@multica/core/types";
import enAgents from "../../../locales/en/agents.json";
import { ComputerInfoRow } from "./computer-info-row";

function makeRuntime(overrides: Partial<AgentRuntime> = {}): AgentRuntime {
  return {
    id: "rt-1",
    workspace_id: "ws-1",
    daemon_id: "daemon-1",
    name: "raw-hostname",
    display_name: "s144",
    runtime_mode: "local",
    provider: "cursor",
    launch_header: "cursor-agent (stream-json)",
    status: "online",
    device_info: "ubuntu",
    metadata: {},
    current_version: "0.3.92",
    update_state: "idle",
    runtime_health: "ok",
    owner_id: "user-1",
    last_seen_at: new Date().toISOString(),
    created_at: new Date().toISOString(),
    updated_at: new Date().toISOString(),
    ...overrides,
  };
}

function renderRow(runtime: AgentRuntime | null) {
  render(
    <I18nProvider locale="en" resources={{ en: { agents: enAgents } }}>
      <ComputerInfoRow runtime={runtime} />
    </I18nProvider>,
  );
}

// Frank, 2026-08-01: standalone info row, deliberately independent of the
// Runtime/code-agent picker row — it must not disappear or get relabeled
// when that picker's own vocabulary changes.
describe("ComputerInfoRow (2026-08-01)", () => {
  it("shows Connected + machine label + version for an online runtime", () => {
    renderRow(makeRuntime());
    expect(screen.getByText("Connected")).toBeInTheDocument();
    expect(screen.getByText("s144")).toBeInTheDocument();
    expect(screen.getByText("v0.3.92")).toBeInTheDocument();
  });

  it("shows hostname — not Provider (host) — when display_name is unset (Frank 2026-08-02)", () => {
    renderRow(
      makeRuntime({
        display_name: "",
        name: "Cursor (s144)",
        computer_connected: true,
      }),
    );
    expect(screen.getByText("Connected")).toBeInTheDocument();
    expect(screen.getByText("s144")).toBeInTheDocument();
    expect(screen.queryByText("Cursor (s144)")).not.toBeInTheDocument();
  });

  it("prefers computer_connected over runtime last_seen for the Connected label", () => {
    renderRow(
      makeRuntime({
        computer_connected: true,
        status: "offline",
        last_seen_at: new Date(Date.now() - 10 * 60_000).toISOString(),
      }),
    );
    expect(screen.getByText("Connected")).toBeInTheDocument();
  });

  it("shows Disconnected for a stale-heartbeat runtime when computer_connected is absent", () => {
    // status still says "online" but last_seen_at is far in the past — the
    // whole point of deriveRuntimeHealth over reading raw `status` (#10).
    renderRow(
      makeRuntime({
        status: "online",
        last_seen_at: new Date(Date.now() - 10 * 60_000).toISOString(),
      }),
    );
    expect(screen.getByText("Disconnected")).toBeInTheDocument();
  });

  it("shows a 'no computer' placeholder when the agent has no bound runtime", () => {
    renderRow(null);
    expect(screen.getByText("No computer")).toBeInTheDocument();
  });
});
