import { describe, expect, it } from "vitest";
import { computeMemoryGrowth } from "./memory-growth";

describe("computeMemoryGrowth", () => {
  it("returns null for zero writes", () => {
    expect(computeMemoryGrowth(0)).toBeNull();
  });

  it("maps bronze progress toward silver", () => {
    const snap = computeMemoryGrowth(2);
    expect(snap?.tier).toBe("bronze");
    expect(snap?.segments[0]?.status).toBe("current");
    expect(snap?.next).toEqual({
      tier: "silver",
      tier_label: "Silver",
      current: 2,
      required: 3,
    });
  });

  it("maps silver progress toward gold", () => {
    const snap = computeMemoryGrowth(5);
    expect(snap?.tier).toBe("silver");
    expect(snap?.segments[0]?.status).toBe("complete");
    expect(snap?.segments[1]?.status).toBe("current");
    expect(snap?.next).toEqual({
      tier: "gold",
      tier_label: "Gold",
      current: 5,
      required: 6,
    });
  });

  it("maps platinum progress toward the final threshold", () => {
    const snap = computeMemoryGrowth(12);
    expect(snap?.tier).toBe("platinum");
    expect(snap?.segments[3]?.status).toBe("current");
    expect(snap?.next).toEqual({
      tier: "platinum",
      tier_label: "Platinum",
      current: 12,
      required: 24,
    });
  });

  it("marks all segments complete at max with no next", () => {
    const snap = computeMemoryGrowth(24);
    expect(snap?.tier).toBe("platinum");
    expect(snap?.segments.every((s) => s.status === "complete")).toBe(true);
    expect(snap?.next).toBeNull();
  });
});
