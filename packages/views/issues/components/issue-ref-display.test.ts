import { describe, expect, it } from "vitest";
import { isIssueUuid, resolveIssueRefDisplayText } from "./issue-ref-display";

describe("isIssueUuid", () => {
  it("accepts canonical UUIDs", () => {
    expect(isIssueUuid("fe57cec6-0a45-4d90-9ef6-6571f429c047")).toBe(true);
  });

  it("rejects human identifiers", () => {
    expect(isIssueUuid("LRM-487")).toBe(false);
    expect(isIssueUuid("#MUL-9")).toBe(false);
  });
});

describe("resolveIssueRefDisplayText (LRM-493)", () => {
  const issue = {
    id: "fe57cec6-0a45-4d90-9ef6-6571f429c047",
    identifier: "LRM-487",
    title: "通知授权 soft-ask",
  };

  it("keeps an author identifier span verbatim (#467/#600)", () => {
    expect(
      resolveIssueRefDisplayText({ text: "LRM-487", issue }),
    ).toBe("LRM-487");
    expect(
      resolveIssueRefDisplayText({ text: "#MUL-9", issue }),
    ).toBe("#MUL-9");
  });

  it("upgrades a UUID span to the live identifier", () => {
    expect(
      resolveIssueRefDisplayText({
        text: "fe57cec6-0a45-4d90-9ef6-6571f429c047",
        issue,
      }),
    ).toBe("LRM-487");
  });

  it("prefers structured label over a UUID span before resolve", () => {
    expect(
      resolveIssueRefDisplayText({
        text: "fe57cec6-0a45-4d90-9ef6-6571f429c047",
        label: "LRM-487",
      }),
    ).toBe("LRM-487");
  });

  it("falls back to title when identifier is missing (LRM-423 口径)", () => {
    expect(
      resolveIssueRefDisplayText({
        text: "fe57cec6-0a45-4d90-9ef6-6571f429c047",
        issue: { id: issue.id, identifier: "", title: "通知授权 soft-ask" },
      }),
    ).toBe("通知授权 soft-ask");
  });

  it("does not silently truncate an unresolved UUID (LRM-238)", () => {
    expect(
      resolveIssueRefDisplayText({
        text: "fe57cec6-0a45-4d90-9ef6-6571f429c047",
      }),
    ).toBe("");
  });
});
