export type ResearchSessionStatus =
  | "drafting"
  | "running"
  | "awaiting_user_confirm"
  | "completed"
  | "archived";

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
  | "agent_activity";

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

export interface ResearchReport {
  id: string;
  session_id: string;
  revision: number;
  content_md: string;
  structured: Record<string, unknown> | unknown;
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

export interface CreateResearchSessionRequest {
  goal: string;
  title?: string;
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
