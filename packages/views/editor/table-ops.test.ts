import { afterEach, describe, expect, it } from "vitest";
import { Editor } from "@tiptap/core";
import StarterKit from "@tiptap/starter-kit";
import { Table } from "@tiptap/extension-table";
import TableRow from "@tiptap/extension-table-row";
import TableCell from "@tiptap/extension-table-cell";
import TableHeader from "@tiptap/extension-table-header";
import {
  deleteColumnAt,
  deleteRowAt,
  findTablePos,
  insertColumnAt,
  insertRowAt,
  tableSize,
} from "./table-ops";

function makeEditor() {
  const element = document.createElement("div");
  document.body.appendChild(element);
  const editor = new Editor({
    element,
    extensions: [
      StarterKit,
      Table.configure({
        resizable: true,
        renderWrapper: true,
        allowTableNodeSelection: true,
        cellMinWidth: 43,
      }),
      TableRow,
      TableHeader,
      TableCell,
    ],
  });
  editor.commands.insertTable({ rows: 3, cols: 3, withHeaderRow: false });
  return editor;
}

describe("table-ops", () => {
  let editor: Editor;

  afterEach(() => {
    editor?.destroy();
    document.body.replaceChildren();
  });

  it("inserts a row above / below the target index", () => {
    editor = makeEditor();
    const tablePos = findTablePos(editor.state.doc);
    expect(tablePos).not.toBeNull();
    expect(tableSize(editor, tablePos!)).toEqual({ rows: 3, cols: 3 });

    expect(insertRowAt(editor, tablePos!, 1)).toBe(true);
    expect(tableSize(editor, tablePos!)).toEqual({ rows: 4, cols: 3 });

    expect(insertRowAt(editor, tablePos!, 0)).toBe(true);
    expect(tableSize(editor, tablePos!)).toEqual({ rows: 5, cols: 3 });

    expect(insertRowAt(editor, tablePos!, 5)).toBe(true);
    expect(tableSize(editor, tablePos!)).toEqual({ rows: 6, cols: 3 });
  });

  it("inserts a column left / right of the target index", () => {
    editor = makeEditor();
    const tablePos = findTablePos(editor.state.doc)!;

    expect(insertColumnAt(editor, tablePos, 1)).toBe(true);
    expect(tableSize(editor, tablePos)).toEqual({ rows: 3, cols: 4 });

    expect(insertColumnAt(editor, tablePos, 0)).toBe(true);
    expect(tableSize(editor, tablePos)).toEqual({ rows: 3, cols: 5 });

    expect(insertColumnAt(editor, tablePos, 5)).toBe(true);
    expect(tableSize(editor, tablePos)).toEqual({ rows: 3, cols: 6 });
  });

  it("deletes the targeted row and column", () => {
    editor = makeEditor();
    const tablePos = findTablePos(editor.state.doc)!;

    expect(deleteRowAt(editor, tablePos, 1)).toBe(true);
    expect(tableSize(editor, tablePos)).toEqual({ rows: 2, cols: 3 });

    expect(deleteColumnAt(editor, tablePos, 1)).toBe(true);
    expect(tableSize(editor, tablePos)).toEqual({ rows: 2, cols: 2 });
  });
});
