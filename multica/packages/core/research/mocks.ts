/**
 * LRM-841 — research FE mock layer.
 *
 * Deterministic, in-memory snapshots so FE slices (819–828) can develop the
 * four UI states (default / empty / loading / error) without waiting on the
 * backend fleet. Type shapes come from `@multica/core/types/research`; report
 * `structured` reuses the v1 fixture from `docs/research/fixtures`.
 *
 * Wire-in is explicit: pass `researchMocks.api` to a query client override or
 * component test harness — no silent fallback in production paths.
 */

import type {
  CreateResearchSessionResponse,
  ListResearchSessionsResponse,
  ResearchGraphEdge,
  ResearchGraphNode,
  ResearchMessage,
  ResearchReport,
  ResearchSession,
  ResearchSessionSnapshot,
  ResearchSource,
  ResearchStageEval,
} from "../types/research";

const now = () => new Date("2026-07-30T12:00:00.000Z").toISOString();

const sessionBase: ResearchSession = {
  id: "sess-00000000-0000-0000-0000-000000000001",
  workspace_id: "ws-mock",
  fleet_id: "fleet-00000000-0000-0000-0000-000000000001",
  created_by: "user-mock",
  title: "Vector DB comparison",
  goal: "Compare managed vector databases for our workload",
  status: "running",
  current_stage: "s2_sources",
  project_id: null,
  channel_id: null,
  handoff_summary: null,
  created_at: now(),
  updated_at: now(),
};

const nodeIds = {
  goal: "node-00000000-0000-0000-0000-000000000001",
  subq1: "node-00000000-0000-0000-0000-000000000002",
  probe1: "node-00000000-0000-0000-0000-000000000003",
  finding1: "node-00000000-0000-0000-0000-000000000004",
  deadEnd: "node-00000000-0000-0000-0000-000000000005",
  stageGate: "node-00000000-0000-0000-0000-000000000006",
  activity: "node-00000000-0000-0000-0000-000000000007",
} as const;

const mockNodes: ResearchGraphNode[] = [
  {
    id: nodeIds.goal,
    session_id: sessionBase.id,
    node_type: "goal",
    title: "Vector DB comparison",
    summary: "Pick a managed vector store for semantic search",
    status: "active",
    actor_agent_id: null,
    payload: {},
    created_at: now(),
    updated_at: now(),
  },
  {
    id: nodeIds.subq1,
    session_id: sessionBase.id,
    node_type: "subquestion",
    title: "Latency under load",
    summary: "p95 latency at 10k QPS",
    status: "active",
    actor_agent_id: null,
    payload: {},
    created_at: now(),
    updated_at: now(),
  },
  {
    id: nodeIds.probe1,
    session_id: sessionBase.id,
    node_type: "probe",
    title: "Benchmark pgvector vs. Milvus",
    summary: "Run recall / latency on identical corpus",
    status: "active",
    actor_agent_id: "agent-mock-scout",
    payload: { confidence: 0.72 },
    confidence: 0.72,
    created_at: now(),
    updated_at: now(),
  },
  {
    id: nodeIds.finding1,
    session_id: sessionBase.id,
    node_type: "finding",
    title: "Milvus higher recall",
    summary: "Milvus recall@10 ≈ 0.94 vs. pgvector 0.88 in this setup",
    status: "active",
    actor_agent_id: "agent-mock-scout",
    payload: { confidence: 0.81 },
    confidence: 0.81,
    created_at: now(),
    updated_at: now(),
  },
  {
    id: nodeIds.deadEnd,
    session_id: sessionBase.id,
    node_type: "dead_end",
    title: "Pinecone serverless quota",
    summary: "Quota ceiling too low for burst traffic",
    status: "active",
    actor_agent_id: null,
    payload: { confidence: 0.4 },
    confidence: 0.4,
    created_at: now(),
    updated_at: now(),
  },
  {
    id: nodeIds.stageGate,
    session_id: sessionBase.id,
    node_type: "stage_gate",
    title: "S2 → S3 validation gate",
    summary: "Approve source coverage before validation",
    status: "active",
    actor_agent_id: null,
    payload: { gate: "s2_sources" },
    created_at: now(),
    updated_at: now(),
  },
  {
    id: nodeIds.activity,
    session_id: sessionBase.id,
    node_type: "agent_activity",
    title: "Scout is fetching benchmarks",
    summary: "Pulling latest ANN benchmark results",
    status: "active",
    actor_agent_id: "agent-mock-scout",
    payload: {},
    created_at: now(),
    updated_at: now(),
  },
];

