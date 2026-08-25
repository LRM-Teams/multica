import fs from "node:fs";
import path from "node:path";
import { describe, expect, it } from "vitest";

describe("D5 Work-node Agent presentation seam", () => {
  it("opens Work nodes in the shared detail rail instead of a floating inspector", () => {
    const source = fs.readFileSync(
      path.join(__dirname, "research-constellation-workspace.tsx"),
      "utf8",
    );
    const selectionHandler = source.slice(
      source.indexOf("const handleCanvasSelect = useCallback("),
      source.indexOf("const handleTrajectorySelect = useCallback("),
    );

    expect(selectionHandler).toContain('setRailMode("detail")');
    expect(selectionHandler).toContain("setRailOpen(true)");
    expect(selectionHandler).not.toContain("openAgentInspector");
  });
});
