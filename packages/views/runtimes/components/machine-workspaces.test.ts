import { describe, expect, it } from "vitest";
import {
  truncateOrphanDirName,
  workspaceDisplayName,
  workspaceDisplayPath,
  workspaceRowStatus,
} from "./machine-workspaces";

describe("machine-workspaces helpers (LRM-1148)", () => {
  it("maps orphan+agent_id to archived, orphan alone to orphaned, else active", () => {
    expect(workspaceRowStatus({ orphan: false, agent_id: "a1" })).toBe(
      "active",
    );
    expect(workspaceRowStatus({ orphan: true, agent_id: "a1" })).toBe(
      "archived",
    );
    expect(workspaceRowStatus({ orphan: true, agent_id: null })).toBe(
      "orphaned",
    );
    expect(workspaceRowStatus({ orphan: true })).toBe("orphaned");
  });

  it("prefers agent_name and truncates orphan dir_name", () => {
    expect(
      workspaceDisplayName({
        agent_name: "Alice",
        dir_name: "546d9101-bd59-4745-8771-48505c1556bf",
      }),
    ).toBe("Alice");
    expect(
      workspaceDisplayName({
        agent_name: null,
        dir_name: "546d9101-bd59-4745-8771-48505c1556bf",
      }),
    ).toBe("546d9101…56bf");
    expect(truncateOrphanDirName("short")).toBe("short");
  });

  it("strips leading workspace UUID from rel_path", () => {
    expect(
      workspaceDisplayPath(
        "7beafc96-3c51-4fcc-9fe7-8c36ceb482ff/agents/546d9101-bd59-4745-8771-48505c1556bf",
      ),
    ).toBe("agents/546d9101-bd59-4745-8771-48505c1556bf");
    expect(workspaceDisplayPath("agent-id/x")).toBe("agent-id/x");
  });
});
