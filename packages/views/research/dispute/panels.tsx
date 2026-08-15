"use client";

/**
 * LRM-1472 / UI-04 — dispute subgraph panels: deliberation timeline,
 * escalation strip, decision history, and the top-level `DisputeCard` browser
 * that composes the whole fixture subgraph with bidirectional focus.
 *
 * Non-color encodings (glyphs, status chips, marker glyph, line styles) keep
 * the lifecycle & stance legible in grayscale/screen readers. All facts come
 * from `buildDisputeModel` (typed edges) — never from text or animation.
 */

import type { ResearchGraphNode } from "@multica/core/types";
import { Badge } from "@multica/ui/components/ui/badge";
import { cn } from "@multica/ui/lib/utils";
import { ArrowUp, ArrowUpRight } from "lucide-react";
import type { ReactNode } from "react";
import { useT } from "../../i18n/use-t";
import { disputeNodeGlyph } from "./encode";
import type { DecisionView, DisputeSubgraphModel, TurnView } from "./model";
import type { FocusNodeHandler } from "./parts";
import { EvidenceRelation, PositionFan } from "./parts";
import { disputeStatusLabel } from "./status-label";

/** Status chip with data-status-key for non-color verification. */
export function StatusBadge({
  node,
  className,
}: {
  node: ResearchGraphNode;
  className?: string;
}) {
  const { t } = useT("research");
  return (
    <Badge
      variant="secondary"
      className={cn("shrink-0 text-[10px] font-medium", className)}
      data-status-key={(node.status || "").toLowerCase()}
    >
      {disputeStatusLabel(node.status, t)}
    </Badge>
  );
}

/** Deliberation spine — turns as rows with marker chips + progress watermark. */
export function DeliberationTimeline({
  turns,
  model,
}: {
  turns: TurnView[];
  model: DisputeSubgraphModel;
}) {
  const { t } = useT("research");
  const delib = model.deliberation;
  if (!delib) return null;
  const payload = (delib.payload ?? {}) as Record<string, unknown>;
  const turnCount = typeof payload.turn_count === "number" ? payload.turn_count : turns.length;
  const budget = typeof payload.budget === "number" ? payload.budget : null;
  const deadlocked = (delib.status || "").toLowerCase() === "deadlocked";
  return (
    <div className="space-y-2" data-testid="dispute-deliberation-timeline">
      <div className="flex flex-wrap items-center gap-2">
        <span className="text-[11px] font-semibold text-foreground">{delib.title}</span>
        <StatusBadge node={delib} />
        {deadlocked ? (
          <Badge
            variant="outline"
            className="border-destructive/40 text-[10px] text-destructive"
            data-testid="dispute-deadlock-badge"
          >
            {t(($) => $.dispute.status.deadlocked)}
          </Badge>
        ) : null}
      </div>
      {turns.length === 0 ? (
        <p className="text-xs text-muted-foreground">{t(($) => $.dispute.empty.turns)}</p>
      ) : (
        <ol className="space-y-1.5" data-testid="dispute-turn-list">
          {turns.map((turn, i) => (
            <li
              key={turn.node.id}
              className="flex items-start gap-2 rounded-md border border-muted-foreground/20 bg-muted/10 px-2.5 py-1.5"
              data-turn-index={i}
              data-testid="dispute-turn-row"
            >
              <span className="shrink-0 text-[10px] font-bold text-muted-foreground" aria-hidden>
                T{i + 1}
              </span>
              <div className="min-w-0 flex-1">
                <div className="flex flex-wrap items-center gap-1.5">
                  <span className="text-xs font-medium text-foreground">
                    {turn.node.title || turn.node.id}
                  </span>
                  {turn.marker ? <MarkerChip marker={turn.marker} /> : null}
                </div>
                {turnBody(turn.node)}
              </div>
            </li>
          ))}
        </ol>
      )}
      {budget != null ? (
        <p className="text-[10px] text-muted-foreground">
          {t(($) => $.dispute.turns)} {turnCount}/{budget}
        </p>
      ) : null}
    </div>
  );
}

function turnBody(node: ResearchGraphNode): ReactNode | null {
  const payload = (node.payload ?? {}) as Record<string, unknown>;
  const text =
    typeof payload.challenge === "string" && payload.challenge
      ? payload.challenge
      : typeof payload.position === "string" && payload.position
        ? payload.position
        : null;
  if (!text) return null;
  return <p className="mt-0.5 line-clamp-2 text-[10px] text-muted-foreground">{text}</p>;
}

/** Turn progress-marker chip (glyph + label — non-color). */
function MarkerChip({ marker }: { marker: NonNullable<TurnView["marker"]> }) {
  const { t } = useT("research");
  const glyph =
    marker === "position_changed"
      ? "↺"
      : marker === "evidence_added"
        ? "＋"
        : marker === "scope_refined"
          ? "⇄"
          : "·";
  return (
    <Badge
      variant="outline"
      className="shrink-0 text-[10px]"
      data-testid="dispute-turn-marker"
      data-marker={marker}
    >
      <span aria-hidden>{glyph}</span> {t(($) => $.dispute.turn_marker[marker])}
    </Badge>
  );
}

