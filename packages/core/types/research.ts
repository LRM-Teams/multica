export type ResearchSessionStatus =
  | "drafting"
  | "running"
  | "awaiting_user_confirm"
  | "completed"
  | "archived"
  | "paused";

export type ResearchStage =
  | "s1_plan"
  | "s2_sources"
  | "s3_validation"
  | "s4_delivery";

export type ResearchGraphNodeType =
  | "goal"
  | "subquestion"
  | "probe"
  | "finding"
  | "conflict"
  | "dead_end"
  | "refuted"
  | "pivot"
  | "roster_change"
  | "stage_gate"
  | "product_round_gate"
  | "agent_activity";

/** LRM-676 depth tier → product-round hard cap (LRM-905 / LRM-911). */
export type ResearchDepthTier = "shallow" | "standard" | "deep" | string;

/** End-of-round judgment from Ronaldo (LRM-905 / LRM-911 / LRM-913). */
export type ResearchProductRoundDecision =
  | "continue"
  | "stop_enough"
  | "stop_budget"
  | string;

export interface ResearchProductRoundCard {
  id: string;
  session_id: string;
  round_number: number;
  decision: ResearchProductRoundDecision;
  /** JSON array of uncovered items (strings or objects). */
  coverage_gaps: unknown;
  confidence_note: string;
  budget_used: number;
  budget_remaining: number;
  goal_patch_proposal: string | null;
  next_round_focus: string | null;
  decided_by_agent_id: string | null;
  created_at: string;
}

export interface ListResearchProductRoundCardsResponse {
  rounds: ResearchProductRoundCard[];
}

export type ResearchGraphEdgeType =
  | "leads_to"
  | "supports"
  | "contradicts"
  | "supersedes"
  | "abandons";

export type ResearchFleetMemberStatus =
  | "pending_prompt_review"
  | "active"
  | "archived";

export interface ResearchFleetMember {
  id: string;
  agent_id: string;
  role: string;
  status: ResearchFleetMemberStatus | string;
  is_lead: boolean;
  name?: string;
  display_name?: string;
  avatar_url?: string | null;
}

export interface ResearchFleetPreviewMember {
  agent_id: string;
  name?: string;
  display_name?: string;
  avatar_url?: string | null;
  role?: string;
  is_lead?: boolean;
}

export interface ResearchFleet {
  id: string;
  workspace_id: string;
  lead_agent_id: string | null;
  members: ResearchFleetMember[];
  created_at: string;
  updated_at: string;
}

export interface ResearchSession {
  id: string;
  workspace_id: string;
  fleet_id: string;
  created_by: string;
  title: string;
  goal: string;
  status: ResearchSessionStatus | string;
  current_stage: ResearchStage | string;
  project_id: string | null;
  channel_id: string | null;
  handoff_summary: string | null;
  created_at: string;
  updated_at: string;
  /** Present on list responses (LRM-805). */
  fleet_preview?: ResearchFleetPreviewMember[];
  /** LRM-911 product-round fields (optional until migration 255). */
  depth_tier?: ResearchDepthTier;
  product_round?: number;
  product_round_budget?: number;
}
export interface ResearchGraphNode {
  id: string;
  session_id: string;
  node_type: ResearchGraphNodeType | string;
  title: string;
  summary: string;
  status: string;
  actor_agent_id: string | null;
  payload: Record<string, unknown> | unknown;
  /** Projected from payload.confidence when present (LRM-806). */
  confidence?: number | null;
  /** LRM-1278: single tree parent from leads_to; null = root. */
  parent_id?: string | null;
  /** LRM-1278: direct child ids from leads_to. */
  child_ids?: string[];
  child_count?: number;
  descendant_count?: number;
  /** Stable theme key: payload.theme_key|dimension_family|… or `type:<node_type>`. */
  theme_key?: string;
  /** Optional payload.phase; session stage remains on session.current_stage. */
  phase?: string;
  /**
   * LRM-1278 quality assessment. Always present on BE snapshot.
   * Missing/illegal payload → pending_review.
   */
  assessment?: "trusted" | "pending_review" | "detour" | string;
  reason?: string | null;
  evidence_summary?: string | null;
  /**
   * LRM-1317 / LRM-1333: projected abandon reason (payload.abandon_reason or
   * deprecate_reason). Omitted when empty — never invent from assessment/edges.
   */
  abandon_reason?: string | null;
  created_at: string;
  updated_at: string;
}

