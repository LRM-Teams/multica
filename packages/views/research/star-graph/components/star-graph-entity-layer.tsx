"use client";

import { StarGraphNode } from "@multica/ui/components/star-graph";
import { cn } from "@multica/ui/lib/utils";
import type { D5LensDisplayHints } from "../../lib/research-d5-lens-display";
import type { MotionDirective } from "../../motion/directives";
import type { StarEntityView } from "../lib/star-canvas-view-model";

export function StarGraphEntityLayer({
  entities,
  selectedNodeId,
  nodeAccessibleNames,
  lensHints,
  motionDirectives,
  onSelectNode,
  onOpenNode,
}: {
  entities: readonly StarEntityView[];
  selectedNodeId?: string | null;
  nodeAccessibleNames?: ReadonlyMap<string, string>;
  lensHints?: D5LensDisplayHints;
  motionDirectives?: ReadonlyMap<string, MotionDirective | null>;
  onSelectNode?: (nodeId: string) => void;
  onOpenNode?: (nodeId: string) => void;
}) {
  return (
    <div data-testid="star-graph-entities" className="absolute inset-0">
      {entities.map((entity) => {
        const selected = entity.id === selectedNodeId;
        const state = selected ? "selected" : entity.view.state;
        const motion = motionDirectives?.get(entity.id) ?? null;
        const dimmed =
          selected ? false : lensHints?.dimmedNodeIds.has(entity.id) ?? false;
        const emphasized =
          selected ? false : lensHints?.emphasizedNodeIds.has(entity.id) ?? false;
        return (
          <StarGraphNode
            key={entity.id}
            tier={entity.view.tier}
            state={state}
            title={entity.view.title}
            subLabel={entity.view.subLabel}
            headerLabel={entity.view.headerLabel}
            agentBadge={entity.view.agentBadge}
            metrics={entity.view.metrics}
            busy={entity.view.state === "run"}
            accessibleName={nodeAccessibleNames?.get(entity.id)}
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
              onSelectNode?.(entity.id);
              onOpenNode?.(entity.id);
            }}
          />
        );
      })}
    </div>
  );
}
