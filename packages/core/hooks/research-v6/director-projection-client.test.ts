import { describe, expect, it } from "vitest";
import type {
  ResearchV6DirectorProjectionDelta,
  ResearchV6DirectorProjectionNode,
  ResearchV6DirectorProjectionSnapshot,
} from "../../types/research-v6-director";
import { ResearchV6DirectorProjectionClient } from "./director-projection-client";

const WORKSPACE_ID = "00000000-0000-4000-8000-000000000001";
const RUN_ID = "00000000-0000-4000-8000-000000000003";
const SNAPSHOT_ID = "00000000-0000-4000-8000-000000000601";
const HASH_A = `sha256:${"a".repeat(64)}`;
const HASH_B = `sha256:${"b".repeat(64)}`;
const HASH_C = `sha256:${"c".repeat(64)}`;

function node(id: string): ResearchV6DirectorProjectionNode {
  return {
    id,
    kind: "insight",
    tier: "L",
    canonicalRef: { kind: "insight", id: RUN_ID },
    branchIds: [],
    state: {
      execution: "succeeded",
      conclusion: "accepted",
      integration: "candidate",
    },
    catalogSummary: id,
    absorbed: false,
    terminal: true,
    expandable: true,
    hiddenChildCount: 2,
    updatedAt: "2026-08-17T08:00:00Z",
  };
}

function snapshot(
  nodes: ResearchV6DirectorProjectionNode[],
  options: Partial<ResearchV6DirectorProjectionSnapshot> = {},
): ResearchV6DirectorProjectionSnapshot {
  return {
    contractKind: "projection_snapshot",
    schemaVersion: 6,
    snapshotId: SNAPSHOT_ID,
    workspaceId: WORKSPACE_ID,
    runId: RUN_ID,
    throughEventSequence: 4,
    projectionHash: HASH_A,
    sliceKey: "default",
    nodes,
    edges: [],
    densityBins: [],
    hasMore: false,
    ...options,
  };
}

function delta(
  eventSequence: number,
  previousHash: string,
  projectionHash: string,
  nodes: ResearchV6DirectorProjectionNode[] = [],
): ResearchV6DirectorProjectionDelta {
  return {
    contractKind: "projection_delta",
    schemaVersion: 6,
    workspaceId: WORKSPACE_ID,
    runId: RUN_ID,
    snapshotId: SNAPSHOT_ID,
    eventSequence: eventSequence,
    previousProjectionHash: previousHash,
    projectionHash: projectionHash,
    upsertNodes: nodes,
    removeNodeIds: [],
    upsertEdges: [],
    removeEdgeIds: [],
    invalidateSliceKeys: [],
  };
}

describe("ResearchV6DirectorProjectionClient", () => {
  it("merges pages pinned to the same snapshot and slice", () => {
    const client = new ResearchV6DirectorProjectionClient();
    client.applySnapshotPage(snapshot([node("a")], { hasMore: true, nextCursor: "p2" }));
    client.applySnapshotPage(snapshot([node("b")]));
    expect(client.getState().views.get("default")?.nodes.size).toBe(2);
  });

  it("applies a contiguous hash chain and admits server-upserted nodes", () => {
    const client = new ResearchV6DirectorProjectionClient();
    client.applySnapshotPage(snapshot([node("a")]));
    expect(client.applyDelta(delta(5, HASH_A, HASH_B, [node("b")]))).toEqual({
      kind: "applied",
      advancedTo: 5,
    });
    expect(client.getState().views.get("default")?.nodes.has("b")).toBe(true);
  });

  it("buffers out-of-order deltas and drains only a valid hash chain", () => {
    const client = new ResearchV6DirectorProjectionClient();
    client.applySnapshotPage(snapshot([node("a")]));
    expect(client.applyDelta(delta(6, HASH_B, HASH_C, [node("c")])).kind).toBe(
      "buffered",
    );
    client.applyDelta(delta(5, HASH_A, HASH_B, [node("b")]));
    expect(client.getState().lastConfirmedSequence).toBe(6);
    expect(client.getState().views.get("default")?.nodes.has("c")).toBe(true);
  });

  it("requires resync instead of applying a broken projection hash chain", () => {
    const client = new ResearchV6DirectorProjectionClient();
    client.applySnapshotPage(snapshot([node("a")]));
    expect(client.applyDelta(delta(5, HASH_C, HASH_B)).kind).toBe(
      "resync_required",
    );
    expect(client.getState().resyncRequired).toBe(true);
  });

  it("never rolls back to a stale page of the current snapshot", () => {
    const client = new ResearchV6DirectorProjectionClient();
    client.applySnapshotPage(snapshot([node("a")]));
    client.applyDelta(delta(5, HASH_A, HASH_B, [node("b")]));
    client.applySnapshotPage(snapshot([node("stale")]));
    expect(client.getState().resyncRequired).toBe(true);
    expect(client.getState().views.get("default")?.nodes.has("stale")).toBe(false);
  });

  it("invalidates only server-declared expansion slices", () => {
    const client = new ResearchV6DirectorProjectionClient();
    client.applySnapshotPage(snapshot([node("a")]));
    client.applySnapshotPage(
      snapshot([node("child")], { sliceKey: "expand:a", hasMore: false }),
    );
    const update = delta(5, HASH_A, HASH_B);
    update.invalidateSliceKeys = ["expand:a"];
    client.applyDelta(update);
    expect(client.getState().views.has("expand:a")).toBe(false);
    expect(client.getState().views.has("default")).toBe(true);
  });

  it("turns an unfilled sequence gap into one resync request", () => {
    const expirations: Array<() => void> = [];
    const client = new ResearchV6DirectorProjectionClient({
      scheduleGapTimeout: (callback) => {
        expirations.push(callback);
        return { cancel: () => {} };
      },
    });
    client.applySnapshotPage(snapshot([node("a")]));
    client.applyDelta(delta(6, HASH_B, HASH_C));
    expect(client.getState().awaitingSequence).toBe(5);
    expirations[0]?.();
    expect(client.getState().resyncRequired).toBe(true);
    expect(client.getState().pendingDeltas.size).toBe(0);
  });
});
