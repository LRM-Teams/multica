import { beforeEach, describe, expect, it } from "vitest";
import { useResearchV6ProjectionStore } from "./store";
import { researchV6FixtureDelta, researchV6FixtureSnapshot } from "../../api/research-v6-fixtures";

describe("useResearchV6ProjectionStore", () => {
  beforeEach(() => {
    useResearchV6ProjectionStore.setState({ runs: {}, clients: {} });
  });

  it("seeds a run from a snapshot and exposes its projection slice", () => {
    const snapshot = researchV6FixtureSnapshot();
    useResearchV6ProjectionStore.getState().applySnapshot("run-1", snapshot);

    const store = useResearchV6ProjectionStore.getState();
    const run = store.runs["run-1"];
    expect(run).toBeDefined();
    expect(run!.lastConfirmedSequence).toBe(2);
    expect(run!.nodes.size).toBe(3);
    expect(run!.edges.size).toBe(2);
    expect(run!.disconnected).toBe(false);
  });

  it("applies deltas and advances the cursor (idempotent on duplicate)", () => {
    const s = useResearchV6ProjectionStore.getState();
    s.applySnapshot("run-1", researchV6FixtureSnapshot());

    const delta = researchV6FixtureDelta();
    s.applyDelta("run-1", delta);
    const runAfter = useResearchV6ProjectionStore.getState().runs["run-1"]!;
    expect(runAfter.lastConfirmedSequence).toBe(4);
    expect(runAfter.nodes.size).toBe(5);
    expect(runAfter.edges.size).toBe(4);
    expect(runAfter.pendingDeltaCount).toBe(0);

    // Duplicate delta is dropped — node/edge counts unchanged.
    useResearchV6ProjectionStore.getState().applyDelta("run-1", delta);
    const afterDup = useResearchV6ProjectionStore.getState().runs["run-1"]!;
    expect(afterDup.lastConfirmedSequence).toBe(4);
    expect(afterDup.nodes.size).toBe(5);
    expect(afterDup.edges.size).toBe(4);
  });

  it("buffers an out-of-order delta until the middleware fills", () => {
    const s = useResearchV6ProjectionStore.getState();
    s.applySnapshot("run-1", researchV6FixtureSnapshot());

    // Delta [4,5) arrives first — gap ahead.
    s.applyDelta("run-1", {
      ...researchV6FixtureDelta(),
      from_sequence_exclusive: 4,
      through_sequence: 5,
    });
    let run = useResearchV6ProjectionStore.getState().runs["run-1"]!;
    expect(run.pendingDeltaCount).toBe(1);
    expect(run.lastConfirmedSequence).toBe(2);

    // The missing [2,4) arrives — drains and commits in order.
    s.applyDelta("run-1", researchV6FixtureDelta());
    run = useResearchV6ProjectionStore.getState().runs["run-1"]!;
    expect(run.pendingDeltaCount).toBe(0);
    expect(run.lastConfirmedSequence).toBe(5);
  });

  it("markDisconnected / teardownRun manage lifecycle", () => {
    const s = useResearchV6ProjectionStore.getState();
    s.applySnapshot("run-1", researchV6FixtureSnapshot());
    s.markDisconnected("run-1", false);
    expect(useResearchV6ProjectionStore.getState().runs["run-1"]!.disconnected).toBe(false);

    s.markDisconnected("run-1", true);
    expect(useResearchV6ProjectionStore.getState().runs["run-1"]!.disconnected).toBe(true);

    s.teardownRun("run-1");
    expect(useResearchV6ProjectionStore.getState().runs["run-1"]).toBeUndefined();
  });
});
