// @vitest-environment jsdom

/**
 * LRM-1204 — [巡检][F] no-login static a11y for exploration + module rails.
 * Source scan + render asserts; mutually exclusive from 1202/1203/1201/1170/1164.
 */
import fs from "node:fs";
import path from "node:path";
import { fileURLToPath } from "node:url";
import { render, screen, within } from "@testing-library/react";
import { describe, expect, it, vi } from "vitest";
import type { ExplorationDimension } from "../lib/m2-visibility";
import { ExplorationRail } from "./exploration-rail";
import { ResearchModuleRail } from "./research-module-rail";

/** Exact structural visibility flips — do not match sm:flex-row / sm:flex-1. */
const FORBIDDEN_STRUCTURAL_SM =
  /\bsm:(?:hidden|block|inline-flex|flex)(?![a-zA-Z0-9_-])/;

const here = path.dirname(fileURLToPath(import.meta.url));

function readSrc(...parts: string[]) {
  return fs.readFileSync(path.join(here, ...parts), "utf8");
}

const RAIL_FILES = ["exploration-rail.tsx", "research-module-rail.tsx"] as const;

vi.mock("../../i18n/use-t", () => ({
  useT: () => ({
    t: (
      fn: (dict: Record<string, unknown>) => unknown,
      vars?: Record<string, unknown>,
    ) => {
      const out = fn({
        m2: {
          rail_title: "Exploration",
          rail_hint: "Review verified directions",
          rail_loading: "Generating dimensions…",
          rail_loading_hint: "Results appear when ready",
          rail_error: "Failed to load rail",
          rail_error_title: "Could not organize the exploration trail",
          rail_error_body: "Please try again later; technical details are hidden.",
          rail_empty_title: "No dimensions yet",
          rail_empty_body: "Start the session to explore.",
          rail_empty_expect_verified: "Verified directions",
          rail_empty_expect_gap: "Questions needing evidence",
          rail_empty_expect_reuse: "Reusable findings",
          rail_question_count: "{{count}} questions",
          rail_summary_pending: "Summary pending",
          rail_summary_verified: "{{count}} directions verified",
          rail_summary_adopted: "{{count}} findings adopted",
          rail_summary_dead: "{{count}} without a usable conclusion",
          rail_summary_joiner: " · ",
          rail_completed_banner: "This research is complete",
          rail_completed_directions: "{{count}} directions",
          rail_completed_findings: "{{count}} findings",
          rail_result_prefix: "Result: ",
          rail_result_open: "Collecting evidence",
          rail_result_covered_fallback: "A usable conclusion is ready",
          rail_result_gap: "Evidence is insufficient",
          rail_result_dead: "No usable conclusion yet",
          rail_result_dead_reason: "Reason: {{reason}}",
          rail_next_expand_covered: "{{count}} questions · expand",
          rail_next_expand_gap: "{{count}} questions · expand gaps",
          rail_next_expand_dead: "{{count}} questions · collapse",
          rail_collapse: "Collapse",
          rail_ready_live: "Exploration trail ready",
          required: "Required",
          status: {
            open: "Verifying",
            covered: "Adopted",
            gap: "Needs evidence",
            dead: "No conclusion",
          },
        },
        session_page: { retry: "Retry" },
        panel: {
          module_trajectory_ico: "轨",
          module_sources_ico: "源",
          module_detail_ico: "详",
          module_trajectory: "Exploration trail",
          module_sources: "Sources",
          module_detail: "Detail",
        },
      });
      if (typeof out === "string" && vars) {
        return out.replace(/\{\{(\w+)\}\}/g, (_, k: string) =>
          String(vars[k] ?? ""),
        );
      }
      return out;
    },
  }),
}));

const sampleDim: ExplorationDimension = {
  family: "market",
  title: "Market",
  status: "open",
  required: true,
  questions: [{ id: "q1", title: "Who buys?", nodeType: "probe" }],
  findingSummary: "Early signal",
};

