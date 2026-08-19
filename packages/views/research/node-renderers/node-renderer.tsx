"use client";

/**
 * Research V6 — node renderer hub (UI-01 / LRM-1475).
 *
 * Funnel: canonical `ResearchV6ProjectionNode` → family surface → card.
 * Consumes ONLY the backend Projection read model (node_kind / node_subtype /
 * title / bounded summary / status / importance / actor / attempt / evidence)
 * and never infers canonical state from chat, animation, or display grouping.
 *
 * The round-dot rule: dots only mark port/status glyphs; the task body is
 * always a clickable card. Display grouping is never written back.
 */

import type {
  ResearchV6ProjectionNode,
  ResearchV6UnknownKindDiagnostic,
} from "@multica/core/types/research-v6";
import { FileText } from "lucide-react";
import { cn } from "@multica/ui/lib/utils";
import { Tooltip, TooltipTrigger, TooltipContent } from "@multica/ui/components/ui/tooltip";
import { classifyNodeFamily, type NodeKindFamily } from "./node-kind-registry";
import { NodeCardShell, type NodeCardZoom } from "./node-card-shell";
import { GenericNodeCard } from "./generic-node-card";
import type { NodeCardState } from "./node-state-matrix";
import { resolveCardState } from "./node-state-matrix";
import { nodeCardFacts } from "./node-detail-fields";
import { importanceToStars } from "./node-importance";
import { useT } from "../../i18n/use-t";

/** Shared stable empty array for the optional diagnostics prop (no re-alloc per render). */
const NO_DIAGNOSTICS: ResearchV6UnknownKindDiagnostic[] = [];

/** Raw status string → visual state candidates (state matrix feeding). */
function statesFromStatus(status: string): NodeCardState[] {
  const s = (status ?? "").toLowerCase();
  const signals: NodeCardState[] = [];
  if (s.includes("fail") || s === "failed" || s.includes("error")) signals.push("failed");
  if (s.includes("running") || s === "active" || s === "in_progress" || s.includes("executing"))
    signals.push("running");
  if (s.includes("stale") || s.includes("superseded") || s === "refuted")
    signals.push("stale");
  if (s === "done" || s === "accepted" || s === "resolved") signals.push("terminal");
  if (s === "pending" || s === "waiting" || s === "queued") signals.push("loading");
  if (signals.length === 0) signals.push("default");
  return signals;
}

export interface NodeRendererProps {
  node: ResearchV6ProjectionNode;
  /** Mutable diagnostics log for unknown kinds (canonical contract). */
  diagnostics?: ResearchV6UnknownKindDiagnostic[];
  /** Zoom density tier. */
  zoom?: NodeCardZoom;
  /** Force a specific state (e.g. detail overlay) instead of projecting. */
  overriddenState?: NodeCardState;
  onOpen?: () => void;
  /** Show the footer meta (task/attempt/evidence) — defaults to 100%+. */
  showMeta?: boolean;
}

/**
 * Render one V6 projection node as a card. Unknown kinds always degrade to
 * `GenericNodeCard`; known kinds render via their family shell.
 */
export function NodeRenderer({
  node,
  diagnostics = NO_DIAGNOSTICS,
  zoom = 1,
  overriddenState,
  onOpen,
  showMeta,
}: NodeRendererProps) {
  const { t } = useT("research");
  const surface = classifyNodeFamily(
    { id: node.id, node_kind: node.node_kind, run_id: node.run_id },
    diagnostics,
  );

  if (surface.isGeneric) {
    return (
      <GenericNodeCard
        nodeId={node.id}
        kind={surface.kind}
        title={node.title}
        summary={node.summary}
        status={node.status}
        diagnostic={surface.diagnostic}
        zoom={zoom}
        onOpen={onOpen}
      />
    );
  }

  const state =
    overriddenState ?? resolveCardState(statesFromStatus(node.status));
  const family = surface.family as NodeKindFamily;
  const stars = importanceToStars(node.importance);
  const meta = renderMeta(
    node,
    showMeta !== false,
    t(($) => $.node_card.task_meta),
  );
  const legend = node.actor_agent_id
    ? t(($) => $.node_card.agent_legend, {
        id: node.actor_agent_id.slice(0, 6),
      })
    : undefined;
  const facts = nodeCardFacts({
    actorAgentId: node.actor_agent_id,
    detail: node.detail,
  });

  return (
    <NodeCardShell
      family={family}
      state={state}
      title={node.title}
      typeLabel={t(
        ($) =>
          $.node_card.kinds[
            surface.kind as keyof typeof $.node_card.kinds
          ],
      )}
      summary={node.summary}
      importance={stars}
      owner={facts.owner}
      objective={facts.objective}
      currentAction={facts.currentAction}
      resolvedCount={facts.resolvedCount}
      progressCount={facts.progressCount}
      riskCount={facts.riskCount}
      zoom={zoom}
      onOpen={onOpen}
      meta={meta}
      legend={legend}
    />
  );
}

function renderMeta(
  node: ResearchV6ProjectionNode,
  show: boolean,
  taskLabel: string,
) {
  if (!show) return undefined;
  const parts: string[] = [];
  if (node.attempt_id) parts.push(`#${node.attempt_id.replace(/^.*?:/, "")}`);
  if (node.task_id) parts.push(taskLabel);
  return (
    <>
      {parts.length > 0 && (
        <span data-testid="node-meta" className="flex items-center gap-1 text-muted-foreground">
          {parts.map((p) => (
            <span key={p} className="rounded bg-muted px-1">
              {p}
            </span>
          ))}
        </span>
      )}
      <NodeEvidenceCount detail={node.detail} />
    </>
  );
}

function NodeEvidenceCount({ detail }: { detail: unknown }) {
  const count = evidenceCountOf(detail);
  if (count === null) return null;
  const title = count === 0 ? "无证据" : `${count} 条证据`;
  return (
    <Tooltip>
      <TooltipTrigger
        render={
          <span
            data-testid="node-evidence-count"
            className={cn("flex items-center gap-1 text-muted-foreground", count === 0 && "opacity-60")}
          />
        }
      >
        <FileText className="h-3 w-3" />
        {count}
      </TooltipTrigger>
      <TooltipContent side="top">{title}</TooltipContent>
    </Tooltip>
  );
}

/** Bounded evidence count from the opaque detail payload (never a fact register). */
function evidenceCountOf(detail: unknown): number | null {
  if (detail && typeof detail === "object" && !Array.isArray(detail)) {
    const d = detail as Record<string, unknown>;
    const evidence = d.evidence;
    const raw =
      typeof d.evidence_count === "number"
        ? d.evidence_count
        : typeof d.evidenceCount === "number"
          ? d.evidenceCount
          : Array.isArray(evidence)
            ? evidence.length
            : null;
    if (typeof raw === "number" && Number.isFinite(raw)) return Math.max(0, Math.round(raw));
    if (Array.isArray(evidence)) return evidence.length;
  }
  return null;
}

/** Convenience alias: V6NodeCard === NodeRenderer. */
export const V6NodeCard = NodeRenderer;
