/** Legacy persisted execution/diagnostic state. Never use as Agent Presence. */
export type AgentStatus = "idle" | "working" | "blocked" | "error" | "offline";

export type AgentRuntimeMode = "local" | "cloud";

export type RuntimeUpdateState =
  | "idle"
  | "pending"
  | "running"
  | "completed"
  | "ready_to_apply"
  | "failed"
  | "timed_out";

export type RuntimeHealthState =
  | "ok"
  | "update_available"
  | "updating"
  | "failed"
  | "offline";

/**
 * Legacy diagnostic compatibility vocabulary from Agent Health. Current Web
 * Presence must never consume this field; lifecycle reasons belong only in
 * diagnostics, Timeline, and recovery surfaces.
 */
export type AgentRuntimeDisplayStatus =
  | "idle"
  | "working"
  | "starting"
  // Read compatibility for older servers; current presentation folds this
  // Computer-connectivity term into Agent Offline.
  | "disconnected"
  | "offline"
  | "stopped"
  | "crashed"
  | "blocked";

export type DaemonUpdateConfigSource =
  | "official_host_default"
  | "self_host_default"
  | "env_enabled"
  | "env_disabled"
  | "cli_disabled";

export type DaemonUpdateIneligibleReason =
  | "desktop_managed"
  | "non_release_build";

export type DaemonUpdatePhase =
  | "disabled"
  | "waiting"
  | "checking"
  | "updating"
  | "restart_pending";

export type DaemonUpdateAttemptSource = "auto" | "server";

export type DaemonUpdateOutcome =
  | "never_checked"
  | "up_to_date"
  | "busy"
  | "fetch_failed"
  | "update_failed"
  | "verification_failed"
  | "update_succeeded"
  | "interrupted";

export type DaemonUpdateErrorCode =
  | "daemon_restarted_during_update"
  | "release_fetch_failed"
  | "download_update_failed"
  | "updated_binary_verification_failed"
  | "desktop_managed";

export interface DaemonUpdateStatus {
  session_id: string;
  revision: number;
  observed_at: string;
  auto_update_effective_enabled: boolean;
  config_source: DaemonUpdateConfigSource;
  ineligible_reason: DaemonUpdateIneligibleReason | null;
  check_interval_seconds: number;
  phase: DaemonUpdatePhase;
  attempt_source: DaemonUpdateAttemptSource | null;
  last_attempt_at: string | null;
  last_outcome: DaemonUpdateOutcome;
  target_version: string | null;
  error_code: DaemonUpdateErrorCode | null;
  error_message: string | null;
  staged_version: string | null;
  activation_generation: number | null;
  received_at: string;
  updated_at: string;
}

export interface RuntimeDevice {
  id: string;
  workspace_id: string;
  daemon_id: string | null;
  name: string;
  /**
   * User-editable machine label. Empty means unset — clients should fall
   * back to `name` (daemon hostname / reported label). Daemon register /
   * heartbeat upsert never overwrites a non-empty value.
   */
  display_name?: string;
  runtime_mode: AgentRuntimeMode;
  provider: string;
  launch_header: string;
  /**
   * FE-facing projection of server/pkg/agent ProviderCapabilities for this
   * runtime's provider. Distinct from `capabilities` (daemon protocol string
   * list). Older servers omit it — treat missing as all-false. Prefer
   * lifecycle preflight's same object when gating the profile restart button.
   */
  provider_capabilities?: ProviderCapabilities;
  status: "online" | "offline";
  /**
   * Legacy composite from daemon registration (e.g.
   * "ubuntu · codex-cli 0.146.0"). Prefer `device_name` for the Basics →
   * OS row. Older servers only send this field.
   */
  device_info: string;
  /**
   * Machine label persisted from daemon register `device_name` (also in
   * `metadata.device_name`). Not a parse of `device_info`. Absent/empty
   * until the daemon re-registers after the server started persisting it.
   */
  device_name?: string;
  metadata: Record<string, unknown>;
  /**
   * Runtime/daemon-advertised protocol capabilities. Older daemons omit this;
   * consumers must treat a missing capability as unsupported, not as proof the
   * action is safe to send.
   */
  capabilities?: string[];
  current_version: string | null;
  /** Canonical release target for this daemon/computer, shared by all siblings. */
  daemon_target_version?: string | null;
  /** Legacy runtime lifecycle target. Computer UI must not use this for release selection. */
  target_version?: string | null;
  update_state: RuntimeUpdateState;
  runtime_health: RuntimeHealthState;
  update_error?: string | null;
  /** Canonical machine lifecycle; siblings with one daemon share this value. */
  machine_upgrade?: MachineUpgrade | null;
  /** Daemon-resolved update truth. Null/absent means an older daemon. */
  auto_update?: DaemonUpdateStatus | null;
  owner_id: string | null;
  /**
   * "private" (default) — only the owner / workspace admins can bind agents.
   * "public" — any workspace member can bind agents. Older servers may omit
   * the field; treat missing as private (fail closed for binding UI).
   */
  visibility?: "private" | "public";
  /**
   * Task #81 — non-null when the daemon's `MULTICA_PINNED_VERSION` reported
   * this machine as pinned. This only reflects the daemon's local intent —
   * the server does not yet enforce it against a server-initiated update
   * (that product decision is separate, unmade), so UI copy must say
   * "recorded intent," never "guaranteed not to be upgraded," and the
   * upgrade control must stay clickable, not disabled by this alone.
   */
  pinned_version?: string | null;
  last_seen_at: string | null;
  /**
   * Task #58 — physical machine (daemon) connectivity, independent of this
   * runtime's own `status` / `last_seen_at`. Present on servers that shipped
   * #1696; omitted on older responses. Machine-level surfaces must prefer
   * these over aggregating runtime heartbeats.
   */
  computer_connected?: boolean;
  /** ISO timestamp of the daemon's own heartbeat; null/absent when never seen. */
  daemon_last_seen_at?: string | null;
  created_at: string;
  updated_at: string;
}

