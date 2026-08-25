export type EvolutionReviewSubmissionStatus =
  | "candidate"
  | "needs_review"
  | "rejected"
  | "promoted"
  | (string & {});

export type EvolutionReviewDecision =
  | "promote"
  | "needs_review"
  | "reject"
  | ""
  | (string & {});

export type EvolutionReviewRiskLevel =
  | "low"
  | "medium"
  | "high"
  | ""
  | (string & {});

export interface EvolutionReviewFile {
  id: string;
  path: string;
  content?: string;
  content_hash: string;
  mime_type: string;
  size_bytes: number;
  created_at?: string | null;
}

export interface EvolutionMaterializedSkill {
  id: string;
  name: string;
  description: string;
}

export interface EvolutionReviewEvidence {
  source: string;
  source_date: string;
  evidence_refs: string[];
}

export interface EvolutionReviewApplies {
  scope: string;
  tags: string[];
  tools: string[];
  task_types: string[];
  project_types: string[];
  languages: string[];
  frameworks: string[];
}

export interface EvolutionReviewSubmission {
  id: string;
  workspace_id: string;
  source_agent_id: string;
  source_member_id?: string;
  unit_type: string;
  local_unit_id: string;
  title: string;
  summary: string;
  content?: string;
  content_hash: string;
  bundle_hash: string;
  bundle_ref: string;
  sensitivity: string;
  confidence: string;
  suggested_scope: string;
  evidence: EvolutionReviewEvidence;
  applies: EvolutionReviewApplies;
  tags: string[];
  tools: string[];
  task_types: string[];
  project_types: string[];
  languages: string[];
  frameworks: string[];
  status: EvolutionReviewSubmissionStatus;
  reject_reason: string;
  review_decision: EvolutionReviewDecision;
  review_confidence?: number | null;
  review_risk_level: EvolutionReviewRiskLevel;
  review_reason: string;
  review_metadata: Record<string, unknown>;
  reviewed_at?: string | null;
  promoted_unit_id?: string | null;
  materialized_skill?: EvolutionMaterializedSkill;
  source_created_at?: string | null;
  created_at?: string | null;
  updated_at?: string | null;
  files?: EvolutionReviewFile[];
}

export interface EvolutionUnitMetric {
  unit_id?: string | null;
  local_unit_id: string;
  unit_type: "memory" | "skill" | "workflow" | "tool_pattern" | "preference" | (string & {});
  title: string;
  injected_count: number;
  used_count: number;
  success_count: number;
  failure_count: number;
  ignored_count: number;
  conflict_count: number;
  success_rate: number;
  last_used_at?: string | null;
}

export interface EvolutionDailyMetric {
  date: string;
  memory_candidates: number;
  skill_candidates: number;
  promoted_memory: number;
  promoted_skill: number;
  team_knowledge_items: number;
  archived_or_deprecated: number;
  feedback_injected: number;
  feedback_used: number;
  feedback_success: number;
  feedback_failure: number;
  memory_curation_run_count: number;
  memory_curation_failed: number;
}

export interface EvolutionTaskEfficiency {
  issue_count: number;
  average_duration_seconds: number;
  average_input_tokens: number;
  average_output_tokens: number;
  average_cache_read_tokens: number;
  average_cache_write_tokens: number;
  average_evolved_units_used: number;
  with_evolved_units_issue_count: number;
  without_evolved_units_issue_count: number;
}

export interface EvolutionCollaborationMetric {
  unmentioned_messages: number;
  attention_rounds: number;
  attention_probes: number;
  attention_silent_rate: number;
  autonomous_claims: number;
  peer_converged: number;
  manager_fallbacks: number;
  full_execution_wakes: number;
  full_execution_reduction_rate: number;
  collaboration_sessions: number;
  turn_order_violation_rate: number;
  contribution_offers: number;
  contribution_offer_adoption_rate: number;
  contribution_offer_helpful_rate: number;
  unauthorized_public_sends_blocked: number;
  policies_retrieved: number;
  policies_used: number;
  policy_success_rate: number;
  attention_tokens: number;
  execution_tokens: number;
  estimated_tokens_saved: number;
  immutable_decision_audit_events: number;
}

export interface EvolutionModelMetric {
  attention_student_version: string;
  attention_student_mode: string;
  missed_attention_rate: number;
  late_rescue_rate: number;
  context_filter_version: string;
  context_compression_rate: number;
  critical_context_recall: number;
}

