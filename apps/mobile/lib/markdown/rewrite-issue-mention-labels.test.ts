import { describe, expect, it } from "vitest";
import {
  collectIssueMentionIds,
  escapeMarkdownLinkLabel,
  rewriteIssueMentionLabels,
} from "./rewrite-issue-mention-labels";

describe("rewriteIssueMentionLabels (LRM-508)", () => {
  it("replaces LRM-xxx label with the live title", () => {
    const titles: Record<string, string> = {
      "fe57cec6-0a45-4d90-9ef6-6571f429c047": "Soft-ask design",
    };
    expect(
      rewriteIssueMentionLabels(
        "see [LRM-487](mention://issue/fe57cec6-0a45-4d90-9ef6-6571f429c047) please",
        (id) => titles[id],
      ),
    ).toBe(
      "see [Soft-ask design](mention://issue/fe57cec6-0a45-4d90-9ef6-6571f429c047) please",
    );
  });

  it("clears the label when title is missing — no LRM-xxx / UUID ink", () => {
    expect(
      rewriteIssueMentionLabels(
        "[LRM-487](mention://issue/fe57cec6-0a45-4d90-9ef6-6571f429c047)",
        () => null,
      ),
    ).toBe("[](mention://issue/fe57cec6-0a45-4d90-9ef6-6571f429c047)");
    expect(
      rewriteIssueMentionLabels(
        "[fe57cec6-0a45-4d90-9ef6-6571f429c047](mention://issue/fe57cec6-0a45-4d90-9ef6-6571f429c047)",
        () => undefined,
      ),
    ).toBe("[](mention://issue/fe57cec6-0a45-4d90-9ef6-6571f429c047)");
  });

  it("escapes brackets inside titles", () => {
    expect(escapeMarkdownLinkLabel("A [B] C")).toBe("A \\[B\\] C");
    expect(
      rewriteIssueMentionLabels("[LRM-1](mention://issue/i1)", () => "A [B] C"),
    ).toBe("[A \\[B\\] C](mention://issue/i1)");
  });

  it("leaves non-issue content untouched", () => {
    const src = "hi [@Alice](mention://member/u1) and https://example.com";
    expect(rewriteIssueMentionLabels(src, () => "x")).toBe(src);
  });
});

describe("collectIssueMentionIds", () => {
  it("returns unique issue ids in order of appearance", () => {
    expect(
      collectIssueMentionIds(
        "[A](mention://issue/i1) then [B](mention://issue/i2) and [A2](mention://issue/i1)",
      ),
    ).toEqual(["i1", "i2"]);
  });
});
