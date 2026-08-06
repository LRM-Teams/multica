// @vitest-environment node

/**
 * LRM-1321 — settings members-tab lucide dual-announce:
 * decorative icons + icon-only row/revoke controls must declare aria-hidden
 * (and icon-only buttons keep accessible names). Mutex vs LRM-1320
 * member-side-panel · LRM-1305 channels chrome · research knives.
 */
import fs from "node:fs";
import path from "node:path";
import { fileURLToPath } from "node:url";
import { describe, expect, it } from "vitest";

const here = path.dirname(fileURLToPath(import.meta.url));
const SRC = "members-tab.tsx";

function readSrc() {
  return fs.readFileSync(path.join(here, SRC), "utf8");
}

describe("members-tab chrome lucide a11y (LRM-1321)", () => {
  it("decorative lucide icons declare aria-hidden", () => {
    const src = readSrc();
    expect(src).toMatch(/<Users\b[\s\S]{0,60}aria-hidden/);
    expect(src).toMatch(/<Plus\b[\s\S]{0,60}aria-hidden/);
    expect(src).toMatch(/<Mail\b[\s\S]{0,60}aria-hidden/);
    expect(src).toMatch(/<Shield\b[\s\S]{0,60}aria-hidden/);
    expect(src).toMatch(/<UserMinus\b[\s\S]{0,60}aria-hidden/);
    expect(src).toMatch(/<RoleIcon\b[\s\S]{0,60}aria-hidden/);
    // Section + pending-row clocks.
    const clockMatches = [...src.matchAll(/<Clock\b[\s\S]{0,60}aria-hidden/g)];
    expect(clockMatches.length).toBeGreaterThanOrEqual(2);
  });

  it("icon-only row menu / revoke keep aria-label; icons are aria-hidden", () => {
    const src = readSrc();
    expect(src).toMatch(
      /aria-label=\{rowMenuAria\}[\s\S]{0,120}data-testid="members-tab-row-menu"[\s\S]{0,160}<MoreHorizontal\b[\s\S]{0,60}aria-hidden/,
    );
    expect(src).toMatch(
      /aria-label=\{t\(\(\$\) => \$\.members\.revoke_invitation_tooltip\)\}[\s\S]{0,200}data-testid="members-tab-revoke-invitation"[\s\S]{0,160}<X\b[\s\S]{0,60}aria-hidden/,
    );
  });
});
