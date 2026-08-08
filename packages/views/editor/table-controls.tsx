"use client";

import {
  useEffect,
  useRef,
  useState,
  type CSSProperties,
  type PointerEvent as ReactPointerEvent,
  type RefObject,
} from "react";
import type { Editor } from "@tiptap/react";
import type { Node as ProseMirrorNode } from "@tiptap/pm/model";
import { Fragment } from "@tiptap/pm/model";
import { NodeSelection } from "@tiptap/pm/state";
import { columnResizingPluginKey } from "@tiptap/pm/tables";
import {
  ArrowDown,
  ArrowLeft,
  ArrowRight,
  ArrowUp,
  Plus,
  Trash2,
} from "lucide-react";
import { cn } from "@multica/ui/lib/utils";
import {
  DropdownMenu,
  DropdownMenuContent,
  DropdownMenuItem,
  DropdownMenuSeparator,
  DropdownMenuTrigger,
} from "@multica/ui/components/ui/dropdown-menu";
import { useT } from "../i18n";
import {
  deleteColumnAt,
  deleteRowAt,
  insertColumnAt,
  insertRowAt,
  selectColumnAt,
  selectRowAt,
} from "./table-ops";

function isColumnResizeDragging(editor: Editor): boolean {
  const state = columnResizingPluginKey.getState(editor.state) as
    | { dragging?: unknown }
    | undefined
    | null;
  return Boolean(state?.dragging);
}

type ActiveTable = {
  node: ProseMirrorNode;
  pos: number;
  rows: number;
  cols: number;
  wrapperRect: DOMRect;
  rect: DOMRect;
  rowRects: DOMRect[];
  colRects: DOMRect[];
  selectionRect: DOMRect | null;
  selected: boolean;
};

type Axis = "row" | "column";

type DragState = {
  type: Axis;
  from: number;
  insertGap: number;
  startX: number;
  startY: number;
  dragging: boolean;
};

type OpenMenu = { type: Axis; index: number; tablePos: number };

const DRAG_THRESHOLD_PX = 4;
const ROW_HANDLE_OUTSET = 18;
const COL_HANDLE_OUTSET = 14;

function findActiveTable(editor: Editor): { node: ProseMirrorNode; pos: number; selected: boolean } | null {
  const { selection, doc } = editor.state;
  if (selection instanceof NodeSelection && selection.node.type.name === "table") {
    return { node: selection.node, pos: selection.from, selected: true };
  }
  const { $from } = selection;
  for (let depth = $from.depth; depth > 0; depth -= 1) {
    const node = $from.node(depth);
    if (node.type.name === "table") return { node, pos: $from.before(depth), selected: false };
  }
  const maybeTable = doc.nodeAt(selection.from);
  return maybeTable?.type.name === "table" ? { node: maybeTable, pos: selection.from, selected: false } : null;
}

function getTableDom(editor: Editor, tablePos: number): HTMLTableElement | null {
  const dom = editor.view.nodeDOM(tablePos);
  if (!(dom instanceof HTMLElement)) return null;
  if (dom instanceof HTMLTableElement) return dom;
  return dom.querySelector("table");
}

function relativeRect(rect: DOMRect, root: DOMRect): DOMRect {
  return new DOMRect(rect.left - root.left, rect.top - root.top, rect.width, rect.height);
}

function unionClientRects(elements: Element[]): DOMRect | null {
  if (elements.length === 0) return null;
  let left = Number.POSITIVE_INFINITY;
  let top = Number.POSITIVE_INFINITY;
  let right = Number.NEGATIVE_INFINITY;
  let bottom = Number.NEGATIVE_INFINITY;
  for (const el of elements) {
    const rect = el.getBoundingClientRect();
    left = Math.min(left, rect.left);
    top = Math.min(top, rect.top);
    right = Math.max(right, rect.right);
    bottom = Math.max(bottom, rect.bottom);
  }
  if (!Number.isFinite(left)) return null;
  return new DOMRect(left, top, right - left, bottom - top);
}

