"use client";

import { useMemo, useState, type ReactElement } from "react";
import type {
  TrajectoryLaneLayout,
  TrajectoryLayoutCommit,
} from "@multica/core/research";
import { sliceTrajectoryLaneLayout } from "@multica/core/research";
import { cn } from "@multica/ui/lib/utils";
import { GIT_BRANCH_COLORS } from "../lib/git-topology";
import { TrajectoryCommitCard } from "./trajectory-commit-card";

/** Row height in CSS px. Fixed so scrollTop maps 1:1 to row math. */
export const TRAJECTORY_ROW_HEIGHT = 56;
/** Rows rendered above/below the viewport window (overscan). */
export const TRAJECTORY_OVERSCAN = 6;

/**
 * Virtualized Git multi-lane graph body (LRM-1480 / UI-06 slice 2).
 *
 * Only the visible window (+ overscan) is mounted as DOM; the source layout
 * may hold 10k rows without building nodes for them. Lane lines that flow
 * between cards are drawn as a single per-window SVG gutter so a dense graph
 * does not nest hundreds of connectors in the card DOM (LRM-1394 no-overlap:
 * connectors never cross card body/status/agent label).
 */
export function TrajectoryGraph({
  layout,
  selectedId,
  onSelect,
  onOpenDetail,
  className,
}: {
  layout: TrajectoryLaneLayout;
  selectedId: string | null;
  onSelect: (id: string) => void;
  onOpenDetail: (id: string) => void;
  className?: string;
}) {
  const [scrollTop, setScrollTop] = useState(0);
  const [viewportHeight, setViewportHeight] = useState(400);

  const totalHeight = layout.rowCount * TRAJECTORY_ROW_HEIGHT;

  const win = useMemo(() => {
    const first = Math.floor(scrollTop / TRAJECTORY_ROW_HEIGHT);
    const last = Math.floor((scrollTop + viewportHeight) / TRAJECTORY_ROW_HEIGHT);
    return sliceTrajectoryLaneLayout(layout, {
      startRow: first,
      endRow: last,
      overscan: TRAJECTORY_OVERSCAN,
    });
  }, [layout, scrollTop, viewportHeight]);

  return (
    <section
      ref={(el) => {
        if (el) setViewportHeight(el.clientHeight || 400);
      }}
      data-testid="trajectory-graph"
      data-window-rows={`${win.commits.length}/${layout.rowCount}`}
      className={cn("relative overflow-y-auto outline-none", className)}
      onScroll={(e) => setScrollTop(e.currentTarget.scrollTop)}
      aria-label="Exploration trajectory graph"
    >
      <div style={{ height: totalHeight, position: "relative" }}>
        <TrajectorySegmentLayer layout={win} />
        {win.commits.map((commit) => (
          <div
            key={commit.id}
            style={{
              position: "absolute",
              top: commit.row * TRAJECTORY_ROW_HEIGHT,
              left: 0,
              right: 0,
              height: TRAJECTORY_ROW_HEIGHT,
              padding: "0 10px 6px",
            }}
          >
            <TrajectoryCommitCard
              layout={win}
              commit={commit}
              selected={selectedId === commit.id}
              onSelect={onSelect}
              onOpenDetail={onOpenDetail}
            />
          </div>
        ))}
      </div>
    </section>
  );
}

/** Draws connecting lane lines for the visible window only. */
function TrajectorySegmentLayer({ layout }: { layout: TrajectoryLaneLayout }) {
  const commits = layout.commits;
  const byId = new Map<string, TrajectoryLayoutCommit>();
  for (const c of commits) byId.set(c.id, c);

  const paths: ReactElement[] = [];
  for (const seg of layout.segments) {
    const from = byId.get(seg.fromCommitId);
    const to = byId.get(seg.toCommitId);
    if (!from || !to) continue;
    const x0 = laneX(from.lane);
    const x1 = laneX(to.lane);
    const y0 = from.row * TRAJECTORY_ROW_HEIGHT + TRAJECTORY_ROW_HEIGHT / 2;
    const y1 = to.row * TRAJECTORY_ROW_HEIGHT + TRAJECTORY_ROW_HEIGHT / 2;
    // Orthogonal connector that never passes through card body (gutter only).
    const midY = (y0 + y1) / 2;
    const d = `M ${x0} ${y0} C ${x0} ${midY}, ${x1} ${midY}, ${x1} ${y1}`;
    const color =
      GIT_BRANCH_COLORS[to.colorSlot % GIT_BRANCH_COLORS.length] ??
      "currentColor";
    paths.push(
      <path
        key={seg.id}
        data-segment-id={seg.id}
        data-relation={seg.relation}
        d={d}
        fill="none"
        stroke={color}
        strokeWidth={seg.relation === "abandoned" ? 1 : 1.5}
        strokeDasharray={seg.lineStyle === "dashed" ? "3 3" : undefined}
        opacity={seg.relation === "abandoned" ? 0.55 : 0.8}
      />,
    );
  }

  return (
    <svg
      aria-hidden="true"
      className="pointer-events-none absolute inset-0 h-full w-full"
      style={{ overflow: "visible" }}
    >
      {paths}
    </svg>
  );
}

function laneX(lane: number): number {
  // Center lane line within the card gutter; 12px inset keeps the line off the
  // card edge so it does not cross status badges or agent labels.
  return 10 + lane * 8 + 4;
}
