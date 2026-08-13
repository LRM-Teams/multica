import { readFileSync } from "node:fs";
import { describe, expect, it } from "vitest";

const chromeSource = readFileSync(
  new URL("./research-d5-chrome.tsx", import.meta.url),
  "utf8",
);
const css = readFileSync(
  new URL("./research-d5-layout.css", import.meta.url),
  "utf8",
);

describe("D5 mobile command actions", () => {
  it("binds the action group to a wrapping full-width mobile slot", () => {
    expect(chromeSource).toContain('className="d5-chrome-actions"');
    expect(css).toMatch(
      /@media \(max-width: 767px\)[\s\S]*?\.d5-chrome-controls\s*\{[^}]*min-width:\s*0[^}]*flex-wrap:\s*wrap/s,
    );
    expect(css).toMatch(
      /@media \(max-width: 767px\)[\s\S]*?\.d5-chrome-actions\s*\{[^}]*flex:\s*1 1 100%[^}]*min-width:\s*0[^}]*justify-content:\s*flex-end/s,
    );
  });
});
