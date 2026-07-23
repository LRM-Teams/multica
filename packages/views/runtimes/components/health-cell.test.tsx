// @vitest-environment jsdom
import { render, screen } from "@testing-library/react";
import { describe, it, expect } from "vitest";
import { I18nProvider } from "@multica/core/i18n/react";
import type { AgentRuntime, RuntimeUpdateState } from "@multica/core/types";
import enRuntimes from "../../locales/en/runtimes.json";
import { HealthCell } from "./runtime-columns";

function makeRuntime(overrides: Partial<AgentRuntime> = {}): AgentRuntime {
  return {
    id: "rt-1",
    workspace_id: "ws-1",
    daemon_id: "daemon-1",
    name: "My Mac",
    runtime_mode: "local",
    provider: "local",
    launch_header: "",
    status: "online",
    device_info: "",
    metadata: {},
    current_version: "0.3.64",
    target_version: "0.3.65",
    update_state: "idle",
    runtime_health: "update_available",
    owner_id: "user-1",
    visibility: "private",
    last_seen_at: "2026-07-23T00:00:00Z",
    created_at: "2026-07-01T00:00:00Z",
    updated_at: "2026-07-01T00:00:00Z",
    ...overrides,
  };
}

function renderCell(updateState: RuntimeUpdateState) {
  return render(
    <I18nProvider locale="en" resources={{ en: { runtimes: enRuntimes } }}>
      <HealthCell runtime={makeRuntime({ update_state: updateState })} now={Date.parse("2026-07-23T00:01:00Z")} />
    </I18nProvider>,
  );
}

describe("Runtime list HealthCell (#687)", () => {
  it("shows 'Ready to apply' for a staged runtime, overriding 'Update available'", () => {
    renderCell("ready_to_apply");
    expect(screen.getByText("Ready to apply")).toBeInTheDocument();
    expect(screen.queryByText("Update available")).toBeNull();
  });

  it("still shows 'Update available' for an idle runtime with an available update", () => {
    renderCell("idle");
    expect(screen.getByText("Update available")).toBeInTheDocument();
    expect(screen.queryByText("Ready to apply")).toBeNull();
  });
});