/** Escalation strip — Director strip (only when escalated) per design §4/§8. */
export function EscalationBanner({
  escalation,
}: {
  escalation: DisputeSubgraphModel["escalation"];
}) {
  const { t } = useT("research");
  if (!escalation.requires) return null;
  const target = escalation.target;
  const targetTitle = target?.title || t(($) => $.dispute.director_adjudicating);
  return (
    <section
      className="flex items-center gap-2 rounded-md border border-warning/40 bg-warning/5 px-2.5 py-2"
      data-testid="dispute-escalation-banner"
      data-escalated="true"
    >
      <span
        className="inline-flex size-5 shrink-0 items-center justify-center rounded-full border-2 border-warning text-warning"
        aria-hidden
      >
        <ArrowUp className="size-3" />
      </span>
      <div className="min-w-0 flex-1">
        <p className="text-[11px] font-semibold text-foreground">
          {t(($) => $.dispute.escalated_to)} {targetTitle}
        </p>
        <p className="text-[10px] text-warning">{t(($) => $.dispute.director_adjudicating)}</p>
      </div>
      <span
        className="shrink-0 rounded border-2 border-dashed border-warning/70 p-1 text-warning"
        aria-hidden
      >
        <ArrowUpRight className="size-3" />
      </span>
    </section>
  );
}

function VerdictNote({ node }: { node: ResearchGraphNode }) {
  const { t } = useT("research");
  const payload = (node.payload ?? {}) as Record<string, unknown>;
  const conditions = Array.isArray(payload.conditions) ? (payload.conditions as string[]) : [];
  const residual = typeof payload.residual_impact === "string" && payload.residual_impact
    ? payload.residual_impact
    : null;
  return (
    <div className="space-y-0.5" data-testid="dispute-verdict-note">
      {conditions.length > 0 ? (
        <p className="text-[10px] text-muted-foreground">
          {t(($) => $.dispute.conditions_label)} {conditions.join(" · ")}
        </p>
      ) : null}
      {residual ? (
        <p className="text-[10px] text-warning">
          {t(($) => $.dispute.residual_note)} {residual}
        </p>
      ) : null}
    </div>
  );
}

/** Decision node + prior-superseded history (newest first), history retained. */
export function DecisionHistory({
  decision,
  onFocusNode,
}: {
  decision: DecisionView;
  onFocusNode?: FocusNodeHandler;
}) {
  const { t } = useT("research");
  const current = decision.current;
  return (
    <section className="space-y-2" data-testid="dispute-decision-history">
      {current ? (
        <div
          className="rounded-lg border border-success/40 bg-success/5 px-2.5 py-2"
          data-node-id={current.id}
          data-testid="dispute-decision-current"
        >
          <div className="flex items-center gap-1.5">
            <span className="text-success-strong" aria-hidden>
              {disputeNodeGlyph("decision", current.status)}
            </span>
            <span className="text-xs font-semibold text-foreground">{current.title}</span>
            <StatusBadge node={current} />
          </div>
          <div className="mt-1.5 space-y-1">
            <VerdictNote node={current} />
            {onFocusNode ? (
              <button
                type="button"
                onClick={(e) => {
                  e.stopPropagation();
                  onFocusNode(current.id);
                }}
                className="inline-flex items-center gap-0.5 rounded px-1 text-[10px] text-muted-foreground hover:bg-muted hover:text-foreground"
                aria-label={t(($) => $.dispute.focus.node)}
              >
                {disputeNodeGlyph("decision", current.status)} {t(($) => $.dispute.view_history)}
              </button>
            ) : null}
          </div>
        </div>
      ) : null}
      {decision.history.length > 0 ? (
        <div>
          <h3 className="mb-1 text-[10px] font-semibold tracking-wide text-muted-foreground uppercase">
            {t(($) => $.dispute.view_history)}
          </h3>
          <ul className="space-y-1.5">
            {decision.history.map((h) => (
              <li
                key={h.node.id}
                className="rounded-md border border-dashed border-muted-foreground/40 px-2.5 py-1.5"
                data-node-id={h.node.id}
                data-testid="dispute-decision-history-row"
                aria-label={`${t(($) => $.dispute.superseded)} ${h.node.title}`}
              >
                <div className="flex items-start justify-between gap-2">
                  <div className="min-w-0">
                    <span className="text-[10px] font-semibold text-muted-foreground line-through">
                      {h.node.title || h.node.id}
                    </span>
                    <VerdictNote node={h.node} />
                    {h.supersededBy ? (
                      <span
                        className="mt-0.5 block text-[10px] text-destructive"
                        data-testid="dispute-superseded-flag"
                      >
                        {disputeNodeGlyph("decision", h.node.status)} {t(($) => $.dispute.superseded)}
                      </span>
                    ) : null}
                  </div>
                  {onFocusNode ? (
                    <button
                      type="button"
                      tabIndex={0}
                      className="nodrag nopan shrink-0 rounded px-1 py-0.5 text-[10px] text-muted-foreground hover:bg-muted hover:text-foreground"
                      onClick={(e) => {
                        e.stopPropagation();
                        onFocusNode(h.node.id);
                      }}
                      aria-label={t(($) => $.dispute.focus.node)}
                    >
                      →
                    </button>
                  ) : null}
                </div>
              </li>
            ))}
          </ul>
        </div>
      ) : null}
    </section>
  );
}

