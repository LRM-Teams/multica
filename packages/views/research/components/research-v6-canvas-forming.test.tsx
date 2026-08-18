import { render, screen } from "@testing-library/react";
import { describe, expect, it, vi } from "vitest";
import enResearch from "../../locales/en/research.json";
import { ResearchCanvasForming } from "./research-canvas-forming";

vi.mock("../../i18n/use-t", () => ({
  useT: () => ({
    t: (selector: (bundle: typeof enResearch) => unknown) => selector(enResearch),
  }),
}));

describe("ResearchCanvasForming Director mode", () => {
  it("does not expose fixed stages or legacy fleet counters", () => {
    render(
      <ResearchCanvasForming
        directorMode
        stage="s2_sources"
        members={[]}
        tasks={[]}
      />,
    );

    const surface = screen.getByTestId("research-session-canvas-forming");
    expect(surface).toHaveAttribute("data-director-mode", "true");
    expect(screen.getByText("Ronaldo is forming the constellation")).toBeTruthy();
    expect(surface.textContent).not.toContain("S2");
    expect(surface.querySelector("dl")).toBeNull();
  });
});
