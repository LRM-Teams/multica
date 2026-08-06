import { describe, expect, it } from "vitest";
import { appendTrajectoryCommits, buildTrajectoryLaneLayout, sliceTrajectoryLaneLayout, type TrajectoryCommit } from "./trajectory-lane-layout";
const c = (id: string, branchKey: string, parentIds: string[] = [], status = "completed"): TrajectoryCommit => ({ id, branchKey, parentIds, status, label: id });

describe("trajectory lane layout", () => {
  it.each([2, 5, 12])("keeps %i branches deterministic and distinct", (count) => {
    const input = [c("root", "main"), ...Array.from({ length: count }, (_, i) => c(`b${i}`, `agent:${i}`, ["root"]))];
    const layout = buildTrajectoryLaneLayout(input);
    expect(buildTrajectoryLaneLayout(input)).toEqual(layout);
    expect(layout.commits.map((item) => item.lane)).toEqual([0, ...Array.from({ length: count }, (_, i) => i + 1)]);
    expect(layout.commits.every((item) => item.label.visible)).toBe(true);
  });
  it("models crossings, repeated merges, and non-color semantics", () => {
    const layout = buildTrajectoryLaneLayout([
      c("root", "main"), c("a", "a", ["root"]), c("b", "b", ["root"]),
      c("cross-a", "a", ["b"]), c("cross-b", "b", ["a"]),
      c("m1", "main", ["cross-a", "cross-b"]), c("dead", "dead", ["m1"], "dead_end"),
      c("m2", "main", ["m1", "dead"]),
    ]);
    expect(layout.junctions.map((item) => item.commitId)).toEqual(["m1", "m2"]);
    expect(layout.segments.filter((item) => item.relation === "merge")).toHaveLength(2);
    expect(layout.segments.find((item) => item.toCommitId === "dead")).toMatchObject({ relation: "abandoned", lineStyle: "dashed", accessibleRole: "abandoned path" });
  });
  it("does not move history on append and keeps branch colors stable", () => {
    const initial = buildTrajectoryLaneLayout([c("root", "main"), c("a", "agent:a", ["root"])]);
    const before = initial.commits.map(({ id, row, lane, colorSlot }) => ({ id, row, lane, colorSlot }));
    const next = appendTrajectoryCommits(initial, [c("a2", "agent:a", ["a"]), c("b", "agent:b", ["a2"])]);
    expect(next.commits.slice(0, 2).map(({ id, row, lane, colorSlot }) => ({ id, row, lane, colorSlot }))).toEqual(before);
    expect(next.commits[2]?.lane).toBe(1);
    expect(buildTrajectoryLaneLayout([c("x", "agent:a")]).commits[0]?.colorSlot).toBe(next.commits[2]?.colorSlot);
  });
  it("reports missing parents, windows rows, and handles 2000 commits linearly", () => {
    const missing = buildTrajectoryLaneLayout([c("orphan", "a", ["missing"])]);
    expect(missing.segments).toEqual([]);
    expect(missing.issues[0]).toMatchObject({ code: "missing_parent", parentId: "missing" });
    const fixture = (size: number) => Array.from({ length: size }, (_, i) => c(`c${i}`, `agent:${i % 12}`, i ? [`c${i - 1}`] : [], i % 97 ? "completed" : "dead_end"));
    const start500 = performance.now(); buildTrajectoryLaneLayout(fixture(500)); const ms500 = performance.now() - start500;
    const start2000 = performance.now(); const large = buildTrajectoryLaneLayout(fixture(2_000)); const ms2000 = performance.now() - start2000;
    const startAppend = performance.now(); appendTrajectoryCommits(large, [c("tail", "tail", ["c1999"], "active")]); const msAppend = performance.now() - startAppend;
    expect(sliceTrajectoryLaneLayout(large, { startRow: 10, endRow: 15, overscan: 1 }).commits.map((item) => item.row)).toEqual([9, 10, 11, 12, 13, 14, 15, 16]);
    expect(ms500).toBeLessThan(250); expect(ms2000).toBeLessThan(1_000); expect(msAppend).toBeLessThan(100);
    console.info(`[trajectory-lane-layout] 500=${ms500.toFixed(2)}ms 2000=${ms2000.toFixed(2)}ms append=${msAppend.toFixed(2)}ms`);
  });
});
