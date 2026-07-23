"use client";

import { Square } from "lucide-react";
import { Button } from "@multica/ui/components/ui/button";
import {
  Tooltip,
  TooltipContent,
  TooltipTrigger,
} from "@multica/ui/components/ui/tooltip";
import { cn } from "@multica/ui/lib/utils";
import { useT } from "../../i18n";

/**
 * LRM-405 — desktop header icon (tooltip/aria only; no forced long label).
 * Hidden when the member cannot send; disabled + empty tooltip when idle.
 */
export function StopAllAgentsHeaderButton({
  hasRunning,
  stopping,
  className,
  onOpenConfirm,
}: {
  hasRunning: boolean;
  stopping?: boolean;
  className?: string;
  onOpenConfirm: () => void;
}) {
  const { t } = useT("channels");
  const label = t(($) => $.agent_status.stop_all_agents);
  const emptyLabel = t(($) => $.agent_status.stop_all_empty);
  const disabled = !hasRunning || !!stopping;

  return (
    <Tooltip>
      {/* Wrap: disabled buttons swallow pointer events, so the empty-state
          tooltip would never show without a non-disabled trigger host. */}
      <TooltipTrigger
        render={
          <span
            className={cn("inline-flex", disabled && "cursor-not-allowed")}
          />
        }
      >
        <Button
          type="button"
          variant="ghost"
          size="icon"
          className={cn("shrink-0 text-muted-foreground", className)}
          aria-label={hasRunning ? label : emptyLabel}
          disabled={disabled}
          onClick={onOpenConfirm}
          data-testid="stop-all-agents-header"
        >
          <Square className="size-3.5 fill-current" />
        </Button>
      </TooltipTrigger>
      <TooltipContent side="bottom">
        {hasRunning ? label : emptyLabel}
      </TooltipContent>
    </Tooltip>
  );
}

/** LRM-405 — mobile / compact overflow menu row (same gates as header). */
export function StopAllAgentsMenuItem({
  hasRunning,
  stopping,
  onOpenConfirm,
}: {
  hasRunning: boolean;
  stopping?: boolean;
  onOpenConfirm: () => void;
}) {
  const { t } = useT("channels");
  const label = t(($) => $.agent_status.stop_all_agents);
  const emptyLabel = t(($) => $.agent_status.stop_all_empty);
  const disabled = !hasRunning || !!stopping;

  return (
    <button
      type="button"
      disabled={disabled}
      onClick={onOpenConfirm}
      className={cn(
        "flex min-h-[44px] items-center gap-3 px-4 py-2.5 text-left text-sm",
        disabled
          ? "cursor-not-allowed text-muted-foreground opacity-50"
          : "hover:bg-accent",
      )}
      aria-label={hasRunning ? label : emptyLabel}
      data-testid="stop-all-agents-menu"
    >
      <Square className="size-5 shrink-0 fill-current text-muted-foreground" />
      <span className="flex-1">{label}</span>
      {!hasRunning ? (
        <span className="text-xs text-muted-foreground">{emptyLabel}</span>
      ) : null}
    </button>
  );
}
