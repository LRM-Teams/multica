import { describe, expect, it } from "vitest";
import type { AgentRuntime } from "../types";
import { deriveRuntimeHealth } from "./derive-health";

const FIXED_NOW = new Date("2026-04-27T12:00:00Z").getTime();

function makeRuntime(overrides: Partial<AgentRuntime> = {}): AgentRuntime {
  return {
    id: "rt-1",
    workspace_id: "ws-1",
    daemon_id: "daemon-1",
    name: "Test Runtime",
    runtime_mode: "local",
    provider: "claude",
    launch_header: "",
    status: "online",
    device_info: "",
    metadata: {},
    current_version: null,
    update_state: "idle",
    runtime_health: "ok",
    owner_id: null,
    last_seen_at: new Date(FIXED_NOW - 10_000).toISOString(),
    created_at: "2026-04-01T00:00:00Z",
    updated_at: "2026-04-01T00:00:00Z",
    ...overrides,
  };
}

describe("deriveRuntimeHealth", () => {
  describe("status === 'offline' (EXPLICIT — #571)", () => {
    it("reads offline immediately, NOT recently_lost, when just dropped (< 5 min)", () => {
      // #571 regression: an explicitly-offline runtime seen 30s ago must read
      // "offline" — never "recently_lost" (which the agent dot maps to
      // "Unstable"). recently_lost is reserved for a STALE ONLINE heartbeat.
      expect(
        deriveRuntimeHealth(
          makeRuntime({
            status: "offline",
            last_seen_at: new Date(FIXED_NOW - 30_000).toISOString(),
          }),
          FIXED_NOW,
        ),
      ).toBe("offline");
    });

    it("stays offline for a mid-range absence (5 min – 6 days)", () => {
      expect(
        deriveRuntimeHealth(
          makeRuntime({
            status: "offline",
            last_seen_at: new Date(FIXED_NOW - 60 * 60_000).toISOString(), // 1 hour
          }),
          FIXED_NOW,
        ),
      ).toBe("offline");
    });

    it("returns about_to_gc when offline beyond 6 days (within 1 day of GC)", () => {
      expect(
        deriveRuntimeHealth(
          makeRuntime({
            status: "offline",
            last_seen_at: new Date(FIXED_NOW - 6.5 * 24 * 3600_000).toISOString(),
          }),
          FIXED_NOW,
        ),
      ).toBe("about_to_gc");
    });

    it("treats null last_seen_at as long-offline (about_to_gc)", () => {
      // No heartbeat ever recorded on an explicitly-offline runtime → treat as
      // infinitely offline → about_to_gc (past the GC horizon).
      expect(
        deriveRuntimeHealth(makeRuntime({ status: "offline", last_seen_at: null }), FIXED_NOW),
      ).toBe("about_to_gc");
    });
  });

  describe("status === 'online' (heartbeat-freshness buckets)", () => {
    it("returns online when the heartbeat is fresh (< 150s)", () => {
      expect(
        deriveRuntimeHealth(
          makeRuntime({
            status: "online",
            last_seen_at: new Date(FIXED_NOW - 60_000).toISOString(), // 60s
          }),
          FIXED_NOW,
        ),
      ).toBe("online");
    });

    it("returns recently_lost when the heartbeat has lagged (150s – 5 min)", () => {
      // The ONLY source of recently_lost / "Unstable": an ONLINE runtime whose
      // heartbeat has gone quiet for 200s.
      expect(
        deriveRuntimeHealth(
          makeRuntime({
            status: "online",
            last_seen_at: new Date(FIXED_NOW - 200_000).toISOString(), // 200s
          }),
          FIXED_NOW,
        ),
      ).toBe("recently_lost");
    });

    it("returns offline when the heartbeat has been silent past 5 min", () => {
      expect(
        deriveRuntimeHealth(
          makeRuntime({
            status: "online",
            last_seen_at: new Date(FIXED_NOW - 6 * 60_000).toISOString(), // 6 min
          }),
          FIXED_NOW,
        ),
      ).toBe("offline");
    });

    it("returns offline when status is online but last_seen_at was never set", () => {
      // A flag with no heartbeat to back it can't be vouched for.
      expect(
        deriveRuntimeHealth(makeRuntime({ status: "online", last_seen_at: null }), FIXED_NOW),
      ).toBe("offline");
    });

    it("respects the 150s boundary (just inside → online)", () => {
      expect(
        deriveRuntimeHealth(
          makeRuntime({
            status: "online",
            last_seen_at: new Date(FIXED_NOW - (150_000 - 1_000)).toISOString(),
          }),
          FIXED_NOW,
        ),
      ).toBe("online");
    });

    it("respects the 150s boundary (just outside → recently_lost)", () => {
      expect(
        deriveRuntimeHealth(
          makeRuntime({
            status: "online",
            last_seen_at: new Date(FIXED_NOW - (150_000 + 1_000)).toISOString(),
          }),
          FIXED_NOW,
        ),
      ).toBe("recently_lost");
    });

    it("respects the 5-minute boundary (just outside → offline)", () => {
      expect(
        deriveRuntimeHealth(
          makeRuntime({
            status: "online",
            last_seen_at: new Date(FIXED_NOW - (5 * 60_000 + 1_000)).toISOString(),
          }),
          FIXED_NOW,
        ),
      ).toBe("offline");
    });
  });
});
