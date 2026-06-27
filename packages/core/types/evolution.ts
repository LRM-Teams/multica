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
  source_created_at?: string | null;
  created_at?: string | null;
  updated_at?: string | null;
  files?: EvolutionReviewFile[];
}

export interface EvolutionReviewDecisionRequest {
  reason?: string;
}

export interface PromoteEvolutionReviewSubmissionResponse {
  status: string;
  unit_id?: string;
}
