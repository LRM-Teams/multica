import { readFileSync } from "node:fs";
import { describe, expect, it } from "vitest";

const honorCss = readFileSync("../ui/styles/base.css", "utf8");

function ruleStartingAt(selector: string): string {
  const start = honorCss.indexOf(selector);
  const open = honorCss.indexOf("{", start);
  const close = honorCss.indexOf("}", open);

  if (start < 0 || open < 0 || close < 0) {
    throw new Error(`Missing CSS rule: ${selector}`);
  }

  return honorCss.slice(open + 1, close);
}

function contrastAgainstWhite(hex: string): number {
  const channel = (offset: number) => {
    const value = Number.parseInt(hex.slice(offset, offset + 2), 16) / 255;
    return value <= 0.04045
      ? value / 12.92
      : ((value + 0.055) / 1.055) ** 2.4;
  };
  const red = channel(0);
  const green = channel(2);
  const blue = channel(4);
  const luminance =
    0.2126 * red + 0.7152 * green + 0.0722 * blue;
  return 1.05 / (luminance + 0.05);
}

function gradientColors(rule: string): string[] {
  const gradient = rule.slice(rule.indexOf("background-image:"));
  return [...gradient.matchAll(/#([0-9a-f]{6})/gi)].flatMap((match) =>
    match[1] ? [match[1]] : [],
  );
}

function solidColor(rule: string): string {
  const color = rule.match(/(?:^|\n)\s*color:\s*#([0-9a-f]{6})/i)?.[1];
  if (!color) throw new Error("Missing solid text color");
  return color;
}

describe("honor name light-theme contrast", () => {
  it("keeps every light-theme gradient stop readable on white", () => {
    const founding = ruleStartingAt(".honor-name--founding {");
    const prismatic = ruleStartingAt(
      ".honor-name--prismatic,\n.honor-name--animated-prismatic {\n  --honor-glow-color",
    );
    const shimmer = ruleStartingAt(".honor-name--shimmer {");

    for (const hex of [
      ...gradientColors(founding),
      ...gradientColors(prismatic),
      ...gradientColors(shimmer),
    ]) {
      expect(contrastAgainstWhite(hex), `#${hex}`).toBeGreaterThanOrEqual(4.5);
    }
  });

  it("keeps solid earned colors readable on white", () => {
    const member = ruleStartingAt(".honor-name--member {");
    const gold = ruleStartingAt(".honor-name--gold {");
    const glow = ruleStartingAt(
      ".honor-name--glow,\n.honor-name--animated-glow {",
    );

    for (const hex of [
      solidColor(member),
      solidColor(gold),
      solidColor(glow),
    ]) {
      expect(contrastAgainstWhite(hex), `#${hex}`).toBeGreaterThanOrEqual(4.5);
    }
  });

  it("uses the text color instead of a white wash for inline glow", () => {
    const tierTwo = ruleStartingAt(
      '.honor-name-glow[data-honor-glow-tier="2"] {',
    );

    expect(tierTwo).toContain("currentColor");
    expect(tierTwo).not.toContain("white");
  });
});
