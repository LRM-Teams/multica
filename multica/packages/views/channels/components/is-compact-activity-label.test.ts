// @vitest-environment node
import { describe, expect, it } from "vitest";
import { ACTIVITY_LABEL_EN } from "../../agents/components/tabs/activity-event";
import { isCompactActivityLabel } from "./is-compact-activity-label";

describe("isCompactActivityLabel (LRM-650)", () => {
  it("accepts concrete EN activity types", () => {
    expect(isCompactActivityLabel(ACTIVITY_LABEL_EN.thinking)).toBe(true);
    expect(isCompactActivityLabel(`${ACTIVITY_LABEL_EN.running_command}…`)).toBe(
      true,
    );
  });

  it("rejects Working / Idle / empty (Compact 不挂 Working)", () => {
    expect(isCompactActivityLabel(ACTIVITY_LABEL_EN.working)).toBe(false);
    expect(isCompactActivityLabel(ACTIVITY_LABEL_EN.idle)).toBe(false);
    expect(isCompactActivityLabel(null)).toBe(false);
    expect(isCompactActivityLabel("")).toBe(false);
  });
});
