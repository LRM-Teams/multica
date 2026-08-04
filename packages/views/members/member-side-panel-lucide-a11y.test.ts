// @vitest-environment node

/**
 * LRM-1320 — members chrome lucide dual-announce:
 * member-side-panel decorative icons inside named controls must declare
 * aria-hidden. Mutex vs members-tab / channels chrome / research knives.
 */
import fs from "node:fs";
import path from "node:path";
import { fileURLToPath } from "node:url";
import { describe, expect, it } from "vitest";

const here = path.dirname(fileURLToPath(import.meta.url));

function readSrc(name: string) {
  return fs.readFileSync(path.join(here, name), "utf8");
}

describe("members chrome lucide a11y (LRM-1320)", () => {
  it("member-side-panel: MessageSquare/Pencil lucide icons declare aria-hidden", () => {
    const src = readSrc("member-side-panel.tsx");
    expect(src).toMatch(/<MessageSquare\b[\s\S]{0,60}aria-hidden/);
    expect(src).toMatch(/<Pencil\b[\s\S]{0,60}aria-hidden/);
  });
});
