import { z } from "zod";
import type {
  Agent,
  AgentPresenceResponse,
  AgentFileContentResponse,
  AgentFilesResponse,
  AgentTemplate,
  AgentTemplateSummary,
  Attachment,
  BillingBalance,
  BillingBatchesPage,
  BillingCheckoutSessionStatus,
  BillingPriceTier,
  BillingTopupsPage,
  BillingTransactionsPage,
  CancelTaskResponse,
  CreateAgentFromTemplateResponse,
  EvolutionMetricsResponse,
  EvolutionTrainingExampleListResponse,
  EvolutionModelRuntimeConfigListResponse,
  EvolutionModelEvalRunListResponse,
  EvolutionReviewSubmission,
  MemoryCurationRunDetail,
  WorkspaceMemoryCurationStatus,
  CreateBillingCheckoutSessionResponse,
  CreateBillingPortalSessionResponse,
  GroupedIssuesResponse,
  ProjectGroupedIssuesResponse,
  ChannelMessageSearchResponse,
  ChannelMessagesPage,
  ChannelThreadMessagesPage,
  AgentHealthResponse,
  AgentRuntime,
  ComputerConnection,
  MachineUpgrade,
  StickerCatalogResponse,
  ListIssuesResponse,
  TimelineEntry,
  User,
  Workspace,
  UpdateAgentFileContentResponse,
  SandboxNodeDockerImagesResponse,
  SandboxNodeTemplatesResponse,
  SandboxSnapshot,
  WebPushPublicKeyResponse,
  WebPushSubscriptionResponse,
  WebPushTestResponse,
  VoiceCall,
  VoiceCallMedia,
  CreateVoiceCallResponse,
  GetVoiceCallResponse,
  EnsureWindyResponse,
  StartVoiceCallDuplexResponse,
  VoiceCallDuplexAudioHint,
  VoiceCallDuplexEventHint,
  WorkspaceSearchResponse,
  RuntimeAgentWorkspacesResponse,
  NotePage,
  NotePageListResponse,
  NoteAIJob,
  NotePageIssueRef,
  NotePageIssueRefListResponse,
} from "../types";
import type { CloudRuntimeNode } from "../runtimes/cloud-runtime";
import type { RawReminderPage } from "../agents/reminder-view-model";

export const NotePageIssueRefSchema: z.ZodType<NotePageIssueRef> = z.object({
  type: z.literal("issue").default("issue"),
  id: z.string().default(""),
  label: z.string().nullable().optional(),
  accessible: z.boolean().default(false),
  page_id: z.string().optional(),
  issue_id: z.string().optional(),
  workspace_id: z.string().optional(),
  identifier: z.string().optional(),
  title: z.string().optional(),
  number: z.number().optional(),
  created_at: z.string().optional(),
}).loose();

export const NotePageIssueRefListResponseSchema: z.ZodType<NotePageIssueRefListResponse> = z.object({
  refs: z.array(NotePageIssueRefSchema).default([]),
}).loose();

export const EMPTY_NOTE_PAGE_ISSUE_REF: NotePageIssueRef = {
  type: "issue",
  id: "",
  accessible: false,
};

export const EMPTY_NOTE_PAGE_ISSUE_REF_LIST: NotePageIssueRefListResponse = { refs: [] };

export const NotePageSchema: z.ZodType<NotePage> = z.object({
  id: z.string().default(""),
  workspace_id: z.string().default(""),
  parent_id: z.string().nullable().default(null),
  owner_user_id: z.string().default(""),
  title: z.string().default("Untitled"),
  content: z.string().default(""),
  sort_key: z.string().default(""),
  share_user_ids: z.array(z.string()).default([]),
  can_manage_shares: z.boolean().default(false),
  created_at: z.string().default(""),
  updated_at: z.string().default(""),
  deleted_at: z.string().nullable().default(null),
  refs: z.array(NotePageIssueRefSchema).optional(),
}).loose();

export const NotePageListResponseSchema: z.ZodType<NotePageListResponse> = z.object({
  pages: z.array(NotePageSchema).default([]),
}).loose();

export const EMPTY_NOTE_PAGE: NotePage = {
  id: "",
  workspace_id: "",
  parent_id: null,
  owner_user_id: "",
  title: "Untitled",
  content: "",
  sort_key: "",
  share_user_ids: [],
  can_manage_shares: false,
  created_at: "",
  updated_at: "",
  deleted_at: null,
  refs: [],
};

export const EMPTY_NOTE_PAGE_LIST: NotePageListResponse = { pages: [] };

export const NoteAIEditResultSchema = z.object({
  action: z.enum(["insert", "replace_selection", "replace_page", "patch"]),
  markdown: z.string().default(""),
  target: z.string().nullable().optional(),
  title: z.string().nullable().optional(),
  rationale: z.string().nullable().optional(),
}).loose();

export const NoteAIJobSchema: z.ZodType<NoteAIJob> = z.object({
  id: z.string().default(""),
  workspace_id: z.string().default(""),
  page_id: z.string().default(""),
  agent_id: z.string().default(""),
  chat_session_id: z.string().default(""),
  task_id: z.string().default(""),
  status: z.enum(["queued", "dispatched", "running", "completed", "failed", "cancelled"]).catch("queued"),
  result: NoteAIEditResultSchema.nullable().optional(),
  failure_reason: z.string().nullable().optional(),
  failure_code: z.enum(["invalid_structured_output", "assistant_failure", "task_failure", "task_error"]).nullable().optional(),
  repair_code: z.enum(["repaired_selected_output", "repaired_page_output"]).nullable().optional(),
  created_at: z.string().default(""),
  updated_at: z.string().optional(),
}).loose();

export const EMPTY_NOTE_AI_JOB: NoteAIJob = {
  id: "",
  workspace_id: "",
  page_id: "",
  agent_id: "",
  chat_session_id: "",
  task_id: "",
  status: "queued",
  result: null,
  failure_reason: null,
  failure_code: null,
  repair_code: null,
  created_at: "",
  updated_at: "",
};

export const ChannelGoalSchema = z.object({
  id: z.string(),
  workspace_id: z.string(),
  channel_id: z.string(),
  title: z.string(),
  objective: z.string(),
  success_criteria: z.array(z.string()).default([]),
  status: z.enum(["active", "paused", "completed", "cancelled"]),
  version: z.number(),
  progress_summary: z.string().default(""),
  current_step: z.string().default(""),
  blocker: z.string().default(""),
  evidence_refs: z.array(z.string()).default([]),
  completed_criteria: z.array(z.string()).default([]),
  created_by_type: z.enum(["user", "agent"]),
  created_by_id: z.string(),
  updated_by_type: z.enum(["user", "agent"]),
  updated_by_id: z.string(),
  created_at: z.string(),
  updated_at: z.string(),
  completed_at: z.string().optional(),
  work_graph: z.object({
    id: z.string(),
    version: z.number(),
    status: z.enum(["active", "paused", "deliverable", "completed", "cancelled", "failed"]),
    total: z.number().default(0),
    completed: z.number().default(0),
    running: z.number().default(0),
    waiting: z.number().default(0),
    stale: z.number().default(0),
  }).optional(),
}).loose();

export const ChannelGoalEnvelopeSchema = z.object({
  goal: ChannelGoalSchema.nullable().default(null),
}).loose();

export const WorkGraphDetailSchema = z.object({
  id: z.string(), workspace_id: z.string(), anchor_kind: z.string(), anchor_id: z.string(),
  status: z.string(), current_version: z.number(), admission_decision: z.enum(["GRAPH", "PROPOSE_GRAPH"]),
  nodes: z.array(z.object({
    id: z.string(), issue_id: z.string(), role: z.string(), context_policy: z.string(),
    execution_status: z.string(), validity_status: z.string(), review_status: z.string(),
    completion_authority: z.enum(["issue_status", "kernel_evidence"]).default("kernel_evidence"),
    effective_completion: z.enum(["pending", "satisfied", "stale", "revoked"]).default("pending"),
    objective: z.string().default(""), completion_contract: z.array(z.string()).default([]),
    based_on_graph_version: z.number(),
  }).loose()).default([]),
  edges: z.array(z.object({ id:z.string(),from_node_id:z.string(),to_node_id:z.string(),edge_type:z.string(),required:z.boolean() }).loose()).default([]),
}).loose();

export const ChannelGoalProcessMarkdownSchema = z.object({
  id: z.string(),
  workspace_id: z.string(),
  channel_id: z.string(),
  goal_id: z.string(),
  manager_agent_id: z.string(),
  content: z.string().default(""),
  version: z.number(),
  updated_by_type: z.enum(["user", "agent"]),
  updated_by_id: z.string(),
  created_at: z.string(),
  updated_at: z.string(),
}).loose();

export const ChannelGoalProcessEnvelopeSchema = z.object({
  process: ChannelGoalProcessMarkdownSchema.nullable().default(null),
}).loose();

export const ChannelGoalProcessListEnvelopeSchema = z.object({
  goal_id: z.string().default(""),
  processes: z.array(ChannelGoalProcessMarkdownSchema).default([]),
}).loose();

export const ChannelGoalSubgoalActorSchema = z.object({
  type: z.enum(["agent", "member"]),
  id: z.string(),
}).loose();

export const ChannelGoalSubgoalWaitingOnSchema = z.object({
  kind: z.enum(["member", "issue", "pr", "lock", "external"]),
  target_id: z.string().optional(),
  note: z.string().optional(),
}).loose();

export const ChannelGoalSubgoalSchema = z.object({
  id: z.string().default(""),
  workspace_id: z.string().default(""),
  channel_id: z.string().default(""),
  goal_id: z.string().default(""),
  title: z.string().default(""),
  purpose: z.string().default(""),
  completion_boundary: z.string().default(""),
  brief: z.string().default(""),
  current_conclusion: z.string().default(""),
  status: z.enum(["captured", "in_progress", "waiting", "resolved", "cancelled"]).default("captured"),
  version: z.number().default(0),
  responsible_type: z.string().default(""),
  responsible_id: z.string().default(""),
  participants: z.array(ChannelGoalSubgoalActorSchema).default([]),
  depends_on: z.array(z.string()).default([]),
  waiting_on: ChannelGoalSubgoalWaitingOnSchema.nullable().default(null),
  artifact_refs: z.array(z.string()).default([]),
  activity_delta: z.array(z.string()).default([]),
  source_message_id: z.string().optional(),
  created_by_type: z.string().default(""),
  created_by_id: z.string().default(""),
  updated_by_type: z.string().default(""),
  updated_by_id: z.string().default(""),
  created_at: z.string().default(""),
  updated_at: z.string().default(""),
  resolved_at: z.string().optional(),
}).loose();

export const ChannelGoalSubgoalListEnvelopeSchema = z.object({
  subgoals: z.array(ChannelGoalSubgoalSchema).default([]),
}).loose();

export const ChannelGoalSubgoalEnvelopeSchema = z.object({
  subgoal: ChannelGoalSubgoalSchema.nullable().default(null),
}).loose();

export const EMPTY_CHANNEL_GOAL_SUBGOAL_LIST = { subgoals: [] };


export interface AppConfigResponse {
  cdn_domain: string;
  environment: "production" | "test";
  allow_signup: boolean;
  google_client_id?: string;
  posthog_key?: string;
  posthog_host?: string;
  analytics_environment?: string;
  daemon_server_url?: string;
  daemon_app_url?: string;
  workspace_creation_disabled?: boolean;
  dev_agent_profile_access_enabled?: boolean;
}

export interface DeleteComputerResponse {
  status: string;
  daemon_id: string;
  deleted_count: number;
  deleted_runtime_ids: string[];
  tasks_cancelled: number;
}

export const DeleteComputerResponseSchema = z.object({
  status: z.string(),
  daemon_id: z.string(),
  deleted_count: z.number(),
  deleted_runtime_ids: z.array(z.string()).default([]),
  tasks_cancelled: z.number().default(0),
}).loose();

export const EMPTY_DELETE_COMPUTER_RESPONSE: DeleteComputerResponse = {
  status: "invalid_response",
  daemon_id: "",
  deleted_count: 0,
  deleted_runtime_ids: [],
  tasks_cancelled: 0,
};

