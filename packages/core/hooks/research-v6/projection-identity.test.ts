import { describe, expect, it } from "vitest";
import type { ResearchV6Delta } from "../../types/research-v6";
import {
  researchV6FixtureDelta,
  researchV6FixtureSnapshot,
} from "../../api/research-v6-fixtures";
import {
  isResearchV6DeltaForRun,
  isResearchV6SnapshotForRun,
} from "./projection-identity";

describe("Research V6 run identity", () => {
  it("accepts canonical snapshot and delta identities", () => {
    const snapshot = researchV6FixtureSnapshot();
    const delta = researchV6FixtureDelta();
    expect(isResearchV6SnapshotForRun(snapshot, snapshot.run_id)).toBe(true);
    expect(isResearchV6DeltaForRun(delta, snapshot.run_id)).toBe(true);
  });

  it("rejects cross-run snapshot and upsert facts", () => {
    const snapshot = researchV6FixtureSnapshot();
    snapshot.nodes[0]!.run_id = "other-run";
    expect(isResearchV6SnapshotForRun(snapshot, snapshot.run_id)).toBe(false);

    const delta = researchV6FixtureDelta();
    delta.edge_upserts[0]!.run_id = "other-run";
    expect(isResearchV6DeltaForRun(delta, "run-fixture")).toBe(false);
  });

  it("requires tombstones to carry the current stable run prefix", () => {
    const base: ResearchV6Delta = {
      from_sequence_exclusive: 1,
      through_sequence: 2,
      node_upserts: [],
      edge_upserts: [],
      node_tombstones: ["run-1:task:gone"],
      edge_tombstones: [],
      affected_root_node_ids: [],
      transition_kind: null,
    };
    expect(isResearchV6DeltaForRun(base, "run-1")).toBe(true);
    expect(
      isResearchV6DeltaForRun(
        { ...base, node_tombstones: ["run-2:task:gone"] },
        "run-1",
      ),
    ).toBe(false);
  });

  it("rejects unrouteable sequence-only frames without an explicit run", () => {
    const empty = {
      from_sequence_exclusive: 1,
      through_sequence: 2,
      node_upserts: [],
      edge_upserts: [],
      node_tombstones: [],
      edge_tombstones: [],
      affected_root_node_ids: [],
      transition_kind: null,
    } satisfies ResearchV6Delta;
    expect(isResearchV6DeltaForRun(empty, "run-1")).toBe(false);
    expect(
      isResearchV6DeltaForRun(
        { ...empty, run_id: "run-1" } as ResearchV6Delta,
        "run-1",
      ),
    ).toBe(true);
  });
});
