// @vitest-environment node
import { describe, expect, it } from "vitest";
import type { AgentHealthState } from "@multica/core/types";
import {
  formatClockTime,
  formatHealthDuration,
  healthStateConfig,
} from "./health";

const ALL_STATES: AgentHealthState[] = [
  "online",
  "suspected_disconnect",
  "reconnecting",
  "recovered",
  "offline",
];

describe("healthStateConfig — diagnostics only", () => {
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
