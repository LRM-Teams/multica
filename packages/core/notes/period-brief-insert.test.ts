import { describe, expect, it } from "vitest";
import { parsePeriodBriefInsertPropose, stripPeriodBriefInsertProposeMarkers } from "./period-brief-insert";

describe("parsePeriodBriefInsertPropose", () => {
  it("reads a fence with page, mode, and run", () => {
    expect(
      parsePeriodBriefInsertPropose({
        content: '放到周报下面。\n<period_brief_insert_propose page="page-9" mode="append" run="run-1"/>',
      }),
    ).toEqual({
      runId: "run-1",
      targetPageId: "page-9",
      mode: "append",
    });
  });

  it("accepts a platform part that stores mode as selected_option_id", () => {
    expect(
      parsePeriodBriefInsertPropose({
        parts: [
          {
            type: "period_brief_insert_propose",
            ref_id: "run-3",
            target_page_id: "page-4",
            selected_option_id: "append",
            label: "周报",
          },
        ],
      }),
    ).toEqual({
      runId: "run-3",
      targetPageId: "page-4",
      mode: "append",
      label: "周报",
    });
  });

  it("accepts a structured part", () => {
    expect(
      parsePeriodBriefInsertPropose({
        parts: [
          {
            type: "period_brief_insert_propose",
            ref_id: "run-2",
            target_page_id: "page-3",
            mode: "child",
            label: "工作介绍",
          },
        ],
      }),
    ).toEqual({
      runId: "run-2",
      targetPageId: "page-3",
      mode: "child",
      label: "工作介绍",
    });
  });

  it("rejects an incomplete fence", () => {
    expect(
      parsePeriodBriefInsertPropose({
        content: '<period_brief_insert_propose page="page-9" mode="append"/>',
      }),
    ).toBeNull();
  });
});

describe("stripPeriodBriefInsertProposeMarkers", () => {
  it("removes the fence from visible text", () => {
    expect(
      stripPeriodBriefInsertProposeMarkers('插到那篇下面。\n<period_brief_insert_propose page="p" mode="append" run="r"/>'),
    ).toBe("插到那篇下面。\n");
  });
});
