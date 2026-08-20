import { z } from "zod";
import type {
  ListResearchSessionsResponse,
  ResearchFleet,
  ResearchSessionSnapshot,
} from "../types/research";

const ResearchFleetMemberSchema = z
  .object({
    id: z.string(),
    agent_id: z.string(),
    role: z.string(),
    status: z.string(),
    is_lead: z.boolean().optional().default(false),
    name: z.string().optional(),
    display_name: z.string().optional(),
    avatar_url: z.string().nullable().optional(),
  })
  .passthrough();

const ResearchFleetPreviewMemberSchema = z
  .object({
    agent_id: z.string(),
    name: z.string().optional(),
    display_name: z.string().optional(),
    avatar_url: z.string().nullable().optional(),
    role: z.string().optional(),
    is_lead: z.boolean().optional(),
  })
  .passthrough();

export const ResearchFleetSchema = z
  .object({
    id: z.string(),
    workspace_id: z.string(),
    lead_agent_id: z.string().nullable().optional().default(null),
    members: nullishArray(ResearchFleetMemberSchema),
    created_at: z.string().optional().default(""),
    updated_at: z.string().optional().default(""),
  })
  .passthrough();

const ResearchSessionListProgressSchema = z.object({
  task_total: z.number().optional().default(0),
  task_completed: z.number().optional().default(0),
  task_running: z.number().optional().default(0),
  task_blocked: z.number().optional().default(0),
  evidence_count: z.number().optional().default(0),
  today_evidence_count: z.number().optional().default(0),
  node_count: z.number().optional().default(0),
  open_question_count: z.number().optional().default(0),
  awaiting_user_action: z.boolean().optional().default(false),
  attention_kind: z.string().nullable().optional(),
  recoverable: z.boolean().optional().default(false),
  last_progress_at: z.string().nullable().optional(),
}).passthrough();

const ResearchActiveAssignmentsSchema = z.array(z.object({
  agent_id: z.string(),
  role: z.string().optional().default(""),
  task_id: z.string(),
  task_title: z.string().optional().default(""),
  state: z.string().optional().default("running"),
}).passthrough());

const ResearchLatestOutcomesSchema = z.array(z.object({
  id: z.string(),
  kind: z.string(),
  title: z.string().optional().default(""),
  summary: z.string().nullable().optional(),
  verification_state: z.string().optional().default(""),
  created_at: z.string().optional().default(""),
}).passthrough());

function safeOptionalProjection<T extends z.ZodTypeAny>(schema: T) {
  return z.unknown().transform((value): z.output<T> | undefined => {
    if (value == null) return undefined;
    const parsed = schema.safeParse(value);
    return parsed.success ? parsed.data : undefined;
  });
}

/** Go encodes a nil slice as JSON null. Zod `.default([])` only covers undefined. */
function nullishArray<T extends z.ZodTypeAny>(schema: T) {
  return z.preprocess((value) => (value == null ? [] : value), z.array(schema));
}

/** Go encodes a nil map / RawMessage as JSON null. */
function nullishRecord() {
  return z.preprocess(
    (value) => (value == null ? {} : value),
    z.record(z.string(), z.unknown()),
  );
}

export const ResearchSessionSchema = z
  .object({
    id: z.string(),
    workspace_id: z.string(),
    fleet_id: z.string().optional().default(""),
    created_by: z.string().optional().default(""),
    title: z.string().optional().default(""),
    goal: z.string().optional().default(""),
    status: z.string().optional().default("running"),
    current_stage: z.string().optional().default("s1_plan"),
    project_id: z.string().nullable().optional().default(null),
    channel_id: z.string().nullable().optional().default(null),
    handoff_summary: z.string().nullable().optional().default(null),
    created_at: z.string().optional().default(""),
    updated_at: z.string().optional().default(""),
    fleet_preview: nullishArray(ResearchFleetPreviewMemberSchema),
    depth_tier: z.string().optional(),
    product_round: z.number().optional(),
    product_round_budget: z.number().optional(),
    list_progress: safeOptionalProjection(ResearchSessionListProgressSchema),
    active_assignments: safeOptionalProjection(ResearchActiveAssignmentsSchema),
    latest_outcomes: safeOptionalProjection(ResearchLatestOutcomesSchema),
    orchestrator_version: z.string().optional().default(""),
    director_agent_id: z.string().nullable().optional().default(null),
  })
  .passthrough();

