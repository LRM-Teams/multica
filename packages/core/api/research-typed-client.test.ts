import { afterEach, describe, expect, it, vi } from "vitest";
import { ApiClient } from "./client";

afterEach(() => {
  vi.unstubAllGlobals();
});

function typedGraphResponse(overrides: Record<string, unknown> = {}) {
  return {
    session_id: "session-a",
    graph_version: 1,
    nodes: [],
    edges: [],
    clusters: [],
    lineage: {},
    ...overrides,
  };
}

function stubGraphResponse(body: unknown) {
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

describe("ApiClient typed graph session boundary", () => {
  it("accepts canonical rows belonging to the requested session", async () => {
    stubGraphResponse(
      typedGraphResponse({
        nodes: [{ id: "n1", session_id: "session-a" }],
        edges: [
          {
            from_node_id: "n1",
            to_node_id: "n2",
            session_id: "session-a",
          },
        ],
        clusters: [{ id: "c1", session_id: "session-a" }],
      }),
    );
    const client = new ApiClient("https://api.example.test");

    await expect(client.getResearchGraphTyped("session-a")).resolves.toMatchObject({
      session_id: "session-a",
      graph_version: 1,
    });
  });

  it("fills an omitted legacy top-level session id from the request", async () => {
    stubGraphResponse(typedGraphResponse({ session_id: "" }));
    const client = new ApiClient("https://api.example.test");

    await expect(client.getResearchGraphTyped("session-a")).resolves.toMatchObject({
      session_id: "session-a",
    });
  });

  it.each([
    ["response", typedGraphResponse({ session_id: "session-b" })],
    [
      "node",
      typedGraphResponse({ nodes: [{ id: "n1", session_id: "session-b" }] }),
    ],
    [
      "edge",
      typedGraphResponse({
        edges: [
          {
            from_node_id: "n1",
            to_node_id: "n2",
            session_id: "session-b",
          },
        ],
      }),
    ],
    [
      "cluster",
      typedGraphResponse({
        clusters: [{ id: "c1", session_id: "session-b" }],
      }),
    ],
  ])("rejects a cross-session %s", async (_kind, response) => {
    stubGraphResponse(response);
    const client = new ApiClient("https://api.example.test");

    await expect(client.getResearchGraphTyped("session-a")).rejects.toThrow(
      "response failed session validation",
    );
  });
});