function measureTable(
  editor: Editor,
  root: HTMLElement,
  tableInfo: { node: ProseMirrorNode; pos: number; selected: boolean },
): ActiveTable | null {
  const tableDom = getTableDom(editor, tableInfo.pos);
  if (!tableDom) return null;
  const rootRect = root.getBoundingClientRect();
  const wrapperDom =
    tableDom.closest(".tableWrapper") instanceof HTMLElement
      ? (tableDom.closest(".tableWrapper") as HTMLElement)
      : tableDom;
  const rows = Array.from(tableDom.querySelectorAll(":scope > tbody > tr"));
  const firstRowCells = rows[0] ? Array.from(rows[0].children) : [];
  const selectedUnion = unionClientRects(Array.from(tableDom.querySelectorAll(".selectedCell")));
  const live = editor.state.doc.nodeAt(tableInfo.pos);
  const node = live?.type.name === "table" ? live : tableInfo.node;
  return {
    node,
    pos: tableInfo.pos,
    selected: tableInfo.selected,
    rows: node.childCount,
    cols: node.firstChild?.childCount ?? 0,
    wrapperRect: relativeRect(wrapperDom.getBoundingClientRect(), rootRect),
    rect: relativeRect(tableDom.getBoundingClientRect(), rootRect),
    rowRects: rows.map((row) => relativeRect(row.getBoundingClientRect(), rootRect)),
    colRects: firstRowCells.map((cell) => relativeRect(cell.getBoundingClientRect(), rootRect)),
    selectionRect: selectedUnion ? relativeRect(selectedUnion, rootRect) : null,
  };
}

function moveArrayItem<T>(items: T[], from: number, to: number) {
  if (from === to || from < 0 || to < 0 || from >= items.length || to >= items.length) return items;
  const next = [...items];
  const [item] = next.splice(from, 1);
  if (!item) return items;
  next.splice(to, 0, item);
  return next;
}

function destinationFromGap(from: number, gap: number): number {
  return gap > from ? gap - 1 : gap;
}

function replaceTable(editor: Editor, table: ActiveTable, nextTable: ProseMirrorNode) {
  const tr = editor.state.tr.replaceWith(table.pos, table.pos + table.node.nodeSize, nextTable);
  tr.setSelection(NodeSelection.create(tr.doc, table.pos));
  editor.view.dispatch(tr.scrollIntoView());
  editor.view.focus();
}

function moveTableRow(editor: Editor, table: ActiveTable, from: number, to: number) {
  const rows = Array.from({ length: table.node.childCount }, (_, index) => table.node.child(index));
  const movedRows = moveArrayItem(rows, from, to);
  if (movedRows === rows) return;
  replaceTable(editor, table, table.node.copy(Fragment.fromArray(movedRows)));
}

function moveTableColumn(editor: Editor, table: ActiveTable, from: number, to: number) {
  const rows: ProseMirrorNode[] = [];
  for (let rowIndex = 0; rowIndex < table.node.childCount; rowIndex += 1) {
    const row = table.node.child(rowIndex);
    const cells = Array.from({ length: row.childCount }, (_, index) => row.child(index));
    rows.push(row.copy(Fragment.fromArray(moveArrayItem(cells, from, to))));
  }
  replaceTable(editor, table, table.node.copy(Fragment.fromArray(rows)));
}

function AxisHandle({ axis, active }: { axis: Axis; active?: boolean }) {
  return (
    <span
      aria-hidden
      className={cn(
        "flex items-center justify-center rounded-sm bg-muted-foreground/35 transition-colors",
        axis === "row" ? "h-5 w-2 flex-col gap-[2px]" : "h-2 w-5 flex-row gap-[2px]",
        active ? "bg-brand" : "group-hover/handle:bg-muted-foreground/55",
      )}
    >
      <span className="size-[3px] rounded-full bg-white" />
      <span className="size-[3px] rounded-full bg-white" />
    </span>
  );
}

function edgeAddClass() {
  return cn(
    "flex items-center justify-center rounded-md text-muted-foreground/50",
    "hover:bg-muted hover:text-muted-foreground",
  );
}

