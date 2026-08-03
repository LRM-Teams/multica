// @vitest-environment node
import { describe, expect, it } from "vitest";
import type { ResearchMessage } from "@multica/core/types";
import {
  RESEARCH_STAGE_ORDER,
  buildStageMessageAnchors,
  messageStageKey,
  resolveStageStepState,
  stageAnchorId,
  stageAnchorTargetId,
  stageIndex,
} from "./research-stages";

describe("research-stages", () => {
  it("orders the four product stages", () => {
    expect(RESEARCH_STAGE_ORDER).toEqual([
      "s1_plan",
      "s2_sources",
      "s3_validation",
      "s4_delivery",
    ]);
  });

  it("marks past / current / upcoming relative to current_stage", () => {
    expect(resolveStageStepState("s1_plan", "s3_validation", "running")).toBe("done");
    expect(resolveStageStepState("s3_validation", "s3_validation", "running")).toBe(
      "current",
    );
    expect(resolveStageStepState("s4_delivery", "s3_validation", "running")).toBe(
      "upcoming",
    );
  });

  it("marks every stage done when the session completed", () => {
    for (const stage of RESEARCH_STAGE_ORDER) {
      expect(resolveStageStepState(stage, "s2_sources", "completed")).toBe("done");
    }
  });

  it("builds first-message anchors from meta.stage", () => {
    const messages = [
      { id: "a", meta: { stage: "s1_plan" } },
      { id: "b", meta: { stage: "s1_plan" } },
      { id: "c", meta: { stage: "s2_sources" } },
      { id: "d", meta: {} },
    ] as ResearchMessage[];
    const map = buildStageMessageAnchors(messages);
    expect(map.get("s1_plan")).toBe("a");
    expect(map.get("s2_sources")).toBe("c");
    expect(map.has("s3_validation")).toBe(false);
    expect(messageStageKey(messages[3]!)).toBeNull();
    expect(stageIndex("s2_sources")).toBe(1);
    expect(stageAnchorId("s2_sources")).toBe("research-stage-s2_sources");
  });

  it("maps a chat message to a stable scroll-target id (LRM-824)", () => {
    expect(stageAnchorTargetId("m-9")).toBe("research-msg-m-9");
  });
});