const DaemonUpdateStatusSchema = z.object({
  session_id: z.string(),
  revision: z.number().int().positive(),
  observed_at: z.string(),
  auto_update_effective_enabled: z.boolean(),
  config_source: z.enum([
    "official_host_default",
    "self_host_default",
    "env_enabled",
    "env_disabled",
    "cli_disabled",
    "deprecated_noop",
    "auto_detect",
  ]),
  ineligible_reason: z
    .enum(["desktop_managed", "non_release_build", "explicit_only"])
    .nullable()
    .default(null),
  check_interval_seconds: z.number().int().positive(),
  phase: z.enum([
    "disabled",
    "waiting",
    "checking",
    "updating",
    "restart_pending",
  ]),
  attempt_source: z.enum(["auto", "server"]).nullable().default(null),
  last_attempt_at: z.string().nullable().default(null),
  last_outcome: z.enum([
    "never_checked",
    "up_to_date",
    "update_available",
    "busy",
    "fetch_failed",
    "update_failed",
    "verification_failed",
    "update_succeeded",
    "interrupted",
    "explicit_only",
  ]),
  target_version: z.string().nullable().default(null),
  error_code: z
    .enum([
      "daemon_restarted_during_update",
      "release_fetch_failed",
      "download_update_failed",
      "updated_binary_verification_failed",
      "desktop_managed",
    ])
    .nullable()
    .default(null),
  error_message: z.string().nullable().default(null),
  staged_version: z.string().nullable().default(null),
  activation_generation: z.number().int().nonnegative().nullable().default(null),
  received_at: z.string(),
  updated_at: z.string(),
}).loose();

export const AgentRuntimeSchema = z.object({
  id: z.string(),
  workspace_id: z.string(),
  daemon_id: z.string().nullable(),
  name: z.string(),
  runtime_mode: z.enum(["local", "cloud"]),
  provider: z.string(),
  launch_header: z.string().default(""),
  status: z.enum(["online", "offline"]),
  device_info: z.string().default(""),
  // Machine label from daemon register (metadata.device_name). Older servers omit it.
  device_name: z.string().optional(),
  // Daemon-reported GOOS. Older servers and daemons omit it.
  os: z.string().optional(),
  metadata: z.record(z.string(), z.unknown()).catch({}),
  capabilities: z.array(z.string()).optional(),
  current_version: z.string().nullable(),
  daemon_target_version: z.string().nullable().optional(),
  target_version: z.string().nullable().optional(),
  update_state: z
    .enum([
      "idle",
      "pending",
      "running",
      "completed",
      "ready_to_apply",
      "failed",
      "timed_out",
    ])
    .catch("idle"),
  runtime_health: z
    .enum(["ok", "update_available", "updating", "failed", "offline"])
    .catch("offline"),
  update_error: z.string().nullable().optional(),
  machine_upgrade: z
    .object({
      id: z.string(),
      daemon_id: z.string(),
      request_id: z.string(),
      requested_target: z.string(),
      resolved_target: z.string().nullable().optional(),
      phase: z.string(),
      result: z.string().nullable().optional(),
      error_code: z.string().nullable().optional(),
      error_message: z.string().nullable().optional(),
      accepted_at: z.string().nullable().optional(),
      accepted_generation: z.string().nullable().optional(),
      accepted_runtime_ids: z.array(z.string()).optional(),
      attested_runtime_ids: z.array(z.string()).optional(),
      source_version: z.string().nullable().optional(),
      rollback_generation: z.string().nullable().optional(),
      rollback_runtime_ids: z.array(z.string()).optional(),
      completed_at: z.string().nullable().optional(),
      created_at: z.string(),
      updated_at: z.string(),
    })
    .loose()
    .nullable()
    .optional()
    .catch(null),
  // Unknown/malformed future update observations degrade only this optional
  // field. The runtime row remains usable by older installed desktop builds.
  auto_update: DaemonUpdateStatusSchema.nullable().optional().catch(null),
  owner_id: z.string().nullable(),
  // Default private when missing so older servers fail closed for binding UI.
  visibility: z.enum(["private", "public"]).catch("private"),
  last_seen_at: z.string().nullable(),
  created_at: z.string(),
  updated_at: z.string(),
}).loose();

export const AgentRuntimeListSchema = z.array(AgentRuntimeSchema);
export const EMPTY_AGENT_RUNTIME_LIST: AgentRuntime[] = [];

export const ComputerConnectionSchema = z.object({
  daemon_id: z.string().min(1),
  owner_id: z.string().min(1),
  connected: z.boolean(),
  last_seen_at: z.string().nullable(),
}).loose();
export const ComputerConnectionListSchema = z.array(ComputerConnectionSchema);
export const EMPTY_COMPUTER_CONNECTION_LIST: ComputerConnection[] = [];

export const MachineUpgradeSchema = z.object({
  id: z.string(),
  daemon_id: z.string(),
  request_id: z.string(),
  requested_target: z.string(),
  resolved_target: z.string().nullable().optional(),
  phase: z.string().default("failed"),
  result: z.string().nullable().optional(),
  error_code: z.string().nullable().optional(),
  error_message: z.string().nullable().optional(),
  accepted_at: z.string().nullable().optional(),
  accepted_generation: z.string().nullable().optional(),
  accepted_runtime_ids: z.array(z.string()).default([]),
  attested_runtime_ids: z.array(z.string()).default([]),
  source_version: z.string().nullable().optional(),
  rollback_generation: z.string().nullable().optional(),
  rollback_runtime_ids: z.array(z.string()).default([]),
  completed_at: z.string().nullable().optional(),
  created_at: z.string(),
  updated_at: z.string(),
}).loose();

export const EMPTY_MACHINE_UPGRADE: MachineUpgrade = {
  id: "",
  daemon_id: "",
  request_id: "",
  requested_target: "",
  phase: "failed",
  accepted_runtime_ids: [],
  attested_runtime_ids: [],
  rollback_runtime_ids: [],
  created_at: "",
  updated_at: "",
};

// ---------------------------------------------------------------------------
// Schemas for the highest-risk API endpoints — those whose responses drive
// the issue detail page (timeline, comments, subscribers) and the issues
// list. These are the surfaces that white-screened in #2143 / #2147 / #2192.
//
// These schemas are intentionally LENIENT:
//   - String enums are stored as `z.string()` rather than `z.enum([...])`.
//     A new server-side enum value should render as a generic fallback in
//     the UI, never crash a `safeParse`.
//   - Optional fields are unioned with `null` and given fallbacks where
//     existing UI code already coerces them.
//   - Arrays default to `[]` so a missing `reactions` / `attachments` /
//     `entries` field doesn't take the page down.
//   - Every object schema ends with `.loose()` so unknown server-side
//     fields pass through unchanged. zod 4's `.object()` defaults to STRIP,
//     which would silently delete fields the schema didn't explicitly list
//     — fine while the TS type doesn't claim them, but the moment a future
//     PR adds a TS field without updating the schema, the cast `as T` lies
//     and the field shows up as `undefined` at runtime. `.loose()` removes
//     that synchronisation hazard.
//
// These schemas are deliberately not typed as `z.ZodType<TimelineEntry>` /
// `z.ZodType<Issue>` etc. — the strict TS types narrow string fields to
// literal unions, which would defeat the leniency above. `parseWithFallback`
// returns the parsed value cast to the caller-supplied `T`, so the strict
// type still flows out at the call site; the schema only guards shape.
// ---------------------------------------------------------------------------

const ReactionSchema = z.object({
  id: z.string(),
  comment_id: z.string(),
  actor_type: z.string(),
  actor_id: z.string(),
  emoji: z.string(),
  created_at: z.string(),
});

// Nested attachments embedded in timeline/comment responses stay lenient on
// purpose: a single malformed attachment must not knock the whole timeline
// into the fallback `[]`.
const AttachmentSchema = z.object({
  id: z.string(),
}).loose();

// Standalone attachment lookup (`GET /api/attachments/{id}`) is the source of
// truth for click-time download URLs. The two fields the download flow opens
// in a new tab — `download_url` and `url` — must be strings, otherwise we'd
// happily `window.open(undefined)`. `filename` gates the toast/title and is
// also enforced so a missing value falls back to the empty record below.
//
// `markdown_url` is parsed lenient: a server old enough to predate
// MUL-3192 omits the field, in which case the schema defaults it to "".
// Callers that need to persist a URL into markdown should go through the
// `useFileUpload` helper (which falls back to the legacy
// `attachmentDownloadPath` shape when `markdown_url` is empty), so the
// empty-string default does not silently break any persistence path.
export const AttachmentResponseSchema = z.object({
  id: z.string(),
  url: z.string(),
  download_url: z.string(),
  markdown_url: z.string().optional().default(""),
  filename: z.string(),
  chat_session_id: z.string().nullable().optional(),
  chat_message_id: z.string().nullable().optional(),
}).loose();

export const EMPTY_ATTACHMENT: Attachment = {
  id: "",
  workspace_id: "",
  issue_id: null,
  comment_id: null,
  chat_session_id: null,
  chat_message_id: null,
  uploader_type: "",
  uploader_id: "",
  filename: "",
  url: "",
  download_url: "",
  markdown_url: "",
  content_type: "",
  size_bytes: 0,
  created_at: "",
};

// All object schemas use `.loose()` so unknown server-side fields pass
// through unchanged. zod 4's `.object()` defaults to STRIP, which would
// silently drop new fields and surface as a "field neither showed up in
// the UI" mystery the next time the TS type adopted them but the schema
// wasn't updated in lock-step. `.loose()` removes that synchronisation
// hazard — the schema validates the shape it knows about and leaves the
// rest alone.
const TimelineEntrySchema = z.object({
  type: z.string(),
  id: z.string(),
  actor_type: z.string(),
  actor_id: z.string(),
  created_at: z.string(),
  action: z.string().optional(),
  details: z.record(z.string(), z.unknown()).optional(),
  content: z.string().optional(),
  parent_id: z.string().nullable().optional(),
  updated_at: z.string().optional(),
  comment_type: z.string().optional(),
  reactions: z.array(ReactionSchema).optional(),
  attachments: z.array(AttachmentSchema).optional(),
  coalesced_count: z.number().optional(),
}).loose();

// /timeline returns a flat array of TimelineEntry, oldest first. The
// previously cursor-paginated wrapper was removed (#1929) — at observed data
// sizes (p99 ~30 entries per issue) paged delivery only created bugs.
export const TimelineEntriesSchema = z.array(TimelineEntrySchema);

export const EMPTY_TIMELINE_ENTRIES: TimelineEntry[] = [];

const OptionalStringSchema = z.preprocess(
  (value) => (typeof value === "string" ? value : undefined),
  z.string().optional(),
);

const BooleanWithDefaultSchema = (fallback: boolean) =>
  z.preprocess(
    (value) => (typeof value === "boolean" ? value : undefined),
    z.boolean().default(fallback),
  );

const ServiceEnvironmentSchema = z.preprocess(
  (value) => (value === "production" || value === "test" ? value : undefined),
  z.enum(["production", "test"]).default("production"),
);

export const AppConfigSchema = z.object({
  cdn_domain: z.string().default(""),
  environment: ServiceEnvironmentSchema,
  allow_signup: BooleanWithDefaultSchema(true),
  google_client_id: OptionalStringSchema,
  posthog_key: OptionalStringSchema,
  posthog_host: OptionalStringSchema,
  analytics_environment: OptionalStringSchema,
  daemon_server_url: OptionalStringSchema,
  daemon_app_url: OptionalStringSchema,
  workspace_creation_disabled: BooleanWithDefaultSchema(false).optional(),
  dev_agent_profile_access_enabled: BooleanWithDefaultSchema(false).optional(),
}).loose();

export const EMPTY_APP_CONFIG: AppConfigResponse = {
  cdn_domain: "",
  environment: "production",
  allow_signup: true,
  google_client_id: "",
  daemon_server_url: "",
  daemon_app_url: "",
  workspace_creation_disabled: false,
  dev_agent_profile_access_enabled: false,
};

export const WebPushPublicKeySchema = z.object({
  public_key: z.string().default(""),
  enabled: BooleanWithDefaultSchema(false),
}).loose();

export const EMPTY_WEB_PUSH_PUBLIC_KEY: WebPushPublicKeyResponse = {
  public_key: "",
  enabled: false,
};

