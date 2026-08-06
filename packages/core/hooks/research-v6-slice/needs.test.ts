import { describe, expect, it } from "vitest";
import { computeSliceNeeds, pendingNeedCount } from "./needs";

describe("computeSliceNeeds (LRM-1465 viewport driver)", () => {
  it("first open requests only the seed slice", () => {
    const needs = computeSliceNeeds({ seedRoot: "root", loadedRoots: [] });
    expect(needs.map((n) => n.root)).toEqual(["root"]);
    expect(needs[0]!.reason).toBe("seed");
  });

  it("never re-requests a root that is already loaded (no duplicate pagination)", () => {
    const needs = computeSliceNeeds({
      seedRoot: "root",
      loadedRoots: ["root", "a", "b"],
      visibleRoots: ["root", "a", "b", "c"],
      expandedRoots: ["root"],
    });
    const roots = needs.map((n) => n.root);
    expect(roots).toContain("c");
    expect(roots).not.toContain("root");
    expect(roots).not.toContain("a");
    expect(roots).not.toContain("b");
    // no duplicates at all
    expect(new Set(roots).size).toBe(roots.length);
  });

  it("composite-node expand deep-loads exactly the expanded root", () => {
    const needs = computeSliceNeeds({
      seedRoot: null,
      loadedRoots: [],
      compositeExpandRoot: "insight-1",
    });
    expect(needs.map((n) => n.root)).toEqual(["insight-1"]);
    expect(needs[0]!.reason).toBe("composite-expand");
    expect(needs[0]!.direction).toBe("both");
  });

  it("viewport pan loads only the newly visible adjacent roots", () => {
    const needs = computeSliceNeeds({
      seedRoot: null,
      loadedRoots: ["root"],
      visibleRoots: ["n1", "n2", "root"],
    });
    expect(needs.map((n) => n.root)).toEqual(["n1", "n2"]);
    expect(needs.every((n) => n.reason === "pan")).toBe(true);
  });

  it("orders needs seed before expand before pan", () => {
    const needs = computeSliceNeeds({
      seedRoot: "root",
      loadedRoots: [],
      expandedRoots: ["e1"],
      visibleRoots: ["v1"],
    });
    expect(needs[0]!.root).toBe("root");
    expect(needs[0]!.priority).toBeLessThan(needs[1]!.priority);
    expect(needs[2]!.priority).toBeGreaterThan(needs[1]!.priority);
  });

  it("returns zero needs when everything is already loaded", () => {
    expect(
      pendingNeedCount({
        seedRoot: "root",
        loadedRoots: ["root", "e1", "v1"],
        expandedRoots: ["e1"],
        visibleRoots: ["v1"],
      }),
    ).toBe(0);
  });
});