export type AgentRuntime = RuntimeDevice;

/** Workspace-scoped Computer connection, independent of Agent runtime rows. */
export interface ComputerConnection {
  daemon_id: string;
  owner_id: string;
  connected: boolean;
  last_seen_at: string | null;
}

/** One durable on-disk Agent workspace at `~/.multica/workspaces/<workspace_id>/agents/<agent_id>`. */
export interface RuntimeAgentWorkspace {
  dir_name: string;
  rel_path: string;
  agent_id?: string | null;
  agent_name?: string | null;
  orphan: boolean;
  size_bytes?: number;
}

export interface RuntimeAgentWorkspacesResponse {
  runtime_id: string;
  /** ok | offline | missing | error */
  status: string;
  items: RuntimeAgentWorkspace[];
  truncated?: boolean;
}

// Coarse classifier set by the backend when a task transitions to "failed".
// Mirrors the migration-055 enum in agent_task_queue.failure_reason. Used by
// the agent presence derivation and the UI failure-message lookup.
export type TaskFailureReason =
  | "agent_error"
  | "timeout"
  | "codex_semantic_inactivity"
  | "runtime_offline"
  | "runtime_recovery"
  | "manual";

// One daily bucket for the Agents-list ACTIVITY sparkline. The back-end
// only returns days that had at least one completion; the front-end fills
// in missing days with zero when rendering the 7-bucket series. The series
// is anchored on completed_at (a task in flight contributes nothing).
export interface AgentActivityBucket {
  agent_id: string;
  // ISO timestamp at midnight UTC of the day.
  bucket_at: string;
  task_count: number;
  failed_count: number;
}

// 30-day total run count per agent, drives the Agents-list RUNS column.
export interface AgentRunCount {
  agent_id: string;
  run_count: number;
}

// One terminal task in the workspace-wide agent activity feed (overview
// timeline). Trimmed to display fields — the agent name is resolved client-side
// from the cached agent list. `status` is one of completed/failed/cancelled.
export interface AgentTaskFeedItem {
  id: string;
  agent_id: string;
  issue_id: string;
  // The linked issue's "PREFIX-N" identifier and title, resolved server-side.
  // Absent for tasks with no linked issue (chat/schedule-spawned). The title
  // is the primary "what did this agent do" description in the timeline row.
  issue_identifier?: string;
  issue_title?: string;
  // Title of the linked chat session — the "what" for chat-spawned tasks that
  // have no issue. Empty session titles are omitted.
  chat_title?: string;
  status: AgentTask["status"];
  completed_at: string | null;
  trigger_summary?: string;
}

// Opaque composite cursor — the (completed_at, id) of the last returned row.
export interface AgentTaskFeedCursor {
  completed_at: string;
  id: string;
}

export interface AgentTaskFeedPage {
  tasks: AgentTaskFeedItem[];
  has_more: boolean;
  next_cursor?: AgentTaskFeedCursor | null;
}

export type AgentHealthState =
  | "online"
  | "suspected_disconnect"
  | "reconnecting"
  | "recovered"
  | "offline";

export type AgentHealthEventType =
  | "server_ping_received"
  | "daemon_liveness_probe_sent"
  | "probe_timeout_reconnect"
  | "transport_reconnected";

export interface AgentHealthSummary {
  agent_id: string;
  runtime_id: string | null;
  state: AgentHealthState;
  reason_code: string;
  state_since: string | null;
  last_seen_at: string | null;
  last_event_at: string | null;
}

export interface AgentHealthEvent {
  id: string;
  agent_id: string;
  runtime_id: string | null;
  type: AgentHealthEventType;
  state_after: AgentHealthState;
  reason_code: string;
  message: string;
  occurred_at: string;
  details?: Record<string, unknown>;
  synthetic?: boolean;
}

export interface AgentHealthResponse {
  health_summary: AgentHealthSummary;
  health_events: AgentHealthEvent[];
}

// Overview "tasks done" KPI — completed/failed/total counts over ALL agent
// tasks in the workspace (issue, chat, and channel-reply tasks alike), so a
// channel reply counts as a finished task, matching the agent activity feed.
export interface AgentTaskStats {
  completed: number;
  failed: number;
  total: number;
}

