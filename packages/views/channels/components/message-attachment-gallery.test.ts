import { describe, expect, it } from "vitest";
import {
  ASPECT_STACK_RATIO,
  resolveGalleryLayout,
  shouldStackByAspectRatios,
} from "./message-attachment-gallery";

describe("shouldStackByAspectRatios (LRM-1098)", () => {
  it("keeps grid when aspects are similar", () => {
    expect(shouldStackByAspectRatios([1.6, 1.4])).toBe(false);
    expect(shouldStackByAspectRatios([1, 1])).toBe(false);
  });

  it("stacks when max/min aspect exceeds the threshold", () => {
    // landscape ~16:9 vs portrait ~9:16
    expect(shouldStackByAspectRatios([16 / 9, 9 / 16])).toBe(true);
    expect(ASPECT_STACK_RATIO).toBe(2);
    expect(shouldStackByAspectRatios([2.1, 1])).toBe(true);
    expect(shouldStackByAspectRatios([2, 1])).toBe(false);
  });

  it("ignores non-positive / incomplete measurements", () => {
    expect(shouldStackByAspectRatios([1.5])).toBe(false);
    expect(shouldStackByAspectRatios([])).toBe(false);
    expect(shouldStackByAspectRatios([0, 1.5, -1])).toBe(false);
  });
});

describe("resolveGalleryLayout", () => {
  it("defaults to grid until two aspects are known", () => {
    expect(resolveGalleryLayout([undefined, undefined], 2)).toBe("grid");
    expect(resolveGalleryLayout([1.5, undefined], 2)).toBe("grid");
  });

  it("stacks once known aspects diverge past the threshold", () => {
    expect(resolveGalleryLayout([16 / 9, 9 / 16], 2)).toBe("stack");
    expect(resolveGalleryLayout([1.2, 1.1], 2)).toBe("grid");
  });
});
