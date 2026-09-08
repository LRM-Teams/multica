import { describe, expect, it } from "vitest";
import {
  isPeriodBriefAwaitingIntent,
  isPeriodBriefClarifyingPlan,
  isPeriodBriefPlanComplete,
  looksLikePeriodBriefRequest,
  listPeriodBriefPlanGaps,
  looksLikeUnconstrainedScope,
  periodBriefRunLocksComposer,
  PERIOD_BRIEF_INTENT_NO,
  PERIOD_BRIEF_INTENT_YES,
  PERIOD_BRIEF_SATELLITE_ASK,
} from "./period-brief-compose";

describe("PERIOD_BRIEF_SATELLITE_ASK", () => {
  it("is the spoken 写汇报 funnel, not a compose fence", () => {
    expect(PERIOD_BRIEF_SATELLITE_ASK).toBe("写汇报");
    expect(PERIOD_BRIEF_SATELLITE_ASK).not.toContain("<period_brief");
  });
});

describe("period brief intent confirm helpers", () => {
  it("distinguishes awaiting_intent from clarifying", () => {
    expect(isPeriodBriefAwaitingIntent({ status: "awaiting_intent" })).toBe(true);
    expect(isPeriodBriefClarifyingPlan({ status: "awaiting_intent" })).toBe(false);
    expect(isPeriodBriefAwaitingIntent({ status: "clarifying" })).toBe(false);
    expect(isPeriodBriefClarifyingPlan({ status: "clarifying" })).toBe(true);
    expect(isPeriodBriefClarifyingPlan({ status: "", window: "week" })).toBe(true);
    expect(isPeriodBriefClarifyingPlan({ status: "", window: "", collector_agent_ids: [] })).toBe(false);
    expect(PERIOD_BRIEF_INTENT_YES).toBe("是");
    expect(PERIOD_BRIEF_INTENT_NO).toBe("否");
  });
});

describe("isPeriodBriefPlanComplete", () => {
  it("requires collectors and a valid window, not a global scope chip", () => {
    expect(isPeriodBriefPlanComplete({ collectorIds: ["c1"] })).toBe(true);
    expect(isPeriodBriefPlanComplete({ collectorIds: [] })).toBe(false);
    expect(
      isPeriodBriefPlanComplete({
        collectorIds: ["c1"],
        customRangeValid: false,
      }),
    ).toBe(false);
  });
});

describe("listPeriodBriefPlanGaps", () => {
  it("reports missing computers", () => {
    expect(listPeriodBriefPlanGaps({ collectorIds: [] })).toEqual(["computers"]);
    expect(listPeriodBriefPlanGaps({ collectorIds: ["c1"] })).toEqual([]);
  });
});

describe("looksLikeUnconstrainedScope", () => {
  it("treats empty text and an explicit full-scope phrase as unconstrained", () => {
    expect(looksLikeUnconstrainedScope("")).toBe(true);
    expect(looksLikeUnconstrainedScope("  ")).toBe(true);
    expect(looksLikeUnconstrainedScope("全部")).toBe(true);
    expect(looksLikeUnconstrainedScope("unconstrained")).toBe(true);
    expect(looksLikeUnconstrainedScope("只整理 ~/multica")).toBe(false);
  });
});

describe("looksLikePeriodBriefRequest", () => {
  it("matches a spoken 写汇报 ask and ignores ordinary note chat", () => {
    expect(looksLikePeriodBriefRequest("帮我写汇报")).toBe(true);
    expect(looksLikePeriodBriefRequest("帮我写个周报")).toBe(true);
    expect(looksLikePeriodBriefRequest("写汇报")).toBe(true);
    expect(looksLikePeriodBriefRequest("整理一份周报")).toBe(true);
    expect(looksLikePeriodBriefRequest("period brief for this week")).toBe(true);
    expect(looksLikePeriodBriefRequest("Write report")).toBe(true);
    expect(looksLikePeriodBriefRequest("Report")).toBe(true);
    expect(looksLikePeriodBriefRequest("这段笔记的标题怎么改")).toBe(false);
    expect(looksLikePeriodBriefRequest("I want to report a bug")).toBe(false);
  });
});

describe("periodBriefRunLocksComposer", () => {
  it("unlocks once the insert card is posted, even if the human never inserts", () => {
    expect(periodBriefRunLocksComposer("collecting")).toBe(true);
    expect(periodBriefRunLocksComposer("synthesizing")).toBe(true);
    expect(periodBriefRunLocksComposer("awaiting_confirm")).toBe(false);
    expect(periodBriefRunLocksComposer("done")).toBe(false);
    expect(periodBriefRunLocksComposer("cancelled")).toBe(false);
  });
});
