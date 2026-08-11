"use client";

import { StarGraphNode } from "@multica/ui/components/star-graph";
import type { StarEntityView } from "../lib/star-canvas-view-model";

export function StarGraphEntityLayer({
  entities,
  selectedNodeId,
  onSelectNode,
  onOpenNode,
}: {
  entities: readonly StarEntityView[];
  selectedNodeId?: string | null;
  onSelectNode?: (nodeId: string) => void;
  onOpenNode?: (nodeId: string) => void;
}) {
  return (
    <div data-testid="star-graph-entities" className="absolute inset-0">
      {entities.map((entity) => {
        const selected = entity.id === selectedNodeId;
        const state = selected ? "selected" : entity.view.state;
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
            style={{
              left: entity.x - entity.radius,
              top: entity.y - entity.radius,
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
