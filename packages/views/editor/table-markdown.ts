import type { Editor, JSONContent } from "@tiptap/core";
import type { Node as ProseMirrorNode } from "@tiptap/pm/model";
import { Table, renderTableToMarkdown } from "@tiptap/extension-table";

/** Default column width for newly inserted cells (matches editor extensions). */
export const TABLE_CELL_DEFAULT_WIDTH = 128;

export const TABLE_COLWIDTH_COMMENT_RE =
  /<!--\s*multica:table-colwidths:([\d,]+)\s*-->/g;

/** Table extension that persists resized column widths in markdown. */
export const TableWithColwidthMarkdown = Table.extend({
  renderMarkdown(node, helpers) {
    const gfm = renderTableToMarkdown(node, helpers);
    const widths = extractColumnWidthsFromTableJson(node);
    if (!tableHasCustomColumnWidths(widths)) {
      return gfm;
    }
    const prefix = `<!-- multica:table-colwidths:${widths.join(",")} -->`;
    return gfm.startsWith("\n") ? `${prefix}${gfm}` : `${prefix}\n${gfm}`;
  },
});

export function extractColumnWidthsFromTableJson(node: JSONContent): number[] {
  const widths: number[] = [];
  const firstRow = node.content?.[0];
  if (!firstRow?.content) {
    return widths;
  }
  for (const cell of firstRow.content) {
    const colspan = (cell.attrs?.colspan as number | undefined) ?? 1;
    const colwidth = cell.attrs?.colwidth as number[] | null | undefined;
    for (let j = 0; j < colspan; j += 1) {
      widths.push(colwidth?.[j] ?? TABLE_CELL_DEFAULT_WIDTH);
    }
  }
  return widths;
}

export function tableHasCustomColumnWidths(widths: number[]): boolean {
  if (widths.length === 0) {
    return false;
  }
  const allDefault = widths.every((width) => width === TABLE_CELL_DEFAULT_WIDTH);
  const allUniform = widths.every((width) => width === widths[0]);
  return !allDefault || !allUniform;
}

export function parseTableColwidthAnnotations(markdown: string): number[][] {
  const annotations: number[][] = [];
  for (const match of markdown.matchAll(TABLE_COLWIDTH_COMMENT_RE)) {
    const raw = match[1];
    if (!raw) continue;
    annotations.push(
      raw.split(",").map((value) => {
        const parsed = Number.parseInt(value, 10);
        return Number.isFinite(parsed) ? parsed : TABLE_CELL_DEFAULT_WIDTH;
      }),
    );
  }
  return annotations;
}

export function applyTableColwidthsFromMarkdown(editor: Editor, sourceMarkdown: string): void {
  const annotations = parseTableColwidthAnnotations(sourceMarkdown);
  if (annotations.length === 0) {
    return;
  }

  let tableIndex = 0;
  const { tr } = editor.state;
  let changed = false;

  editor.state.doc.descendants((node, pos) => {
    if (node.type.name !== "table") {
      return;
    }
    const widths = annotations[tableIndex];
    tableIndex += 1;
    if (!widths || widths.length === 0) {
      return;
    }
    applyColwidthsToTable(tr, pos, node, widths);
    changed = true;
  });

  if (changed) {
    editor.view.dispatch(tr);
  }
}

function applyColwidthsToTable(
  tr: Editor["state"]["tr"],
  tablePos: number,
  tableNode: ProseMirrorNode,
  columnWidths: number[],
): void {
  let rowPos = tablePos + 1;
  for (let rowIndex = 0; rowIndex < tableNode.childCount; rowIndex += 1) {
    const row = tableNode.child(rowIndex);
    let cellPos = rowPos + 1;
    let col = 0;
    for (let cellIndex = 0; cellIndex < row.childCount; cellIndex += 1) {
      const cell = row.child(cellIndex);
      const colspan = (cell.attrs.colspan as number | undefined) ?? 1;
      const colwidth: number[] = [];
      for (let j = 0; j < colspan; j += 1) {
        colwidth.push(columnWidths[col + j] ?? TABLE_CELL_DEFAULT_WIDTH);
      }
      tr.setNodeMarkup(cellPos, undefined, { ...cell.attrs, colwidth });
      col += colspan;
      cellPos += cell.nodeSize;
    }
    rowPos += row.nodeSize;
  }
}
