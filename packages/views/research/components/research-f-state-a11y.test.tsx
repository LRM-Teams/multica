// @vitest-environment jsdom

/**
 * LRM-1192 — [巡检][F] no-login static a11y contract for error / offline / forming.
 * Source scan + render asserts; no authenticated routes.
 */
import fs from "node:fs";
import path from "node:path";
import { fileURLToPath } from "node:url";
import { render, screen } from "@testing-library/react";
import { describe, expect, it, vi } from "vitest";
import type { SessionInterrupt } from "../lib/session-interrupt";
import { ReportSourcesFailureBanner } from "../report/report-sources-failure-banner";
import { ResearchCanvasForming } from "./research-canvas-forming";
import { ResearchOfflineBanner } from "./research-offline-banner";
import { ResearchServerErrorPage } from "./research-server-error-page";
import { ResearchSessionInterruptBanner } from "./research-session-interrupt-banner";

/** Exact structural visibility flips — do not match sm:flex-row / sm:flex-1. */
const FORBIDDEN_STRUCTURAL_SM = /\bsm:(?:hidden|block|inline-flex|flex)(?![a-zA-Z0-9_-])/;

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
          server_error_title: "Server error",
          server_error_hint: "Retry the request.",
        },
        interrupt: {
          title: "Session interrupted",
          hint: "Retry wake.",
          retry: "Retry",
          retrying: "Retrying…",
          retry_again: "Retry again",
          retry_failed_title: "Retry failed",
          retry_failed_hint: "Try again.",
          reasons: {
            unknown: "Unknown",
            runtime_offline: "Runtime offline",
            wake_failed: "Wake failed",
          },
        },
        reader: {
          sources_all_failed_title: "All sources failed",
          sources_all_failed_body: "Nothing usable.",
          sources_all_failed_retry: "Retry sources",
          sources_partial_failed_hint: "{{count}} sources failed",
        },
        session_page: {
          canvas_forming_title: "Canvas forming",
          canvas_forming_hint: "Nodes arriving…",
        },
        status: {
          paused: "Paused",
        },
        step_card: {
          standby: "Standing by",
          status: {
            waiting: "Waiting",
            running: "Running",
            failed: "Failed",
          },
        },
      }),
  }),
}));

const BANNER_FILES = [
  "research-offline-banner.tsx",
  "research-session-interrupt-banner.tsx",
  "research-server-error-page.tsx",
  "research-canvas-forming.tsx",
  path.join("..", "report", "report-sources-failure-banner.tsx"),
] as const;

describe("research F-state a11y static contract (LRM-1192)", () => {
  it("bans sm structural visibility flips on F-state banner sources", () => {
    for (const file of BANNER_FILES) {
      const src = readSrc(file);
      expect(src, file).not.toMatch(FORBIDDEN_STRUCTURAL_SM);
    }
  });

  it("source: interrupt / server-error / sources-failure expose role=alert", () => {
    expect(readSrc("research-session-interrupt-banner.tsx")).toMatch(
      /role=["']alert["']/,
    );
    expect(readSrc("research-server-error-page.tsx")).toMatch(/role=["']alert["']/);
    expect(readSrc("..", "report", "report-sources-failure-banner.tsx")).toMatch(
      /role=["']alert["']/,
    );
  });

  it("source: canvas forming exposes mode-aware aria-busy + aria-live=polite", () => {
    const src = readSrc("research-canvas-forming.tsx");
    expect(src).toMatch(/aria-busy=\{mode === ["']forming["']\}/);
    expect(src).toMatch(/aria-live=["']polite["']/);
  });

  it("source: offline banner keeps a single <output> shell; failed adds role=alert", () => {
    const src = readSrc("research-offline-banner.tsx");
    // LRM-1345 A-1 — contract change (not a relaxation). Previous assertions:
    //   expect(src).toMatch(/if\s*\(\s*failed\s*\)/);
    //   expect(src).toMatch(/role=["']alert["']/);
    // The `if (failed)` early return is now banned: two different shell tags made
    // React remount the subtree on every mode change and drop Retry focus to body.
    expect(src).not.toMatch(/if\s*\(\s*failed\s*\)/);
    expect(src.match(/return \(/g)?.length).toBe(1);
    expect(src).toMatch(/role=\{failed \? ["']alert["'] : undefined\}/);
    expect(src).toMatch(/<output\b/);
  });

  it("render: interrupt / server-error / sources-all-failed are alerts", () => {
    const interrupt: SessionInterrupt = {
      messageId: "wake-1",
      reason: "runtime_offline",
      headline: "Agent stopped",
      recoveryHint: null,
      createdAt: "2026-08-03T12:00:00Z",
    };
    render(
      <ResearchSessionInterruptBanner
        interrupt={interrupt}
        phase="idle"
        onRetry={() => {}}
      />,
    );
    expect(screen.getByTestId("research-session-interrupt-banner").getAttribute("role")).toBe(
      "alert",
    );

    render(<ResearchServerErrorPage onRetry={() => {}} />);
    expect(screen.getByTestId("research-server-error-page").getAttribute("role")).toBe("alert");

    render(<ReportSourcesFailureBanner mode="all" failedCount={3} onRetry={() => {}} />);
    expect(screen.getByTestId("research-sources-all-failed").getAttribute("role")).toBe("alert");
  });

  it("render: offline/reconnecting are status; failed is alert", () => {
    const { unmount: u1 } = render(<ResearchOfflineBanner mode="offline" />);
    const offline = screen.getByTestId("research-offline-banner");
    expect(offline.tagName.toLowerCase()).toBe("output");
    expect(offline.getAttribute("role")).toBeNull();
    u1();

    const { unmount: u2 } = render(
      <ResearchOfflineBanner mode="reconnecting" onRetry={() => {}} />,
    );
    const reconnecting = screen.getByTestId("research-offline-banner");
    expect(reconnecting.tagName.toLowerCase()).toBe("output");
    u2();

    render(<ResearchOfflineBanner mode="failed" onRetry={() => {}} />);
    const failed = screen.getByTestId("research-offline-banner");
    expect(failed.getAttribute("role")).toBe("alert");
    // LRM-1345 A-1 — contract change (not a relaxation). Previous assertion:
    //   expect(failed.tagName.toLowerCase()).not.toBe("output");
    // failed now rides the same <output> shell with role="alert" so the focused
    // Retry node survives the failed ⇄ reconnecting transition.
    expect(failed.tagName.toLowerCase()).toBe("output");
  });

  it("render: canvas forming announces busy politely while stalled is not busy", () => {
    const { unmount } = render(<ResearchCanvasForming />);
    const forming = screen.getByTestId("research-session-canvas-forming");
    expect(forming.getAttribute("aria-busy")).toBe("true");
    expect(forming.getAttribute("aria-live")).toBe("polite");
    unmount();

    render(<ResearchCanvasForming mode="stalled" />);
    const stalled = screen.getByTestId("research-session-canvas-forming");
    expect(stalled.getAttribute("aria-busy")).toBe("false");
    expect(stalled.getAttribute("aria-live")).toBe("polite");
  });
});
