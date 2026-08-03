// @vitest-environment node
import { describe, expect, it } from "vitest";
import {
  filterSessions,
  isSessionListFilterActive,
  matchesStatusFilter,
  matchesTitleQuery,
  shouldShowSessionGoalChip,
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

describe("shouldShowSessionGoalChip (LRM-1104)", () => {
  it("hides the chip when title is empty and falls back to goal (equal)", () => {
    const goal =
      "如何开发一个网页游戏。对标游戏传奇网页版。告诉我需要的各种人员，开发环境要求。目前我们的设备是几台 linux 服务器";
    expect(
      shouldShowSessionGoalChip(s({ id: "1", status: "running", title: "", goal })),
    ).toBe(false);
  });

  it("hides the chip when title and goal are equal after whitespace collapse", () => {
    expect(
      shouldShowSessionGoalChip(
        s({
          id: "1",
          status: "running",
          title: "  Alpha market map  ",
          goal: "Alpha\nmarket map",
        }),
      ),
    ).toBe(false);
  });

  it("hides the chip when title and goal are mutual prefixes", () => {
    expect(
      shouldShowSessionGoalChip(
        s({
          id: "1",
          status: "running",
          title: "LRM-904 live联调：动态编制 hire/archive 证据（可删）",
          goal: "LRM-904 live联调：动态编制 hire/archive 证据（可删）——补 hire 回执",
        }),
      ),
    ).toBe(false);
    expect(
      shouldShowSessionGoalChip(
        s({
          id: "2",
          status: "running",
          title: "LRM-904/918 真缺口联调：专利/IP 专长 hire→干活→产出（非 pad 测 c…）",
          goal: "LRM-904/918 真缺口联调：专利/IP 专长 hire→干活→产出",
        }),
      ),
    ).toBe(false);
  });

  it("keeps the chip when title has a value and goal adds distinct information", () => {
    expect(
      shouldShowSessionGoalChip(
        s({
          id: "1",
          status: "running",
          title: "Alpha market map",
          goal: "Map the alpha market across regions with pricing and share",
        }),
      ),
    ).toBe(true);
  });

  it("hides the chip when goal is empty", () => {
    expect(
      shouldShowSessionGoalChip(
        s({ id: "1", status: "running", title: "Named session", goal: "  " }),
      ),
    ).toBe(false);
  });
});

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
