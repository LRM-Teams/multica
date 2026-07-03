import { describe, expect, it } from "vitest";
import type { AgentRuntime } from "../types";
import {
  aggregateRuntimeHealthState,
  runtimeCanStartSelfUpdate,
  runtimeCurrentVersion,
  runtimeHasHealthAttention,
} from "./runtime-health-state";

function makeRuntime(overrides: Partial<AgentRuntime> = {}): AgentRuntime {
  return {
    id: "rt-1",
    workspace_id: "ws-1",
    daemon_id: "daemon-1",
    name: "Runtime",
    runtime_mode: "local",
    provider: "claude",
    launch_header: "",
    status: "online",
    device_info: "",
    metadata: {},
    current_version: "0.3.0",
    target_version: "0.4.0",
    update_state: "idle",
    runtime_health: "ok",
    owner_id: "user-1",
    visibility: "private",
    last_seen_at: "2026-07-03T00:00:00Z",
    created_at: "2026-07-03T00:00:00Z",
    updated_at: "2026-07-03T00:00:00Z",
    ...overrides,
  };
}

describe("runtime health contract helpers", () => {
  it("starts update only from the backend health state and target version", () => {
    expect(
      runtimeCanStartSelfUpdate(
        makeRuntime({ runtime_health: "update_available" }),
        "user-1",
      ),
    ).toBe(true);

    expect(
      runtimeCanStartSelfUpdate(
        makeRuntime({
          runtime_health: "updating",
          update_state: "completed",
        }),
        "user-1",
      ),
    ).toBe(false);

    expect(
      runtimeCanStartSelfUpdate(
        makeRuntime({ runtime_health: "update_available", target_version: null }),
        "user-1",
      ),
    ).toBe(false);
  });

  it("keeps updating visible without allowing duplicate starts", () => {
    expect(
      runtimeHasHealthAttention(
        makeRuntime({ runtime_health: "updating" }),
        "user-1",
      ),
    ).toBe(true);

    expect(
      runtimeCanStartSelfUpdate(
        makeRuntime({ runtime_health: "updating" }),
        "user-1",
      ),
    ).toBe(false);
  });

  it("uses current_version as the confirmed display version", () => {
    expect(
      runtimeCurrentVersion(
        makeRuntime({
          current_version: "0.4.0",
          metadata: { cli_version: "0.3.0" },
        }),
      ),
    ).toBe("0.4.0");
  });

  it("aggregates the highest-severity runtime health state", () => {
    expect(
      aggregateRuntimeHealthState([
        makeRuntime({ runtime_health: "ok" }),
        makeRuntime({ runtime_health: "updating" }),
        makeRuntime({ runtime_health: "failed" }),
      ]),
    ).toBe("failed");
  });
});