export const WebPushSubscriptionSchema = z.object({
  id: z.string().default(""),
  workspace_id: z.string().default(""),
  user_id: z.string().default(""),
  endpoint: z.string().default(""),
  expiration_time: OptionalStringSchema,
  device_id: OptionalStringSchema,
  user_agent: OptionalStringSchema,
  last_active_at: z.string().default(""),
}).loose();

export const EMPTY_WEB_PUSH_SUBSCRIPTION: WebPushSubscriptionResponse = {
  id: "",
  workspace_id: "",
  user_id: "",
  endpoint: "",
  last_active_at: "",
};

export const WebPushTestSchema = z.object({
  ok: BooleanWithDefaultSchema(false),
  delivered: z.number().default(0),
  failed: z.number().default(0),
  gone: z.number().default(0),
  attempted: z.number().default(0),
}).loose();

export const EMPTY_WEB_PUSH_TEST: WebPushTestResponse = {
  ok: false,
  delivered: 0,
  failed: 0,
  gone: 0,
  attempted: 0,
};

export const CommentSchema = z.object({
  id: z.string(),
  issue_id: z.string(),
  author_type: z.string(),
  author_id: z.string(),
  content: z.string(),
  type: z.string(),
  parent_id: z.string().nullable(),
  reactions: z.array(ReactionSchema).default([]),
  attachments: z.array(AttachmentSchema).default([]),
  created_at: z.string(),
  updated_at: z.string(),
}).loose();

export const CommentsListSchema = z.array(CommentSchema);

const CommentTriggerPreviewAgentSchema = z.object({
  id: z.string(),
  name: z.string().default(""),
  avatar_url: z.string().optional(),
  source: z.string().default(""),
  reason: z.string().default(""),
}).loose();

export const CommentTriggerPreviewSchema = z.object({
  agents: z.array(CommentTriggerPreviewAgentSchema).default([]),
}).loose();

const evolutionString = (fallback = "") => z.preprocess(
  (value) => typeof value === "string" ? value : fallback,
  z.string(),
);
const evolutionStringArray = z.preprocess(
  (value) => Array.isArray(value) ? value.filter((item): item is string => typeof item === "string") : [],
  z.array(z.string()),
);
const evolutionObject = <T extends z.ZodRawShape>(shape: T) => z.preprocess(
  (value) => value && typeof value === "object" && !Array.isArray(value) ? value : {},
  z.object(shape).loose(),
);

const EvolutionReviewFileSchema = evolutionObject({
  id: evolutionString(),
  path: evolutionString(),
  content: evolutionString().optional(),
  content_hash: evolutionString(),
  mime_type: evolutionString(),
  size_bytes: z.preprocess((value) => typeof value === "number" ? value : 0, z.number()),
  created_at: z.string().nullable().optional(),
});

const EvolutionMaterializedSkillSchema = evolutionObject({
  id: z.string(),
  name: evolutionString(),
  description: evolutionString(),
});

const EvolutionReviewEvidenceSchema = evolutionObject({
  source: evolutionString(),
  source_date: evolutionString(),
  evidence_refs: evolutionStringArray,
});

const EvolutionReviewAppliesSchema = evolutionObject({
  scope: evolutionString(),
  tags: evolutionStringArray,
  tools: evolutionStringArray,
  task_types: evolutionStringArray,
  project_types: evolutionStringArray,
  languages: evolutionStringArray,
  frameworks: evolutionStringArray,
});

export const EvolutionReviewSubmissionSchema = z.object({
  id: z.string(),
  workspace_id: z.string().default(""),
  source_agent_id: z.string().default(""),
  source_member_id: z.string().optional(),
  unit_type: z.string().default(""),
  local_unit_id: z.string().default(""),
  title: z.string().default(""),
  summary: z.string().default(""),
  content: z.string().optional(),
  content_hash: z.string().default(""),
  bundle_hash: z.string().default(""),
  bundle_ref: z.string().default(""),
  sensitivity: z.string().default(""),
  confidence: z.string().default(""),
  suggested_scope: z.string().default(""),
  evidence: EvolutionReviewEvidenceSchema.default({ source: "", source_date: "", evidence_refs: [] }),
  applies: EvolutionReviewAppliesSchema.default({ scope: "", tags: [], tools: [], task_types: [], project_types: [], languages: [], frameworks: [] }),
  tags: evolutionStringArray,
  tools: evolutionStringArray,
  task_types: evolutionStringArray,
  project_types: evolutionStringArray,
  languages: evolutionStringArray,
  frameworks: evolutionStringArray,
  status: z.string().default("needs_review"),
  reject_reason: z.string().default(""),
  review_decision: z.string().default(""),
  review_confidence: z.number().nullable().optional(),
  review_risk_level: z.string().default(""),
  review_reason: z.string().default(""),
  review_metadata: z.preprocess(
    (value) => value && typeof value === "object" && !Array.isArray(value) ? value : {},
    z.record(z.string(), z.unknown()),
  ),
  reviewed_at: z.string().nullable().optional(),
  promoted_unit_id: z.string().nullable().optional(),
  materialized_skill: z.preprocess(
    (value) => value && typeof value === "object" && !Array.isArray(value) ? value : undefined,
    EvolutionMaterializedSkillSchema.optional(),
  ),
  source_created_at: z.string().nullable().optional(),
  created_at: z.string().nullable().optional(),
  updated_at: z.string().nullable().optional(),
  files: z.preprocess(
    (value) => value == null ? undefined : Array.isArray(value) ? value : undefined,
    z.array(EvolutionReviewFileSchema).optional(),
  ),
}).loose();

export const EvolutionReviewSubmissionListSchema = z.array(EvolutionReviewSubmissionSchema);

export const EMPTY_EVOLUTION_REVIEW_SUBMISSION_LIST: EvolutionReviewSubmission[] = [];

const EvolutionUnitMetricSchema = z.object({
  unit_id: z.string().nullable().optional(),
  local_unit_id: z.string().default(""),
  unit_type: z.string().default(""),
  title: z.string().default(""),
  injected_count: z.number().default(0),
  used_count: z.number().default(0),
  success_count: z.number().default(0),
  failure_count: z.number().default(0),
  ignored_count: z.number().default(0),
  conflict_count: z.number().default(0),
  success_rate: z.number().default(0),
  last_used_at: z.string().nullable().optional(),
}).loose();

const EvolutionDailyMetricSchema = z.object({
  date: z.string().default(""),
  memory_candidates: z.number().default(0),
  skill_candidates: z.number().default(0),
  promoted_memory: z.number().default(0),
  promoted_skill: z.number().default(0),
  team_knowledge_items: z.number().default(0),
  archived_or_deprecated: z.number().default(0),
  feedback_injected: z.number().default(0),
  feedback_used: z.number().default(0),
  feedback_success: z.number().default(0),
  feedback_failure: z.number().default(0),
  memory_curation_run_count: z.number().default(0),
  memory_curation_failed: z.number().default(0),
}).loose();

const EvolutionTaskEfficiencySchema = z.object({
  issue_count: z.number().default(0),
  average_duration_seconds: z.number().default(0),
  average_input_tokens: z.number().default(0),
  average_output_tokens: z.number().default(0),
  average_cache_read_tokens: z.number().default(0),
  average_cache_write_tokens: z.number().default(0),
  average_evolved_units_used: z.number().default(0),
  with_evolved_units_issue_count: z.number().default(0),
  without_evolved_units_issue_count: z.number().default(0),
}).loose();

const EvolutionCollaborationMetricSchema = z.object({
  unmentioned_messages: z.number().default(0),
  attention_rounds: z.number().default(0),
  attention_probes: z.number().default(0),
  attention_silent_rate: z.number().default(0),
  autonomous_claims: z.number().default(0),
  peer_converged: z.number().default(0),
  manager_fallbacks: z.number().default(0),
  full_execution_wakes: z.number().default(0),
  full_execution_reduction_rate: z.number().default(0),
  collaboration_sessions: z.number().default(0),
  turn_order_violation_rate: z.number().default(0),
  contribution_offers: z.number().default(0),
  contribution_offer_adoption_rate: z.number().default(0),
  contribution_offer_helpful_rate: z.number().default(0),
  unauthorized_public_sends_blocked: z.number().default(0),
  policies_retrieved: z.number().default(0),
  policies_used: z.number().default(0),
  policy_success_rate: z.number().default(0),
  attention_tokens: z.number().default(0),
  execution_tokens: z.number().default(0),
  estimated_tokens_saved: z.number().default(0),
  immutable_decision_audit_events: z.number().default(0),
}).loose();

const EMPTY_EVOLUTION_COLLABORATION_METRICS = {
  unmentioned_messages: 0,
  attention_rounds: 0,
  attention_probes: 0,
  attention_silent_rate: 0,
  autonomous_claims: 0,
  peer_converged: 0,
  manager_fallbacks: 0,
  full_execution_wakes: 0,
  full_execution_reduction_rate: 0,
  collaboration_sessions: 0,
  turn_order_violation_rate: 0,
  contribution_offers: 0,
  contribution_offer_adoption_rate: 0,
  contribution_offer_helpful_rate: 0,
  unauthorized_public_sends_blocked: 0,
  policies_retrieved: 0,
  policies_used: 0,
  policy_success_rate: 0,
  attention_tokens: 0,
  execution_tokens: 0,
  estimated_tokens_saved: 0,
  immutable_decision_audit_events: 0,
};

const EvolutionModelMetricSchema = z.object({
  attention_student_version: z.string().default(""),
  attention_student_mode: z.string().default("off"),
  missed_attention_rate: z.number().default(0),
  late_rescue_rate: z.number().default(0),
  context_filter_version: z.string().default(""),
  context_compression_rate: z.number().default(0),
  critical_context_recall: z.number().default(0),
}).loose();

const EMPTY_EVOLUTION_MODEL_METRICS = {
  attention_student_version: "",
  attention_student_mode: "off",
  missed_attention_rate: 0,
  late_rescue_rate: 0,
  context_filter_version: "",
  context_compression_rate: 0,
  critical_context_recall: 0,
};

const EMPTY_EVOLUTION_TASK_EFFICIENCY = {
  issue_count: 0,
  average_duration_seconds: 0,
  average_input_tokens: 0,
  average_output_tokens: 0,
  average_cache_read_tokens: 0,
  average_cache_write_tokens: 0,
  average_evolved_units_used: 0,
  with_evolved_units_issue_count: 0,
  without_evolved_units_issue_count: 0,
};

export const EvolutionMetricsSchema = z.object({
  unit_metrics: z.array(EvolutionUnitMetricSchema).default([]),
  daily_metrics: z.array(EvolutionDailyMetricSchema).default([]),
  task_efficiency: EvolutionTaskEfficiencySchema.default(EMPTY_EVOLUTION_TASK_EFFICIENCY),
  collaboration_evolution: EvolutionCollaborationMetricSchema.default(EMPTY_EVOLUTION_COLLABORATION_METRICS),
  model_evolution: EvolutionModelMetricSchema.default(EMPTY_EVOLUTION_MODEL_METRICS),
}).loose();

export const EMPTY_EVOLUTION_METRICS: EvolutionMetricsResponse = {
  unit_metrics: [],
  daily_metrics: [],
  task_efficiency: EMPTY_EVOLUTION_TASK_EFFICIENCY,
  collaboration_evolution: EMPTY_EVOLUTION_COLLABORATION_METRICS,
  model_evolution: EMPTY_EVOLUTION_MODEL_METRICS,
};

const JsonObjectSchema = z.record(z.string(), z.unknown()).default({});

export const EvolutionTrainingExampleSchema = z.object({
  id: z.string(),
  workspace_id: z.string(),
  model_kind: z.enum(["attention_student", "context_filter"]),
  source_kind: z.string(),
  source_id: z.string().optional(),
  agent_id: z.string().optional(),
  channel_id: z.string().optional(),
  message_id: z.string().optional(),
  input: JsonObjectSchema,
  teacher_label: JsonObjectSchema,
  student_prediction: JsonObjectSchema,
  split: z.enum(["unassigned", "train", "validation", "test", "holdout"]),
  status: z.enum(["candidate", "gold", "rejected", "archived"]),
  created_at: z.string(),
  updated_at: z.string(),
}).loose();

export const EvolutionTrainingExampleListSchema = z.object({
  workspace_id: z.string().default(""),
  examples: z.array(EvolutionTrainingExampleSchema).default([]),
  total: z.number().default(0),
}).loose();