export interface ResearchGraphEdge {
  id: string;
  session_id: string;
  from_node_id: string;
  to_node_id: string;
  edge_type: ResearchGraphEdgeType | string;
  created_at: string;
}

export interface ResearchSource {
  id: string;
  session_id: string;
  url: string;
  title: string;
  source_class: string;
  credibility_weight: number;
  stance: string;
  relevance: number;
  summary: string;
  excerpt: string;
  payload: Record<string, unknown> | unknown;
  created_at: string;
  updated_at: string;
}

/** Outline tree node for report navigation (LRM-843). */
export interface ResearchReportOutlineNode {
  id: string;
  title: string;
  /** Heading level 1–6. */
  level: number;
  /** Child section ids (order preserved). */
  children: string[];
}

/** One report chapter/section body. */
export interface ResearchReportSection {
  id: string;
  title: string;
  level: number;
  markdown: string;
  citation_ids: string[];
}

/** In-text citation pointing at a research_source row (and optional quote). */
export interface ResearchReportCitation {
  id: string;
  /** 1-based display index. */
  index: number;
  source_id: string;
  label: string;
  quote?: string;
  /** Optional page / section / anchor locator. */
  locator?: string;
}

/**
 * Denormalized source snapshot embedded in the report for export / offline mock.
 * Live session still exposes `sources[]` on the snapshot; prefer matching by `source_id`.
 */
export interface ResearchReportSourceRef {
  source_id: string;
  title: string;
  url: string;
  credibility_weight: number;
  source_class: string;
}

/**
 * `report.structured` payload when `schema_version === 1`.
 * Server currently stores this as opaque JSON (no hard validation).
 */
export interface ResearchReportStructuredV1 {
  schema_version: 1;
  title: string;
  outline: ResearchReportOutlineNode[];
  sections: ResearchReportSection[];
  citations: ResearchReportCitation[];
  sources: ResearchReportSourceRef[];
  gaps?: string[];
  conclusion?: string;
}

export type ResearchReportStructured = ResearchReportStructuredV1;

export interface ResearchReport {
  id: string;
  session_id: string;
  /** Row / delivery version — increments on every PATCH. */
  revision: number;
  /** Authoritative readable Markdown body. */
  content_md: string;
  /**
   * Structured outline / sections / citations / sources.
   * Legacy rows may be `{}` or omit `schema_version` — see normalizeReportStructured.
   */
  structured: ResearchReportStructured | Record<string, unknown> | unknown;
  created_at: string;
  updated_at: string;
}

export interface ResearchStageEval {
  id: string;
  session_id: string;
  stage: string;
  passed: boolean;
  score: number;
  findings: unknown;
  remediation: string;
  created_at: string;
}

export type ResearchMessageCardKind = "chat" | "process" | string;

/**
 * Agent clarification question (LRM-822).
 * Carried on ResearchMessage.meta with op: "clarification_question".
 * Submit/skip reply as a plain user chat body via postResearchMessage.
 */
export type ResearchClarificationLayout = "binary" | "list" | "form";

export interface ResearchClarificationOption {
  id: string;
  label: string;
  description?: string;
}

export interface ResearchClarificationField {
  id: string;
  label: string;
  type: "text" | "textarea";
  required?: boolean;
  placeholder?: string;
}

export interface ResearchClarificationQuestion {
  /** Stable id referenced in the user reply body. */
  question_id: string;
  prompt: string;
  layout: ResearchClarificationLayout;
  options: ResearchClarificationOption[];
  fields: ResearchClarificationField[];
  /** Default true — skip must not block the session (AC). */
  allow_skip: boolean;
  /** Source message id (process/chat card that asked). */
  message_id: string;
  created_at: string;
}

/** LRM-1310/1330 utterance match decision (omit when absent — never invent). */
export type ResearchMatchDecisionAction =
  | "continue"
  | "branch_after"
  | "deprecate"
  | "pending_confirm";

export interface ResearchMatchDecisionItem {
  node_id: string;
  action: ResearchMatchDecisionAction;
  reason?: string;
}

export type ResearchNodeCommandAction = "continue" | "fork" | "retry" | "reassign";

