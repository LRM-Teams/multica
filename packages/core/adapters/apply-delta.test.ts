// @vitest-environment node
import { describe, expect, it } from "vitest";
import {
  adaptV6Delta,
  adaptV6Snapshot,
  applyCanvasDelta,
} from "./index";
import type { CanvasDelta } from "./index";
import {
  twoComponentSnapshot,
  v6FixtureDelta,
  v6FixtureSnapshot,
} from "./fixtures";

const base = () => adaptV6Snapshot(v6FixtureSnapshot());

describe("applyCanvasDelta — (§7.2) idempotent, sequence-framed", () => {
  it("applies upserts and advances the event sequence", () => {
    const delta = adaptV6Delta(v6FixtureDelta());
    const applied = applyCanvasDelta(base(), delta);
    expect(applied.needsResync).toBe(false);
    expect(applied.wasDuplicate).toBe(false);
    expect(applied.snapshot.throughEventSequence).toBe(8);
    expect(applied.snapshot.nodes.some((n) => n.id.includes("c3"))).toBe(true);
  });

  it("re-applying the same frame is a no-op (duplicate detection)", () => {
    const delta = adaptV6Delta(v6FixtureDelta());
    const first = applyCanvasDelta(base(), delta);
    // Same frame arrives again; the snapshot watermark is already 8.
    const dup = applyCanvasDelta(first.snapshot, delta);
    expect(dup.wasDuplicate).toBe(true);
    expect(dup.appliedNodeIds).toHaveLength(0);
    expect(dup.snapshot.nodes.filter((n) => n.id.includes("c3"))).toHaveLength(1);
  });

  it("buffers/signals a future (gapped) frame instead of guessing", () => {
    // Client is at watermark 6; delta claims to start at 9 → a gap exists.
    const delta = adaptV6Delta({
      ...v6FixtureDelta(),
      from_sequence_exclusive: 9,
      through_sequence: 10,
    });
    const snap = base();
    const applied = applyCanvasDelta(snap, delta);
    expect(applied.needsResync).toBe(true);
    expect(applied.snapshot).toBe(snap);
  });

  it("signals resync on an overlapping frame that also contains new events", () => {
    // from=4 < watermark=6 < through=8: some already applied, some new.
    const delta = adaptV6Delta({
      ...v6FixtureDelta(),
      from_sequence_exclusive: 4,
      through_sequence: 8,
    });
    const applied = applyCanvasDelta(base(), delta);
    expect(applied.needsResync).toBe(true);
  });

  it("drops the view node and every dangling edge on a visibility tombstone", () => {
    const delta = adaptV6Delta(v6FixtureDelta());
    const applied = applyCanvasDelta(base(), delta);
    const snapshot = applied.snapshot;
    // claim:c1 removed.
    expect(snapshot.nodes.find((n) => n.id.includes(":claim:c1"))).toBeUndefined();
    // Dangling edges e1,e3,e4,e6 removed; edge e5 still references i1 + c2.
    expect(snapshot.edges.find((e) => e.id === "e1")).toBeUndefined();
    expect(snapshot.edges.find((e) => e.id === "e4")).toBeUndefined();
    expect(snapshot.edges.find((e) => e.id === "e5")).toBeDefined();
    // Every remaining edge touches only present nodes (no dangling edges).
    for (const e of snapshot.edges) {
      expect(snapshot.nodes.some((n) => n.id === e.from)).toBe(true);
      expect(snapshot.nodes.some((n) => n.id === e.to)).toBe(true);
    }
  });

  it("reports affected roots so the renderer recomputes only that region", () => {
    const delta = adaptV6Delta(v6FixtureDelta());
    const applied = applyCanvasDelta(base(), delta);
    // Retained neighbors of the tombstoned claim:c1 (pre-removal, both
    // directions): q1 (in), h1 (in), i1 (out), c2 (out via contradicts).
    expect(applied.affectedRootIds.sort()).toEqual([
      "run-v6-contract-fixture:claim:c2",
      "run-v6-contract-fixture:hypothesis:h1",
      "run-v6-contract-fixture:insight:i1",
      "run-v6-contract-fixture:question:q1",
    ]);
  });

  it("recomputes a deterministic content hash after each delta", () => {
    const delta = adaptV6Delta(v6FixtureDelta());
    const applied = applyCanvasDelta(base(), delta);
    const again = applyCanvasDelta(base(), delta);
    expect(applied.snapshot.graphContentHash).toBe(again.snapshot.graphContentHash);
    // Hash changed because canonical state changed (c1 removed, c3 added).
    expect(applied.snapshot.graphContentHash).not.toBe(base().graphContentHash);
  });

  it("replaces a replayed node in place rather than duplicating it", () => {
    const snap = twoComponentSnapshot();
    const a1 = snap.nodes.find((n) => n.id === "a1")!;
    // A delta that re-emits a1 with a newer updatedAt + new status.
    const delta: CanvasDelta = {
      fromSequenceExclusive: 1,
      throughSequence: 2,
      upsertNodes: [{ ...a1, status: "done", updatedAt: "2026-08-02T00:00:00Z" }],
      upsertEdges: [],
      tombstoneNodeIds: [],
      tombstoneEdgeIds: [],
      affectedRootIds: [],
      transitionKind: "task_dispatched",
    };
    const applied = applyCanvasDelta(snap, delta);
    expect(applied.snapshot.nodes.filter((n) => n.id === "a1")).toHaveLength(1);
    expect(applied.snapshot.nodes.find((n) => n.id === "a1")!.status).toBe("done");
    // Content changed → hash changes.
    expect(applied.snapshot.graphContentHash).not.toBe(snap.graphContentHash);
  });
});