const mockEdges: ResearchGraphEdge[] = [
  {
    id: "edge-00000000-0000-0000-0000-000000000001",
    session_id: sessionBase.id,
    from_node_id: nodeIds.goal,
    to_node_id: nodeIds.subq1,
    edge_type: "leads_to",
    created_at: now(),
  },
  {
    id: "edge-00000000-0000-0000-0000-000000000002",
    session_id: sessionBase.id,
    from_node_id: nodeIds.subq1,
    to_node_id: nodeIds.probe1,
    edge_type: "leads_to",
    created_at: now(),
  },
  {
    id: "edge-00000000-0000-0000-0000-000000000003",
    session_id: sessionBase.id,
    from_node_id: nodeIds.probe1,
    to_node_id: nodeIds.finding1,
    edge_type: "supports",
    created_at: now(),
  },
  {
    id: "edge-00000000-0000-0000-0000-000000000004",
    session_id: sessionBase.id,
    from_node_id: nodeIds.probe1,
    to_node_id: nodeIds.deadEnd,
    edge_type: "abandons",
    created_at: now(),
  },
];

const sourceMilvus: ResearchSource = {
  id: "src-00000000-0000-0000-0000-000000000001",
  session_id: sessionBase.id,
  url: "https://milvus.io/docs/benchmarks",
  title: "Milvus benchmarks",
  source_class: "primary",
  credibility_weight: 0.9,
  stance: "supports",
  relevance: 0.85,
  summary: "Official Milvus ANN benchmark numbers",
  excerpt: "recall@10 0.94 at 10k QPS",
  payload: {},
  created_at: now(),
  updated_at: now(),
};

const sourcePgvector: ResearchSource = {
  id: "src-00000000-0000-0000-0000-000000000002",
  session_id: sessionBase.id,
  url: "https://supabase.com/docs/guides/ai/vector-indexes",
  title: "pgvector HNSW tuning",
  source_class: "secondary",
  credibility_weight: 0.75,
  stance: "neutral",
  relevance: 0.7,
  summary: "pgvector index parameters and trade-offs",
  excerpt: "m=16, ef_construction=64",
  payload: {},
  created_at: now(),
  updated_at: now(),
};

/** LRM-821 — fetch-failed shell so citation UI can render the degraded card. */
const sourceFetchFailed: ResearchSource = {
  id: "src-00000000-0000-0000-0000-000000000003",
  session_id: sessionBase.id,
  url: "https://example.invalid/pinecone-quota",
  title: "",
  source_class: "secondary",
  credibility_weight: 0,
  stance: "unknown",
  relevance: 0,
  summary: "",
  excerpt: "",
  payload: { fetch_failed: true, status: "fetch_failed" },
  created_at: now(),
  updated_at: now(),
};

const mockSources: ResearchSource[] = [sourceMilvus, sourcePgvector, sourceFetchFailed];

const mockReport: ResearchReport = {
  id: "rep-00000000-0000-0000-0000-000000000001",
  session_id: sessionBase.id,
  revision: 2,
  content_md:
    "# 调研结论\n\n## 背景\nManaged vector store shortlist.\n\n## 发现\nMilvus recall higher.[^1]\n",
  structured: {
    schema_version: 1,
    title: "Vector DB comparison",
    outline: [
      { id: "sec-bg", title: "背景", level: 1, children: [] },
      { id: "sec-find", title: "发现", level: 1, children: [] },
    ],
    sections: [
      {
        id: "sec-bg",
        title: "背景",
        level: 1,
        markdown: "Managed vector store shortlist.",
        citation_ids: [],
      },
      {
        id: "sec-find",
        title: "发现",
        level: 1,
        markdown: "Milvus recall higher.[^1] Pinecone quota page failed.[^2]",
        citation_ids: ["c1", "c2"],
      },
    ],
    citations: [
      {
        id: "c1",
        index: 1,
        source_id: sourceMilvus.id,
        label: "[1]",
        quote: "recall@10 0.94",
      },
      {
        id: "c2",
        index: 2,
        source_id: sourceFetchFailed.id,
        label: "[2]",
        quote: "",
      },
    ],
    sources: [
      {
        source_id: sourceMilvus.id,
        title: sourceMilvus.title,
        url: sourceMilvus.url,
        credibility_weight: sourceMilvus.credibility_weight,
        source_class: sourceMilvus.source_class,
      },
      {
        source_id: sourceFetchFailed.id,
        title: "",
        url: sourceFetchFailed.url,
        credibility_weight: 0,
        source_class: sourceFetchFailed.source_class,
      },
    ],
    gaps: ["Pinecone burst quota data missing"],
    conclusion: "Milvus leads on recall under this workload.",
  },
  created_at: now(),
  updated_at: now(),
};