describe("research rails a11y static contract (LRM-1204)", () => {
  it("bans sm structural visibility flips on rail sources", () => {
    for (const file of RAIL_FILES) {
      const src = readSrc(file);
      expect(src, file).not.toMatch(FORBIDDEN_STRUCTURAL_SM);
    }
  });

  it("source: ExplorationRail declares labelled shell + loading live region", () => {
    const src = readSrc("exploration-rail.tsx");
    expect(src).toMatch(/data-testid=["']exploration-rail["']/);
    expect(src).toMatch(/aria-labelledby=\{labelledBy\}/);
    expect(src).toMatch(/data-testid=["']exploration-rail-loading["']/);
    expect(src).toMatch(/aria-busy/);
    expect(src).toMatch(/aria-live=["']polite["']/);
    expect(src).toMatch(/role=["']alert["']/);
    expect(src).toMatch(/data-testid=["']exploration-rail-error["']/);
    expect(src).toMatch(/aria-expanded=\{expanded\}/);
  });

  it("source: ResearchModuleRail exposes pressed toggles", () => {
    const src = readSrc("research-module-rail.tsx");
    expect(src).toMatch(/data-testid=["']research-module-rail["']/);
    expect(src).toMatch(/aria-pressed=\{on\}/);
  });

  it("render: loading rail is busy + polite; spinner aria-hidden", () => {
    const { container } = render(
      <ExplorationRail dimensions={[]} sessionStatus="running" />,
    );
    const root = container.querySelector('[data-testid="exploration-rail"]');
    expect(root).toBeTruthy();
    expect(root).toHaveAttribute("aria-labelledby");
    // LRM-1201: busy + polite live region live on the shell, not the loading subtree.
    expect(root).toHaveAttribute("aria-busy", "true");
    const live = container.querySelector(
      '[data-testid="exploration-rail-live"]',
    );
    expect(live).toHaveAttribute("aria-live", "polite");
    expect(live).toHaveTextContent("Generating dimensions…");
    const loading = container.querySelector(
      '[data-testid="exploration-rail-loading"]',
    );
    expect(loading).toBeTruthy();
    expect(
      within(loading as HTMLElement).getByText("Generating dimensions…"),
    ).toBeTruthy();
    const spinner = loading?.querySelector("svg");
    expect(spinner).toHaveAttribute("aria-hidden", "true");
  });

  it("render: error rail is alert; empty has perceivable copy", () => {
    const { rerender, container } = render(
      <ExplorationRail dimensions={[]} error="boom" />,
    );
    const err = container.querySelector(
      '[data-testid="exploration-rail-error"]',
    );
    expect(err).toHaveAttribute("role", "alert");
    // LRM-1281/1287: raw error never enters DOM — only safe user copy.
    expect(container.textContent).not.toContain("boom");
    expect(
      screen.getByText("Could not organize the exploration trail"),
    ).toBeTruthy();

    rerender(<ExplorationRail dimensions={[]} sessionStatus="drafting" />);
    const empty = container.querySelector(
      '[data-testid="exploration-rail-empty"]',
    );
    expect(empty).toBeTruthy();
    // Empty title is also mirrored into the persistent live region — scope to empty.
    expect(within(empty as HTMLElement).getByText("No dimensions yet")).toBeTruthy();
    expect(
      within(empty as HTMLElement).getByText("Start the session to explore."),
    ).toBeTruthy();
  });

  it("render: ready cards expose aria-expanded; decorative icons hidden", () => {
    const { container } = render(
      <ExplorationRail
        dimensions={[sampleDim]}
        selectedFamily="market"
        onSelectFamily={() => {}}
      />,
    );
    const toggle = container.querySelector(
      '[data-testid="exploration-result-card"] button[aria-expanded]',
    );
    expect(toggle).toHaveAttribute("aria-expanded", "true");
    const decor = [
      ...(toggle?.querySelectorAll("[aria-hidden]") ?? []),
    ];
    expect(decor.length).toBeGreaterThan(0);
    for (const el of decor) {
      expect(el).toHaveAttribute("aria-hidden", "true");
    }
  });

  it("render: module rail pressed state matches active panel", () => {
    const { container, rerender } = render(
      <ResearchModuleRail active="sources" onSelect={() => {}} />,
    );
    const root = container.querySelector(
      '[data-testid="research-module-rail"]',
    );
    expect(root).toBeTruthy();
    expect(
      container.querySelector('[data-testid="research-module-sources"]'),
    ).toHaveAttribute("aria-pressed", "true");
    expect(
      container.querySelector('[data-testid="research-module-trajectory"]'),
    ).toHaveAttribute("aria-pressed", "false");

    rerender(<ResearchModuleRail active={null} onSelect={() => {}} />);
    for (const id of ["trajectory", "sources", "detail"] as const) {
      expect(
        container.querySelector(`[data-testid="research-module-${id}"]`),
      ).toHaveAttribute("aria-pressed", "false");
    }
  });
});