export interface AgentTask {
  id: string;
  agent_id: string;
  runtime_id: string;
  // Empty string ("") when the task has no linked issue — either chat- or
  // schedule-spawned. Check chat_session_id / autopilot_run_id to tell
  // which source produced it.
  issue_id: string;
  status:
    | "queued"
    | "dispatched"
    | "running"
    | "completed"
    | "failed"
    | "cancelled";
  priority: number;
  dispatched_at: string | null;
  started_at: string | null;
  completed_at: string | null;
  result: unknown;
  error: string | null;
  /** Immutable model/reasoning choice captured when this run was created. */
  execution_config?: {
    model: string;
    thinking_level: string;
    snapshotted: boolean;
  };
  // Empty string when the task is not in a failed state (the backend uses
  // `omitempty`, so the field may also be missing on non-failed tasks).
  failure_reason?: TaskFailureReason | "";
  created_at: string;
  /** Non-empty when the task was spawned from a chat session. */
  chat_session_id?: string;
  /** Non-empty when the task was spawned by a legacy autopilot run (read-only). */
  autopilot_run_id?: string;
  /** Set when this task was created as an auto-retry of a parent task. */
  parent_task_id?: string;
  /** 1-based attempt counter; >1 means this is a retry. */
  attempt?: number;
  /** Set when an issue comment triggered this task (@mention or assignee comment). */
  trigger_comment_id?: string;
  /**
   * Canonical short description of what triggered this task — snapshot
   * taken at creation time. For comment-triggered tasks it's the
   * comment text (truncated to ~200 chars); for legacy schedule runs it's the
   * schedule title; NULL for direct assignments and chat tasks.
   * Persists even if the source comment / schedule is later edited
   * or deleted.
   */
  trigger_summary?: string;
  /**
   * Server-computed source discriminator used by the activity row to label
   * tasks that have no linked issue (so e.g. quick-create tasks render
   * with a meaningful title instead of falling through to "Untracked").
   */
  kind?:
    | "comment"
    | "autopilot"
    | "chat"
    | "quick_create"
    | "direct";
  /**
   * Local working directory pinned for this task by the daemon. Empty until
   * the daemon reports a work_dir (typically once execution starts). This is
   * the canonical absolute path the agent runs in; UI surfaces should prefer
   * `relative_work_dir` to avoid leaking the user's home directory.
   */
  work_dir?: string;
  /**
   * Privacy-safe display form of `work_dir`, derived on the server. For
   * canonical Agent workspaces the daemon's workspaces root has been stripped
   * off (`<workspaceUUID>/agents/<agentUUID>`). Unexpected external paths are
   * reduced to a safe home-relative path or basename so neither the home
   * directory nor username leaks into the UI. Older backends omit the field —
   * render it conditionally and never render `work_dir` raw (not even in
   * a tooltip / `title` / `aria-label`, since the goal is that screen
   * shares and screenshots also stay safe).
   */
  relative_work_dir?: string;
}

export interface Agent {
  id: string;
  workspace_id: string;
  runtime_id: string;
  /** Stable unique handle used for routing and bare @handle fallback. */
  name: string;
  /** Human-facing label. Falls back to `name` for older server payloads. */
  display_name?: string;
  /** Permanent agent honor level used by compact identity surfaces. */
  honor_level?: number;
  description: string;
  instructions: string;
  avatar_url: string | null;
  /**
   * Server-owned provenance for the persisted avatar value. Clients must
   * never infer provenance or a display fallback from the URL at render time.
   */
  avatar_source?: "assigned" | "picked" | "uploaded";
  runtime_mode: AgentRuntimeMode;
  /** Display name for the bound runtime, denormalized by the API. */
  runtime_name?: string | null;
  /**
   * Presence-safe projection of the bound runtime's connectivity. Always
   * attached when the runtime row exists, so temporary runtime-list gaps do
   * not erase the agent's last known reachability (LRM-248 AC5).
   */
  runtime_status?: "online" | "offline" | null;
  /** ISO heartbeat from the bound runtime; pairs with `runtime_status`. */
  runtime_last_seen_at?: string | null;
  /**
   * Diagnostic/deprecated compatibility projection. Do not use for avatar,
   * Profile, list, filter, or chat Presence; those read AgentPresence.
   */
  runtime_display_status?: AgentRuntimeDisplayStatus | null;
  /**
   * Sticky provider-quota lock (tasks #64/#77). Non-empty detail ⇒ locked.
   * Until null while locked means unknown end (still locked — never invent).
   */
  provider_blocked_until?: string | null;
  provider_block_detail?: string | null;
  /** Durable first-start lifecycle reported by the selected Computer. */
  start_intent_status?: "pending" | "accepted" | "queued" | "ready" | "failed";
  /** Sanitized local failure category when `start_intent_status` is failed. */
  start_intent_failure_code?: string;
  runtime_config: Record<string, unknown>;
  custom_args: string[];
  /**
   * Coarse metadata signalling whether the agent has any custom env
   * vars configured, without exposing the keys or values. Reads of
   * the real map go through the dedicated `GET /api/agents/{id}/env`
   * endpoint (owner/admin only, audited). MUL-2600.
   *
   * Optional in the type so older backends (pre-MUL-2600) that omit
   * the field don't crash the renderer; downstream code should treat
   * `undefined` as "unknown — assume no env" rather than "definitely
   * has env".
   */
  has_custom_env?: boolean;
  /**
   * Number of keys in the agent's custom_env map. Always present
   * alongside `has_custom_env`. Treat `undefined` as zero. MUL-2600.
   */
  custom_env_key_count?: number;
  /**
   * MCP server configuration forwarded to runtimes that consume
   * `agent.mcp_config` (see providerSupportsMcpConfig). Each backend
 * materialises it in the runtime-native place: Claude/Pi flags, Cursor
 * `.cursor/mcp.json`, Codex config.toml, ACP session params, OpenCode env
 * config, OpenClaw wrapper config, etc. `null` (or the field omitted on
 * legacy backends) means no managed config; the daemon falls back to the
 * CLI's own default. MUL-2764.
   *
   * When the caller can't see secrets (an agent actor, or a non-owner
   * non-admin), the server replaces the value with `null` and sets
   * `mcp_config_redacted` to true so the UI can render a "configured
   * but hidden" state without exposing potentially sensitive fields.
   */
  mcp_config?: unknown | null;
  /**
   * True when the server stripped `mcp_config` from this response
   * because the caller lacks permission to see secrets. The UI uses
   * this to distinguish "no config" (`mcp_config === null &&
   * !mcp_config_redacted`) from "config exists but you can't see it".
   * Older backends omit this field; treat `undefined` as false.
   */
  mcp_config_redacted?: boolean;
  /**
   * Retired with agent.visibility (#908, task #908 batch3) — the channel-scoped
   * binding this used to require no longer exists server-side. The backend
   * always returns `null` here and rejects a non-null value on write; the
   * field itself is deleted in a later batch alongside the DB column.
   */
  home_channel_id?: string | null;
  status: AgentStatus;
  /** Platform-managed research worker marker; absent for ordinary agents. */
  managed_role?: "research_fleet";
  /** Workspace-level authority. Agents can be members or admins, never owners. */
  workspace_role: "member" | "admin";
  max_concurrent_tasks: number;
  model: string;
  /**
   * Runtime-native reasoning/effort token (e.g. Claude's
   * `low|medium|high|xhigh|max`, Codex's
   * `none|minimal|low|medium|high|xhigh`). Empty string means "no
   * override": the backend omits the effort flag and the upstream CLI
   * config / built-in default decides at run time. The picker is
   * per-runtime per-model — the API never normalises across providers.
   * Older backends omit this field entirely; treat undefined as ""
   * (MUL-2339).
   */
  thinking_level?: string;
  owner_id: string | null;
  skills: AgentSkillSummary[];
  created_at: string;
  updated_at: string;
  archived_at: string | null;
  archived_by: string | null;
  /**
   * Phase② Memory growth (LRM-303 / LRM-304). Present only on GetAgent when
   * the agent has ≥1 valid memory write. Omitted for zero writes — FE hides
   * the growth block. Never attach to message rows.
   */
  memory_growth?: AgentMemoryGrowth | null;
}

