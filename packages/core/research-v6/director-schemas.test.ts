import { describe, expect, it } from "vitest";
import {
  parseResearchV6DirectorProjectionDeltaPage,
  parseResearchV6DirectorProjectionSnapshot,
  parseResearchV6DirectorProjectionSliceRequest,
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
    expect(parseResearchV6DirectorProjectionSnapshot(snapshot()).slice_key).toBe(
      "default",
    );
  });

  it("degrades the legacy experimental projection shape without throwing", () => {
    expect(
      parseResearchV6DirectorProjectionSnapshot({
        snapshot_id: SNAPSHOT_ID,
        run_id: ID,
        graph_content_hash: { nodes: HASH, edges: HASH },
        nodes: [],
        edges: [],
      }).slice_key,
    ).toBe("invalid-response");
  });

  it("degrades invented fields at the production boundary", () => {
    expect(
      parseResearchV6DirectorProjectionSnapshot({
        ...snapshot(),
        direction: "both",
      }).slice_key,
    ).toBe("invalid-response");
  });

  it("parses the HTTP delta page envelope independently of a delta", () => {
    expect(
      parseResearchV6DirectorProjectionDeltaPage({
        run_id: ID,
        deltas: [],
        next_cursor: null,
        resync_required: true,
      }).resync_required,
    ).toBe(true);
  });

  it("degrades malformed detail and report payloads without throwing", async () => {
    const schemas = await import("./director-schemas");
    expect(schemas.parseResearchV6DirectorNodeDetail({})).toBeNull();
    expect(schemas.parseResearchV6DirectorReportDetail({})).toBeNull();
    expect(schemas.parseResearchV6DirectorProjectionDelta({})).toBeNull();
  });

  it("fixes derivation expansion depth to exactly one layer", () => {
    expect(
      parseResearchV6DirectorProjectionSliceRequest({
        root: "insight:one",
        depth: 1,
        snapshot_id: SNAPSHOT_ID,
      }).depth,
    ).toBe(1);
    expect(() =>
      parseResearchV6DirectorProjectionSliceRequest({
        root: "insight:one",
        depth: 2,
        snapshot_id: SNAPSHOT_ID,
      }),
    ).toThrow();
  });
});
