import { describe, expect, it } from "vitest";
import type { AgentRuntime } from "../types";
import {
  aggregateRuntimeHealthState,
  deriveRuntimeHealthPresentation,
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

describe("runtimeCanStartSelfUpdate — update_state eligibility (#687)", () => {
  it("is ineligible while staged: update_available health but update_state ready_to_apply", () => {
    // The backend keeps runtime_health "update_available" through ready_to_apply;
    // gating on health alone would re-open the prompt on an already-staged daemon.
    expect(
      runtimeCanStartSelfUpdate(
        makeRuntime({
          runtime_health: "update_available",
          update_state: "ready_to_apply",
        }),
        "user-1",
      ),
    ).toBe(false);
  });

  it("is ineligible while an update is underway (pending / running)", () => {
    for (const update_state of ["pending", "running"] as const) {
      expect(
        runtimeCanStartSelfUpdate(
          makeRuntime({ runtime_health: "update_available", update_state }),
          "user-1",
        ),
      ).toBe(false);
    }
  });

  it("stays eligible for a newer release while a prior update is completed", () => {
    // A terminal `completed` row lingers (~6h). A newer release during that window
    // projects update_available + completed and MUST remain startable — otherwise
    // consecutive upgrades are blocked by stale terminal history.
    expect(
      runtimeCanStartSelfUpdate(
        makeRuntime({
          runtime_health: "update_available",
          update_state: "completed",
        }),
        "user-1",
      ),
    ).toBe(true);
  });

  it("stays eligible for a genuinely idle runtime with an available update", () => {
    expect(
      runtimeCanStartSelfUpdate(
        makeRuntime({ runtime_health: "update_available", update_state: "idle" }),
        "user-1",
      ),
    ).toBe(true);
  });
});

describe("deriveRuntimeHealthPresentation (#687)", () => {
  it("shows ready_to_apply, overriding the collapsed update_available health", () => {
    expect(
      deriveRuntimeHealthPresentation(
        makeRuntime({
          runtime_health: "update_available",
          update_state: "ready_to_apply",
        }),
      ),
    ).toBe("ready_to_apply");
  });

  it("shows updating for an in-progress update_state even if health lags", () => {
    expect(
      deriveRuntimeHealthPresentation(
        makeRuntime({ runtime_health: "update_available", update_state: "running" }),
      ),
    ).toBe("updating");
    expect(
      deriveRuntimeHealthPresentation(
        makeRuntime({ runtime_health: "update_available", update_state: "pending" }),
      ),
    ).toBe("updating");
  });

  it("keeps update_available for idle/completed (a newer release the user can start)", () => {
    expect(
      deriveRuntimeHealthPresentation(
        makeRuntime({ runtime_health: "update_available", update_state: "idle" }),
      ),
    ).toBe("update_available");
    expect(
      deriveRuntimeHealthPresentation(
        makeRuntime({ runtime_health: "update_available", update_state: "completed" }),
      ),
    ).toBe("update_available");
  });

  it("does not degrade failed / offline / ok", () => {
    expect(
      deriveRuntimeHealthPresentation(
        makeRuntime({ runtime_health: "failed", update_state: "failed" }),
      ),
    ).toBe("failed");
    expect(
      deriveRuntimeHealthPresentation(
        makeRuntime({ runtime_health: "offline", update_state: "idle" }),
      ),
    ).toBe("offline");
    expect(
      deriveRuntimeHealthPresentation(makeRuntime({ runtime_health: "ok" })),
    ).toBe("ok");
  });
});
