// @vitest-environment node
import { describe, expect, it } from "vitest";
import { isWikiEdgeType, isWikiPageKind, tabToApiKind } from "./page-kind";

describe("tabToApiKind", () => {
  it("maps UI tabs to BE kinds", () => {
    expect(tabToApiKind("all")).toBeUndefined();
    expect(tabToApiKind("topic")).toBe("context");
    expect(tabToApiKind("decision")).toBe("decision");
    expect(tabToApiKind("goal")).toBe("goal");
  });
});

describe("isWikiPageKind / isWikiEdgeType", () => {
  it("accepts MVP kinds and edges", () => {
    expect(isWikiPageKind("context")).toBe(true);
    expect(isWikiPageKind("memory")).toBe(false);
    expect(isWikiEdgeType("derived_from")).toBe(true);
    expect(isWikiEdgeType("related")).toBe(false);
  });
});