/** Collapsible labelled section. */
export function Section({
  title,
  defaultOpen = true,
  children,
}: {
  title: string;
  defaultOpen?: boolean;
  children: ReactNode;
}) {
  return (
    <section data-testid="dispute-section">
      <h3 className="mb-1 text-[10px] font-semibold tracking-wide text-muted-foreground uppercase">
        {title}
      </h3>
      {defaultOpen ? children : null}
    </section>
  );
}

/**
 * Top-level dispute subgraph browser. Composes the fan, evidence, deliberation,
 * escalation and decision history from the typed-edge model. Primary bounded
 * "browse" surface for the fixture (acceptance 1). Drops to a real empty state
 * when the subgraph has no dispute root.
 */
export function DisputeCard({
  model,
  onFocusNode,
  defaultOpen = true,
}: {
  model: DisputeSubgraphModel;
  onFocusNode?: FocusNodeHandler;
  /** Whether panels start expanded (collapsible sections). */
  defaultOpen?: boolean;
}) {
  const { t } = useT("research");
  const root = model.root;
  if (!root) {
    return (
      <div data-testid="dispute-card-empty" className="rounded-lg border bg-muted/10 px-3 py-3">
        <p className="text-xs text-muted-foreground">{t(($) => $.dispute.empty.root)}</p>
      </div>
    );
  }

  const rootPayload = (root.payload ?? {}) as Record<string, unknown>;
  const conflictType =
    typeof rootPayload.conflict_type === "string" ? rootPayload.conflict_type : null;
  const severity = typeof rootPayload.severity === "string" ? rootPayload.severity : null;

  return (
    <article
      className="space-y-3 rounded-xl border bg-card p-3"
      data-testid="dispute-card"
      data-node-id={root.id}
      data-lifecycle={model.lifecycle}
    >
      <header className="flex flex-wrap items-center gap-2">
        <span className="text-warning" aria-hidden>
          {disputeNodeGlyph("dispute", root.status)}
        </span>
        <h2 className="min-w-0 flex-1 text-sm font-semibold text-foreground">{root.title}</h2>
        <StatusBadge node={root} />
      </header>

      {conflictType ? (
        <p className="text-[11px] text-muted-foreground">
          {t(($) => $.dispute.conflict_type_label)}{" "}
          {conflictTypeLabel(conflictType, t)} {severity ? `· ${severity}` : ""}
        </p>
      ) : null}

      <EscalationBanner escalation={model.escalation} />

      <Section title={t(($) => $.dispute.positions)} defaultOpen={defaultOpen}>
        <PositionFan
          positions={model.positions}
          overflow={model.overflowPositions}
          onFocusNode={onFocusNode}
        />
      </Section>

      <Section title={t(($) => $.dispute.evidence)} defaultOpen={defaultOpen}>
        <EvidenceRelation evidence={model.evidence} onFocusNode={onFocusNode} />
      </Section>

      {model.deliberation ? (
        <Section title={t(($) => $.dispute.turns)} defaultOpen={defaultOpen}>
          <DeliberationTimeline turns={model.turns} model={model} />
        </Section>
      ) : null}

      {model.decision.current || model.decision.history.length > 0 ? (
        <Section title={t(($) => $.dispute.verdict)} defaultOpen={defaultOpen}>
          <DecisionHistory decision={model.decision} onFocusNode={onFocusNode} />
        </Section>
      ) : null}

      {model.blocking ? (
        <p
          className="rounded-md border border-destructive/40 bg-destructive/5 px-2.5 py-1.5 text-[11px] font-medium text-destructive"
          data-testid="dispute-blocking-banner"
        >
          ⛔ {t(($) => $.dispute.gate_blocking)}
        </p>
      ) : null}
    </article>
  );
}

function conflictTypeLabel(
  type: string,
  t: ReturnType<typeof useT<"research">>["t"],
): string {
  switch (type) {
    case "fact":
      return t(($) => $.dispute.conflict_type.fact);
    case "definition":
      return t(($) => $.dispute.conflict_type.definition);
    case "scope":
      return t(($) => $.dispute.conflict_type.scope);
    case "time":
      return t(($) => $.dispute.conflict_type.time);
    case "population":
      return t(($) => $.dispute.conflict_type.population);
    case "method":
      return t(($) => $.dispute.conflict_type.method);
    case "measurement":
      return t(($) => $.dispute.conflict_type.measurement);
    case "interpretation":
      return t(($) => $.dispute.conflict_type.interpretation);
    case "source_identity":
      return t(($) => $.dispute.conflict_type.source_identity);
    default:
      return type;
  }
}