export interface EvolutionMetricsResponse {
  unit_metrics: EvolutionUnitMetric[];
  daily_metrics: EvolutionDailyMetric[];
  task_efficiency: EvolutionTaskEfficiency;
  collaboration_evolution: EvolutionCollaborationMetric;
  model_evolution: EvolutionModelMetric;
}


export type EvolutionModelKind = "attention_student" | "context_filter";
export type EvolutionTrainingExampleStatus = "candidate" | "gold" | "rejected" | "archived";
export type EvolutionTrainingExampleSplit = "unassigned" | "train" | "validation" | "test" | "holdout";

export interface EvolutionTrainingExample {
  id: string;
  workspace_id: string;
  model_kind: EvolutionModelKind;
  source_kind: string;
  source_id?: string;
  agent_id?: string;
  channel_id?: string;
  message_id?: string;
  input: Record<string, unknown>;
  teacher_label: Record<string, unknown>;
  student_prediction: Record<string, unknown>;
  split: EvolutionTrainingExampleSplit;
  status: EvolutionTrainingExampleStatus;
  created_at: string;
  updated_at: string;
}

export interface EvolutionTrainingExampleListResponse {
  workspace_id: string;
  examples: EvolutionTrainingExample[];
  total: number;
}

export interface EvolutionTrainingExampleCreateRequest {
  model_kind: EvolutionModelKind;
  source_kind?: string;
  source_id?: string;
  agent_id?: string;
  channel_id?: string;
  message_id?: string;
  input?: Record<string, unknown>;
  teacher_label?: Record<string, unknown>;
  student_prediction?: Record<string, unknown>;
  split?: EvolutionTrainingExampleSplit;
  status?: EvolutionTrainingExampleStatus;
}

export interface EvolutionTrainingExampleUpdateRequest {
  teacher_label?: Record<string, unknown>;
  student_prediction?: Record<string, unknown>;
  split?: EvolutionTrainingExampleSplit;
  status?: EvolutionTrainingExampleStatus;
}

export interface EvolutionModelRuntimeConfig {
  workspace_id: string;
  model_kind: EvolutionModelKind;
  mode: "off" | "shadow" | "canary";
  active_version: string;
  candidate_version: string;
  rollout_percent: number;
  config: Record<string, unknown>;
  updated_by?: string;
  created_at: string;
  updated_at: string;
}

export interface EvolutionModelRuntimeConfigListResponse {
  configs: EvolutionModelRuntimeConfig[];
  total: number;
}

export interface EvolutionModelRuntimeConfigUpdateRequest {
  mode: "off" | "shadow" | "canary";
  active_version?: string;
  candidate_version?: string;
  rollout_percent?: number;
  config?: Record<string, unknown>;
}

export interface EvolutionModelEvalRun {
  id: string;
  workspace_id: string;
  model_kind: EvolutionModelKind;
  model_version: string;
  mode: "offline" | "shadow" | "canary";
  status: "completed" | "running" | "failed";
  dataset_filter: Record<string, unknown>;
  metrics: Record<string, unknown>;
  example_count: number;
  created_at: string;
}

export interface EvolutionModelEvalRunListResponse {
  eval_runs: EvolutionModelEvalRun[];
  total: number;
}

export interface EvolutionModelEvalRunCreateRequest {
  model_kind: EvolutionModelKind;
  model_version: string;
  mode?: "offline" | "shadow" | "canary";
  status?: "completed" | "running" | "failed";
  dataset_filter?: Record<string, unknown>;
  metrics?: Record<string, unknown>;
}

export interface MemoryCurationRunStats {
  agents_scanned: number;
  agents_changed: number;
  daily_files_written: number;
  review_candidates_added: number;
  entries_promoted: number;
  shared_candidates_added: number;
  shared_candidates_synced: number;
  entries_archived: number;
  duplicates_merged: number;
  conflicts_found: number;
  evidence_collected: number;
  error_count: number;
}

export interface MemoryCurationStageStatus {
  id: string;
  stage: string;
  trigger_kind: string;
  status: string;
  stats: MemoryCurationRunStats;
  error?: string;
  created_at: string;
  started_at?: string | null;
  finished_at?: string | null;
}

