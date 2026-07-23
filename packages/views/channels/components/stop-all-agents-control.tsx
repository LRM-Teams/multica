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
 * LRM-447 — equal-weight ghost in the Members · Search · Stop rail (`size-7`).
 * Hidden by the caller when the member cannot send; disabled + empty tooltip
 * when idle. Stop path is confirm → LRM-425 inbox bulk cancel (not here).
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
  const label = t(($) => $.stop_all_agents.aria);
  const emptyLabel = t(($) => $.stop_all_agents.empty_tooltip);
  const disabled = !hasRunning || !!stopping;

  return (
    <Tooltip>
      {/* Wrap: disabled buttons swallow pointer events, so the empty-state
          tooltip would never show without a non-disabled trigger host. */}
      <TooltipTrigger
        render={
          <span className={cn("inline-flex", disabled && "cursor-not-allowed")}>
            <Button
              type="button"
              variant="ghost"
              size="icon"
              className={cn(
                "size-7 rounded-md text-muted-foreground hover:bg-muted hover:text-foreground disabled:opacity-40",
                className,
              )}
              aria-label={hasRunning ? label : emptyLabel}
              disabled={disabled}
              onClick={onOpenConfirm}
              data-testid="stop-all-agents-header"
            >
              <Square className="size-3.5 fill-current" />
            </Button>
          </span>
        }
      />
      <TooltipContent side="bottom">
        {hasRunning ? t(($) => $.stop_all_agents.tooltip) : emptyLabel}
      </TooltipContent>
    </Tooltip>
  );
}

/**
 * LRM-405 — mobile / compact overflow menu row (same gates as header).
 * Touch target ≥44px; empty state shows muted hint and does not fire.
 */
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
  const label = t(($) => $.stop_all_agents.menu_label);
  const emptyLabel = t(($) => $.stop_all_agents.empty_tooltip);
  const disabled = !hasRunning || !!stopping;

  return (
    <button
      type="button"
      disabled={disabled}
      onClick={() => {
        if (disabled) return;
        onOpenConfirm();
      }}
      className={cn(
        "flex min-h-11 items-center gap-3 px-4 py-2.5 text-left text-sm",
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
