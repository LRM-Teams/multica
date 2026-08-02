// @vitest-environment node
import { describe, expect, it } from "vitest";
import type { AgentHealthState, AgentHealthSummary } from "@multica/core/types";
import {
  formatClockTime,
  formatHealthDuration,
  healthStateConfig,
  resolveHealthDotClass,
} from "./health";

const ALL_STATES: AgentHealthState[] = [
  "online",
  "suspected_disconnect",
  "reconnecting",
  "recovered",
  "offline",
];

function summary(state: AgentHealthState): AgentHealthSummary {
  return {
    agent_id: "a1",
    runtime_id: null,
    state,
    reason_code: "heartbeat_received",
    state_since: "2026-07-06T09:00:00Z",
    last_seen_at: "2026-07-06T09:40:00Z",
    last_event_at: "2026-07-06T09:40:00Z",
  };
}

describe("healthStateConfig — dot color source (Iris §1)", () => {
  it("maps online + recovered to green (success)", () => {
    expect(healthStateConfig.online.dotClass).toBe("bg-success");
    expect(healthStateConfig.recovered.dotClass).toBe("bg-success");
  });

  it("maps suspected_disconnect + reconnecting to amber (warning)", () => {
    expect(healthStateConfig.suspected_disconnect.dotClass).toBe("bg-warning");
    expect(healthStateConfig.reconnecting.dotClass).toBe("bg-warning");
  });

  it("maps offline to gray (muted)", () => {
    expect(healthStateConfig.offline.dotClass).toBe("bg-muted-foreground/40");
  });

  it("covers all five states with no hardcoded raw colors", () => {
    for (const s of ALL_STATES) {
      const cfg = healthStateConfig[s];
      expect(cfg).toBeDefined();
      // Semantic tokens only — never a raw tailwind palette color.
      expect(cfg.dotClass).not.toMatch(/(red|green|amber|yellow|gray)-\d/);
    }
  });
});

describe("resolveHealthDotClass — LRM-248 Online/Offline live badge", () => {
  it("folds the explicitly-decided-green states to Online green (LRM-248 allowance, must not regress)", () => {
    expect(resolveHealthDotClass(summary("online"), "bg-fallback")).toBe(
      "bg-success",
    );
    expect(
      resolveHealthDotClass(summary("suspected_disconnect"), "bg-fallback"),
    ).toBe("bg-success");
    expect(
      resolveHealthDotClass(summary("reconnecting"), "bg-fallback"),
    ).toBe("bg-success");
    expect(resolveHealthDotClass(summary("recovered"), "bg-fallback")).toBe(
      "bg-success",
    );
    expect(resolveHealthDotClass(summary("offline"), "bg-fallback")).toBe(
      "bg-muted-foreground/40",
    );
  });

  it("falls back gracefully when the summary is unavailable (API not live)", () => {
    expect(resolveHealthDotClass(undefined, "bg-success")).toBe("bg-success");
  });

  // Task #93: the backend can emit a state this FE type doesn't declare yet
  // (e.g. "restarting" from an active lifecycle operation overlay in
  // GetAgentHealth) — the response schema is loose, so this reaches the FE
  // as a live runtime value despite the AgentHealthState union claiming it
  // can't happen. An unrecognized state must never read as confidently
  // online; it falls to the existing Offline gray, the same conservative
  // default used elsewhere for unknown/missing facts.
  it("folds an unrecognized state to Offline gray, not Online green", () => {
    const unknownState = "restarting" as AgentHealthState;
    expect(resolveHealthDotClass(summary(unknownState), "bg-fallback")).toBe(
      "bg-muted-foreground/40",
    );
  });
});

describe("formatHealthDuration", () => {
  const base = new Date("2026-07-06T09:00:00Z").getTime();

  it("renders minutes under an hour", () => {
    expect(formatHealthDuration("2026-07-06T09:00:00Z", base + 2 * 60_000)).toBe(
      "2m",
    );
  });

  it("renders hours under a day", () => {
    expect(
      formatHealthDuration("2026-07-06T09:00:00Z", base + 3 * 60 * 60_000),
    ).toBe("3h");
  });

  it("renders days beyond 24h", () => {
    expect(
      formatHealthDuration("2026-07-06T09:00:00Z", base + 50 * 60 * 60_000),
    ).toBe("2d");
  });

  it("renders <1m for sub-minute and clamps negatives", () => {
    expect(formatHealthDuration("2026-07-06T09:00:00Z", base + 5_000)).toBe(
      "<1m",
    );
    expect(formatHealthDuration("2026-07-06T09:00:00Z", base - 5_000)).toBe(
      "<1m",
    );
  });
});

describe("formatClockTime", () => {
  it("renders a 24-hour HH:MM clock", () => {
    // Pin a UTC locale via the ISO offset — assert the shape, not the tz.
    const out = formatClockTime("2026-07-06T09:41:00Z", "en-GB");
    expect(out).toMatch(/^\d{2}:\d{2}$/);
  });
});