export interface MemoryCurationRunDiagnostic {
  severity: string;
  code: string;
  message: string;
  action?: string;
}

export interface MemoryCurationTargetAgent {
  id: string;
  name: string;
}

export interface MemoryCurationRunTimelineItem {
  key: string;
  agent_id?: string;
  label: string;
  status: string;
  timestamp?: string;
  detail?: string;
}

export interface MemoryCurationAgentRun {
  workspace_id: string;
  agent_id: string;
  agent_name?: string;
  root: string;
  changed: boolean;
  daily_files_written: number;
  review_candidates_added: number;
  skill_candidates_added: number;
  evidence_collected: number;
  conflicts_found: number;
  error?: string;
  curator_output_excerpt?: string;
}

export interface MemoryCurationChildRun {
  id: string;
  parent_run_id: string;
  workspace_id: string;
  agent_id: string;
  agent_name?: string;
  runtime_id?: string;
  runtime_name?: string;
  stage: string;
  status: string;
  attempt: number;
  started_at?: string | null;
  finished_at?: string | null;
  error?: string;
  changed: boolean;
  daily_files_written: number;
  review_candidates_added: number;
  skill_candidates_added: number;
  evidence_collected: number;
  conflicts_found: number;
  output_excerpt?: string;
}

export interface MemoryCurationRunArtifact {
  kind: string;
  title: string;
  agent_id?: string;
  detail?: string;
  content?: string;
}

export interface MemoryCurationRunDetail extends MemoryCurationStageStatus {
  workspace_id: string;
  agent_id?: string | null;
  date_from?: string | null;
  date_to?: string | null;
  dry_run: boolean;
  force: boolean;
  stats_summary: MemoryCurationRunStats;
  diagnostics: MemoryCurationRunDiagnostic[];
  runtime_id?: string;
  runtime_name?: string;
  runtime_device_info?: string;
  runtime_last_seen_at?: string | null;
  attempt?: number;
  claimed_at?: string | null;
  claimed_age_seconds?: number;
  curator_agent_id?: string;
  curator_agent_name?: string;
  curator_model?: string;
  curator_mode?: string;
  confidence_threshold?: number;
  target_agent_ids: string[];
  target_agents: MemoryCurationTargetAgent[];
  timeline: MemoryCurationRunTimelineItem[];
  agent_results: MemoryCurationAgentRun[];
  child_runs: MemoryCurationChildRun[];
  artifacts: MemoryCurationRunArtifact[];
}

export interface WorkspaceMemoryCurationStatus {
  workspace_id: string;
  pending_runs: number;
  failed_runs_24h: number;
  stages: MemoryCurationStageStatus[];
  /** Latest self-review run's review_candidates_added (local proposals). */
  local_proposals?: number;
  /** Workspace DB pending candidates awaiting team curation. */
  pending_candidates?: number;
  /** Subset of pending candidates typed as skill / team_skill. */
  pending_skills?: number;
  /** Candidates already marked promoted in the registry. */
  promoted_candidates?: number;
  /** Shared team_knowledge_item rows in the workspace registry. */
  team_knowledge_items?: number;
}

export type MemoryCuratorMode = "observe" | "review" | "auto_safe" | "auto";
export type MemoryCuratorTargetScope = "owned_all" | "selected";

export interface MemoryCuratorProfile {
  id: string;
  workspace_id: string;
  user_id: string;
  enabled: boolean;
  self_review_enabled: boolean;
  team_curation_enabled: boolean;
  mode: MemoryCuratorMode;
  runtime_id: string;
  curator_agent_id: string;
  model_override: string;
  target_scope: MemoryCuratorTargetScope;
  target_agent_ids: string[];
  timezone: string;
  schedule_hour: number;
  catch_up_enabled: boolean;
  confidence_threshold: number;
  config_version: number;
  created_at: string;
  updated_at: string;
}

export interface UpdateMemoryCuratorProfileRequest {
  enabled: boolean;
  self_review_enabled: boolean;
  team_curation_enabled: boolean;
  mode: MemoryCuratorMode;
  runtime_id: string;
  curator_agent_id: string;
  model_override?: string;
  target_scope: MemoryCuratorTargetScope;
  target_agent_ids: string[];
  timezone: string;
  schedule_hour: number;
  catch_up_enabled: boolean;
  confidence_threshold: number;
}