export interface ResearchNodeCommandRequest {
  action: ResearchNodeCommandAction;
  client_request_id: string;
  target_agent_id?: string;
}

export interface ResearchNodeCommandResponse {
  command_id: string;
  action: ResearchNodeCommandAction;
  client_request_id: string;
  replayed: boolean;
  state_version: number;
  queued: boolean;
  assigned?: string | null;
}

export interface ResearchMatchDecision {
  utterance_id: string;
  confidence?: number;
  primary_anchor_node_id?: string;
  matched_node_ids: string[];
  decisions: ResearchMatchDecisionItem[];
}

export interface ResearchMessage {
  id: string;
  session_id: string;
  sender_type: "user" | "agent" | "system" | string;
  sender_id: string | null;
  target_agent_id: string | null;
  body: string;
  card_kind?: ResearchMessageCardKind;
  meta?: Record<string, unknown> | unknown;
  /** Projected from meta.match_decision when present (LRM-1330). */
  match_decision?: ResearchMatchDecision;
  created_at: string;
}

/** LRM-1318 / LRM-1306 side-panel row. Never invent from title/summary. */
export type ResearchThoughtStrategyState = "drafting" | "active" | "settled";

export interface ResearchThoughtStrategy {
  node_id: string;
  rationale: string;
  expected_outcome: string;
  strategy_label?: string | null;
  strategy_revision?: string | null;
  state: ResearchThoughtStrategyState | string;
  updated_at?: string;
}

/** Create-session response includes a kickoff snapshot so the canvas paints immediately. */
export interface CreateResearchSessionResponse {
  session: ResearchSession;
  fleet: ResearchFleet;
  nodes?: ResearchGraphNode[];
  edges?: ResearchGraphEdge[];
  messages?: ResearchMessage[];
  run?: ResearchRunSnapshot;
}

export interface ResearchSessionSnapshot {
  session: ResearchSession;
  fleet: ResearchFleet;
  nodes: ResearchGraphNode[];
  edges: ResearchGraphEdge[];
  sources: ResearchSource[];
  report: ResearchReport | null;
  evals: ResearchStageEval[];
  messages: ResearchMessage[];
  /** LRM-1318 side-panel rows (LRM-1306). Always an array; may be empty. */
  thought_strategies?: ResearchThoughtStrategy[];
  /** Durable execution state. */
  run?: ResearchRunSnapshot;
}

export interface ResearchRunConfig {
  max_tasks: number;
  max_parallel_tasks: number;
  max_attempts_per_task: number;
  max_snapshot_bytes: number;
  max_result_bytes: number;
  max_run_seconds: number;
  task_timeout_seconds: number;
  stale_after_seconds: number;
  marginal_gain_threshold: number;
  marginal_gain_rounds: number;
}

export interface ResearchRunStats {
  accepted_results: number;
  evidence_batches: number;
  low_gain_streak: number;
  last_coverage_delta: number;
  last_measured_gain: number;
  last_confidence: number;
  sources_created: number;
  observations_created: number;
  claims_created: number;
  budget_exhaustion_count: number;
}

export interface ResearchRun {
  session_id: string;
  workspace_id: string;
  fleet_id: string;
  created_by: string;
  title: string;
  goal: string;
  status: string;
  current_stage: string;
  depth_tier: string;
  goal_version: number;
  plan_version: number;
  state_version: number;
  orchestrator_version: string;
  config: ResearchRunConfig;
  stats: ResearchRunStats;
  initialized_at?: string;
  last_progress_at: string;
  next_reconcile_at: string;
  stop_reason?: string;
  last_error?: string;
}

export interface ResearchRunContract {
  goal_version: number;
  goal: string;
  scope: Record<string, unknown>;
  audience: string;
  freshness: string;
  language: string;
  source_policy: Record<string, unknown>;
  run_limits: ResearchRunConfig;
  reason: string;
  created_at: string;
}

export interface ResearchRunMethod {
  goal_version: number;
  plan_version: number;
  decision_question: string;
  method_rationale: string;
  analysis_methods: string[];
  evidence_requirements: string[];
  inclusion_criteria: string[];
  exclusion_criteria: string[];
  source_strategy: string[];
  counterevidence_strategy: string[];
  stopping_conditions: string[];
  uncertainties: string[];
  planning_risks: string[];
  created_by_task_id: string;
  created_by_agent_id: string;
  created_at: string;
}

