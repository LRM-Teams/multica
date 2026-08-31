import { describe, expect, it } from "vitest";
import { runnerActivityVisuals } from "./runner-activity-visuals";

describe("runnerActivityVisuals", () => {
  it("derives command color and motion from facts", () => {
    expect(runnerActivityVisuals({ activity_kind: "working", detail_kind: "running_command" })).toMatchObject({
      dotClass: "bg-dot-working", pulse: true, show: true,
    });
  });

  it("hides online summaries and shows errors without pulsing", () => {
    expect(runnerActivityVisuals({ activity_kind: "online", detail_kind: "idle" }).show).toBe(false);
    expect(runnerActivityVisuals({ activity_kind: "error", detail_kind: "runtime_error" })).toMatchObject({
      dotClass: "bg-dot-fail", pulse: false, show: true,
    });
  });
});
