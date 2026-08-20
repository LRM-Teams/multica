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

  it("accepts a V6 kickoff whose gate findings are null and fleet_id is empty", async () => {
    stubResponse({
      session: {
        id: "session-v6",
        workspace_id: "workspace-1",
        fleet_id: "",
        status: "running",
        current_stage: "s1_plan",
      },
      nodes: [],
      edges: [],
      messages: [],
      run: {
        run: {
          session_id: "session-v6",
          workspace_id: "workspace-1",
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
            max_tasks: 60,
            max_parallel_tasks: 5,
            max_attempts_per_task: 3,
            max_snapshot_bytes: 65536,
            max_result_bytes: 524288,
            max_run_seconds: 28800,
            task_timeout_seconds: 1800,
            stale_after_seconds: 900,
            marginal_gain_threshold: 0.03,
            marginal_gain_rounds: 2,
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
          scope: {},
          audience: "",
          freshness: "",
          language: "follow the user's language",
          source_policy: {},
          run_limits: {
            max_tasks: 60,
            max_parallel_tasks: 5,
            max_attempts_per_task: 3,
            max_snapshot_bytes: 65536,
            max_result_bytes: 524288,
            max_run_seconds: 28800,
            task_timeout_seconds: 1800,
            stale_after_seconds: 900,
            marginal_gain_threshold: 0.03,
            marginal_gain_rounds: 2,
          },
          reason: "v6_bootstrap",
          created_at: "2026-08-20T00:00:00Z",
        },
        questions: [],
        tasks: [],
        attempts: [],
        sources: [],
        observations: [],
        claims: [],
        gate: { passed: true, findings: null },
      },
    });
    const client = new ApiClient("https://api.example.test");
    await expect(
      client.createResearchSession({ goal: "Research" }, "workspace-1"),
    ).resolves.toMatchObject({
      session: { id: "session-v6", fleet_id: "" },
      run: { gate: { passed: true, findings: [] } },
    });
  });

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
