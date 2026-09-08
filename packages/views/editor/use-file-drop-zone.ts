import { useState, useEffect, useCallback, useRef, type DragEvent } from "react";
import {
  NOTE_PAGE_DRAG_MIME,
  isNotePageId,
  parseNotePageDragPayload,
  type NotePageRef,
} from "@multica/core/notes/page-ref";

interface UseFileDropZoneOptions {
  onDrop: (files: File[]) => void;
  /** Notes tree drag — whole page reference into the composer. */
  onNotePageDrop?: (ref: NotePageRef) => void;
  enabled?: boolean;
}

function dragHasFiles(types: readonly string[]): boolean {
  return types.includes("Files");
}

/** True when the drag likely carries a notes tree page (not a file upload). */
function dragMayBeNotePage(types: readonly string[]): boolean {
  if (types.includes(NOTE_PAGE_DRAG_MIME)) return true;
  // Safari often strips custom MIME from `types` and keeps text/*.
  if (types.includes("text/uri-list")) return true;
  if (types.includes("text/plain") && !types.includes("Files")) return true;
  return false;
}

function notePageRefFromDataTransfer(data: DataTransfer): NotePageRef | null {
  const typed = parseNotePageDragPayload(data.getData(NOTE_PAGE_DRAG_MIME));
  if (typed) return typed;
  const uri = data.getData("text/uri-list").trim().split("\n")[0]?.trim() ?? "";
  if (uri.toLowerCase().startsWith("multica-note-page:")) {
    const pageId = uri.slice("multica-note-page:".length).trim();
    if (isNotePageId(pageId)) {
      return { pageId, title: "Untitled" };
    }
  }
  // Safari / some embeds drop only text/plain (the page id used for tree reorder).
  const plain = data.getData("text/plain").trim();
  if (isNotePageId(plain)) {
    return { pageId: plain, title: "Untitled" };
  }
  return null;
}

function useFileDropZone({
  onDrop,
  onNotePageDrop,
  enabled = true,
}: UseFileDropZoneOptions) {
  const [isDragOver, setIsDragOver] = useState(false);
  const onDropRef = useRef(onDrop);
  onDropRef.current = onDrop;
  const onNotePageDropRef = useRef(onNotePageDrop);
  onNotePageDropRef.current = onNotePageDrop;
  const enabledRef = useRef(enabled);
  enabledRef.current = enabled;
  const zoneElRef = useRef<HTMLElement | null>(null);
  const nativeDropHandlerRef = useRef<((event: globalThis.DragEvent) => void) | null>(null);

  // Clear on any document-level drop or dragend (e.g. user drops outside the zone)
  useEffect(() => {
    if (!enabled) return;
    const clear = () => setIsDragOver(false);
    document.addEventListener("drop", clear);
    document.addEventListener("dragend", clear);
    return () => {
      document.removeEventListener("drop", clear);
      document.removeEventListener("dragend", clear);
    };
  }, [enabled]);

  useEffect(() => {
    return () => {
      const el = zoneElRef.current;
      const handler = nativeDropHandlerRef.current;
      if (el && handler) {
        el.removeEventListener("drop", handler, true);
      }
      zoneElRef.current = null;
      nativeDropHandlerRef.current = null;
    };
  }, []);

  const bindNativeNoteDropCapture = useCallback((zone: EventTarget) => {
    if (!(zone instanceof HTMLElement)) return;
    if (zoneElRef.current === zone && nativeDropHandlerRef.current) return;

    const prevEl = zoneElRef.current;
    const prevHandler = nativeDropHandlerRef.current;
    if (prevEl && prevHandler) {
      prevEl.removeEventListener("drop", prevHandler, true);
    }

    const onNativeDropCapture = (event: globalThis.DragEvent) => {
      if (!enabledRef.current || !onNotePageDropRef.current || !event.dataTransfer) {
        return;
      }
      const ref = notePageRefFromDataTransfer(event.dataTransfer);
      if (!ref) return;
      // Must run in native capture before ProseMirror handleDrop inserts
      // text/plain (the page UUID) into the composer.
      event.preventDefault();
      event.stopPropagation();
      setIsDragOver(false);
      onNotePageDropRef.current(ref);
    };

    zone.addEventListener("drop", onNativeDropCapture, true);
    zoneElRef.current = zone;
    nativeDropHandlerRef.current = onNativeDropCapture;
  }, []);

  const handleDragEnter = useCallback(
    (e: DragEvent) => {
      e.preventDefault();
      if (!enabled) return;
      bindNativeNoteDropCapture(e.currentTarget);
      const types = Array.from(e.dataTransfer.types);
      if (
        dragHasFiles(types) ||
        (onNotePageDropRef.current && dragMayBeNotePage(types))
      ) {
        setIsDragOver(true);
      }
    },
    [bindNativeNoteDropCapture, enabled],
  );

  const handleDragOver = useCallback(
    (e: DragEvent) => {
      e.preventDefault();
      if (!enabled) return;
      bindNativeNoteDropCapture(e.currentTarget);
      const types = Array.from(e.dataTransfer.types);
      if (onNotePageDropRef.current && dragMayBeNotePage(types)) {
        e.dataTransfer.dropEffect = "copy";
      }
    },
    [bindNativeNoteDropCapture, enabled],
  );

  const handleDragLeave = useCallback((e: DragEvent) => {
    if (!e.currentTarget.contains(e.relatedTarget as Node)) {
      setIsDragOver(false);
    }
  }, []);

  // React capture / bubble as fallback when native bind has not run yet.
  const handleDropCapture = useCallback(
    (e: DragEvent) => {
      if (!enabled || !onNotePageDropRef.current) return;
      const ref = notePageRefFromDataTransfer(e.dataTransfer);
      if (!ref) return;
      e.preventDefault();
      e.stopPropagation();
      setIsDragOver(false);
      onNotePageDropRef.current(ref);
    },
    [enabled],
  );

  const handleDrop = useCallback(
    (e: DragEvent) => {
      const alreadyHandled = e.nativeEvent.defaultPrevented;
      e.preventDefault();
      setIsDragOver(false);
      if (alreadyHandled || !enabled) return;

      if (onNotePageDropRef.current) {
        const ref = notePageRefFromDataTransfer(e.dataTransfer);
        if (ref) {
          onNotePageDropRef.current(ref);
          return;
        }
      }

      const files = Array.from(e.dataTransfer.files ?? []);
      if (files.length > 0) {
        onDropRef.current(files);
      }
    },
    [enabled],
  );

  return {
    isDragOver,
    dropZoneProps: {
      onDragEnter: handleDragEnter,
      onDragOver: handleDragOver,
      onDragLeave: handleDragLeave,
      onDropCapture: handleDropCapture,
      onDrop: handleDrop,
    },
  };
}

export { useFileDropZone, notePageRefFromDataTransfer };
