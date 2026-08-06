// @vitest-environment node
import { describe, expect, it } from "vitest";
import { stripMentionAtPrefix } from "./actor-mention-chip-label";

describe("stripMentionAtPrefix (LRM-515)", () => {
  it("strips one or more leading @", () => {
    expect(stripMentionAtPrefix("@bei-ke-han-mu-11")).toBe("bei-ke-han-mu-11");
    expect(stripMentionAtPrefix("@@alice")).toBe("alice");
  });

  it("returns undefined for empty / @-only", () => {
    expect(stripMentionAtPrefix(undefined)).toBeUndefined();
    expect(stripMentionAtPrefix("")).toBeUndefined();
    expect(stripMentionAtPrefix("@")).toBeUndefined();
    expect(stripMentionAtPrefix("   ")).toBeUndefined();
  });
});
