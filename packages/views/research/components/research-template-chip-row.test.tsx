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
 * LRM-1189: drop raw-hex selected glow; selected + hover dark halves match the
 * LRM-1175 inject-tag blue tone. Light classes stay frozen.
 */
describe("LRM-1189 research template chip row · tokens and dark parity", () => {
  it("has no raw hex and no arbitrary shadow anywhere in the source", () => {
    expect(SOURCE).not.toMatch(/#[0-9a-fA-F]{3,8}\b/);
    expect(SOURCE).not.toContain("shadow-[");
  });

  it("selected chip keeps the frozen light triple and gains dark variants", () => {
    render(
      <ResearchTemplateChipRow selectedId={FIRST.id} onToggle={() => {}} />,
    );
    const className = chipClass(FIRST.id);

    expect(className).toContain("border-blue-400");
    expect(className).toContain("bg-blue-50");
    expect(className).toContain("text-blue-700");
    expect(className).toContain("dark:bg-blue-400/[0.14]");
    expect(className).toContain("dark:text-blue-200");
    expect(className).toContain("dark:border-blue-400/45");
    expect(className).not.toContain("shadow-[");
  });

  it("unselected chip hover tone also carries a dark variant", () => {
    render(<ResearchTemplateChipRow selectedId={null} onToggle={() => {}} />);
    const className = chipClass(FIRST.id);

    expect(className).toContain("hover:border-blue-300");
    expect(className).toContain("hover:bg-blue-50/60");
    expect(className).toContain("hover:text-blue-700");
    expect(className).toContain("dark:hover:bg-blue-400/[0.10]");
    expect(className).toContain("dark:hover:text-blue-200");
    expect(className).toContain("dark:hover:border-blue-400/45");
  });

  it("every light blue tone in the source has a dark counterpart on the same line", () => {
    const toneLines = SOURCE.split("\n").filter((line) =>
      /(bg-blue-50|text-blue-700|border-blue-(300|400))/.test(line),
    );

    expect(toneLines.length).toBeGreaterThan(0);
    for (const line of toneLines) {
      expect(line).toMatch(/dark:/);
    }
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
    expect(SOURCE).toContain("focus-visible:ring-brand/30");
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
