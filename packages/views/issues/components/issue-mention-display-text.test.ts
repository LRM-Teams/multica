import { describe, expect, it } from "vitest";
import { resolveIssueMentionDisplayText } from "./issue-mention-display-text";

describe("resolveIssueMentionDisplayText", () => {
  it("prefers non-UUID author label, then identifier, never UUID", () => {
    expect(
      resolveIssueMentionDisplayText(
        "fe57cec6-0a45-4d90-9ef6-6571f429c047",
        "LRM-487",
        "LRM-487",
      ),
    ).toBe("LRM-487");
    expect(
      resolveIssueMentionDisplayText(
        "fe57cec6-0a45-4d90-9ef6-6571f429c047",
        undefined,
        "LRM-487",
      ),
    ).toBe("LRM-487");
    expect(
      resolveIssueMentionDisplayText(
        "fe57cec6-0a45-4d90-9ef6-6571f429c047",
        "fe57cec6-0a45-4d90-9ef6-6571f429c047",
        undefined,
      ),
    ).toBeNull();
    expect(resolveIssueMentionDisplayText("LRM-126", undefined, undefined)).toBe("LRM-126");
  });
});
