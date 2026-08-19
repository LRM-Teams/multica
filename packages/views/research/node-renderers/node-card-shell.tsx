"use client";

/**
 * Research V6 — NodeCardShell (UI-01 / LRM-1475).
 *
 * Universal card shell shared by all six families + generic. Structure:
 *
 *   ┌──────────────────────────────────────────┐
 *   │ accentBar (family/state colour)         │
 *   │ [icon] 类型徽标      [importance★]       │
 *   │ ┌────────────────────────────────────┐  │
 *   │ │ TITLE (1-2 lines, truncate)        │  │
 *   │ └────────────────────────────────────┘  │
 *   │ 👤 负责人                                │
 *   │ ◎ 目标                                  │
 *   │ ↻ 当前动作                              │
 *   │ ✓ 已解决 · 新进展 · 风险                │
 *   │ summary (muted, zoom-dependable)        │
 *   │ actor · attempt · evidence · >           │  ← footer (zoom-dependable)
 *   └──────────────────────────────────────────┘
 *
 * Zoom density (LRM-1469 §2): at 40% only the accent bar + title + kind badge
 * stay (independent degraded layout — never fit-whole-then-shrink); at 160%
 * the summary, footer and importance stars expand.
 *
 * Dot rule (parent goal): round dots only mark ports / status glyphs; the task
 * body is a clickable card, never a bare dot.
 *
 * The card is an interactive `<button>` when `onOpen` is provided (native
 * semantics for keyboard/screen readers) and a plain `<article>` otherwise —
 * this satisfies react-doctor's non-interactive-element-interactions rule.
 */

import type { ReactNode } from "react";
import { ChevronRight, Star } from "lucide-react";
import { cn } from "@multica/ui/lib/utils";
import { Tooltip, TooltipTrigger, TooltipContent } from "@multica/ui/components/ui/tooltip";
import { useT } from "../../i18n/use-t";
import type { NodeCardState } from "./node-state-matrix";
import { stateVisualFor } from "./node-state-matrix";
import type { NodeKindFamily } from "./node-kind-registry";
import { familyVisualFor } from "./node-family-visuals";

/** Zoom density tier (LRM-1469 §2 / AC3: 40% / 100% / 160%). */
export type NodeCardZoom = 0.4 | 1 | 1.6;

export interface NodeCardShellProps {
  /** Resolved visual family. */
  family: NodeKindFamily;
  /** Resolved visual state. */
  state: NodeCardState;
  /** Card title. */
  title: string;
  /** Family/kind type badge label (e.g. "任务" / "未知"). */
  typeLabel: string;
  /** Bounded summary (hidden at 40%). */
  summary?: string;
  /** Importance 1..3 → ★★★ / ★★☆ / ★☆☆ (0 = none). */
  importance?: number;
  /** 负责人 (👤) — separate row, never merged into prose. */
  owner?: string;
  /** 目标 (◎) — separate row, ≤1 line clamp. */
  objective?: string;
  /** 当前动作 (↻) — separate row. */
  currentAction?: string;
  /** 已解决 / 新进展 / 风险 counts — one row, separate chips. */
  resolvedCount?: number | null;
  progressCount?: number | null;
  riskCount?: number | null;
  /** Zoom density tier. */
  zoom?: NodeCardZoom;
  /** Click target (whole card). */
  onOpen?: () => void;
  /** Footer row chips (attempt / evidence / chevron) — shown at 100%+. */
  meta?: ReactNode;
  /** Optional footer legend (actor name) — shown at 160%. */
  legend?: ReactNode;
  /** Extra shell classes. */
  className?: string;
}

function ImportanceStars({ level }: { level: number }): ReactNode {
  const { t } = useT("research");
  if (level < 1) return null;
  return (
    <Tooltip>
      <TooltipTrigger
        render={
          <span
            className="flex items-center gap-0.5"
            data-testid="node-importance"
          />
        }
      >
        <span className="sr-only">{t(($) => $.node_card.importance_sr, { level })}</span>
        {[1, 2, 3].map((i) => (
          <Star
            key={i}
            aria-hidden="true"
            className={cn(
              "h-3 w-3",
              i <= level ? "fill-warning text-warning" : "text-muted-foreground/50",
            )}
          />
        ))}
      </TooltipTrigger>
      <TooltipContent side="top">
        {t(($) => $.node_card.importance_sr, { level })}
      </TooltipContent>
    </Tooltip>
  );
}

