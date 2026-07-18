"use client";

import type { RuntimeTokenStats } from "@multica/core/types";
import { Tooltip, TooltipContent, TooltipTrigger } from "@multica/ui/components/ui/tooltip";
import { cn } from "@multica/ui/lib/utils";

function formatCompactTokens(value?: number | null): string | null {
  if (value == null || !Number.isFinite(value) || value <= 0) return null;
  if (value >= 1_000_000) return `${(value / 1_000_000).toFixed(value >= 10_000_000 ? 0 : 1)}M`;
  if (value >= 1_000) return `${(value / 1_000).toFixed(value >= 100_000 ? 0 : 1)}k`;
  return String(Math.round(value));
}

function formatMoney(value?: number | null): string | null {
  if (value == null || !Number.isFinite(value) || value <= 0) return null;
  return `$${value.toFixed(value >= 10 ? 2 : 3)}`;
}

export function runtimeTokenStatsLabel(stats?: RuntimeTokenStats | null): string | null {
  if (!stats) return null;
  const chunks: string[] = [];
  const input = formatCompactTokens(stats.input_tokens);
  const output = formatCompactTokens(stats.output_tokens);
  const cacheRead = formatCompactTokens(stats.cache_read_tokens);
  if (input) chunks.push(`in ${input}`);
  if (output) chunks.push(`out ${output}`);
  if (cacheRead) chunks.push(`R${cacheRead}`);
  const cost = formatMoney(stats.cost_usd);
  if (cost) chunks.push(cost);
  if (stats.context_percent != null && Number.isFinite(stats.context_percent)) {
    const pct = stats.context_percent.toFixed(stats.context_percent >= 10 ? 1 : 2);
    const window = formatCompactTokens(stats.context_window);
    chunks.push(window ? `${pct}%/${window}` : `${pct}%`);
  }
  if (stats.auto_compaction_enabled != null) chunks.push(stats.auto_compaction_enabled ? "auto" : "manual");
  return chunks.length ? chunks.join(" ") : null;
}

export function RuntimeTokenStatsBadge({
  stats,
  className,
  compact = false,
}: {
  stats?: RuntimeTokenStats | null;
  className?: string;
  compact?: boolean;
}) {
  const label = runtimeTokenStatsLabel(stats);
  if (!label) return null;
  const warning = (stats?.context_percent ?? 0) >= 60;
  const title = [
    stats?.model ? `Model: ${stats.model}` : null,
    stats?.context_tokens && stats?.context_window
      ? `Context: ${stats.context_tokens.toLocaleString()} / ${stats.context_window.toLocaleString()} tokens`
      : null,
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
            <span className={cn("truncate", compact ? "max-w-[7.5rem]" : "max-w-[11rem] sm:max-w-[14rem]")}>{label}</span>
          </span>
        }
      />
      {title ? <TooltipContent className="whitespace-pre-line text-xs">{title}</TooltipContent> : null}
    </Tooltip>
  );
}
