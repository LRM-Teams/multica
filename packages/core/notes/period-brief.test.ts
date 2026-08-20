import { describe, expect, it } from "vitest";
import {
  PERIOD_BRIEF_COLLECTOR_PACK_FIXTURE,
  PERIOD_BRIEF_FIXTURE_MARKDOWN,
  collectorPackLooksStructured,
  periodBriefFromCollectorPackFixture,
  periodBriefLooksStructured,
} from "./period-brief";
import { NOTES_ASSISTANT_AGENT_NAME } from "./notes-assistant-agent";
import {
  PERIOD_BRIEF_AGENT_NAME,
  RETIRED_PERIOD_BRIEF_AGENT_NAME,
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
    { id: "a2", name: NOTES_ASSISTANT_AGENT_NAME },
    { id: "a3", name: RETIRED_PERIOD_BRIEF_AGENT_NAME },
  ];

  it("resolves only 笔记助手 — leftover 周报 is not the synthesizer", () => {
    expect(PERIOD_BRIEF_AGENT_NAME).toBe(NOTES_ASSISTANT_AGENT_NAME);
    expect(resolvePeriodBriefSynthesizerId(agents)).toBe("a2");
    expect(isPeriodBriefAgent(agents[1])).toBe(true);
    expect(isPeriodBriefAgent(agents[2])).toBe(false);
    expect(resolvePeriodBriefAgent(agents)?.id).toBe("a2");
  });

  it("returns null when 笔记助手 is absent", () => {
    const without = agents.filter((a) => a.name !== NOTES_ASSISTANT_AGENT_NAME);
    expect(resolvePeriodBriefSynthesizerId(without)).toBeNull();
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
        owner_id: "user-1",
      },
      {
        id: "specialty",
        name: "coder",
        runtime_id: "r1",
        runtime_mode: "local" as const,
        runtime_status: "online" as const,
        owner_id: "user-1",
      },
      {
        id: "off-1",
        name: "period-collect-offline",
        runtime_id: "r3",
        runtime_mode: "local" as const,
        runtime_status: "offline" as const,
        owner_id: "user-1",
      },
      {
        id: "weekly-1",
        name: NOTES_ASSISTANT_AGENT_NAME,
        runtime_id: "r1",
        runtime_mode: "local" as const,
        runtime_status: "online" as const,
        owner_id: "user-1",
      },
    ];
    expect(defaultPeriodBriefCollectorIds(agents)).toEqual(["local-1"]);
    const offline = agents[2];
    if (!offline) throw new Error("expected offline collector fixture");
    expect(isPeriodBriefCollectorOnline(offline)).toBe(false);
    expect(togglePeriodBriefCollectorId(["local-1"], "off-1")).toEqual(["local-1", "off-1"]);
    expect(togglePeriodBriefCollectorId(["local-1", "off-1"], "local-1")).toEqual(["off-1"]);
  });

  it("excludes collectors on computers owned by someone else when userId is set", () => {
    const agents = [
      {
        id: "mine",
        name: "period-collect-mine",
        runtime_id: "r-mine",
        runtime_mode: "local" as const,
        runtime_status: "online" as const,
        owner_id: "user-1",
      },
      {
        id: "theirs",
        name: "period-collect-theirs",
        runtime_id: "r-theirs",
        runtime_mode: "local" as const,
        runtime_status: "online" as const,
        owner_id: "user-2",
      },
    ];
    const runtimes = [
      { id: "r-mine", status: "online" as const, owner_id: "user-1" },
      { id: "r-theirs", status: "online" as const, owner_id: "user-2" },
    ];
    expect(defaultPeriodBriefCollectorIds(agents, runtimes, "user-1")).toEqual(["mine"]);
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
