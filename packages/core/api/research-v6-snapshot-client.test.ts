import { afterEach, describe, expect, it, vi } from "vitest";
import { researchV6FixtureSnapshot } from "./research-v6-fixtures";
import { ApiClient } from "./client";

afterEach(() => {
  vi.unstubAllGlobals();
});

function stubResponse(body: unknown) {
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

describe("ApiClient Research V6 snapshot identity boundary", () => {
  it("accepts a projection whose complete identity matches the requested run", async () => {
    const snapshot = researchV6FixtureSnapshot();
    stubResponse(snapshot);
    const client = new ApiClient("https://api.example.test");
    await expect(
      client.getResearchV6ProjectionSnapshot(snapshot.run_id),
    ).resolves.toMatchObject({
      snapshot_id: snapshot.snapshot_id,
      run_id: snapshot.run_id,
    });
  });

  const corruptions: Array<[
    string,
    (snapshot: ReturnType<typeof researchV6FixtureSnapshot>) => void,
  ]> = [
    [
      "snapshot run",
      (snapshot) => {
        snapshot.run_id = "other-run";
      },
    ],
    [
      "node run",
      (snapshot) => {
        snapshot.nodes[0]!.run_id = "other-run";
      },
    ],
    [
      "edge run",
      (snapshot) => {
        snapshot.edges[0]!.run_id = "other-run";
      },
    ],
    [
      "duplicate node",
      (snapshot) => {
        snapshot.nodes[1]!.id = snapshot.nodes[0]!.id;
      },
    ],
    [
      "invalid sequence",
      (snapshot) => {
        snapshot.through_event_sequence = -1;
      },
    ],
  ];

  it.each(corruptions)("rejects conflicting %s identity", async (_, corrupt) => {
    const snapshot = researchV6FixtureSnapshot();
    const requestedRunId = snapshot.run_id;
    corrupt(snapshot);
    stubResponse(snapshot);
    const client = new ApiClient("https://api.example.test");
    await expect(
      client.getResearchV6ProjectionSnapshot(requestedRunId),
    ).rejects.toThrow("snapshot identity validation");
  });

  it("rejects a paginated first page instead of presenting it as the complete graph", async () => {
    const snapshot = researchV6FixtureSnapshot();
    snapshot.next_cursor = "next-page";
    stubResponse(snapshot);
    const client = new ApiClient("https://api.example.test");
    await expect(
      client.getResearchV6ProjectionSnapshot(snapshot.run_id),
    ).rejects.toThrow("complete snapshot identity validation");
  });

  it("forwards the caller AbortSignal to the V6 request", async () => {
    const snapshot = researchV6FixtureSnapshot();
    stubResponse(snapshot);
    const client = new ApiClient("https://api.example.test");
    const controller = new AbortController();
    await client.getResearchV6ProjectionSnapshot(snapshot.run_id, {
      signal: controller.signal,
    });
    const fetchMock = vi.mocked(fetch);
    expect(fetchMock).toHaveBeenCalledWith(
      expect.stringContaining(`/api/research/v6/runs/${snapshot.run_id}/projection/snapshot`),
      expect.objectContaining({ signal: controller.signal }),
    );
  });
});
