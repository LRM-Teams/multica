// @vitest-environment jsdom

/**
 * LRM-1265 / LRM-1248·H — chrome handoff pending expression moves from submit
 * native disabled up to the trigger (aria-disabled + click/open guard).
 * Browser-level focus (activeElement ≠ BODY) is covered by Chromium probe shots.
 */
import fs from "node:fs";
import path from "node:path";
import { fileURLToPath } from "node:url";
import { describe, expect, it } from "vitest";

const here = path.dirname(fileURLToPath(import.meta.url));
const src = fs.readFileSync(
  path.join(here, "research-session-chrome-actions.tsx"),
  "utf8",
);

function handoffBlock(): string {
  const start = src.indexOf("{showHandoff ? (");
  const end = src.indexOf("{showDeliveryButton ? (", start);
  expect(start).toBeGreaterThanOrEqual(0);
  expect(end).toBeGreaterThan(start);
  return src.slice(start, end);
}

describe("LRM-1265 chrome handoff pending a11y", () => {
  it("trigger: aria-disabled + opacity + onClick/open guard; no native disabled", () => {
    const block = handoffBlock();
    expect(block).toContain('data-testid="research-session-primary"');
    expect(block).toContain("aria-disabled={handoffPending || undefined}");
    expect(block).toContain('handoffPending && "opacity-50 cursor-not-allowed"');
    expect(block).toContain("if (handoffPending) return;");
    expect(block).toContain("if (handoffPending && open) return;");
    expect(block).not.toMatch(
      /research-session-primary[\s\S]{0,400}disabled=\{handoffPending/,
    );
  });

  it("submit: only unchecked targets use native disabled (no handoffPending ||)", () => {
    const block = handoffBlock();
    expect(block).toContain("disabled={!createProject && !createChannel}");
    expect(block).not.toMatch(
      /disabled=\{handoffPending\s*\|\|\s*\(!createProject\s*&&\s*!createChannel\)\}/,
    );
  });

  it("does not touch session-page send/Stop from this patch surface", () => {
    expect(fs.existsSync(path.join(here, "research-session-page.tsx"))).toBe(true);
    const page = fs.readFileSync(path.join(here, "research-session-page.tsx"), "utf8");
    expect(page).toContain('data-testid="research-session-composer-send"');
    expect(page).toContain('data-testid="research-session-composer-stop"');
  });
});