export const EMPTY_EVOLUTION_TRAINING_EXAMPLE_LIST: EvolutionTrainingExampleListResponse = { workspace_id: "", examples: [], total: 0 };

export const EvolutionModelRuntimeConfigSchema = z.object({
  workspace_id: z.string(),
  model_kind: z.enum(["attention_student", "context_filter"]),
  mode: z.enum(["off", "shadow", "canary"]),
  active_version: z.string().default(""),
  candidate_version: z.string().default(""),
  rollout_percent: z.number().default(0),
  config: JsonObjectSchema,
  updated_by: z.string().optional(),
  created_at: z.string(),
  updated_at: z.string(),
}).loose();

export const EvolutionModelRuntimeConfigListSchema = z.object({
  configs: z.array(EvolutionModelRuntimeConfigSchema).default([]),
  total: z.number().default(0),
}).loose();

export const EMPTY_EVOLUTION_MODEL_RUNTIME_CONFIG_LIST: EvolutionModelRuntimeConfigListResponse = { configs: [], total: 0 };

export const EvolutionModelEvalRunSchema = z.object({
  id: z.string(),
  workspace_id: z.string(),
  model_kind: z.enum(["attention_student", "context_filter"]),
  model_version: z.string(),
  mode: z.enum(["offline", "shadow", "canary"]),
  status: z.enum(["completed", "running", "failed"]),
  dataset_filter: JsonObjectSchema,
  metrics: JsonObjectSchema,
  example_count: z.number().default(0),
  created_at: z.string(),
}).loose();

export const EvolutionModelEvalRunListSchema = z.object({
  eval_runs: z.array(EvolutionModelEvalRunSchema).default([]),
  total: z.number().default(0),
}).loose();

export const EMPTY_EVOLUTION_MODEL_EVAL_RUN_LIST: EvolutionModelEvalRunListResponse = { eval_runs: [], total: 0 };

const EMPTY_MEMORY_CURATION_RUN_STATS = {
  agents_scanned: 0,
  agents_changed: 0,
  daily_files_written: 0,
  review_candidates_added: 0,
  entries_promoted: 0,
  shared_candidates_added: 0,
  shared_candidates_synced: 0,
  entries_archived: 0,
  duplicates_merged: 0,
  conflicts_found: 0,
  evidence_collected: 0,
  error_count: 0,
};

const MemoryCurationRunStatsSchema = z.object({
  agents_scanned: z.number().default(0),
  agents_changed: z.number().default(0),
  daily_files_written: z.number().default(0),
  review_candidates_added: z.number().default(0),
  entries_promoted: z.number().default(0),
  shared_candidates_added: z.number().default(0),
  shared_candidates_synced: z.number().default(0),
  entries_archived: z.number().default(0),
  duplicates_merged: z.number().default(0),
  conflicts_found: z.number().default(0),
  evidence_collected: z.number().default(0),
  error_count: z.number().default(0),
}).loose();

const MemoryCurationStageStatusSchema = z.object({
  id: z.string(),
  stage: z.string().default(""),
  trigger_kind: z.string().default(""),
  status: z.string().default(""),
  stats: MemoryCurationRunStatsSchema.default(EMPTY_MEMORY_CURATION_RUN_STATS),
  error: z.string().optional(),
  created_at: z.string().default(""),
  started_at: z.string().nullable().optional(),
  finished_at: z.string().nullable().optional(),
}).loose();

const MemoryCurationRunDiagnosticSchema = z.object({
  severity: z.string().default(""),
  code: z.string().default(""),
  message: z.string().default(""),
  action: z.string().optional(),
}).loose();

const MemoryCurationTargetAgentSchema = z.object({
  id: z.string().default(""),
  name: z.string().default(""),
}).loose();

const MemoryCurationRunTimelineItemSchema = z.object({
  key: z.string().default(""),
  agent_id: z.string().optional(),
  label: z.string().default(""),
  status: z.string().default(""),
  timestamp: z.string().optional(),
  detail: z.string().optional(),
}).loose();

const MemoryCurationAgentRunSchema = z.object({
  workspace_id: z.string().default(""),
  agent_id: z.string().default(""),
  agent_name: z.string().optional(),
  root: z.string().default(""),
  changed: z.boolean().default(false),
  daily_files_written: z.number().default(0),
  review_candidates_added: z.number().default(0),
  skill_candidates_added: z.number().default(0),
  evidence_collected: z.number().default(0),
  conflicts_found: z.number().default(0),
  error: z.string().optional(),
  curator_output_excerpt: z.string().optional(),
}).loose();

const MemoryCurationChildRunSchema = z.object({
  id: z.string().default(""),
  parent_run_id: z.string().default(""),
  workspace_id: z.string().default(""),
  agent_id: z.string().default(""),
  agent_name: z.string().optional(),
  runtime_id: z.string().optional(),
  runtime_name: z.string().optional(),
  stage: z.string().default(""),
  status: z.string().default(""),
  attempt: z.number().default(0),
  started_at: z.string().nullable().optional(),
  finished_at: z.string().nullable().optional(),
  error: z.string().optional(),
  changed: z.boolean().default(false),
  daily_files_written: z.number().default(0),
  review_candidates_added: z.number().default(0),
  skill_candidates_added: z.number().default(0),
  evidence_collected: z.number().default(0),
  conflicts_found: z.number().default(0),
  output_excerpt: z.string().optional(),
}).loose();

const MemoryCurationRunArtifactSchema = z.object({
  kind: z.string().default(""),
  title: z.string().default(""),
  agent_id: z.string().optional(),
  detail: z.string().optional(),
  content: z.string().optional(),
}).loose();

export const MemoryCurationRunDetailSchema: z.ZodType<MemoryCurationRunDetail> = MemoryCurationStageStatusSchema.extend({
  workspace_id: z.string().default(""),
  agent_id: z.string().nullable().optional(),
  date_from: z.string().nullable().optional(),
  date_to: z.string().nullable().optional(),
  dry_run: z.boolean().default(false),
  force: z.boolean().default(false),
  stats_summary: MemoryCurationRunStatsSchema.default(EMPTY_MEMORY_CURATION_RUN_STATS),
  diagnostics: z.array(MemoryCurationRunDiagnosticSchema).default([]),
  runtime_id: z.string().optional(),
  runtime_name: z.string().optional(),
  runtime_device_info: z.string().optional(),
  runtime_last_seen_at: z.string().nullable().optional(),
  attempt: z.number().optional(),
  claimed_at: z.string().nullable().optional(),
  claimed_age_seconds: z.number().optional(),
  curator_agent_id: z.string().optional(),
  curator_agent_name: z.string().optional(),
  curator_model: z.string().optional(),
  curator_mode: z.string().optional(),
  confidence_threshold: z.number().optional(),
  target_agent_ids: z.array(z.string()).default([]),
  target_agents: z.array(MemoryCurationTargetAgentSchema).default([]),
  timeline: z.array(MemoryCurationRunTimelineItemSchema).default([]),
  agent_results: z.array(MemoryCurationAgentRunSchema).default([]),
  child_runs: z.array(MemoryCurationChildRunSchema).default([]),
  artifacts: z.array(MemoryCurationRunArtifactSchema).default([]),
}).loose();

export const EMPTY_MEMORY_CURATION_RUN_DETAIL: MemoryCurationRunDetail = {
  id: "",
  workspace_id: "",
  stage: "",
  trigger_kind: "",
  status: "",
  stats: EMPTY_MEMORY_CURATION_RUN_STATS,
  stats_summary: EMPTY_MEMORY_CURATION_RUN_STATS,
  created_at: "",
  dry_run: false,
  force: false,
  diagnostics: [],
  target_agent_ids: [],
  target_agents: [],
  timeline: [],
  agent_results: [],
  child_runs: [],
  artifacts: [],
};

export const WorkspaceMemoryCurationStatusSchema = z.object({
  workspace_id: z.string().default(""),
  pending_runs: z.number().default(0),
  failed_runs_24h: z.number().default(0),
  stages: z.array(MemoryCurationStageStatusSchema).default([]),
  local_proposals: z.number().default(0),
  pending_candidates: z.number().default(0),
  pending_skills: z.number().default(0),
  promoted_candidates: z.number().default(0),
  team_knowledge_items: z.number().default(0),
}).loose();

export const EMPTY_WORKSPACE_MEMORY_CURATION_STATUS: WorkspaceMemoryCurationStatus = {
  workspace_id: "",
  pending_runs: 0,
  failed_runs_24h: 0,
  stages: [],
  local_proposals: 0,
  pending_candidates: 0,
  pending_skills: 0,
  promoted_candidates: 0,
  team_knowledge_items: 0,
};

export const MemoryCuratorProfileSchema = z.object({
  id: z.string().default(""),
  workspace_id: z.string().default(""),
  user_id: z.string().default(""),
  enabled: z.boolean().default(false),
  self_review_enabled: z.boolean().default(false),
  team_curation_enabled: z.boolean().default(false),
  mode: z.enum(["observe", "review", "auto_safe", "auto"]).catch("review"),
  runtime_id: z.string().default(""),
  curator_agent_id: z.string().default(""),
  model_override: z.string().default(""),
  target_scope: z.enum(["owned_all", "selected"]).catch("owned_all"),
  target_agent_ids: z.array(z.string()).default([]),
  timezone: z.string().default("Asia/Shanghai"),
  schedule_hour: z.number().int().min(0).max(23).default(1),
  catch_up_enabled: z.boolean().default(true),
  confidence_threshold: z.number().min(0).max(1).default(0.8),
  config_version: z.number().default(0),
  created_at: z.string().default(""),
  updated_at: z.string().default(""),
}).loose();

export const EMPTY_MEMORY_CURATOR_PROFILE = {
  id: "",
  workspace_id: "",
  user_id: "",
  enabled: false,
  self_review_enabled: false,
  team_curation_enabled: false,
  mode: "review" as const,
  runtime_id: "",
  curator_agent_id: "",
  model_override: "",
  target_scope: "owned_all" as const,
  target_agent_ids: [],
  timezone: "Asia/Shanghai",
  schedule_hour: 1,
  catch_up_enabled: true,
  confidence_threshold: 0.8,
  config_version: 0,
  created_at: "",
  updated_at: "",
};

export const StartMemoryCurationRunResponseSchema = z.object({
  id: z.string(),
  status: z.string().default("queued"),
}).loose();

export const MemoryCurationBackfillResponseSchema = z.object({
  since: z.string().default(""),
  until: z.string().default(""),
  dry_run: z.boolean().default(false),
  queued: z.array(z.object({
    date: z.string().default(""),
    stage: z.string().default(""),
    target_agent_ids: z.array(z.string()).default([]),
    run_id: z.string().optional(),
    status: z.string().optional(),
  }).loose()).default([]),
  skipped: z.array(z.object({
    date: z.string().default(""),
    reason: z.string().default(""),
  }).loose()).default([]),
  queued_days: z.number().default(0),
  skip_days: z.number().default(0),
}).loose();

export const EMPTY_MEMORY_CURATION_BACKFILL_RESPONSE = {
  since: "",
  until: "",
  dry_run: false,
  queued: [],
  skipped: [],
  queued_days: 0,
  skip_days: 0,
};

export const MemoryCurationDailySummaryDaySchema = z.object({
  date: z.string().default(""),
  memory_candidates: z.number().default(0),
  skill_candidates: z.number().default(0),
  team_knowledge_items: z.number().default(0),
  team_skills: z.number().default(0),
}).loose();

export const MemoryCurationDailySummaryResponseSchema = z.object({
  timezone: z.string().default("Asia/Shanghai"),
  since: z.string().default(""),
  until: z.string().default(""),
  days: z.array(MemoryCurationDailySummaryDaySchema).default([]),
}).loose();

export const EMPTY_MEMORY_CURATION_DAILY_SUMMARY = {
  timezone: "Asia/Shanghai",
  since: "",
  until: "",
  days: [],
};

export const MemoryCurationCandidateItemSchema = z.object({
  id: z.string().default(""),
  source_agent_id: z.string().optional(),
  source_agent_name: z.string().optional(),
  run_id: z.string().optional(),
  candidate_type: z.string().default(""),
  scope: z.string().default(""),
  title: z.string().default(""),
  snippet: z.string().default(""),
  content: z.string().optional(),
  confidence: z.number().default(0),
  status: z.string().default(""),
  created_at: z.string().default(""),
}).loose();

