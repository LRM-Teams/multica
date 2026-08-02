import { describe, expect, it } from "vitest";
import {
  appendCreateParamsToGoal,
  normalizeCreateParams,
} from "./research-create-params";
import { normalizeSessionGoalText, resolveSessionGoalModel } from "./session-goal";

describe("resolveSessionGoalModel (LRM-1008)", () => {
  it("strips create-params trailer and truncates summary", () => {
    const goal = appendCreateParamsToGoal(
      "调研向量数据库",
      normalizeCreateParams({ depth_tier: "standard" }),
    );
    const model = resolveSessionGoalModel({
      goal,
      summaryMaxChars: 4,
    });
    expect(model.text).toBe("调研向量数据库");
    expect(model.summary).toBe("调研向…");
    expect(model.state).toBe("ready");
  });

  it("marks empty / loading / error / pending / updated", () => {
    expect(resolveSessionGoalModel({ goal: "" }).state).toBe("empty");
    expect(resolveSessionGoalModel({ goal: "x", loading: true }).state).toBe(
      "loading",
    );
    expect(resolveSessionGoalModel({ goal: "x", error: true }).state).toBe(
      "error",
    );
    expect(
      resolveSessionGoalModel({
        goal: "current",
        pendingSubstantive: "new topic",
      }).state,
    ).toBe("pending_substantive");
    expect(
      resolveSessionGoalModel({ goal: "current", justUpdated: true }).state,
    ).toBe("updated");
  });

  it("keeps previous only when it differs from current", () => {
    const same = resolveSessionGoalModel({
      goal: "same",
      previousGoal: "same",
    });
    expect(same.previousText).toBeNull();
    const diff = resolveSessionGoalModel({
      goal: "new",
      previousGoal: "old",
    });
    expect(diff.previousText).toBe("old");
    expect(diff.note).toBe("optimized");
  });

  it("normalizeSessionGoalText collapses trailers", () => {
    expect(normalizeSessionGoalText("  hello  ")).toBe("hello");
  });
});
