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
    expect(handler).toContain("setReportOpen(false)");
    expect(handler).toContain("setInspectorAgentId(null)");
    expect(handler).not.toContain('setRailMode("detail")');
    expect(handler).not.toContain("setRailOpen(true)");
  });
});