export const ResearchPresenceResponseSchema = z
  .object({
    session_id: z.string().optional().default(""),
    presence: z.preprocess(
      (value) => (value === null ? undefined : value),
      z
        .record(
          z.string(),
          z
            .object({
              activity: z.string().optional(),
              updated_at: z.number().optional(),
              updatedAt: z.number().optional(),
              phase: z.string().optional(),
              role: z.string().optional(),
              fleet_member_id: z.string().nullable().optional(),
              task_id: z.string().nullable().optional(),
              node_id: z.string().nullable().optional(),
              branch_id: z.string().nullable().optional(),
              stage: z.string().nullable().optional(),
              expires_at: z.number().nullable().optional(),
              stale_reason: z.string().nullable().optional(),
            })
            .passthrough(),
        )
        .optional()
        .default({}),
    ),
  })
  .passthrough();

export const ResearchNodeCommandResponseSchema = z.object({
  command_id: z.string(),
  action: z.enum(["continue", "fork", "retry", "reassign"]),
  client_request_id: z.string(),
  replayed: z.boolean().optional().default(false),
  state_version: z.number(),
  queued: z.boolean().optional().default(false),
  assigned: z.string().nullable().optional(),
}).passthrough();

export const EMPTY_RESEARCH_NODE_COMMAND: import("../types/research").ResearchNodeCommandResponse = {
  command_id: "", action: "continue", client_request_id: "", replayed: false, state_version: 0, queued: false,
};

export const ResearchProductRoundCardSchema = z
  .object({
    id: z.string(),
    session_id: z.string().optional().default(""),
    round_number: z.number(),
    decision: z.string(),
    coverage_gaps: z.unknown().optional().default([]),
    confidence_note: z.string().optional().default(""),
    budget_used: z.number().optional().default(0),
    budget_remaining: z.number().optional().default(0),
    goal_patch_proposal: z.string().nullable().optional().default(null),
    next_round_focus: z.string().nullable().optional().default(null),
    decided_by_agent_id: z.string().nullable().optional().default(null),
    created_at: z.string().optional().default(""),
  })
  .passthrough();

export const ListResearchProductRoundCardsResponseSchema = z
  .object({
    rounds: nullishArray(ResearchProductRoundCardSchema),
  })
  .passthrough();

export const EMPTY_RESEARCH_PRODUCT_ROUNDS: {
  rounds: import("../types/research").ResearchProductRoundCard[];
} = { rounds: [] };
const ResearchGraphNodeSchema = z
  .object({
    id: z.string(),
    session_id: z.string().optional().default(""),
    node_type: z.string(),
    title: z.string().optional().default(""),
    summary: z.string().optional().default(""),
    status: z.string().optional().default("active"),
    actor_agent_id: z.string().nullable().optional().default(null),
    payload: z.unknown().optional().default({}),
    confidence: z.number().nullable().optional(),
    created_at: z.string().optional().default(""),
    updated_at: z.string().optional().default(""),
  })
  .passthrough();

const ResearchGraphEdgeSchema = z
  .object({
    id: z.string(),
    session_id: z.string().optional().default(""),
    from_node_id: z.string(),
    to_node_id: z.string(),
    edge_type: z.string(),
    created_at: z.string().optional().default(""),
  })
  .passthrough();

const ResearchSourceSchema = z
  .object({
    id: z.string(),
    session_id: z.string().optional().default(""),
    url: z.string().optional().default(""),
    title: z.string().optional().default(""),
    source_class: z.string().optional().default("other"),
    credibility_weight: z.number().nullable().optional(),
    stance: z.string().optional().default(""),
    relevance: z.number().nullable().optional(),
    summary: z.string().optional().default(""),
    excerpt: z.string().optional().default(""),
    payload: z.unknown().optional().default({}),
    created_at: z.string().optional().default(""),
    updated_at: z.string().optional().default(""),
  })
  .passthrough();

const ResearchReportSchema = z
  .object({
    id: z.string(),
    session_id: z.string().optional().default(""),
    revision: z.number().optional().default(1),
    content_md: z.string().optional().default(""),
    structured: z.unknown().optional().default({}),
    created_at: z.string().optional().default(""),
    updated_at: z.string().optional().default(""),
  })
  .passthrough();

