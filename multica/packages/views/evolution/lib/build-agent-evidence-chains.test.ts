import { describe, expect, it } from "vitest";
import type { Agent, EvolutionReviewSubmission, EvolutionUnitMetric } from "@multica/core/types";
import {
  buildAgentEvidenceSummaries,
  filterChains,
} from "./build-agent-evidence-chains";

function agent(id: string, name: string): Agent {
  return {
    id,
    name,
    display_name: name,
    workspace_id: "ws",
    owner_id: "u1",
  } as Agent;
}

function submission(
  overrides: Partial<EvolutionReviewSubmission> &
    Pick<EvolutionReviewSubmission, "id" | "source_agent_id" | "status">,
): EvolutionReviewSubmission {
  return {
    workspace_id: "ws",
    unit_type: "memory",
    local_unit_id: overrides.local_unit_id ?? overrides.id,
    title: "Fleet strip four states",
    summary: "mode chip + frosted card",
    content: "",
    content_hash: "h",
    bundle_hash: "b",
    bundle_ref: "ref",
    sensitivity: "low",
    confidence: "0.8",
    suggested_scope: "agent-private",
    evidence: { source: "daily", source_date: "2026-08-02", evidence_refs: ["LRM-980"] },
    applies: {
      scope: "agent",
      tags: [],
      tools: [],
      task_types: [],
      project_types: [],
      languages: [],
      frameworks: [],
    },
    tags: [],
    tools: [],
    task_types: [],
    project_types: [],
    languages: [],
    frameworks: [],
    reject_reason: "",
    review_decision: "",
    review_risk_level: "",
    review_reason: "",
    review_metadata: {},
    created_at: "2026-08-02T02:00:00Z",
    ...overrides,
  } as EvolutionReviewSubmission;
}

describe("buildAgentEvidenceSummaries (LRM-986)", () => {
  it("builds a complete Write → Promote → Used chain", () => {
    const agents = [agent("a1", "UI Designer"), agent("a2", "FE")];
    const submissions = [
      submission({
        id: "s1",
        source_agent_id: "a1",
        status: "promoted",
        local_unit_id: "u1",
        promoted_unit_id: "pu1",
        reviewed_at: "2026-08-02T03:40:00Z",
        review_decision: "promote",
        review_confidence: 0.82,
      }),
    ];
    const metrics: EvolutionUnitMetric[] = [
      {
        unit_id: "pu1",
        local_unit_id: "u1",
        unit_type: "memory",
        title: "Fleet strip four states",
        injected_count: 2,
        used_count: 1,
        success_count: 1,
        failure_count: 0,
        ignored_count: 0,
        conflict_count: 0,
        success_rate: 1,
        last_used_at: "2026-08-02T09:36:00Z",
      },
    ];

    const rows = buildAgentEvidenceSummaries(agents, submissions, metrics);
    expect(rows[0]?.agent.id).toBe("a1");
    expect(rows[0]?.chainComplete).toBe(true);
    expect(rows[0]?.writes).toBe(1);
    expect(rows[0]?.promoted).toBe(1);
    expect(rows[0]?.used).toBe(1);
    expect(rows[0]?.chains[0]?.nodes.map((n) => n.kind)).toEqual([
      "write",
      "promote",
      "used",
    ]);
    expect(rows[1]?.subtitleKey).toBe("evidenceNoWrites");
  });

  it("shows empty-friendly subtitle when agent has no submissions", () => {
    const rows = buildAgentEvidenceSummaries([agent("a1", "Scout")], [], []);
    expect(rows[0]?.writes).toBe(0);
    expect(rows[0]?.subtitleKey).toBe("evidenceNoWrites");
    expect(rows[0]?.chains).toEqual([]);
  });

  it("adds pending_use when promoted but unused", () => {
    const submissions = [
      submission({
        id: "s1",
        source_agent_id: "a1",
        status: "promoted",
        local_unit_id: "u1",
        promoted_unit_id: "pu1",
      }),
    ];
    const rows = buildAgentEvidenceSummaries([agent("a1", "UI")], submissions, []);
    expect(rows[0]?.chains[0]?.nodes.map((n) => n.kind)).toEqual([
      "write",
      "promote",
      "pending_use",
    ]);
    expect(rows[0]?.chainComplete).toBe(false);
  });

  it("filters promoted/used chains", () => {
    const submissions = [
      submission({ id: "s1", source_agent_id: "a1", status: "candidate", local_unit_id: "u1" }),
      submission({
        id: "s2",
        source_agent_id: "a1",
        status: "promoted",
        local_unit_id: "u2",
        promoted_unit_id: "pu2",
      }),
    ];
    const metrics: EvolutionUnitMetric[] = [
      {
        unit_id: "pu2",
        local_unit_id: "u2",
        unit_type: "memory",
        title: "x",
        injected_count: 1,
        used_count: 3,
        success_count: 1,
        failure_count: 0,
        ignored_count: 0,
        conflict_count: 0,
        success_rate: 1,
        last_used_at: "2026-08-02T09:36:00Z",
      },
    ];
    const rows = buildAgentEvidenceSummaries([agent("a1", "UI")], submissions, metrics);
    const chains = rows[0]!.chains;
    expect(filterChains(chains, "promote")).toHaveLength(1);
    expect(filterChains(chains, "used")).toHaveLength(1);
    expect(filterChains(chains, "all")).toHaveLength(2);
  });
});
