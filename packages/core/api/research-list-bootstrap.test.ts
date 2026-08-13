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
        status: 200,
        headers: { "Content-Type": "application/json" },
      }),
    ),
  );
}

function session(id: string, workspaceId = "ws1") {
  return {
    id,
    workspace_id: workspaceId,
    fleet_id: "f1",
    status: "running",
    current_stage: "s1_plan",
  };
}

describe("Research list/bootstrap response boundaries", () => {
  it("keeps a canonical empty session list distinct from malformed data", async () => {
    const client = new ApiClient("https://api.example.test");
    stubResponse({ sessions: [] });
    await expect(client.listResearchSessions("ws1")).resolves.toEqual({ sessions: [] });

    stubResponse({ sessions: "not-an-array" });
    await expect(client.listResearchSessions("ws1")).rejects.toThrow(
      "schema validation",
    );

    stubResponse({});
    await expect(client.listResearchSessions("ws1")).rejects.toThrow(
      "schema validation",
    );
  });

  it("rejects cross-workspace and duplicate sessions", async () => {
    const client = new ApiClient("https://api.example.test");
    stubResponse({ sessions: [session("s1", "ws2")] });
    await expect(client.listResearchSessions("ws1")).rejects.toThrow(
      "identity validation",
    );

    stubResponse({
      sessions: [
        session("s1"),
        session("s1"),
      ],
    });
    await expect(client.listResearchSessions("ws1")).rejects.toThrow(
      "identity validation",
    );
  });

  it("accepts a self-consistent fleet and rejects synthetic empty fallback", async () => {
    const client = new ApiClient("https://api.example.test");
    stubResponse({
      id: "f1",
      workspace_id: "ws1",
      lead_agent_id: "a1",
      members: [{ id: "m1", agent_id: "a1", role: "lead", status: "active" }],
    });
    await expect(client.ensureResearchFleet("ws1")).resolves.toMatchObject({ id: "f1" });

    stubResponse({});
    await expect(client.ensureResearchFleet("ws1")).rejects.toThrow(
      "schema validation",
    );
  });

  it("rejects cross-workspace, duplicate, and dangling-lead fleet identities", async () => {
    const client = new ApiClient("https://api.example.test");
    stubResponse({ id: "f1", workspace_id: "ws2", members: [] });
    await expect(client.ensureResearchFleet("ws1")).rejects.toThrow(
      "identity validation",
    );

    stubResponse({
      id: "f1",
      workspace_id: "ws1",
      lead_agent_id: "missing",
      members: [
        { id: "m1", agent_id: "a1", role: "lead", status: "active" },
        { id: "m1", agent_id: "a2", role: "peer", status: "active" },
      ],
    });
    await expect(client.ensureResearchFleet("ws1")).rejects.toThrow(
      "identity validation",
    );
  });
});
