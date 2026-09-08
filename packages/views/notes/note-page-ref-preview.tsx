"use client";

import { FileText, X } from "lucide-react";
import { useT } from "../i18n/use-t";

export function NotePageRefPreview({
  refs,
  onRemove,
}: {
  refs: { pageId: string; title: string }[];
  onRemove: (pageId: string) => void;
}) {
  const { t } = useT("layout");
  if (refs.length === 0) return null;
  return (
    <div data-testid="note-page-ref-preview" className="flex flex-col">
      {refs.map((ref) => (
        <div
          key={ref.pageId}
          data-testid="note-page-ref-chip"
          className="flex min-w-0 items-center gap-2 border-b border-border/70 px-3 py-2"
        >
          <FileText className="size-3.5 shrink-0 text-muted-foreground" aria-hidden />
          <div className="min-w-0 flex-1 truncate text-xs text-muted-foreground">
            <span className="font-medium text-foreground/75">
              {t(($) => $.notes_page.assistant_page_ref_label)}
            </span>
            <span>{": "}</span>
            <span className="text-foreground/90">{ref.title || "Untitled"}</span>
          </div>
          <button
            type="button"
            onClick={() => onRemove(ref.pageId)}
            aria-label={t(($) => $.notes_page.assistant_page_ref_cancel)}
            className="inline-flex size-6 shrink-0 items-center justify-center rounded-md hover:bg-muted focus-visible:outline-none focus-visible:ring-1 focus-visible:ring-ring"
          >
            <X className="size-3.5" />
          </button>
        </div>
      ))}
    </div>
  );
}
