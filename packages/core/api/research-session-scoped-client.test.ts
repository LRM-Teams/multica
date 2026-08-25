import { afterEach, describe, expect, it, vi } from "vitest";
import { ApiClient } from "./client";

afterEach(() => {
  vi.unstubAllGlobals();
});

function stubResponse(body: unknown, status = 200) {
  vi.stubGlobal(
    "fetch",
    vi.fn().mockResolvedValue(
      new Response(JSON.stringify(body), {
        status,
        headers: { "Content-Type": "application/json" },
      }),
    ),
  );
}

function roundCard(sessionId: string) {
  return {
    id: "round-1",
    session_id: sessionId,
    round_number: 1,
    decision: "continue",
  };
}

describe("ApiClient session-scoped Research reads", () => {
  it("degrades malformed presence and rejects cross-session responses", async () => {
    const client = new ApiClient("https://api.example.test");
    stubResponse({ session_id: "s1", presence: [] });
    await expect(client.getResearchPresence("s1")).resolves.toEqual({
      session_id: "s1",
      presence: {},
    });

    stubResponse({ session_id: "s2", presence: {} });
    await expect(client.getResearchPresence("s1")).rejects.toThrow(
      "response failed session validation",
    );
  });

  it("normalizes an omitted compatible presence session id", async () => {
    stubResponse({ presence: {} });
    const client = new ApiClient("https://api.example.test");
    await expect(client.getResearchPresence("s1")).resolves.toMatchObject({
      session_id: "s1",
    });
  });

  it("rejects a cross-session product-round list", async () => {
    stubResponse({ rounds: [roundCard("s2")] });
    const client = new ApiClient("https://api.example.test");
    await expect(client.listResearchProductRoundCards("s1")).rejects.toThrow(
      "response failed session validation",
    );
  });

  it("only degrades product rounds for an absent capability", async () => {
    const client = new ApiClient("https://api.example.test");
    stubResponse({ message: "missing" }, 404);
    await expect(client.listResearchProductRoundCards("s1")).resolves.toEqual({
      rounds: [],
    });

    stubResponse({ message: "boom" }, 500);
    await expect(client.listResearchProductRoundCards("s1")).rejects.toThrow();
  });

  it("validates and normalizes a single product-round card", async () => {
    stubResponse(roundCard(""));
    const client = new ApiClient("https://api.example.test");
    await expect(client.getResearchProductRoundCard("s1", 1)).resolves.toMatchObject({
      session_id: "s1",
      round_number: 1,
    });

    stubResponse(roundCard("s2"));
    await expect(client.getResearchProductRoundCard("s1", 1)).rejects.toThrow(
      "response failed session validation",
    );
  });

  it("validates message mutation identity", async () => {
    stubResponse({ id: "m1", session_id: "s1", body: "accepted" });
    const client = new ApiClient("https://api.example.test");
    await expect(
      client.postResearchMessage("s1", {
        body: "hello",
        clientRequestId: "00000000-0000-4000-8000-000000000001",
      }),
    ).resolves.toMatchObject({ id: "m1", session_id: "s1" });

    stubResponse({ id: "m2", session_id: "s2", body: "foreign" });
    await expect(
      client.postResearchMessage("s1", {
        body: "hello",
        clientRequestId: "00000000-0000-4000-8000-000000000002",
      }),
    ).rejects.toThrow("response failed session validation");
  });

  it("sends exact immutable research references with natural-language steering", async () => {
    stubResponse({ id: "m1", session_id: "s1", body: "check this" });
    const client = new ApiClient("https://api.example.test");
    const selectedRef = {
      stableId: "insight:00000000-0000-4000-8000-000000000306",
      kind: "insight",
      entityId: "00000000-0000-4000-8000-000000000306",
      revision: 2,
      contentHash: `sha256:${"c".repeat(64)}`,
      displaySummary: "Latency boundary",
    };
    const clientRequestId = "00000000-0000-4000-8000-000000000021";

    await client.postResearchMessage("s1", {
      body: "check this",
      clientRequestId,
      selectedResearchRefs: [selectedRef],
    });

    const fetchMock = vi.mocked(fetch);
    const request = fetchMock.mock.calls[0]?.[1];
    expect(JSON.parse(String(request?.body))).toEqual({
      body: "check this",
      client_request_id: clientRequestId,
      selected_research_refs: [
        {
          stable_id: selectedRef.stableId,
          kind: selectedRef.kind,
          entity_id: selectedRef.entityId,
          revision: selectedRef.revision,
          content_hash: selectedRef.contentHash,
          display_summary: selectedRef.displaySummary,
        },
      ],
    });
  });

  it.each(["confirm", "stop"] as const)(
    "rejects a cross-session %s mutation response",
    async (operation) => {
      stubResponse({ id: "s2", workspace_id: "ws1" });
      const client = new ApiClient("https://api.example.test");
      await expect(
        operation === "confirm"
          ? client.confirmResearchSession("s1")
          : client.stopResearchSession("s1"),
      ).rejects.toThrow("response failed session validation");
    },
  );
});
