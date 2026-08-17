"use client";

import type { ResearchSelectedReference } from "@multica/core/types";
import { Button } from "@multica/ui/components/ui/button";
import { cn } from "@multica/ui/lib/utils";
import { X } from "lucide-react";
import { useT } from "../../i18n/use-t";

export function ResearchSelectedRefChip({
  reference,
  onRemove,
  disabled = false,
  className,
}: {
  reference: ResearchSelectedReference;
  onRemove: (stableId: string) => void;
  disabled?: boolean;
  className?: string;
}) {
  const { t } = useT("research");

  return (
    <li
      className={cn(
        "group relative flex min-w-0 max-w-full items-center gap-2 rounded-xl border border-primary/20 bg-primary/7 py-1.5 pr-9 pl-2.5 text-foreground",
        className,
      )}
      data-kind={reference.kind}
      data-testid="research-selected-ref-chip"
      title={reference.display_summary}
    >
      <span
        aria-hidden="true"
        className="shrink-0 rounded-md bg-primary/12 px-1.5 py-0.5 text-[10px] font-semibold text-primary"
      >
        {reference.kind}
      </span>
      <span className="min-w-0 truncate text-xs font-medium">
        {reference.display_summary}
      </span>
      <span className="sr-only">
        {t(($) => $.panel.selected_ref_revision, {
          revision: reference.revision,
        })}
      </span>
      <Button
        type="button"
        variant="ghost"
        size="icon"
        className="absolute top-1/2 right-0.5 size-8 -translate-y-1/2 rounded-lg text-muted-foreground hover:bg-primary/10 hover:text-foreground"
        aria-label={t(($) => $.panel.selected_ref_remove, {
          summary: reference.display_summary,
        })}
        disabled={disabled}
        onClick={() => onRemove(reference.stable_id)}
      >
        <X className="size-3.5" aria-hidden="true" />
      </Button>
    </li>
  );
}
