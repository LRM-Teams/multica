// @vitest-environment jsdom

import { describe, expect, it, vi } from "vitest";
import { fireEvent, render, screen } from "@testing-library/react";
import { ResearchModuleRail } from "./research-module-rail";
import { ResearchCanvasDock } from "./research-canvas-dock";

vi.mock("../../i18n/use-t", () => ({
  useT: () => ({
    t: (fn: (dict: Record<string, unknown>) => unknown) =>
      fn({
        panel: {
          module_trajectory: "Search trail",
          module_sources: "Source strategy",
          module_detail: "Node detail",
          module_trajectory_ico: "Trail",
          module_sources_ico: "Src",
          module_detail_ico: "Detail",
        },
        dock: {
          label: "Research canvas tools",
          zoom_in: "Zoom in",
          zoom_out: "Zoom out",
          fit: "Fit view",
          toggle_detail: "Toggle detail panel",
        },
      }),
  }),
}));

describe("ResearchModuleRail (LRM-1151 Dock)", () => {
  it("toggles one module at a time via the parent", () => {
    const onSelect = vi.fn();
    render(<ResearchModuleRail active="trajectory" onSelect={onSelect} />);
    expect(
      screen.getByTestId("research-module-trajectory").getAttribute("data-active"),
    ).toBe("true");
    fireEvent.click(screen.getByTestId("research-module-sources"));
    expect(onSelect).toHaveBeenCalledWith("sources");
  });

  it("renders bar layout as a full-width three-equal group", () => {
    render(
      <ResearchModuleRail layout="bar" active={null} onSelect={vi.fn()} />,
    );
    expect(screen.getByTestId("research-module-rail").getAttribute("data-layout")).toBe(
      "bar",
    );
  });

  it("sources dock shows full title; bar keeps short glyph + full aria-label (LRM-1329)", () => {
    const { rerender } = render(
      <ResearchModuleRail layout="dock" active={null} onSelect={vi.fn()} />,
    );
    const dockBtn = screen.getByTestId("research-module-sources");
    expect(dockBtn.textContent).toMatch(/Source strategy/);
    expect(dockBtn.className).not.toMatch(/max-w-\[4\.5rem\]/);

    rerender(
      <ResearchModuleRail layout="bar" active={null} onSelect={vi.fn()} />,
    );
    const barBtn = screen.getByTestId("research-module-sources");
    expect(barBtn.getAttribute("aria-label")).toBe("Source strategy");
    expect(barBtn.textContent).toMatch(/Src/);
  });
});

describe("ResearchCanvasDock (LRM-1151)", () => {
  it("hosts module group + zoom + detail with mutual aria-pressed", () => {
    const onSelectModule = vi.fn();
    render(
      <ResearchCanvasDock
        zoomPct={100}
        onZoomIn={vi.fn()}
        onZoomOut={vi.fn()}
        onFit={vi.fn()}
        detailOpen={false}
        onToggleDetail={vi.fn()}
        activeModule="sources"
        onSelectModule={onSelectModule}
      />,
    );
    expect(screen.getByTestId("research-canvas-dock").getAttribute("data-layout")).toBe(
      "desktop",
    );
    expect(
      screen.getByTestId("research-module-sources").getAttribute("aria-pressed"),
    ).toBe("true");
    expect(
      screen.getByTestId("research-module-trajectory").getAttribute("aria-pressed"),
    ).toBe("false");
    fireEvent.click(screen.getByTestId("research-module-detail"));
    expect(onSelectModule).toHaveBeenCalledWith("detail");
  });

  it("hides zoom on mobile layout", () => {
    render(
      <ResearchCanvasDock
        layout="mobile"
        zoomPct={100}
        onZoomIn={vi.fn()}
        onZoomOut={vi.fn()}
        onFit={vi.fn()}
        showZoom={false}
        showDetailToggle={false}
        detailOpen={false}
        onToggleDetail={vi.fn()}
        activeModule={null}
        onSelectModule={vi.fn()}
      />,
    );
    expect(screen.getByTestId("research-canvas-dock").getAttribute("data-layout")).toBe(
      "mobile",
    );
    expect(screen.queryByLabelText("Zoom in")).toBeNull();
  });
});
