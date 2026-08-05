import type { TrajectoryLayoutRow } from "./trajectory-performance-fixture";
import {
  getTrajectoryWindow,
  TRAJECTORY_ROW_HEIGHT,
} from "./trajectory-performance-fixture";

export function TrajectoryVirtualWindow({
  rows,
  scrollTop,
  viewportHeight,
}: {
  rows: readonly TrajectoryLayoutRow[];
  scrollTop: number;
  viewportHeight: number;
}) {
  const window = getTrajectoryWindow(rows, scrollTop, viewportHeight);
  return (
    <div
      data-testid="trajectory-virtual-window"
      data-window={`${window.start}:${window.end}`}
      style={{ height: viewportHeight, overflow: "hidden", position: "relative" }}
    >
      <div style={{ height: rows.length * TRAJECTORY_ROW_HEIGHT }}>
        {window.rows.map((commit) => (
          <article
            aria-label={`${commit.title}, ${commit.agentId}, ${commit.laneId}`}
            data-commit-id={commit.id}
            data-lane={commit.lane}
            key={commit.id}
            style={{
              height: TRAJECTORY_ROW_HEIGHT,
              position: "absolute",
              top: commit.row * TRAJECTORY_ROW_HEIGHT,
            }}
          >
            {commit.parentIds.map((parentId) => (
              <span
                aria-hidden="true"
                data-segment={`${parentId}:${commit.id}`}
                key={`${parentId}:${commit.id}`}
              />
            ))}
            {commit.title}
          </article>
        ))}
      </div>
    </div>
  );
}
