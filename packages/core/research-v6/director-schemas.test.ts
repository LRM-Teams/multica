import { describe, expect, it } from "vitest";
import {
  parseResearchV6DirectorProjectionDeltaPage,
  parseResearchV6DirectorProjectionSnapshot,
  encodeResearchV6DirectorProjectionSliceRequest,
} from "./director-schemas";

const ID = "00000000-0000-4000-8000-000000000003";
const SNAPSHOT_ID = "00000000-0000-4000-8000-000000000601";
const HASH = `sha256:${"d".repeat(64)}`;

function snapshot() {
  return {
    contract_kind: "projection_snapshot",
    schema_version: 6,
    snapshot_id: SNAPSHOT_ID,
    workspace_id: ID,
    run_id: ID,
    through_event_sequence: 47,
    projection_hash: HASH,
    slice_key: "default",
    nodes: [
      {
        id: "insight:one",
        kind: "insight",
        tier: "L",
        canonical_ref: { kind: "insight", id: ID },
        branch_ids: [ID],
        state: {
          execution: "succeeded",
          conclusion: "accepted",
          integration: "candidate",
        },
        title: "Stable result",
        catalog_summary: "Bounded summary",
        absorbed: false,
        terminal: true,
        expandable: true,
        hidden_child_count: 4,
        updated_at: "2026-08-17T08:00:00Z",
      },
    ],
    edges: [],
    density_bins: [],
    has_more: false,
  };
}

describe("Director V6 projection wire schemas", () => {
  it("accepts the exact strict projection snapshot", () => {
    expect(parseResearchV6DirectorProjectionSnapshot(snapshot()).sliceKey).toBe(
      "default",
    );
  });

  it("accepts a running Director Work node with an empty catalog summary", () => {
    const value = snapshot();
    value.nodes[0] = {
      ...value.nodes[0]!,
      id: "work_s:director",
      kind: "work_s",
      tier: "S",
      canonical_ref: { kind: "work_item", id: ID },
      branch_ids: [],
      state: {
        execution: "running",
        conclusion: "proposed",
        integration: "unmatched",
      },
      title: "director",
      catalog_summary: "",
      terminal: false,
      expandable: false,
      hidden_child_count: 0,
    };

    const parsed = parseResearchV6DirectorProjectionSnapshot(value);
    expect(parsed.sliceKey).toBe("default");
    expect(parsed.nodes[0]?.catalogSummary).toBe("");
  });

  it("degrades the legacy experimental projection shape without throwing", () => {
    expect(
      parseResearchV6DirectorProjectionSnapshot({
        snapshot_id: SNAPSHOT_ID,
        run_id: ID,
        graph_content_hash: { nodes: HASH, edges: HASH },
        nodes: [],
        edges: [],
      }).sliceKey,
    ).toBe("invalid-response");
  });

  it("degrades invented fields at the production boundary", () => {
    expect(
      parseResearchV6DirectorProjectionSnapshot({
        ...snapshot(),
        direction: "both",
      }).sliceKey,
    ).toBe("invalid-response");
  });

  it("keeps unknown future enum values without dropping the projection", () => {
    const value = snapshot();
    value.nodes[0] = {
      ...value.nodes[0]!,
      kind: "future_result",
      tier: "FUTURE_TIER",
      canonical_ref: { kind: "future_artifact", id: ID },
      state: {
        execution: "queued_remote",
        conclusion: "under_review",
        integration: "awaiting_match",
      },
    };

    const parsed = parseResearchV6DirectorProjectionSnapshot(value);
    expect(parsed.sliceKey).toBe("default");
    expect(parsed.nodes[0]?.kind).toBe("future_result");
    expect(parsed.nodes[0]?.state.execution).toBe("queued_remote");
  });

  it("parses the HTTP delta page envelope independently of a delta", () => {
    expect(
      parseResearchV6DirectorProjectionDeltaPage({
        run_id: ID,
        deltas: [],
        next_cursor: null,
        resync_required: true,
      }).resyncRequired,
    ).toBe(true);
  });

  it("degrades malformed detail and report payloads without throwing", async () => {
    const schemas = await import("./director-schemas");
    expect(schemas.parseResearchV6DirectorNodeDetail({}).node.id).toBe("invalid-response");
    expect(schemas.parseResearchV6DirectorReportDetail({}).id).toBeTruthy();
    expect(schemas.parseResearchV6DirectorProjectionDelta({}).eventSequence).toBe(0);
  });

  it("keeps an unknown node detail view forward-compatible", async () => {
    const schemas = await import("./director-schemas");
    const parsed = schemas.parseResearchV6DirectorNodeDetail({
      snapshot_id: SNAPSHOT_ID,
      through_event_sequence: 47,
      projection_hash: HASH,
      view: "future_audit_view",
      node: snapshot().nodes[0],
      incoming: [],
      outgoing: [],
      history_refs: [],
      agent_refs: [],
      work_item_refs: [],
      attempt_refs: [],
      evidence_refs: [],
      discussion_refs: [],
      report_refs: [],
    });
    expect(parsed.view).toBe("future_audit_view");
  });

  it("fixes derivation expansion depth to exactly one layer", () => {
    expect(
      encodeResearchV6DirectorProjectionSliceRequest({
        root: "insight:one",
        depth: 1,
        snapshotId: SNAPSHOT_ID,
      }).depth,
    ).toBe(1);
    expect(() =>
      encodeResearchV6DirectorProjectionSliceRequest({
        root: "insight:one",
        depth: 2 as 1,
        snapshotId: SNAPSHOT_ID,
      }),
    ).toThrow();
  });
});