export const MemoryCurationCandidateListResponseSchema = z.object({
  items: z.array(MemoryCurationCandidateItemSchema).default([]),
  total: z.number().default(0),
}).loose();

export const EMPTY_MEMORY_CURATION_CANDIDATE_LIST = {
  items: [],
  total: 0,
};

export const TeamKnowledgeListItemSchema = z.object({
  id: z.string().default(""),
  kind: z.string().default(""),
  title: z.string().default(""),
  snippet: z.string().default(""),
  content: z.string().optional(),
  status: z.string().default(""),
  created_at: z.string().default(""),
}).loose();

export const TeamKnowledgeListResponseSchema = z.object({
  items: z.array(TeamKnowledgeListItemSchema).default([]),
  total: z.number().default(0),
}).loose();

export const EMPTY_TEAM_KNOWLEDGE_LIST = {
  items: [],
  total: 0,
};

export const KnowledgeEdgeSchema = z.object({
  id: z.string().default(""),
  edge_type: z.string().default(""),
  from_kind: z.string().default(""),
  from_id: z.string().default(""),
  to_kind: z.string().default(""),
  to_id: z.string().default(""),
  created_by_type: z.string().default(""),
  created_by_id: z.string().optional(),
  created_at: z.string().default(""),
}).loose();

export const KnowledgeNeighborsResponseSchema = z.object({
  page_id: z.string().default(""),
  edges: z.array(KnowledgeEdgeSchema).default([]),
  hops: z.number().default(1),
}).loose();

export const PromoteKnowledgeResponseSchema = z.object({
  id: z.string().default(""),
  kind: z.string().default(""),
  title: z.string().default(""),
  content: z.string().default(""),
  status: z.string().default(""),
  metadata: z.unknown().optional(),
  edges: z.array(KnowledgeEdgeSchema).default([]),
  created_at: z.string().default(""),
}).loose();

export const EMPTY_KNOWLEDGE_NEIGHBORS = {
  page_id: "",
  edges: [],
  hops: 1,
};


function stripLegacyMessageAvatar<T extends Record<string, unknown>>(
  value: T,
): T {
  const { author_avatar_url: _legacyAvatar, ...message } = value;
  return message as T;
}

function stripLegacyQuoteAvatar<T extends Record<string, unknown>>(
  value: T,
): T {
  const { authorAvatarUrl: _legacyAvatar, ...snapshot } = value;
  return snapshot as T;
}

const ChannelMessageSearchResultSchema = z.object({
  message_id: z.string().default(""),
  channel_id: z.string().default(""),
  thread_root_message_id: z.string().nullable().optional(),
  in_thread: z.boolean().optional(),
  type: z.string().default(""),
  author_id: z.string().nullable().default(null),
  author_name: z.string().default(""),
  content: z.string().default(""),
  parts: z.array(z.unknown()).optional(),
  created_at: z.string().default(""),
}).loose().transform((value) => stripLegacyMessageAvatar(value));

const ChannelReactionSchema = z.object({
  id: z.string().default(""),
  channel_id: z.string().default(""),
  message_id: z.string().default(""),
  actor_type: z.string().default(""),
  actor_id: z.string().default(""),
  emoji: z.string().default(""),
  created_at: z.string().default(""),
}).loose();

const ChannelMessageReplySchema = z.object({
  id: z.string().default(""),
  type: z.string().default(""),
  author_id: z.string().nullable().default(null),
  author_name: z.string().default(""),
  content: z.string().default(""),
  parts: z.array(z.unknown()).optional(),
  created_at: z.string().default(""),
}).loose().transform((value) => stripLegacyMessageAvatar(value));

const ChannelMessageQuoteSnapshotSchema = z.object({
  type: z.string().default(""),
  authorId: z.string().nullable().optional(),
  authorName: z.string().default(""),
  content: z.string().default(""),
  parts: z.array(z.unknown()).optional(),
  createdAt: z.string().default(""),
}).loose().transform((value) => stripLegacyQuoteAvatar(value));

const ChannelMessageQuoteSchema = z.object({
  messageId: z.string().default(""),
  snapshot: ChannelMessageQuoteSnapshotSchema.nullable().optional(),
  status: z.string().default("inaccessible"),
}).loose();

const ChannelThreadParticipantSchema = z.object({
  key: z.string().default(""),
  member_type: z.string().default(""),
  member_id: z.string().default(""),
  name: z.string().default(""),
  display_name: z.string().default(""),
  followed: z.boolean().default(false),
}).loose();

const ChannelMessageSchema = z.object({
  id: z.string().default(""),
  channel_id: z.string().default(""),
  workspace_id: z.string().default(""),
  type: z.string().default(""),
  author_id: z.string().nullable().default(null),
  author_name: z.string().default(""),
  content: z.string().default(""),
  parts: z.array(z.unknown()).optional(),
  source: z.string().default(""),
  external_message_id: z.string().nullable().default(null),
  client_message_id: z.string().nullable().optional(),
  reply_to_message_id: z.string().nullable().optional(),
  reply_to: ChannelMessageReplySchema.nullable().optional(),
  quote_message_id: z.string().nullable().optional(),
  quote: ChannelMessageQuoteSchema.nullable().optional(),
  thread_root_message_id: z.string().nullable().optional(),
  thread_root: ChannelMessageReplySchema.nullable().optional(),
  thread_reply_count: z.number().default(0).optional(),
  thread_last_reply_at: z.string().nullable().optional(),
  thread_unread_count: z.number().default(0).optional(),
  thread_followed: z.boolean().default(false).optional(),
  thread_participants: z.array(ChannelThreadParticipantSchema).nullish().transform((value) => value ?? []).optional(),
  thread_id: z.string().nullable().optional(),
  trigger_depth: z.number().default(0).optional(),
  seq: z.number().default(0).optional(),
  reactions: z.array(ChannelReactionSchema).default([]).optional(),
  attachments: z.array(AttachmentSchema).default([]).optional(),
  created_at: z.string().default(""),
  edited_at: z.string().nullable().optional(),
  deleted_at: z.string().nullable().optional(),
}).loose().transform((value) => stripLegacyMessageAvatar(value));

const ChannelMessagesCursorSchema = z.object({
  seq: z.number().default(0),
  created_at: z.string().default(""),
  id: z.string().default(""),
}).loose();

export const ChannelMessagesPageSchema = z.object({
  messages: z.array(ChannelMessageSchema).default([]),
  limit: z.number().default(50),
  has_more: z.boolean().default(false),
  next_cursor: ChannelMessagesCursorSchema.nullable().optional().default(null),
  // around_seq mode only (task #340). Left undefined (not defaulted) so a
  // caller can tell "absent" from a real value; the server only sends these
  // for around_seq requests. NOTE: the server currently omits anchor_index
  // when it is 0 (omitempty), so an around-mode caller must treat a missing
  // value as 0, not as "not around mode".
  anchor_index: z.number().optional(),
  has_more_after: z.boolean().optional(),
  after_cursor: ChannelMessagesCursorSchema.nullable().optional(),
  unread_total: z.number().optional(),
}).loose();

export const EMPTY_CHANNEL_MESSAGES_PAGE: ChannelMessagesPage = {
  messages: [],
  limit: 50,
  has_more: false,
  next_cursor: null,
};

const ChannelThreadMessagesCursorSchema = z.object({
  before_seq: z.number().optional(),
  before: z.string().default(""),
  before_id: z.string().default(""),
}).loose();

export const ChannelThreadMessagesPageSchema = z.object({
  messages: z.array(ChannelMessageSchema).default([]),
  next_cursor: ChannelThreadMessagesCursorSchema.nullable().optional().default(null),
}).loose();

export const EMPTY_CHANNEL_THREAD_MESSAGES_PAGE: ChannelThreadMessagesPage = {
  messages: [],
  next_cursor: null,
};

export const ChannelMessageSearchResponseSchema = z.object({
  query: z.string().default(""),
  total: z.number().default(0),
  results: z.array(ChannelMessageSearchResultSchema).default([]),
}).loose();

export const EMPTY_CHANNEL_MESSAGE_SEARCH_RESPONSE: ChannelMessageSearchResponse = {
  query: "",
  total: 0,
  results: [],
};

// ---- Workspace-level global search (LRM-605 BE ↔ LRM-606 FE) ----
// Contract for the collaboration-content search surface (scope tabs
// 全部/Messages/Channels/DMs/People). Distinct from the single-channel
// ChannelMessageSearch* above.
const WorkspaceSearchHighlightRangeSchema = z.object({
  start: z.number(),
  end: z.number(),
}).loose();

const WorkspaceSearchMessageSchema = z.object({
  result_type: z.string().default("message"),
  message_id: z.string().default(""),
  channel_id: z.string().default(""),
  channel_name: z.string().default(""),
  channel_kind: z.string().default("group"),
  thread_root_message_id: z.string().nullable().optional(),
  hit_count: z.number().default(1),
  author_id: z.string().nullable().optional(),
  author_type: z.string().nullable().optional(),
  author_name: z.string().default(""),
  content: z.string().default(""),
  snippet: z.string().default(""),
  highlight_ranges: z.array(WorkspaceSearchHighlightRangeSchema).default([]),
  created_at: z.string().default(""),
}).loose();

const WorkspaceSearchChannelSchema = z.object({
  channel_id: z.string().default(""),
  name: z.string().default(""),
  kind: z.string().default("group"),
  description: z.string().nullable().optional(),
}).loose();

const WorkspaceSearchDMSchema = z.object({
  channel_id: z.string().default(""),
  name: z.string().default(""),
  kind: z.string().default("dm"),
  description: z.string().nullable().optional(),
}).loose();

const WorkspaceSearchPersonSchema = z.object({
  actor_type: z.string().default("user"),
  actor_id: z.string().default(""),
  name: z.string().default(""),
  display_name: z.string().default(""),
  avatar_url: z.string().nullable().optional(),
}).loose();

const WorkspaceSearchCountsSchema = z.object({
  messages: z.number().default(0),
  channels: z.number().default(0),
  dms: z.number().default(0),
  people: z.number().default(0),
}).loose();

export const WorkspaceSearchResponseSchema = z.object({
  query: z.string().default(""),
  scope: z.string().default("all"),
  counts: WorkspaceSearchCountsSchema.default({ messages: 0, channels: 0, dms: 0, people: 0 }),
  messages: z.array(WorkspaceSearchMessageSchema).default([]),
  channels: z.array(WorkspaceSearchChannelSchema).default([]),
  dms: z.array(WorkspaceSearchDMSchema).default([]),
  people: z.array(WorkspaceSearchPersonSchema).default([]),
}).loose();

export const EMPTY_WORKSPACE_SEARCH_RESPONSE: WorkspaceSearchResponse = {
  query: "",
  scope: "all",
  counts: { messages: 0, channels: 0, dms: 0, people: 0 },
  messages: [],
  channels: [],
  dms: [],
  people: [],
};

const StickerAssetSchema = z.object({
  pack_id: z.string().default(""),
  sticker_id: z.string().default(""),
  name: z.string().default(""),
  name_en: z.string().default(""),
  emotion: z.string().default(""),
  asset_url: z.string().default(""),
  mime_type: z.string().default(""),
  alt: z.string().default(""),
  tags: z.array(z.string()).default([]),
  animated: z.boolean().default(false),
}).loose();

const StickerPackSchema = z.object({
  id: z.string().default(""),
  name: z.string().default(""),
  source: z.string().default(""),
  license: z.string().default(""),
  stickers: z.array(StickerAssetSchema).default([]),
}).loose();

export const StickerCatalogResponseSchema = z.object({
  stickers: z.array(z.unknown()).default([]),
  license: z.string().default(""),
  source: z.string().default(""),
  packs: z.array(StickerPackSchema).default([]),
}).loose();

export const EMPTY_STICKER_CATALOG_RESPONSE: StickerCatalogResponse = {
  stickers: [],
  license: "",
  source: "",
  packs: [],
};

// Metadata is primitive-only by API/DB contract. Stay lenient on shape:
// unknown keys land as `unknown` to a caller, but the field itself defaults
// to {} so consumers never need to nil-guard `issue.metadata`.
const IssueMetadataSchema = z.record(z.string(), z.union([z.string(), z.number(), z.boolean()])).default({});

