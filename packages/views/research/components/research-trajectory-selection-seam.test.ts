// @vitest-environment node

import { readFileSync } from "node:fs";
import { describe, expect, it } from "vitest";

const source = readFileSync(
  new URL("./research-constellation-workspace.tsx", import.meta.url),
  "utf8",
);

describe("D5 trajectory selection seam", () => {
  it("keeps single-click selection separate from full node opening", () => {
    expect(source).toContain("const handleTrajectorySelect = useCallback(");
    expect(source).toContain("onSelect={handleTrajectorySelect}");
    expect(source).toContain("onOpenNodeDetail={handleCanvasSelect}");

    const handler = source.slice(
      source.indexOf("const handleTrajectorySelect = useCallback("),
      source.indexOf("const graphRemainingCount"),
    );
    expect(handler).toContain("closeOverlay()");
    expect(handler).not.toContain('setRailMode("detail")');
    expect(handler).not.toContain("setRailOpen(true)");
  });

  it("reopens the detail rail only for a full ordinary-node open", () => {
    const handler = source.slice(
      source.indexOf("const handleCanvasSelect = useCallback("),
      source.indexOf("const handleTrajectorySelect = useCallback("),
    );
    expect(handler).toContain('setRailMode("detail")');
    expect(handler).toContain("setRailOpen(true)");
    expect(handler).not.toContain("openAgentInspector");
    expect(handler).toContain("openReport");
  });
});
