// @vitest-environment node

/**
 * LRM-1340 — settings workspace/account-tab lucide dual-announce:
 * decorative / labeled-button icons must declare aria-hidden.
 * Mutex vs LRM-1321 members-tab · LRM-1320 member-side-panel ·
 * tokens/repos tabs · research knives.
 */
import fs from "node:fs";
import path from "node:path";
import { fileURLToPath } from "node:url";
import { describe, expect, it } from "vitest";

const here = path.dirname(fileURLToPath(import.meta.url));

function readSrc(name: string) {
  return fs.readFileSync(path.join(here, name), "utf8");
}

describe("workspace/account chrome lucide a11y (LRM-1340)", () => {
  it("workspace-tab decorative lucide icons declare aria-hidden", () => {
    const src = readSrc("workspace-tab.tsx");
    expect(src).toMatch(/<Loader2\b[\s\S]{0,80}aria-hidden/);
    expect(src).toMatch(/<Camera\b[\s\S]{0,80}aria-hidden/);
    expect(src).toMatch(/<Save\b[\s\S]{0,80}aria-hidden/);
    expect(src).toMatch(/<LogOut\b[\s\S]{0,80}aria-hidden/);
  });

  it("account-tab decorative lucide icons declare aria-hidden", () => {
    const src = readSrc("account-tab.tsx");
    expect(src).toMatch(/<Loader2\b[\s\S]{0,80}aria-hidden/);
    expect(src).toMatch(/<Camera\b[\s\S]{0,80}aria-hidden/);
    expect(src).toMatch(/<Save\b[\s\S]{0,80}aria-hidden/);
  });
});
