// @vitest-environment jsdom

/**
 * LRM-1204 — [巡检][F] no-login static a11y for exploration + module rails.
 * Source scan + render asserts; mutually exclusive from 1202/1203/1201/1170/1164.
 */
import fs from "node:fs";
import path from "node:path";
import { fileURLToPath } from "node:url";
import { render, screen } from "@testing-library/react";
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
    t: (fn: (dict: Record<string, unknown>) => unknown) =>
      fn({
        m2: {
          rail_title: "Exploration",
          rail_hint: "Dimensions to cover",
          rail_loading: "Generating dimensions…",
          rail_error: "Failed to load rail",
          rail_empty_title: "No dimensions yet",
          rail_empty_body: "Start the session to explore.",
          rail_question_count: "{{count}} questions",
          rail_summary_pending: "Summary pending",
          required: "Required",
          status: {
            open: "Open",
            covered: "Covered",
            gap: "Gap",
            dead: "Dead",
          },
        },
        session_page: { retry: "Retry" },
        panel: {
          module_trajectory_ico: "轨",
          module_sources_ico: "源",
          module_detail_ico: "详",
          module_trajectory: "Trajectory",
          module_sources: "Sources",
          module_detail: "Detail",
        },
      }),
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
    const loading = container.querySelector(
      '[data-testid="exploration-rail-loading"]',
    );
    expect(loading).toBeTruthy();
    expect(loading).toHaveAttribute("aria-busy", "true");
    expect(loading).toHaveAttribute("aria-live", "polite");
    expect(screen.getByText("Generating dimensions…")).toBeTruthy();
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
    expect(screen.getByText("boom")).toBeTruthy();

    rerender(<ExplorationRail dimensions={[]} sessionStatus="drafting" />);
    expect(
      container.querySelector('[data-testid="exploration-rail-empty"]'),
    ).toBeTruthy();
    expect(screen.getByText("No dimensions yet")).toBeTruthy();
    expect(screen.getByText("Start the session to explore.")).toBeTruthy();
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