/** Tier id for Memory growth (LRM-274 Phase② / LRM-303). */
export type AgentMemoryGrowthTier = "bronze" | "silver" | "gold" | "platinum";

/** Four-segment bar slot status (LRM-303). */
export type AgentMemoryGrowthSegmentStatus =
  | "complete"
  | "current"
  | "upcoming";

export interface AgentMemoryGrowthSegment {
  tier: AgentMemoryGrowthTier | string;
  tier_label: string;
  status: AgentMemoryGrowthSegmentStatus | string;
}

/** Fine progress toward the next tier (`Next · n/m writes`). */
export interface AgentMemoryGrowthNext {
  tier: AgentMemoryGrowthTier | string;
  tier_label: string;
  current: number;
  required: number;
}

/**
 * Profile/card Memory growth payload from LRM-303
 * (`GET /api/agents/:id` / full member profile).
 */
export interface AgentMemoryGrowth {
  total_writes: number;
  tier: AgentMemoryGrowthTier | string;
  tier_label: string;
  segments: AgentMemoryGrowthSegment[];
  next?: AgentMemoryGrowthNext | null;
}

/**
 * Minimal skill shape embedded in an Agent payload (`GET /api/agents`,
 * `GET /api/agents/:id`). Only id/name/description are populated — the
 * agent list batch query joins exactly those three columns. For full skill
 * info, use `GET /api/agents/:id/skills` (returns `SkillSummary[]`) or
 * `GET /api/skills/:id` (returns the full `Skill`).
 */
export interface AgentSkillSummary {
  id: string;
  name: string;
  description: string;
}

export interface AgentCreationDraft {
  id: string;
  workspace_id: string;
  created_by_agent_id?: string | null;
  target_user_id: string;
  name: string;
  description: string;
  instructions: string;
  avatar_url?: string | null;
  project_id?: string | null;
  channel_id?: string | null;
  can_execute_code: boolean;
  suggested_channels: string[];
  recommended_tools: string[];
  initial_notes?: Record<string, string>;
  initial_memory?: Record<string, string>;
  status: "draft" | "used" | "dismissed";
  used_agent_id?: string | null;
  created_at: string;
  updated_at: string;
  used_at?: string | null;
}

/** Canonical Message-backed agent:create Proposal shown in shared timelines. */
export interface AgentCreationProposal {
  message_id: string;
  status: "prepared" | "executed";
  /** Permanent Agent name proposed for creation. */
  name: string;
  description: string;
  preferred_computer?: string;
  committer_user_id?: string;
  result_agent_id?: string;
}

export interface CreateAgentDraftRequest {
  name: string;
  description?: string;
  instructions?: string;
  avatar_url?: string | null;
  project_id?: string | null;
  channel_id?: string | null;
  can_execute_code?: boolean;
  suggested_channels?: string[];
  recommended_tools?: string[];
  initial_notes?: Record<string, string>;
  initial_memory?: Record<string, string>;
}

export interface EnsureWindyResponse {
  agent: Agent;
  dm_id?: string;
}

/** Verified avatar write intent. The server derives and persists the URL and
 * source; clients never submit a raw agent avatar URL. */
export type AgentAvatarSelection =
  | { kind: "uploaded"; attachment_id: string; preset_url?: never }
  | { kind: "picked"; preset_url: string; attachment_id?: never };

export interface CreateAgentRequest {
  /** Permanent Agent name used for @mentions. */
  name: string;
  /** Optional human-facing label. Editable later from Profile. */
  display_name?: string;
  description?: string;
  instructions?: string;
  avatar_selection?: AgentAvatarSelection;
  runtime_id: string;
  runtime_config?: Record<string, unknown>;
  custom_env?: Record<string, string>;
  custom_args?: string[];
  /**
   * Retired with agent.visibility (#908, task #908 batch3) — the server
   * always rejects a non-null value here now. Field itself is deleted in
   * a later batch alongside the DB column.
   */
  home_channel_id?: string | null;
  max_concurrent_tasks?: number;
  model?: string;
  /** Optional runtime-native reasoning/effort token. See `Agent.thinking_level`. */
  thinking_level?: string;
  /** Optional non-URL seed context for new agent notes (research / legacy seed). */
  initial_notes?: Record<string, string>;
  /** Optional non-URL seed context for durable memory (research / legacy seed). */
  initial_memory?: Record<string, string>;
  /** Optional template slug used by the onboarding agent picker. Surfaced
   *  as the `template` property on the `agent_created` PostHog event. */
  template?: string;
  /** Canonical agent:create Proposal Message to atomically commit. */
  action_message_id?: string;
  /** Research / legacy seed only — not the hire path (agent drafts create is 410). */
  draft_id?: string;
}

