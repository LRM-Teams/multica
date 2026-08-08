import { describe, expect, it, vi } from "vitest";
import { Editor } from "@tiptap/core";
import StarterKit from "@tiptap/starter-kit";
import { Table } from "@tiptap/extension-table";
import TableRow from "@tiptap/extension-table-row";
import TableCell from "@tiptap/extension-table-cell";
import TableHeader from "@tiptap/extension-table-header";
import { columnResizingPluginKey } from "@tiptap/pm/tables";
import type { EditorView } from "@tiptap/pm/view";
import { StableTableView } from "./stable-table-view";

function tableNode() {
  const element = document.createElement("div");
  const editor = new Editor({
    element,
    extensions: [
      StarterKit,
      Table,
      TableRow,
      TableHeader,
      TableCell,
    ],
  });
  editor.commands.insertTable({ rows: 2, cols: 2, withHeaderRow: false });
  let node = null as ReturnType<typeof editor.state.doc.nodeAt>;
  editor.state.doc.descendants((child) => {
    if (child.type.name === "table") {
      node = child;
      return false;
    }
    return true;
  });
  editor.destroy();
  if (!node) throw new Error("expected a table node");
  return node;
}

describe("StableTableView", () => {
  it("keeps live <col> widths while a column resize drag is active", () => {
    const node = tableNode();
    const view = {
      state: {} as EditorView["state"],
    } as EditorView;
    const tableView = new StableTableView(node, 43, view);
    const col = tableView.colgroup.querySelector("col");
    expect(col).toBeTruthy();

    col!.style.width = "220px";

    vi.spyOn(columnResizingPluginKey, "getState").mockReturnValue({
      activeHandle: 1,
      dragging: { startX: 0, startWidth: 128 },
    });

    expect(tableView.update(node)).toBe(true);
    expect(col!.style.width).toBe("220px");

    vi.restoreAllMocks();
  });

  it("syncs <col> widths from attrs when not dragging", () => {
    const node = tableNode();
    const view = {
      state: {} as EditorView["state"],
    } as EditorView;
    const tableView = new StableTableView(node, 43, view);
    const col = tableView.colgroup.querySelector("col");
    expect(col).toBeTruthy();

    col!.style.width = "220px";

    vi.spyOn(columnResizingPluginKey, "getState").mockReturnValue({
      activeHandle: -1,
      dragging: false,
    });

    expect(tableView.update(node)).toBe(true);
    expect(col!.style.width).not.toBe("220px");

    vi.restoreAllMocks();
  });
});