const mockEvals: ResearchStageEval[] = [
  {
    id: "eval-00000000-0000-0000-0000-000000000001",
    session_id: sessionBase.id,
    stage: "s1_plan",
    passed: true,
    score: 0.86,
    findings: { note: "plan covers 4 candidate stores" },
    remediation: "",
    created_at: now(),
  },
  {
    id: "eval-00000000-0000-0000-0000-000000000002",
    session_id: sessionBase.id,
    stage: "s2_sources",
    passed: false,
    score: 0.58,
    findings: { missing: "Pinecone quota docs" },
    remediation: "Add official Pinecone limits page",
    created_at: now(),
  },
];

const mockMessageUser: ResearchMessage = {
  id: "msg-00000000-0000-0000-0000-000000000001",
  session_id: sessionBase.id,
  sender_type: "user",
  sender_id: "user-mock",
  target_agent_id: null,
  body: "Start with pgvector vs. Milvus recall",
  card_kind: "chat",
  meta: {},
  created_at: now(),
};

const mockMessageAgent: ResearchMessage = {
  id: "msg-00000000-0000-0000-0000-000000000002",
  session_id: sessionBase.id,
  sender_type: "agent",
  sender_id: "agent-mock-scout",
  target_agent_id: null,
  body: "Benchmarks pulled; Milvus recall@10 0.94.",
  card_kind: "chat",
  meta: {},
  created_at: now(),
};

const mockMessageWakeFailed: ResearchMessage = {
  id: "msg-00000000-0000-0000-0000-000000000003",
  session_id: sessionBase.id,
  sender_type: "system",
  sender_id: null,
  target_agent_id: null,
  body: "Scout wake failed: runtime offline. Reassign to active member.",
  card_kind: "process",
  meta: {
    op: "wake_failed",
    reason: "runtime_offline",
    recovery_hint: "reassign",
  },
  created_at: now(),
};

/** LRM-822 — agent clarification with list options (interactive chat card). */
const mockMessageClarificationList: ResearchMessage = {
  id: "msg-00000000-0000-0000-0000-000000000004",
  session_id: sessionBase.id,
  sender_type: "agent",
  sender_id: "agent-mock-lead",
  target_agent_id: null,
  body: "Which vector store dimension should we prioritize first?",
  card_kind: "process",
  meta: {
    op: "clarification_question",
    question_id: "clarify-list-001",
    prompt: "Which vector store dimension should we prioritize first?",
    layout: "list",
    allow_skip: true,
    options: [
      {
        id: "cost",
        label: "Cost / quota",
        description: "Hosting + egress economics",
      },
      {
        id: "recall",
        label: "Recall quality",
        description: "Recall@10 under production load",
      },
      {
        id: "ops",
        label: "Ops complexity",
        description: "Day-2 ops and migration risk",
      },
    ],
  },
  created_at: now(),
};

/** LRM-822 — short form fields (no freehand draw). */
const mockMessageClarificationForm: ResearchMessage = {
  id: "msg-00000000-0000-0000-0000-000000000005",
  session_id: sessionBase.id,
  sender_type: "agent",
  sender_id: "agent-mock-lead",
  target_agent_id: null,
  body: "Share hard constraints for the comparison.",
  card_kind: "process",
  meta: {
    op: "clarification_question",
    question_id: "clarify-form-001",
    prompt: "Share hard constraints for the comparison.",
    layout: "form",
    allow_skip: true,
    fields: [
      {
        id: "budget",
        label: "Monthly budget (USD)",
        type: "text",
        required: true,
        placeholder: "e.g. 2000",
      },
      {
        id: "latency",
        label: "p95 latency target",
        type: "text",
        placeholder: "e.g. < 40ms",
      },
      {
        id: "notes",
        label: "Other constraints",
        type: "textarea",
        placeholder: "Compliance, team skills, launch window…",
      },
    ],
  },
  created_at: now(),
};

const mockMessages: ResearchMessage[] = [
  mockMessageUser,
  mockMessageAgent,
  mockMessageWakeFailed,
];

const mockFleet = {
  id: sessionBase.fleet_id,
  workspace_id: sessionBase.workspace_id,
  lead_agent_id: "agent-mock-lead",
  members: [
    {
      id: "fm-00000000-0000-0000-0000-000000000001",
      agent_id: "agent-mock-lead",
      role: "research lead",
      status: "active",
      is_lead: true,
      name: "Lead",
      display_name: "Research Lead",
      avatar_url: null,
    },
    {
      id: "fm-00000000-0000-0000-0000-000000000002",
      agent_id: "agent-mock-scout",
      role: "scout",
      status: "active",
      is_lead: false,
      name: "Scout",
      display_name: "Scout",
      avatar_url: null,
    },
  ],
  created_at: now(),
  updated_at: now(),
};

