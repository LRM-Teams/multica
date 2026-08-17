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
    canonical_ref: { kind: "insight", id: RUN_ID },
    branch_ids: [],
    state: {
      execution: "succeeded",
      conclusion: "accepted",
      integration: "candidate",
    },
    catalog_summary: id,
    absorbed: false,
    terminal: true,
    expandable: true,
    hidden_child_count: 2,
    updated_at: "2026-08-17T08:00:00Z",
  };
}

function snapshot(
  nodes: ResearchV6DirectorProjectionNode[],
  options: Partial<ResearchV6DirectorProjectionSnapshot> = {},
): ResearchV6DirectorProjectionSnapshot {
  return {
    contract_kind: "projection_snapshot",
    schema_version: 6,
    snapshot_id: SNAPSHOT_ID,
    workspace_id: WORKSPACE_ID,
    run_id: RUN_ID,
    through_event_sequence: 4,
    projection_hash: HASH_A,
    slice_key: "default",
    nodes,
    edges: [],
    density_bins: [],
    has_more: false,
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
    contract_kind: "projection_delta",
    schema_version: 6,
    workspace_id: WORKSPACE_ID,
    run_id: RUN_ID,
    snapshot_id: SNAPSHOT_ID,
    event_sequence: eventSequence,
    previous_projection_hash: previousHash,
    projection_hash: projectionHash,
    upsert_nodes: nodes,
    remove_node_ids: [],
    upsert_edges: [],
    remove_edge_ids: [],
    invalidate_slice_keys: [],
  };
}

describe("ResearchV6DirectorProjectionClient", () => {
  it("merges pages pinned to the same snapshot and slice", () => {
    const client = new ResearchV6DirectorProjectionClient();
    client.applySnapshotPage(snapshot([node("a")], { has_more: true, next_cursor: "p2" }));
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
      snapshot([node("child")], { slice_key: "expand:a", has_more: false }),
    );
    const update = delta(5, HASH_A, HASH_B);
    update.invalidate_slice_keys = ["expand:a"];
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
