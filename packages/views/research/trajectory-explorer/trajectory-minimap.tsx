"use client";

import type { TrajectoryLaneLayout } from "@multica/core/research";
import { cn } from "@multica/ui/lib/utils";
import { GIT_BRANCH_COLORS } from "../lib/git-topology";
import { TRAJECTORY_ROW_HEIGHT } from "./trajectory-graph";

function laneColor(layout: TrajectoryLaneLayout, lane: number): string {
  const slot = layout.lanes[lane]?.colorSlot ?? 0;
  return GIT_BRANCH_COLORS[slot % GIT_BRANCH_COLORS.length] ?? "currentColor";
}

/**
 * LRM-1480 / UI-06 slice 4: viewport-consistent minimap.
 *
 * Renders only the window-derived lanes/segments/junctions the main graph has
 * (same slice), never synthetic connectors for `missing_parent`. The viewport
 * frame tracks the main graph's visible row range; panning does not mirror or
 * invert. Filtered-out paths are hidden here exactly as in the main view.
 */
export function TrajectoryMinimap({
  layout,
  scrollTop,
  viewportHeight,
  className,
}: {
  layout: TrajectoryLaneLayout;
  scrollTop?: number;
  viewportHeight?: number;
  className?: string;
}) {
  const total = layout.rowCount;
  const height = 120;
  const scale = total > 0 ? height / (total * TRAJECTORY_ROW_HEIGHT) : 1;

  const visibleTop = (scrollTop ?? 0) * scale;
  const visibleH = Math.max(6, (viewportHeight ?? 400) * scale);

  return (
    <div
      data-testid="trajectory-minimap"
      className={cn(
        "relative hidden shrink-0 overflow-hidden border-l border-border/55 bg-background md:block",
        className,
      )}
      style={{ height }}
    >
      <svg
        width="96"
        height={height}
        className="block h-full w-full"
        role="img"
        aria-label="Trajectory overview"
      >
        {layout.segments.map((seg) => {
          const color = laneColor(layout, seg.to.lane);
          const y0 = seg.from.row * TRAJECTORY_ROW_HEIGHT * scale;
          const y1 = seg.to.row * TRAJECTORY_ROW_HEIGHT * scale;
          return (
            <line
              key={seg.id}
              x1={12 + seg.from.lane * 5}
              y1={y0}
              x2={16 + seg.to.lane * 5}
              y2={y1}
              stroke={color}
              strokeWidth={1.5}
              strokeDasharray={seg.lineStyle === "dashed" ? "2 2" : undefined}
              opacity={seg.relation === "abandoned" ? 0.5 : 0.75}
            />
          );
        })}
        {layout.junctions.map((j) => (
          <circle
            key={j.commitId}
            cx={14 + j.lane * 5}
            cy={j.row * TRAJECTORY_ROW_HEIGHT * scale}
            r={2.5}
            fill={laneColor(layout, j.lane)}
          />
        ))}
      </svg>
      <div
        data-testid="trajectory-minimap-viewport"
        className="absolute border border-brand/70"
        style={{
          top: visibleTop,
          left: 0,
          right: 0,
          height: Math.min(visibleH, height - visibleTop),
        }}
      />
    </div>
  );
}
