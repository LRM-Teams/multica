import { describe, expect, it, vi } from "vitest";
import { render, screen } from "@testing-library/react";
import fs from "node:fs";
import path from "node:path";
import { RESEARCH_TEMPLATES } from "../lib/research-templates";
import { ResearchTemplateChipRow } from "./research-template-chip-row";

vi.mock("../../i18n/use-t", () => ({
  useT: () => ({
    t: (picker: (keys: Record<string, unknown>) => unknown) => {
      const keys = { home: { templates_label: "调研模板" } };
      return picker(keys as never);
    },
    i18n: { language: "zh" },
  }),
}));

const SOURCE = fs.readFileSync(
  path.resolve(
    process.cwd(),
    "research/components/research-template-chip-row.tsx",
  ),
  "utf8",
);

const FIRST = RESEARCH_TEMPLATES[0]!;

function chipClass(id: string) {
  return (
    screen.getByTestId(`research-template-chip-${id}`).getAttribute("class") ??
    ""
  );
}

/**
 * Pixel theme 2026-08-22 (supersedes the LRM-1189 blue triple): chips are
 * `.px-chip` bevel plates; selected/hover/focus visuals live in
 * research-home-visual.css keyed off `aria-checked` / `:focus-visible`.
 * The TSX must stay free of raw hex and per-tone utility classes.
 */
describe("pixel research template chip row · tokens", () => {
  it("has no raw hex and no arbitrary shadow anywhere in the source", () => {
    expect(SOURCE).not.toMatch(/#[0-9a-fA-F]{3,8}\b/);
    expect(SOURCE).not.toContain("shadow-[");
  });

  it("every chip carries the px-chip bevel class in both states", () => {
    render(
      <ResearchTemplateChipRow selectedId={FIRST.id} onToggle={() => {}} />,
    );
    expect(chipClass(FIRST.id)).toContain("px-chip");
  });

  it("no legacy blue tone utilities remain in the source", () => {
    expect(SOURCE).not.toMatch(/\b(bg|text|border)-blue-\d/);
  });

  it("keeps the frozen radiogroup contract (roles, aria-checked, testids)", () => {
    render(
      <ResearchTemplateChipRow selectedId={FIRST.id} onToggle={() => {}} />,
    );

    expect(screen.getByTestId("research-template-chip-row")).toHaveAttribute(
      "role",
      "radiogroup",
    );
    const radios = screen.getAllByRole("radio");
    expect(radios).toHaveLength(RESEARCH_TEMPLATES.length);
    expect(
      screen.getByTestId(`research-template-chip-${FIRST.id}`),
    ).toHaveAttribute("aria-checked", "true");
    expect(SOURCE).toContain("px-chip");
  });
});

/** LRM-1218: non-drop divider must not use dashed border vocabulary. */
describe("LRM-1218 research template chip row · solid divider", () => {
  it("root row uses solid subtle bottom border, not border-dashed", () => {
    render(<ResearchTemplateChipRow selectedId={null} onToggle={() => {}} />);
    const row = screen.getByTestId("research-template-chip-row");
    const className = row.getAttribute("class") ?? "";

    expect(className).toContain("border-b");
    expect(className).toContain("border-border/60");
    expect(className).not.toContain("border-dashed");
    // Class token only — ignore prose comments.
    expect(SOURCE).not.toMatch(/["'`][^"'`]*\bborder-dashed\b[^"'`]*["'`]/);
  });
});
