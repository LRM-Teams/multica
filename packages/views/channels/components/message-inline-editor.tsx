"use client";

import { useCallback } from "react";

/**
 * Inline single-message editor. Enter (without Shift) saves, Escape cancels —
 * a save calls back into the bubble's onEdit (a PATCH), never a re-send, so an
 * edit can never produce a new agent wake (H5).
 */
export function MessageInlineEditor({
  value,
  onChange,
  onSave,
  onCancel,
  editLabel,
  saveLabel,
  cancelLabel,
}: {
  value: string;
  onChange: (next: string) => void;
  onSave: () => void;
  onCancel: () => void;
  editLabel: string;
  saveLabel: string;
  cancelLabel: string;
}) {
  // Move focus into the editor the user just opened (the Edit trigger it
  // replaced has unmounted). A stable ref callback focuses once on mount —
  // no autoFocus prop, no effect.
  const focusOnMount = useCallback((node: HTMLTextAreaElement | null) => {
    node?.focus();
  }, []);
  return (
    <div data-testid="message-editor" className="mt-0.5">
      <textarea
        ref={focusOnMount}
        aria-label={editLabel}
        value={value}
        onChange={(event) => onChange(event.target.value)}
        onKeyDown={(event) => {
          if (event.key === "Enter" && !event.shiftKey) {
            event.preventDefault();
            onSave();
          } else if (event.key === "Escape") {
            event.preventDefault();
            onCancel();
          }
        }}
        rows={2}
        className="w-full resize-none rounded-md border border-input bg-card px-2 py-1.5 text-sm leading-6 text-ink outline-none focus-visible:ring-1 focus-visible:ring-ring"
      />
      <div className="mt-1.5 flex items-center gap-2">
        <button
          type="button"
          onClick={onSave}
          className="inline-flex h-7 items-center rounded-md bg-primary px-3 text-xs font-medium text-primary-foreground transition-colors hover:bg-primary/90 focus-visible:outline-none focus-visible:ring-1 focus-visible:ring-ring"
        >
          {saveLabel}
        </button>
        <button
          type="button"
          onClick={onCancel}
          className="inline-flex h-7 items-center rounded-md px-3 text-xs font-medium text-muted-foreground transition-colors hover:bg-muted hover:text-foreground focus-visible:outline-none focus-visible:ring-1 focus-visible:ring-ring"
        >
          {cancelLabel}
        </button>
      </div>
    </div>
  );
}
