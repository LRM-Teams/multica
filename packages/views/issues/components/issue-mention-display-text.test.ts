import { describe, expect, it } from "vitest";
import { resolveIssueMentionDisplayText } from "./issue-mention-display-text";

describe("resolveIssueMentionDisplayText (LRM-508 title-first)", () => {
  it("returns trimmed title when present", () => {
    expect(resolveIssueMentionDisplayText("Fix login")).toBe("Fix login");
    expect(resolveIssueMentionDisplayText("  Soft-ask design  ")).toBe("Soft-ask design");
  });

  it("returns null for missing/empty title — never invents LRM-xxx or UUID", () => {
    expect(resolveIssueMentionDisplayText(undefined)).toBeNull();
    expect(resolveIssueMentionDisplayText(null)).toBeNull();
    expect(resolveIssueMentionDisplayText("")).toBeNull();
    expect(resolveIssueMentionDisplayText("   ")).toBeNull();
  });
});
