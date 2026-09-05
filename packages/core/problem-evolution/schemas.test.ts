import { describe, expect, it } from "vitest";
import {
  ProblemEvolutionCandidateSchema,
  ProblemEvolutionSnapshotSchema,
  PROBLEM_EVOLUTION_SCHEMA_VERSION,
} from "./schemas";

describe("ProblemEvolutionCandidateSchema", () => {
  it("keeps an unevaluated candidate's score null rather than zero", () => {
    // "not evaluated" and "scored zero" drive different UI, so a missing score
    // must not collapse into a 0 total.
    const parsed = ProblemEvolutionCandidateSchema.parse({
      id: "cand-1",
      run_id: "run-1",
      external_ref: "c1",
      generation: 0,
      lane: "baseline",
      operator: "baseline",
      status: "producing",
      score: null,
    });
    expect(parsed.score).toBeNull();
    expect(parsed.cost).toBe(0);
  });

  it("parses a scored candidate", () => {
    const parsed = ProblemEvolutionCandidateSchema.parse({
      id: "cand-1",
      run_id: "run-1",
      external_ref: "c1",
      generation: 1,
      lane: "repair",
      operator: "repair",
      status: "selectable",
      score: {
        schema_version: PROBLEM_EVOLUTION_SCHEMA_VERSION,
        total: 0.62,
        scale: "unit_interval",
        hard_gate_passed: false,
        dimensions: [{ dimension_id: "correctness", score: 0.62, weight: 1 }],
      },
    });
    expect(parsed.score?.total).toBeCloseTo(0.62);
    expect(parsed.score?.hard_gate_passed).toBe(false);
  });
});

describe("ProblemEvolutionSnapshotSchema", () => {
  it("requires the graph version the canvas orders updates by", () => {
    const result = ProblemEvolutionSnapshotSchema.safeParse({
      run: {
        id: "run-1",
        workspace_id: "ws-1",
        mode: "solution",
        status: "running",
        graph_version: 3,
      },
      candidates: [],
      graph_version: 3,
    });
    expect(result.success).toBe(true);

    const missingVersion = ProblemEvolutionSnapshotSchema.safeParse({
      run: {
        id: "run-1",
        workspace_id: "ws-1",
        mode: "solution",
        status: "running",
        graph_version: 3,
      },
      candidates: [],
      graph_version: "3",
    });
    expect(missingVersion.success).toBe(false);
  });
});
