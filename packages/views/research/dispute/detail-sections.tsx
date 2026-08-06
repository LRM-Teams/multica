"use client";

/**
 * LRM-1472 / UI-04 §5 — dispute detail panel sections keyed by node_type.
 * Rendered inside `ResearchNodeDetail` for dispute-domain nodes. Every section
 * carries non-color encoding (glyphs, status chips, marker chips) and exposes
 * `focusNode(id)` affordances to move the canvas + panel to a related node.
 */

import type { ResearchGraphNode } from "@multica/core/types";
import { Badge } from "@multica/ui/components/ui/badge";
import type { ReactNode } from "react";
import { useT } from "../../i18n/use-t";
import type { DisputeSubgraphModel } from "./model";
import type { FocusNodeHandler } from "./parts";
import { stanceGlyph, stanceLabel } from "./stance";
import {
  DecisionHistory,
  DeliberationTimeline,
  EscalationBanner,
  StatusBadge,
} from "./panels";
import { PositionFan, EvidenceRelation } from "./parts";

function Label({
  text,
  children,
}: {
  text: string;
  children: ReactNode;
}) {
  return (
    <section>
      <h3 className="mb-1 text-[10px] font-semibold tracking-wide text-muted-foreground uppercase">
        {text}
      </h3>
      {children}
    </section>
  );
}

/** Dispute root detail: conflict type, severity, impact, positions, evidence, gate. */
export function DisputeDetailSection({
  model,
  onFocusNode,
}: {
  model: DisputeSubgraphModel;
  onFocusNode?: FocusNodeHandler;
}) {
  const { t } = useT("research");
  const root = model.root;
  if (!root) return null;
  const payload = (root.payload ?? {}) as Record<string, unknown>;
  const conflictType = typeof payload.conflict_type === "string" ? payload.conflict_type : null;
  const severity = typeof payload.severity === "string" ? payload.severity : null;
  const impact = typeof payload.impact_scope === "string" ? payload.impact_scope : null;

  return (
    <div className="space-y-4">
      <div className="flex flex-wrap items-center gap-2">
        <Badge variant="outline" className="text-[10px]">
          ⚖ {t(($) => $.node.dispute)}
        </Badge>
        <StatusBadge node={root} />
        {conflictType ? (
          <Badge variant="secondary" className="text-[10px]">
            {conflictTypeLabel(conflictType, t)}
          </Badge>
        ) : null}
        {severity ? (
          <Badge variant="secondary" className="text-[10px]">
            {t(($) => $.dispute.severity_label)} {severity}
          </Badge>
        ) : null}
        {impact ? (
          <Badge variant="outline" className="text-[10px]">
            {impact}
          </Badge>
        ) : null}
      </div>

      <EscalationBanner escalation={model.escalation} />

      <Label text={t(($) => $.dispute.positions)}>
        <PositionFan positions={model.positions} overflow={[]} onFocusNode={onFocusNode} />
      </Label>

      <Label text={t(($) => $.dispute.evidence)}>
        <EvidenceRelation evidence={model.evidence} onFocusNode={onFocusNode} />
      </Label>

      {model.blocking ? (
        <p
          className="rounded-md border border-destructive/40 bg-destructive/5 px-2.5 py-1.5 text-[11px] font-medium text-destructive"
          data-testid="dispute-detail-blocking-banner"
        >
          ⛔ {t(($) => $.dispute.gate_blocking)}
        </p>
      ) : null}
    </div>
  );
}

