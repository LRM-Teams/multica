"use client";

import { useEffect, useRef, useState } from "react";
import type { AgentMemoryGrowth, MemoryGrowthTierId } from "@multica/core/types";
import { cn } from "@multica/ui/lib/utils";

/** Design tokens from LRM-274 v3 scheme A (Slack profile field). */
const TIER_DOT: Record<MemoryGrowthTierId, string> = {
  bronze: "#a67c52",
  silver: "#8a94a6",
  gold: "#c9a227",
  platinum: "#5b7c99",
};

const SEGMENT_FILL: Record<MemoryGrowthTierId, string> = {
  bronze: "#a67c52",
  silver: "#8a94a6",
  gold: "#c9a227",
  platinum: "#5b7c99",
};

const FINE_BAR = "#c4a35a";
const SEGMENT_EMPTY = "#e8e6e1";
const PULSE_MS = 400;

interface MemoryGrowthFieldProps {
  growth: AgentMemoryGrowth;
  /** Stretch segments on mobile profile (centered header layouts). */
  align?: "start" | "center";
  className?: string;
  title?: string;
  nextLabel?: (tierLabel: string) => string;
  writesLabel?: (current: number, required: number) => string;
}

/**
 * LRM-304 scheme A: Memory growth row for profile / member card / agent side panel.
 * Color dot + tier name + four-segment bar + fine "Next · n/m writes".
 * Callers must not render when growth is missing (zero XP / loading / error).
 */
export function MemoryGrowthField({
  growth,
  align = "start",
  className,
  title = "Memory growth",
  nextLabel = (tierLabel) => `Next · ${tierLabel}`,
  writesLabel = (current, required) => `${current} / ${required} writes`,
}: MemoryGrowthFieldProps) {
  const [pulse, setPulse] = useState(false);
  const prevTier = useRef<string | null>(null);

  useEffect(() => {
    if (prevTier.current === null) {
      prevTier.current = growth.tier;
      return;
    }
    if (prevTier.current === growth.tier) return;
    prevTier.current = growth.tier;
    setPulse(true);
    const timer = window.setTimeout(() => setPulse(false), PULSE_MS);
    return () => window.clearTimeout(timer);
  }, [growth.tier]);

  const progressPct =
    growth.next && growth.next.required > 0
      ? Math.min(100, Math.round((growth.next.current / growth.next.required) * 100))
      : 0;

  return (
    <section
      className={cn("min-w-0", className)}
      aria-label={title}
      data-testid="memory-growth-field"
    >
      <div
        className={cn(
          "mb-2 text-[11px] font-semibold uppercase tracking-[0.03em] text-muted-foreground",
          align === "center" && "text-center",
        )}
      >
        {title}
      </div>

      <div
        className={cn(
          "flex min-h-9 items-center gap-2.5",
          align === "center" && "justify-center",
        )}
      >
        <span
          className={cn(
            "inline-flex items-center gap-1.5 text-[13px] font-semibold text-foreground transition-[opacity,transform] duration-[400ms] ease-out",
            pulse && "scale-[1.04] opacity-70",
          )}
        >
          <i
            aria-hidden
            className="inline-block size-2.5 shrink-0 rounded-full shadow-[inset_0_0_0_1px_rgba(0,0,0,0.08)]"
            style={{ backgroundColor: TIER_DOT[growth.tier] }}
          />
          {growth.tier_label}
        </span>
      </div>

      <div
        className="mt-2.5 flex gap-1"
        role="img"
        aria-label={growth.segments.map((s) => `${s.tier_label}: ${s.status}`).join(", ")}
      >
        {growth.segments.map((segment) => {
          const filled =
            segment.status === "complete" || segment.status === "current";
          // v3 mock: completed segments use current-tier color; current uses gold accent.
          const fill =
            segment.status === "current"
              ? SEGMENT_FILL.gold
              : segment.status === "complete"
                ? SEGMENT_FILL[growth.tier]
                : SEGMENT_EMPTY;
          return (
            <div
              key={segment.tier}
              title={segment.tier_label}
              className="h-1 min-w-0 flex-1 rounded-sm"
              style={{ backgroundColor: filled ? fill : SEGMENT_EMPTY }}
              data-status={segment.status}
            />
          );
        })}
      </div>

      {growth.next ? (
        <>
          <div className="mt-2 flex items-center justify-between gap-2 text-xs text-muted-foreground">
            <span className="truncate">{nextLabel(growth.next.tier_label)}</span>
            <span className="shrink-0 tabular-nums">
              {writesLabel(growth.next.current, growth.next.required)}
            </span>
          </div>
          <div className="mt-1 h-[3px] overflow-hidden rounded-sm bg-[#eeeeee]">
            <div
              className="h-full rounded-sm transition-[width] duration-300 ease-out"
              style={{ width: `${progressPct}%`, backgroundColor: FINE_BAR }}
            />
          </div>
        </>
      ) : null}
    </section>
  );
}