const ResearchStageEvalSchema = z
  .object({
    id: z.string(),
    session_id: z.string().optional().default(""),
    stage: z.string().optional().default(""),
    passed: z.boolean().optional().default(false),
    score: z.number().optional().default(0),
    findings: z.unknown().optional().default([]),
    remediation: z.string().optional().default(""),
    created_at: z.string().optional().default(""),
  })
  .passthrough();

const ResearchMatchDecisionSchema = z
  .object({
    utterance_id: z.string(),
    confidence: z.number().optional(),
    primary_anchor_node_id: z.string().optional(),
    matched_node_ids: nullishArray(z.string()),
    decisions: nullishArray(
      z.object({
        node_id: z.string(),
        action: z.enum(["continue", "branch_after", "deprecate", "pending_confirm"]),
        reason: z.string().optional(),
      }),
    ),
  })
  .passthrough();

export const ResearchMessageSchema = z
  .object({
    id: z.string(),
    session_id: z.string().optional().default(""),
    sender_type: z.string().optional().default("user"),
    sender_id: z.string().nullable().optional().default(null),
    target_agent_id: z.string().nullable().optional().default(null),
    body: z.string().optional().default(""),
    card_kind: z.string().optional().default("chat"),
    meta: z.unknown().optional().default({}),
    match_decision: ResearchMatchDecisionSchema.optional(),
    created_at: z.string().optional().default(""),
  })
  .passthrough();

/** LRM-1318 thought/strategy panel row (LRM-1306). */
const ResearchThoughtStrategySchema = z
  .object({
    node_id: z.string(),
    rationale: z.string().optional().default(""),
    expected_outcome: z.string().optional().default(""),
    strategy_label: z.string().nullable().optional(),
    strategy_revision: z.string().nullable().optional(),
    state: z.string().optional().default("active"),
    updated_at: z.string().optional(),
  })
  .passthrough();

export const ResearchRunSchema = z
  .object({
    session_id: z.string(),
    workspace_id: z.string(),
    fleet_id: z.string(),
    created_by: z.string(),
    title: z.string(),
    goal: z.string(),
    status: z.string(),
    current_stage: z.string(),
    depth_tier: z.string(),
    goal_version: z.number(),
    plan_version: z.number(),
    state_version: z.number(),
    orchestrator_version: z.string(),
    config: z
      .object({
        max_tasks: z.number(),
        max_parallel_tasks: z.number(),
        max_attempts_per_task: z.number(),
        max_snapshot_bytes: z.number(),
        max_result_bytes: z.number(),
        max_run_seconds: z.number(),
        task_timeout_seconds: z.number(),
        stale_after_seconds: z.number(),
        marginal_gain_threshold: z.number(),
        marginal_gain_rounds: z.number(),
      })
      .passthrough(),
    stats: z
      .object({
        accepted_results: z.number(),
        evidence_batches: z.number(),
        low_gain_streak: z.number(),
        last_coverage_delta: z.number(),
        last_measured_gain: z.number(),
        last_confidence: z.number(),
        sources_created: z.number(),
        observations_created: z.number(),
        claims_created: z.number(),
        budget_exhaustion_count: z.number(),
      })
      .passthrough(),
    initialized_at: z.string().optional(),
    last_progress_at: z.string(),
    next_reconcile_at: z.string(),
    stop_reason: z.string().optional(),
    last_error: z.string().optional(),
  })
  .passthrough();

const ResearchEvidenceStandardSchema = z
  .object({
    client_key: z.string(),
    purpose: z.string(),
    minimum_independent_sources: z.number(),
    required_source_traits: nullishArray(z.string()),
    minimum_strength: z.number(),
    minimum_directness: z.number(),
    minimum_method_fit: z.number(),
    counterevidence_required: z.boolean().optional().default(false),
  })
  .passthrough();

const ResearchClaimEvidenceSchema = z
  .object({
    observation_id: z.string(),
    relation: z.string(),
    strength: z.number(),
    directness: z.number().optional(),
    method_fit: z.number().optional(),
    verification_status: z.string(),
    verified_by_task_id: z.string().optional(),
    rationale: z.string().optional(),
  })
  .passthrough();