export const IssueSchema = z.object({
  id: z.string(),
  workspace_id: z.string(),
  number: z.number(),
  identifier: z.string(),
  title: z.string(),
  description: z.string().nullable(),
  status: z.string(),
  priority: z.string(),
  assignee_type: z.string().nullable(),
  assignee_id: z.string().nullable(),
  creator_type: z.string(),
  creator_id: z.string(),
  parent_issue_id: z.string().nullable(),
  project_id: z.string().nullable(),
  position: z.number(),
  start_date: z.string().nullable(),
  due_date: z.string().nullable(),
  metadata: IssueMetadataSchema,
  reactions: z.array(z.unknown()).optional(),
  labels: z.array(z.unknown()).optional(),
  created_at: z.string(),
  updated_at: z.string(),
}).loose();

export const CreateNotePageIssueResponseSchema = z.object({
  issue: IssueSchema,
  ref: NotePageIssueRefSchema,
}).loose();

export const ListIssuesResponseSchema = z.object({
  issues: z.array(IssueSchema).default([]),
  total: z.number().default(0),
}).loose();

export const EMPTY_LIST_ISSUES_RESPONSE: ListIssuesResponse = {
  issues: [],
  total: 0,
};

const IssueAssigneeGroupSchema = z.object({
  id: z.string(),
  assignee_type: z.string().nullable(),
  assignee_id: z.string().nullable(),
  issues: z.array(IssueSchema).default([]),
  total: z.number().default(0),
}).loose();

export const GroupedIssuesResponseSchema = z.object({
  groups: z.array(IssueAssigneeGroupSchema).default([]),
}).loose();

export const EMPTY_GROUPED_ISSUES_RESPONSE: GroupedIssuesResponse = {
  groups: [],
};

const IssueProjectGroupSchema = z.object({
  id: z.string(),
  project_id: z.string().nullable(),
  project_title: z.string().nullable(),
  issues: z.array(IssueSchema).default([]),
  total: z.number().default(0),
}).loose();

export const ProjectGroupedIssuesResponseSchema = z.object({
  groups: z.array(IssueProjectGroupSchema).default([]),
}).loose();

export const EMPTY_PROJECT_GROUPED_ISSUES_RESPONSE: ProjectGroupedIssuesResponse = {
  groups: [],
};

const SubscriberSchema = z.object({
  issue_id: z.string(),
  user_type: z.string(),
  user_id: z.string(),
  reason: z.string(),
  created_at: z.string(),
}).loose();

export const SubscribersListSchema = z.array(SubscriberSchema);

export const ChildIssuesResponseSchema = z.object({
  issues: z.array(IssueSchema).default([]),
}).loose();

export const CloudRuntimeNodeSchema = z.object({
  id: z.string(),
  owner_id: z.string(),
  instance_id: z.string(),
  region: z.string(),
  instance_type: z.string(),
  image_id: z.string(),
  subnet_id: z.string(),
  name: z.string(),
  status: z.string(),
  tags: z.record(z.string(), z.string()).default({}),
  metadata: z.record(z.string(), z.unknown()).default({}),
  created_at: z.string(),
  updated_at: z.string(),
}).loose();

export const CloudRuntimeNodeListSchema = z.array(CloudRuntimeNodeSchema);

export const EMPTY_CLOUD_RUNTIME_NODE_LIST: CloudRuntimeNode[] = [];

export const EMPTY_CLOUD_RUNTIME_NODE: CloudRuntimeNode = {
  id: "",
  owner_id: "",
  instance_id: "",
  region: "",
  instance_type: "",
  image_id: "",
  subnet_id: "",
  name: "",
  status: "",
  tags: {},
  metadata: {},
  created_at: "",
  updated_at: "",
};

// ---------------------------------------------------------------------------
// Workspace dashboard schemas
//
// The dashboard hits three independent rollup endpoints. Each returns a flat
// array, and every field is consumed by chart / KPI math — a missing number
// silently degrades to NaN downstream, so we coerce missing numbers to 0.
// String fields default to "" (no enum narrowing) to survive future model /
// agent ID drift, and so a single null from tz-aware SQL bucketing fails
// only that row instead of dropping the whole array to the `[]` fallback.
// ---------------------------------------------------------------------------

const DashboardUsageDailySchema = z.object({
  date: z.string().default(""),
  model: z.string().default(""),
  input_tokens: z.number().default(0),
  output_tokens: z.number().default(0),
  cache_read_tokens: z.number().default(0),
  cache_write_tokens: z.number().default(0),
  task_count: z.number().default(0),
}).loose();

export const DashboardUsageDailyListSchema = z.array(DashboardUsageDailySchema);

const DashboardUsageByAgentSchema = z.object({
  agent_id: z.string().default(""),
  model: z.string().default(""),
  input_tokens: z.number().default(0),
  output_tokens: z.number().default(0),
  cache_read_tokens: z.number().default(0),
  cache_write_tokens: z.number().default(0),
  task_count: z.number().default(0),
}).loose();

export const DashboardUsageByAgentListSchema = z.array(DashboardUsageByAgentSchema);

const DashboardAgentRunTimeSchema = z.object({
  agent_id: z.string().default(""),
  total_seconds: z.number().default(0),
  task_count: z.number().default(0),
  failed_count: z.number().default(0),
}).loose();

export const DashboardAgentRunTimeListSchema = z.array(DashboardAgentRunTimeSchema);

const DashboardRunTimeDailySchema = z.object({
  date: z.string().default(""),
  total_seconds: z.number().default(0),
  task_count: z.number().default(0),
  failed_count: z.number().default(0),
}).loose();

export const DashboardRunTimeDailyListSchema = z.array(DashboardRunTimeDailySchema);

// ---------------------------------------------------------------------------
// Runtime usage schemas — the runtime-detail page's four usage endpoints
// (`/api/runtimes/:id/usage*`). Same leniency rules as the dashboard
// schemas above: numbers default to 0, strings to "", `.loose()` passes
// unknown fields.
// ---------------------------------------------------------------------------

const RuntimeUsageSchema = z.object({
  runtime_id: z.string().default(""),
  date: z.string().default(""),
  provider: z.string().default(""),
  model: z.string().default(""),
  input_tokens: z.number().default(0),
  output_tokens: z.number().default(0),
  cache_read_tokens: z.number().default(0),
  cache_write_tokens: z.number().default(0),
}).loose();

export const RuntimeUsageListSchema = z.array(RuntimeUsageSchema);

export const WorkspaceSchema = z.object({
  id: z.string(),
  name: z.string(),
  slug: z.string(),
  description: z.string().nullable().default(null),
  context: z.string().nullable().default(null),
  settings: z.record(z.string(), z.unknown()).default({}),
  issue_prefix: z.string().default(""),
  avatar_url: z.string().nullable().default(null),
  onboarding_agent_id: z.string().nullable().optional(),
  created_at: z.string().default(""),
  updated_at: z.string().default(""),
  last_active_at: z.string().nullable().optional(),
}).loose();

export const WorkspaceListSchema = z.array(WorkspaceSchema);

export const EMPTY_WORKSPACE: Workspace = {
  id: "",
  name: "",
  slug: "",
  description: null,
  context: null,
  settings: {},
  issue_prefix: "",
  avatar_url: null,
  onboarding_agent_id: null,
  created_at: "",
  updated_at: "",
};

const RuntimeAgentWorkspaceSchema = z.object({
  dir_name: z.string(),
  rel_path: z.string(),
  agent_id: z.string().nullable().optional(),
  agent_name: z.string().nullable().optional(),
  orphan: z.boolean(),
  size_bytes: z.number().optional(),
}).loose();

export const RuntimeAgentWorkspacesResponseSchema = z.object({
  runtime_id: z.string(),
  status: z.string(),
  items: z.array(RuntimeAgentWorkspaceSchema),
  truncated: z.boolean().optional(),
}).loose();

export const EMPTY_RUNTIME_AGENT_WORKSPACES_RESPONSE: RuntimeAgentWorkspacesResponse = {
  runtime_id: "",
  status: "error",
  items: [],
};

const AgentHealthSummarySchema = z.object({
  agent_id: z.string().default(""),
  runtime_id: z.string().nullable().default(null),
  state: z.string().default("offline"),
  reason_code: z.string().default(""),
  state_since: z.string().nullable().default(null),
  last_seen_at: z.string().nullable().default(null),
  last_event_at: z.string().nullable().default(null),
}).loose();

const AgentHealthEventSchema = z.object({
  id: z.string().default(""),
  agent_id: z.string().default(""),
  runtime_id: z.string().nullable().default(null),
  type: z.string().default("server_ping_received"),
  state_after: z.string().default("offline"),
  reason_code: z.string().default(""),
  message: z.string().default(""),
  occurred_at: z.string().default(""),
  details: z.record(z.string(), z.unknown()).optional(),
  synthetic: z.boolean().optional(),
}).loose();

export const AgentHealthResponseSchema = z.object({
  health_summary: AgentHealthSummarySchema.default({
    agent_id: "",
    runtime_id: null,
    state: "offline",
    reason_code: "schema_fallback",
    state_since: null,
    last_seen_at: null,
    last_event_at: null,
  }),
  health_events: z.array(AgentHealthEventSchema).default([]),
}).loose();

export const EMPTY_AGENT_HEALTH_RESPONSE: AgentHealthResponse = {
  health_summary: {
    agent_id: "",
    runtime_id: null,
    state: "offline",
    reason_code: "empty",
    state_since: null,
    last_seen_at: null,
    last_event_at: null,
  },
  health_events: [],
};

const RunnerActivitySummarySchema = z.object({
  label: z.string().default(""),
  tone: z.string().default("muted"),
  visibility: z.string().default("hidden"),
}).loose();

const RunnerActivityTimelineRowSchema = z.object({
  id: z.string().default(""),
  occurred_at: z.string().default(""),
  title: z.string().default("Working..."),
  subtext: z.string().optional(),
  tone: z.string().default("muted"),
  body_kind: z.string().default("generic"),
  body: z.string().optional(),
}).loose();

export const RunnerActivityResponseSchema = z.object({
  summary: RunnerActivitySummarySchema.nullable().default(null),
  timeline: z.array(RunnerActivityTimelineRowSchema).default([]),
}).loose();

export const EMPTY_RUNNER_ACTIVITY_RESPONSE = { summary: null, timeline: [] };

const RunnerActivitySummaryItemSchema = z.object({
  agent_id: z.string().min(1),
  summary: RunnerActivitySummarySchema,
}).loose();

export const RunnerActivitySummariesResponseSchema = z.object({
  items: z.array(RunnerActivitySummaryItemSchema).default([]),
}).loose();

export const EMPTY_RUNNER_ACTIVITY_SUMMARIES_RESPONSE = { items: [] };

const AgentPresenceItemSchema = z.object({
  agent_id: z.string().min(1),
  presence: z.enum(["online", "offline"]),
}).loose();

export const AgentPresenceResponseSchema: z.ZodType<AgentPresenceResponse> = z.object({
  items: z.array(AgentPresenceItemSchema),
}).loose().superRefine((value, ctx) => {
  const seen = new Set<string>();
  for (const [index, item] of value.items.entries()) {
    if (seen.has(item.agent_id)) {
      ctx.addIssue({
        code: "custom",
        path: ["items", index, "agent_id"],
        message: "duplicate Agent Presence row",
      });
    }
    seen.add(item.agent_id);
  }
});

export const EMPTY_AGENT_PRESENCE_RESPONSE: AgentPresenceResponse = { items: [] };

const AgentFileNodeSchema = z.object({
  path: z.string(),
  is_dir: z.boolean().default(false),
  size: z.number().optional(),
}).loose();

export const AgentFilesResponseSchema = z.object({
  agent_id: z.string().default(""),
  status: z.string().default("error"),
  nodes: z.array(AgentFileNodeSchema),
  truncated: z.boolean().default(false),
}).loose();

export const EMPTY_AGENT_FILES_RESPONSE: AgentFilesResponse = {
  agent_id: "",
  status: "error",
  nodes: [],
  truncated: false,
};

export const AgentFileContentResponseSchema = z.object({
  content: z.string().default(""),
  encoding: z.string().default(""),
  mime_type: z.string().default(""),
  content_hash: z.string().default(""),
  truncated: z.boolean().default(false),
  too_large: z.boolean().default(false),
  binary: z.boolean().default(false),
}).loose();