/** Agent template summary — fields needed by the picker grid. Does NOT
 *  include `instructions` to keep the list payload small; the detail
 *  endpoint or the create flow returns the full template body. */
export interface AgentTemplateSummary {
  slug: string;
  name: string;
  description: string;
  /** Optional grouping for the picker UI ("Engineering" / "Writing" / …). */
  category?: string;
  /** Optional lucide-react icon name (e.g. "Search"). Frontend falls back
   *  to a generic icon when empty. */
  icon?: string;
  /** Optional semantic color token for the icon badge — one of "info" /
   *  "success" / "warning" / "primary" / "secondary". Frontend has a
   *  static class map so Tailwind can JIT-scan all variants. */
  accent?: string;
  skills: AgentTemplateSkillRef[];
}

/** Full agent template — same as `AgentTemplateSummary` plus the
 *  instructions block. Returned by `GET /api/agent-templates/:slug`. */
export interface AgentTemplate extends AgentTemplateSummary {
  instructions: string;
}

/** Skill reference inside an agent template. `source_url` is the upstream
 *  GitHub / skills.sh URL fetched on create; `cached_*` mirror the upstream
 *  frontmatter at template-author time and let the picker render without
 *  HTTP fetches. */
export interface AgentTemplateSkillRef {
  source_url: string;
  cached_name: string;
  cached_description: string;
}

export interface CreateAgentFromTemplateRequest {
  template_slug: string;
  /** Legacy display input; the server derives a stable handle from it. */
  name?: string;
  /** Preferred human-facing label for new clients. */
  display_name?: string;
  runtime_id: string;
  model?: string;
  max_concurrent_tasks?: number;
  /** Optional overrides applied to the template before creation. nil/omit
   *  uses the template's own value. */
  description?: string;
  instructions?: string;
  avatar_selection?: AgentAvatarSelection;
  /** Workspace skill IDs attached **in addition to** the template's
   *  skills. Server dedupes against template skills automatically. */
  extra_skill_ids?: string[];
}

export interface CreateAgentFromTemplateResponse {
  agent: Agent;
  /** Skill IDs that were newly created in the workspace from upstream URLs. */
  imported_skill_ids: string[];
  /** Skill IDs that already existed in the workspace (same name) and were
   *  reused rather than re-imported. The UI can surface this as a toast so
   *  the user knows their pre-existing skill wasn't overwritten. */
  reused_skill_ids: string[];
}

/** 422 body returned by `POST /api/agents/from-template` when one or more
 *  template skill URLs cannot be reached. The transaction is rolled back —
 *  no partial workspace state. */
export interface CreateAgentFromTemplateFailure {
  error: string;
  failed_urls: string[];
}

export interface UpdateAgentRequest {
  /** Preferred human-facing label for new clients. */
  display_name?: string;
  description?: string;
  instructions?: string;
  avatar_selection?: AgentAvatarSelection;
  runtime_id?: string;
  runtime_config?: Record<string, unknown>;
  /**
   * NOTE: `custom_env` is intentionally NOT updatable through this
   * request shape. Env edits flow through `client.updateAgentEnv` /
   * `PUT /api/agents/{id}/env` — that path is owner/admin only,
   * denies agent actors, and writes a persistent audit row. The
   * server REJECTS any `PUT /api/agents/{id}` body that includes
   * `custom_env` with a 400; do not put the field in this payload.
   * MUL-2600.
   */
  custom_args?: string[];
  /**
   * MCP server configuration. Tri-state semantics (MUL-2764):
   *   - field omitted → no change
   *   - `null` → clear the column; the daemon falls back to the CLI's
   *     built-in default at launch
   *   - object → replace the stored JSON verbatim; runtime backends
   *     validate / translate it according to their own MCP integration
   */
  mcp_config?: unknown | null;
  /**
   * Retired with agent.visibility (#908, task #908 batch3) — the server
   * always rejects a non-null value here now. Field itself is deleted in
   * a later batch alongside the DB column.
   */
  home_channel_id?: string | null;
  status?: AgentStatus;
  max_concurrent_tasks?: number;
  model?: string;
	/** Completed runtime-model discovery request backing an execution-config save. */
	model_catalog_request_id?: string;
  /**
   * Runtime-native reasoning/effort token. Tri-state semantics (MUL-2339):
   *   - field omitted → no change
   *   - "" → clear the override; backend omits the effort flag and the
   *     local CLI config / built-in default decides what the model runs at
   *   - non-empty → set; validated server-side against the target
   *     runtime's provider enum, rejected with 400 if not recognised
   */
  thinking_level?: string;
}

/**
 * Wire shape for the dedicated env-management endpoints
 * (`GET /api/agents/{id}/env` and `PUT /api/agents/{id}/env`). Kept
 * deliberately separate from `Agent` so generic agent reads cannot
 * accidentally surface env values. MUL-2600.
 */
export interface AgentEnvResponse {
  agent_id: string;
  custom_env: Record<string, string>;
}

/**
 * Body for `PUT /api/agents/{id}/env`. Values equal to `"****"` are
 * treated by the server as "preserve the existing value for this key"
 * — a defence-in-depth guard so a UI that round-trips a masked map
 * cannot accidentally clobber real secrets. Submit only the keys
 * touched in the form; omitted keys are removed by the server.
 */
