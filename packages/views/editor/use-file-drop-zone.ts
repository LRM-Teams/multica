import { useState, useEffect, useCallback, useRef, type DragEvent } from "react";
import {
  NOTE_PAGE_DRAG_MIME,
  parseNotePageDragPayload,
  type NotePageRef,
} from "@multica/core/notes/page-ref";

interface UseFileDropZoneOptions {
  onDrop: (files: File[]) => void;
  /** Notes tree drag — whole page reference into the composer. */
  onNotePageDrop?: (ref: NotePageRef) => void;
  enabled?: boolean;
}

function dragHasNotePage(types: readonly string[]): boolean {
  return types.includes(NOTE_PAGE_DRAG_MIME);
}

function dragHasFiles(types: readonly string[]): boolean {
  return types.includes("Files");
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

  const handleDragEnter = useCallback(
    (e: DragEvent) => {
      e.preventDefault();
      if (!enabled) return;
      const types = Array.from(e.dataTransfer.types);
      if (dragHasFiles(types) || (onNotePageDropRef.current && dragHasNotePage(types))) {
        setIsDragOver(true);
      }
    },
    [enabled],
  );

  const handleDragOver = useCallback(
    (e: DragEvent) => {
      e.preventDefault();
      if (!enabled) return;
      const types = Array.from(e.dataTransfer.types);
      if (onNotePageDropRef.current && dragHasNotePage(types)) {
        e.dataTransfer.dropEffect = "copy";
      }
    },
    [enabled],
  );

  const handleDragLeave = useCallback((e: DragEvent) => {
    if (!e.currentTarget.contains(e.relatedTarget as Node)) {
      setIsDragOver(false);
    }
  }, []);

  const handleDrop = useCallback(
    (e: DragEvent) => {
      const alreadyHandled = e.nativeEvent.defaultPrevented;
      e.preventDefault();
      setIsDragOver(false);
      if (alreadyHandled || !enabled) return;

      const noteRaw = e.dataTransfer.getData(NOTE_PAGE_DRAG_MIME);
      if (noteRaw && onNotePageDropRef.current) {
        const ref = parseNotePageDragPayload(noteRaw);
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
      onDrop: handleDrop,
    },
  };
}

export { useFileDropZone };