export const EMPTY_AGENT_FILE_CONTENT_RESPONSE: AgentFileContentResponse = {
  content: "",
  encoding: "",
  mime_type: "",
  content_hash: "",
  truncated: false,
  too_large: false,
  binary: false,
};

export const UpdateAgentFileContentResponseSchema = z.object({
  content_hash: z.string().default(""),
  conflict: z.boolean().default(false),
}).loose();

export const EMPTY_UPDATE_AGENT_FILE_CONTENT_RESPONSE: UpdateAgentFileContentResponse = {
  content_hash: "",
  conflict: false,
};

const RuntimeHourlyActivitySchema = z.object({
  hour: z.number().default(0),
  count: z.number().default(0),
}).loose();

export const RuntimeHourlyActivityListSchema = z.array(RuntimeHourlyActivitySchema);

const RuntimeUsageByAgentSchema = z.object({
  agent_id: z.string().default(""),
  model: z.string().default(""),
  input_tokens: z.number().default(0),
  output_tokens: z.number().default(0),
  cache_read_tokens: z.number().default(0),
  cache_write_tokens: z.number().default(0),
  task_count: z.number().default(0),
}).loose();

export const RuntimeUsageByAgentListSchema = z.array(RuntimeUsageByAgentSchema);

const RuntimeUsageByHourSchema = z.object({
  hour: z.number().default(0),
  model: z.string().default(""),
  input_tokens: z.number().default(0),
  output_tokens: z.number().default(0),
  cache_read_tokens: z.number().default(0),
  cache_write_tokens: z.number().default(0),
  task_count: z.number().default(0),
}).loose();

export const RuntimeUsageByHourListSchema = z.array(RuntimeUsageByHourSchema);

// ---------------------------------------------------------------------------
// Task cancellation (`POST /api/tasks/:id/cancel`)
//
// This response is consumed directly by chat recovery. The embedded task
// object stays loose so daemon/runtime fields can drift, but the optional
// `cancelled_chat_message` payload must be well-formed before the UI deletes
// a message from cache or restores text into the input.
// ---------------------------------------------------------------------------

const AgentTaskResponseSchema = z.object({
  id: z.string(),
  agent_id: z.string().default(""),
  runtime_id: z.string().default(""),
  issue_id: z.string().default(""),
  status: z.string().default("cancelled"),
  priority: z.number().default(0),
  dispatched_at: z.string().nullable().default(null),
  started_at: z.string().nullable().default(null),
  completed_at: z.string().nullable().default(null),
  result: z.unknown().default(null),
  error: z.string().nullable().default(null),
  failure_reason: z.string().optional(),
  created_at: z.string().default(""),
  chat_session_id: z.string().optional(),
  autopilot_run_id: z.string().optional(),
  parent_task_id: z.string().optional(),
  attempt: z.number().optional(),
  trigger_comment_id: z.string().optional(),
  trigger_summary: z.string().optional(),
  kind: z.string().optional(),
  work_dir: z.string().optional(),
  relative_work_dir: z.string().optional(),
}).loose();

const CancelledChatMessageSchema = z.object({
  chat_session_id: z.string(),
  message_id: z.string(),
  content: z.string(),
  restore_to_input: z.boolean().default(false),
}).loose();

export const CancelTaskResponseSchema = AgentTaskResponseSchema.extend({
  cancelled_chat_message: CancelledChatMessageSchema.nullish()
    .transform((value) => value ?? undefined),
}).loose();

export const EMPTY_CANCEL_TASK_RESPONSE: CancelTaskResponse = {
  id: "",
  agent_id: "",
  runtime_id: "",
  issue_id: "",
  status: "cancelled",
  priority: 0,
  dispatched_at: null,
  started_at: null,
  completed_at: null,
  result: null,
  error: null,
  created_at: "",
};

// ---------------------------------------------------------------------------
// Agent template catalog — `/api/agent-templates*` and the
// create-from-template response. The desktop app's create-agent picker
// reaches these endpoints, and a future server change to the template shape
// would white-screen older installed builds (#2192 pattern) without these
// parsers. Lenient by the same rules as IssueSchema above: arrays default to
// `[]`, optional fields stay optional, `.loose()` lets unknown fields pass
// through unchanged.
// ---------------------------------------------------------------------------

const AgentTemplateSkillRefSchema = z.object({
  source_url: z.string(),
  cached_name: z.string().default(""),
  cached_description: z.string().default(""),
}).loose();

const AgentTemplateSummarySchemaBase = z.object({
  slug: z.string(),
  name: z.string(),
  description: z.string().default(""),
  category: z.string().optional(),
  icon: z.string().optional(),
  accent: z.string().optional(),
  // skills MUST default to [] — picker code reads `template.skills.length`
  // and `.map(...)`, both of which crash on `undefined`. The most common
  // future drift (field renamed / wrapped) lands here.
  skills: z.array(AgentTemplateSkillRefSchema).default([]),
}).loose();

export const AgentTemplateSummarySchema = AgentTemplateSummarySchemaBase;

// List endpoint historically returns a bare array. Server could legitimately
// migrate to `{templates: [...]}` later — we accept either shape so an old
// desktop survives the upgrade.
export const AgentTemplateSummaryListSchema = z.union([
  z.array(AgentTemplateSummarySchemaBase),
  z.object({ templates: z.array(AgentTemplateSummarySchemaBase).default([]) })
    .loose()
    .transform((v) => v.templates),
]);

export const EMPTY_AGENT_TEMPLATE_SUMMARY_LIST: AgentTemplateSummary[] = [];

export const AgentTemplateSchema = AgentTemplateSummarySchemaBase.extend({
  // Detail-only field. Default "" so a malformed detail still renders the
  // header + skill list; the user just sees an empty Instructions block.
  instructions: z.string().default(""),
}).loose();

// Used as the parse fallback for `GET /api/agent-templates/:slug`. Slug comes
// from the URL, so we round-trip the requested one back into the fallback
// at the call site (see `getAgentTemplate` in client.ts).
export const EMPTY_AGENT_TEMPLATE_DETAIL: AgentTemplate = {
  slug: "",
  name: "",
  description: "",
  skills: [],
  instructions: "",
};

// `agent` is a full Agent record — schematising every field would duplicate
// a 50-field interface and bit-rot fast. We keep it loose and require only
// `id`, the one field the create-from-template flow consumes (used to
// navigate to the new agent's detail page). Downstream code already
// optional-chains the rest.
const MinimalAgentSchema = z.object({
  id: z.string(),
}).loose();

export const CreateAgentFromTemplateResponseSchema = z.object({
  agent: MinimalAgentSchema,
  imported_skill_ids: z.array(z.string()).default([]),
  reused_skill_ids: z.array(z.string()).default([]),
}).loose();

export const EnsureWindyResponseSchema: z.ZodType<EnsureWindyResponse> = z.object({
  agent: z.custom<Agent>((value) => MinimalAgentSchema.safeParse(value).success),
  dm_id: z.string().optional(),
}).loose();

// Fallback when the success response fails to parse. The agent server-side
// has likely been created already, so we can't pretend nothing happened —
// the caller (`create-agent-dialog.tsx`) is responsible for noticing
// `agent.id === ""` and skipping navigation while keeping the list
// invalidation, so the user finds their new agent in the list.
export const EMPTY_CREATE_AGENT_FROM_TEMPLATE_RESPONSE: CreateAgentFromTemplateResponse = {
  agent: { id: "" } as Agent,
  imported_skill_ids: [],
  reused_skill_ids: [],
};

// ---------------------------------------------------------------------------
// Structured error body — POST /api/workspaces/:wsId/issues 409 conflict.
//
// When the server detects an active issue with the same title in the same
// workspace, it returns `{ code: "active_duplicate_issue", error, issue }`
// instead of letting the create through. The UI uses the embedded issue ref
// to offer "view existing" rather than dropping the user into a generic
// "create failed" toast.
//
// Strict guarantees:
//   - `code` is a literal so a future server rename (e.g. `duplicate_issue`)
//     fails the parse and falls back to a normal error toast — drift never
//     ships as a broken duplicate UI.
//   - `issue` is required; without an id/identifier/title the "view existing"
//     button has nothing to point at, so we'd rather fall back than guess.
//   - `issue.status` is intentionally OMITTED: the duplicate toast doesn't
//     render a StatusIcon (which has no fallback for unknown enum values),
//     so a future server-side rename of `status` must not knock this branch
//     out. `.loose()` lets the field pass through unchanged for any other
//     consumer.
// ---------------------------------------------------------------------------

export const DuplicateIssueErrorBodySchema = z.object({
  code: z.literal("active_duplicate_issue"),
  error: z.string().optional(),
  issue: z.object({
    id: z.string(),
    identifier: z.string(),
    title: z.string(),
  }).loose(),
}).loose();

export interface DuplicateIssueErrorBody {
  code: "active_duplicate_issue";
  error?: string;
  issue: {
    id: string;
    identifier: string;
    title: string;
  };
}

// Body returned with a 409 when creating a channel whose (workspace, name) pair
// already exists. `code` is the stable, machine-readable branch key the FE keys
// its i18n message off; `error` is a human string for logs/fallback only.
export const ChannelCreateErrorBodySchema = z.object({
  code: z.literal("channel_name_taken"),
  error: z.string().optional(),
}).loose();

export interface ChannelCreateErrorBody {
  code: "channel_name_taken";
  error?: string;
}


// ---------------------------------------------------------------------------
// User (`/api/me` GET + PATCH). The auth store and Settings → Account both
// trust this shape — a drift here would knock both surfaces out. Kept
// lenient by the same rules as IssueSchema: enums stay `z.string()`,
// nullable fields are unioned with `null`, unknown server fields pass
// through via `.loose()`. `profile_description` is the field added in
// MUL-2406; the server emits `""` when unset (NOT NULL DEFAULT ''), so
// the schema defaults to `""` too — keeps the type tight without
// breaking older backends that don't return the column yet.
// ---------------------------------------------------------------------------

export const UserSchema = z.object({
  id: z.string(),
  name: z.string().default(""),
  display_name: z.string().default(""),
  email: z.string().default(""),
  avatar_url: z.string().nullable().default(null),
  onboarded_at: z.string().nullable().default(null),
  onboarding_questionnaire: z.record(z.string(), z.unknown()).default({}),
  starter_content_state: z.string().nullable().default(null),
  language: z.string().nullable().default(null),
  profile_description: z.string().default(""),
  timezone: z.string().nullable().default(null),
  created_at: z.string().default(""),
  updated_at: z.string().default(""),
}).loose();

export const EMPTY_USER: User = {
  id: "",
  name: "",
  display_name: "",
  email: "",
  avatar_url: null,
  onboarded_at: null,
  onboarding_questionnaire: {},
  starter_content_state: null,
  language: null,
  profile_description: "",
  timezone: null,
  created_at: "",
  updated_at: "",
};

// ---------------------------------------------------------------------------
// Billing schemas (cloud-billing proxy surface)
//
// All billing JSON we receive comes from multica-cloud verbatim — we proxy
// the bytes without re-shaping. These schemas use `loose()` so a future
// non-breaking field addition on the cloud side doesn't crash us; required
// fields are still strictly enforced. EMPTY_* constants supply the
// fallback parseWithFallback uses when the upstream response is malformed
// or unparseable.

export const BillingBalanceSchema = z.object({
  owner_id: z.string(),
  balance_micro: z.number(),
  balance_credit: z.number(),
  updated_at: z.string(),
}).loose();

export const EMPTY_BILLING_BALANCE: BillingBalance = {
  owner_id: "",
  balance_micro: 0,
  balance_credit: 0,
  updated_at: "",
};

// `tx_type` and `source` are kept as plain strings here; the cloud doc
// enumerates the canonical values but the frontend display tolerates
// unknown ones gracefully. Strict enums would crash the page on a future
// addition (e.g. a new `topup` source kind).
export const BillingTransactionSchema = z.object({
  id: z.string(),
  owner_id: z.string(),
  idempotency_key: z.string().default(""),
  tx_type: z.string(),
  source: z.string(),
  amount_micro: z.number(),
  balance_after: z.number(),
  reference_id: z.string().default(""),
  description: z.string().default(""),
  metadata: z.record(z.string(), z.unknown()).default({}),
  created_at: z.string(),
}).loose();