export interface UpdateAgentEnvRequest {
  custom_env: Record<string, string>;
}

// Skills

/**
 * Lightweight skill shape returned by list endpoints (`GET /api/skills`,
 * `GET /api/agents/:id/skills`). The full SKILL.md `content` is intentionally
 * omitted — bodies routinely run 50–200KB each and shipping them in list
 * payloads tripped CLI timeouts on high-latency links (GH
 * multica-ai/multica#2174). Use `Skill` from a detail endpoint when you need
 * the body. For skills embedded in an `Agent` payload see `AgentSkillSummary`.
 */
/**
 * LRM-954 — Skill grant / promotion tier.
 * L1=`agent` (default on import), L2=`channel`, L3=`workspace`.
 */
export type SkillGrantLevel = "agent" | "channel" | "workspace";

/** Server-computed promote gates for the current caller (LRM-954). */
export interface SkillCapabilities {
  can_promote_to_channel: boolean;
  can_promote_to_workspace: boolean;
}

export interface SkillSummary {
  id: string;
  workspace_id: string;
  name: string;
  description: string;
  config: Record<string, unknown>;
  created_by: string | null;
  created_at: string;
  updated_at: string;
  /** LRM-954; absent on older servers → treat as `"agent"`. */
  grant_level?: SkillGrantLevel;
  /** Set when `grant_level === "channel"`. */
  channel_id?: string | null;
  /** Promote buttons; absent → no promote entry. */
  capabilities?: SkillCapabilities;
}

export interface Skill extends SkillSummary {
  content: string;
  files: SkillFile[];
}

/** One row from `GET /api/skills/{id}/promotions` (LRM-954). */
export interface SkillPromotion {
  id: string;
  skill_id: string;
  from_level: SkillGrantLevel;
  to_level: SkillGrantLevel;
  channel_id: string | null;
  actor_type: "member" | "agent" | string;
  actor_id: string;
  actor_display_name?: string | null;
  created_at: string;
}

export interface SkillPromotionsResponse {
  items: SkillPromotion[];
  total?: number;
}

export interface PromoteSkillRequest {
  to_level: "channel" | "workspace";
  /** Required when `to_level === "channel"`. */
  channel_id?: string;
}

export interface SkillFile {
  id: string;
  skill_id: string;
  path: string;
  content: string;
  created_at: string;
  updated_at: string;
}

export interface PlatformSkillSummary {
  name: string;
  description: string;
  installed_skill_id?: string;
}

export interface AgentMemory {
  id: string;
  workspace_id: string;
  agent_id: string;
  name: string;
  content: string;
  config: Record<string, unknown>;
  sync_key: string;
  content_hash: string;
  created_by: string | null;
  created_at: string;
  updated_at: string;
}

export interface AgentFileNode {
  path: string;
  is_dir: boolean;
  size?: number;
}

export type AgentFilesStatus = "ok" | "offline" | "missing" | "error";

export interface AgentFilesResponse {
  agent_id: string;
  status: AgentFilesStatus;
  nodes: AgentFileNode[];
  truncated: boolean;
}

export interface AgentFileContentResponse {
  content: string;
  encoding: string;
  mime_type: string;
  content_hash: string;
  truncated: boolean;
  too_large: boolean;
  binary: boolean;
}

export interface UpdateAgentFileContentRequest {
  path: string;
  content: string;
  expected_content_hash?: string;
}

export interface UpdateAgentFileContentResponse {
  content_hash: string;
  conflict: boolean;
}

export type AgentSkillSuggestionAction = "add" | "remove";

export type AgentSkillSuggestionStatus = "pending" | "accepted" | "dismissed";

export interface AgentSkillSuggestion {
  id: string;
  workspace_id: string;
  agent_id: string;
  skill_id: string;
  action: AgentSkillSuggestionAction;
  reason: string;
  matcher_score: number;
  matcher_details?: Record<string, unknown>;
  status: AgentSkillSuggestionStatus;
  skill_name: string;
  skill_description: string;
  created_at: string;
  updated_at: string;
}

export interface ListAgentSkillSuggestionsResponse {
  suggestions: AgentSkillSuggestion[];
}

// Agent lifecycle actions (#632/#633). Single server entry, three kinds; the
// client only ever sends `action_kind` (the server resolves the workspace root
// from the agent/workspace binding — never a path).
export type AgentLifecycleActionKind =
  | "restart"
  | "reset_session_restart"
  | "full_reset_restart";

export type AgentLifecycleExecutionMode = "immediate" | "after_current_run";

export type AgentLifecycleOperationStatus =
  | "scheduled"
  | "running"
  | "succeeded"
  | "failed";

/**
 * Per-action executability from the preflight — server-authoritative. The FE
 * must not derive active/idle from `agent.status`; `supported`/`disabled_reason`
 * is the final judge (covers permission, no runtime, offline/old daemon, and the
 * dormant `unsupported_runtime_capability` gate before #677 D6 activates).
 * `full_reset_restart` is idle-only: while a run is active it reports
 * `{ supported: false, disabled_reason: "agent_active" }` and is never scheduled.
 */
export interface AgentLifecycleActionState {
  supported: boolean;
  disabled_reason?: string | null;
  execution_mode: AgentLifecycleExecutionMode;
}

export interface AgentLifecycleOperation {
  id: string;
  agent_id: string;
  runtime_id: string | null;
  action_kind: AgentLifecycleActionKind;
  status: AgentLifecycleOperationStatus;
  execution_mode: AgentLifecycleExecutionMode;
  step?: string | null;
  reason_code?: string | null;
  created_at: string;
  started_at?: string | null;
  finished_at?: string | null;
}

