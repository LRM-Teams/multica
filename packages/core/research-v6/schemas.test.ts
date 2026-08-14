import { describe, expect, it } from "vitest";
import { researchV6FixtureDelta, researchV6FixtureSnapshot } from "../api/research-v6-fixtures";
import {
  parseResearchV6Delta,
  parseResearchV6Snapshot,
  ResearchV6NodeKinds,
} from "./schemas";

describe("research-v6 contract fixtures + schemas", () => {
  it("snapshot fixture round-trips through the lenient schema", () => {
    const snapshot = researchV6FixtureSnapshot();
    const parsed = parseResearchV6Snapshot(snapshot);
    expect(parsed.snapshot_id).toBe(snapshot.snapshot_id);
    expect(parsed.through_event_sequence).toBe(2);
    expect(parsed.nodes).toHaveLength(3);
    expect(parsed.edges).toHaveLength(2);
    expect(parsed.graph_content_hash).toEqual(snapshot.graph_content_hash);
  });

  it("delta fixture round-trips through the lenient schema", () => {
    const delta = researchV6FixtureDelta();
    const parsed = parseResearchV6Delta(delta);
    expect(parsed).not.toBeNull();
    expect(parsed!.from_sequence_exclusive).toBe(2);
    expect(parsed!.through_sequence).toBe(4);
    expect(parsed!.transition_kind).toBe("result_accepted");
  });

  it("preserves a post-delta server hash and downgrades an omitted legacy hash to null", () => {
    const delta = researchV6FixtureDelta();
    const hashed = parseResearchV6Delta({
      ...delta,
      graph_content_hash: { nodes: "sha256:nodes", edges: "sha256:edges" },
    });
    expect(hashed?.graph_content_hash).toEqual({ nodes: "sha256:nodes", edges: "sha256:edges" });
    expect(parseResearchV6Delta(delta)?.graph_content_hash).toBeNull();
  });

  it("unknown future node_kind still parses (generic degrade, no client crash)", () => {
    const delta = researchV6FixtureDelta();
    const future = {
      ...delta,
      node_upserts: [
        { ...delta.node_upserts[0]!, node_kind: "some_future_kind", id: "run:x:f" },
      ],
    };
    const parsed = parseResearchV6Delta(future);
    expect(parsed).not.toBeNull();
    expect(parsed!.node_upserts[0]!.node_kind).toBe("some_future_kind");
  });

  it("registered V6 node_kinds cover the doc's minimum set", () => {
    // design doc 7.1 lists these kinds; keep the list anchored so drift shows.
    const required = [
      "task", "attempt", "result_artifact", "search_plan", "query_execution",
      "source_candidate", "screening_decision", "source_snapshot", "observation",
      "claim", "question", "hypothesis", "branch", "insight", "insight_derivation",
      "integration_round", "integration_contribution", "dispute", "dispute_position",
      "deliberation", "deliberation_turn", "decision", "team_formation",
      "team_membership", "divergence_pass", "capability_observation",
      "report_revision", "evaluation_defect", "monitoring_cycle", "episode",
    ];
    for (const kind of required) {
      expect(ResearchV6NodeKinds).toContain(kind);
    }
  });
});
