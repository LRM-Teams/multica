"use client";

import { Laptop } from "lucide-react";
import { Button } from "@multica/ui/components/ui/button";
import { cn } from "@multica/ui/lib/utils";
import { useT } from "../i18n";

/**
 * Compact, dismissible reminder that one owned Computer has no collector.
 * Configuring is optional — the Period Brief can continue with the rest.
 */
export function NotesCollectorSetupCard({
  slotKey,
  label,
  reason = "missing-collector",
  onOpenRuntimePicker,
  onDismiss,
  className,
}: {
  slotKey: string;
  label: string;
  reason?: "missing-collector" | "missing-runtime";
  onOpenRuntimePicker?: () => void;
  onDismiss: () => void;
  className?: string;
}) {
  const { t } = useT("layout");
  const waitingForRuntime = reason === "missing-runtime";

  return (
    <div
      className={cn(
        "flex w-full items-start gap-2 rounded-lg border border-dashed px-2 py-1.5 text-left text-sm",
        className,
      )}
      data-testid={
        waitingForRuntime
          ? `period-brief-collector-waiting-runtime-${slotKey}`
          : `period-brief-collector-missing-${slotKey}`
      }
      data-computer-label={label}
    >
      <Laptop className="mt-0.5 size-3.5 shrink-0 text-muted-foreground" />
      <div className="min-w-0 flex-1">
        <p className="truncate font-medium leading-5">{label}</p>
        <p className="text-[11px] leading-4 text-muted-foreground">
          {waitingForRuntime
            ? t(($) => $.notes_page.period_brief_collector_missing_runtime_hint)
            : t(($) => $.notes_page.period_brief_collector_missing_hint)}
        </p>
      </div>
      <div className="flex shrink-0 items-center gap-1">
        {waitingForRuntime || !onOpenRuntimePicker ? null : (
          <Button
            type="button"
            size="sm"
            variant="outline"
            className="h-7 px-2 text-xs"
            data-testid="period-brief-collector-missing-configure"
            onClick={onOpenRuntimePicker}
          >
            {t(($) => $.notes_page.period_brief_collector_setup_choose_runtime)}
          </Button>
        )}
        <Button
          type="button"
          size="sm"
          variant="ghost"
          className="h-7 px-2 text-xs"
          data-testid="period-brief-collector-missing-dismiss"
          onClick={onDismiss}
        >
          {t(($) => $.notes_page.period_brief_collector_missing_dismiss)}
        </Button>
      </div>
    </div>
  );
}
