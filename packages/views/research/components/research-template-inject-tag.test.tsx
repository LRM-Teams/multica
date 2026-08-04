import { describe, expect, it, vi } from "vitest";
import { render, screen } from "@testing-library/react";
import fs from "node:fs";
import path from "node:path";
import { RESEARCH_TEMPLATES } from "../lib/research-templates";
import { ResearchTemplateInjectTag } from "./research-template-inject-tag";

vi.mock("../../i18n/use-t", () => ({
  useT: () => ({
    t: (
      picker: (keys: Record<string, unknown>) => unknown,
      vars?: Record<string, unknown>,
    ) => {
      const keys = {
        home: {
          template_chip: `已注入 ${String(vars?.title ?? "")}`,
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
    "research/components/research-template-inject-tag.tsx",
  ),
  "utf8",
);

/**
 * LRM-1175 (design acceptance of the LRM-1140 A2 frozen composer spec):
 * the injected-template tag must carry a dark variant per LRM-269 — a low-alpha
 * same-hue tint with light text, never a near-white fill block on a dark canvas.
 */
describe("LRM-1175 research template inject tag · light/dark tone parity", () => {
  it.each(RESEARCH_TEMPLATES.map((template) => template.id))(
    "renders both a light tone and a dark: variant for %s",
    (id) => {
      const template = RESEARCH_TEMPLATES.find((item) => item.id === id)!;
      render(<ResearchTemplateInjectTag template={template} />);
      const tag = screen.getByTestId("research-template-inject-tag");
      const className = tag.getAttribute("class") ?? "";

      expect(tag).toHaveAttribute("data-template-id", id);
      // light half (A2 frozen) still present
      expect(className).toMatch(/\bbg-(blue|indigo|teal)-50\b/);
      // dark half present for background, text and border
      expect(className).toMatch(/\bdark:bg-(blue|indigo|teal)-400\/\[0\.14\]/);
      expect(className).toMatch(/\bdark:text-(blue|indigo|teal)-200\b/);
      expect(className).toMatch(/\bdark:border-(blue|indigo|teal)-(300|400)\/45\b/);
    },
  );

  it("never leaves a light-only fill: every bg-*-50 tone has a dark: counterpart", () => {
    const toneBlock = SOURCE.slice(
      SOURCE.indexOf("const TAG_TONES"),
      SOURCE.indexOf("};", SOURCE.indexOf("const TAG_TONES")),
    );
    const tones = toneBlock
      .split("\n")
      .filter((line) => /bg-(blue|indigo|teal)-50\b/.test(line));

    expect(tones.length).toBe(RESEARCH_TEMPLATES.length);
    for (const tone of tones) {
      expect(tone).toMatch(/dark:bg-/);
      expect(tone).toMatch(/dark:text-/);
    }
  });

  it("keeps the frozen DOM contract (testid + aria-label + status dot)", () => {
    render(<ResearchTemplateInjectTag template={RESEARCH_TEMPLATES[0]!} />);
    const tag = screen.getByTestId("research-template-inject-tag");

    expect(tag.getAttribute("aria-label")).toContain(
      RESEARCH_TEMPLATES[0]!.title.zh,
    );
    expect(tag.querySelector("[aria-hidden]")).not.toBeNull();
    expect(tag.textContent).toContain(RESEARCH_TEMPLATES[0]!.title.zh);
  });
});
