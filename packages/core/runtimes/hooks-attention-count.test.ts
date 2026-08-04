// @vitest-environment node
import { describe, expect, it } from "vitest";
import type { AgentRuntime } from "../types";
import {
  attentionMachineKey,
  countMyAttentionMachines,
  summarizeMyAttentionMachines,
} from "./hooks";

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
    runtime_health: "update_available",
    owner_id: "user-1",
    visibility: "private",
    last_seen_at: "2026-07-03T00:00:00Z",
    created_at: "2026-07-03T00:00:00Z",
    updated_at: "2026-07-03T00:00:00Z",
    ...overrides,
  } as AgentRuntime;
}

describe("countMyAttentionMachines (task #31)", () => {
  it("ignores another owner's update_available runtime", () => {
    expect(
      countMyAttentionMachines(
        [
          makeRuntime({
            id: "rt-other",
            daemon_id: "daemon-other",
            owner_id: "user-other",
          }),
        ],
        "user-1",
      ),
    ).toBe(0);
  });

  it("collapses multiple provider runtimes on one daemon to one machine", () => {
    expect(
      countMyAttentionMachines(
        [
          makeRuntime({ id: "rt-a", daemon_id: "daemon-mine", provider: "claude" }),
          makeRuntime({ id: "rt-b", daemon_id: "daemon-mine", provider: "cursor" }),
        ],
        "user-1",
      ),
    ).toBe(1);
  });

  it("counts two owned machines separately", () => {
    expect(
      countMyAttentionMachines(
        [
          makeRuntime({ id: "rt-a", daemon_id: "daemon-1" }),
          makeRuntime({ id: "rt-b", daemon_id: "daemon-2" }),
        ],
        "user-1",
      ),
    ).toBe(2);
  });

  it("links to the first owned attention runtime, never another owner's", () => {
    expect(
      summarizeMyAttentionMachines(
        [
          makeRuntime({ id: "rt-other", owner_id: "user-other" }),
          makeRuntime({ id: "rt-mine", daemon_id: "daemon-mine" }),
        ],
        "user-1",
      ),
    ).toEqual({ count: 1, firstRuntimeId: "rt-mine" });
  });

  it("attentionMachineKey prefers daemon_id", () => {
    expect(attentionMachineKey(makeRuntime({ daemon_id: "d-1", id: "rt-9" }))).toBe(
      "d-1",
    );
    expect(
      attentionMachineKey(makeRuntime({ daemon_id: null as unknown as string, id: "rt-9" })),
    ).toBe("runtime:rt-9");
  });
});
