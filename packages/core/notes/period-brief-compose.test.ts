import { describe, expect, it } from "vitest";
import {
  formatPeriodBriefUserTurn,
  looksLikePeriodBriefRequest,
  periodBriefRunLocksComposer,
  resolvePeriodBriefComposeRequest,
} from "./period-brief-compose";

const collectors = [
  { id: "collector-a", label: "采集 · Laptop A", runtime_mode: "local" as const },
  { id: "collector-b", label: "采集 · 云端 · Cloud Box", runtime_mode: "cloud" as const },
];

const chips = {
  window: "week" as const,
  date: "2026-08-21",
  start_date: "2026-08-15",
  end_date: "2026-08-21",
  collector_ids: ["collector-a", "collector-b"],
};

describe("resolvePeriodBriefComposeRequest", () => {
  it("keeps chip selections when the request is blank", () => {
    expect(resolvePeriodBriefComposeRequest(chips, collectors, "  ")).toEqual({
      window: "week",
      date: "2026-08-21",
      collector_ids: ["collector-a", "collector-b"],
    });
  });

  it("passes trimmed text as focus without changing chips when it names neither window nor computer", () => {
    expect(
      resolvePeriodBriefComposeRequest(chips, collectors, "只整理 ~/multica 下笔记助手相关改动"),
    ).toEqual({
      window: "week",
      date: "2026-08-21",
      collector_ids: ["collector-a", "collector-b"],
      focus: "只整理 ~/multica 下笔记助手相关改动",
    });
  });

  it("lets text win when it names a different window than the chips", () => {
    expect(
      resolvePeriodBriefComposeRequest(chips, collectors, "写一份本月的汇报"),
    ).toEqual({
      window: "month",
      date: "2026-08-21",
      collector_ids: ["collector-a", "collector-b"],
      focus: "写一份本月的汇报",
    });
  });

  it("lets text win when it names a computer that conflicts with the chips", () => {
    expect(
      resolvePeriodBriefComposeRequest(chips, collectors, "只采集 Cloud Box"),
    ).toEqual({
      window: "week",
      date: "2026-08-21",
      collector_ids: ["collector-b"],
      focus: "只采集 Cloud Box",
    });
  });

  it("uses an explicit date range from text as a custom window", () => {
    expect(
      resolvePeriodBriefComposeRequest(chips, collectors, "采集 2026-08-10 到 2026-08-14"),
    ).toEqual({
      window: "custom",
      start_date: "2026-08-10",
      end_date: "2026-08-14",
      collector_ids: ["collector-a", "collector-b"],
      focus: "采集 2026-08-10 到 2026-08-14",
    });
  });

  it("maps 上周 onto the week before the chip anchor date", () => {
    expect(
      resolvePeriodBriefComposeRequest(chips, collectors, "整理上周"),
    ).toEqual({
      window: "week",
      date: "2026-08-14",
      collector_ids: ["collector-a", "collector-b"],
      focus: "整理上周",
    });
  });
});

describe("formatPeriodBriefUserTurn", () => {
  it("renders chips as one ordinary user turn", () => {
    expect(
      formatPeriodBriefUserTurn({
        windowLabel: "本周",
        collectorLabels: ["采集 · Laptop A", "采集 · 云端 · Cloud Box"],
        focus: "只整理 ~/multica",
      }),
    ).toBe(
      ["写汇报", "", "时间：本周", "电脑：采集 · Laptop A、采集 · 云端 · Cloud Box", "", "只整理 ~/multica"].join(
        "\n",
      ),
    );
  });
});

describe("looksLikePeriodBriefRequest", () => {
  it("matches a spoken 写汇报 ask and ignores ordinary note chat", () => {
    expect(looksLikePeriodBriefRequest("帮我写汇报")).toBe(true);
    expect(looksLikePeriodBriefRequest("整理一份周报")).toBe(true);
    expect(looksLikePeriodBriefRequest("period brief for this week")).toBe(true);
    expect(looksLikePeriodBriefRequest("这段笔记的标题怎么改")).toBe(false);
  });
});

describe("periodBriefRunLocksComposer", () => {
  it("locks the bubble while collectors or synthesis are running", () => {
    expect(periodBriefRunLocksComposer("collecting")).toBe(true);
    expect(periodBriefRunLocksComposer("awaiting_confirm")).toBe(false);
    expect(periodBriefRunLocksComposer("done")).toBe(false);
  });
});
