// @vitest-environment node
import { describe, expect, it } from "vitest";
import { RUNNER_ACTIVITY_LABEL_EN } from "../../agents/runner-activity-labels";
import { isCompactActivityLabel } from "./is-compact-activity-label";

describe("isCompactActivityLabel (LRM-650)", () => {
  it("accepts concrete EN activity types", () => {
    expect(isCompactActivityLabel("Thinking...")).toBe(true);
    expect(isCompactActivityLabel("Running command...…")).toBe(
      true,
    );
  });

  it("rejects Working / Idle / empty (Compact 不挂 Working)", () => {
    expect(isCompactActivityLabel(RUNNER_ACTIVITY_LABEL_EN.working)).toBe(false);
    expect(isCompactActivityLabel(RUNNER_ACTIVITY_LABEL_EN.idle)).toBe(false);
    expect(isCompactActivityLabel(null)).toBe(false);
    expect(isCompactActivityLabel("")).toBe(false);
  });
});
