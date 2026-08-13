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

  it("wires the selected lens as the sole tab stop and handles tab keys", () => {
    const src = readFileSync(
      join(import.meta.dirname, "research-d5-chrome.tsx"),
      "utf8",
    );
    expect(src).toContain("tabIndex={activeLens === lens ? 0 : -1}");
    expect(src).toContain("onKeyDown={handleLensKeyDown}");
    expect(src).toContain("resolveD5LensNavigationIndex");
  });

  it("surfaces the capability-gated V5 fallback as a classic projection", () => {
    const src = readFileSync(
      join(import.meta.dirname, "research-d5-chrome.tsx"),
      "utf8",
    );
    expect(src).toContain('projectionSource === "v5"');
    expect(src).toContain('data-testid="research-d5-classic-projection"');
    expect(src).toContain('role="status"');
  });
});
