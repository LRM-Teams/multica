"use client";

/**
 * LRM-1472 / UI-04 — dispute subgraph list rows: position fan + evidence.
 * Each row carries a NON-COLOR encoding (glyph + stance label + line style) so
 * stance stays legible in grayscale and for screen readers. Facts come from
 * the typed-edge model (`buildDisputeModel`), never from position text.
 *
 * `onFocusNode(id)` is the §5 detail→canvas affordance: pans + selects the
 * target node and switches the detail panel. Never mutates the canonical graph.
 */

import type { ResearchGraphNode } from "@multica/core/types";
import { cn } from "@multica/ui/lib/utils";
import { ChevronRight } from "lucide-react";
import { useT } from "../../i18n/use-t";
import type { PositionView } from "./model";
import type { DisputeSubgraphModel } from "./model";
import { stanceGlyph, stanceTone, stanceLabel } from "./stance";

export type FocusNodeHandler = (nodeId: string) => void;

/** A roving row with focus affordance. */
function FocusRow({
  node,
  glyph,
  title,
  meta,
  tone,
  onFocusNode,
  testId,
}: {
  node: ResearchGraphNode;
  glyph: string;
  title: string;
  meta?: string;
  tone?: "success" | "danger" | "warning" | "default";
  onFocusNode?: FocusNodeHandler;
  testId?: string;
}) {
  const { t } = useT("research");
  const toneClass =
    tone === "success"
      ? "border-success/40 bg-success/5"
      : tone === "danger"
        ? "border-destructive/40 bg-destructive/5"
        : tone === "warning"
          ? "border-warning/40 bg-warning/5"
          : "border-muted-foreground/30 bg-muted/10";
  const glyphClass =
    tone === "success"
      ? "text-success-strong"
      : tone === "danger"
        ? "text-destructive"
        : tone === "warning"
          ? "text-warning"
          : "text-muted-foreground";
  return (
    <li
      className={cn(
        "group flex items-start justify-between gap-2 rounded-md border px-2.5 py-2 text-left transition-colors",
        toneClass,
      )}
      data-node-id={node.id}
      data-testid={testId}
      aria-label={`${glyph} ${title}`}
    >
      <div className="min-w-0">
        <span className={cn("mr-1.5 font-semibold", glyphClass)} aria-hidden>
          {glyph}
        </span>
        <span className="text-xs font-semibold text-foreground">{title}</span>
        {meta ? (
          <span className="ml-1.5 truncate text-[10px] text-muted-foreground">{meta}</span>
        ) : null}
      </div>
      {onFocusNode ? (
        <button
          type="button"
          tabIndex={0}
          className="nodrag nopan inline-flex shrink-0 items-center gap-0.5 rounded px-1 py-0.5 text-[10px] text-muted-foreground opacity-60 transition-opacity hover:opacity-100 hover:bg-muted hover:text-foreground group-focus-within:opacity-100"
          onClick={(e) => {
            e.stopPropagation();
            onFocusNode(node.id);
          }}
          aria-label={t(($) => $.dispute.focus.node)}
          title={t(($) => $.dispute.focus.node)}
        >
          <ChevronRight className="size-3" aria-hidden />
        </button>
      ) : null}
    </li>
  );
}

/**
 * Position fan — supports / contradicts / refines rows. Each row focuses the
 * position node on canvas. >5 positions wrap; the rest collapse into a `+N`
 * overflow chip (design §4/§8).
 */
export function PositionFan({
  positions,
  overflow,
  onFocusNode,
}: {
  positions: PositionView[];
  /** Positions that wrapped to the +N overflow (design §4). */
  overflow: PositionView[];
  onFocusNode?: FocusNodeHandler;
}) {
  const { t } = useT("research");
  if (positions.length === 0) {
    return <p className="text-xs text-muted-foreground">{t(($) => $.dispute.empty.positions)}</p>;
  }
  return (
    <ul className="space-y-1.5" data-testid="dispute-position-fan">
      {positions.map((p) => (
        <FocusRow
          key={p.node.id}
          node={p.node}
          glyph={stanceGlyph(p.stance)}
          title={p.node.title || p.node.id}
          tone={stanceTone(p.stance)}
          onFocusNode={onFocusNode}
          testId="dispute-position-row"
          meta={stanceLabel(p.stance, t)}
        />
      ))}
      {overflow.length > 0 ? (
        <li
          className="rounded-md border border-dashed px-2.5 py-1.5 text-[11px] text-muted-foreground"
          data-testid="dispute-position-overflow"
        >
          {t(($) => $.dispute.overflow_more, { count: overflow.length })}
        </li>
      ) : null}
    </ul>
  );
}

/** Evidence relation with supports/contradicts badge + focus affordance. */
export function EvidenceRelation({
  evidence,
  onFocusNode,
}: {
  evidence: DisputeSubgraphModel["evidence"];
  onFocusNode?: FocusNodeHandler;
}) {
  const { t } = useT("research");
  if (evidence.length === 0) {
    return <p className="text-xs text-muted-foreground">{t(($) => $.dispute.empty.evidence)}</p>;
  }
  return (
    <ul className="space-y-1.5" data-testid="dispute-evidence-list">
      {evidence.map((ev) => (
        <FocusRow
          key={ev.node.id}
          node={ev.node}
          glyph={ev.role === "contradicts" ? "✕" : "✓"}
          title={ev.node.title || ev.node.id}
          tone={ev.role === "contradicts" ? "danger" : "success"}
          meta={
            ev.role === "contradicts"
              ? t(($) => $.dispute.stance.contradicts)
              : t(($) => $.dispute.stance.supports)
          }
          onFocusNode={onFocusNode}
          testId="dispute-evidence-row"
        />
      ))}
    </ul>
  );
}
