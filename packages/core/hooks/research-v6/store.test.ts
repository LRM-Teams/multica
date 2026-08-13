import { beforeEach, describe, expect, it } from "vitest";
import { useResearchV6ProjectionStore } from "./store";
import {
  researchV6FixtureDelta,
  researchV6FixtureSnapshot,
  researchV6Fixtures,
} from "../../api/research-v6-fixtures";

const RUN_ID = researchV6Fixtures.runId;

describe("useResearchV6ProjectionStore", () => {
  beforeEach(() => {
    useResearchV6ProjectionStore.setState({ runs: {}, clients: {} });
  });

  it("seeds a run from a snapshot and exposes its projection slice", () => {
    const snapshot = researchV6FixtureSnapshot();
    useResearchV6ProjectionStore.getState().applySnapshot(RUN_ID, snapshot);

    const store = useResearchV6ProjectionStore.getState();
    const run = store.runs[RUN_ID];
    expect(run).toBeDefined();
    expect(run!.lastConfirmedSequence).toBe(2);
    expect(run!.nodes.size).toBe(3);
    expect(run!.edges.size).toBe(2);
    expect(run!.disconnected).toBe(false);
  });

  it("applies deltas and advances the cursor (idempotent on duplicate)", () => {
    const s = useResearchV6ProjectionStore.getState();
    s.applySnapshot(RUN_ID, researchV6FixtureSnapshot());

    const delta = researchV6FixtureDelta();
    s.applyDelta(RUN_ID, delta);
    const runAfter = useResearchV6ProjectionStore.getState().runs[RUN_ID]!;
    expect(runAfter.lastConfirmedSequence).toBe(4);
    expect(runAfter.nodes.size).toBe(5);
    expect(runAfter.edges.size).toBe(4);
    expect(runAfter.pendingDeltaCount).toBe(0);

    // Duplicate delta is dropped — node/edge counts unchanged.
    useResearchV6ProjectionStore.getState().applyDelta(RUN_ID, delta);
    const afterDup = useResearchV6ProjectionStore.getState().runs[RUN_ID]!;
    expect(afterDup.lastConfirmedSequence).toBe(4);
    expect(afterDup.nodes.size).toBe(5);
    expect(afterDup.edges.size).toBe(4);
  });

  it("buffers an out-of-order delta until the middleware fills", () => {
    const s = useResearchV6ProjectionStore.getState();
    s.applySnapshot(RUN_ID, researchV6FixtureSnapshot());

    // Delta [4,5) arrives first — gap ahead.
    s.applyDelta(RUN_ID, {
      ...researchV6FixtureDelta(),
      from_sequence_exclusive: 4,
      through_sequence: 5,
    });
    let run = useResearchV6ProjectionStore.getState().runs[RUN_ID]!;
    expect(run.pendingDeltaCount).toBe(1);
    expect(run.lastConfirmedSequence).toBe(2);

    // The missing [2,4) arrives — drains and commits in order.
    s.applyDelta(RUN_ID, researchV6FixtureDelta());
    run = useResearchV6ProjectionStore.getState().runs[RUN_ID]!;
    expect(run.pendingDeltaCount).toBe(0);
    expect(run.lastConfirmedSequence).toBe(5);
  });

  it("markDisconnected / teardownRun manage lifecycle", () => {
    const s = useResearchV6ProjectionStore.getState();
    s.applySnapshot(RUN_ID, researchV6FixtureSnapshot());
    s.markDisconnected(RUN_ID, false);
    expect(useResearchV6ProjectionStore.getState().runs[RUN_ID]!.disconnected).toBe(false);

    s.markDisconnected(RUN_ID, true);
    expect(useResearchV6ProjectionStore.getState().runs[RUN_ID]!.disconnected).toBe(true);

    s.teardownRun(RUN_ID);
    expect(useResearchV6ProjectionStore.getState().runs[RUN_ID]).toBeUndefined();
  });
});
