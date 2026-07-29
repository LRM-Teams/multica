"use client";

import type { ResearchGraphNode, ResearchSource } from "@multica/core/types";
import { Badge } from "@multica/ui/components/ui/badge";
import { useT } from "../../i18n/use-t";
import { visualForNodeType } from "../lib/node-visuals";

const EMPTY_SOURCES: ResearchSource[] = [];

function payloadString(payload: unknown, key: string): string | null {
  if (!payload || typeof payload !== "object") return null;
  const value = (payload as Record<string, unknown>)[key];
  return typeof value === "string" && value.trim() ? value : null;
}

function payloadNumber(payload: unknown, key: string): number | null {
  if (!payload || typeof payload !== "object") return null;
  const value = (payload as Record<string, unknown>)[key];
  return typeof value === "number" && Number.isFinite(value) ? value : null;
}

export function ResearchNodeDetail({
  node,
  sources = EMPTY_SOURCES,
}: {
  node: ResearchGraphNode;
  sources?: ResearchSource[];
}) {
  const { t } = useT("research");
  const visual = visualForNodeType(node.node_type);
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
      default:
        return node.node_type;
    }
  })();

  const sourceId = payloadString(node.payload, "source_id");
  const linked = sourceId ? sources.find((s) => s.id === sourceId) : undefined;
  const url = linked?.url || payloadString(node.payload, "url");
  const weight =
    linked?.credibility_weight ?? payloadNumber(node.payload, "credibility_weight");
  const sourceClass = linked?.source_class || payloadString(node.payload, "source_class");

  return (
    <div className="pointer-events-auto absolute bottom-4 left-4 z-10 max-w-md rounded-xl border bg-card/95 p-3 shadow-lg backdrop-blur">
      <div className="mb-2 flex flex-wrap items-center gap-2">
        <span className={`h-2 w-2 rounded-full ${visual.accentBarClass}`} />
        <Badge variant="outline" className="text-[10px] uppercase">
          {typeLabel}
        </Badge>
        <Badge variant="secondary" className="text-[10px]">
          {node.status}
        </Badge>
        {sourceClass ? (
          <Badge variant="outline" className="text-[10px]">
            {sourceClass}
          </Badge>
        ) : null}
        {typeof weight === "number" ? (
          <Badge variant="secondary" className="text-[10px]">
            {t(($) => $.panel.weight)} {weight.toFixed(2)}
          </Badge>
        ) : null}
      </div>
      <div className="text-sm font-semibold">{node.title}</div>
      {node.summary ? (
        <p className="mt-1 whitespace-pre-wrap text-xs leading-relaxed text-muted-foreground">
          {node.summary}
        </p>
      ) : null}
      {url ? (
        <a
          href={url}
          target="_blank"
          rel="noreferrer"
          className="mt-2 block truncate text-[11px] text-primary underline-offset-2 hover:underline"
        >
          {linked?.title || url}
        </a>
      ) : null}
      {linked?.excerpt ? (
        <p className="mt-1 line-clamp-3 text-[11px] text-muted-foreground">{linked.excerpt}</p>
      ) : null}
    </div>
  );
}
