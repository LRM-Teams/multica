"use client";

import { ClipboardList, ListTree } from "lucide-react";
import { Button } from "@multica/ui/components/ui/button";
import { cn } from "@multica/ui/lib/utils";
import { useT } from "../i18n";

export type NoteAssistantQuickAction = "period_brief" | "highlights";

export function NoteAssistantQuickActions({
  disabled = false,
  layout = "docked",
  onAction,
}: {
  disabled?: boolean;
  /** `centered` fills an empty thread; `docked` sits above the composer. */
  layout?: "centered" | "docked";
  onAction: (action: NoteAssistantQuickAction) => void;
}) {
  const { t } = useT("layout");

  return (
    <div
      data-testid="note-assistant-quick-actions"
      data-layout={layout}
      className={cn(
        "flex w-full flex-wrap items-center gap-2",
        layout === "centered" ? "max-w-sm justify-center" : "justify-start",
      )}
    >
      <Button
        type="button"
        variant="outline"
        size="sm"
        disabled={disabled}
        data-testid="note-assistant-action-period-brief"
        aria-label={t(($) => $.notes_page.assistant_satellite_period_hint)}
        onClick={() => onAction("period_brief")}
        className="gap-1.5"
      >
        <ClipboardList className="size-3.5 shrink-0" />
        {t(($) => $.notes_page.assistant_satellite_period)}
      </Button>
      <Button
        type="button"
        variant="outline"
        size="sm"
        disabled={disabled}
        data-testid="note-assistant-action-highlights"
        aria-label={t(($) => $.notes_page.assistant_satellite_highlights_hint)}
        onClick={() => onAction("highlights")}
        className="gap-1.5"
      >
        <ListTree className="size-3.5 shrink-0" />
        {t(($) => $.notes_page.assistant_satellite_highlights)}
      </Button>
    </div>
  );
}
