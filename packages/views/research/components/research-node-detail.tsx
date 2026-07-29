"use client";

import type { ResearchGraphNode } from "@multica/core/types";
import { Badge } from "@multica/ui/components/ui/badge";
import { useT } from "../../i18n/use-t";
import { visualForNodeType } from "../lib/node-visuals";

export function ResearchNodeDetail({ node }: { node: ResearchGraphNode }) {
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

  return (
    <div className="pointer-events-none absolute bottom-4 left-4 z-10 max-w-md rounded-xl border bg-card/95 p-3 shadow-lg backdrop-blur">
      <div className="mb-2 flex items-center gap-2">
        <span className={`h-2 w-2 rounded-full ${visual.accentBarClass}`} />
        <Badge variant="outline" className="text-[10px] uppercase">
          {typeLabel}
        </Badge>
        <Badge variant="secondary" className="text-[10px]">
          {node.status}
        </Badge>
      </div>
      <div className="text-sm font-semibold">{node.title}</div>
      {node.summary ? (
        <p className="mt-1 whitespace-pre-wrap text-xs leading-relaxed text-muted-foreground">
          {node.summary}
        </p>
      ) : null}
    </div>
  );
}
