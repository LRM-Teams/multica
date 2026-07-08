import { describe, expect, it } from "vitest";
import { toFlatItemIndex } from "./virtuoso-flat-index";

describe("toFlatItemIndex", () => {
  it("offsets the data index by the prepend anchor", () => {
    expect(toFlatItemIndex(0, 5)).toBe(5);
    expect(toFlatItemIndex(1000, 3)).toBe(1003);
  });

  it("keeps a row's flat index stable across a prepend (the anchor invariant)", () => {
    // Prepending 200 older rows drops firstItemIndex 1000 -> 800; the row that
    // was data-index 3 becomes data-index 203, and its flat index is unchanged —
    // which is exactly why scroll targets must go through this mapping.
    expect(toFlatItemIndex(1000, 3)).toBe(toFlatItemIndex(800, 203));
  });
});