const ResearchAttemptArtifactContextSchema = z
  .object({
    attempt_id: z.string(),
    manifest_id: z.string().optional(),
    manifest_hash: z.string().optional(),
    policy_watermark: z.number().optional(),
    manifest_filtered: z.boolean().optional().default(false),
  })
  .passthrough();

export const ResearchArtifactProjectionSchema = z
  .object({
    projection_hash: z.string(),
    items: nullishArray(
      z
        .object({
          id: z.string(),
          run_id: z.string(),
          entity_kind: z.string(),
          entity_id: z.string(),
          current_version: z.number().int().nullable(),
          eligibility_revision: z.number().int(),
          lifecycle_status: z.string(),
          provenance_completeness: z.string(),
          schema_name: z.string(),
          schema_version: z.string(),
          access_level: z.string(),
          goal_version: z.number().int().nullable(),
          plan_version: z.number().int().nullable(),
          produced_by_task_id: z.string().optional(),
          produced_by_attempt_id: z.string().optional(),
          produced_by_agent_id: z.string().optional(),
          version_count: z.number().int().nonnegative(),
          input_reference_count: z.number().int().nonnegative(),
          output_reference_count: z.number().int().nonnegative(),
        })
        .passthrough(),
    ),
  })
  .passthrough()
  .catch({ projection_hash: "", items: [] });

const ResearchRunSnapshotSchema = z
  .object({
    run: ResearchRunSchema,
    contract: z
      .object({
        goal_version: z.number(),
        goal: z.string(),
        scope: nullishRecord(),
        audience: z.string(),
        freshness: z.string(),
        language: z.string(),
        source_policy: nullishRecord(),
        run_limits: ResearchRunSchema.shape.config,
        reason: z.string(),
        created_at: z.string(),
      })
      .passthrough(),
    method: z
      .object({
        goal_version: z.number(),
        plan_version: z.number(),
        decision_question: z.string(),
        method_rationale: z.string(),
        analysis_methods: nullishArray(z.string()),
        evidence_requirements: nullishArray(z.string()),
        evidence_standards: nullishArray(ResearchEvidenceStandardSchema),
        inclusion_criteria: nullishArray(z.string()),
        exclusion_criteria: nullishArray(z.string()),
        source_strategy: nullishArray(z.string()),
        counterevidence_strategy: nullishArray(z.string()),
        stopping_conditions: nullishArray(z.string()),
        uncertainties: nullishArray(z.string()),
        planning_risks: nullishArray(z.string()),
        created_by_task_id: z.string(),
        created_by_agent_id: z.string(),
        created_at: z.string(),
      })
      .passthrough()
      .optional(),
    questions: nullishArray(
      z
        .object({
          id: z.string(),
          client_key: z.string(),
          kind: z.string(),
          question: z.string(),
          required: z.boolean(),
          status: z.string(),
          priority: z.number(),
          coverage: z.number(),
          goal_version: z.number(),
          plan_version: z.number(),
        })
        .passthrough(),
    ),
    tasks: nullishArray(
      z
        .object({
          id: z.string(),
          client_key: z.string(),
          kind: z.string(),
          objective: z.string(),
          required_capability: z.string(),
          status: z.string(),
          attempt_count: z.number(),
          goal_version: z.number(),
          plan_version: z.number(),
        })
        .passthrough(),
    ),
    attempts: nullishArray(
      z
        .object({
          id: z.string(),
          task_id: z.string(),
          attempt_number: z.number(),
          assigned_agent_id: z.string(),
          status: z.string(),
        })
        .passthrough(),
    ),
    sources: nullishArray(
      z
        .object({
          id: z.string(),
          canonical_url: z.string(),
          title: z.string(),
          publisher: z.string(),
          source_class: z.string(),
          evidence_traits: nullishArray(z.string()),
          independence_key: z.string(),
          retrieved_at: z.string(),
          content_hash: z.string(),
          snapshot_excerpt: z.string(),
          metadata: z.unknown(),
          verification_status: z.string(),
          created_at: z.string(),
        })
        .passthrough(),
    ),
    observations: nullishArray(
      z
        .object({
          id: z.string(),
          source_snapshot_id: z.string(),
          datum: z.unknown(),
          verification_status: z.string(),
          created_at: z.string(),
        })
        .passthrough(),
    ),
    claims: nullishArray(
      z
        .object({
          id: z.string(),
          client_key: z.string(),
          evidence_standard_key: z.string().optional(),
          text: z.string(),
          significance: z.string(),
          confidence: z.number(),
          status: z.string(),
          goal_version: z.number(),
          plan_version: z.number(),
          evidence: nullishArray(ResearchClaimEvidenceSchema),
          created_at: z.string(),
          updated_at: z.string(),
        })
        .passthrough(),
    ),
    gate: z
      .object({
        passed: z.boolean(),
        findings: nullishArray(
          z
            .object({
              code: z.string(),
              severity: z.string(),
              message: z.string(),
              metadata: z.record(z.string(), z.unknown()).optional(),
            })
            .passthrough(),
        ),
      })
      .passthrough(),
    attempt_context: ResearchAttemptArtifactContextSchema.optional(),
    artifact_projection: ResearchArtifactProjectionSchema.optional(),
  })
  .passthrough();

