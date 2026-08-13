// @vitest-environment node

import { readFileSync } from "node:fs";
import { describe, expect, it } from "vitest";

const source = readFileSync(
  new URL("./research-session-page.tsx", import.meta.url),
  "utf8",
);

describe("research session load error copy", () => {
  it("keeps parser/API diagnostics out of the primary localized message", () => {
    const block = source.slice(
      source.indexOf('data-testid="research-session-load-error"'),
      source.indexOf("// LRM-799"),
    );
    expect(block).toContain("$.session_page.load_failed");
    expect(block).toContain("$.session_page.load_failed_hint");
    expect(block).toContain('data-testid="research-session-load-error-diagnostics"');
    expect(block).toContain("{error.message}");
    expect(block).not.toContain("? error.message");
  });
});
