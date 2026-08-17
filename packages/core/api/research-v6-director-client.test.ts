import { afterEach, describe, expect, it, vi } from "vitest";
import { ApiClient } from "./client";

const WORKSPACE_ID = "00000000-0000-4000-8000-000000000001";
const RUN_ID = "00000000-0000-4000-8000-000000000003";
const SNAPSHOT_ID = "00000000-0000-4000-8000-000000000601";
const HASH = `sha256:${"d".repeat(64)}`;

afterEach(() => vi.unstubAllGlobals());

function response(body: unknown) {
  vi.stubGlobal(
    "fetch",
    vi.fn().mockResolvedValue(
      new Response(JSON.stringify(body), {
        status: 200,
        headers: { "Content-Type": "application/json" },
      }),
    ),
  );
}

function snapshot() {
  return {
    contract_kind: "projection_snapshot",
    schema_version: 6,
    snapshot_id: SNAPSHOT_ID,
    workspace_id: WORKSPACE_ID,
    run_id: RUN_ID,
    through_event_sequence: 47,
    projection_hash: HASH,
    slice_key: "default",
    nodes: [],
    edges: [],
    density_bins: [],
    has_more: false,
  };
}

describe("ApiClient Director V6 projection HTTP contract", () => {
  it("requests a snapshot page with its opaque cursor", async () => {
    response(snapshot());
    const client = new ApiClient("https://api.example.test");
    await client.getResearchV6DirectorProjectionSnapshot(WORKSPACE_ID, RUN_ID, {
      cursor: "opaque page",
    });
    expect(fetch).toHaveBeenCalledWith(
      expect.stringContaining("/projection/snapshot?cursor=opaque+page"),
      expect.any(Object),
    );
  });

  it("requests exactly one derivation layer pinned to the snapshot", async () => {
    response({ ...snapshot(), slice_key: "insight:one:depth:1" });
    const client = new ApiClient("https://api.example.test");
    await client.getResearchV6DirectorProjectionSlice(WORKSPACE_ID, RUN_ID, {
      root: "insight:one",
      depth: 1,
      snapshot_id: SNAPSHOT_ID,
    });
    expect(fetch).toHaveBeenCalledWith(
      expect.stringContaining(
        `/projection/slice?root=insight%3Aone&depth=1&snapshot_id=${SNAPSHOT_ID}`,
      ),
      expect.any(Object),
    );
  });

  it("uses after rather than the superseded delta query name", async () => {
    response({ run_id: RUN_ID, deltas: [], next_cursor: null, resync_required: false });
    const client = new ApiClient("https://api.example.test");
    await client.getResearchV6DirectorProjectionDeltaPage(WORKSPACE_ID, RUN_ID, 47);
    expect(fetch).toHaveBeenCalledWith(
      expect.stringContaining("/projection/deltas?after=47"),
      expect.any(Object),
    );
  });

  it("requests resync instead of throwing for a cross-run delta page", async () => {
    response({
      run_id: "00000000-0000-4000-8000-000000000099",
      deltas: [],
      next_cursor: null,
      resync_required: false,
    });
    const client = new ApiClient("https://api.example.test");
    await expect(
      client.getResearchV6DirectorProjectionDeltaPage(WORKSPACE_ID, RUN_ID, 47),
    ).resolves.toMatchObject({ run_id: RUN_ID, deltas: [], resync_required: true });
  });

  it("sends the full snapshot identity when resuming", async () => {
    response({ run_id: RUN_ID, deltas: [], next_cursor: null, resync_required: true });
    const client = new ApiClient("https://api.example.test");
    await client.resumeResearchV6DirectorProjection(WORKSPACE_ID, RUN_ID, {
      snapshot_id: SNAPSHOT_ID,
      last_confirmed_sequence: 47,
      projection_hash: HASH,
    });
    expect(fetch).toHaveBeenCalledWith(
      expect.stringContaining("/projection/resume"),
      expect.objectContaining({
        method: "POST",
        body: JSON.stringify({
          snapshot_id: SNAPSHOT_ID,
          last_confirmed_sequence: 47,
          projection_hash: HASH,
        }),
      }),
    );
  });

  it("degrades a cross-workspace response to a safe local identity", async () => {
    response({ ...snapshot(), workspace_id: "00000000-0000-4000-8000-000000000099" });
    const client = new ApiClient("https://api.example.test");
    await expect(
      client.getResearchV6DirectorProjectionSnapshot(WORKSPACE_ID, RUN_ID),
    ).resolves.toMatchObject({ workspace_id: WORKSPACE_ID, run_id: RUN_ID });
  });
});
