export type TrajectoryFixtureMember = {
  id: string;
  name: string;
  started: boolean;
};

export type TrajectoryFixtureNode = {
  id: string;
  agentId: string;
  sourceIds: string[];
};

export type TrajectoryCommit = {
  id: string;
  laneId: string;
  parentIds: string[];
  agentId: string;
  title: string;
};

export type TrajectoryStressFixture = {
  seed: number;
  members: TrajectoryFixtureMember[];
  nodes: TrajectoryFixtureNode[];
  sourceIds: string[];
  commits: TrajectoryCommit[];
  laneIds: string[];
};

export type TrajectoryLayoutRow = TrajectoryCommit & {
  row: number;
  lane: number;
};

export type TrajectoryWindow = {
  start: number;
  end: number;
  rows: TrajectoryLayoutRow[];
};

export type TrajectoryInteractionState = {
  filter: string;
  zoom: number;
  checkoutId: string | null;
  selectedId: string | null;
};

export const TRAJECTORY_PERFORMANCE_THRESHOLDS_MS = {
  initial2000: 250,
  incremental20: 100,
  scrollWindow: 50,
} as const;

export const TRAJECTORY_ROW_HEIGHT = 48;
export const TRAJECTORY_OVERSCAN_ROWS = 6;

function mulberry32(seed: number): () => number {
  let value = seed;
  return () => {
    value |= 0;
    value = (value + 0x6d2b79f5) | 0;
    let mixed = Math.imul(value ^ (value >>> 15), 1 | value);
    mixed = (mixed + Math.imul(mixed ^ (mixed >>> 7), 61 | mixed)) ^ mixed;
    return ((mixed ^ (mixed >>> 14)) >>> 0) / 4294967296;
  };
}

/**
 * Stable, production-shaped data for the trajectory performance gate. The
 * session half deliberately mirrors the acceptance recording: 96 research
 * nodes, 180 sources, four active agents and one visible not-started member.
 */
export function createTrajectoryStressFixture(options: {
  commitCount: 500 | 2000 | number;
  laneCount: 12 | 30 | number;
  seed?: number;
}): TrajectoryStressFixture {
  const { commitCount, laneCount, seed = 1395 } = options;
  if (commitCount < 1 || laneCount < 1) {
    throw new Error("trajectory fixture counts must be positive");
  }

  const random = mulberry32(seed);
  const members = Array.from({ length: 5 }, (_, index) => ({
    id: `agent-${index + 1}`,
    name: `Agent ${index + 1}`,
    started: index < 4,
  }));
  const sourceIds = Array.from({ length: 180 }, (_, index) => `source-${index + 1}`);
  const nodes = Array.from({ length: 96 }, (_, index) => {
    const sourceStart = (index * 7) % sourceIds.length;
    return {
      id: `node-${index + 1}`,
      agentId: members[index % 4]!.id,
      sourceIds: [
        sourceIds[sourceStart]!,
        sourceIds[(sourceStart + 1) % sourceIds.length]!,
      ],
    };
  });
  const laneIds = Array.from({ length: laneCount }, (_, index) => `lane-${index + 1}`);
  const latestByLane = new Map<string, string>();
  const commits = Array.from({ length: commitCount }, (_, index) => {
    const laneIndex = index % laneCount;
    const laneId = laneIds[laneIndex]!;
    const id = `commit-${index + 1}`;
    const parentIds: string[] = [];
    const sameLaneParent = latestByLane.get(laneId);
    if (sameLaneParent) parentIds.push(sameLaneParent);

    // A deterministic cross-lane parent every 11 commits creates frequent,
    // repeatable merges without turning the fixture into a random snapshot.
    if (index > laneCount && index % 11 === 0) {
      const mergeLane = laneIds[(laneIndex + 1 + Math.floor(random() * (laneCount - 1))) % laneCount]!;
      const mergeParent = latestByLane.get(mergeLane);
      if (mergeParent && mergeParent !== sameLaneParent) parentIds.push(mergeParent);
    }

    latestByLane.set(laneId, id);
    return {
      id,
      laneId,
      parentIds,
      agentId: members[laneIndex % 4]!.id,
      title: `Trajectory commit ${index + 1}`,
    };
  });

  return { seed, members, nodes, sourceIds, commits, laneIds };
}

export function layoutTrajectoryCommits(
  commits: readonly TrajectoryCommit[],
  laneIds: readonly string[],
): TrajectoryLayoutRow[] {
  const laneIndex = new Map(laneIds.map((id, index) => [id, index]));
  return commits.map((commit, row) => {
    const lane = laneIndex.get(commit.laneId);
    if (lane === undefined) throw new Error(`unknown trajectory lane: ${commit.laneId}`);
    return { ...commit, row, lane };
  });
}

export function appendTrajectoryLayout(
  existingRows: readonly TrajectoryLayoutRow[],
  commits: readonly TrajectoryCommit[],
  laneIds: readonly string[],
): TrajectoryLayoutRow[] {
  const laneIndex = new Map(laneIds.map((id, index) => [id, index]));
  const offset = existingRows.length;
  return commits.map((commit, index) => {
    const lane = laneIndex.get(commit.laneId);
    if (lane === undefined) throw new Error(`unknown trajectory lane: ${commit.laneId}`);
    return { ...commit, row: offset + index, lane };
  });
}

export function getTrajectoryWindow(
  rows: readonly TrajectoryLayoutRow[],
  scrollTop: number,
  viewportHeight: number,
  overscan = TRAJECTORY_OVERSCAN_ROWS,
): TrajectoryWindow {
  const start = Math.max(0, Math.floor(scrollTop / TRAJECTORY_ROW_HEIGHT) - overscan);
  const visibleCount = Math.ceil(viewportHeight / TRAJECTORY_ROW_HEIGHT);
  const end = Math.min(rows.length, start + visibleCount + overscan * 2);
  return { start, end, rows: rows.slice(start, end) };
}

export function reconcileTrajectoryInteraction(
  state: TrajectoryInteractionState,
  visibleCommitIds: ReadonlySet<string>,
  patch: Partial<TrajectoryInteractionState>,
): TrajectoryInteractionState {
  const next = { ...state, ...patch };
  const checkoutId = next.checkoutId && visibleCommitIds.has(next.checkoutId)
    ? next.checkoutId
    : null;
  const selectedId = next.selectedId && visibleCommitIds.has(next.selectedId)
    ? next.selectedId
    : checkoutId;
  return {
    ...next,
    zoom: Math.min(2, Math.max(0.5, next.zoom)),
    checkoutId,
    selectedId,
  };
}

export function trajectoryDiagnostic(input: {
  nodeCount: number;
  laneCount: number;
  elapsedMs: number;
  window: Pick<TrajectoryWindow, "start" | "end">;
}): string {
  return `nodes=${input.nodeCount} lanes=${input.laneCount} elapsed=${input.elapsedMs.toFixed(2)}ms window=${input.window.start}..${input.window.end}`;
}
