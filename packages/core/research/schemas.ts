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
    members: z.array(ResearchFleetMemberSchema).optional().default([]),
    created_at: z.string().optional().default(""),
    updated_at: z.string().optional().default(""),
  })
  .passthrough();

const ResearchSessionSchema = z
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
    fleet_preview: z.array(ResearchFleetPreviewMemberSchema).optional(),
    depth_tier: z.string().optional(),
    product_round: z.number().optional(),
    product_round_budget: z.number().optional(),
  })
  .passthrough();

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
    rounds: z.array(ResearchProductRoundCardSchema).optional().default([]),
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
    credibility_weight: z.number().optional().default(0.5),
    stance: z.string().optional().default(""),
    relevance: z.number().optional().default(0.5),
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

const ResearchMessageSchema = z
  .object({
    id: z.string(),
    session_id: z.string().optional().default(""),
    sender_type: z.string().optional().default("user"),
    sender_id: z.string().nullable().optional().default(null),
    target_agent_id: z.string().nullable().optional().default(null),
    body: z.string().optional().default(""),
    card_kind: z.string().optional().default("chat"),
    meta: z.unknown().optional().default({}),
    created_at: z.string().optional().default(""),
  })
  .passthrough();

export const CreateResearchSessionResponseSchema = z
  .object({
    session: ResearchSessionSchema,
    fleet: ResearchFleetSchema,
    nodes: z.array(ResearchGraphNodeSchema).optional().default([]),
    edges: z.array(ResearchGraphEdgeSchema).optional().default([]),
    messages: z.array(ResearchMessageSchema).optional().default([]),
  })
  .passthrough();

export const ListResearchSessionsResponseSchema = z
  .object({
    sessions: z.array(ResearchSessionSchema).optional().default([]),
  })
  .passthrough();

export const ResearchSessionSnapshotSchema = z
  .object({
    session: ResearchSessionSchema,
    fleet: ResearchFleetSchema,
    nodes: z.array(ResearchGraphNodeSchema).optional().default([]),
    edges: z.array(ResearchGraphEdgeSchema).optional().default([]),
    sources: z.array(ResearchSourceSchema).optional().default([]),
    report: ResearchReportSchema.nullable().optional().default(null),
    evals: z.array(ResearchStageEvalSchema).optional().default([]),
    messages: z.array(ResearchMessageSchema).optional().default([]),
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
};
