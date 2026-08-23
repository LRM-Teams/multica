import { describe, expect, it } from "vitest";
import { familyForNodeKind } from "./node-kind-registry";

describe("familyForNodeKind", () => {
  it.each([
    ["goal", "structure"],
    ["attempt", "execution"],
    ["source_snapshot", "evidence"],
    ["insight", "cognition"],
    ["team_membership", "collaboration"],
    ["report_revision", "governance"],
  ] as const)("maps %s to %s", (kind, family) => {
    expect(familyForNodeKind(kind)).toBe(family);
  });

  it("degrades unknown kinds to the generic display family", () => {
    expect(familyForNodeKind("future_kind")).toBe("generic");
  });
});
