// @vitest-environment node
import { describe, it, expect } from "vitest";
import { preprocessIssueRefs } from "@multica/ui/markdown";

const P = "LRM";

describe("preprocessIssueRefs", () => {
  it("wraps a bare identifier into an issue mention link", () => {
    expect(preprocessIssueRefs("See LRM-14 for details", P)).toBe(
      "See [LRM-14](mention://issue/LRM-14) for details",
    );
  });

  it("wraps multiple identifiers", () => {
    expect(preprocessIssueRefs("LRM-7 and LRM-8 ship tonight", P)).toBe(
      "[LRM-7](mention://issue/LRM-7) and [LRM-8](mention://issue/LRM-8) ship tonight",
    );
  });

  it("only matches the workspace prefix (no false positives)", () => {
    const text = "UTF-8 COVID-19 GPT-4 ABC-12 are not LRM issues";
    expect(preprocessIssueRefs(text, P)).toBe(text);
  });

  it("requires a word boundary (no partial-prefix matches)", () => {
    const text = "XLRM-14 and LRM-14x are not identifiers";
    expect(preprocessIssueRefs(text, P)).toBe(text);
  });

  it("leaves identifiers inside inline code untouched", () => {
    expect(preprocessIssueRefs("run `LRM-14` now", P)).toBe("run `LRM-14` now");
  });

  it("leaves identifiers inside fenced code blocks untouched", () => {
    const text = "```\nLRM-14\n```";
    expect(preprocessIssueRefs(text, P)).toBe(text);
  });

  it("does not double-wrap an existing markdown/mention link", () => {
    const text = "[LRM-14](mention://issue/abc12345-1234-1234-1234-1234567890ab)";
    expect(preprocessIssueRefs(text, P)).toBe(text);
  });

  it("handles trailing punctuation", () => {
    expect(preprocessIssueRefs("Closes LRM-14.", P)).toBe(
      "Closes [LRM-14](mention://issue/LRM-14).",
    );
  });

  it("is a no-op when the prefix is empty", () => {
    expect(preprocessIssueRefs("LRM-14", "")).toBe("LRM-14");
  });

  it("escapes regex-special characters in the prefix", () => {
    // A pathological prefix must not blow up the RegExp constructor.
    expect(preprocessIssueRefs("A.B-3 here", "A.B")).toBe(
      "[A.B-3](mention://issue/A.B-3) here",
    );
    // And must not match a different literal where the dot acts as a wildcard.
    expect(preprocessIssueRefs("AXB-3 here", "A.B")).toBe("AXB-3 here");
  });
});
