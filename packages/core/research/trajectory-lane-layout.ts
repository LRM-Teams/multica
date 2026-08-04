export interface TrajectoryCommit { id: string; branchKey: string; parentIds: readonly string[]; status: string; label?: string }
export type TrajectorySegmentRelation = "main" | "branch" | "merge" | "abandoned";
export interface TrajectoryLayoutCommit { id: string; row: number; lane: number; branchKey: string; colorSlot: number; status: string; label: { text: string; branchKey: string; status: string; visible: true } }
export interface TrajectorySegment { id: string; fromCommitId: string; toCommitId: string; parentIndex: number; from: { row: number; lane: number }; to: { row: number; lane: number }; relation: TrajectorySegmentRelation; lineStyle: "solid" | "dashed"; accessibleRole: "main path" | "branch path" | "merge parent" | "abandoned path" }
export interface TrajectoryLaneLayout {
  commits: readonly TrajectoryLayoutCommit[];
  lanes: readonly { branchKey: string; lane: number; colorSlot: number; accessibleLabel: string }[];
  segments: readonly TrajectorySegment[];
  junctions: readonly { commitId: string; row: number; lane: number; parentSegmentIds: readonly string[]; accessibleRole: "merge junction" }[];
  issues: readonly { code: "missing_parent"; commitId: string; parentId: string; parentIndex: number }[];
  rowCount: number;
}
interface State { commitById: Map<string, TrajectoryLayoutCommit>; sourceById: Map<string, TrajectoryCommit>; laneByBranch: Map<string, TrajectoryLaneLayout["lanes"][number]> }
const states = new WeakMap<TrajectoryLaneLayout, State>();

export function trajectoryColorSlot(branchKey: string): number {
  let hash = 0x811c9dc5;
  for (let i = 0; i < branchKey.length; i += 1) { hash ^= branchKey.charCodeAt(i); hash = Math.imul(hash, 0x01000193); }
  return (hash >>> 0) % 12;
}
function empty(): TrajectoryLaneLayout {
  const result: TrajectoryLaneLayout = { commits: [], lanes: [], segments: [], junctions: [], issues: [], rowCount: 0 };
  states.set(result, { commitById: new Map(), sourceById: new Map(), laneByBranch: new Map() });
  return result;
}
function semantics(parent: TrajectoryCommit, child: TrajectoryCommit, index: number) {
  if (index > 0) return { relation: "merge", lineStyle: "solid", accessibleRole: "merge parent" } as const;
  if (["abandoned", "dead_end"].includes(parent.status) || ["abandoned", "dead_end"].includes(child.status)) return { relation: "abandoned", lineStyle: "dashed", accessibleRole: "abandoned path" } as const;
  if (parent.branchKey === child.branchKey) return { relation: "main", lineStyle: "solid", accessibleRole: "main path" } as const;
  return { relation: "branch", lineStyle: "solid", accessibleRole: "branch path" } as const;
}
export function buildTrajectoryLaneLayout(input: readonly TrajectoryCommit[]): TrajectoryLaneLayout { return appendTrajectoryCommits(empty(), input); }

/** Appends only the new suffix; historical row/lane geometry is reused verbatim. */
export function appendTrajectoryCommits(previous: TrajectoryLaneLayout, input: readonly TrajectoryCommit[]): TrajectoryLaneLayout {
  if (input.length === 0) return previous;
  const prior = states.get(previous);
  if (!prior) throw new Error("trajectory layout was not created by this module");
  const commitById = new Map(prior.commitById), sourceById = new Map(prior.sourceById), laneByBranch = new Map(prior.laneByBranch);
  const commits = [...previous.commits], lanes = [...previous.lanes], segments = [...previous.segments], junctions = [...previous.junctions], issues = [...previous.issues];
  for (const source of input) {
    if (commitById.has(source.id)) throw new Error(`duplicate trajectory commit id: ${source.id}`);
    let lane = laneByBranch.get(source.branchKey);
    if (!lane) {
      lane = { branchKey: source.branchKey, lane: lanes.length, colorSlot: trajectoryColorSlot(source.branchKey), accessibleLabel: source.branchKey };
      lanes.push(lane); laneByBranch.set(source.branchKey, lane);
    }
    const commit: TrajectoryLayoutCommit = { id: source.id, row: commits.length, lane: lane.lane, branchKey: source.branchKey, colorSlot: lane.colorSlot, status: source.status, label: { text: source.label?.trim() || source.branchKey, branchKey: source.branchKey, status: source.status, visible: true } };
    const parentSegmentIds: string[] = [];
    source.parentIds.forEach((parentId, parentIndex) => {
      const parent = commitById.get(parentId), parentSource = sourceById.get(parentId);
      if (!parent || !parentSource) { issues.push({ code: "missing_parent", commitId: source.id, parentId, parentIndex }); return; }
      const id = `${parentId}->${source.id}:${parentIndex}`;
      segments.push({ id, fromCommitId: parentId, toCommitId: source.id, parentIndex, from: { row: parent.row, lane: parent.lane }, to: { row: commit.row, lane: commit.lane }, ...semantics(parentSource, source, parentIndex) });
      parentSegmentIds.push(id);
    });
    if (source.parentIds.length > 1) junctions.push({ commitId: source.id, row: commit.row, lane: commit.lane, parentSegmentIds, accessibleRole: "merge junction" });
    commits.push(commit); commitById.set(source.id, commit); sourceById.set(source.id, source);
  }
  const result: TrajectoryLaneLayout = { commits, lanes, segments, junctions, issues, rowCount: commits.length };
  states.set(result, { commitById, sourceById, laneByBranch });
  return result;
}
export function sliceTrajectoryLaneLayout(layout: TrajectoryLaneLayout, window: { startRow: number; endRow: number; overscan?: number }): TrajectoryLaneLayout {
  const overscan = Math.max(0, Math.floor(window.overscan ?? 0)), start = Math.max(0, Math.floor(window.startRow) - overscan), end = Math.max(start, Math.floor(window.endRow) + overscan);
  const commits = layout.commits.slice(start, Math.min(layout.rowCount, end + 1)), ids = new Set(commits.map((c) => c.id)), laneIds = new Set(commits.map((c) => c.lane));
  return { commits, lanes: layout.lanes.filter((l) => laneIds.has(l.lane)), segments: layout.segments.filter((s) => s.to.row >= start && s.from.row <= end), junctions: layout.junctions.filter((j) => ids.has(j.commitId)), issues: layout.issues.filter((i) => ids.has(i.commitId)), rowCount: layout.rowCount };
}
