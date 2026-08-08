import type { Node as ProseMirrorNode } from "@tiptap/pm/model";
import {
  columnResizingPluginKey,
  updateColumnsOnResize,
} from "@tiptap/pm/tables";
import type { EditorView, NodeView, ViewMutationRecord } from "@tiptap/pm/view";

/**
 * TipTap's default TableView re-applies colwidths from the document on every
 * node-view update. Column resize only commits attrs on mouseup; during the
 * drag, widths live in the DOM via displayColumnWidth. Any mid-drag view
 * update therefore snaps the edge back to the pre-drag attrs until the next
 * mousemove — and on pause there is no mousemove to repair it.
 *
 * Skip syncing <col> styles while the resize plugin reports an active drag.
 * Uses updateColumnsOnResize (same helper as the live preview) so DOM and
 * committed widths share one code path.
 */
export class StableTableView implements NodeView {
  node: ProseMirrorNode;
  cellMinWidth: number;
  view: EditorView | null;
  dom: HTMLDivElement;
  table: HTMLTableElement;
  colgroup: HTMLTableColElement;
  contentDOM: HTMLTableSectionElement;

  constructor(node: ProseMirrorNode, cellMinWidth: number, view?: EditorView) {
    this.node = node;
    this.cellMinWidth = cellMinWidth;
    this.view = view ?? null;

    this.dom = document.createElement("div");
    this.dom.className = "tableWrapper";
    this.table = this.dom.appendChild(document.createElement("table"));
    if (typeof node.attrs.style === "string" && node.attrs.style) {
      this.table.style.cssText = node.attrs.style;
    }
    this.colgroup = this.table.appendChild(document.createElement("colgroup"));
    updateColumnsOnResize(node, this.colgroup, this.table, cellMinWidth);
    this.contentDOM = this.table.appendChild(document.createElement("tbody"));
  }

  update(node: ProseMirrorNode): boolean {
    if (node.type !== this.node.type) return false;

    const resizeState = this.view
      ? (columnResizingPluginKey.getState(this.view.state) as
          | { dragging?: unknown }
          | undefined
          | null)
      : null;
    const dragging = Boolean(resizeState?.dragging);

    this.node = node;
    if (!dragging) {
      updateColumnsOnResize(
        node,
        this.colgroup,
        this.table,
        this.cellMinWidth,
      );
    }
    return true;
  }

  ignoreMutation(mutation: ViewMutationRecord): boolean {
    const target = mutation.target as Node;
    const isInsideWrapper = this.dom.contains(target);
    const isInsideContent = this.contentDOM.contains(target);
    if (isInsideWrapper && !isInsideContent) {
      return (
        mutation.type === "attributes" ||
        mutation.type === "childList" ||
        mutation.type === "characterData"
      );
    }
    return false;
  }
}
