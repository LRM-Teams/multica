"use client";

import {
  StarGraphNode,
  resolveStarGraphState,
  type StarGraphTier,
} from "@multica/ui/components/star-graph";
import { cn } from "@multica/ui/lib/utils";
import type { D5LensDisplayHints } from "../../lib/research-d5-lens-display";
import type { MotionDirective } from "../../motion/directives";
import type { StarEntityView } from "../lib/star-canvas-view-model";

export interface StarGraphEntityLabels {
  tierHeaders: Record<StarGraphTier, string>;
  documentCount: (count: number) => string;
  confidence: (value: number) => string;
  conclusionCount: (count: number) => string;
  documentBadge: (count: number) => string;
}

export function StarGraphEntityLayer({
  entities,
  selectedNodeId,
  nodeAccessibleNames,
  lensHints,
  motionDirectives,
  labels,
  onSelectNode,
  onOpenNode,
}: {
  entities: readonly StarEntityView[];
  selectedNodeId?: string | null;
  nodeAccessibleNames?: ReadonlyMap<string, string>;
  lensHints?: D5LensDisplayHints;
  motionDirectives?: ReadonlyMap<string, MotionDirective | null>;
  labels: StarGraphEntityLabels;
  onSelectNode?: (nodeId: string) => void;
  onOpenNode?: (nodeId: string) => void;
}) {
  return (
    <div data-testid="star-graph-entities" className="absolute inset-0">
      {entities.map((entity) => {
        const selected = entity.id === selectedNodeId;
        const focusable = selected || (!selectedNodeId && entity === entities[0]);
        const state = resolveStarGraphState(
          selected ? [entity.view.state, "selected"] : [entity.view.state],
        );
        const motion = motionDirectives?.get(entity.id) ?? null;
        const dimmed =
          selected ? false : lensHints?.dimmedNodeIds.has(entity.id) ?? false;
        const emphasized =
          selected ? false : lensHints?.emphasizedNodeIds.has(entity.id) ?? false;
        return (
          <StarGraphNode
            key={entity.id}
            nodeId={entity.id}
            tabIndex={focusable ? 0 : -1}
            tier={entity.view.tier}
            state={state}
            title={entity.view.title}
            subLabel={entity.view.subLabel}
            headerLabel={
              entity.view.tier === "s"
                ? undefined
                : labels.tierHeaders[entity.view.tier]
            }
            agentBadge={entity.view.agentBadge}
            metrics={entity.view.metrics}
            metricText={
              entity.view.metrics
                ? {
                    documentCount:
                      entity.view.metrics.documentCount != null
                        ? labels.documentCount(entity.view.metrics.documentCount)
                        : undefined,
                    confidence:
                      entity.view.metrics.confidence != null
                        ? labels.confidence(entity.view.metrics.confidence)
                        : undefined,
                    conclusionCount:
                      entity.view.metrics.conclusionCount != null
                        ? labels.conclusionCount(entity.view.metrics.conclusionCount)
                        : undefined,
                    documentBadge:
                      entity.view.metrics.documentCount != null
                        ? labels.documentBadge(entity.view.metrics.documentCount)
                        : undefined,
                  }
                : undefined
            }
            busy={entity.view.state === "run"}
            accessibleName={nodeAccessibleNames?.get(entity.id)}
            nodeId={entity.id}
            tabIndex={selected ? 0 : -1}
            className={cn(
              dimmed && "sg-lens-dim",
              emphasized && "sg-lens-emphasis",
              motion?.className,
              motion?.markerClass,
            )}
            style={{
              left: entity.x - entity.radius,
              top: entity.y - entity.radius,
              ...motion?.style,
            }}
            onOpen={() => {
              // Opening is a single semantic command. The open owner also
              // writes selection before presenting detail; calling both here
              // duplicates store writes when the session uses one handler for
              // select + open. Selection remains the fallback for consumers
              // that only need a clickable graph.
              if (onOpenNode) onOpenNode(entity.id);
              else onSelectNode?.(entity.id);
            }}
          />
        );
      })}
    </div>
  );
}
