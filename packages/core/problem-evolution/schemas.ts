import { z } from "zod";

/**
 * Score and behavior-profile payloads are versioned on the wire. A bump on the
 * Go side must land with a matching change here, because the canvas reads these
 * shapes directly.
 */
export const PROBLEM_EVOLUTION_SCHEMA_VERSION = 1;

export const ProblemEvolutionScoreDimensionSchema = z.object({
  dimension_id: z.string(),
  score: z.number(),
  weight: z.number(),
  hard: z.boolean().optional().default(false),
});

export const ProblemEvolutionScoreSchema = z.object({
  schema_version: z.number(),
  total: z.number(),
  scale: z.string(),
  hard_gate_passed: z.boolean(),
  dimensions: z.array(ProblemEvolutionScoreDimensionSchema),
});

export const ProblemEvolutionBehaviorProfileSchema = z.object({
  schema_version: z.number(),
  kind: z.string(),
  entries: z.array(z.object({ key: z.string(), value: z.number() })),
});

export const ProblemEvolutionRunSchema = z.object({
  id: z.string(),
  workspace_id: z.string(),
  mode: z.string(),
  title: z.string().default(""),
  problem_spec: z.unknown().optional(),
  artifact_type: z.string().default("markdown"),
  status: z.string(),
  stage: z.string().default(""),
  runtime_id: z.string().nullable().optional(),
  model_config: z.unknown().optional(),
  budget_config: z.unknown().optional(),
  stop_config: z.unknown().optional(),
  evaluator_contract_id: z.string().nullable().optional(),
  evaluator_content_hash: z.string().default(""),
  evolver_version: z.string().default(""),
  best_candidate_id: z.string().nullable().optional(),
  final_candidate_id: z.string().nullable().optional(),
  graph_version: z.number(),
  generation: z.number().default(0),
  candidate_count: z.number().default(0),
  model_call_count: z.number().default(0),
  best_score: z.number().default(0),
  total_cost: z.number().default(0),
  harness_proposals: z.number().default(4),
  harness_execute_count: z.number().default(2),
  benchmark_mode: z.boolean().default(false),
  winner_harness_id: z.string().nullable().optional(),
  // Blind validation is scored once, on a sample the search never saw, so it is
  // reported alongside best_score rather than replacing it.
  blind_score: z.number().nullable().optional(),
  overfit_gap: z.number().nullable().optional(),
  task_set_id: z.string().nullable().optional(),
  stop_reason: z.string().default(""),
  failure_reason: z.string().default(""),
  started_at: z.string().nullable().optional(),
  finished_at: z.string().nullable().optional(),
  created_at: z.string().default(""),
  updated_at: z.string().default(""),
});

export const ProblemEvolutionEvaluatorContractSchema = z.object({
  id: z.string(),
  mode: z.string(),
  status: z.string(),
  version: z.number(),
  contract: z.unknown().optional(),
  feedback_policy: z.unknown().optional(),
  content_hash: z.string().default(""),
  frozen_at: z.string().nullable().optional(),
  created_at: z.string().default(""),
  updated_at: z.string().default(""),
});

export const ProblemEvolutionCandidateSchema = z.object({
  id: z.string(),
  run_id: z.string(),
  external_ref: z.string(),
  generation: z.number(),
  lane: z.string(),
  operator: z.string(),
  status: z.string(),
  score: ProblemEvolutionScoreSchema.nullable().optional(),
  behavior_profile: ProblemEvolutionBehaviorProfileSchema.nullable().optional(),
  feasible: z.boolean().default(true),
  feedback_rounds: z.number().default(0),
  artifact_ref: z.string().default(""),
  artifact_hash: z.string().default(""),
  summary: z.string().default(""),
  change_summary: z.string().default(""),
  failure_class: z.string().default(""),
  runtime_seconds: z.number().default(0),
  token_usage: z.unknown().optional(),
  cost: z.number().default(0),
  created_at: z.string().default(""),
  updated_at: z.string().default(""),
});

export const ProblemEvolutionEventSchema = z.object({
  id: z.string(),
  seq: z.number(),
  client_event_id: z.string(),
  event_type: z.string(),
  candidate_id: z.string().nullable().optional(),
  actor_type: z.string().default(""),
  actor_id: z.string().default(""),
  payload: z.unknown().optional(),
  created_at: z.string().default(""),
});

export const ProblemEvolutionCandidateEdgeSchema = z.object({
  parent_id: z.string(),
  child_id: z.string(),
  relation: z.string(),
  parent_index: z.number().default(0),
});

export const ProblemEvolutionTaskSetSchema = z.object({
  id: z.string(),
  workspace_id: z.string(),
  source: z.string().default(""),
  dataset_ref: z.string(),
  dataset_revision: z.string().default(""),
  task_names: z.array(z.string()).default([]),
  holdout_task_names: z.array(z.string()).default([]),
  rollouts_per_task: z.number().default(1),
  max_parallel: z.number().default(4),
  created_at: z.string().default(""),
  updated_at: z.string().default(""),
});

