import { StarGraphNode } from "@multica/ui/components/star-graph";
import type { CSSProperties } from "react";
import type { StarGraphCollapseGhost } from "../lib/star-graph-collapse-ghosts";
import type { StarGraphEntityLabels } from "./star-graph-entity-layer";

interface CollapseGhostStyle extends CSSProperties {
  "--collapse-target-x": string;
  "--collapse-target-y": string;
}

export function StarGraphCollapseGhostLayer({
  ghosts,
  labels,
  lowPerformance = false,
  sTierPresentation = "label",
}: {
  ghosts: readonly StarGraphCollapseGhost[];
  labels: StarGraphEntityLabels;
  lowPerformance?: boolean;
  sTierPresentation?: "label" | "point";
}) {
  if (ghosts.length === 0) return null;

  return (
    <div
      aria-hidden="true"
      className="sg-collapse-ghost-layer absolute inset-0"
      data-testid="star-graph-collapse-ghost-layer"
    >
      {ghosts.map(({ entity, targetX, targetY, delayMs }) => (
        <StarGraphNode
          key={entity.id}
          nodeId={undefined}
          tabIndex={-1}
          tier={entity.view.tier}
          state={entity.view.state}
          title={entity.view.title}
          subLabel={entity.view.subLabel}
          headerLabel={
            entity.view.tier === "s"
              ? undefined
              : labels.tierHeaders[entity.view.tier]
          }
          agentBadge={entity.view.agentBadge}
          sTierPresentation={sTierPresentation}
          metrics={entity.view.metrics}
          className="sg-collapse-ghost"
          style={{
            left: entity.x - entity.radius,
            top: entity.y - entity.radius,
            animationDelay: `${delayMs}ms`,
            "--collapse-target-x": `${targetX - entity.x}px`,
            "--collapse-target-y": `${targetY - entity.y}px`,
            filter: lowPerformance ? "none" : undefined,
          } as CollapseGhostStyle}
        />
      ))}
    </div>
  );
}
