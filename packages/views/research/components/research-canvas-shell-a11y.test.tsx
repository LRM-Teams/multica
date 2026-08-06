// @vitest-environment jsdom

/**
 * LRM-1226 — [巡检][F] no-login static a11y for canvas shell dock.
 * forming/empty covered by LRM-1192 / LRM-1197 — do not reopen.
 * Does not touch canvas/graph-node/lane or module-rail production (1204).
 */
import fs from "node:fs";
import path from "node:path";
import { fileURLToPath } from "node:url";
import { render, screen, within } from "@testing-library/react";
import { describe, expect, it, vi } from "vitest";
import { ResearchCanvasDock } from "./research-canvas-dock";

/** Exact structural visibility flips — do not match sm:flex-row / sm:flex-1. */
const FORBIDDEN_STRUCTURAL_SM =
  /\bsm:(?:hidden|block|inline-flex|flex)(?![a-zA-Z0-9_-])/;

const here = path.dirname(fileURLToPath(import.meta.url));

function readSrc(...parts: string[]) {
  return fs.readFileSync(path.join(here, ...parts), "utf8");
}

vi.mock("../../i18n/use-t", () => ({
  useT: () => ({
    t: (fn: (dict: Record<string, unknown>) => unknown) =>
      fn({
        dock: {
          label: "Canvas dock",
          zoom_out: "Zoom out",
          zoom_in: "Zoom in",
          fit: "Fit to view",
          toggle_detail: "Toggle detail panel",
        },
        panel: {
          module_trajectory: "Trajectory",
          module_sources: "Sources",
          module_detail: "Detail",
          module_trajectory_ico: "轨",
          module_sources_ico: "源",
          module_detail_ico: "详",
        },
      }),
  }),
}));

/** Avoid asserting/owning module-rail production (LRM-1204). */
vi.mock("./research-module-rail", () => ({
  ResearchModuleRail: ({ layout }: { layout?: string }) => (
    <div data-testid="mock-module-rail" data-layout={layout ?? "dock"} />
  ),
}));

const DOCK_FILE = "research-canvas-dock.tsx";

const baseProps = {
  zoomPct: 100,
  onZoomIn: vi.fn(),
  onZoomOut: vi.fn(),
  onFit: vi.fn(),
  detailOpen: false,
  onToggleDetail: vi.fn(),
};

describe("research canvas shell dock a11y static contract (LRM-1226)", () => {
  it("bans sm structural visibility flips on dock source", () => {
    expect(readSrc(DOCK_FILE)).not.toMatch(FORBIDDEN_STRUCTURAL_SM);
  });

  it("source: root is toolbar with accessible name + testid; dividers aria-hidden", () => {
    const src = readSrc(DOCK_FILE);
    expect(src).toMatch(/role=["']toolbar["']/);
    expect(src).toMatch(/aria-label=\{t\(\(\$\) => \$\.dock\.label\)\}/);
    expect(src).toMatch(/data-testid=["']research-canvas-dock["']/);
    expect(src).toMatch(/aria-hidden/);
    expect(src).toMatch(/aria-pressed=\{detailOpen\}/);
    expect(src).toMatch(/aria-busy=\{disabled \|\| undefined\}/);
  });

  it("source: zoom / fit / detail controls carry aria-label; icons aria-hidden", () => {
    const src = readSrc(DOCK_FILE);
    expect(src).toMatch(/aria-label=\{t\(\(\$\) => \$\.dock\.zoom_out\)\}/);
    expect(src).toMatch(/aria-label=\{t\(\(\$\) => \$\.dock\.zoom_in\)\}/);
    expect(src).toMatch(/aria-label=\{t\(\(\$\) => \$\.dock\.fit\)\}/);
    expect(src).toMatch(/aria-label=\{t\(\(\$\) => \$\.dock\.toggle_detail\)\}/);
    // Decorative lucide icons inside labeled buttons must not dual-announce.
    expect(src).toMatch(/<ZoomOut\b[\s\S]{0,60}aria-hidden/);
    expect(src).toMatch(/<ZoomIn\b[\s\S]{0,60}aria-hidden/);
    expect(src).toMatch(/<Scan\b[\s\S]{0,60}aria-hidden/);
    expect(src).toMatch(/<PanelRight\b[\s\S]{0,60}aria-hidden/);
  });

  it("render: desktop toolbar named; zoom/fit/detail labeled; pressed tracks detailOpen", () => {
    const { rerender } = render(
      <ResearchCanvasDock {...baseProps} detailOpen={false} />,
    );
    const toolbar = screen.getByRole("toolbar", { name: "Canvas dock" });
    expect(toolbar).toHaveAttribute("data-testid", "research-canvas-dock");
    expect(toolbar).toHaveAttribute("data-layout", "desktop");

    expect(
      within(toolbar).getByRole("button", { name: "Zoom out" }),
    ).toBeTruthy();
    expect(
      within(toolbar).getByRole("button", { name: "Zoom in" }),
    ).toBeTruthy();
    expect(
      within(toolbar).getByRole("button", { name: "Fit to view" }),
    ).toBeTruthy();
    const detail = within(toolbar).getByRole("button", {
      name: "Toggle detail panel",
    });
    expect(detail).toHaveAttribute("aria-pressed", "false");

    rerender(<ResearchCanvasDock {...baseProps} detailOpen />);
    expect(
      within(
        screen.getByRole("toolbar", { name: "Canvas dock" }),
      ).getByRole("button", { name: "Toggle detail panel" }),
    ).toHaveAttribute("aria-pressed", "true");
  });

  it("render: disabled sets aria-busy and disables controls", () => {
    render(<ResearchCanvasDock {...baseProps} disabled />);
    const toolbar = screen.getByRole("toolbar", { name: "Canvas dock" });
    expect(toolbar).toHaveAttribute("aria-busy", "true");
    for (const name of [
      "Zoom out",
      "Zoom in",
      "Fit to view",
      "Toggle detail panel",
    ]) {
      expect(within(toolbar).getByRole("button", { name })).toBeDisabled();
    }
  });

  it("render: mobile keeps toolbar name and does not expose desktop zoom/detail", () => {
    render(
      <ResearchCanvasDock
        {...baseProps}
        layout="mobile"
        onSelectModule={vi.fn()}
      />,
    );
    const toolbar = screen.getByRole("toolbar", { name: "Canvas dock" });
    expect(toolbar).toHaveAttribute("data-layout", "mobile");
    expect(
      within(toolbar).queryByRole("button", { name: "Zoom out" }),
    ).toBeNull();
    expect(
      within(toolbar).queryByRole("button", { name: "Zoom in" }),
    ).toBeNull();
    expect(
      within(toolbar).queryByRole("button", { name: "Fit to view" }),
    ).toBeNull();
    expect(
      within(toolbar).queryByRole("button", { name: "Toggle detail panel" }),
    ).toBeNull();
    expect(within(toolbar).getByTestId("mock-module-rail")).toHaveAttribute(
      "data-layout",
      "bar",
    );
  });
});
