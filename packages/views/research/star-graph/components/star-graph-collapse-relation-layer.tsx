import { cn } from "@multica/ui/lib/utils";
import type { StarRelationView } from "../lib/star-canvas-view-model";
import {
  quadraticEdgePath,
  relationEdgeClass,
} from "./star-graph-canvas-utils";

export function StarGraphCollapseRelationLayer({
  relations,
  width,
  height,
  lowPerformance = false,
}: {
  relations: readonly StarRelationView[];
  width: number;
  height: number;
  lowPerformance?: boolean;
}) {
  if (relations.length === 0 || width <= 0 || height <= 0) return null;

  return (
    <svg
      aria-hidden="true"
      className={cn(
        "sg-collapse-relation-layer pointer-events-none absolute inset-0 h-full w-full",
        lowPerformance && "sg-collapse-relation-layer-low-performance",
      )}
      data-testid="star-graph-collapse-relation-layer"
      preserveAspectRatio="none"
      viewBox={`0 0 ${width} ${height}`}
    >
      {relations.map((relation, index) => (
        <path
          key={relation.id}
          className={cn(
            relationEdgeClass(relation.kind, relation.edgeType),
            "sg-collapse-relation",
          )}
          d={quadraticEdgePath(relation.from, relation.to)}
          pathLength={1}
          style={{ animationDelay: `${Math.min(index, 8) * 18}ms` }}
        />
      ))}
    </svg>
  );
}
