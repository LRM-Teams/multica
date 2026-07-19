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
  attention_rounds: number;
  attention_probes: number;
  attention_silent_rate: number;
  autonomous_claims: number;
  peer_converged: number;
  manager_fallbacks: number;
  full_execution_wakes: number;
  collaboration_sessions: number;
  contribution_offers: number;
  contribution_offer_adoption_rate: number;
  unauthorized_public_sends_blocked: number;
  attention_tokens: number;
  execution_tokens: number;
  immutable_decision_audit_events: number;
}

export interface EvolutionMetricsResponse {
  unit_metrics: EvolutionUnitMetric[];
  daily_metrics: EvolutionDailyMetric[];
  task_efficiency: EvolutionTaskEfficiency;
  collaboration_evolution: EvolutionCollaborationMetric;
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
  curator_agent_id?: string;
  curator_agent_name?: string;
  curator_model?: string;
  curator_mode?: string;
  confidence_threshold?: number;
  target_agent_ids: string[];
  target_agents: MemoryCurationTargetAgent[];
  timeline: MemoryCurationRunTimelineItem[];
  agent_results: MemoryCurationAgentRun[];
  artifacts: MemoryCurationRunArtifact[];
}

export interface WorkspaceMemoryCurationStatus {
  workspace_id: string;
  pending_runs: number;
  failed_runs_24h: number;
  stages: MemoryCurationStageStatus[];
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

export interface EvolutionReviewDecisionRequest {
  reason?: string;
  apply_review_suggestions?: boolean;
}

export interface PromoteEvolutionReviewSubmissionResponse {
  status: string;
  unit_id?: string;
}
