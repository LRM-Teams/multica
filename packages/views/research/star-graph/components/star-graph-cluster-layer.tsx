"use client";

import type { StarGraphLayoutCluster } from "@multica/core/research";

export function StarGraphClusterLayer({
  clusters,
  clusterLabels,
  hiddenCounts,
  hiddenCountLabel,
}: {
  clusters: readonly StarGraphLayoutCluster[];
  clusterLabels?: ReadonlyMap<string, string>;
  hiddenCounts?: ReadonlyMap<string, number>;
  hiddenCountLabel?: (count: number) => string;
}) {
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
          <span className="sg-cluster-label">
            {clusterLabels?.get(cluster.clusterId) ?? cluster.clusterId}
          </span>
          {hiddenCounts?.get(cluster.clusterId) ? (
            <span
              data-testid={`star-graph-cluster-hidden-${cluster.clusterId}`}
              className="sg-cluster-hidden-badge"
            >
              {hiddenCountLabel
                ? hiddenCountLabel(hiddenCounts.get(cluster.clusterId)!)
                : `+${hiddenCounts.get(cluster.clusterId)}`}
            </span>
          ) : null}
        </div>
      ))}
    </div>
  );
}
