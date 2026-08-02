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

export interface ResearchMessage {
  id: string;
  session_id: string;
  sender_type: "user" | "agent" | "system" | string;
  sender_id: string | null;
  target_agent_id: string | null;
  body: string;
  card_kind?: ResearchMessageCardKind;
  meta?: Record<string, unknown> | unknown;
  created_at: string;
}

/** Create-session response includes a kickoff snapshot so the canvas paints immediately. */
export interface CreateResearchSessionResponse {
  session: ResearchSession;
  fleet: ResearchFleet;
  nodes?: ResearchGraphNode[];
  edges?: ResearchGraphEdge[];
  messages?: ResearchMessage[];
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
}

/** Create-time source preference weights (0–1). Persisted via goal trailer until BE columns land. */
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
