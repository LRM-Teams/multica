import { afterEach, describe, expect, it } from "vitest";
import { Editor } from "@tiptap/core";
import StarterKit from "@tiptap/starter-kit";
import { Markdown } from "@tiptap/markdown";
import TableRow from "@tiptap/extension-table-row";
import TableHeader from "@tiptap/extension-table-header";
import TableCell from "@tiptap/extension-table-cell";
import {
  TABLE_CELL_DEFAULT_WIDTH,
  TableWithColwidthMarkdown,
  applyTableColwidthsFromMarkdown,
  extractColumnWidthsFromTableJson,
  parseTableColwidthAnnotations,
  tableHasCustomColumnWidths,
} from "./table-markdown";

const TableCellWithDefaultWidth = TableCell.extend({
  addAttributes() {
    const parent = this.parent?.() as Record<string, Record<string, unknown>>;
    return {
      ...parent,
      colwidth: {
        ...parent.colwidth,
        default: [TABLE_CELL_DEFAULT_WIDTH],
      },
    };
  },
});

const TableHeaderWithDefaultWidth = TableHeader.extend({
  addAttributes() {
    const parent = this.parent?.() as Record<string, Record<string, unknown>>;
    return {
      ...parent,
      colwidth: {
        ...parent.colwidth,
        default: [TABLE_CELL_DEFAULT_WIDTH],
      },
    };
  },
});

function makeEditor() {
  const element = document.createElement("div");
  document.body.appendChild(element);
  return new Editor({
    element,
    extensions: [
      StarterKit,
      TableWithColwidthMarkdown.configure({
        resizable: true,
        renderWrapper: true,
        cellMinWidth: 43,
      }),
      TableRow,
      TableHeaderWithDefaultWidth,
      TableCellWithDefaultWidth,
      Markdown,
    ],
  });
}

function firstTableColwidths(editor: Editor): number[] {
  let widths: number[] = [];
  editor.state.doc.descendants((node) => {
    if (node.type.name !== "table" || widths.length > 0) {
      return;
    }
    const firstRow = node.firstChild;
    if (!firstRow) {
      return;
    }
    for (let i = 0; i < firstRow.childCount; i += 1) {
      const cell = firstRow.child(i);
      const colwidth = cell.attrs.colwidth as number[] | null;
      widths = widths.concat(colwidth ?? []);
    }
  });
  return widths;
}

describe("table-markdown", () => {
  let editor: Editor;

  afterEach(() => {
    editor?.destroy();
    document.body.innerHTML = "";
  });

  it("detects custom column widths", () => {
    expect(tableHasCustomColumnWidths([128, 128, 128])).toBe(false);
    expect(tableHasCustomColumnWidths([200, 128, 128])).toBe(true);
    expect(tableHasCustomColumnWidths([160, 160, 160])).toBe(true);
  });

  it("serializes resized column widths into a markdown annotation", () => {
    editor = makeEditor();
    editor.commands.insertTable({ rows: 2, cols: 3, withHeaderRow: true });
    applyTableColwidthsFromMarkdown(
      editor,
      "<!-- multica:table-colwidths:220,128,128 -->\n",
    );

    expect(firstTableColwidths(editor)).toEqual([220, 128, 128]);

    const markdown = editor.getMarkdown();
    expect(markdown).toContain("<!-- multica:table-colwidths:220,128,128 -->");
    expect(parseTableColwidthAnnotations(markdown)).toEqual([[220, 128, 128]]);
  });

  it("restores column widths from markdown annotations on load", () => {
    const markdown = `<!-- multica:table-colwidths:220,128,128 -->
| A | B | C |
| - | - | - |
| 1 | 2 | 3 |
`;

    editor = makeEditor();
    editor.commands.setContent(markdown, { contentType: "markdown" });
    applyTableColwidthsFromMarkdown(editor, markdown);

    expect(firstTableColwidths(editor)).toEqual([220, 128, 128]);
  });

  it("round-trips resized tables through markdown", () => {
    editor = makeEditor();
    editor.commands.insertTable({ rows: 2, cols: 2, withHeaderRow: true });
    applyTableColwidthsFromMarkdown(editor, "<!-- multica:table-colwidths:180,128 -->\n");

    const saved = editor.getMarkdown();
    editor.commands.clearContent();
    editor.commands.setContent(saved, { contentType: "markdown" });
    applyTableColwidthsFromMarkdown(editor, saved);

    expect(firstTableColwidths(editor)).toEqual([180, 128]);
  });

  it("extracts widths from table JSON", () => {
    const widths = extractColumnWidthsFromTableJson({
      type: "table",
      content: [
        {
          type: "tableRow",
          content: [
            { type: "tableHeader", attrs: { colwidth: [200] } },
            { type: "tableHeader", attrs: { colwidth: [140] } },
          ],
        },
      ],
    });
    expect(widths).toEqual([200, 140]);
  });
});