/** Position detail: stance, applicable conditions, proposer, evidence sufficiency. */
export function PositionDetailSection({
  node,
  model,
  onFocusNode,
}: {
  node: ResearchGraphNode;
  model: DisputeSubgraphModel;
  onFocusNode?: FocusNodeHandler;
}) {
  const { t } = useT("research");
  const payload = (node.payload ?? {}) as Record<string, unknown>;
  const stance = (payload.stance as DisputeSubgraphModel["positions"][number]["stance"] | undefined) ?? "supports";
  const author = typeof payload.author === "string" ? payload.author : null;
  const applicable =
    typeof payload.applicable_condition === "string" && payload.applicable_condition
      ? payload.applicable_condition
      : null;
  const position = model.positions.find((p) => p.node.id === node.id);
  const supports = position?.evidenceIds.length ?? 0;
  const contradicts = position?.contradictsIds.length ?? 0;

  return (
    <div className="space-y-4">
      <div className="flex flex-wrap items-center gap-2">
        <Badge variant="outline" className="text-[10px]">
          {stanceGlyph(stance)} {t(($) => $.node.dispute_position)}
        </Badge>
        <StatusBadge node={node} />
      </div>

      <Label text={t(($) => $.dispute.stance_label)}>
        <p className="text-sm">{stanceLabel(stance, t)}</p>
      </Label>

      {author ? (
        <Label text={t(($) => $.node.executor)}>
          <p className="text-sm font-medium">{author}</p>
        </Label>
      ) : null}

      {applicable ? (
        <Label text={t(($) => $.dispute.conditions_label)}>
          <p className="text-sm text-muted-foreground">{applicable}</p>
        </Label>
      ) : null}

      <Label text={t(($) => $.dispute.evidence)}>
        <p className="text-sm">
          {t(($) => $.dispute.stance.supports)} {supports} ·{" "}
          {t(($) => $.dispute.stance.contradicts)} {contradicts}
        </p>
      </Label>

      {onFocusNode ? (
        <button
          type="button"
          onClick={() => onFocusNode(node.id)}
          className="text-xs text-primary underline-offset-2 hover:underline"
        >
          {t(($) => $.dispute.focus.node)}
        </button>
      ) : null}
    </div>
  );
}

/** Deliberation detail: participants, turned timeline, deadlock + escalation reasons. */
export function DeliberationDetailSection({
  model,
}: {
  model: DisputeSubgraphModel;
}) {
  const { t } = useT("research");
  const delib = model.deliberation;
  if (!delib) return null;
  const payload = (delib.payload ?? {}) as Record<string, unknown>;
  const participants = Array.isArray(payload.participant_ids)
    ? (payload.participant_ids as string[])
    : [];
  const deadlock = typeof payload.deadlock_reason === "string" ? payload.deadlock_reason : null;
  const escalation = typeof payload.escalation_reason === "string" ? payload.escalation_reason : null;

  return (
    <div className="space-y-4">
      {participants.length > 0 ? (
        <Label text={t(($) => $.dispute.participants)}>
          <div className="flex flex-wrap gap-1">
            {participants.map((p) => (
              <Badge key={p} variant="secondary" className="text-[10px]">
                {p}
              </Badge>
            ))}
          </div>
        </Label>
      ) : null}

      <Label text={t(($) => $.dispute.turns)}>
        <DeliberationTimeline turns={model.turns} model={model} />
      </Label>

      {deadlock ? (
        <Label text={t(($) => $.dispute.deadlock_reason)}>
          <p className="text-sm text-warning">{deadlock}</p>
        </Label>
      ) : null}

      {escalation ? (
        <Label text={t(($) => $.dispute.escalation_reason)}>
          <p className="text-sm text-warning">{escalation}</p>
        </Label>
      ) : null}
    </div>
  );
}

/** Single turn body + progress-marker chip. */
export function TurnDetailSection({ node }: { node: ResearchGraphNode }) {
  const { t } = useT("research");
  const payload = (node.payload ?? {}) as Record<string, unknown>;
  const marker = payload.marker as
    | "position_changed"
    | "evidence_added"
    | "scope_refined"
    | "no_change"
    | undefined;
  const body =
    typeof payload.challenge === "string" && payload.challenge
      ? payload.challenge
      : typeof payload.position === "string" && payload.position
        ? payload.position
        : null;
  return (
    <div className="space-y-4">
      <div className="flex flex-wrap items-center gap-2">
        <Badge variant="outline" className="text-[10px]">
          · {t(($) => $.node.deliberation_turn)}
        </Badge>
        <StatusBadge node={node} />
      </div>
      {marker ? (
        <p className="text-sm">
          {t(($) => $.dispute.turn_marker[marker])}
        </p>
      ) : null}
      {body ? (
        <p className="whitespace-pre-wrap text-sm leading-relaxed">{body}</p>
      ) : null}
    </div>
  );
}

/** Decision detail: verdict, conditions + residual, history. */
export function DecisionDetailSection({
  model,
  onFocusNode,
}: {
  model: DisputeSubgraphModel;
  onFocusNode?: FocusNodeHandler;
}) {
  const { t } = useT("research");
  return (
    <div className="space-y-4">
      <Label text={t(($) => $.dispute.verdict)}>
        <DecisionHistory decision={model.decision} onFocusNode={onFocusNode} />
      </Label>
    </div>
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