/**
 * FE-facing projection of server/pkg/agent ProviderCapabilities. Exposed as a
 * set on runtime + lifecycle preflight — do not add one-off top-level bools
 * per capability. Older servers omit the object; treat missing keys as false.
 */
export interface ProviderCapabilities {
  force_restart: boolean;
  custom_model_id: boolean;
  model_selection: boolean;
  thinking_discovery: boolean;
  canonical_resident: boolean;
  needs_inline_system_prompt: boolean;
}

export interface AgentLifecyclePreflight {
  actions: Record<AgentLifecycleActionKind, AgentLifecycleActionState>;
  active_operation?: AgentLifecycleOperation | null;
  /**
   * Provider capability set for this agent's runtime. Gate the profile restart
   * button on `provider_capabilities.force_restart` — do not hardcode a
   * provider allow-list. Older servers omit the object; treat missing as
   * all-false.
   */
  provider_capabilities?: ProviderCapabilities;
}

export interface DecideAgentSkillSuggestionRequest {
  decision: "accept" | "dismiss";
}

export interface CreateSkillRequest {
  name: string;
  description?: string;
  content?: string;
  config?: Record<string, unknown>;
  files?: { path: string; content: string }[];
}

export interface UpdateSkillRequest {
  name?: string;
  description?: string;
  content?: string;
  config?: Record<string, unknown>;
  files?: { path: string; content: string }[];
}

export interface SetAgentSkillsRequest {
  skill_ids: string[];
}

export interface IssueUsageSummary {
  total_input_tokens: number;
  total_output_tokens: number;
  total_cache_read_tokens: number;
  total_cache_write_tokens: number;
  task_count: number;
}

export interface RuntimeUsage {
  runtime_id: string;
  date: string;
  provider: string;
  model: string;
  input_tokens: number;
  output_tokens: number;
  cache_read_tokens: number;
  cache_write_tokens: number;
}

export interface RuntimeHourlyActivity {
  hour: number;
  count: number;
}

// One (agent, model) row of the "Cost by agent" tab on the runtime detail
// page. Model stays on the wire because cost is computed client-side from
// a per-model pricing table — the client groups these rows by agent_id and
// sums cost per agent across models.
export interface RuntimeUsageByAgent {
  agent_id: string;
  model: string;
  input_tokens: number;
  output_tokens: number;
  cache_read_tokens: number;
  cache_write_tokens: number;
  task_count: number;
}

// One (hour, model) row for the "By hour" tab; hour ∈ 0..23. Hours with
// zero activity are omitted by the server; the client fills the gap to
// render a continuous axis. Model preserved for client-side cost math.
export interface RuntimeUsageByHour {
  hour: number;
  model: string;
  input_tokens: number;
  output_tokens: number;
  cache_read_tokens: number;
  cache_write_tokens: number;
  task_count: number;
}

// One (date, model) bucket of token usage for the workspace dashboard.
// Same shape as RuntimeUsage but workspace-scoped (no runtime_id, no
// provider field on the wire) and optionally narrowed to a single project
// on the server side. Cost stays client-side via the model pricing table.
export interface DashboardUsageDaily {
  date: string;
  model: string;
  input_tokens: number;
  output_tokens: number;
  cache_read_tokens: number;
  cache_write_tokens: number;
  task_count: number;
}

// Per-(agent, model) token totals for the workspace dashboard. Identical
// wire shape to RuntimeUsageByAgent — the client folds by agent_id and
// sums cost.
export interface DashboardUsageByAgent {
  agent_id: string;
  model: string;
  input_tokens: number;
  output_tokens: number;
  cache_read_tokens: number;
  cache_write_tokens: number;
  task_count: number;
}

// Per-agent total terminal-task run-time + counts. Powers the workspace
// dashboard's "time by agent" list. failed_count is a subset of
// task_count (failed tasks still contribute to total_seconds because
// they consumed runtime to fail).
export interface DashboardAgentRunTime {
  agent_id: string;
  total_seconds: number;
  task_count: number;
  failed_count: number;
}

// One (date) bucket of terminal-task run-time + counts for the workspace
// dashboard. Powers the Time and Tasks metrics on the daily-trend toggle
// — same toggle as Tokens / Cost, anchored on completed_at so day buckets
// line up with the per-agent run-time card.
export interface DashboardRunTimeDaily {
  date: string;
  total_seconds: number;
  task_count: number;
  failed_count: number;
}

export type RuntimeUpdateStatus =
  // Update requested but not yet delivered — the server is durably holding
  // the request until the runtime's next heartbeat proves it reachable
  // (2026-08-02: replaces the old one-shot 120s delivery window that a
  // sleeping laptop could simply miss). Not terminal, not yet "running".
  | "queued"
  | "pending"
  | "running"
  | "completed"
  | "ready_to_apply"
  | "failed"
  | "timeout";

export type MachineUpgradePhase =
  | "queued"
  | "starting"
  | "staging"
  | "verifying"
  | "handoff"
  | "converging"
  | "rollback_pending"
  | "completed"
  | "failed"
  | "rolled_back"
  | "timeout"
  | "cancelled";

/** The daemon-scoped source of truth projected by every sibling runtime. */
export interface MachineUpgrade {
  id: string;
  daemon_id: string;
  request_id: string;
  requested_target: string;
  resolved_target?: string | null;
  phase: MachineUpgradePhase;
  result?: "completed" | "failed" | "rolled_back" | "timeout" | "cancelled" | null;
  error_code?: string | null;
  error_message?: string | null;
  accepted_at?: string | null;
  accepted_generation?: string | null;
  accepted_runtime_ids?: string[];
  attested_runtime_ids?: string[];
  source_version?: string | null;
  rollback_generation?: string | null;
  rollback_runtime_ids?: string[];
  completed_at?: string | null;
  created_at: string;
  updated_at: string;
}