export const BillingTransactionsPageSchema = z.object({
  items: z.array(BillingTransactionSchema).default([]),
  total: z.number().default(0),
  page: z.number().default(1),
  page_size: z.number().default(20),
}).loose();

export const EMPTY_BILLING_TRANSACTIONS_PAGE: BillingTransactionsPage = {
  items: [],
  total: 0,
  page: 1,
  page_size: 20,
};

export const BillingBatchSchema = z.object({
  id: z.string(),
  owner_id: z.string(),
  source_tx_id: z.string().default(""),
  source_type: z.string(),
  total_micro: z.number(),
  remaining_micro: z.number(),
  // Cloud either omits the key (never expires) or sends a string
  // timestamp. Null is also tolerated since some serializers emit
  // explicit nulls for absent timestamps.
  expires_at: z.string().nullable().optional(),
  created_at: z.string(),
  updated_at: z.string(),
}).loose();

export const BillingBatchesPageSchema = z.object({
  items: z.array(BillingBatchSchema).default([]),
  total: z.number().default(0),
  page: z.number().default(1),
  page_size: z.number().default(20),
}).loose();

export const EMPTY_BILLING_BATCHES_PAGE: BillingBatchesPage = {
  items: [],
  total: 0,
  page: 1,
  page_size: 20,
};

export const BillingTopupSchema = z.object({
  id: z.string(),
  owner_id: z.string(),
  amount_cents: z.number(),
  currency: z.string().default("usd"),
  credits: z.number(),
  bonus_credits: z.number().default(0),
  status: z.string(),
  tier_id: z.string().default(""),
  stripe_checkout_id: z.string().default(""),
  // Only set after status reaches `credited` — leave optional rather
  // than coerce to "" so a UI can branch on existence.
  purchase_batch_id: z.string().optional(),
  created_at: z.string(),
  updated_at: z.string(),
}).loose();

export const BillingTopupsPageSchema = z.object({
  items: z.array(BillingTopupSchema).default([]),
  total: z.number().default(0),
  page: z.number().default(1),
  page_size: z.number().default(20),
}).loose();

export const EMPTY_BILLING_TOPUPS_PAGE: BillingTopupsPage = {
  items: [],
  total: 0,
  page: 1,
  page_size: 20,
};

export const BillingPriceTierSchema = z.object({
  id: z.string(),
  // Cloud doc says display_name falls back to id; tolerate empty too.
  display_name: z.string().default(""),
  amount_cents: z.number(),
  credits: z.number(),
  bonus_credits: z.number().optional(),
  bonus_expires_in: z.string().optional(),
}).loose();

export const BillingPriceTierListSchema = z.array(BillingPriceTierSchema);

export const EMPTY_BILLING_PRICE_TIER_LIST: BillingPriceTier[] = [];

export const CreateBillingCheckoutSessionResponseSchema = z.object({
  order_id: z.string(),
  session_id: z.string(),
  url: z.string(),
}).loose();

export const EMPTY_CREATE_BILLING_CHECKOUT_SESSION_RESPONSE: CreateBillingCheckoutSessionResponse = {
  order_id: "",
  session_id: "",
  url: "",
};

export const BillingCheckoutSessionStatusSchema = z.object({
  order_id: z.string(),
  status: z.string(),
  amount_cents: z.number(),
  credits: z.number(),
  bonus_credits: z.number().default(0),
  currency: z.string().default("usd"),
  tier_id: z.string().default(""),
}).loose();

export const EMPTY_BILLING_CHECKOUT_SESSION_STATUS: BillingCheckoutSessionStatus = {
  order_id: "",
  status: "pending",
  amount_cents: 0,
  credits: 0,
  bonus_credits: 0,
  currency: "usd",
  tier_id: "",
};

export const CreateBillingPortalSessionResponseSchema = z.object({
  url: z.string(),
}).loose();

export const EMPTY_CREATE_BILLING_PORTAL_SESSION_RESPONSE: CreateBillingPortalSessionResponse = {
  url: "",
};

export const SandboxTemplateSchema = z.object({
  template_id: z.string(),
  status: z.string().default("unknown"),
  created_at: z.string().optional(),
  image_info: z.string().optional(),
  instance_type: z.string().optional(),
  last_error: z.string().optional(),
  version: z.string().optional(),
  job_id: z.string().optional(),
  is_default: z.boolean().optional(),
}).loose();

export const SandboxNodeTemplatesResponseSchema = z.object({
  templates: z.array(SandboxTemplateSchema).default([]),
  default_template_id: z.string().optional(),
  synced_at: z.string().optional(),
  node_online: z.boolean().optional(),
}).loose();

export const EMPTY_SANDBOX_NODE_TEMPLATES_RESPONSE: SandboxNodeTemplatesResponse = {
  templates: [],
  default_template_id: "",
  synced_at: "",
  node_online: false,
};

export const DockerImageSchema = z.object({
  image_ref: z.string(),
  repository: z.string().default(""),
  tag: z.string().default(""),
  id: z.string().default(""),
  digest: z.string().optional(),
  created_at: z.string().optional(),
  created_since: z.string().optional(),
  size: z.string().optional(),
}).loose();

export const SandboxNodeDockerImagesResponseSchema = z.object({
  images: z.array(DockerImageSchema).default([]),
  synced_at: z.string().optional(),
  node_online: z.boolean().optional(),
  error: z.string().optional(),
}).loose();

export const EMPTY_SANDBOX_NODE_DOCKER_IMAGES_RESPONSE: SandboxNodeDockerImagesResponse = {
  images: [],
  synced_at: "",
  node_online: false,
  error: "",
};

export const SandboxSnapshotSchema = z.object({
  id: z.string(),
  workspace_id: z.string().default(""),
  node_id: z.string().default(""),
  instance_id: z.string().optional(),
  creator_user_id: z.string().optional(),
  cube_snapshot_id: z.string().default(""),
  name: z.string().default(""),
  description: z.string().default(""),
  status: z.string().default("creating"),
  error: z.string().optional(),
  created_at: z.string().default(""),
  updated_at: z.string().default(""),
}).loose();

export const SandboxSnapshotListSchema = z.array(SandboxSnapshotSchema);

export const VoiceTranscriptResponseSchema = z.object({
  text: z.string(),
}).loose();

export const EMPTY_VOICE_TRANSCRIPT_RESPONSE = {
  text: "",
};

export const VoiceCallSchema = z.object({
  id: z.string().min(1),
  channel_id: z.string().min(1),
  agent_id: z.string().min(1),
  status: z.string().min(1),
  started_at: z.string().min(1),
  connected_at: z.string().nullable().optional().default(null),
  ended_at: z.string().nullable().optional().default(null),
  end_reason: z.string().optional().default(""),
  error_code: z.string().optional().default(""),
  input_audio_ms: z.number().int().nonnegative(),
  output_audio_ms: z.number().int().nonnegative(),
  updated_at: z.string().min(1),
}).loose();

export const VoiceCallMediaSchema = z.object({
  app_id: z.string().min(1),
  room_id: z.string().min(1),
  user_id: z.string().min(1),
  token: z.string().min(1),
  expires_at: z.string().min(1),
}).loose();

export const CreateVoiceCallResponseSchema = z.object({
  call: VoiceCallSchema,
  media: VoiceCallMediaSchema,
}).loose();

export const GetVoiceCallResponseSchema = z.object({
  call: VoiceCallSchema,
}).loose();

export const VoiceCallDuplexAudioHintSchema = z.object({
  input_format: z.string().min(1),
  input_sample_rate: z.number().int().positive(),
  output_format: z.string().min(1),
  output_sample_rate: z.number().int().positive(),
}).loose();

export const VoiceCallDuplexEventHintSchema = z.object({
  client: z.array(z.string()),
  server: z.array(z.string()),
}).loose();

export const StartVoiceCallDuplexResponseSchema = z.object({
  call: VoiceCallSchema,
  mode: z.string().min(1),
  ws_path: z.string().min(1),
  audio: VoiceCallDuplexAudioHintSchema,
  events: VoiceCallDuplexEventHintSchema,
}).loose();

export const EMPTY_VOICE_CALL: VoiceCall = {
  id: "",
  channel_id: "",
  agent_id: "",
  status: "unknown",
  started_at: "",
  connected_at: null,
  ended_at: null,
  end_reason: "",
  error_code: "",
  input_audio_ms: 0,
  output_audio_ms: 0,
  updated_at: "",
};

export const EMPTY_VOICE_CALL_MEDIA: VoiceCallMedia = {
  app_id: "",
  room_id: "",
  user_id: "",
  token: "",
  expires_at: "",
};

export const EMPTY_CREATE_VOICE_CALL_RESPONSE: CreateVoiceCallResponse = {
  call: EMPTY_VOICE_CALL,
  media: EMPTY_VOICE_CALL_MEDIA,
};

export const EMPTY_GET_VOICE_CALL_RESPONSE: GetVoiceCallResponse = {
  call: EMPTY_VOICE_CALL,
};

export const EMPTY_VOICE_CALL_DUPLEX_AUDIO_HINT: VoiceCallDuplexAudioHint = {
  input_format: "",
  input_sample_rate: 0,
  output_format: "",
  output_sample_rate: 0,
};

export const EMPTY_VOICE_CALL_DUPLEX_EVENT_HINT: VoiceCallDuplexEventHint = {
  client: [],
  server: [],
};

export const EMPTY_START_VOICE_CALL_DUPLEX_RESPONSE: StartVoiceCallDuplexResponse = {
  call: EMPTY_VOICE_CALL,
  mode: "",
  ws_path: "",
  audio: EMPTY_VOICE_CALL_DUPLEX_AUDIO_HINT,
  events: EMPTY_VOICE_CALL_DUPLEX_EVENT_HINT,
};

export const EMPTY_SANDBOX_SNAPSHOT: SandboxSnapshot = {
  id: "",
  workspace_id: "",
  node_id: "",
  cube_snapshot_id: "",
  name: "",
  description: "",
  status: "creating",
  created_at: "",
  updated_at: "",
};

// Reminders (task #655/#656, `agent_reminder_read.go`'s `humanReminder*`
// shapes). `status`/`schedule_kind`/`definition_status` stay `z.string()`
// (never `z.enum()`) so an unrecognized value still parses the row instead
// of rejecting the whole page — `adaptUpcomingRow`/`adaptFiredRow` in
// reminder-view-model.ts are the boundary that narrows to the app's strict
// literal unions and drops a row it can't safely classify, never
// misrendering an unknown value as one of the known states.
const RawReminderAnchorSchema = z.object({
  available: z.boolean(),
  // Not `z.enum()` — an unrecognized future anchor kind must degrade just
  // this row's anchor (see `adaptAnchor`), not fail the whole array element
  // and, transitively, the entire page.
  kind: z.string().optional(),
  // LRM-507: readable channel/DM name (preferred over legacy `display`).
  display_name: z.string().optional(),
  display: z.string().optional(),
  href: z.string().optional(),
}).loose();

const RawReminderDefinitionSchema = z.object({
  id: z.string(),
  title: z.string(),
  status: z.string(),
  schedule_kind: z.string(),
  next_fire_at: z.string().optional(),
  last_fire_at: z.string().optional(),
  cadence: z.string().optional(),
  schedule_timezone: z.string().optional(),
  snooze_count: z.number().default(0),
  anchor: RawReminderAnchorSchema,
}).loose();

const RawReminderOccurrenceSchema = z.object({
  id: z.string(),
  reminder_id: z.string(),
  title: z.string(),
  status: z.string(),
  definition_status: z.string(),
  schedule_kind: z.string(),
  cadence_scheduled_for: z.string(),
  due_at: z.string(),
  fired_at: z.string(),
  cadence: z.string().optional(),
  schedule_timezone: z.string().optional(),
  anchor: RawReminderAnchorSchema,
}).loose();

export const RawReminderPageSchema = z.object({
  definitions: z.array(RawReminderDefinitionSchema).default([]),
  occurrences: z.array(RawReminderOccurrenceSchema).default([]),
  limit: z.number().default(0),
  has_more: z.boolean().default(false),
  next_cursor: z.string().optional(),
}).loose();

export const EMPTY_REMINDER_PAGE: RawReminderPage = {
  definitions: [],
  occurrences: [],
  limit: 0,
  has_more: false,
};