export type GraphMemoryType = "legacy" | "graph";
export type GraphMemoryMode = "inject" | "agent";
export type GraphMemoryChannelModeOverride = "inherit" | GraphMemoryMode;

export interface GraphMemoryProfile {
  workspace_id: string;
  memory_type: GraphMemoryType;
  graph_memory_mode: GraphMemoryMode;
  memory_agent_runtime_id: string;
  memory_agent_model: string;
  memory_agent_thinking: string;
  recall_ttt_enabled: boolean;
  consolidation_ttt_enabled: boolean;
  memory_agent_idle_grace_seconds: number;
  memory_agent_max_nodes_per_call: number;
  memory_agent_max_nodes_per_minute: number;
  memory_agent_max_continuous_turn_seconds: number;
  memory_agent_max_tokens_per_hour: number;
  explore_agents: number;
  explore_max_rounds: number;
  ttt_enabled: boolean;
  explore_nodes_per_expansion: number;
  max_hierarchy_fanout: number;
  max_relation_edges_per_node: number;
  dive_max_rounds: number;
  dive_max_viewed_nodes: number;
  dive_max_source_files: number;
  dive_timeout_seconds: number;
  w_round: number;
  source_max_file_bytes: number;
  source_max_total_bytes: number;
  source_max_pdf_pages: number;
  source_max_av_seconds: number;
  source_max_image_megapixels: number;
  dive_model: string;
  dive_provider: string;
  config_version: number;
  updated_at: string;
}

export interface UpdateGraphMemoryProfileRequest {
  memory_type: GraphMemoryType;
  graph_memory_mode?: GraphMemoryMode;
  memory_agent_runtime_id?: string;
  memory_agent_model?: string;
  memory_agent_thinking?: string;
  recall_ttt_enabled?: boolean;
  consolidation_ttt_enabled?: boolean;
  memory_agent_idle_grace_seconds?: number;
  memory_agent_max_nodes_per_call?: number;
  memory_agent_max_nodes_per_minute?: number;
  memory_agent_max_continuous_turn_seconds?: number;
  memory_agent_max_tokens_per_hour?: number;
  explore_agents: number;
  explore_max_rounds: number;
  confirm_empty_start?: boolean;
  // CAS guard: required when updating an existing profile row (spec §16).
  config_version?: number;
  ttt_enabled?: boolean;
  explore_nodes_per_expansion?: number;
  max_hierarchy_fanout?: number;
  max_relation_edges_per_node?: number;
  dive_max_rounds?: number;
  dive_max_viewed_nodes?: number;
  dive_max_source_files?: number;
  dive_timeout_seconds?: number;
  w_round?: number;
  source_max_file_bytes?: number;
  source_max_total_bytes?: number;
  source_max_pdf_pages?: number;
  source_max_av_seconds?: number;
  source_max_image_megapixels?: number;
  dive_model?: string;
  dive_provider?: string;
}

export type GraphMemoryChannelAgentStatus = "provisioning" | "active" | "blocked" | "inactive";

export interface GraphMemoryChannelMode {
  workspace_id: string;
  channel_id: string;
  override: GraphMemoryChannelModeOverride;
  effective_mode: GraphMemoryMode;
  status: GraphMemoryChannelAgentStatus;
  blocked_reason: string;
  agent_id: string;
  runtime_id: string;
}

export interface GraphMemoryCitation {
  id: string;
  node_id: string;
  graph_version: number;
  level: string;
  epistemic_status: string;
  tags: string[];
  title: string;
  first_paragraph: string;
  excerpt: string;
  content_hash: string;
  captured_at: string;
}

export interface GraphMemoryMessageCitations {
  message_id: string;
  items: GraphMemoryCitation[];
}

export interface GraphMemoryGraphStatus {
  kind: "project" | "channel";
  owner_id: string;
  current_version: number;
  versions: number[];
  staging_segments: number;
  // Backend emits an RFC3339 timestamp or omits/nulls the field when the
  // graph was never consolidated.
  last_consolidated_at: string | null;
  consolidation_backoff: boolean;
  recall_queries_24h: number;
  recall_hit_rate_24h: number;
}

export interface GraphMemoryStatus {
  workspace_id: string;
  memory_type: GraphMemoryType;
  scoped_writer_ready: boolean;
  empty_start: boolean;
  graphs: GraphMemoryGraphStatus[];
}

