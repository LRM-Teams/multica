import { describe, expect, it } from "vitest";
import {
  PERIOD_BRIEF_COLLECTOR_PACK_FIXTURE,
  PERIOD_BRIEF_FIXTURE_MARKDOWN,
  collectorPackLooksStructured,
  periodBriefFromCollectorPackFixture,
  periodBriefLooksStructured,
} from "./period-brief";
import {
  PERIOD_BRIEF_AGENT_NAME,
  isPeriodBriefAgent,
  resolvePeriodBriefAgent,
  resolvePeriodBriefSynthesizerId,
} from "./period-brief-agent";
import {
  defaultPeriodBriefCollectorIds,
  isPeriodBriefCollectorOnline,
  togglePeriodBriefCollectorId,
} from "./period-brief-collectors";
import {
  defaultPeriodBriefCustomRange,
  isValidPeriodBriefCustomRange,
  shiftPeriodBriefCalendarDay,
} from "./period-brief-window";

describe("periodBriefLooksStructured", () => {
  it("accepts the fixture Brief with reporting headings", () => {
    expect(periodBriefLooksStructured(PERIOD_BRIEF_FIXTURE_MARKDOWN)).toBe(true);
  });

  it("rejects a raw commit dump", () => {
    const dump = [
      "a1b2c3d wire SSO",
      "e4f5a6b fix digest",
      "1122334 add denylist",
      "5566778 harvest git",
      "99aabb0 owner switch",
      "ccddeef scope remotes",
    ].join("\n");
    expect(periodBriefLooksStructured(dump)).toBe(false);
  });
});

describe("collector pack → Brief (K2-T1)", () => {
  it("fixture pack is structured and synthesizes a structured Brief", () => {
    expect(collectorPackLooksStructured(PERIOD_BRIEF_COLLECTOR_PACK_FIXTURE)).toBe(true);
    const brief = periodBriefFromCollectorPackFixture(PERIOD_BRIEF_COLLECTOR_PACK_FIXTURE);
    expect(periodBriefLooksStructured(brief)).toBe(true);
    expect(brief).toContain("## 主线");
    expect(brief).toContain("collector packs");
    expect(brief).toContain("## 本机未归类");
    expect(brief).toContain("~/tmp");
    expect(brief).toBe(PERIOD_BRIEF_FIXTURE_MARKDOWN);
  });
});

describe("resolvePeriodBriefSynthesizerId", () => {
  const agents = [
    { id: "a1", name: "wendy" },
    { id: "a2", name: PERIOD_BRIEF_AGENT_NAME },
    { id: "a3", name: "coder" },
  ];

  it("prefers the weekly-report agent over preferredAgentId", () => {
    expect(resolvePeriodBriefSynthesizerId(agents, "a1")).toBe("a2");
    expect(isPeriodBriefAgent(agents[1])).toBe(true);
    expect(resolvePeriodBriefAgent(agents)?.id).toBe("a2");
  });

  it("falls back to preferred then first when 周报 is absent", () => {
    const without = agents.filter((a) => a.name !== PERIOD_BRIEF_AGENT_NAME);
    expect(resolvePeriodBriefSynthesizerId(without, "a3")).toBe("a3");
    expect(resolvePeriodBriefSynthesizerId(without, null)).toBe("a1");
  });
});

describe("defaultPeriodBriefCollectorIds", () => {
  it("includes only online dedicated collectors, not specialty or synthesizer agents", () => {
    const agents = [
      {
        id: "local-1",
        name: "period-collect-laptop1",
        runtime_id: "r1",
        runtime_mode: "local" as const,
        runtime_status: "online" as const,
      },
      {
        id: "specialty",
        name: "coder",
        runtime_id: "r1",
        runtime_mode: "local" as const,
        runtime_status: "online" as const,
      },
      {
        id: "off-1",
        name: "period-collect-offline",
        runtime_id: "r3",
        runtime_mode: "local" as const,
        runtime_status: "offline" as const,
      },
      {
        id: "weekly-1",
        name: PERIOD_BRIEF_AGENT_NAME,
        runtime_id: "r1",
        runtime_mode: "local" as const,
        runtime_status: "online" as const,
      },
    ];
    expect(defaultPeriodBriefCollectorIds(agents)).toEqual(["local-1"]);
    const offline = agents[2];
    if (!offline) throw new Error("expected offline collector fixture");
    expect(isPeriodBriefCollectorOnline(offline)).toBe(false);
    expect(togglePeriodBriefCollectorId(["local-1"], "off-1")).toEqual(["local-1", "off-1"]);
    expect(togglePeriodBriefCollectorId(["local-1", "off-1"], "local-1")).toEqual(["off-1"]);
  });
});

describe("periodBriefCustomRange", () => {
  it("validates inclusive YYYY-MM-DD order", () => {
    expect(isValidPeriodBriefCustomRange("2026-08-10", "2026-08-12")).toBe(true);
    expect(isValidPeriodBriefCustomRange("2026-08-12", "2026-08-10")).toBe(false);
    expect(isValidPeriodBriefCustomRange("bad", "2026-08-10")).toBe(false);
  });

  it("defaults to a 7-day inclusive range ending today", () => {
    expect(defaultPeriodBriefCustomRange("2026-08-19")).toEqual({
      start_date: "2026-08-13",
      end_date: "2026-08-19",
    });
    expect(shiftPeriodBriefCalendarDay("2026-03-01", -1)).toBe("2026-02-28");
  });
});