export const SteerResearchRunResponseSchema = z
  .object({ run: ResearchRunSchema })
  .passthrough();

export const CreateResearchSessionResponseSchema = z
  .object({
    session: ResearchSessionSchema,
    fleet: ResearchFleetSchema.optional(),
    nodes: nullishArray(ResearchGraphNodeSchema),
    edges: nullishArray(ResearchGraphEdgeSchema),
    messages: nullishArray(ResearchMessageSchema),
    run: ResearchRunSnapshotSchema.optional(),
  })
  .passthrough();

export const ListResearchSessionsResponseSchema = z
  .object({
    sessions: nullishArray(ResearchSessionSchema),
  })
  .passthrough();

export const ResearchSessionSnapshotSchema = z
  .object({
    session: ResearchSessionSchema,
    fleet: ResearchFleetSchema,
    nodes: nullishArray(ResearchGraphNodeSchema),
    edges: nullishArray(ResearchGraphEdgeSchema),
    sources: nullishArray(ResearchSourceSchema),
    report: ResearchReportSchema.nullable().optional().default(null),
    evals: nullishArray(ResearchStageEvalSchema),
    messages: nullishArray(ResearchMessageSchema),
    thought_strategies: nullishArray(ResearchThoughtStrategySchema),
    run: ResearchRunSnapshotSchema.optional(),
  })
  .passthrough();

export const EMPTY_RESEARCH_FLEET: ResearchFleet = {
  id: "",
  workspace_id: "",
  lead_agent_id: null,
  members: [],
  created_at: "",
  updated_at: "",
};

export const EMPTY_RESEARCH_SESSIONS: ListResearchSessionsResponse = {
  sessions: [],
};

export const EMPTY_RESEARCH_SNAPSHOT: ResearchSessionSnapshot = {
  session: {
    id: "",
    workspace_id: "",
    fleet_id: "",
    created_by: "",
    title: "",
    goal: "",
    status: "drafting",
    current_stage: "s1_plan",
    project_id: null,
    channel_id: null,
    handoff_summary: null,
    created_at: "",
    updated_at: "",
  },
  fleet: EMPTY_RESEARCH_FLEET,
  nodes: [],
  edges: [],
  sources: [],
  report: null,
  evals: [],
  messages: [],
  thought_strategies: [],
};

export const ResearchV6ReleaseSchema = z.object({
  workspace_id: z.string().optional().default(""),
  create_enabled: z.boolean().optional().default(true),
  maintenance_reason: z.string().optional().default(""),
  paused_run_count: z.number().optional().default(0),
});

export const ResearchMonitorListSchema = z.object({
  monitors: z
    .array(
      z.object({
        id: z.string(),
        status: z.string().optional().default(""),
        last_cycle_status: z.string().optional(),
      }).passthrough(),
    )
    .optional()
    .default([]),
});

export const ResearchProductionWindowSchema = z.object({
  llm_judge: z.boolean().optional().default(false),
  quality_signal: z.string().optional().default("user_confirmed_delivery"),
  report: z
    .object({
      sufficient_data: z.boolean().optional(),
      within_bounds: z.boolean().optional(),
    })
    .passthrough()
    .optional(),
});

