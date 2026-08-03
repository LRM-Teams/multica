// @vitest-environment node
import { describe, expect, it } from "vitest";
import {
  filterSessions,
  isRedundantGoalChip,
  isSessionListFilterActive,
  matchesStatusFilter,
  matchesTitleQuery,
  sessionGoalChipSummary,
  sessionShortTitle,
} from "./session-list-filter";
import type { ResearchSession } from "@multica/core/types";

function s(
  partial: Partial<ResearchSession> & Pick<ResearchSession, "id" | "status">,
): ResearchSession {
  return {
    workspace_id: "w",
    fleet_id: "f",
    created_by: "u",
    title: "",
    goal: "goal",
    current_stage: "s1_plan",
    project_id: null,
    channel_id: null,
    handoff_summary: null,
    created_at: "2026-07-31T00:00:00Z",
    updated_at: "2026-07-31T00:00:00Z",
    ...partial,
  };
}

describe("session-list-filter (LRM-818)", () => {
  it("matches titles case-insensitively in real time", () => {
    expect(matchesTitleQuery(s({ id: "1", status: "running", title: "行业调研" }), "行业")).toBe(
      true,
    );
    expect(matchesTitleQuery(s({ id: "1", status: "running", title: "Industry" }), "ind")).toBe(
      true,
    );
    expect(matchesTitleQuery(s({ id: "1", status: "running", title: "Industry" }), "zzz")).toBe(
      false,
    );
  });

  it("falls back to goal when title is empty", () => {
    expect(matchesTitleQuery(s({ id: "1", status: "running", title: "", goal: "向量库" }), "向量")).toBe(
      true,
    );
  });

  it("status filter is mutually exclusive across three buckets", () => {
    expect(matchesStatusFilter(s({ id: "1", status: "running" }), "in_progress")).toBe(true);
    expect(matchesStatusFilter(s({ id: "1", status: "completed" }), "completed")).toBe(true);
    expect(matchesStatusFilter(s({ id: "1", status: "failed" }), "failed")).toBe(true);
    expect(matchesStatusFilter(s({ id: "1", status: "running" }), "failed")).toBe(false);
    expect(matchesStatusFilter(s({ id: "1", status: "failed" }), "in_progress")).toBe(false);
  });

  it("combines query + status", () => {
    const list = [
      s({ id: "a", status: "running", title: "Alpha research" }),
      s({ id: "b", status: "completed", title: "Alpha done" }),
      s({ id: "c", status: "failed", title: "Alpha failed" }),
    ];
    expect(filterSessions(list, "Alpha", "in_progress").map((x) => x.id)).toEqual(["a"]);
    expect(filterSessions(list, "Alpha", "failed").map((x) => x.id)).toEqual(["c"]);
  });

  it("detects active filters for clear control", () => {
    expect(isSessionListFilterActive("", null)).toBe(false);
    expect(isSessionListFilterActive("  x ", null)).toBe(true);
    expect(isSessionListFilterActive("", "completed")).toBe(true);
  });
});

describe("session goal chip dedupe (LRM-1104)", () => {
  it("treats equal title/goal summaries as redundant", () => {
    expect(isRedundantGoalChip("如何开发一个网页游戏", "如何开发一个网页游戏")).toBe(
      true,
    );
  });

  it("treats mutual prefixes (truncated title vs shorter goal chip) as redundant", () => {
    const goal =
      "如何开发一个网页游戏。对标游戏传奇网页版。告诉我需要的各种人员，开发环境要求。目前我们的设备是几台 linux 服务器";
    const title = sessionShortTitle({ title: "", goal });
    const chip = sessionGoalChipSummary({ title: "", goal });
    expect(title.includes("…")).toBe(true);
    expect(chip).toBeNull();
    expect(isRedundantGoalChip(title, title.slice(0, 35) + "…")).toBe(true);
  });

  it("keeps the chip when title is set and goal adds extra information", () => {
    const session = s({
      id: "1",
      status: "running",
      title: "Alpha market map",
      goal: "Map the alpha market across regions with pricing and share",
    });
    expect(sessionGoalChipSummary(session)).toBe(
      "Map the alpha market across regions…",
    );
  });

  it("hides the chip when an explicit title equals the goal", () => {
    const session = s({
      id: "1",
      status: "running",
      title: "行业调研",
      goal: "行业调研",
    });
    expect(sessionGoalChipSummary(session)).toBeNull();
  });
});