export function TableControls({ editor, rootRef }: { editor: Editor; rootRef: RefObject<HTMLElement | null> }) {
  const { t } = useT("editor");
  const [activeTable, setActiveTable] = useState<ActiveTable | null>(null);
  const [drag, setDrag] = useState<DragState | null>(null);
  const [openMenu, setOpenMenu] = useState<OpenMenu | null>(null);
  const [columnResizing, setColumnResizing] = useState(false);
  const activeTableRef = useRef<ActiveTable | null>(null);
  const openMenuRef = useRef<OpenMenu | null>(null);
  const dragRef = useRef<DragState | null>(null);
  const skipMenuOpenRef = useRef(false);
  const menuOpenAtDownRef = useRef(false);

  openMenuRef.current = openMenu;

  useEffect(() => {
    const update = () => {
      const root = rootRef.current;
      if (!root) return;

      // Column-resize updates DOM widths on every mousemove. Remeasuring /
      // re-rendering our overlays mid-drag makes the dragged edge jump.
      const resizing = isColumnResizeDragging(editor);
      setColumnResizing(resizing);
      if (resizing) return;

      const tableInfo = findActiveTable(editor);
      if (tableInfo) {
        const next = measureTable(editor, root, tableInfo);
        if (next) {
          activeTableRef.current = next;
          setActiveTable(next);
          return;
        }
      }

      // Keep the last table pinned while a handle menu is open or a drag is in
      // progress. Otherwise blur-from-menu unmounts controls before the action runs.
      const pinnedPos = openMenuRef.current?.tablePos ?? activeTableRef.current?.pos;
      if ((openMenuRef.current || dragRef.current) && typeof pinnedPos === "number") {
        const live = editor.state.doc.nodeAt(pinnedPos);
        if (live?.type.name === "table") {
          const next = measureTable(editor, root, {
            node: live,
            pos: pinnedPos,
            selected: false,
          });
          if (next) {
            activeTableRef.current = next;
            setActiveTable(next);
            return;
          }
        }
      }

      if (!openMenuRef.current && !dragRef.current) {
        activeTableRef.current = null;
        setActiveTable(null);
      }
    };
    update();
    editor.on("selectionUpdate", update);
    editor.on("transaction", update);
    window.addEventListener("resize", update);
    window.addEventListener("scroll", update, true);
    return () => {
      editor.off("selectionUpdate", update);
      editor.off("transaction", update);
      window.removeEventListener("resize", update);
      window.removeEventListener("scroll", update, true);
    };
  }, [editor, rootRef]);

  if (!activeTable || !editor.isEditable) return null;

  const runMenuAction = (action: () => boolean) => {
    action();
    setOpenMenu(null);
  };

  const gapFromPointer = (type: Axis, clientX: number, clientY: number) => {
    const table = activeTableRef.current;
    const root = rootRef.current;
    if (!table || !root) return 0;
    const rootRect = root.getBoundingClientRect();
    const point = type === "row" ? clientY - rootRect.top : clientX - rootRect.left;
    const rects = type === "row" ? table.rowRects : table.colRects;
    for (let index = 0; index < rects.length; index += 1) {
      const rect = rects[index]!;
      const start = type === "row" ? rect.top : rect.left;
      const size = type === "row" ? rect.height : rect.width;
      if (point < start + size / 2) return index;
    }
    return rects.length;
  };

  const onHandlePointerDown = (
    event: ReactPointerEvent<HTMLButtonElement>,
    type: Axis,
    index: number,
  ) => {
    if (event.button !== 0) return;
    event.preventDefault();
    skipMenuOpenRef.current = false;
    menuOpenAtDownRef.current = openMenu?.type === type && openMenu.index === index;
    event.currentTarget.setPointerCapture(event.pointerId);
    const next: DragState = {
      type,
      from: index,
      insertGap: index,
      startX: event.clientX,
      startY: event.clientY,
      dragging: false,
    };
    dragRef.current = next;
    setDrag(next);
    if (type === "row") selectRowAt(editor, activeTable.pos, index);
    else selectColumnAt(editor, activeTable.pos, index);
  };

  const onHandlePointerMove = (event: ReactPointerEvent<HTMLButtonElement>) => {
    const current = dragRef.current;
    if (!current) return;
    const distance = Math.hypot(event.clientX - current.startX, event.clientY - current.startY);
    const insertGap = gapFromPointer(current.type, event.clientX, event.clientY);
    const dragging = current.dragging || distance >= DRAG_THRESHOLD_PX;
    if (dragging) skipMenuOpenRef.current = true;
    const next = { ...current, dragging, insertGap };
    dragRef.current = next;
    setDrag(next);
  };

  const onHandlePointerUp = (event: ReactPointerEvent<HTMLButtonElement>, type: Axis, index: number) => {
    const current = dragRef.current;
    const table = activeTableRef.current;
    dragRef.current = null;
    setDrag(null);
    if (event.currentTarget.hasPointerCapture(event.pointerId)) {
      event.currentTarget.releasePointerCapture(event.pointerId);
    }
    if (!current || !table) return;
    if (current.dragging) {
      const to = destinationFromGap(current.from, current.insertGap);
      if (to !== current.from) {
        if (current.type === "row") moveTableRow(editor, table, current.from, to);
        else moveTableColumn(editor, table, current.from, to);
      }
      setOpenMenu(null);
      return;
    }
    if (!skipMenuOpenRef.current) {
      setOpenMenu(
        menuOpenAtDownRef.current ? null : { type, index, tablePos: table.pos },
      );
    }
  };

  const visibleLeft = activeTable.wrapperRect.left;
  const visibleRight = activeTable.wrapperRect.left + activeTable.wrapperRect.width;
  const visibleBottom = activeTable.wrapperRect.top + activeTable.wrapperRect.height;
  const tableLeft = activeTable.rect.left;
  const tableTop = activeTable.rect.top;

  const addColumnStyle: CSSProperties = {
    left: visibleRight + 2,
    top: tableTop,
    width: 18,
    height: Math.min(activeTable.rect.height, activeTable.wrapperRect.height),
  };
  const addRowStyle: CSSProperties = {
    left: tableLeft,
    top: visibleBottom + 4,
    width: Math.min(activeTable.rect.width, activeTable.wrapperRect.width),
    height: 18,
  };
  const colVisible = (rect: DOMRect) =>
    rect.left + rect.width > visibleLeft + 2 && rect.left < visibleRight - 2;

  const dropIndicatorStyle = (): CSSProperties | null => {
    if (!drag?.dragging) return null;
    if (destinationFromGap(drag.from, drag.insertGap) === drag.from) return null;

    if (drag.type === "row") {
      const gap = drag.insertGap;
      const y =
        gap <= 0
          ? activeTable.rowRects[0]?.top ?? tableTop
          : gap >= activeTable.rowRects.length
            ? (activeTable.rowRects.at(-1)?.top ?? 0) + (activeTable.rowRects.at(-1)?.height ?? 0)
            : activeTable.rowRects[gap]!.top;
      return {
        left: tableLeft,
        top: y - 1,
        width: Math.min(activeTable.rect.width, activeTable.wrapperRect.width),
        height: 2,
      };
    }

    const gap = drag.insertGap;
    const x =
      gap <= 0
        ? activeTable.colRects[0]?.left ?? tableLeft
        : gap >= activeTable.colRects.length
          ? (activeTable.colRects.at(-1)?.left ?? 0) + (activeTable.colRects.at(-1)?.width ?? 0)
          : activeTable.colRects[gap]!.left;
    const clampedLeft = Math.min(Math.max(x, visibleLeft), visibleRight);
    return {
      left: clampedLeft - 1,
      top: tableTop,
      width: 2,
      height: activeTable.rect.height,
    };
  };

  const dropStyle = dropIndicatorStyle();

  return (
    <div className="pointer-events-none absolute inset-0 z-20 overflow-visible">
      {activeTable.selectionRect && !drag?.dragging && !columnResizing && (
        <div
          aria-hidden
          className="absolute z-[1] border border-brand"
          style={{
            left: activeTable.selectionRect.left,
            top: activeTable.selectionRect.top,
            width: activeTable.selectionRect.width,
            height: activeTable.selectionRect.height,
          }}
        />
      )}

      {dropStyle && (
        <div aria-hidden className="absolute z-[2] rounded-full bg-brand" style={dropStyle} />
      )}

      <div
        className="pointer-events-auto absolute flex items-center gap-1"
        style={{ left: Math.max(0, tableLeft - 2), top: Math.max(0, tableTop - 26) }}
      >
        <button
          type="button"
          className={cn(
            "flex size-5 items-center justify-center rounded-md text-muted-foreground/40",
            "hover:bg-muted hover:text-muted-foreground",
            activeTable.selected && "bg-muted text-foreground",
          )}
          title={t(($) => $.table_controls.select_table)}
          aria-label={t(($) => $.table_controls.select_table)}
          onMouseDown={(event) => event.preventDefault()}
          onClick={() => {
            setOpenMenu(null);
            const tr = editor.state.tr.setSelection(NodeSelection.create(editor.state.doc, activeTable.pos));
            editor.view.dispatch(tr);
            editor.view.focus();
          }}
        >
          <span className="grid grid-cols-2 gap-0.5" aria-hidden>
            <span className="size-1 rounded-[1px] bg-current opacity-70" />
            <span className="size-1 rounded-[1px] bg-current opacity-70" />
            <span className="size-1 rounded-[1px] bg-current opacity-70" />
            <span className="size-1 rounded-[1px] bg-current opacity-70" />
          </span>
        </button>
        {activeTable.selected && (
          <button
            type="button"
            className="flex size-5 items-center justify-center rounded-md text-muted-foreground/50 hover:bg-muted hover:text-destructive"
            title={t(($) => $.table_controls.delete_table)}
            aria-label={t(($) => $.table_controls.delete_table)}
            onMouseDown={(event) => event.preventDefault()}
            onClick={() => editor.chain().focus().deleteTable().run()}
          >
            <Trash2 className="size-3.5" />
          </button>
        )}
      </div>

      <button
        type="button"
        className={cn("pointer-events-auto absolute", edgeAddClass())}
        style={addColumnStyle}
        title={t(($) => $.table_controls.add_column)}
        aria-label={t(($) => $.table_controls.add_column)}
        onMouseDown={(event) => event.preventDefault()}
        onClick={() => insertColumnAt(editor, activeTable.pos, activeTable.cols)}
      >
        <Plus className="size-3.5" />
      </button>

      <button
        type="button"
        className={cn("pointer-events-auto absolute", edgeAddClass())}
        style={addRowStyle}
        title={t(($) => $.table_controls.add_row)}
        aria-label={t(($) => $.table_controls.add_row)}
        onMouseDown={(event) => event.preventDefault()}
        onClick={() => insertRowAt(editor, activeTable.pos, activeTable.rows)}
      >
        <Plus className="size-3.5" />
      </button>

      {activeTable.colRects.map((rect, index) => {
        if (!colVisible(rect)) return null;
        const menuOpen = openMenu?.type === "column" && openMenu.index === index;
        const dropTarget =
          drag?.type === "column" &&
          drag.dragging &&
          destinationFromGap(drag.from, drag.insertGap) === index;
        const handleLeft = Math.max(rect.left, visibleLeft);
        const handleRight = Math.min(rect.left + rect.width, visibleRight);
        const zoneWidth = Math.max(20, handleRight - handleLeft);
        const menuTablePos = openMenu?.tablePos ?? activeTable.pos;
        return (
          <div
            key={`col-${index}`}
            className="pointer-events-none absolute"
            style={{
              left: handleLeft,
              top: tableTop - COL_HANDLE_OUTSET,
              width: zoneWidth,
              height: COL_HANDLE_OUTSET,
            }}
          >
            <DropdownMenu
              open={menuOpen}
              onOpenChange={(open) => {
                if (!open) setOpenMenu(null);
              }}
            >
              <DropdownMenuTrigger
                render={
                  <button
                    type="button"
                    className={cn(
                      "group/handle pointer-events-auto absolute left-1/2 top-1/2 flex -translate-x-1/2 -translate-y-1/2 items-center justify-center opacity-0 transition-opacity",
                      "hover:opacity-100 focus-visible:opacity-100",
                      (menuOpen || dropTarget) && "opacity-100",
                    )}
                    aria-label={t(($) => $.table_controls.column_menu)}
                    onPointerDown={(event) => onHandlePointerDown(event, "column", index)}
                    onPointerMove={onHandlePointerMove}
                    onPointerUp={(event) => onHandlePointerUp(event, "column", index)}
                    onClick={(event) => {
                      event.preventDefault();
                    }}
                  />
                }
              >
                <AxisHandle axis="column" active={menuOpen || Boolean(dropTarget)} />
              </DropdownMenuTrigger>
              <DropdownMenuContent
                align="center"
                sideOffset={6}
                finalFocus={false}
                onMouseDown={(event) => event.preventDefault()}
              >
                <DropdownMenuItem
                  onPointerDown={(event) => {
                    event.preventDefault();
                    runMenuAction(() => insertColumnAt(editor, menuTablePos, index));
                  }}
                >
                  <ArrowLeft className="size-4" />
                  {t(($) => $.table_controls.insert_column_left)}
                </DropdownMenuItem>
                <DropdownMenuItem
                  onPointerDown={(event) => {
                    event.preventDefault();
                    runMenuAction(() => insertColumnAt(editor, menuTablePos, index + 1));
                  }}
                >
                  <ArrowRight className="size-4" />
                  {t(($) => $.table_controls.insert_column_right)}
                </DropdownMenuItem>
                <DropdownMenuSeparator />
                <DropdownMenuItem
                  variant="destructive"
                  onPointerDown={(event) => {
                    event.preventDefault();
                    runMenuAction(() => deleteColumnAt(editor, menuTablePos, index));
                  }}
                >
                  <Trash2 className="size-4" />
                  {t(($) => $.table_controls.delete_column)}
                </DropdownMenuItem>
              </DropdownMenuContent>
            </DropdownMenu>
          </div>
        );
      })}

      {activeTable.rowRects.map((rect, index) => {
        const menuOpen = openMenu?.type === "row" && openMenu.index === index;
        const dropTarget =
          drag?.type === "row" &&
          drag.dragging &&
          destinationFromGap(drag.from, drag.insertGap) === index;
        const menuTablePos = openMenu?.tablePos ?? activeTable.pos;
        return (
          <div
            key={`row-${index}`}
            className="pointer-events-none absolute"
            style={{
              // Sit just outside the cell grid — no table indentation required.
              left: tableLeft - ROW_HANDLE_OUTSET,
              top: rect.top,
              width: ROW_HANDLE_OUTSET,
              height: rect.height,
            }}
          >
            <DropdownMenu
              open={menuOpen}
              onOpenChange={(open) => {
                if (!open) setOpenMenu(null);
              }}
            >
              <DropdownMenuTrigger
                render={
                  <button
                    type="button"
                    className={cn(
                      "group/handle pointer-events-auto absolute left-1/2 top-1/2 flex -translate-x-1/2 -translate-y-1/2 items-center justify-center opacity-0 transition-opacity",
                      "hover:opacity-100 focus-visible:opacity-100",
                      (menuOpen || dropTarget) && "opacity-100",
                    )}
                    aria-label={t(($) => $.table_controls.row_menu)}
                    onPointerDown={(event) => onHandlePointerDown(event, "row", index)}
                    onPointerMove={onHandlePointerMove}
                    onPointerUp={(event) => onHandlePointerUp(event, "row", index)}
                    onClick={(event) => {
                      event.preventDefault();
                    }}
                  />
                }
              >
                <AxisHandle axis="row" active={menuOpen || Boolean(dropTarget)} />
              </DropdownMenuTrigger>
              <DropdownMenuContent
                align="start"
                side="left"
                sideOffset={6}
                finalFocus={false}
                onMouseDown={(event) => event.preventDefault()}
              >
                <DropdownMenuItem
                  onPointerDown={(event) => {
                    event.preventDefault();
                    runMenuAction(() => insertRowAt(editor, menuTablePos, index));
                  }}
                >
                  <ArrowUp className="size-4" />
                  {t(($) => $.table_controls.insert_row_above)}
                </DropdownMenuItem>
                <DropdownMenuItem
                  onPointerDown={(event) => {
                    event.preventDefault();
                    runMenuAction(() => insertRowAt(editor, menuTablePos, index + 1));
                  }}
                >
                  <ArrowDown className="size-4" />
                  {t(($) => $.table_controls.insert_row_below)}
                </DropdownMenuItem>
                <DropdownMenuSeparator />
                <DropdownMenuItem
                  variant="destructive"
                  onPointerDown={(event) => {
                    event.preventDefault();
                    runMenuAction(() => deleteRowAt(editor, menuTablePos, index));
                  }}
                >
                  <Trash2 className="size-4" />
                  {t(($) => $.table_controls.delete_row)}
                </DropdownMenuItem>
              </DropdownMenuContent>
            </DropdownMenu>
          </div>
        );
      })}
    </div>
  );
}