/** One structured card-face row: glyph + sr label + clamped value (separate lines). */
function CardFaceRow({
  icon,
  label,
  value,
  testid,
}: {
  icon: string;
  label: string;
  value: string;
  testid: string;
}) {
  const { t } = useT("research");
  return (
    <div data-testid={testid} className="flex min-w-0 items-start gap-1 text-[10px] leading-tight">
      <span aria-hidden="true" className="shrink-0">{icon}</span>
      <span className="sr-only">{t(($) => $.node_card.row_label_sr, { label })}</span>
      <span className="min-w-0 truncate text-muted-foreground">{value}</span>
    </div>
  );
}

/** 已解决 / 新进展 / 风险 counts — one row of separate chips. */
function ProgressCountsRow({
  resolved,
  progress,
  risk,
  expanded,
}: {
  resolved: number | null | undefined;
  progress: number | null | undefined;
  risk: number | null | undefined;
  expanded: boolean;
}) {
  const { t } = useT("research");
  const hasAny =
    (resolved !== null && resolved !== undefined) ||
    (progress !== null && progress !== undefined) ||
    (risk !== null && risk !== undefined);
  if (!hasAny) return null;
  const chips: Array<{ label: string; value: number }> = [];
  if (resolved !== null && resolved !== undefined) {
    chips.push({ label: t(($) => $.node_card.resolved), value: resolved });
  }
  if (progress !== null && progress !== undefined) {
    chips.push({ label: t(($) => $.node_card.progress), value: progress });
  }
  if (risk !== null && risk !== undefined) {
    chips.push({ label: t(($) => $.node_card.risk), value: risk });
  }
  if (chips.every((c) => c.value === 0)) {
    return (
      <div data-testid="node-progress-none" className="text-[10px] text-muted-foreground">
        {t(($) => $.node_card.no_progress)}
      </div>
    );
  }
  return (
    <div
      className="flex flex-wrap items-center gap-x-2 gap-y-0.5 pt-0.5"
      data-testid="node-progress-counts"
    >
      {chips.map((c) => (
        <span key={c.label} className="flex items-center gap-0.5 text-[10px] text-muted-foreground">
          <span aria-hidden="true">{"✓"}</span>
          <span className="sr-only">{t(($) => $.node_card.row_label_sr, { label: c.label })}</span>
          <span>{c.value}</span>
          <span className={cn("tabular-nums", expanded ? "inline" : "hidden")}>{c.label}</span>
        </span>
      ))}
    </div>
  );
}