/** Full session snapshot — the default development state. */
export const mockResearchSnapshotDefault: ResearchSessionSnapshot = {
  session: sessionBase,
  fleet: mockFleet,
  nodes: mockNodes,
  edges: mockEdges,
  sources: mockSources,
  report: mockReport,
  evals: mockEvals,
  messages: mockMessages,
};

/** Empty snapshot — first-visit / no sessions yet. */
export const mockResearchSnapshotEmpty: ResearchSessionSnapshot = {
  session: { ...sessionBase, id: "sess-empty", status: "drafting", current_stage: "s1_plan" },
  fleet: { ...mockFleet, id: "fleet-empty", members: [] },
  nodes: [],
  edges: [],
  sources: [],
  report: null,
  evals: [],
  messages: [],
};

/** Loading placeholder — snapshot shape with empty collections (skeleton state). */
export const mockResearchSnapshotLoading: ResearchSessionSnapshot = {
  session: { ...sessionBase, id: "sess-loading", status: "drafting" },
  fleet: { ...mockFleet, id: "fleet-loading", members: [] },
  nodes: [],
  edges: [],
  sources: [],
  report: null,
  evals: [],
  messages: [],
};

/** Error snapshot — session row plus wake_failed process card (LRM-823/828). */
export const mockResearchSnapshotError: ResearchSessionSnapshot = {
  session: { ...sessionBase, id: "sess-error", status: "running" },
  fleet: mockFleet,
  nodes: mockNodes,
  edges: mockEdges,
  sources: mockSources,
  report: mockReport,
  evals: mockEvals,
  messages: [mockMessageWakeFailed],
};

/** Clarification snapshot — list + form controls for LRM-822 Gate Shots / harness. */
export const mockResearchSnapshotClarification: ResearchSessionSnapshot = {
  session: { ...sessionBase, id: "sess-clarify", status: "running" },
  fleet: mockFleet,
  nodes: mockNodes,
  edges: mockEdges,
  sources: mockSources,
  report: mockReport,
  evals: mockEvals,
  messages: [
    mockMessageUser,
    mockMessageClarificationList,
    mockMessageClarificationForm,
  ],
};

/** LRM-840 — stage gate awaiting human approve/reject (status visible, not frozen). */
export const mockResearchSnapshotAwaitingConfirm: ResearchSessionSnapshot = {
  session: {
    ...sessionBase,
    id: "sess-awaiting-confirm",
    status: "awaiting_user_confirm",
    current_stage: "s4_delivery",
  },
  fleet: mockFleet,
  nodes: mockNodes,
  edges: mockEdges,
  sources: mockSources,
  report: mockReport,
  evals: mockEvals,
  messages: [mockMessageUser],
};

export const mockResearchSessionsList: ListResearchSessionsResponse = {
  sessions: [sessionBase],
};

export const mockResearchSessionsListEmpty: ListResearchSessionsResponse = {
  sessions: [],
};

export const mockCreateResearchSessionResponse: CreateResearchSessionResponse = {
  session: sessionBase,
  fleet: mockFleet,
  nodes: mockNodes.slice(0, 3),
  edges: mockEdges.slice(0, 2),
  messages: [mockMessageUser],
};

/**
 * Explicit mock api surface for tests / storybook / dev harnesses.
 * Swap in via query-client override or DI — never silently in prod.
 */
export const researchMocks = {
  api: {
    ensureResearchFleet: async () => mockFleet,
    listResearchSessions: async () => mockResearchSessionsList,
    createResearchSession: async () => mockCreateResearchSessionResponse,
    getResearchSessionSnapshot: async () => mockResearchSnapshotDefault,
    getResearchPresence: async () => ({
      session_id: sessionBase.id,
      presence: {
        "agent-mock-scout": {
          activity: "fetching benchmarks",
          updated_at: Math.floor(Date.now() / 1000),
        },
      },
    }),
  },
  snapshots: {
    default: mockResearchSnapshotDefault,
    empty: mockResearchSnapshotEmpty,
    loading: mockResearchSnapshotLoading,
    error: mockResearchSnapshotError,
    clarification: mockResearchSnapshotClarification,
    awaitingConfirm: mockResearchSnapshotAwaitingConfirm,
  },
  lists: {
    default: mockResearchSessionsList,
    empty: mockResearchSessionsListEmpty,
  },
  createResponse: mockCreateResearchSessionResponse,
};

export type ResearchMockSnapshotState = keyof typeof researchMocks.snapshots;
