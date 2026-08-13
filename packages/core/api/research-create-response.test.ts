import { afterEach, describe, expect, it, vi } from "vitest";
import { ApiClient } from "./client";

afterEach(() => {
  vi.unstubAllGlobals();
});

function stubResponse(body: unknown) {
  vi.stubGlobal(
    "fetch",
    vi.fn().mockResolvedValue(
      new Response(JSON.stringify(body), {
        status: 201,
        headers: { "Content-Type": "application/json" },
      }),
    ),
  );
}

function createResponse() {
  return {
    session: {
      id: "session-1",
      workspace_id: "workspace-1",
      fleet_id: "fleet-1",
    },
    fleet: {
      id: "fleet-1",
      workspace_id: "workspace-1",
      members: [],
    },
    nodes: [
      {
        id: "node-1",
        session_id: "session-1",
        node_type: "question",
      },
    ],
    edges: [],
    messages: [
      {
        id: "message-1",
        session_id: "session-1",
        sender_type: "system",
      },
    ],
  };
}

describe("ApiClient.createResearchSession response boundary", () => {
  it("accepts a self-consistent kickoff snapshot", async () => {
    stubResponse(createResponse());
    const client = new ApiClient("https://api.example.test");
    await expect(client.createResearchSession({ goal: "Research" })).resolves.toMatchObject({
      session: { id: "session-1" },
      fleet: { id: "fleet-1" },
    });
  });

  it("rejects a kickoff response from another workspace", async () => {
    stubResponse(createResponse());
    const client = new ApiClient("https://api.example.test");
    await expect(
      client.createResearchSession({ goal: "Research" }, "workspace-2"),
    ).rejects.toThrow("identity validation");
  });

  it("rejects malformed responses instead of returning an empty successful session", async () => {
    stubResponse({ session: {}, fleet: {} });
    const client = new ApiClient("https://api.example.test");
    await expect(client.createResearchSession({ goal: "Research" })).rejects.toThrow(
      "schema validation",
    );
  });

  const corruptions: Array<[
    string,
    (response: ReturnType<typeof createResponse>) => void,
  ]> = [
    [
      "empty session",
      (response) => {
        response.session.id = "";
      },
    ],
    [
      "fleet workspace",
      (response) => {
        response.fleet.workspace_id = "workspace-2";
      },
    ],
    [
      "fleet id",
      (response) => {
        response.fleet.id = "fleet-2";
      },
    ],
    [
      "node session",
      (response) => {
        response.nodes[0]!.session_id = "session-2";
      },
    ],
    [
      "message session",
      (response) => {
        response.messages[0]!.session_id = "session-2";
      },
    ],
  ];

  it.each(corruptions)("rejects a conflicting kickoff identity: %s", async (_, corrupt) => {
    const response = createResponse();
    corrupt(response);
    stubResponse(response);
    const client = new ApiClient("https://api.example.test");
    await expect(client.createResearchSession({ goal: "Research" })).rejects.toThrow(
      "identity validation",
    );
  });
});
