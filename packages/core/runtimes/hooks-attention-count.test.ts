// @vitest-environment node
import { describe, expect, it } from "vitest";
import type { AgentRuntime } from "../types";
import { makeRuntime as sharedRuntime } from "./runtime-fixture";
import {
  attentionMachineKey,
  countMyAttentionMachines,
  summarizeMyAttentionMachines,
} from "./hooks";

function makeRuntime(overrides: Partial<AgentRuntime> = {}): AgentRuntime {
  return sharedRuntime({ runtime_health: "update_available", ...overrides });
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
