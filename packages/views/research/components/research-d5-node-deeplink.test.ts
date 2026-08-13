// @vitest-environment node

import { readFileSync } from "node:fs";
import { describe, expect, it } from "vitest";

const source = readFileSync(
  new URL("./research-session-page.tsx", import.meta.url),
  "utf8",
);

describe("D5 node deep-link restoration", () => {
  it("opens the detail rail only after the linked node resolves", () => {
    const effect = source.slice(
      source.indexOf('const linkedNodeId = nav.searchParams.get("node")'),
      source.indexOf("// LRM-776"),
    );
    expect(effect).toContain("if (!resolved) return");
    expect(effect).toContain("if (appliedNodeLinkRef.current === linkKey) return");
    expect(effect).toContain("appliedNodeLinkRef.current = linkKey");
    expect(effect).toContain("selectSessionCanvasNode(sessionId, linkedNodeId)");
    expect(effect).toContain('setD5RailMode("detail")');
    expect(effect).toContain("setD5RailOpen(true)");
  });
});
