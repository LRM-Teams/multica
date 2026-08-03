import { describe, expect, it, vi } from "vitest";
import { render, screen } from "@testing-library/react";
import fs from "node:fs";
import path from "node:path";
import { RESEARCH_TEMPLATES } from "../lib/research-templates";
import { ResearchTemplateChipRow } from "./research-template-chip-row";

vi.mock("../../i18n/use-t", () => ({
  useT: () => ({
    t: (picker: (keys: Record<string, unknown>) => unknown) => {
      const keys = {
        home: {
          templates_label: "调研模板",
        },
      };
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

/**
 * LRM-1189: template chip selected glow must drop raw hex, and both selected +
 * hover states need dark: halves matching the LRM-1175 inject-tag blue tone.
 */
describe("LRM-1189 research template chip row · no raw hex + dark tones", () => {
  it("source has no raw hex and no shadow-[0_0_0_2px_…] selected glow", () => {
    expect(SOURCE).not.toMatch(/#[0-9a-fA-F]{3,8}\b/);
    expect(SOURCE).not.toMatch(/shadow-\[0_0_0_2px_/);
  });

  it("selected chip keeps light blue tone and adds dark: LRM-1175-family halves", () => {
    render(
      <ResearchTemplateChipRow
        selectedId={RESEARCH_TEMPLATES[0]!.id}
        onToggle={() => {}}
      />,
    );
    const chip = screen.getByTestId(
      `research-template-chip-${RESEARCH_TEMPLATES[0]!.id}`,
    );
    const className = chip.getAttribute("class") ?? "";

    expect(className).toMatch(/\bborder-blue-400\b/);
    expect(className).toMatch(/\bbg-blue-50\b/);
    expect(className).toMatch(/\btext-blue-700\b/);
    expect(className).toMatch(/\bdark:border-blue-400\/45\b/);
    expect(className).toMatch(/\bdark:bg-blue-400\/\[0\.14\]/);
    expect(className).toMatch(/\bdark:text-blue-200\b/);
    expect(className).not.toMatch(/shadow-/);
  });

  it("unselected chip keeps light hover and adds dark:hover halves", () => {
    render(
      <ResearchTemplateChipRow selectedId={null} onToggle={() => {}} />,
    );
    const chip = screen.getByTestId(
      `research-template-chip-${RESEARCH_TEMPLATES[0]!.id}`,
    );
    const className = chip.getAttribute("class") ?? "";

    expect(className).toMatch(/\bhover:border-blue-300\b/);
    expect(className).toMatch(/\bhover:bg-blue-50\/60\b/);
    expect(className).toMatch(/\bhover:text-blue-700\b/);
    expect(className).toMatch(/\bdark:hover:border-blue-400\/45\b/);
    expect(className).toMatch(/\bdark:hover:bg-blue-400\/\[0\.10\]/);
    expect(className).toMatch(/\bdark:hover:text-blue-200\b/);
  });
});
