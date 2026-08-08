import type { Editor } from "@tiptap/core";
import type { Node as ProseMirrorNode } from "@tiptap/pm/model";
import {
  CellSelection,
  TableMap,
  type TableRect,
  addColumn,
  addRow,
  removeColumn,
  removeRow,
} from "@tiptap/pm/tables";

export type TableContext = TableRect & { tablePos: number };

export function findTablePos(doc: ProseMirrorNode, hintPos?: number | null): number | null {
  if (typeof hintPos === "number") {
    const node = doc.nodeAt(hintPos);
    if (node?.type.name === "table") return hintPos;
  }
  let found: number | null = null;
  doc.descendants((node, pos) => {
    if (found != null) return false;
    if (node.type.name === "table") {
      found = pos;
      return false;
    }
    return true;
  });
  return found;
}

export function getTableContext(editor: Editor, tablePos: number): TableContext | null {
  const table = editor.state.doc.nodeAt(tablePos);
  if (!table || table.type.name !== "table") return null;
  const map = TableMap.get(table);
  return {
    tablePos,
    table,
    tableStart: tablePos + 1,
    map,
    left: 0,
    top: 0,
    right: map.width,
    bottom: map.height,
  };
}

function cellPosAt(ctx: TableContext, row: number, col: number): number | null {
  if (row < 0 || col < 0 || row >= ctx.map.height || col >= ctx.map.width) return null;
  return ctx.tableStart + ctx.map.map[row * ctx.map.width + col]!;
}

export function selectRowAt(editor: Editor, tablePos: number, row: number) {
  const ctx = getTableContext(editor, tablePos);
  if (!ctx) return false;
  const anchor = cellPosAt(ctx, row, 0);
  const head = cellPosAt(ctx, row, ctx.map.width - 1);
  if (anchor == null || head == null) return false;
  const tr = editor.state.tr.setSelection(CellSelection.create(editor.state.doc, anchor, head));
  editor.view.dispatch(tr);
  editor.view.focus();
  return true;
}

export function selectColumnAt(editor: Editor, tablePos: number, col: number) {
  const ctx = getTableContext(editor, tablePos);
  if (!ctx) return false;
  const anchor = cellPosAt(ctx, 0, col);
  const head = cellPosAt(ctx, ctx.map.height - 1, col);
  if (anchor == null || head == null) return false;
  const tr = editor.state.tr.setSelection(CellSelection.create(editor.state.doc, anchor, head));
  editor.view.dispatch(tr);
  editor.view.focus();
  return true;
}

/** Insert a row at `rowIndex` (0 = before first, `height` = after last). */
export function insertRowAt(editor: Editor, tablePos: number, rowIndex: number): boolean {
  const ctx = getTableContext(editor, tablePos);
  if (!ctx) return false;
  const row = Math.max(0, Math.min(rowIndex, ctx.map.height));
  const tr = addRow(editor.state.tr, ctx, row);
  const next = tr.doc.nodeAt(tablePos);
  if (next?.type.name === "table") {
    const map = TableMap.get(next);
    const focusRow = Math.min(row, map.height - 1);
    const pos = tablePos + 1 + map.map[focusRow * map.width]!;
    tr.setSelection(CellSelection.create(tr.doc, pos));
  }
  editor.view.dispatch(tr.scrollIntoView());
  editor.view.focus();
  return true;
}

/** Insert a column at `colIndex` (0 = before first, `width` = after last). */
export function insertColumnAt(editor: Editor, tablePos: number, colIndex: number): boolean {
  const ctx = getTableContext(editor, tablePos);
  if (!ctx) return false;
  const col = Math.max(0, Math.min(colIndex, ctx.map.width));
  const tr = addColumn(editor.state.tr, ctx, col);
  const next = tr.doc.nodeAt(tablePos);
  if (next?.type.name === "table") {
    const map = TableMap.get(next);
    const focusCol = Math.min(col, map.width - 1);
    const pos = tablePos + 1 + map.map[focusCol]!;
    tr.setSelection(CellSelection.create(tr.doc, pos));
  }
  editor.view.dispatch(tr.scrollIntoView());
  editor.view.focus();
  return true;
}

export function deleteRowAt(editor: Editor, tablePos: number, rowIndex: number): boolean {
  const ctx = getTableContext(editor, tablePos);
  if (!ctx) return false;
  if (ctx.map.height <= 1) {
    return editor.chain().focus().deleteTable().run();
  }
  if (rowIndex < 0 || rowIndex >= ctx.map.height) return false;
  const tr = editor.state.tr;
  removeRow(tr, ctx, rowIndex);
  const next = tr.doc.nodeAt(tablePos);
  if (next?.type.name === "table") {
    const map = TableMap.get(next);
    const focusRow = Math.min(rowIndex, map.height - 1);
    const pos = tablePos + 1 + map.map[focusRow * map.width]!;
    tr.setSelection(CellSelection.create(tr.doc, pos));
  }
  editor.view.dispatch(tr.scrollIntoView());
  editor.view.focus();
  return true;
}

export function deleteColumnAt(editor: Editor, tablePos: number, colIndex: number): boolean {
  const ctx = getTableContext(editor, tablePos);
  if (!ctx) return false;
  if (ctx.map.width <= 1) {
    return editor.chain().focus().deleteTable().run();
  }
  if (colIndex < 0 || colIndex >= ctx.map.width) return false;
  const tr = editor.state.tr;
  removeColumn(tr, ctx, colIndex);
  const next = tr.doc.nodeAt(tablePos);
  if (next?.type.name === "table") {
    const map = TableMap.get(next);
    const focusCol = Math.min(colIndex, map.width - 1);
    const pos = tablePos + 1 + map.map[focusCol]!;
    tr.setSelection(CellSelection.create(tr.doc, pos));
  }
  editor.view.dispatch(tr.scrollIntoView());
  editor.view.focus();
  return true;
}

export function tableSize(editor: Editor, tablePos: number): { rows: number; cols: number } | null {
  const ctx = getTableContext(editor, tablePos);
  if (!ctx) return null;
  return { rows: ctx.map.height, cols: ctx.map.width };
}
