import { describe, expect, it, vi } from "vitest";
import { render, screen } from "@testing-library/react";
import enResearch from "../../locales/en/research.json";

vi.mock("../../i18n/use-t", () => ({
  useT: () => ({
    t: (fn: (dict: typeof enResearch) => unknown, vars?: Record<string, unknown>) => {
      const raw = fn(enResearch);
      if (typeof raw !== "string" || !vars) return raw;
      return raw.replace(/\{\{(\w+)\}\}/g, (_, key: string) => String(vars[key] ?? ""));
    },
  }),
}));

import { ResearchSessionStageEnergy } from "./research-session-stage-energy";

describe("ResearchSessionStageEnergy (LRM-1285 / LRM-1279)", () => {
  it("running S2: done / current / upcoming / upcoming with data-stage-state", () => {
    const { container } = render(
      <ResearchSessionStageEnergy currentStage="s2_sources" sessionStatus="running" />,
    );
    const states = [...container.querySelectorAll("[data-stage-state]")].map((el) =>
      el.getAttribute("data-stage-state"),
    );
    expect(states).toEqual(["done", "current", "upcoming", "upcoming"]);
    expect(screen.getByText(enResearch.stage.s2_sources)).toBeTruthy();
  });

  it("completed and archived fill all segments as done", () => {
    for (const status of ["completed", "archived"] as const) {
      const { container, unmount } = render(
        <ResearchSessionStageEnergy currentStage="s2_sources" sessionStatus={status} />,
      );
      const states = [...container.querySelectorAll("[data-stage-state]")].map((el) =>
        el.getAttribute("data-stage-state"),
      );
      expect(states).toEqual(["done", "done", "done", "done"]);
      unmount();
    }
  });

  it("unknown stage falls back safely without crashing", () => {
    const { container } = render(
      <ResearchSessionStageEnergy currentStage="s9_unknown" sessionStatus="running" />,
    );
    expect(screen.getByText("s9_unknown")).toBeTruthy();
    const states = [...container.querySelectorAll("[data-stage-state]")].map((el) =>
      el.getAttribute("data-stage-state"),
    );
    // resolver: cur < 0 → idx0 current, rest upcoming
    expect(states).toEqual(["current", "upcoming", "upcoming", "upcoming"]);
  });

  it("exposes role=img aria with stage, status, and done count; segments are aria-hidden", () => {
    const { container } = render(
      <ResearchSessionStageEnergy currentStage="s2_sources" sessionStatus="running" />,
    );
    const img = screen.getByRole("img");
    expect(img.getAttribute("aria-label")).toContain(enResearch.stage.s2_sources);
    expect(img.getAttribute("aria-label")).toContain(enResearch.status.running);
    expect(img.getAttribute("aria-label")).toContain("1/4");
    for (const seg of container.querySelectorAll("[data-stage-state]")) {
      expect(seg.getAttribute("aria-hidden")).toBe("true");
    }
  });

  it("current core only pulses on group-hover/focus-within and respects motion-reduce", () => {
    const { container } = render(
      <ResearchSessionStageEnergy currentStage="s2_sources" sessionStatus="running" />,
    );
    const core = container.querySelector('[data-stage-state="current"] span');
    expect(core?.className).toContain("motion-safe:group-hover:animate-pulse");
    expect(core?.className).toContain("motion-safe:group-focus-within:animate-pulse");
    expect(core?.className).toContain("motion-reduce:animate-none");
  });

  it("uses lane CSS variables — no hex in inline styles", () => {
    const { container } = render(
      <ResearchSessionStageEnergy currentStage="s3_validation" sessionStatus="running" />,
    );
    const html = container.innerHTML;
    expect(html).toMatch(/var\(--research-lane-1\)/);
    expect(html).toMatch(/var\(--research-lane-4\)/);
    expect(html).not.toMatch(/#[0-9a-fA-F]{3,8}/);
  });
});
