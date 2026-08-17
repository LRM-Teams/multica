"use client";

import { useId } from "react";
import { cn } from "@multica/ui/lib/utils";
import type { D5LensDisplayHints } from "../../lib/research-d5-lens-display";
import type { StarRelationView } from "../lib/star-canvas-view-model";
import {
  isEdgeLabelClear,
  quadraticEdgePath,
  relationEdgeClass,
} from "./star-graph-canvas-utils";

const EMPTY_LABEL_OBSTACLES: readonly {
  id: string;
  x: number;
  y: number;
  radius: number;
}[] = [];

export function StarGraphEdges({
  relations,
  width,
  height,
  lensHints,
  relationLabels,
  labelObstacles = EMPTY_LABEL_OBSTACLES,
  revealingRelationIds,
  revealLowPerformance = false,
}: {
  relations: readonly StarRelationView[];
  width: number;
  height: number;
  lensHints?: D5LensDisplayHints;
  relationLabels?: Partial<Record<StarRelationView["kind"], string>>;
  labelObstacles?: readonly { id: string; x: number; y: number; radius: number }[];
  revealingRelationIds?: ReadonlySet<string>;
  revealLowPerformance?: boolean;
}) {
  const idPrefix = useId().replaceAll(":", "");
  if (relations.length === 0 || width <= 0 || height <= 0) return null;

  // Labels are navigation aids, not canonical facts. Show at most one
  // representative label per visible visual family, plus a small number of
  // explicitly emphasized relations. Unknown/neutral and fusion relations
  // stay unlabeled until the product has truthful localized names for them.
  const labelIndexes = new Set<number>();
  const representedFamilies = new Set<StarRelationView["kind"]>();
  for (let index = 0; index < relations.length; index += 1) {
    const relation = relations[index]!;
    const labelKind = truthfulLabelKind(relation);
    if (labelKind && !representedFamilies.has(labelKind)) {
      labelIndexes.add(index);
      representedFamilies.add(labelKind);
    }
  }
  let emphasizedLabelCount = 0;
  for (let index = 0; index < relations.length && emphasizedLabelCount < 4; index += 1) {
    if (lensHints?.emphasizedRelationIds.has(relations[index]!.id)) {
      labelIndexes.add(index);
      emphasizedLabelCount += 1;
    }
  }

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
          <stop stopColor="var(--research-lane-1)" />
          <stop offset="1" stopColor="var(--research-lane-2)" />
        </linearGradient>
      </defs>
      {relations.map((relation, index) => {
        const pathId = `${idPrefix}-edge-${index}`;
        const path = quadraticEdgePath(relation.from, relation.to);
        const labelKind = truthfulLabelKind(relation);
        const revealing = revealingRelationIds?.has(relation.id) ?? false;
        return (
          <g key={relation.id}>
            <path
              id={pathId}
              data-testid={`star-graph-edge-${relation.id}`}
              data-kind={relation.kind}
              data-edge-type={relation.edgeType}
              className={cn(
                relationEdgeClass(relation.kind, relation.edgeType),
                lensHints?.dimmedRelationIds.has(relation.id) && "sg-lens-dim",
                lensHints?.emphasizedRelationIds.has(relation.id) && "sg-lens-emphasis",
                revealing && "sg-edge-expansion-base",
              )}
              d={path}
            />
            {revealing && !revealLowPerformance ? (
              <path
                data-testid={`star-graph-edge-reveal-${relation.id}`}
                className="sg-edge-expansion-trace"
                d={path}
                pathLength={1}
                style={{ animationDelay: `${Math.min(index, 6) * 34}ms` }}
              />
            ) : null}
            {labelKind &&
              labelIndexes.has(index) &&
              relationLabels?.[labelKind] &&
              isEdgeLabelClear(relation, labelObstacles) ? (
              <text className="sg-edge-label">
                <textPath href={`#${pathId}`} startOffset="50%" textAnchor="middle">
                  {relationLabels[labelKind]}
                </textPath>
              </text>
            ) : null}
          </g>
        );
      })}
    </svg>
  );
}

function truthfulLabelKind(
  relation: StarRelationView,
): StarRelationView["kind"] | null {
  const visualClass = relationEdgeClass(relation.kind, relation.edgeType);
  if (visualClass === "sg-edge-decompose") return "decompose";
  if (visualClass === "sg-edge-support") return "support";
  if (visualClass === "sg-edge-challenge") return "challenge";
  if (visualClass === "sg-edge-newdir") return "newdir";
  return null;
}