/** The shell renders a native button when interactive, a plain article otherwise. */
export function NodeCardShell({
  family,
  state,
  title,
  typeLabel,
  summary,
  importance = 0,
  owner,
  objective,
  currentAction,
  resolvedCount,
  progressCount,
  riskCount,
  zoom = 1,
  onOpen,
  meta,
  legend,
  className,
}: NodeCardShellProps) {
  const { t } = useT("research");
  const familyVisual = familyVisualFor(family);
  const stateVisual = stateVisualFor(state);
  const stateLabel = t(($) => $.node_card.states[state]);
  const FamilyIcon = familyVisual.icon;
  const compact = zoom <= 0.4;
  const expanded = zoom >= 1.6;
  const interactive = Boolean(onOpen);
  const glyph = stateVisual.statusGlyph;

  const base = cn(
    "group/node relative w-52 overflow-hidden rounded-lg border bg-card text-left",
    // zoom-independent: base font adjusts with the global zoom scale
    stateVisual.borderClass,
    stateVisual.shellClass,
    interactive && "cursor-pointer transition-shadow hover:shadow-md",
    className,
  );

  const cardInner = (
    <>
      {/* accent bar */}
      <div
        data-testid="node-accent-bar"
        className={cn("h-1 w-full", stateVisual.accentBarClass ?? familyVisual.accentBarClass)}
      />

      {/* terminal corner check */}
      {stateVisual.cornerCheck && (
        <span
          data-testid="node-terminal-check"
          className="absolute right-1.5 top-2 flex h-4 w-4 items-center justify-center rounded-full bg-success text-[9px] font-bold text-white"
          aria-label={t(($) => $.node_card.states.terminal)}
        >
          {"✓"}
        </span>
      )}

      <div className="space-y-1 p-2.5">
        {/* Row A: icon + kind badge + importance */}
        <header className="flex items-center gap-1.5">
          <FamilyIcon
            data-testid="node-kind-icon"
            className={cn("h-3.5 w-3.5 shrink-0", familyVisual.iconClass)}
          />
          <span
            data-testid="node-type-badge"
            className={cn(
              "truncate text-[10px] font-medium uppercase tracking-wide",
              familyVisual.badgeTextClass,
            )}
          >
            {typeLabel}
          </span>
          {!compact && (
            <span className="ml-auto">
              <ImportanceStars level={importance} />
            </span>
          )}
        </header>

        {/* Title — always present (1-2 lines). */}
        <h3
          data-testid="node-title"
          className={cn(
            "line-clamp-2 [overflow-wrap:anywhere] text-sm font-medium leading-snug",
            stateVisual.titleClass,
          )}
        >
          {title}
        </h3>

        {/* Summary — hidden at 40%. */}
        {summary && !compact && (
          <p
            data-testid="node-summary"
            className="line-clamp-2 [overflow-wrap:anywhere] text-xs text-muted-foreground"
          >
            {summary}
          </p>
        )}

        {/* Card-face rows — owner / objective / current action (separate lines,
            never merged into prose; hidden at 40% per zoom density). */}
        {!compact && (
          <div className="space-y-0.5 pt-0.5">
            {owner && <CardFaceRow icon="👤" label={t(($) => $.node_card.owner)} value={owner} testid="node-owner" />}
            {objective && <CardFaceRow icon="◎" label={t(($) => $.node_card.objective)} value={objective} testid="node-objective" />}
            {currentAction && (
              <CardFaceRow icon="↻" label={t(($) => $.node_card.current_action)} value={currentAction} testid="node-current-action" />
            )}
            <ProgressCountsRow
              resolved={resolvedCount}
              progress={progressCount}
              risk={riskCount}
              expanded={expanded}
            />
          </div>
        )}

        {/* State badge */}
        <div className="flex items-center gap-1 pt-0.5">
          {glyph && glyph !== "none" && <StatusGlyph glyph={glyph} />}
          <span
            data-testid="node-state-badge"
            className={cn(
              "text-[10px] font-medium uppercase tracking-wide",
              stateVisual.badgeToneClass,
            )}
          >
            {stateLabel}
          </span>
        </div>

        {/* Footer row — only at 100%+. */}
        {!compact && (
          <footer className="flex items-center gap-2 border-t border-border/70 pt-1.5 text-[10px]">
            {legend && (
              <span data-testid="node-legend" className="truncate text-muted-foreground">
                {legend}
              </span>
            )}
            <span className="ml-auto flex items-center gap-1.5">
              {meta}
              {interactive && (
                <ChevronRight
                  data-testid="node-chevron"
                  className={cn(
                    "h-3 w-3 text-muted-foreground transition-transform",
                    expanded && "-rotate-90",
                  )}
                />
              )}
            </span>
          </footer>
        )}

        {/* Legend tail at 160% — extra detail. */}
        {expanded && !compact && legend && (
          <div data-testid="node-expanded-meta" className="pt-0.5 text-[10px] text-muted-foreground">
            {t(($) => $.node_card.importance_sr, { level: importance || "-" })}
          </div>
        )}
      </div>
    </>
  );

  const commonProps = {
    "data-testid": "node-card" as const,
    "data-family": family,
    "data-state": state,
    "data-zoom": zoom,
    className: base,
    "aria-busy": stateVisual["aria-busy"] ?? undefined,
    "aria-label": `${typeLabel}: ${title}`,
  };

  if (interactive) {
    return (
      <button type="button" {...commonProps} onClick={onOpen}>
        {cardInner}
      </button>
    );
  }

  return <article {...commonProps}>{cardInner}</article>;
}

function StatusGlyph({
  glyph,
}: {
  glyph: NonNullable<ReturnType<typeof statusGlyph>>;
}) {
  switch (glyph) {
    case "spinner":
      return (
        <span
          data-testid="node-glyph-loading"
          className="h-2 w-2 animate-spin rounded-full border border-current border-t-transparent"
        />
      );
    case "pulse":
      return <span data-testid="node-glyph-running" className="h-2 w-2 animate-pulse rounded-full bg-brand" />;
    case "failure":
      return (
        <span
          data-testid="node-glyph-failed"
          className="flex h-2.5 w-2.5 items-center justify-center rounded-full bg-destructive text-[7px] font-bold text-white"
        >
          {"✕"}
        </span>
      );
    case "stale-dot":
      return <span data-testid="node-glyph-stale" className="h-2 w-2 rounded-full bg-muted-foreground/60" />;
    case "check":
      return <span data-testid="node-glyph-check" className="h-2 w-2 rounded-full bg-success" />;
    default:
      return null;
  }
}

function statusGlyph(state: NodeCardState) {
  return stateVisualFor(state).statusGlyph;
}
