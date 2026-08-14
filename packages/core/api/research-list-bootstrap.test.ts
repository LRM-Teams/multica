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

  it("accepts homepage progress and remains compatible when it is absent", async () => {
    const client = new ApiClient("https://api.example.test");
    stubResponse({
      sessions: [
        {
          ...session("s1"),
          list_progress: {
            task_total: 12,
            task_completed: 5,
            task_running: 3,
            task_blocked: 1,
            evidence_count: 18,
            today_evidence_count: 4,
            node_count: 6,
            open_question_count: 2,
            awaiting_user_action: false,
            attention_kind: "blocked_tasks",
            recoverable: true,
            last_progress_at: "2026-08-14T08:41:00Z",
          },
          active_assignments: [
            {
              agent_id: "a1",
              role: "validator",
              task_id: "t1",
              task_title: "Verify pricing evidence",
              state: "running",
            },
          ],
          latest_outcomes: [
            {
              id: "o1",
              kind: "claim",
              title: "Pricing is usage based",
              verification_state: "supported",
              created_at: "2026-08-14T08:40:00Z",
            },
          ],
        },
        session("s2"),
      ],
    });

    await expect(client.listResearchSessions("ws1")).resolves.toMatchObject({
      sessions: [
        {
          id: "s1",
          list_progress: { task_completed: 5, evidence_count: 18 },
          active_assignments: [{ task_id: "t1" }],
          latest_outcomes: [{ id: "o1", kind: "claim" }],
        },
        { id: "s2" },
      ],
    });
  });

  it("normalizes nullable projections and preserves unknown attention states", async () => {
    const client = new ApiClient("https://api.example.test");
    stubResponse({
      sessions: [
        {
          ...session("s1"),
          list_progress: {
            attention_kind: "future_attention_kind",
          },
          active_assignments: null,
          latest_outcomes: null,
        },
      ],
    });

    await expect(client.listResearchSessions("ws1")).resolves.toMatchObject({
      sessions: [
        {
          id: "s1",
          list_progress: { attention_kind: "future_attention_kind" },
          active_assignments: undefined,
          latest_outcomes: undefined,
        },
      ],
    });
  });

  it("ignores malformed optional projections without dropping the session list", async () => {
    const client = new ApiClient("https://api.example.test");
    stubResponse({
      sessions: [
        {
          ...session("s1"),
          list_progress: { task_total: "12" },
          active_assignments: {},
          latest_outcomes: "invalid",
        },
      ],
    });

    await expect(client.listResearchSessions("ws1")).resolves.toMatchObject({
      sessions: [
        {
          id: "s1",
          list_progress: undefined,
          active_assignments: undefined,
          latest_outcomes: undefined,
        },
      ],
    });
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
