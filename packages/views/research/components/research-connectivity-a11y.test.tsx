// @vitest-environment jsdom

/**
 * LRM-1232 — [巡检][F] connectivity offline banner a11y.
 * LRM-1192 locked output/alert tags; this knife adds reconnecting aria-busy.
 * Does not touch fleet-strip / canvas / graph-node / reduced-motion (1165).
 */
import fs from "node:fs";
import path from "node:path";
import { fileURLToPath } from "node:url";
import { render, screen } from "@testing-library/react";
import { describe, expect, it, vi } from "vitest";
import { ResearchOfflineBanner } from "./research-offline-banner";

const here = path.dirname(fileURLToPath(import.meta.url));

function readSrc(...parts: string[]) {
  return fs.readFileSync(path.join(here, ...parts), "utf8");
}

vi.mock("../../i18n/use-t", () => ({
  useT: () => ({
    t: (fn: (dict: Record<string, unknown>) => unknown) =>
      fn({
        connectivity: {
          offline_title: "You are offline",
          offline_hint: "Stay put.",
          reconnecting_title: "Reconnecting…",
          reconnecting_hint: "Refreshing…",
          reconnect_failed_title: "Reconnect failed",
          reconnect_failed_hint: "Try again.",
          retry: "Retry",
          retrying: "Retrying…",
        },
      }),
  }),
}));

const BANNER_FILE = "research-offline-banner.tsx";

describe("research connectivity offline banner a11y (LRM-1232)", () => {
  it("source: reconnecting sets aria-busy; failed upgrades the same output to alert", () => {
    const src = readSrc(BANNER_FILE);
    expect(src).toMatch(/aria-busy=\{reconnecting\s*\|\|\s*undefined\}/);
    // LRM-1345 A-1 — was `role="alert"` on a separate <div>; the role now rides the
    // single <output> shell so the subtree (and focus) survives a mode change.
    expect(src).toMatch(/role=\{failed \? ["']alert["'] : undefined\}/);
    expect(src).toMatch(/<output\b/);
    // A-2 — no native disabled anywhere on the retry control.
    expect(src).not.toMatch(/\bdisabled=\{reconnecting\}/);
    expect(src).toMatch(/aria-disabled=\{reconnecting\s*\|\|\s*undefined\}/);
  });

  it("render: offline is output without aria-busy", () => {
    render(<ResearchOfflineBanner mode="offline" />);
    const banner = screen.getByTestId("research-offline-banner");
    expect(banner.tagName.toLowerCase()).toBe("output");
    expect(banner.getAttribute("aria-busy")).toBeNull();
    expect(banner.getAttribute("role")).toBeNull();
  });

  // LRM-1345 A-2 — contract change (not a relaxation). Previous assertion:
  //   expect((retry as HTMLButtonElement).disabled).toBe(true);
  it("render: reconnecting is output with aria-busy; retry aria-disabled but focusable", () => {
    render(<ResearchOfflineBanner mode="reconnecting" onRetry={() => {}} />);
    const banner = screen.getByTestId("research-offline-banner");
    expect(banner.tagName.toLowerCase()).toBe("output");
    expect(banner.getAttribute("aria-busy")).toBe("true");
    const retry = screen.getByTestId("research-offline-banner-retry");
    expect((retry as HTMLButtonElement).disabled).toBe(false);
    expect(retry.getAttribute("aria-disabled")).toBe("true");
    retry.focus();
    expect(document.activeElement).toBe(retry);
  });

  // LRM-1345 A-1 — contract change (not a relaxation). Previous assertion:
  //   expect(banner.tagName.toLowerCase()).not.toBe("output");
  it("render: failed is an alert on the same output shell, without aria-busy", () => {
    render(<ResearchOfflineBanner mode="failed" onRetry={() => {}} />);
    const banner = screen.getByTestId("research-offline-banner");
    expect(banner.getAttribute("role")).toBe("alert");
    expect(banner.tagName.toLowerCase()).toBe("output");
    expect(banner.getAttribute("aria-busy")).toBeNull();
  });
});
