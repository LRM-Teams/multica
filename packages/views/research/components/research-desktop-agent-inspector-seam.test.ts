import fs from "node:fs";
import path from "node:path";
import { describe, expect, it } from "vitest";

describe("D5 S-node Agent inspector seam", () => {
  it("routes an S node with a canonical actor to the inspector on every viewport", () => {
    const source = fs.readFileSync(
      path.join(__dirname, "research-constellation-workspace.tsx"),
      "utf8",
    );
    const sNodeBranch = source.slice(
      source.indexOf('if (level === "s" && typedNode?.actor_agent_id)'),
      source.indexOf('if (level === "l" || level === "xl" || level === "xxl")'),
    );

    expect(sNodeBranch).toContain("openAgentInspector(typedNode.actor_agent_id)");
    expect(sNodeBranch).toContain("setRailOpen(false)");
    expect(sNodeBranch).not.toContain("if (isMobile)");
    expect(sNodeBranch).not.toContain("closeOverlay()");
  });
});
