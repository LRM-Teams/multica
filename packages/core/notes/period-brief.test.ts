import { describe, expect, it } from "vitest";
import {
  PERIOD_BRIEF_FIXTURE_MARKDOWN,
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
  it("includes online local and cloud agents but not the synthesizer", () => {
    const agents = [
      {
        id: "local-1",
        name: "coder",
        runtime_id: "r1",
        runtime_mode: "local" as const,
        runtime_status: "online" as const,
      },
      {
        id: "cloud-1",
        name: "cloud",
        runtime_id: "r2",
        runtime_mode: "cloud" as const,
        runtime_status: "online" as const,
      },
      {
        id: "off-1",
        name: "sleep",
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
    expect(defaultPeriodBriefCollectorIds(agents)).toEqual(["local-1", "cloud-1"]);
    expect(isPeriodBriefCollectorOnline(agents[2])).toBe(false);
    expect(togglePeriodBriefCollectorId(["local-1"], "cloud-1")).toEqual(["local-1", "cloud-1"]);
    expect(togglePeriodBriefCollectorId(["local-1", "cloud-1"], "local-1")).toEqual(["cloud-1"]);
  });
});
