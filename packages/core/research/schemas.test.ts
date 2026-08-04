import { describe, expect, it } from "vitest";
import {
  EMPTY_RESEARCH_SNAPSHOT,
  ListResearchSessionsResponseSchema,
  ResearchSessionSnapshotSchema,
  ResearchNodeCommandResponseSchema,
  EMPTY_RESEARCH_NODE_COMMAND,
  SteerResearchRunResponseSchema,
} from "./schemas";
import { parseWithFallback } from "../api/schema";

describe("research schemas", () => {
  it("accepts a minimal valid snapshot", () => {
    const raw = {
      session: { id: "s1", workspace_id: "w1", title: "t", goal: "g" },
      fleet: { id: "f1", workspace_id: "w1", members: [] },
      nodes: [],
      edges: [],
      sources: [],
      report: null,
      evals: [],
      messages: [],
    };
    const parsed = parseWithFallback(raw, ResearchSessionSnapshotSchema, EMPTY_RESEARCH_SNAPSHOT, {
      endpoint: "test",
    });
    expect(parsed.session.id).toBe("s1");
    expect(parsed.fleet.id).toBe("f1");
  });

  it("falls back on malformed list response", () => {
    const parsed = parseWithFallback(
      { sessions: "nope" },
      ListResearchSessionsResponseSchema,
      { sessions: [] },
      { endpoint: "test" },
    );
    expect(parsed.sessions).toEqual([]);
  });

  it("defaults message card_kind and meta when missing", () => {
    const raw = {
      session: { id: "s1", workspace_id: "w1" },
      fleet: { id: "f1", workspace_id: "w1", members: [] },
      messages: [{ id: "m1", body: "kickoff", sender_type: "system" }],
    };
    const parsed = parseWithFallback(raw, ResearchSessionSnapshotSchema, EMPTY_RESEARCH_SNAPSHOT, {
      endpoint: "test",
    });
    expect(parsed.messages[0]?.card_kind).toBe("chat");
    expect(parsed.messages[0]?.meta).toEqual({});
  });

  it("accepts the durable Research Run snapshot", () => {
    const raw = {
      session: { id: "s1", workspace_id: "w1" },
      fleet: { id: "f1", workspace_id: "w1", members: [] },
      run: {
        run: {
          session_id: "s1",
          workspace_id: "w1",
          fleet_id: "f1",
          created_by: "u1",
          title: "t",
          goal: "g",
          status: "running",
          current_stage: "s2_sources",
          depth_tier: "deep",
          goal_version: 2,
          plan_version: 3,
          state_version: 9,
          orchestrator_version: "research-run-v1",
          config: {
            max_tasks: 180,
            max_parallel_tasks: 10,
            max_attempts_per_task: 4,
            max_snapshot_bytes: 131072,
            max_result_bytes: 1048576,
            max_run_seconds: 86400,
            task_timeout_seconds: 3600,
            stale_after_seconds: 1200,
            marginal_gain_threshold: 0.02,
            marginal_gain_rounds: 3,
          },
          stats: {
            accepted_results: 1,
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
          last_progress_at: "2026-08-03T00:00:00Z",
          next_reconcile_at: "2026-08-03T00:00:15Z",
        },
        contract: {
          goal_version: 2,
          goal: "g",
          scope: {},
          audience: "",
          freshness: "",
          language: "zh",
          source_policy: {},
          run_limits: {
            max_tasks: 180,
            max_parallel_tasks: 10,
            max_attempts_per_task: 4,
            max_snapshot_bytes: 131072,
            max_result_bytes: 1048576,
            max_run_seconds: 86400,
            task_timeout_seconds: 3600,
            stale_after_seconds: 1200,
            marginal_gain_threshold: 0.02,
            marginal_gain_rounds: 3,
          },
          reason: "user_requested",
          created_at: "2026-08-03T00:00:00Z",
        },
        questions: [],
        tasks: [],
        attempts: [],
        gate: { passed: false, findings: [] },
      },
    };
    const parsed = parseWithFallback(raw, ResearchSessionSnapshotSchema, EMPTY_RESEARCH_SNAPSHOT, {
      endpoint: "test",
    });
    expect(parsed.run?.run.goal_version).toBe(2);
  });

  it("drops the optional run projection when that projection is malformed", () => {
    const raw = {
      session: { id: "s1", workspace_id: "w1" },
      fleet: { id: "f1", workspace_id: "w1", members: [] },
      run: { run: { session_id: 7 } },
    };
    const parsed = parseWithFallback(raw, ResearchSessionSnapshotSchema, EMPTY_RESEARCH_SNAPSHOT, {
      endpoint: "test",
    });
    expect(parsed).toBe(EMPTY_RESEARCH_SNAPSHOT);
  });

  it("falls back on a malformed steer response", () => {
    const fallback = null;
    const parsed = parseWithFallback(
      { run: { session_id: 7 } },
      SteerResearchRunResponseSchema,
      fallback,
      { endpoint: "test" },
    );
    expect(parsed).toBe(fallback);
  });

  it("keeps process card fields", () => {
    const raw = {
      session: { id: "s1", workspace_id: "w1" },
      fleet: { id: "f1", workspace_id: "w1", members: [] },
      messages: [
        {
          id: "m1",
          body: "调研团已就位",
          sender_type: "system",
          card_kind: "process",
          meta: { op: "session_kickoff" },
        },
      ],
    };
    const parsed = parseWithFallback(raw, ResearchSessionSnapshotSchema, EMPTY_RESEARCH_SNAPSHOT, {
      endpoint: "test",
    });
    expect(parsed.messages[0]?.card_kind).toBe("process");
    expect((parsed.messages[0]?.meta as { op?: string })?.op).toBe("session_kickoff");
  });

  it("falls back when a node command response drifts", () => {
    const parsed = parseWithFallback(
      { command_id: null, action: "future_action" },
      ResearchNodeCommandResponseSchema,
      EMPTY_RESEARCH_NODE_COMMAND,
      { endpoint: "test" },
    );
    expect(parsed).toBe(EMPTY_RESEARCH_NODE_COMMAND);
  });
});
