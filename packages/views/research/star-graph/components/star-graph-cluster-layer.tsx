"use client";

import type { StarGraphLayoutCluster } from "@multica/core/research";
import type { StarEntityView } from "../lib/star-canvas-view-model";

export function StarGraphClusterLayer({
  clusters,
  entities,
  rootId,
  newFrontierLabel,
}: {
  clusters: readonly StarGraphLayoutCluster[];
  entities: readonly StarEntityView[];
  rootId: string | null;
  newFrontierLabel?: string;
}) {
  const newFrontier = computeNewFrontierZone(entities, rootId);

  return (
    <div data-testid="star-graph-clusters" className="pointer-events-none absolute inset-0">
      {clusters.map((cluster) => (
        <div
          key={cluster.clusterId}
          data-testid={`star-graph-cluster-${cluster.clusterId}`}
          className="sg-cluster-boundary"
          style={{
            left: cluster.x - cluster.radius,
            top: cluster.y - cluster.radius,
            width: cluster.radius * 2,
            height: cluster.radius * 2,
          }}
        >
          <span className="sg-cluster-label">{cluster.clusterId}</span>
        </div>
      ))}
      {newFrontier && (
        <div
          data-testid="star-graph-new-frontier"
          className="sg-new-frontier-zone"
          style={{
            left: newFrontier.x,
            top: newFrontier.y,
            width: newFrontier.width,
            height: newFrontier.height,
          }}
        >
          {newFrontierLabel ? (
            <span className="sg-new-frontier-label">{newFrontierLabel}</span>
          ) : null}
        </div>
      )}
    </div>
  );
}

function computeNewFrontierZone(
  entities: readonly StarEntityView[],
  rootId: string | null,
): { x: number; y: number; width: number; height: number } | null {
  const members = entities.filter(
    (entity) =>
      entity.id !== rootId &&
      (entity.clusterId == null || entity.clusterId === "") &&
      entity.tier !== "xxl",
  );
  if (members.length === 0) return null;

  let minX = Infinity;
  let minY = Infinity;
  let maxX = -Infinity;
  let maxY = -Infinity;
  for (const entity of members) {
    minX = Math.min(minX, entity.x - entity.radius);
    minY = Math.min(minY, entity.y - entity.radius);
    maxX = Math.max(maxX, entity.x + entity.radius);
    maxY = Math.max(maxY, entity.y + entity.radius);
  }

  const pad = 28;
  return {
    x: minX - pad,
    y: minY - pad,
    width: maxX - minX + pad * 2,
    height: maxY - minY + pad * 2,
  };
}