export const ProblemEvolutionSnapshotSchema = z.object({
  run: ProblemEvolutionRunSchema,
  evaluator: ProblemEvolutionEvaluatorContractSchema.nullable().optional(),
  candidates: z.array(ProblemEvolutionCandidateSchema),
  edges: z.array(ProblemEvolutionCandidateEdgeSchema).default([]),
  graph_version: z.number(),
  latest_seq: z.number().default(0),
});

export const ProblemEvolutionResultSchema = z.object({
  best_candidate_ref: z.string().default(""),
  search_best_score: z.number().default(0),
  blind_score: z.number().nullable().optional(),
  overfit_gap: z.number().nullable().optional(),
  blind_validated: z.boolean().default(false),
  // scope_claim is how far the run's result may be generalised. The UI shows it
  // verbatim rather than deriving its own claim from the scores.
  scope_claim: z.string().default("search_sample_only"),
  winner_harness_ref: z.string().default(""),
  candidates_total: z.number().default(0),
  generations_total: z.number().default(0),
  total_cost_usd: z.number().default(0),
  stop_reason: z.string().default(""),
  failure_reason: z.string().default(""),
  feedback_bandwidth: z.string().default(""),
});

export const ProblemEvolutionReproductionSchema = z.object({
  replayable: z.boolean().default(false),
  missing_for_replay: z.array(z.string()).default([]),
  schema_version: z.number().default(1),
  evaluator_content_hash: z.string().default(""),
  evolver_version: z.string().default(""),
  search_seed: z.number().default(0),
  blind_seed: z.number().default(0),
  model_config: z.unknown().optional(),
  stop_config: z.unknown().optional(),
  allowed_event_types: z.array(z.string()).default([]),
});

export const ProblemEvolutionExportSchema = z.object({
  schema_version: z.number(),
  run: ProblemEvolutionRunSchema,
  evaluator: ProblemEvolutionEvaluatorContractSchema.nullable().optional(),
  candidates: z.array(ProblemEvolutionCandidateSchema).default([]),
  edges: z.array(ProblemEvolutionCandidateEdgeSchema).default([]),
  evaluations: z.array(z.unknown()).default([]),
  artifacts: z.array(z.unknown()).default([]),
  harnesses: z.array(z.unknown()).default([]),
  result: ProblemEvolutionResultSchema,
  reproduction: ProblemEvolutionReproductionSchema,
  secret_audit: z.unknown().optional(),
});

export const ProblemEvolutionComparisonSchema = z.object({
  left: ProblemEvolutionResultSchema,
  right: ProblemEvolutionResultSchema,
  comparable: z.boolean(),
  differences: z.array(z.string()).default([]),
  search_delta: z.number().default(0),
  blind_delta: z.number().nullable().optional(),
  preferred_run_id: z.string().default(""),
  preference_basis: z.string().default(""),
});

export const ProblemEvolutionRunListSchema = z.array(ProblemEvolutionRunSchema);
export const ProblemEvolutionEventListSchema = z.array(ProblemEvolutionEventSchema);

export type ProblemEvolutionRun = z.infer<typeof ProblemEvolutionRunSchema>;
export type ProblemEvolutionEvaluatorContract = z.infer<
  typeof ProblemEvolutionEvaluatorContractSchema
>;
export type ProblemEvolutionCandidate = z.infer<typeof ProblemEvolutionCandidateSchema>;
export type ProblemEvolutionCandidateEdge = z.infer<
  typeof ProblemEvolutionCandidateEdgeSchema
>;
export type ProblemEvolutionTaskSet = z.infer<typeof ProblemEvolutionTaskSetSchema>;
export type ProblemEvolutionEvent = z.infer<typeof ProblemEvolutionEventSchema>;
export type ProblemEvolutionSnapshot = z.infer<typeof ProblemEvolutionSnapshotSchema>;
export type ProblemEvolutionScore = z.infer<typeof ProblemEvolutionScoreSchema>;
export type ProblemEvolutionResult = z.infer<typeof ProblemEvolutionResultSchema>;
export type ProblemEvolutionReproduction = z.infer<
  typeof ProblemEvolutionReproductionSchema
>;
export type ProblemEvolutionExport = z.infer<typeof ProblemEvolutionExportSchema>;
export type ProblemEvolutionComparison = z.infer<
  typeof ProblemEvolutionComparisonSchema
>;

export const EMPTY_PROBLEM_EVOLUTION_SNAPSHOT: ProblemEvolutionSnapshot = {
  run: {
    id: "",
    workspace_id: "",
    mode: "solution",
    title: "",
    artifact_type: "markdown",
    status: "draft",
    stage: "",
    evaluator_content_hash: "",
    evolver_version: "",
    graph_version: 0,
    generation: 0,
    candidate_count: 0,
    model_call_count: 0,
    best_score: 0,
    total_cost: 0,
    harness_proposals: 4,
    harness_execute_count: 2,
    benchmark_mode: false,
    stop_reason: "",
    failure_reason: "",
    created_at: "",
    updated_at: "",
  },
  candidates: [],
  edges: [],
  graph_version: 0,
  latest_seq: 0,
};
