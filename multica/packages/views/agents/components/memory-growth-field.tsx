"use client";

import { useEffect, useRef, useState } from "react";
import type { AgentMemoryGrowth } from "@multica/core/types";
import { cn } from "@multica/ui/lib/utils";
import { useT } from "../../i18n/use-t";

/** Card-local tier-up pulse (design ≤400ms; no toast / broadcast). */
export const MEMORY_GROWTH_PULSE_MS = 400;

/** v3 design tokens for Bronze → Platinum (Slack field color dots). */
const TIER_DOT: Record<string, string> = {
  bronze: "#a67c52",
  silver: "#8a94a6",
  gold: "#c9a227",
  platinum: "#5b7c99",
};

const SEGMENT_TRACK = "#e8e6e1";
const FINE_BAR_TRACK = "#eeeeee";
const FINE_BAR_FILL = "#c4a35a";

/**
 * LRM-304 · Slack-style Memory growth field (v3 scheme A).
 *
 * Surfaces: profile / hover agent card / side-panel profile only.
 * Do not mount on message rows (Phase① +N stays there).
 *
 * Loading / missing / zero-writes: render nothing (no placeholder flash).
 */
export function MemoryGrowthField({
  growth,
  className,
  align = "start",
}: {
  growth: AgentMemoryGrowth | null | undefined;
  className?: string;
  /** Mobile profile mock centers the tier chip; desktop cards stay start. */
  align?: "start" | "center";
}) {
  const { t } = useT("agents");
  const [pulse, setPulse] = useState(false);
  const prevTierRef = useRef<string | null>(null);

  // Detect tier upgrades during render (same pattern as AgentXpBurst) — do not
  // sync prop → state inside an effect (react-doctor / LRM-304 CI).
  const tier = growth?.tier ?? null;
  if (tier) {
    const prev = prevTierRef.current;
    if (prev === null) {
      prevTierRef.current = tier;
    } else if (prev !== tier) {
      prevTierRef.current = tier;
      if (!pulse) setPulse(true);
    }
  }

  useEffect(() => {
    if (!pulse) return;
    const timer = window.setTimeout(() => setPulse(false), MEMORY_GROWTH_PULSE_MS);
    return () => window.clearTimeout(timer);
  }, [pulse]);

  if (!growth || growth.total_writes <= 0) return null;

  const tierKey = growth.tier.toLowerCase();
  const dotColor = TIER_DOT[tierKey] ?? TIER_DOT.silver;
  const segments =
    growth.segments.length === 4
      ? growth.segments
      : growth.segments.slice(0, 4);
  const next = growth.next ?? null;
  const fineRatio =
    next && next.required > 0
      ? Math.min(1, Math.max(0, next.current / next.required))
      : 1;

  return (
    <section
      data-testid="memory-growth-field"
      data-tier={growth.tier}
      aria-label={t(($) => $.memory_growth.label)}
      className={cn("min-w-0", className)}
    >
      <p
        className="mb-2 text-[11px] font-semibold uppercase tracking-[0.03em] text-muted-foreground"
      >
        {t(($) => $.memory_growth.label)}
      </p>

      <div
        className={cn(
          "flex min-h-9 items-center gap-2.5",
          align === "center" && "justify-center",
        )}
      >
        <span
          data-testid="memory-growth-tier"
          className={cn(
            "inline-flex items-center gap-1.5 text-[13px] font-semibold text-foreground",
            pulse &&
              "motion-safe:animate-[memory-growth-pulse_400ms_ease-out] motion-reduce:animate-none",
          )}
        >
          <i
            aria-hidden
            className="inline-block size-2.5 shrink-0 rounded-full shadow-[inset_0_0_0_1px_rgba(0,0,0,0.08)]"
            style={{ backgroundColor: dotColor }}
          />
          {growth.tier_label}
        </span>
      </div>

      <div
        data-testid="memory-growth-segments"
        className="mt-2.5 flex gap-1"
        aria-hidden
      >
        {segments.map((segment) => {
          const status = segment.status;
          const filled = status === "complete" || status === "current";
          const isCurrent = status === "current";
          const fill = filled
            ? TIER_DOT[segment.tier.toLowerCase()] ?? dotColor
            : SEGMENT_TRACK;
          return (
            <div
              key={segment.tier}
              title={segment.tier_label}
              data-status={status}
              className={cn(
                "h-1 flex-1 rounded-sm",
                isCurrent &&
                  pulse &&
                  "motion-safe:animate-[memory-growth-pulse_400ms_ease-out]",
              )}
              style={{ backgroundColor: fill }}
            />
          );
        })}
      </div>

      {next ? (
        <>
          <div className="mt-2 flex items-center justify-between gap-3 text-xs text-muted-foreground">
            <span data-testid="memory-growth-next-tier">
              {t(($) => $.memory_growth.next_tier, { tier: next.tier_label })}
            </span>
            <span
              data-testid="memory-growth-writes"
              className="shrink-0 tabular-nums"
            >
              {t(($) => $.memory_growth.writes_progress, {
                current: next.current,
                required: next.required,
              })}
            </span>
          </div>
          <div
            data-testid="memory-growth-fine-bar"
            className="mt-1 h-[3px] overflow-hidden rounded-sm"
            style={{ backgroundColor: FINE_BAR_TRACK }}
          >
            <i
              className="block h-full rounded-sm"
              style={{
                width: `${fineRatio * 100}%`,
                backgroundColor: FINE_BAR_FILL,
              }}
            />
          </div>
        </>
      ) : null}

      <style>{`
        @keyframes memory-growth-pulse {
          0% { transform: scale(1); opacity: 1; }
          40% { transform: scale(1.06); opacity: 1; }
          100% { transform: scale(1); opacity: 1; }
        }
      `}</style>
    </section>
  );
}
