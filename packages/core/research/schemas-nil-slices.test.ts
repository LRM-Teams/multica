import { describe, expect, it } from "vitest";
import {
  CreateResearchSessionResponseSchema,
  ListResearchProductRoundCardsResponseSchema,
  ListResearchSessionsResponseSchema,
  ResearchFleetSchema,
  ResearchSessionSnapshotSchema,
} from "./schemas";
import { TypedGraphResponseSchema } from "./graph-typed";

/**
 * Go encodes a nil slice as JSON null. These fixtures are the live V6
 * wire shape (and the next slices that appear after planning) — the
 * client uses safeParse and throws on failure, so `.default([])` is not
 * enough.
 */
function v6Run(overrides: Record<string, unknown> = {}) {
  return {
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
    ...overrides,
  };
}

function v6Contract() {
  return {
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
  };
}

describe("research schemas accept Go nil slices", () => {
  it("parses a V6 GET snapshot whose gate findings are null", () => {
    const parsed = ResearchSessionSnapshotSchema.safeParse({
      session: {
        id: "session-v6",
        workspace_id: "workspace-1",
        fleet_id: "",
        status: "running",
        current_stage: "s1_plan",
      },
      fleet: {
        id: "fleet-1",
        workspace_id: "workspace-1",
        members: null,
      },
      nodes: null,
      edges: null,
      sources: null,
      report: null,
      evals: null,
      messages: null,
      thought_strategies: null,
      run: {
        run: v6Run(),
        contract: { ...v6Contract(), scope: null, source_policy: null },
        questions: null,
        tasks: null,
        attempts: null,
        sources: null,
        observations: null,
        claims: null,
        gate: { passed: true, findings: null },
      },
    });
    expect(parsed.success).toBe(true);
    if (!parsed.success) return;
    expect(parsed.data.fleet.members).toEqual([]);
    expect(parsed.data.nodes).toEqual([]);
    expect(parsed.data.run?.questions).toEqual([]);
    expect(parsed.data.run?.gate.findings).toEqual([]);
    expect(parsed.data.run?.contract.scope).toEqual({});
  });

  it("parses a V6 create body and a session list that omit fleet membership", () => {
    const created = CreateResearchSessionResponseSchema.safeParse({
      session: {
        id: "session-v6",
        workspace_id: "workspace-1",
        fleet_id: "",
        status: "running",
        current_stage: "s1_plan",
      },
      nodes: null,
      edges: null,
      messages: null,
      run: {
        run: v6Run(),
        contract: v6Contract(),
        questions: [],
        tasks: [],
        attempts: [],
        sources: [],
        observations: [],
        claims: [],
        gate: { passed: true, findings: null },
      },
    });
    expect(created.success).toBe(true);

    const listed = ListResearchSessionsResponseSchema.safeParse({
      sessions: [
        {
          id: "session-v6",
          workspace_id: "workspace-1",
          fleet_id: "",
          status: "running",
          current_stage: "s1_plan",
          fleet_preview: null,
        },
      ],
    });
    expect(listed.success).toBe(true);
    if (!listed.success) return;
    expect(listed.data.sessions[0]?.fleet_id).toBe("");
  });

  it("normalizes a planned method and claims whose slices are still nil", () => {
    const parsed = ResearchSessionSnapshotSchema.safeParse({
      session: { id: "session-v6", workspace_id: "workspace-1" },
      fleet: { id: "fleet-1", workspace_id: "workspace-1" },
      run: {
        run: v6Run(),
        contract: v6Contract(),
        method: {
          goal_version: 1,
          plan_version: 1,
          decision_question: "q",
          method_rationale: "r",
          analysis_methods: null,
          evidence_requirements: null,
          evidence_standards: [
            {
              client_key: "k",
              purpose: "p",
              minimum_independent_sources: 1,
              required_source_traits: null,
              minimum_strength: 0.5,
              minimum_directness: 0.5,
              minimum_method_fit: 0.5,
            },
          ],
          inclusion_criteria: null,
          exclusion_criteria: null,
          source_strategy: null,
          counterevidence_strategy: null,
          stopping_conditions: null,
          uncertainties: null,
          planning_risks: null,
          created_by_task_id: "t1",
          created_by_agent_id: "a1",
          created_at: "2026-08-20T00:00:00Z",
        },
        claims: [
          {
            id: "c1",
            client_key: "ck",
            text: "claim",
            significance: "high",
            confidence: 0.4,
            status: "open",
            goal_version: 1,
            plan_version: 1,
            evidence: null,
            created_at: "2026-08-20T00:00:00Z",
            updated_at: "2026-08-20T00:00:00Z",
          },
        ],
        sources: [
          {
            id: "src1",
            canonical_url: "https://example.test",
            title: "src",
            publisher: "",
            source_class: "other",
            evidence_traits: null,
            independence_key: "k",
            retrieved_at: "2026-08-20T00:00:00Z",
            content_hash: "h",
            snapshot_excerpt: "",
            metadata: null,
            verification_status: "unverified",
            created_at: "2026-08-20T00:00:00Z",
          },
        ],
        gate: { passed: false, findings: null },
      },
    });
    expect(parsed.success).toBe(true);
    if (!parsed.success) return;
    expect(parsed.data.run?.method?.analysis_methods).toEqual([]);
    expect(parsed.data.run?.method?.evidence_standards?.[0]?.required_source_traits).toEqual([]);
    expect(parsed.data.run?.claims[0]?.evidence).toEqual([]);
    expect(parsed.data.run?.sources[0]?.evidence_traits).toEqual([]);
  });

  it("accepts a fleet and product-round list with null arrays", () => {
    expect(
      ResearchFleetSchema.safeParse({
        id: "f1",
        workspace_id: "w1",
        members: null,
      }).success,
    ).toBe(true);
    const rounds = ListResearchProductRoundCardsResponseSchema.safeParse({
      rounds: null,
    });
    expect(rounds.success).toBe(true);
    if (!rounds.success) return;
    expect(rounds.data.rounds).toEqual([]);
  });

  it("accepts a typed graph whose collection fields are null", () => {
    const parsed = TypedGraphResponseSchema.safeParse({
      session_id: "session-v6",
      graph_version: 1,
      nodes: [
        {
          id: "n1",
          merged_from: null,
          child_ids: null,
          children_of: null,
        },
      ],
      edges: null,
      clusters: null,
      lineage: null,
    });
    expect(parsed.success).toBe(true);
    if (!parsed.success) return;
    expect(parsed.data.nodes[0]?.merged_from).toEqual([]);
    expect(parsed.data.edges).toEqual([]);
    expect(parsed.data.clusters).toEqual([]);
  });
});
