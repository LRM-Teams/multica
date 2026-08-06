// @vitest-environment node
import { describe, expect, it } from "vitest";
import { buildFileTree } from "./file-tree-utils";

describe("buildFileTree", () => {
  it("builds nested directories and sorts directories before files", () => {
    const tree = buildFileTree([
      { path: "memory/MEMORY.md", is_dir: false, size: 10 },
      { path: "notes", is_dir: true },
      { path: "README.md", is_dir: false },
      { path: "notes/channels.md", is_dir: false },
    ]);

    expect(tree.map((node) => node.path)).toEqual(["memory", "notes", "README.md"]);
    expect(tree[0]?.children.map((node) => node.path)).toEqual(["memory/MEMORY.md"]);
    expect(tree[1]?.children.map((node) => node.path)).toEqual(["notes/channels.md"]);
  });
});
