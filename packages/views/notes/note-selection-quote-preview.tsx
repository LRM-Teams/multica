"use client";

import { X } from "lucide-react";
import { useT } from "../i18n/use-t";

export function NoteSelectionQuotePreview({
  excerpts,
  onRemove,
}: {
  excerpts: { id: string; summary: string }[];
  onRemove: (excerptId: string) => void;
}) {
  const { t } = useT("layout");
  if (excerpts.length === 0) return null;
  const numbered = excerpts.length > 1;
  return (
    <div data-testid="note-selection-quote-preview">
      {excerpts.map((excerpt, index) => (
        <div
          key={excerpt.id}
          data-testid="note-selection-quote-excerpt"
          className="flex min-w-0 items-center gap-2 border-b border-border/70 px-3 py-2"
        >
          <div className="min-w-0 flex-1 truncate text-xs text-muted-foreground">
            <span className="select-none text-muted-foreground/80">{"> "}</span>
            <span className="font-medium text-foreground/75">
              {numbered
                ? t(($) => $.notes_page.assistant_selection_quote_label_nth, { n: index + 1 })
                : t(($) => $.notes_page.assistant_selection_quote_label)}
            </span>
            <span>{": "}</span>
            <span>{excerpt.summary}</span>
          </div>
          <button
            type="button"
            onClick={() => onRemove(excerpt.id)}
            aria-label={t(($) => $.notes_page.assistant_selection_quote_cancel)}
            className="inline-flex size-6 shrink-0 items-center justify-center rounded-md hover:bg-muted focus-visible:outline-none focus-visible:ring-1 focus-visible:ring-ring"
          >
            <X className="size-3.5" />
          </button>
        </div>
      ))}
    </div>
  );
}
