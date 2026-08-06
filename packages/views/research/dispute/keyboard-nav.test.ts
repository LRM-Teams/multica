import { describe, expect, it } from "vitest";
import {
  disputeNavFromKey,
  moveDisputeNavIndex,
  rovingTabNext,
} from "./keyboard-nav";

describe("dispute keyboard nav (roving tabindex)", () => {
  it("maps arrow keys to next/prev only", () => {
    expect(disputeNavFromKey("ArrowDown")).toBe("next");
    expect(disputeNavFromKey("ArrowRight")).toBe("next");
    expect(disputeNavFromKey("ArrowUp")).toBe("prev");
    expect(disputeNavFromKey("ArrowLeft")).toBe("prev");
    expect(disputeNavFromKey("Enter")).toBeNull();
    expect(disputeNavFromKey("Tab")).toBeNull();
  });

  it("clamps at the ends of a linear roster", () => {
    expect(moveDisputeNavIndex(0, 4, "prev")).toBe(0);
    expect(moveDisputeNavIndex(3, 4, "next")).toBe(3);
    expect(moveDisputeNavIndex(1, 4, "next")).toBe(2);
    expect(moveDisputeNavIndex(1, 4, "prev")).toBe(0);
  });

  it("starts from the correct end when nothing is focused", () => {
    expect(rovingTabNext({ focusedIndex: -1, length: 4, direction: "next" })).toEqual({
      index: 0,
      changed: true,
    });
    expect(rovingTabNext({ focusedIndex: -1, length: 4, direction: "prev" })).toEqual({
      index: 3,
      changed: true,
    });
  });

  it("returns changed=false when already at the boundary", () => {
    expect(rovingTabNext({ focusedIndex: 0, length: 4, direction: "prev" })).toEqual({
      index: 0,
      changed: false,
    });
    expect(rovingTabNext({ focusedIndex: 3, length: 4, direction: "next" })).toEqual({
      index: 3,
      changed: false,
    });
  });

  it("handles empty rosters", () => {
    expect(moveDisputeNavIndex(0, 0, "next")).toBe(-1);
    expect(rovingTabNext({ focusedIndex: -1, length: 0, direction: "next" })).toEqual({
      index: -1,
      changed: false,
    });
  });
});
