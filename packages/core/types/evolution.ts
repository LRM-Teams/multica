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

export interface EvolutionMetricsResponse {
  unit_metrics: EvolutionUnitMetric[];
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
  created_at: string;
  started_at?: string | null;
  finished_at?: string | null;
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
  stage: "l1" | "l2" | "l3" | "l4" | "all";
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
