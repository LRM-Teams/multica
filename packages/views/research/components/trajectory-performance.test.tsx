import { render } from "@testing-library/react";
import { describe, expect, it } from "vitest";
import {
  appendTrajectoryLayout,
  createTrajectoryStressFixture,
  getTrajectoryWindow,
  layoutTrajectoryCommits,
  reconcileTrajectoryInteraction,
  TRAJECTORY_PERFORMANCE_THRESHOLDS_MS,
  trajectoryDiagnostic,
  type TrajectoryInteractionState,
} from "./trajectory-performance-fixture";
import { TrajectoryVirtualWindow } from "./trajectory-virtual-window";

function timed<T>(work: () => T): { elapsedMs: number; value: T } {
  const start = performance.now();
  const value = work();
  return { elapsedMs: performance.now() - start, value };
}

function expectWithinThreshold(input: {
  elapsedMs: number;
  thresholdMs: number;
  nodeCount: number;
  laneCount: number;
  start: number;
  end: number;
}) {
  const diagnostic = trajectoryDiagnostic({
    nodeCount: input.nodeCount,
    laneCount: input.laneCount,
    elapsedMs: input.elapsedMs,
    window: { start: input.start, end: input.end },
  });
  expect(input.elapsedMs, diagnostic).toBeLessThan(input.thresholdMs);
}

describe("trajectory performance gate (LRM-1395)", () => {
  it("replays 96 nodes/180 sources with four active branches and a visible not-started member", () => {
    const fixture = createTrajectoryStressFixture({ commitCount: 500, laneCount: 12 });
    expect(fixture.nodes).toHaveLength(96);
    expect(fixture.sourceIds).toHaveLength(180);
    expect(new Set(fixture.nodes.map((node) => node.agentId))).toEqual(
      new Set(["agent-1", "agent-2", "agent-3", "agent-4"]),
    );
    expect(fixture.members).toContainEqual({
      id: "agent-5",
      name: "Agent 5",
      started: false,
    });
  });

  it.each([
    { commitCount: 500, laneCount: 12 },
    { commitCount: 2000, laneCount: 30 },
  ])("benchmarks deterministic initial layout for $commitCount commits/$laneCount lanes", ({ commitCount, laneCount }) => {
    const fixture = createTrajectoryStressFixture({ commitCount, laneCount });
    const measured = timed(() => layoutTrajectoryCommits(fixture.commits, fixture.laneIds));
    expect(measured.value).toHaveLength(commitCount);
    expect(new Set(measured.value.map((row) => row.id)).size).toBe(commitCount);
    expectWithinThreshold({
      elapsedMs: measured.elapsedMs,
      thresholdMs: TRAJECTORY_PERFORMANCE_THRESHOLDS_MS.initial2000,
      nodeCount: fixture.nodes.length,
      laneCount,
      start: 0,
      end: measured.value.length,
    });
  });

  it("benchmarks a 20-commit incremental append and scrolling the 2000-commit window", () => {
    const fixture = createTrajectoryStressFixture({ commitCount: 2020, laneCount: 30 });
    const base = fixture.commits.slice(0, 2000);
    const initialRows = layoutTrajectoryCommits(base, fixture.laneIds);
    const incremental = timed(() =>
      appendTrajectoryLayout(initialRows, fixture.commits.slice(2000), fixture.laneIds),
    );
    const allRows = [...initialRows, ...incremental.value];
    const scroll = timed(() => getTrajectoryWindow(allRows, 60_000, 720));

    expect(incremental.value).toHaveLength(20);
    expect(incremental.value[0]?.row).toBe(2000);
    expectWithinThreshold({
      elapsedMs: incremental.elapsedMs,
      thresholdMs: TRAJECTORY_PERFORMANCE_THRESHOLDS_MS.incremental20,
      nodeCount: fixture.nodes.length,
      laneCount: fixture.laneIds.length,
      start: 2000,
      end: 2020,
    });
    expectWithinThreshold({
      elapsedMs: scroll.elapsedMs,
      thresholdMs: TRAJECTORY_PERFORMANCE_THRESHOLDS_MS.scrollWindow,
      nodeCount: fixture.nodes.length,
      laneCount: fixture.laneIds.length,
      start: scroll.value.start,
      end: scroll.value.end,
    });
  });

  it("mounts only the viewport and overscan instead of all 2000 commit cards", () => {
    const fixture = createTrajectoryStressFixture({ commitCount: 2000, laneCount: 30 });
    const rows = layoutTrajectoryCommits(fixture.commits, fixture.laneIds);
    const { container, rerender } = render(
      <TrajectoryVirtualWindow rows={rows} scrollTop={0} viewportHeight={720} />,
    );
    expect(container.querySelectorAll("[data-commit-id]").length).toBeLessThanOrEqual(27);
    expect(container.querySelectorAll("[data-commit-id]").length).not.toBe(2000);

    rerender(<TrajectoryVirtualWindow rows={rows} scrollTop={60_000} viewportHeight={720} />);
    const cards = [...container.querySelectorAll<HTMLElement>("[data-commit-id]")];
    const segments = [...container.querySelectorAll<HTMLElement>("[data-segment]")];
    expect(cards.length).toBeLessThanOrEqual(27);
    expect(new Set(cards.map((card) => card.dataset.commitId)).size).toBe(cards.length);
    expect(segments.length).toBeLessThanOrEqual(cards.length * 2);
    expect(new Set(segments.map((segment) => segment.dataset.segment)).size).toBe(
      segments.length,
    );
  });

  it("clears stale selection through rapid filter/zoom/checkout and retains bounded window state", () => {
    const fixture = createTrajectoryStressFixture({ commitCount: 2000, laneCount: 30 });
    const rows = layoutTrajectoryCommits(fixture.commits, fixture.laneIds);
    let state: TrajectoryInteractionState = {
      filter: "",
      zoom: 1,
      checkoutId: "commit-1",
      selectedId: "commit-1",
    };
    const retainedWindows = new Map<string, readonly string[]>();

    for (let index = 0; index < 1000; index += 1) {
      const window = getTrajectoryWindow(rows, (index * 997) % 90_000, 720);
      const ids = new Set(window.rows.map((row) => row.id));
      const checkoutId = window.rows[index % window.rows.length]?.id ?? null;
      state = reconcileTrajectoryInteraction(state, ids, {
        filter: index % 2 === 0 ? "agent-1" : "",
        zoom: 0.5 + (index % 16) / 10,
        checkoutId,
        selectedId: index % 3 === 0 ? "stale-selection" : checkoutId,
      });
      retainedWindows.clear();
      retainedWindows.set(`${window.start}:${window.end}`, [...ids]);
      expect(state.selectedId === null || ids.has(state.selectedId)).toBe(true);
      expect(state.checkoutId === null || ids.has(state.checkoutId)).toBe(true);
    }

    expect(retainedWindows.size).toBe(1);
    expect(retainedWindows.values().next().value?.length).toBeLessThanOrEqual(27);
    expect(state.zoom).toBeGreaterThanOrEqual(0.5);
    expect(state.zoom).toBeLessThanOrEqual(2);
  });

  it("includes node/lane/elapsed/window details in failure diagnostics", () => {
    expect(
      trajectoryDiagnostic({
        nodeCount: 96,
        laneCount: 30,
        elapsedMs: 12.345,
        window: { start: 120, end: 147 },
      }),
    ).toBe("nodes=96 lanes=30 elapsed=12.35ms window=120..147");
  });
});
