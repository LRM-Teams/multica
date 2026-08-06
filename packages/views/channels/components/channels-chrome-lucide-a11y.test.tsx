// @vitest-environment node

/**
 * LRM-1305 — channels chrome lucide dual-announce:
 * agent-files-panel / channel-details-panel decorative icons inside named
 * controls must declare aria-hidden. Mutex vs research knives + members side-panel.
 */
import fs from "node:fs";
import path from "node:path";
import { fileURLToPath } from "node:url";
import { describe, expect, it } from "vitest";

const here = path.dirname(fileURLToPath(import.meta.url));

function readSrc(name: string) {
  return fs.readFileSync(path.join(here, name), "utf8");
}

describe("channels chrome lucide a11y (LRM-1305)", () => {
  it("agent-files-panel: Eye/EyeOff/Save/X lucide icons declare aria-hidden", () => {
    const src = readSrc("agent-files-panel.tsx");
    expect(src).toMatch(/<Eye\b[\s\S]{0,60}aria-hidden/);
    expect(src).toMatch(/<EyeOff\b[\s\S]{0,60}aria-hidden/);
    expect(src).toMatch(/<Save\b[\s\S]{0,60}aria-hidden/);
    // Both close affordances (panel + editor).
    const xMatches = [...src.matchAll(/<X\b[\s\S]{0,60}aria-hidden/g)];
    expect(xMatches.length).toBeGreaterThanOrEqual(2);
  });

  it("channel-details-panel: row/back lucide icons declare aria-hidden", () => {
    const src = readSrc("channel-details-panel.tsx");
    expect(src).toMatch(/<ChevronLeft\b[\s\S]{0,60}aria-hidden/);
    expect(src).toMatch(/<Bell\b[\s\S]{0,60}aria-hidden/);
    expect(src).toMatch(/<VolumeX\b[\s\S]{0,60}aria-hidden/);
    expect(src).toMatch(/<Search\b[\s\S]{0,60}aria-hidden/);
    expect(src).toMatch(/<Settings\b[\s\S]{0,60}aria-hidden/);
    expect(src).toMatch(/<Trash2\b[\s\S]{0,60}aria-hidden/);
  });
});
