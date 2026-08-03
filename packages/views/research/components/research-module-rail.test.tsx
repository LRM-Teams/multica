// @vitest-environment jsdom

import { describe, expect, it, vi } from "vitest";
import { fireEvent, render, screen } from "@testing-library/react";
import { ResearchModuleRail } from "./research-module-rail";

vi.mock("@multica/ui/hooks/use-mobile", () => ({
  useIsMobile: () => false,
}));

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
      }),
  }),
}));

describe("ResearchModuleRail (LRM-1061)", () => {
  it("toggles one module at a time via the parent", () => {
    const onSelect = vi.fn();
    render(<ResearchModuleRail active="trajectory" onSelect={onSelect} />);
    expect(
      screen.getByTestId("research-module-trajectory").getAttribute("data-active"),
    ).toBe("true");
    fireEvent.click(screen.getByTestId("research-module-sources"));
    expect(onSelect).toHaveBeenCalledWith("sources");
  });
});