export interface ResearchRunQuestion {
  id: string;
  parent_question_id?: string;
  created_by_task_id?: string;
  client_key: string;
  kind: string;
  question: string;
  required: boolean;
  status: string;
  priority: number;
  coverage: number;
  goal_version: number;
  plan_version: number;
}

export interface ResearchRunTask {
  id: string;
  question_id?: string;
  parent_task_id?: string;
  client_key: string;
  kind: string;
  objective: string;
  required_capability: string;
  expected_result?: string;
  acceptance_criteria?: Record<string, unknown> | unknown;
  priority?: number;
  status: string;
  assigned_agent_id?: string;
  attempt_count: number;
  goal_version: number;
  plan_version: number;
  max_attempts?: number;
  timeout_seconds?: number;
  ready_at?: string;
  started_at?: string;
  completed_at?: string;
  terminal_reason?: string;
}

export interface ResearchRunAttempt {
  id: string;
  task_id: string;
  attempt_number: number;
  assigned_agent_id: string;
  inbox_task_id?: string;
  dispatch_key?: string;
  client_request_id?: string;
  status: string;
  result_hash?: string;
  failure_class?: string;
  diagnostics?: string;
  dispatched_at?: string;
  started_at?: string;
  result_submitted_at?: string;
  completed_at?: string;
}

export interface ResearchRunSourceSnapshot {
  id: string;
  produced_by_task_id?: string;
  canonical_url: string;
  title: string;
  publisher: string;
  source_class: string;
  independence_key: string;
  retrieved_at: string;
  content_hash: string;
  snapshot_excerpt: string;
  metadata: unknown;
  verification_status: string;
  created_at: string;
}

export interface ResearchRunObservation {
  id: string;
  source_snapshot_id: string;
  produced_by_task_id?: string;
  quote?: string;
  datum: unknown;
  locator?: string;
  interpretation?: string;
  verification_status: string;
  created_at: string;
}

export interface ResearchRunClaimEvidence {
  observation_id: string;
  relation: string;
  strength: number;
  verification_status: string;
  verified_by_task_id?: string;
  rationale?: string;
}

export interface ResearchRunClaim {
  id: string;
  produced_by_task_id?: string;
  client_key: string;
  text: string;
  significance: string;
  confidence: number;
  status: string;
  goal_version: number;
  plan_version: number;
  resolution?: string;
  evidence: ResearchRunClaimEvidence[];
  created_at: string;
  updated_at: string;
}

export interface ResearchRunGateFinding {
  code: string;
  severity: string;
  message: string;
  metadata?: Record<string, unknown>;
}

export interface ResearchRunSnapshot {
  run: ResearchRun;
  contract: ResearchRunContract;
  method?: ResearchRunMethod;
  questions: ResearchRunQuestion[];
  tasks: ResearchRunTask[];
  attempts: ResearchRunAttempt[];
  sources: ResearchRunSourceSnapshot[];
  observations: ResearchRunObservation[];
  claims: ResearchRunClaim[];
  gate: { passed: boolean; findings: ResearchRunGateFinding[] };
}

export interface SteerResearchRunRequest {
  goal: string;
  reason?: string;
  allow_running_finish?: boolean;
  scope?: Record<string, unknown>;
  audience?: string;
  freshness?: string;
  language?: string;
  source_policy?: Record<string, unknown>;
  run_limits?: Partial<ResearchRunConfig>;
}

/** Create-time source preference weights (0–1), persisted in the Research Contract source policy. */
export interface ResearchSourceWeights {
  primary: number;
  secondary: number;
  community: number;
}

export interface CreateResearchSessionRequest {
  goal: string;
  title?: string;
  /** LRM-676 / LRM-838 — shallow|standard|deep product-round caps. */
  depth_tier?: ResearchDepthTier;
  /** Report / delivery language preference (LRM-838). */
  language?: string;
  /** Source credibility preference weights (LRM-838). */
  source_weights?: ResearchSourceWeights;
}

export interface ResearchHandoffRequest {
  create_project?: boolean;
  create_channel?: boolean;
  project_title?: string;
  channel_name?: string;
}

export interface ListResearchSessionsResponse {
  sessions: ResearchSession[];
}
