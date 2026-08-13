import { describe, expect, it } from "vitest";
import { resolveD5LensNavigationIndex } from "./research-d5-lens-keyboard";

describe("resolveD5LensNavigationIndex", () => {
  it.each([
    ["ArrowRight", 0, 1],
    ["ArrowDown", 2, 3],
    ["ArrowLeft", 2, 1],
    ["ArrowUp", 1, 0],
    ["Home", 3, 0],
    ["End", 0, 3],
  ])("maps %s from %i to %i", (key, current, expected) => {
    expect(resolveD5LensNavigationIndex(key, current, 4)).toBe(expected);
  });

  it("wraps at both ends", () => {
    expect(resolveD5LensNavigationIndex("ArrowRight", 3, 4)).toBe(0);
    expect(resolveD5LensNavigationIndex("ArrowLeft", 0, 4)).toBe(3);
  });

  it("ignores unrelated keys and invalid collections", () => {
    expect(resolveD5LensNavigationIndex("Enter", 1, 4)).toBeNull();
    expect(resolveD5LensNavigationIndex("ArrowRight", -1, 4)).toBeNull();
    expect(resolveD5LensNavigationIndex("ArrowRight", 0, 0)).toBeNull();
  });
});
