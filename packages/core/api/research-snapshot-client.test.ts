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

  it("accepts a V6 snapshot whose Go slices serialized as null", async () => {
    stubSnapshot(
      snapshot({
        fleet: { id: "f1", workspace_id: "ws1", members: null },
        nodes: null,
        edges: null,
        sources: null,
        evals: null,
        messages: null,
        thought_strategies: null,
        run: {
          run: {
            session_id: "s1",
            workspace_id: "ws1",
            fleet_id: "",
            created_by: "u1",
            title: "t",
            goal: "g",
            status: "running",
            current_stage: "s1_plan",
            depth_tier: "standard",
            goal_version: 1,
            plan_version: 1,
            state_version: 1,
            orchestrator_version: "research-run-v6",
            config: {
              max_tasks: 1,
              max_parallel_tasks: 1,
              max_attempts_per_task: 1,
              max_snapshot_bytes: 1,
              max_result_bytes: 1,
              max_run_seconds: 1,
              task_timeout_seconds: 1,
              stale_after_seconds: 1,
              marginal_gain_threshold: 0,
              marginal_gain_rounds: 1,
            },
            stats: {
              accepted_results: 0,
              evidence_batches: 0,
              low_gain_streak: 0,
              last_coverage_delta: 0,
              last_measured_gain: 0,
              last_confidence: 0,
              sources_created: 0,
              observations_created: 0,
              claims_created: 0,
              budget_exhaustion_count: 0,
            },
            last_progress_at: "2026-08-20T00:00:00Z",
            next_reconcile_at: "2026-08-20T00:00:00Z",
          },
          contract: {
            goal_version: 1,
            goal: "g",
            scope: null,
            audience: "",
            freshness: "",
            language: "",
            source_policy: null,
            run_limits: {
              max_tasks: 1,
              max_parallel_tasks: 1,
              max_attempts_per_task: 1,
              max_snapshot_bytes: 1,
              max_result_bytes: 1,
              max_run_seconds: 1,
              task_timeout_seconds: 1,
              stale_after_seconds: 1,
              marginal_gain_threshold: 0,
              marginal_gain_rounds: 1,
            },
            reason: "v6_bootstrap",
            created_at: "2026-08-20T00:00:00Z",
          },
          questions: null,
          tasks: null,
          attempts: null,
          sources: null,
          observations: null,
          claims: null,
          gate: { passed: true, findings: null },
        },
      }),
    );
    const client = new ApiClient("https://api.example.test");
    await expect(client.getResearchSessionSnapshot("s1")).resolves.toMatchObject({
      session: { id: "s1" },
      fleet: { id: "f1", members: [] },
      nodes: [],
      run: { gate: { passed: true, findings: [] }, questions: [] },
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
