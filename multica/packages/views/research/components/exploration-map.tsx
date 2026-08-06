"use client";

import { cn } from "@multica/ui/lib/utils";
import type { ResearchGraphEdge, ResearchGraphNode } from "@multica/core/types";
import { useT } from "../../i18n/use-t";
import { normalizeNodeStatusKey, visualForNodeType } from "../lib/node-visuals";

const STATUS_CLASS: Record<string, string> = {
  active: "border-border bg-background",
  done: "border-border bg-muted/40",
  abandoned: "border-destructive/40 bg-destructive/5 opacity-80",
};

export function ExplorationMap({
  nodes,
  edges,
  selectedId,
  onSelect,
}: {
  nodes: ResearchGraphNode[];
  edges: ResearchGraphEdge[];
  selectedId?: string | null;
  onSelect?: (node: ResearchGraphNode) => void;
}) {
  const { t } = useT("research");
  const sorted = [...nodes].sort((a, b) => a.created_at.localeCompare(b.created_at));

  return (
    <div className="flex h-full flex-col gap-2 overflow-y-auto p-3">
      <div className="text-xs font-medium text-muted-foreground">{t(($) => $.panel.graph)}</div>
      <div className="relative flex flex-col gap-2">
        {sorted.length === 0 ? (
          <p className="text-sm text-muted-foreground">—</p>
        ) : (
          sorted.map((node, idx) => {
            const inbound = edges.filter((e) => e.to_node_id === node.id);
            const visual = visualForNodeType(node.node_type);
            const statusKey = normalizeNodeStatusKey(node.status);
            const statusLabel = t(($) => $.node.status[statusKey]);
            const typeLabel = (() => {
              switch (node.node_type) {
                case "goal":
                  return t(($) => $.node.goal);
                case "subquestion":
                  return t(($) => $.node.subquestion);
                case "probe":
                  return t(($) => $.node.probe);
                case "finding":
                  return t(($) => $.node.finding);
                case "conflict":
                  return t(($) => $.node.conflict);
                case "dead_end":
                  return t(($) => $.node.dead_end);
                case "refuted":
                  return t(($) => $.node.refuted);
                case "pivot":
                  return t(($) => $.node.pivot);
                case "roster_change":
                  return t(($) => $.node.roster_change);
                case "stage_gate":
                  return t(($) => $.node.stage_gate);
                case "agent_activity":
                  return t(($) => $.node.agent_activity);
                case "product_round_gate":
                  return t(($) => $.node.product_round_gate);
                default:
                  return node.node_type;
              }
            })();
            return (
              <button
                key={node.id}
                type="button"
                onClick={() => onSelect?.(node)}
                className={cn(
                  "w-full rounded-md border px-3 py-2 text-left transition-colors hover:bg-accent/40",
                  STATUS_CLASS[node.status] ?? STATUS_CLASS.active,
                  visual.ringClass,
                  selectedId === node.id && "bg-accent",
                )}
              >
                <div className="flex items-center justify-between gap-2">
                  <span className="text-[11px] uppercase tracking-wide text-muted-foreground">
                    {typeLabel}
                    {idx > 0 && inbound.length > 0
                      ? ` · ${inbound.map((e) => e.edge_type).join(",")}`
                      : ""}
                  </span>
                  <span className="text-[10px] text-muted-foreground">{statusLabel}</span>
                </div>
                <div className="truncate text-sm font-medium">{node.title || node.node_type}</div>
                {node.summary ? (
                  <div className="mt-0.5 line-clamp-2 text-xs text-muted-foreground">{node.summary}</div>
                ) : null}
              </button>
            );
          })
        )}
      </div>
    </div>
  );
}
