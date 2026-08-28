import { describe, expect, it } from "vitest";
import { decideGraphVersion, shouldAcceptSnapshot } from "./graph-version";

describe("decideGraphVersion", () => {
  it("applies the next consecutive version", () => {
    expect(decideGraphVersion(4, 5)).toBe("apply");
  });

  it("discards an update the canvas already rendered", () => {
    expect(decideGraphVersion(7, 7)).toBe("discard");
  });

  it("discards a late update from before the current version", () => {
    // This is the flapping case: an invalidate for version 5 arriving after
    // the client already rendered 8 must not roll the canvas backwards.
    expect(decideGraphVersion(8, 5)).toBe("discard");
  });

  it("refetches when versions were skipped", () => {
    expect(decideGraphVersion(2, 9)).toBe("refetch");
  });

  it("treats a non-numeric version as unusable", () => {
    expect(decideGraphVersion(3, Number.NaN)).toBe("discard");
  });

  it("honours a wider skew tolerance", () => {
    expect(decideGraphVersion(2, 4, { maxSkew: 2 })).toBe("apply");
    expect(decideGraphVersion(2, 5, { maxSkew: 2 })).toBe("refetch");
  });
});

describe("shouldAcceptSnapshot", () => {
  it("accepts a newer snapshot", () => {
    expect(shouldAcceptSnapshot(3, 4)).toBe(true);
  });

  it("rejects a snapshot that resolved after a newer event", () => {
    expect(shouldAcceptSnapshot(6, 4)).toBe(false);
    expect(shouldAcceptSnapshot(6, 6)).toBe(false);
  });
});
