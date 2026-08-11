"use client";

import { cn } from "@multica/ui/lib/utils";
import type { D5LensDisplayHints } from "../../lib/research-d5-lens-display";
import type { StarRelationView } from "../lib/star-canvas-view-model";
import { quadraticEdgePath, relationEdgeClass } from "./star-graph-canvas-utils";

export function StarGraphEdges({
  relations,
  width,
  height,
  lensHints,
}: {
  relations: readonly StarRelationView[];
  width: number;
  height: number;
  lensHints?: D5LensDisplayHints;
}) {
  if (relations.length === 0 || width <= 0 || height <= 0) return null;

  return (
    <svg
      data-testid="star-graph-edges"
      className="sg-edge-layer pointer-events-none absolute inset-0 h-full w-full"
      viewBox={`0 0 ${width} ${height}`}
      preserveAspectRatio="none"
      aria-hidden="true"
    >
      <defs>
        <linearGradient id="sg-merge-gradient" x1="0%" y1="0%" x2="100%" y2="0%">
          <stop stopColor="var(--sg-merge-start)" />
          <stop offset="1" stopColor="var(--sg-merge-end)" />
        </linearGradient>
      </defs>
      {relations.map((relation) => (
        <path
          key={relation.id}
          data-testid={`star-graph-edge-${relation.id}`}
          data-kind={relation.kind}
          data-edge-type={relation.edgeType}
          className={cn(
            relationEdgeClass(relation.kind, relation.edgeType),
            lensHints?.dimmedRelationIds.has(relation.id) && "sg-lens-dim",
            lensHints?.emphasizedRelationIds.has(relation.id) && "sg-lens-emphasis",
          )}
          d={quadraticEdgePath(relation.from, relation.to)}
        />
      ))}
    </svg>
  );
}