export interface RuntimeUpdate {
  id: string;
  runtime_id: string;
  status: RuntimeUpdateStatus;
  target_version: string;
  output?: string;
  error?: string;
  created_at: string;
  updated_at: string;
}

// Task #8/#43 (2026-07-31) — remote daemon restart via heartbeat pickup.
// Simpler lifecycle than RuntimeUpdate: there's no "did the restart
// succeed" terminal confirmation from this API itself — `delivered` means
// the daemon's heartbeat claimed the request and called its own
// triggerRestart(), not that the process has come back up. The runtime's
// own presence (existing runtime-list query) is what shows the daemon is
// back online, same as how UpdateSection hands off to the global surfaces
// once a request is airborne.
export type RuntimeRestartStatus = "pending" | "delivered" | "timeout";

export interface RuntimeRestart {
  id: string;
  runtime_id: string;
  status: RuntimeRestartStatus;
  created_at: string;
  updated_at: string;
  delivered_at?: string;
}

export interface RuntimeModel {
  id: string;
  label: string;
  provider?: string;
  default?: boolean;
  /**
   * Per-model reasoning/effort catalog discovered by the daemon. Currently
   * populated for claude, codex, opencode, and pi runtimes; omitted (or undefined)
   * for every other provider, which the UI treats as "no thinking-level
   * picker for this model". See MUL-2339.
   */
  thinking?: RuntimeModelThinking;
}

export interface RuntimeModelThinking {
  /** Levels the user is allowed to pick for this model. */
  supported_levels: RuntimeModelThinkingLevel[];
  /** Informational: the level the upstream CLI documents as its built-in
   *  default when no `--effort` flag is passed. Surfaced by the daemon
   *  but not actively rendered today — Multica's empty `thinking_level`
   *  means "no override; let the local CLI config decide", which may
   *  itself differ from this value. */
  default_level?: string;
}

export interface RuntimeModelThinkingLevel {
  /** Runtime-native token passed to the CLI; never normalised. */
  value: string;
  /** Display label matching each CLI's own UI (`Low`, `Extra high`, …). */
  label: string;
  /** Optional helper copy lifted from upstream catalog
   *  (`codex debug models` emits one per level). */
  description?: string;
}

export type RuntimeModelListStatus =
  | "pending"
  | "running"
  | "completed"
  | "failed"
  | "timeout";

export interface RuntimeModelListRequest {
  id: string;
  runtime_id: string;
  status: RuntimeModelListStatus;
  models?: RuntimeModel[];
  supported: boolean;
  /**
   * Backend-owned capability (agent.CustomModelIDSupported): whether this
   * runtime accepts an arbitrary typed model id. Older servers omit it —
   * treat missing as false so the free-form input stays hidden.
   */
  custom_model_id_supported?: boolean;
  /**
   * Backend-owned capability (agent.Capabilities.ThinkingDiscovery): whether
   * this runtime exposes a reasoning/effort catalog. Older servers omit it —
   * treat missing as false so the thinking-level picker stays hidden (#59).
   */
  thinking_discovery?: boolean;
  error?: string;
  created_at: string;
  updated_at: string;
}

// Result shape returned by resolveRuntimeModels — includes the
// "supported" bit so the UI can distinguish "no models discovered"
// from "provider does not honour per-agent model selection", plus
// whether a free-form Custom model ID input is allowed, plus whether
// the runtime exposes a thinking/effort catalog (#59).
export interface RuntimeModelsResult {
  models: RuntimeModel[];
  supported: boolean;
  customModelIdSupported: boolean;
  thinkingDiscovery: boolean;
}

export type RuntimeLocalSkillStatus =
  | "pending"
  | "running"
  | "completed"
  | "conflict"
  | "failed"
  | "timeout";

export type RuntimeLocalSkillImportAction = "overwrite";

export interface RuntimeLocalSkillImportConflict {
  existing_skill_id: string;
  existing_created_by?: string;
  can_overwrite: boolean;
}

export interface RuntimeLocalSkillSummary {
  key: string;
  name: string;
  description?: string;
  source_path: string;
  provider: string;
  file_count: number;
}

export interface RuntimeLocalSkillListRequest {
  id: string;
  runtime_id: string;
  status: RuntimeLocalSkillStatus;
  skills?: RuntimeLocalSkillSummary[];
  supported: boolean;
  error?: string;
  created_at: string;
  updated_at: string;
}

export interface CreateRuntimeLocalSkillImportRequest {
  skill_key: string;
  name?: string;
  description?: string;
  action?: RuntimeLocalSkillImportAction;
  target_skill_id?: string;
  supports_conflict?: boolean;
}

export interface RuntimeLocalSkillImportRequest {
  id: string;
  runtime_id: string;
  skill_key: string;
  name?: string;
  description?: string;
  action?: RuntimeLocalSkillImportAction;
  target_skill_id?: string;
  supports_conflict?: boolean;
  status: RuntimeLocalSkillStatus;
  skill?: Skill;
  conflict?: RuntimeLocalSkillImportConflict;
  error?: string;
  created_at: string;
  updated_at: string;
}

export interface RuntimeLocalSkillsResult {
  skills: RuntimeLocalSkillSummary[];
  supported: boolean;
}

export interface RuntimeLocalSkillImportResult {
  status: "created" | "updated" | "conflict";
  skill?: Skill;
  conflict?: RuntimeLocalSkillImportConflict;
}
export type AgentPresence = "online" | "offline";

export interface AgentPresenceItem {
  agent_id: string;
  presence: AgentPresence;
}

export interface AgentPresenceResponse {
  items: AgentPresenceItem[];
}
