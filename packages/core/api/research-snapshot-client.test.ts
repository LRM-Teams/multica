import { afterEach, describe, expect, it, vi } from "vitest";
import { ApiClient } from "./client";

afterEach(() => {
  vi.unstubAllGlobals();
});

function snapshot(overrides: Record<string, unknown> = {}) {
  return {
    session: { id: "s1", workspace_id: "ws1" },
    fleet: { id: "f1", workspace_id: "ws1" },
    nodes: [],
    edges: [],
    sources: [],
    report: null,
    evals: [],
    messages: [],
    ...overrides,
  };
}

function stubSnapshot(body: unknown) {
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

describe("ApiClient research snapshot boundary", () => {
  it("returns a canonical snapshot for the requested session", async () => {
    stubSnapshot(
      snapshot({
        nodes: [{ id: "n1", session_id: "s1", node_type: "finding" }],
      }),
    );
    const client = new ApiClient("https://api.example.test");

    await expect(client.getResearchSessionSnapshot("s1")).resolves.toMatchObject({
      session: { id: "s1" },
      nodes: [{ id: "n1", session_id: "s1" }],
    });
  });

  it("rejects a malformed successful response instead of showing an empty session", async () => {
    stubSnapshot({ session: { id: "s1" }, nodes: "not-an-array" });
    const client = new ApiClient("https://api.example.test");

    await expect(client.getResearchSessionSnapshot("s1")).rejects.toThrow(
      "response failed schema validation",
    );
  });

  it.each([
    ["session", snapshot({ session: { id: "s2", workspace_id: "ws1" } })],
    [
      "node",
      snapshot({ nodes: [{ id: "n1", session_id: "s2", node_type: "finding" }] }),
    ],
    [
      "message",
      snapshot({ messages: [{ id: "m1", session_id: "s2", body: "foreign" }] }),
    ],
    [
      "report",
      snapshot({ report: { id: "r1", session_id: "s2", content_md: "foreign" } }),
    ],
  ])("rejects a cross-session %s", async (_kind, body) => {
    stubSnapshot(body);
    const client = new ApiClient("https://api.example.test");

    await expect(client.getResearchSessionSnapshot("s1")).rejects.toThrow(
      "response failed session validation",
    );
  });
});