export interface GraphMemoryAuditSummary {
  workspace_id: string;
  queries_24h: number;
  recall_hits_24h: number;
  recall_hit_rate_24h: number;
  avg_explore_rounds_24h: number;
  judged_queries_24h: number;
  regressions_total: number;
}

export interface GraphMemoryChannelLineageEntry {
  generation: number;
  graph_kind: "project" | "channel";
  graph_owner_id: string;
  valid_from: string;
  valid_to: string;
}

export interface GraphMemoryChannelLineage {
  workspace_id: string;
  channel_id: string;
  routing_mode: "standalone" | "project_lineage" | "";
  current: { graph_kind: "project" | "channel"; graph_owner_id: string; generation: number } | null;
  lineage: GraphMemoryChannelLineageEntry[];
}

export interface GraphMemoryConsolidationRun {
  id: string;
  workspace_id: string;
  status: "queued" | "running" | "succeeded" | "failed" | string;
  trigger_kind: string;
  error: string;
  created_at: string;
  started_at: string;
  finished_at: string;
}

export interface StartMemoryCurationRunRequest {
  agent_ids?: string[];
  all_agents?: boolean;
  stage: "agent_self_review" | "team_curation" | "all";
  since?: string;
  until?: string;
  include_history?: boolean;
  dry_run?: boolean;
  force?: boolean;
}

export interface StartMemoryCurationRunResponse {
  id: string;
  status: string;
}

export interface MemoryCurationBackfillRequest {
  since?: string;
  until?: string;
  dry_run?: boolean;
}

export interface MemoryCurationBackfillDayPlan {
  date: string;
  stage: string;
  target_agent_ids: string[];
  run_id?: string;
  status?: string;
}

export interface MemoryCurationBackfillSkip {
  date: string;
  reason: string;
}

export interface MemoryCurationBackfillResponse {
  since: string;
  until: string;
  dry_run: boolean;
  queued: MemoryCurationBackfillDayPlan[];
  skipped: MemoryCurationBackfillSkip[];
  queued_days: number;
  skip_days: number;
}

export interface MemoryCurationDailySummaryDay {
  date: string;
  memory_candidates: number;
  skill_candidates: number;
  team_knowledge_items: number;
  team_skills: number;
}

export interface MemoryCurationDailySummaryResponse {
  timezone: string;
  since: string;
  until: string;
  days: MemoryCurationDailySummaryDay[];
}

export interface MemoryCurationCandidateItem {
  id: string;
  source_agent_id?: string;
  source_agent_name?: string;
  run_id?: string;
  candidate_type: string;
  scope: string;
  title: string;
  snippet: string;
  content?: string;
  confidence: number;
  status: string;
  created_at: string;
}

export interface MemoryCurationCandidateListResponse {
  items: MemoryCurationCandidateItem[];
  total: number;
}

export interface TeamKnowledgeListItem {
  id: string;
  kind: string;
  title: string;
  snippet: string;
  content?: string;
  status: string;
  created_at: string;
}

export interface TeamKnowledgeListResponse {
  items: TeamKnowledgeListItem[];
  total: number;
}

export interface EvolutionReviewDecisionRequest {
  reason?: string;
  apply_review_suggestions?: boolean;
}

export interface PromoteEvolutionReviewSubmissionResponse {
  status: string;
  unit_id?: string;
}


/** Explicit knowledge edge (LRM-1000 / LRM-1001). */
export interface KnowledgeEdge {
  id: string;
  edge_type: string;
  from_kind: string;
  from_id: string;
  to_kind: string;
  to_id: string;
  created_by_type: string;
  created_by_id?: string;
  created_at: string;
}

export interface KnowledgeNeighborsResponse {
  page_id: string;
  edges: KnowledgeEdge[];
  hops: number;
}

export interface PromoteKnowledgeRequest {
  source_type: "issue" | "channel";
  source_id: string;
  target_kind: "context" | "decision";
  title: string;
  content: string;
  subject_id?: string;
  supersedes_id?: string;
  shared_to_agent_id?: string;
}

export interface PromoteKnowledgeResponse {
  id: string;
  kind: string;
  title: string;
  content: string;
  status: string;
  metadata?: unknown;
  edges: KnowledgeEdge[];
  created_at: string;
}
