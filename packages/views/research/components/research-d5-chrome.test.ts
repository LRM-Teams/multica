import { readFileSync } from "node:fs";
import { join } from "node:path";
import { describe, expect, it } from "vitest";

describe("ResearchD5Chrome", () => {
  it("does not mount the legacy session chrome second row", () => {
    const src = readFileSync(
      join(import.meta.dirname, "research-d5-chrome.tsx"),
      "utf8",
    );
    expect(src).not.toMatch(/<ResearchSessionChrome[\s/>]/);
    expect(src).toContain("ResearchSessionChromeActions");
  });
});
