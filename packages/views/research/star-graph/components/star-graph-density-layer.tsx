import type { CSSProperties } from "react";

export interface StarGraphDensityBin {
  id: string;
  bounds: { x: number; y: number; width: number; height: number };
  total: number;
  execution_counts: Readonly<Record<string, number>>;
}

interface DensityStyle extends CSSProperties {
  "--sg-density-inverse-scale": number;
}

export function StarGraphDensityLayer({
  bins,
  zoom,
}: {
  bins: readonly StarGraphDensityBin[];
  zoom: number;
}) {
  if (zoom > 0.62 || bins.length === 0) return null;
  const inverseScale = 1 / Math.max(zoom, 0.18);
  return (
    <div
      className="sg-density-layer pointer-events-none absolute inset-0"
      data-testid="star-graph-density-layer"
      aria-hidden="true"
    >
      {bins.map((bin) => {
        const status = dominantExecutionStatus(bin.execution_counts);
        return (
          <div
            key={bin.id}
            className="sg-density-bin"
            data-execution={status}
            data-testid={`star-graph-density-${bin.id}`}
            style={
              {
                left: bin.bounds.x,
                top: bin.bounds.y,
                width: bin.bounds.width,
                height: bin.bounds.height,
                "--sg-density-inverse-scale": inverseScale,
              } satisfies DensityStyle
            }
          >
            <span className="sg-density-count">{bin.total}</span>
          </div>
        );
      })}
    </div>
  );
}

export function dominantExecutionStatus(
  counts: Readonly<Record<string, number>>,
): "running" | "failed" | "pending" | "succeeded" | "mixed" {
  const ranked = Object.entries(counts)
    .filter(([, count]) => Number.isFinite(count) && count > 0)
    .sort((left, right) => right[1] - left[1]);
  if (ranked.length === 0 || (ranked[1] && ranked[1][1] === ranked[0]?.[1])) {
    return "mixed";
  }
  const status = ranked[0]?.[0];
  if (status === "running") return "running";
  if (status === "failed" || status === "lost") return "failed";
  if (status === "succeeded") return "succeeded";
  return "pending";
}
