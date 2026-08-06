"use client";

import type { RuntimeTokenStats } from "@multica/core/types";
import { Tooltip, TooltipContent, TooltipTrigger } from "@multica/ui/components/ui/tooltip";
import { cn } from "@multica/ui/lib/utils";
import { formatCompactRuntimeTokens, runtimeTokenStatsLabel } from "./runtime-token-stats";

export function RuntimeTokenStatsBadge({
  stats,
  className,
  compact = false,
  emptyLabel,
}: {
  stats?: RuntimeTokenStats | null;
  className?: string;
  compact?: boolean;
  emptyLabel?: string;
}) {
  const label = runtimeTokenStatsLabel(stats) ?? emptyLabel ?? null;
  if (!label) return null;
  const warning = (stats?.context_percent ?? 0) >= 60;
  const cacheRead = formatCompactRuntimeTokens(stats?.cache_read_tokens);
  const title = [
    stats?.model ? `Model: ${stats.model}` : null,
    stats?.context_tokens && stats?.context_window
      ? `Context: ${stats.context_tokens.toLocaleString()} / ${stats.context_window.toLocaleString()} tokens`
      : null,
    cacheRead ? `Cache read: ${stats!.cache_read_tokens!.toLocaleString()} tokens` : null,
    stats?.total_tokens ? `Session billed tokens: ${stats.total_tokens.toLocaleString()}` : null,
  ].filter(Boolean).join("\n");
  return (
    <Tooltip>
      <TooltipTrigger
        render={
          <span
            className={cn(
              "inline-flex max-w-full items-center rounded-full border px-1.5 py-0.5 font-mono text-[10px] leading-none",
              warning
                ? "border-amber-300 bg-amber-50 text-amber-800"
                : "border-border bg-muted/60 text-muted-foreground",
              className,
            )}
          >
            {/* Compact still needs room for "in … out … cache …" on phone. */}
            <span className={cn("truncate", compact ? "max-w-[10.5rem]" : "max-w-[11rem] sm:max-w-[14rem]")}>{label}</span>
          </span>
        }
      />
      {title ? <TooltipContent className="whitespace-pre-line text-xs">{title}</TooltipContent> : null}
    </Tooltip>
  );
}
