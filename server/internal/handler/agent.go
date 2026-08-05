package handler

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/go-chi/chi/v5"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/multica-ai/multica/server/internal/agentworkspace"
	"github.com/multica-ai/multica/server/internal/analytics"
	"github.com/multica-ai/multica/server/internal/logger"
	obsmetrics "github.com/multica-ai/multica/server/internal/metrics"
	"github.com/multica-ai/multica/server/internal/middleware"
	"github.com/multica-ai/multica/server/internal/service"
	"github.com/multica-ai/multica/server/pkg/agent"
	db "github.com/multica-ai/multica/server/pkg/db/generated"
	"github.com/multica-ai/multica/server/pkg/protocol"
)

// Mirrors AGENT_DESCRIPTION_MAX_LENGTH in packages/core/agents/constants.ts
// and the agent_description_length CHECK constraint in migration 060. Counted
// in unicode code points (utf8.RuneCountInString), matching Postgres
// char_length and the front-end's String.prototype.length-with-counter UX.
const maxAgentDescriptionLength = 255

type AgentResponse struct {
	ID           string  `json:"id"`
	WorkspaceID  string  `json:"workspace_id"`
	RuntimeID    string  `json:"runtime_id"`
	Name         string  `json:"name"`
	DisplayName  string  `json:"display_name"`
	Description  string  `json:"description"`
	Instructions string  `json:"instructions"`
	AvatarURL    *string `json:"avatar_url"`
	AvatarSource string  `json:"avatar_source"`
	RuntimeMode  string  `json:"runtime_mode"`
	RuntimeName  string  `json:"runtime_name"`
	// Presence-safe projection of the bound runtime. Always filled when the
	// runtime row exists. Runtime tenancy remains workspace-scoped (LRM-248 AC5).
	RuntimeStatus     string  `json:"runtime_status,omitempty"`
	RuntimeLastSeenAt *string `json:"runtime_last_seen_at,omitempty"`
	// RuntimeDisplayStatus is the honest, read-time status for this surface
	// (task #42③): unlike RuntimeStatus above (a raw passthrough of
	// agent_runtime.status, which can read "online" for up to ~180s after
	// the daemon actually went silent), this is freshness-gated the same
	// way agentHealthSummary already gates the Activity Health tab. Prefer
	// this field for any UI that shows a live status badge; RuntimeStatus
	// stays for callers that need the raw dispatch-relevant value.
	RuntimeDisplayStatus string `json:"runtime_display_status,omitempty"`
	// ProviderBlockedUntil / Detail (tasks #64/#77): sticky provider-quota
	// display lock. Detail non-empty ⇒ locked; Until nil while locked means
	// unknown end (still locked). Heartbeats do not clear it. Claim/drain
	// gating is a separate card.
	ProviderBlockedUntil *string `json:"provider_blocked_until,omitempty"`
	ProviderBlockDetail  string  `json:"provider_block_detail,omitempty"`
	// RuntimePinnedVersion (task #81) is non-nil when the daemon's
	// MULTICA_PINNED_VERSION reported this machine as pinned. This only
	// reflects the daemon's local intent — the server does not yet enforce
	// it against a server-initiated update, so UI copy must say "recorded
	// intent," not "guaranteed not to be upgraded."
	RuntimePinnedVersion *string         `json:"runtime_pinned_version,omitempty"`
	RuntimeConfig        any             `json:"runtime_config"`
	CustomArgs           []string        `json:"custom_args"`
	McpConfig            json.RawMessage `json:"mcp_config"`
	// custom_env is intentionally NOT serialized on agent resources. The
	// agent_list/get/create/update/archive/restore responses and WS events
	// only expose coarse metadata (has_custom_env, custom_env_key_count) so
	// the UI can show "N variables configured" without dragging secrets
	// across the API surface. Reading values requires the dedicated, audited
	// `GET /api/agents/{id}/env` endpoint; writing requires `PUT` to the
	// same path. agent-actor tokens are denied there. See MUL-2600.
	HasCustomEnv      bool   `json:"has_custom_env"`
	CustomEnvKeyCount int    `json:"custom_env_key_count"`
	McpConfigRedacted bool   `json:"mcp_config_redacted"`
	Status            string `json:"status"`
	// ManagedRole is reserved for independent platform-managed agent classes
	// such as research_fleet. Channel manager identity comes exclusively from
	// channel_member.role.
	ManagedRole        string `json:"managed_role,omitempty"`
	WorkspaceRole      string `json:"workspace_role"`
	MaxConcurrentTasks int32  `json:"max_concurrent_tasks"`
	Model              string `json:"model"`
	// ThinkingLevel is the runtime-native reasoning/effort token persisted
	// for this agent (empty = use runtime default). The picker is per-runtime
	// per-model; the API never normalizes across providers. See MUL-2339.
	ThinkingLevel string              `json:"thinking_level"`
	OwnerID       *string             `json:"owner_id"`
	Skills        []AgentSkillSummary `json:"skills"`
	CreatedAt     string              `json:"created_at"`
	UpdatedAt     string              `json:"updated_at"`
	ArchivedAt    *string             `json:"archived_at"`
	ArchivedBy    *string             `json:"archived_by"`
	// HonorLevel is batch-projected on ListAgents for compact identity surfaces.
	// Other agent endpoints may omit it; the dedicated honor endpoint remains
	// the source for the complete dashboard.
	HonorLevel int `json:"honor_level,omitempty"`
	// Memory growth tier/progress for profile & agent card (LRM-303). Null when
	// the agent has zero valid Phase① memory writes.
	MemoryGrowth *AgentMemoryGrowthResponse `json:"memory_growth,omitempty"`
}

func agentToResponse(a db.Agent) AgentResponse {
	var rc any
	if a.RuntimeConfig != nil {
		json.Unmarshal(a.RuntimeConfig, &rc)
	}
	if rc == nil {
		rc = map[string]any{}
	}

	// Compute env metadata WITHOUT exposing the values. We unmarshal here
	// only to count keys; the map never reaches the response. A coarse
	// has_custom_env / key_count is what the UI gets — to read the values
	// the caller must hit GET /api/agents/{id}/env (owner/admin only,
	// audited).
	envKeyCount := 0
	if a.CustomEnv != nil {
		var customEnv map[string]string
		if err := json.Unmarshal(a.CustomEnv, &customEnv); err != nil {
			slog.Warn("failed to unmarshal agent custom_env", "agent_id", uuidToString(a.ID), "error", err)
		}
		envKeyCount = len(customEnv)
	}

	var customArgs []string
	if a.CustomArgs != nil {
		if err := json.Unmarshal(a.CustomArgs, &customArgs); err != nil {
			slog.Warn("failed to unmarshal agent custom_args", "agent_id", uuidToString(a.ID), "error", err)
		}
	}
	if customArgs == nil {
		customArgs = []string{}
	}

	var mcpConfig json.RawMessage
	if a.McpConfig != nil {
		mcpConfig = json.RawMessage(a.McpConfig)
	}

	managedRole := ""
	if a.ManagedRole.Valid {
		managedRole = a.ManagedRole.String
	}
	return AgentResponse{
		ID:                 uuidToString(a.ID),
		WorkspaceID:        uuidToString(a.WorkspaceID),
		RuntimeID:          uuidToString(a.RuntimeID),
		Name:               a.Name,
		DisplayName:        agentDisplayName(a),
		Description:        a.Description,
		Instructions:       a.Instructions,
		AvatarURL:          textToPtr(a.AvatarUrl),
		AvatarSource:       a.AvatarSource,
		RuntimeMode:        a.RuntimeMode,
		RuntimeName:        defaultAgentRuntimeName(a.RuntimeMode),
		RuntimeConfig:      rc,
		CustomArgs:         customArgs,
		McpConfig:          mcpConfig,
		HasCustomEnv:       envKeyCount > 0,
		CustomEnvKeyCount:  envKeyCount,
		Status:             a.Status,
		WorkspaceRole:      a.WorkspaceRole,
		MaxConcurrentTasks: a.MaxConcurrentTasks,
		Model:              a.Model.String,
		ThinkingLevel:      a.ThinkingLevel.String,
		OwnerID:            uuidToPtr(a.OwnerID),
		ManagedRole:        managedRole,
		Skills:             []AgentSkillSummary{},
		CreatedAt:          timestampToString(a.CreatedAt),
		UpdatedAt:          timestampToString(a.UpdatedAt),
		ArchivedAt:         timestampToPtr(a.ArchivedAt),
		ArchivedBy:         uuidToPtr(a.ArchivedBy),
	}
}

func defaultAgentRuntimeName(runtimeMode string) string {
	if runtimeMode == "cloud" {
		return "Cloud"
	}
	return ""
}

func (h *Handler) attachAgentRuntimeName(ctx context.Context, resp *AgentResponse) {
	resps := []AgentResponse{*resp}
	h.attachAgentRuntimeNames(ctx, resps)
	*resp = resps[0]
}

func (h *Handler) attachAgentRuntimeNames(ctx context.Context, resps []AgentResponse) {
	if len(resps) == 0 {
		return
	}
	runtimeIDs := make([]pgtype.UUID, 0, len(resps))
	byRuntimeID := map[string][]int{}
	for i := range resps {
		if strings.TrimSpace(resps[i].RuntimeName) == "" {
			resps[i].RuntimeName = defaultAgentRuntimeName(resps[i].RuntimeMode)
		}
		if strings.TrimSpace(resps[i].RuntimeID) == "" {
			continue
		}
		runtimeID := parseUUID(resps[i].RuntimeID)
		if !runtimeID.Valid {
			continue
		}
		key := uuidToString(runtimeID)
		if _, ok := byRuntimeID[key]; !ok {
			runtimeIDs = append(runtimeIDs, runtimeID)
		}
		byRuntimeID[key] = append(byRuntimeID[key], i)
	}
	if len(runtimeIDs) == 0 {
		return
	}

	// Per-agent crash fact (agent.crashed_since). Must be loaded here — the
	// same place that computes RuntimeDisplayStatus — or GET /agents will
	// never show "crashed" even when the column is set (Parker's 2026-08-02
	// attachAgentRuntimeNames lesson from #1801/#1802: pure-function green
	// ≠ user-facing green). Narrow query avoids regenerating every SELECT *
	// FROM agent while make sqlc is broken (task #83).
	//
	// Load BEFORE the runtime Query below: holding an open rows cursor while
	// acquiring another pool connection deadlocks under concurrent CreateAgent
	// (CI: TestAgentAvatar_ConcurrentCreatesAndDirectInsertsAreComplete).
	agentIDs := make([]pgtype.UUID, 0, len(resps))
	for i := range resps {
		if id := parseUUID(resps[i].ID); id.Valid {
			agentIDs = append(agentIDs, id)
		}
	}
	crashedByAgent := map[string]pgtype.Timestamptz{}
	providerBlockByAgent := map[string]db.ListAgentProviderBlockByIDsRow{}
	if len(agentIDs) > 0 && h.Queries != nil {
		if crashRows, err := h.Queries.ListAgentCrashedSinceByIDs(ctx, agentIDs); err != nil {
			slog.Warn("failed to load agent crashed_since", "error", err)
		} else {
			for _, row := range crashRows {
				crashedByAgent[uuidToString(row.ID)] = row.CrashedSince
			}
		}
		if blockRows, err := h.Queries.ListAgentProviderBlockByIDs(ctx, agentIDs); err != nil {
			slog.Warn("failed to load agent provider block", "error", err)
		} else {
			for _, row := range blockRows {
				providerBlockByAgent[uuidToString(row.ID)] = row
			}
		}
	}

	// task #84: ListAgentRuntimeConnectivityByIDs uses sqlc.embed(agent_runtime)
	// instead of a hand-listed column SELECT, so a future agent_runtime column
	// reaches this response the moment its migration lands and the query is
	// regenerated — no Go changes here. See the query's doc comment
	// (pkg/db/queries/runtime.sql) for the three "done but unreachable"
	// incidents (#1801/#1802/#81) this replaces.
	rows, err := h.Queries.ListAgentRuntimeConnectivityByIDs(ctx, runtimeIDs)
	if err != nil {
		slog.Warn("failed to load agent runtime names", "error", err)
		return
	}

	now := time.Now()
	for _, row := range rows {
		rt := row.AgentRuntime
		for _, idx := range byRuntimeID[uuidToString(rt.ID)] {
			resps[idx].RuntimeName = row.EffectiveName
			// Always project connectivity onto the agent so private-runtime
			// filtering on the runtimes list cannot blank live presence.
			resps[idx].RuntimeStatus = rt.Status
			resps[idx].RuntimeLastSeenAt = timestampToPtr(rt.LastSeenAt)
			block := providerBlockByAgent[resps[idx].ID]
			if block.ProviderBlockDetail != "" {
				resps[idx].ProviderBlockDetail = block.ProviderBlockDetail
				if block.ProviderBlockedUntil.Valid {
					resps[idx].ProviderBlockedUntil = timestampToPtr(block.ProviderBlockedUntil)
				}
			}
			resps[idx].RuntimeDisplayStatus = agentRuntimeDisplayStatus(
				resps[idx].Status, rt, crashedByAgent[resps[idx].ID],
				block.ProviderBlockDetail, block.ProviderBlockedUntil, now,
			)
			resps[idx].RuntimePinnedVersion = nullableTextPtr(rt.PinnedVersion)
		}
	}
}

type AgentTaskResponse struct {
	ID          string         `json:"id"`
	AgentID     string         `json:"agent_id"`
	ActorID     string         `json:"actor_id,omitempty"`
	ActorType   string         `json:"actor_type,omitempty"`
	DisplayName string         `json:"display_name,omitempty"`
	AvatarURL   *string        `json:"avatar_url,omitempty"`
	Handle      *string        `json:"handle,omitempty"`
	ActorStatus string         `json:"actor_status,omitempty"`
	Actor       *ActorIdentity `json:"actor,omitempty"`
	RuntimeID   string         `json:"runtime_id"`
	IssueID     string         `json:"issue_id"`
	WorkspaceID string         `json:"workspace_id"`
	// WorkspaceContext is the workspace-level system prompt set in workspace
	// settings (`workspace.context` DB column). Injected into the agent brief
	// as `## Workspace Context` so every agent running in this workspace —
	// regardless of issue / chat / autopilot / quick-create — sees the same
	// shared context. Empty when the workspace owner hasn't set it.
	WorkspaceContext string         `json:"workspace_context,omitempty"`
	ThreadName       string         `json:"thread_name,omitempty"` // semantic title for provider-native session/thread history
	Status           string         `json:"status"`
	Priority         int32          `json:"priority"`
	DispatchedAt     *string        `json:"dispatched_at"`
	StartedAt        *string        `json:"started_at"`
	CompletedAt      *string        `json:"completed_at"`
	Result           any            `json:"result"`
	Error            *string        `json:"error"`
	FailureReason    string         `json:"failure_reason,omitempty"` // see TaskService.MaybeRetryFailedTask
	Attempt          int32          `json:"attempt"`
	MaxAttempts      int32          `json:"max_attempts"`
	ParentTaskID     *string        `json:"parent_task_id,omitempty"`
	Agent            *TaskAgentData `json:"agent,omitempty"`
	// ExecutionConfig is the immutable runtime configuration captured when this
	// execution was created. It is distinct from the agent Profile defaults,
	// which only govern later work.
	ExecutionConfig *service.TaskExecutionConfig `json:"execution_config,omitempty"`
	// ArealProxy carries the AReaL RL proxy provider config extracted from the
	// task's context.areal_proxy at claim time (written by the session-open
	// hook, Task 5). When present the daemon launches the runtime against the
	// RL proxy — `pi -p --provider areal --model areal-default --api-key
	// <api_key>` with the proxy base_url — so the trained agent's LLM traffic
	// routes through the bridge and its trajectory is captured. Nil for the
	// overwhelming majority of (non-trained) tasks; omitempty so old daemons
	// ignore it. See §4.4.
	ArealProxy         *ArealProxyData `json:"areal_proxy,omitempty"`
	SharedWorkdirEnvID string          `json:"shared_workdir_env_id,omitempty"`
	ProjectID          string          `json:"project_id,omitempty"`   // issue's project, when present
	ChannelID          string          `json:"channel_id,omitempty"`   // exact DM/channel surface, when present
	ChannelKind        string          `json:"channel_kind,omitempty"` // "dm" | "group" when ChannelID is set; personal-memory entry gate
	// ScopedSecrets carries channel/project secrets for daemon injection after
	// scope filtering (LRM-953). Empty until a secret store populates them.
	ScopedSecrets  []ScopedSecretData `json:"scoped_secrets,omitempty"`
	ProjectTitle   string             `json:"project_title,omitempty"` // for surfacing in agent context
	CreatedAt      string             `json:"created_at"`
	PriorSessionID string             `json:"prior_session_id,omitempty"` // session ID from a previous task on same issue
	PriorWorkDir   string             `json:"prior_work_dir,omitempty"`   // work_dir from a previous task on same issue
	// RuntimeStateGeneration is the D6-1a wire contract from agent_runtime_state:
	// inbox claim ensures the row and fills generation only. FreshSessionNoticeReason
	// stays empty on the claim path until D6-2 completes canonical archive/read-switch
	// (shipping cutover/reset notice while PriorSessionID still resumes legacy chat/issue
	// sessions would inject a false "brand new / history archived" brief). omitempty
	// keeps older daemons happy.
	RuntimeStateGeneration   int64  `json:"runtime_state_generation,omitempty"`
	FreshSessionNoticeReason string `json:"fresh_session_notice_reason,omitempty"`
	WorkDir                  string `json:"work_dir,omitempty"` // local working directory pinned for this task; populated once the daemon reports it
	// RelativeWorkDir is a privacy-safe display form of WorkDir intended for
	// the UI. For current tasks it strips the daemon's workspace root so the
	// user sees `<workspaceUUID>/agents/<agentUUID>`; for legacy/external paths we strip
	// recognised home-directory prefixes (`/Users/<name>/`, `/home/<name>/`,
	// `<drive>:/Users/<name>/`) and otherwise fall back to the basename so
	// the field never carries the user's home dir or account name. Empty
	// when WorkDir is empty, or when stripping leaves nothing. See
	// relativeWorkDir() for the full rules. Older clients can still read
	// WorkDir directly; newer UIs should prefer RelativeWorkDir.
	RelativeWorkDir          string                             `json:"relative_work_dir,omitempty"`
	TriggerCommentID         *string                            `json:"trigger_comment_id,omitempty"`          // comment that triggered this task
	TriggerThreadID          string                             `json:"trigger_thread_id,omitempty"`           // root comment ID for the triggering thread
	TriggerCommentContent    string                             `json:"trigger_comment_content,omitempty"`     // content of the triggering comment
	TriggerSummary           *string                            `json:"trigger_summary,omitempty"`             // canonical short description snapshot — comment text / autopilot title — taken at task creation; survives source edits/deletes
	TriggerAuthorType        string                             `json:"trigger_author_type,omitempty"`         // "agent" or "member" — author kind of the triggering comment
	TriggerAuthorName        string                             `json:"trigger_author_name,omitempty"`         // display name of the triggering comment author
	NewCommentCount          int                                `json:"new_comment_count,omitempty"`           // issue-wide comments since this agent's last run; excludes injected trigger + own comments; omitempty so old daemons ignore it
	NewCommentsSince         string                             `json:"new_comments_since,omitempty"`          // RFC3339 anchor (last run's started_at) the count is measured from; omitempty so old daemons ignore it
	AssignmentSnapshot       *protocol.IssueAssignmentSnapshot  `json:"assignment_snapshot,omitempty"`         // assignment-time stable fields plus claim-time current status
	ChannelGoal              *protocol.ChannelGoalContext       `json:"channel_goal,omitempty"`                // active channel goal at claim time
	ChatSessionID            string                             `json:"chat_session_id,omitempty"`             // non-empty for chat tasks
	ChatMessage              string                             `json:"chat_message,omitempty"`                // user message for chat tasks
	ChatContextSummary       string                             `json:"chat_context_summary,omitempty"`        // compact surface-scoped context handoff when native resume is skipped
	ChatMessageAttachments   []ChatAttachmentMeta               `json:"chat_message_attachments,omitempty"`    // attachments on the user message — agent calls `multica attachment view --id <id> --output <path>` per entry
	AutopilotRunID           string                             `json:"autopilot_run_id,omitempty"`            // non-empty for autopilot-spawned tasks
	AutopilotID              string                             `json:"autopilot_id,omitempty"`                // autopilot that spawned this task
	AutopilotTitle           string                             `json:"autopilot_title,omitempty"`             // autopilot title used as task context
	AutopilotDescription     string                             `json:"autopilot_description,omitempty"`       // autopilot description used as task prompt
	AutopilotSource          string                             `json:"autopilot_source,omitempty"`            // manual, schedule, webhook, or api
	AutopilotTriggerPayload  json.RawMessage                    `json:"autopilot_trigger_payload,omitempty"`   // optional trigger payload for webhook/api runs
	QuickCreatePrompt        string                             `json:"quick_create_prompt,omitempty"`         // user's natural-language input for quick-create tasks
	QuickCreateAttachmentIDs []string                           `json:"quick_create_attachment_ids,omitempty"` // attachment ids uploaded in the quick-create prompt and bound on issue create
	QuickCreateSource        *protocol.QuickCreateSourceContext `json:"quick_create_source,omitempty"`         // bounded chat/thread source context for quick-create tasks
	AgentRadarPrompt         string                             `json:"agent_radar_prompt,omitempty"`          // full prompt for platform-scheduled proactive radar tasks
	ParentIssueID            string                             `json:"parent_issue_id,omitempty"`             // for quick-create tasks opened from "Add sub issue" — UUID of the parent issue the new issue should be filed under
	ParentIssueIdentifier    string                             `json:"parent_issue_identifier,omitempty"`     // human-readable identifier (e.g. MUL-123) of the quick-create parent issue, resolved on claim for prompt context
	// RequestingUserName + RequestingUserProfileDescription mirror the user
	// the agent is acting on behalf of (see daemon/types.go). v1 sources them
	// from the runtime owner so they're populated for daemon runtimes and
	// empty otherwise. The daemon emits both into the brief under
	// `## Requesting User`; the heading is skipped entirely when description
	// is empty.
	RequestingUserName               string `json:"requesting_user_name,omitempty"`
	RequestingUserProfileDescription string `json:"requesting_user_profile_description,omitempty"`
	// Initiator* identify the actor who triggered THIS task — the real
	// requester behind the current comment/mention or chat message — as
	// distinct from the runtime owner whose credentials the agent runs with.
	// Resolved at claim time: comment-triggered tasks use the triggering
	// comment's author; chat tasks use the chat session creator. Empty for
	// task kinds with no attributable human initiator (on-assign, autopilot,
	// quick-create). InitiatorEmail is set only for member initiators
	// ("member"); agent initiators ("agent") carry a name but no email. The
	// daemon emits these into the brief under `## Task Initiator` so a
	// workspace-visible, multi-user agent can attribute the request and apply
	// per-person privacy / access rules instead of seeing every requester as
	// the owner. The agent's effective Multica credentials stay owner-scoped —
	// this is an attested identity, not a credential. See MUL-2645.
	InitiatorType  string `json:"initiator_type,omitempty"`  // "member" or "agent"
	InitiatorID    string `json:"initiator_id,omitempty"`    // user UUID (member) or agent UUID
	InitiatorName  string `json:"initiator_name,omitempty"`  // display name of the initiator
	InitiatorEmail string `json:"initiator_email,omitempty"` // member email; empty for agent initiators
	Kind           string `json:"kind"`                      // discriminator: "comment" | "autopilot" | "chat" | "quick_create" | "direct" — used by the activity row to label tasks that have no linked issue
	// AuthToken is the `mat_` bearer the daemon writes into the per-run
	// MULTICA_TOKEN_FILE wrapper. Canonical inbox runs bind it to a single
	// delivery. Credential-transport-capable runs leave this empty so the daemon
	// provisions/reuses a durable agent credential locally. In all cases,
	// auth middleware treats requests authenticated with an agent bearer as
	// actor=agent and owner-only endpoints reject it.
	// Empty when the runtime has no owning user.
	AuthToken string `json:"auth_token,omitempty"`

	ReferencedEntities           []protocol.ReferencedEntitySnapshot `json:"referenced_entities,omitempty"`             // bounded, permission-filtered snapshots for canonical references in this turn
	ReferencedEntityOmittedCount int                                 `json:"referenced_entity_omitted_count,omitempty"` // syntactically valid references beyond the hydration cap

	// InboxEvent carries the canonical delivery lease. The daemon executes the
	// projected task payload and reports lifecycle state through inbox endpoints.
	InboxEvent *AgentInboxLeaseResponse `json:"inbox_event,omitempty"`
}

type agentTaskSortItem struct {
	Task   AgentTaskResponse
	SortAt time.Time
}

// ChatAttachmentMeta is the structured attachment metadata embedded in
// claim responses for chat tasks. The agent uses these to run
// `multica attachment view --id <id> --output <path>` rather than guessing from the
// markdown URL (which is signed and 30-min expiring on private CDN).
// The mirror struct on the daemon side lives in internal/daemon/types.go
// and uses the same JSON field names.
type ChatAttachmentMeta struct {
	ID          string `json:"id"`
	Filename    string `json:"filename"`
	ContentType string `json:"content_type,omitempty"`
}

// ScopedSecretData is a claim-time secret with a hard channel/project/agent
// scope for daemon injection filtering (LRM-953).
type ScopedSecretData struct {
	Key       string `json:"key"`
	Value     string `json:"value"`
	Scope     string `json:"scope,omitempty"` // agent | channel | project
	ChannelID string `json:"channel_id,omitempty"`
	ProjectID string `json:"project_id,omitempty"`
}

// TaskAgentData holds agent info included in claim responses so the daemon
// can set up the execution environment (branch naming, skill files, instructions).
type ManagerChannelData struct {
	ID   string `json:"id"`
	Name string `json:"name"`
}

type TaskAgentData struct {
	ID              string                    `json:"id"`
	Name            string                    `json:"name"`
	ManagedRole     string                    `json:"managed_role,omitempty"`
	ManagerChannels []ManagerChannelData      `json:"manager_channels,omitempty"`
	Instructions    string                    `json:"instructions"`
	Skills          []service.AgentSkillData  `json:"skills,omitempty"`
	Memories        []service.AgentMemoryData `json:"memories,omitempty"`
	CustomEnv       map[string]string         `json:"custom_env,omitempty"`
	CustomArgs      []string                  `json:"custom_args,omitempty"`
	McpConfig       json.RawMessage           `json:"mcp_config,omitempty"`
	Model           string                    `json:"model,omitempty"`
	ThinkingLevel   string                    `json:"thinking_level,omitempty"`
}

// ArealProxyData is the wire shape of the RL proxy provider config stored at
// context.areal_proxy on a trained task. The daemon consumes it at ExecOptions
// build (see internal/daemon.ArealProxy, kept in sync). SessionID is not needed
// for runtime launch (the close hook reads it from context directly) so it is
// intentionally omitted here.
type ArealProxyData struct {
	Provider string `json:"provider"`
	Model    string `json:"model"`
	APIKey   string `json:"api_key"`
	BaseURL  string `json:"base_url"`
}

// parseArealProxy extracts the areal_proxy provider config from a task's
// context JSONB. It returns nil for the common case of a non-trained task (no
// context / no areal_proxy key), for malformed JSON, and for an incomplete
// sub-object (missing api_key or base_url) — so a normal task is never
// accidentally routed through the proxy. Provider/Model are allowed to be empty
// here; the daemon defaults them to areal/areal-default.
func parseArealProxy(raw []byte) *ArealProxyData {
	if len(raw) == 0 {
		return nil
	}
	var envelope struct {
		ArealProxy *ArealProxyData `json:"areal_proxy"`
	}
	if err := json.Unmarshal(raw, &envelope); err != nil {
		return nil
	}
	p := envelope.ArealProxy
	if p == nil || p.APIKey == "" || p.BaseURL == "" {
		return nil
	}
	return p
}

// taskToResponse maps a queue row to its wire shape. workspaceID is threaded
// in because the row itself doesn't carry one (workspace lives on the agent
// / issue / chat session) — we ask the caller to resolve it once and pass it
// down. It populates WorkspaceID and powers the privacy-safe RelativeWorkDir
// derivation; pass "" only on daemon-facing paths that genuinely don't have
// it, in which case RelativeWorkDir falls back to the existing WorkDir.
func taskToResponse(t db.AgentInboxEvent, workspaceID string) AgentTaskResponse {
	var result any
	if t.Result != nil {
		json.Unmarshal(t.Result, &result)
	}
	failureReason := ""
	if t.FailureReason.Valid {
		failureReason = t.FailureReason.String
	}
	workDir := ""
	if t.WorkDir.Valid {
		workDir = t.WorkDir.String
	}
	status := t.Status
	switch t.Status {
	case "pending", "failed":
		status = "queued"
	case "draining":
		if t.StartedAt.Valid {
			status = "running"
		} else {
			status = "dispatched"
		}
	case "suppressed":
		status = "cancelled"
	case "acked":
		if t.TerminalOutcome.Valid {
			status = t.TerminalOutcome.String
		}
		if status != "failed" && status != "cancelled" {
			status = "completed"
		}
	}
	resp := AgentTaskResponse{
		ID:               uuidToString(t.ID),
		AgentID:          uuidToString(t.AgentID),
		RuntimeID:        uuidToString(t.RuntimeID),
		IssueID:          uuidToString(t.IssueID),
		WorkspaceID:      workspaceID,
		Status:           status,
		Priority:         t.Priority,
		DispatchedAt:     timestampToPtr(t.DispatchedAt),
		StartedAt:        timestampToPtr(t.StartedAt),
		CompletedAt:      timestampToPtr(t.CompletedAt),
		Result:           result,
		Error:            textToPtr(t.Error),
		FailureReason:    failureReason,
		Attempt:          t.Attempt,
		MaxAttempts:      t.MaxAttempts,
		ParentTaskID:     uuidToPtr(t.ParentTaskID),
		CreatedAt:        timestampToString(t.CreatedAt),
		TriggerCommentID: uuidToPtr(t.TriggerCommentID),
		TriggerSummary:   textToPtr(t.TriggerSummary),
		WorkDir:          workDir,
		RelativeWorkDir:  relativeWorkDir(workDir, workspaceID, uuidToString(t.AgentID)),
		// Surface task source so the UI can distinguish issue-linked tasks
		// from chat-spawned or autopilot-spawned ones; all three may arrive
		// with issue_id = "" once a task has no linked issue.
		ChannelID:      uuidToString(t.ChannelID),
		ChatSessionID:  uuidToString(t.ChatSessionID),
		AutopilotRunID: uuidToString(t.AutopilotRunID),
		Kind:           computeTaskKind(t),
	}
	if config, ok := service.TaskExecutionConfigFromContext(t.Context); ok {
		resp.ExecutionConfig = &config
	}
	// Trained-task RL proxy override (§4.4): surface context.areal_proxy on the
	// claim response so the daemon can route the runtime through the bridge.
	resp.ArealProxy = parseArealProxy(t.Context)
	// Shared_sandbox workdir anchor (research D5): surface
	// context.shared_workdir on the claim response so the daemon anchors the
	// run to the sample's single shared working directory.
	resp.SharedWorkdirEnvID = parseSharedWorkdirEnvID(t.Context)
	return resp
}

// parseSharedWorkdirEnvID extracts the shared_sandbox sample env id from a
// task's context JSONB. It returns "" for the common case of a non-shared
// task (no context / no shared_workdir key), for malformed JSON, and for an
// incomplete marker (missing env_id) — so a normal task keeps its per-agent
// workdir root.
func parseSharedWorkdirEnvID(raw []byte) string {
	if len(raw) == 0 {
		return ""
	}
	var envelope struct {
		SharedWorkdir *struct {
			EnvID string `json:"env_id"`
		} `json:"shared_workdir"`
	}
	if err := json.Unmarshal(raw, &envelope); err != nil {
		return ""
	}
	if envelope.SharedWorkdir == nil {
		return ""
	}
	return envelope.SharedWorkdir.EnvID
}

// relativeWorkDir produces a privacy-safe display form of the daemon-reported
// absolute work_dir. The contract: the returned string must never contain
// the user's home directory prefix or their account name. The chip is
// rendered in transcripts that frequently end up in screen shares,
// screenshots, and recordings, so this function is the only guard.
//
//   - For current tasks (work_dir laid out as `<workspacesRoot>/<wsUUID>/
//     agents/<agentUUID>`), it strips everything up to and including the
//     workspaces root, returning `<wsUUID>/agents/<agentUUID>`.
//   - For unexpected external paths, we try to recognise common
//     home-directory prefixes
//     (`/Users/<name>/`, `/home/<name>/`, `<drive>:/Users/<name>/`) and strip
//     them, returning the remainder (e.g. `repos/foo`). When the prefix
//     can't be recognised — unusual home layouts, network mounts, paths
//     under `/opt`, `/srv`, etc. — we fall back to the basename so we never
//     accidentally render a path component that happens to be a username.
//
// Returns empty when work_dir is empty, or when stripping leaves nothing
// (i.e. work_dir was exactly the user's home — rendering nothing is
// preferable to a chip that says `<name>`).
func relativeWorkDir(workDir, workspaceID, agentID string) string {
	if workDir == "" {
		return ""
	}
	// Normalize Windows separators so the rest of the function only
	// reasons about forward slashes.
	normalized := strings.ReplaceAll(workDir, "\\", "/")

	if workspaceID != "" && agentID != "" {
		envRootSuffix := agentworkspace.RootRelPath(workspaceID, agentID)
		if idx := strings.Index(normalized, envRootSuffix); idx >= 0 {
			return normalized[idx:]
		}
	}

	if stripped, ok := stripHomePrefix(normalized); ok {
		return stripped
	}

	return basename(normalized)
}

// homeDirPattern matches the well-known per-user home layouts on macOS,
// Linux, and Windows after backslash normalization:
//
//	/Users/<name>[/<rest>]
//	/home/<name>[/<rest>]
//	<drive>:/Users/<name>[/<rest>]
//
// Case-insensitive because macOS and Windows are case-insensitive at the
// filesystem layer; matching `/users/...` the same as `/Users/...` keeps
// the strip robust against unusual casings seen on shared drives.
// Capture group 1 is the optional remainder after the username segment.
var homeDirPattern = regexp.MustCompile(`(?i)^(?:[A-Za-z]:)?/(?:Users|home)/[^/]+(?:/(.*))?$`)

// stripHomePrefix recognises common home-directory layouts and returns
// the path remainder after the username segment. Returns (remainder, true)
// when a known home prefix matched. The remainder may be the empty string
// (work_dir was exactly the home directory) — the caller treats that as
// "nothing safe to display".
func stripHomePrefix(p string) (string, bool) {
	m := homeDirPattern.FindStringSubmatch(p)
	if m == nil {
		return "", false
	}
	return m[1], true
}

// basename returns the last non-empty segment of a forward-slash path.
// Used as the ultimate privacy-safe fallback when we can't otherwise
// recognise the path: a single segment can never expose the home prefix,
// and the leaf is the most useful safe context available.
func basename(p string) string {
	p = strings.TrimRight(p, "/")
	if p == "" {
		return ""
	}
	if idx := strings.LastIndex(p, "/"); idx >= 0 {
		return p[idx+1:]
	}
	return p
}

// computeTaskKind picks the source-discriminator string the activity UI uses
// to choose how to render a task row. Computed from the existing FK shape so
// no extra DB lookup is needed: chat / autopilot / comment-on-issue (any
// triggered task with both an issue_id and trigger_comment_id) / quick_create
// (no linked source — the agent is creating the issue itself) / direct
// (assignee-driven task on an existing issue).
func computeTaskKind(t db.AgentInboxEvent) string {
	if uuidToString(t.ChatSessionID) != "" {
		return "chat"
	}
	// LRM-1079: channel-only wakes have no chat_session_id but still present as
	// chat work for presence / activity labeling.
	if uuidToString(t.ChannelID) != "" && uuidToString(t.IssueID) == "" {
		return "chat"
	}
	if uuidToString(t.AutopilotRunID) != "" {
		return "autopilot"
	}
	if uuidToString(t.IssueID) == "" {
		return "quick_create"
	}
	if uuidToString(t.TriggerCommentID) != "" {
		return "comment"
	}
	return "direct"
}

func (h *Handler) ListAgents(w http.ResponseWriter, r *http.Request) {
	workspaceID := h.resolveWorkspaceID(r)
	member, ok := h.workspaceMember(w, r, workspaceID)
	if !ok {
		return
	}
	userID := requestUserID(r)

	var agents []db.Agent
	var err error
	if r.URL.Query().Get("include_archived") == "true" {
		agents, err = h.Queries.ListAllAgents(r.Context(), parseUUID(workspaceID))
	} else {
		agents, err = h.Queries.ListAgents(r.Context(), parseUUID(workspaceID))
	}
	if err != nil {
		writeError(w, http.StatusInternalServerError, "failed to list agents")
		return
	}

	// Batch-load skills for all agents to avoid N+1.
	skillRows, err := h.Queries.ListAgentSkillsByWorkspace(r.Context(), parseUUID(workspaceID))
	if err != nil {
		writeError(w, http.StatusInternalServerError, "failed to load agent skills")
		return
	}
	skillMap := map[string][]AgentSkillSummary{}
	for _, row := range skillRows {
		agentID := uuidToString(row.AgentID)
		skillMap[agentID] = append(skillMap[agentID], AgentSkillSummary{
			ID:          uuidToString(row.ID),
			Name:        row.Name,
			Description: row.Description,
		})
	}

	honorLevels, err := h.Queries.ListAgentHonorLevelsByWorkspace(
		r.Context(),
		parseUUID(workspaceID),
	)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "failed to load agent honor levels")
		return
	}
	honorLevelByAgentID := make(map[string]int, len(honorLevels))
	for _, row := range honorLevels {
		honorLevelByAgentID[uuidToString(row.AgentID)] = int(row.Level)
	}

	// Batch-load research-fleet membership to avoid N+1 (task #903: the
	// research_fleet_member table is the single source of truth for "is
	// this agent a research-fleet member" — agent.managed_role is retired).
	fleetMemberIDs, err := h.Queries.ListActiveResearchFleetMemberAgentIDsByWorkspace(r.Context(), parseUUID(workspaceID))
	if err != nil {
		writeError(w, http.StatusInternalServerError, "failed to load research fleet membership")
		return
	}
	researchFleetAgentIDs := make(map[string]bool, len(fleetMemberIDs))
	for _, id := range fleetMemberIDs {
		researchFleetAgentIDs[uuidToString(id)] = true
	}

	// mcp_config still uses the workspace-level always-redact setting and
	// the per-row owner/admin gate — secrets in MCP server configs follow
	// the same exposure rules as custom_env used to. custom_env itself is
	// never serialized on agent resources anymore (MUL-2600); see the
	// AgentResponse comment.
	ws, err := h.Queries.GetWorkspace(r.Context(), parseUUID(workspaceID))
	if err != nil {
		slog.Warn("GetWorkspace failed for redact check", "workspace_id", workspaceID, "error", err)
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "internal error"})
		return
	}
	alwaysRedact := workspaceAlwaysRedactSecrets(ws.Settings)

	// Resolve the request actor once — used below to redact secrets/internal
	// fields per row. Every agent in the workspace is listable by every
	// member (task #908: agent existence/listing is no longer visibility-gated).
	actorType, actorID := h.resolveActor(r, userID, workspaceID)
	// Research Fleet agents are infrastructure and stay out of the workspace
	// agent directory / issue assignee picker.
	visible := make([]AgentResponse, 0, len(agents))
	for _, a := range agents {
		if researchFleetAgentIDs[uuidToString(a.ID)] {
			continue
		}
		resp := agentToResponse(a)
		resp.HonorLevel = honorLevelByAgentID[resp.ID]
		if skills, ok := skillMap[resp.ID]; ok {
			resp.Skills = skills
		}
		// Agent actors NEVER see mcp_config secrets, even when their host's
		// PAT would normally satisfy the owner/admin role gate. Otherwise an
		// agent running under an owner's daemon could read other agents'
		// MCP configs (which routinely embed third-party API tokens) — the
		// same lateral-movement vector MUL-2600 closed for custom_env.
		if actorType == "agent" || alwaysRedact || !canViewAgentSecrets(a, userID, member.Role) {
			redactMcpConfig(&resp)
		}
		if !h.canAccessAgentInternals(r.Context(), a, actorType, actorID, workspaceID) {
			redactAgentInternals(&resp)
		}
		visible = append(visible, resp)
	}
	h.attachAgentRuntimeNames(r.Context(), visible)

	writeJSON(w, http.StatusOK, visible)
}

func (h *Handler) GetAgent(w http.ResponseWriter, r *http.Request) {
	id := chi.URLParam(r, "id")
	agent, ok := h.loadAgentForUser(w, r, id)
	if !ok {
		return
	}
	// GetAgent is unconditional (task #908: every agent is navigable/visible
	// by every workspace member) — the 403 that used to gate this whole
	// endpoint is gone. What still gates is the internal-construction fields
	// below (Instructions/RuntimeConfig/CustomArgs/MemoryGrowth), via
	// canAccessAgentInternals.
	workspaceID := uuidToString(agent.WorkspaceID)
	actorType, actorID := h.resolveActor(r, requestUserID(r), workspaceID)
	hasInternalsAccess := h.canAccessAgentInternals(r.Context(), agent, actorType, actorID, workspaceID)
	resp := agentToResponse(agent)
	// Use the summary query (no `content` column) — the embedded
	// AgentSkillSummary only needs id/name/description, and reading large
	// SKILL.md bodies just to discard them is the exact regression we fixed
	// in #2174.
	if err := h.attachAgentSkills(r.Context(), &resp, agent.ID); err != nil {
		writeError(w, http.StatusInternalServerError, "failed to load agent skills")
		return
	}
	h.attachAgentRuntimeName(r.Context(), &resp)

	if hasInternalsAccess {
		if growth, err := h.loadAgentMemoryGrowth(r.Context(), agent.ID); err != nil {
			writeError(w, http.StatusInternalServerError, "failed to load agent memory growth")
			return
		} else {
			resp.MemoryGrowth = growth
		}
	}

	// mcp_config redaction (custom_env was removed from this response shape
	// in MUL-2600; secrets are now fetched via GET /api/agents/{id}/env).
	userID := requestUserID(r)
	ws, err := h.Queries.GetWorkspace(r.Context(), agent.WorkspaceID)
	if err != nil {
		slog.Warn("GetWorkspace failed for redact check", "workspace_id", uuidToString(agent.WorkspaceID), "error", err)
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "internal error"})
		return
	}
	alwaysRedact := workspaceAlwaysRedactSecrets(ws.Settings)
	if !hasInternalsAccess {
		redactAgentInternals(&resp)
	}
	// Agent actors NEVER see mcp_config (see ListAgents for the rationale).
	if actorType == "agent" || alwaysRedact {
		redactMcpConfig(&resp)
	} else if member, ok := ctxMember(r.Context()); ok {
		if !canViewAgentSecrets(agent, userID, member.Role) {
			redactMcpConfig(&resp)
		}
	}

	writeJSON(w, http.StatusOK, resp)
}

type CreateAgentRequest struct {
	// Username is an explicit, stable handle chosen by the caller. When it is
	// omitted, the server generates the username from display_name and applies
	// numeric collision suffixes.
	Username           *string               `json:"username"`
	DisplayName        string                `json:"display_name"`
	Description        string                `json:"description"`
	Instructions       string                `json:"instructions"`
	AvatarSelection    *AgentAvatarSelection `json:"avatar_selection"`
	RuntimeID          string                `json:"runtime_id"`
	RuntimeConfig      any                   `json:"runtime_config"`
	CustomEnv          map[string]string     `json:"custom_env"`
	CustomArgs         []string              `json:"custom_args"`
	McpConfig          json.RawMessage       `json:"mcp_config"`
	MaxConcurrentTasks int32                 `json:"max_concurrent_tasks"`
	Model              string                `json:"model"`
	ThinkingLevel      string                `json:"thinking_level"`
	InitialNotes       map[string]string     `json:"initial_notes"`
	InitialMemory      map[string]string     `json:"initial_memory"`
	// Template records which template slug was used to seed this agent
	// (e.g. "coding" / "planning" / "writing" / "assistant"). Empty when
	// the caller didn't come from a template picker — the `agent_created`
	// event still fires with `template=""`, which is the correct signal
	// for "manually authored agent".
	Template string `json:"template"`
}

func decodeJSONBodyWithRawFields(body io.Reader, dst any) (map[string]json.RawMessage, error) {
	payload, err := io.ReadAll(body)
	if err != nil {
		return nil, err
	}

	if err := json.Unmarshal(payload, dst); err != nil {
		return nil, err
	}

	var raw map[string]json.RawMessage
	if err := json.Unmarshal(payload, &raw); err != nil {
		return nil, err
	}
	if raw == nil {
		raw = map[string]json.RawMessage{}
	}

	return raw, nil
}

func (h *Handler) CreateAgent(w http.ResponseWriter, r *http.Request) {
	workspaceID := h.resolveWorkspaceID(r)

	var req CreateAgentRequest
	rawFields, err := decodeJSONBodyWithRawFields(r.Body, &req)
	if err != nil {
		writeError(w, http.StatusBadRequest, "invalid request body")
		return
	}
	if _, ok := rawFields["name"]; ok {
		writeError(w, http.StatusBadRequest, "name is no longer accepted; use display_name")
		return
	}
	if _, ok := rawFields["avatar_url"]; ok {
		writeError(w, http.StatusBadRequest, "avatar_url is no longer accepted; use avatar_selection")
		return
	}

	ownerID, ok := requireUserID(w, r)
	if !ok {
		return
	}
	// LRM-2343: agent creation (both Proposal commit and bare manual create)
	// is gated behind the unified `manageAgents` capability (workspace
	// owner/admin, human only). UI enable/disable is never the only defense.
	if _, ok := h.requireManageAgents(w, r, workspaceID, "workspace not found"); !ok {
		return
	}

	displayName := strings.TrimSpace(req.DisplayName)
	if displayName == "" && req.Username == nil {
		writeError(w, http.StatusBadRequest, "display_name is required")
		return
	}
	if utf8.RuneCountInString(req.Description) > maxAgentDescriptionLength {
		writeError(w, http.StatusBadRequest, fmt.Sprintf("description must be %d characters or fewer", maxAgentDescriptionLength))
		return
	}
	if req.RuntimeID == "" {
		writeError(w, http.StatusBadRequest, "runtime_id is required")
		return
	}
	if req.MaxConcurrentTasks == 0 {
		req.MaxConcurrentTasks = 6
	}

	runtimeUUID, ok := parseUUIDOrBadRequest(w, req.RuntimeID, "runtime_id")
	if !ok {
		return
	}
	wsUUID, ok := parseUUIDOrBadRequest(w, workspaceID, "workspace id")
	if !ok {
		return
	}

	runtime, err := h.Queries.GetAgentRuntimeForWorkspace(r.Context(), db.GetAgentRuntimeForWorkspaceParams{
		ID:          runtimeUUID,
		WorkspaceID: wsUUID,
	})
	if err != nil {
		writeError(w, http.StatusBadRequest, "invalid runtime_id")
		return
	}

	if _, ok := h.workspaceMember(w, r, workspaceID); !ok {
		return
	}

	// thinking_level validation: provider-level enum only. Per-model gaps
	// are enforced by the daemon at execution time (MUL-2339, Trump's
	// review note — keep API behaviour consistent: literal-invalid →
	// always 400; combination-invalid → daemon-side task error).
	if !agent.IsKnownThinkingValue(runtime.Provider, req.ThinkingLevel) {
		writeError(w, http.StatusBadRequest, fmt.Sprintf("thinking_level %q is not a recognised value for runtime %q", req.ThinkingLevel, runtime.Provider))
		return
	}

	// Probe workspace agent count BEFORE the insert so the funnel has a
	// clean "first agent ever in this workspace" signal — Step 4 of
	// onboarding always lands in this branch. A non-fatal read: if the
	// list fails we fall through with isFirstAgent=false rather than
	// blocking creation, since the primary DB operation is the insert.
	isFirstAgent := false
	if existing, listErr := h.Queries.ListAgents(r.Context(), wsUUID); listErr == nil {
		isFirstAgent = len(existing) == 0
	}

	rc, _ := json.Marshal(req.RuntimeConfig)
	if req.RuntimeConfig == nil {
		rc = []byte("{}")
	}

	ce, _ := json.Marshal(req.CustomEnv)
	if req.CustomEnv == nil {
		ce = []byte("{}")
	}

	ca, _ := json.Marshal(req.CustomArgs)
	if req.CustomArgs == nil {
		ca = []byte("[]")
	}

	var mc []byte
	if rawMcpConfig, ok := rawFields["mcp_config"]; ok && !bytes.Equal(bytes.TrimSpace(rawMcpConfig), []byte("null")) {
		mc = append([]byte(nil), rawMcpConfig...)
	}

	// Hire hard-cut: agent:create cards use action_card_id (no draft_id bridge).
	actionCardID, hasActionCardID, err := extractActionCardID(rawFields)
	if err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	if hasActionCardID {
		card, code := h.loadActionCard(r, workspaceID, actionCardID)
		switch code {
		case agentActionLookupOK:
			if card.Status != agentActionStatusPrepared {
				writeCodedError(w, http.StatusConflict, agentActionLookupNotPrepared, "action card is not prepared")
				return
			}
			if card.ActionType != agentActionTypeCreate {
				writeError(w, http.StatusBadRequest, "action_card_id is not an agent:create card")
				return
			}
		case agentActionLookupNotFound:
			writeCodedError(w, http.StatusNotFound, agentActionLookupNotFound, "action card not found")
			return
		default:
			writeCodedError(w, http.StatusNotFound, agentActionLookupNotFound, "action card not found")
			return
		}
	}
	// LRM-2343 S2: canonical Message-backed commit seam. The agent:create
	// proposal lives on a channel_message; CreateAgent carries its id and the
	// whole commit (CAS prepared->executed + Agent + snapshots) is one tx.
	actionMessageID, hasActionMessageID, err := extractActionMessageID(rawFields)
	if err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	if hasActionMessageID && hasActionCardID {
		writeError(w, http.StatusBadRequest, "action_message_id and action_card_id are mutually exclusive")
		return
	}
	// Research / legacy seed only: draft_id still loads initial notes/memory when
	// present. Agent hire must not use drafts (agent draft create is 410).
	draftID, hasDraftID, err := extractDraftID(rawFields)
	if err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	if hasDraftID && hasActionCardID {
		writeError(w, http.StatusBadRequest, "action_card_id and draft_id are mutually exclusive")
		return
	}
	if hasDraftID && hasActionMessageID {
		writeError(w, http.StatusBadRequest, "action_message_id and draft_id are mutually exclusive")
		return
	}
	initialNotes := cleanInitialContextMap(req.InitialNotes, allowedInitialNoteSeedPath)
	initialMemory := cleanInitialContextMap(req.InitialMemory, allowedInitialMemorySeedPath)
	var draftAvatar resolvedAgentAvatar
	if hasDraftID {
		draftSeed, draftCode := h.loadAgentDraftForCreate(r, workspaceID, draftID)
		switch draftCode {
		case agentDraftLookupOK:
			// continue
		case agentDraftLookupAlreadyUsed:
			writeCodedError(w, http.StatusConflict, agentDraftLookupAlreadyUsed, "agent draft already used; reopen the hiring card to create a new draft")
			return
		default:
			writeCodedError(w, http.StatusNotFound, agentDraftLookupNotFound, "agent draft not found")
			return
		}
		if len(draftSeed.InitialNotes) > 0 || len(draftSeed.InitialMemory) > 0 {
			initialNotes = draftSeed.InitialNotes
			initialMemory = draftSeed.InitialMemory
		}
		if draftSeed.AvatarURL.Valid {
			draftAvatar = assignedAgentAvatar(draftSeed.AvatarURL.String)
		}
	}
	avatar, ok := h.resolveAgentAvatarSelection(w, r, wsUUID, ownerID, pgtype.UUID{}, req.AvatarSelection)
	if !ok {
		return
	}
	if !avatar.Set && draftAvatar.Set {
		avatar = draftAvatar
	}

	createParams := db.CreateAgentParams{
		WorkspaceID:        wsUUID,
		Description:        req.Description,
		Instructions:       req.Instructions,
		RuntimeMode:        runtime.RuntimeMode,
		RuntimeConfig:      rc,
		RuntimeID:          runtime.ID,
		MaxConcurrentTasks: req.MaxConcurrentTasks,
		OwnerID:            parseUUID(ownerID),
		CustomEnv:          ce,
		CustomArgs:         ca,
		McpConfig:          mc,
		Model:              pgtype.Text{String: strings.TrimSpace(req.Model), Valid: strings.TrimSpace(req.Model) != ""},
		ThinkingLevel:      pgtype.Text{String: req.ThinkingLevel, Valid: req.ThinkingLevel != ""},
	}
	if err := service.RequireAgentModel(createParams.Model.String); err != nil {
		writeError(w, http.StatusBadRequest, "model is required")
		return
	}
	applyCreateAgentAvatar(&createParams, avatar)

	var created db.Agent
	createdViaActionMessage := false
	if hasActionMessageID {
		// LRM-2343 S2: commit the prepared proposal Message atomically. This path
		// creates the Agent, adds the system #general membership and CAS's the
		// action prepared->executed all in one transaction, with action_message_id
		// + final-payload-hash idempotency (same replay -> same Agent; different
		// -> 409). It returns early here; the shared post-commit side effects
		// (skill suggestions, reconcile, events, reminder) run below.
		created, err = h.createAgentFromActionMessage(r.Context(), wsUUID, parseUUID(ownerID), actionMessageID, createParams, displayName)
		if err != nil {
			var cErr *codedActionCommitError
			if errors.As(err, &cErr) {
				writeCodedError(w, cErr.status, cErr.code, cErr.msg)
			} else {
				writeError(w, http.StatusInternalServerError, "failed to create agent from proposal: "+err.Error())
			}
			return
		}
		createdViaActionMessage = true
	} else if req.Username != nil {
		if err := validateIdentityHandle(*req.Username); err != nil {
			writeError(w, http.StatusBadRequest, "username must be 1-32 lowercase letters, digits, or hyphens")
			return
		}
		createParams.Name = *req.Username
		createParams.DisplayName = firstNonEmpty(displayName, *req.Username)
		created, err = h.Queries.CreateAgent(r.Context(), createParams)
		if identityUniqueViolation(err, "agent_workspace_name_unique") {
			writeError(w, http.StatusConflict, "username is already in use")
			return
		}
	} else {
		created, err = h.createAgentWithIdentity(r.Context(), h.Queries, createParams, displayName, displayName)
	}
	if err != nil {
		if errors.Is(err, errIdentityHandleInvalid) {
			writeError(w, http.StatusBadRequest, "username must be 1-32 lowercase letters, digits, or hyphens")
			return
		}
		if identityUniqueViolation(err, "agent_avatar_attachment_unique") {
			writeError(w, http.StatusConflict, "avatar attachment is already bound")
			return
		}
		slog.Warn("create agent failed", append(logger.RequestAttrs(r), "error", err, "workspace_id", workspaceID)...)
		writeError(w, http.StatusInternalServerError, "failed to create agent: "+err.Error())
		return
	}
	slog.Info("agent created", append(logger.RequestAttrs(r), "agent_id", uuidToString(created.ID), "name", created.Name, "workspace_id", workspaceID)...)
	if !createdViaActionMessage && hasActionCardID {
		code, markErr := h.markActionCardDone(r, workspaceID, actionCardID, parseUUID(ownerID), created.ID)
		if markErr != nil {
			slog.Warn("mark action card done failed", append(logger.RequestAttrs(r), "error", markErr, "action_card_id", uuidToString(actionCardID))...)
		} else if code == agentActionLookupNotPrepared {
			// Card raced to non-prepared; agent row already created — report conflict.
			writeCodedError(w, http.StatusConflict, agentActionLookupNotPrepared, "action card is not prepared")
			return
		} else if code == agentActionLookupNotFound {
			writeCodedError(w, http.StatusNotFound, agentActionLookupNotFound, "action card not found")
			return
		}
	}
	if hasDraftID {
		h.MarkAgentDraftUsed(r, workspaceID, ownerID, draftID, created.ID)
	}
	if len(initialNotes) > 0 || len(initialMemory) > 0 {
		h.seedAgentInitialContext(r, created, initialNotes, initialMemory)
	}
	h.refreshAgentSkillSuggestions(r.Context(), created)

	if runtime.Status == "online" {
		h.TaskService.ReconcileAgentStatus(r.Context(), created.ID)
		created, _ = h.Queries.GetAgent(r.Context(), created.ID)
	}

	resp := agentToResponse(created)
	h.attachAgentRuntimeName(r.Context(), &resp)
	actorType, actorID := h.resolveActor(r, ownerID, workspaceID)
	h.publishAgentVisibilityEvent(protocol.EventAgentCreated, workspaceID, actorType, actorID, created, map[string]any{"agent": broadcastAgentResponse(resp)})
	if created.RuntimeID.Valid {
		h.projectReminderOwnerStart(r.Context(), uuidToString(created.ID), uuidToString(created.RuntimeID))
	}

	obsmetrics.RecordEvent(h.Analytics, h.Metrics, analytics.AgentCreated(
		ownerID,
		workspaceID,
		uuidToString(created.ID),
		runtime.Provider,
		runtime.RuntimeMode,
		req.Template,
		isFirstAgent,
	))

	redactAgentResponseForActor(&resp, actorType)
	writeJSON(w, http.StatusCreated, resp)
}

func (h *Handler) seedAgentInitialContext(r *http.Request, agent db.Agent, initialNotes, initialMemory map[string]string) {
	if h == nil || h.DaemonHub == nil || !agent.RuntimeID.Valid {
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), agentFileRPCTimeout)
	defer cancel()
	resp, err := h.DaemonHub.RequestSeedAgentContext(ctx, protocol.SeedAgentContextRequestPayload{
		RequestID:     randomID(),
		RuntimeID:     uuidToString(agent.RuntimeID),
		RelPath:       agentRootRelPath(agent),
		InitialNotes:  initialNotes,
		InitialMemory: initialMemory,
		MaxBytes:      256 * 1024,
	})
	if err != nil {
		slog.Warn("seed agent initial context failed", append(logger.RequestAttrs(r), "error", err, "agent_id", uuidToString(agent.ID))...)
		return
	}
	if resp.Error != "" || resp.TooLarge {
		slog.Warn("seed agent initial context rejected", append(logger.RequestAttrs(r), "error", resp.Error, "too_large", resp.TooLarge, "agent_id", uuidToString(agent.ID))...)
	}
}

type UpdateAgentRequest struct {
	Username        *string               `json:"username"`
	DisplayName     *string               `json:"display_name"`
	Description     *string               `json:"description"`
	Instructions    *string               `json:"instructions"`
	AvatarSelection *AgentAvatarSelection `json:"avatar_selection"`
	RuntimeID       *string               `json:"runtime_id"`
	RuntimeConfig   any                   `json:"runtime_config"`
	// custom_env is intentionally NOT updatable through this endpoint.
	// Use `PUT /api/agents/{id}/env` for env changes — that path is
	// owner/admin-only, denies agent actors, and writes a persisted
	// audit log entry. A `PUT /api/agents/{id}` body that carries
	// `custom_env` is rejected with 400 in the handler below so a
	// caller never believes they rotated a secret when the value is
	// actually unchanged, and so a client that round-tripped a
	// previously-returned masked map cannot silently overwrite real
	// secret values with literal `****`. See MUL-2600.
	CustomArgs         *[]string        `json:"custom_args"`
	McpConfig          *json.RawMessage `json:"mcp_config"`
	Status             *string          `json:"status"`
	MaxConcurrentTasks *int32           `json:"max_concurrent_tasks"`
	Model              *string          `json:"model"`
	// ThinkingLevel is treated as a tri-state per-MUL-2339:
	//   - field omitted → no change (leave existing value alone)
	//   - field present with "" → explicit clear (use runtime default)
	//   - field present with non-empty value → set (validated server-side)
	// Distinguishing those modes is why this is a pointer; the raw-fields
	// map captured at decode time tells us whether the key was sent.
	ThinkingLevel *string `json:"thinking_level"`
	// ModelCatalogRequestID binds a Profile execution-config save to the
	// completed runtime model discovery that populated its picker. It is not
	// persisted on the agent: it is proof for this mutation that the selected
	// model/reasoning combination is actually advertised by the target runtime.
	// Legacy callers that do not send it retain provider-level validation while
	// they migrate to the Profile contract.
	ModelCatalogRequestID *string `json:"model_catalog_request_id"`
}

// workspaceAlwaysRedactSecrets reports whether the workspace has opted
// into unconditional redaction of secret-bearing fields (currently
// `mcp_config`) on read responses, regardless of the caller's role.
//
// The legacy JSON key is still `always_redact_env` for backwards-
// compatibility with workspaces that flipped the setting before MUL-2600
// shipped. The setting no longer affects `custom_env` because that field
// is never serialized on agent resources anymore — secrets there are
// fetched exclusively through `GET /api/agents/{id}/env` with audit
// logging — so the flag now only governs `mcp_config` exposure.
func workspaceAlwaysRedactSecrets(settings []byte) bool {
	if len(settings) == 0 {
		return false
	}
	var s struct {
		AlwaysRedactEnv bool `json:"always_redact_env"`
	}
	if err := json.Unmarshal(settings, &s); err != nil {
		return false
	}
	return s.AlwaysRedactEnv
}

// canViewAgentSecrets checks whether the requesting user is allowed to
// see the agent's secret-bearing fields (currently `mcp_config`). Only
// the agent owner or workspace owner/admin qualify; for everyone else
// the response is redacted. `custom_env` is no longer part of an agent
// resource response (see MUL-2600), so this predicate is shared only by
// the remaining mcp_config redaction path.
func canViewAgentSecrets(agent db.Agent, userID string, memberRole string) bool {
	if roleAllowed(memberRole, "owner", "admin") {
		return true
	}
	return uuidToString(agent.OwnerID) == userID
}

// broadcastAgentResponse strips secret-bearing fields from an
// AgentResponse before it goes onto the WebSocket bus. Mutation
// handlers call this when fanning out create/update/archive/restore
// events: subscribers (which include agent processes that have
// authenticated with their own task tokens) must not learn another
// agent's mcp_config via a WS push that bypassed the read-path
// redaction in ListAgents / GetAgent. The caller still receives the
// canonical form in the HTTP response; only the broadcast copy is
// redacted.
func broadcastAgentResponse(resp AgentResponse) AgentResponse {
	out := resp
	redactMcpConfig(&out)
	return out
}

// redactAgentInternals clears the agent's internal-construction fields —
// Instructions, RuntimeConfig, CustomArgs, MemoryGrowth — from the response
// when the caller isn't the agent's owner, a workspace owner/admin, or
// another agent. Task #908: existence/identity/usage (name, avatar,
// description, model, thinking_level, being chatted with or assigned work)
// is unconditional for every workspace member; how the agent is built stays
// admin|owner. See canAccessAgentInternals.
//
// MaxConcurrentTasks is deliberately NOT in this bucket (nor treated as
// unconditional usage info) — Frank flagged 2026-07-30 16:43 that it may be
// entirely dead now that execution is single-session; pending confirmation
// (batch 3), it's left exactly as-is rather than guessed into either side.
func redactAgentInternals(resp *AgentResponse) {
	resp.Instructions = ""
	resp.RuntimeConfig = nil
	resp.CustomArgs = []string{}
	resp.MemoryGrowth = nil
}

// redactMcpConfig removes the mcp_config value from the response when the caller is not
// authorised to view it. The field is set to null; McpConfigRedacted is set to true so
// callers know a config exists without seeing its contents (which may contain secrets).
func redactMcpConfig(resp *AgentResponse) {
	if resp.McpConfig != nil {
		resp.McpConfig = nil
		resp.McpConfigRedacted = true
	}
}

// redactAgentResponseForActor strips secret-bearing fields from an agent
// resource HTTP response when the request actor is an agent. Read
// handlers already gate on actorType — mutation handlers
// (create/update/archive/restore) must apply the same rule, otherwise
// an agent with a host owner/admin token can do an unrelated mutation
// (e.g. flip max_concurrent_tasks) on a target agent and harvest the
// target's mcp_config from the mutation response. MUL-2600.
func redactAgentResponseForActor(resp *AgentResponse, actorType string) {
	if actorType == "agent" {
		redactMcpConfig(resp)
	}
}

// canManageAgent checks whether the current user can manage an agent's full
// lifecycle (archive, restore, skills, and unrestricted updates). Only the
// agent owner or workspace owner/admin can do that, regardless of whether the
// agent is public or private.
func (h *Handler) canManageAgent(w http.ResponseWriter, r *http.Request, agent db.Agent) bool {
	// Agent principal (Raft align / task #125): may only manage self.
	// Human owner/admin workspace management is unchanged.
	if p, ok := middleware.AgentPrincipalFromContext(r.Context()); ok {
		if p.AgentID != uuidToString(agent.ID) {
			writeError(w, http.StatusForbidden, "agents may only manage themselves")
			return false
		}
		return true
	}
	wsID := uuidToString(agent.WorkspaceID)
	member, ok := h.requireWorkspaceRole(w, r, wsID, "agent not found", "owner", "admin", "member")
	if !ok {
		return false
	}
	isAdmin := roleAllowed(member.Role, "owner", "admin")
	isAgentOwner := uuidToString(agent.OwnerID) == requestUserID(r)
	if !isAdmin && !isAgentOwner {
		writeError(w, http.StatusForbidden, "only the agent owner can manage this agent")
		return false
	}
	return true
}

// canUpdateAgent permits the normal owner/admin update path for humans,
// and self-only updates for AgentPrincipal (task #125 / Raft align).
func (h *Handler) canUpdateAgent(w http.ResponseWriter, r *http.Request, agent db.Agent, rawFields map[string]json.RawMessage) bool {
	if p, ok := middleware.AgentPrincipalFromContext(r.Context()); ok {
		if p.AgentID != uuidToString(agent.ID) {
			writeError(w, http.StatusForbidden, "agents may only update themselves")
			return false
		}
		return true
	}
	wsID := uuidToString(agent.WorkspaceID)
	member, ok := h.requireWorkspaceRole(w, r, wsID, "agent not found", "owner", "admin", "member")
	if !ok {
		return false
	}
	isAdmin := roleAllowed(member.Role, "owner", "admin")
	isAgentOwner := uuidToString(agent.OwnerID) == requestUserID(r)
	if !isAdmin && !isAgentOwner {
		writeError(w, http.StatusForbidden, "only the agent owner can manage this agent")
		return false
	}
	return true
}

func agentUpdateAffectsEvolutionMatching(req UpdateAgentRequest) bool {
	return req.DisplayName != nil ||
		req.Description != nil ||
		req.Instructions != nil ||
		req.RuntimeConfig != nil ||
		req.CustomArgs != nil ||
		req.RuntimeID != nil ||
		req.Model != nil
}

func (h *Handler) UpdateAgent(w http.ResponseWriter, r *http.Request) {
	id := chi.URLParam(r, "id")
	existing, ok := h.loadAgentForUser(w, r, id)
	if !ok {
		return
	}

	var req UpdateAgentRequest
	rawFields, err := decodeJSONBodyWithRawFields(r.Body, &req)
	if err != nil {
		writeError(w, http.StatusBadRequest, "invalid request body")
		return
	}
	if _, ok := rawFields["name"]; ok {
		writeError(w, http.StatusBadRequest, "name is no longer accepted; use display_name")
		return
	}
	if _, ok := rawFields["avatar_url"]; ok {
		writeError(w, http.StatusBadRequest, "avatar_url is no longer accepted; use avatar_selection")
		return
	}
	if _, ok := rawFields["workspace_role"]; ok {
		writeError(w, http.StatusBadRequest, "workspace_role is not accepted on this endpoint; use PATCH /api/workspaces/{workspaceId}/agents/{agentId}/role")
		return
	}
	if !h.canUpdateAgent(w, r, existing, rawFields) {
		return
	}

	// Hard-reject any attempt to write custom_env through the generic
	// update endpoint. Silently dropping the field (which is what an
	// `omitempty` field would do) was the pre-PR behaviour and led to
	// users believing they had rotated a secret when the value was
	// actually unchanged. env values move only through `PUT
	// /api/agents/{id}/env` — that endpoint is owner/admin-only, denies
	// agent actors, and writes a queryable audit row.
	if _, ok := rawFields["custom_env"]; ok {
		writeError(w, http.StatusBadRequest, "custom_env is no longer accepted on this endpoint; use PUT /api/agents/{id}/env (or `multica agent env set`)")
		return
	}

	params := db.UpdateAgentParams{
		ID: existing.ID,
	}
	if req.Username != nil {
		if err := validateIdentityHandle(*req.Username); err != nil {
			writeError(w, http.StatusBadRequest, "username must be 1-32 lowercase letters, digits, or hyphens")
			return
		}
		params.Name = pgtype.Text{String: *req.Username, Valid: true}
	}
	if req.DisplayName != nil {
		displayName := strings.TrimSpace(*req.DisplayName)
		if displayName == "" {
			writeError(w, http.StatusBadRequest, "display_name is required")
			return
		}
		params.DisplayName = pgtype.Text{String: displayName, Valid: true}
	}
	if req.Description != nil {
		if utf8.RuneCountInString(*req.Description) > maxAgentDescriptionLength {
			writeError(w, http.StatusBadRequest, fmt.Sprintf("description must be %d characters or fewer", maxAgentDescriptionLength))
			return
		}
		params.Description = pgtype.Text{String: *req.Description, Valid: true}
	}
	if req.Instructions != nil {
		params.Instructions = pgtype.Text{String: *req.Instructions, Valid: true}
	}
	avatar, ok := h.resolveAgentAvatarSelection(w, r, existing.WorkspaceID, requestUserID(r), existing.ID, req.AvatarSelection)
	if !ok {
		return
	}
	applyUpdateAgentAvatar(&params, avatar)
	if req.RuntimeConfig != nil {
		rc, _ := json.Marshal(req.RuntimeConfig)
		params.RuntimeConfig = rc
	}
	if req.CustomArgs != nil {
		ca, _ := json.Marshal(*req.CustomArgs)
		params.CustomArgs = ca
	}
	rawMcpConfig, hasMcpConfig := rawFields["mcp_config"]
	shouldClearMcpConfig := hasMcpConfig && bytes.Equal(bytes.TrimSpace(rawMcpConfig), []byte("null"))
	if hasMcpConfig && !shouldClearMcpConfig {
		params.McpConfig = append([]byte(nil), rawMcpConfig...)
	}

	// Resolve the runtime that will be in force after this update so the
	// thinking_level validation hits the right provider enum. When the
	// request doesn't move the agent, we still need to load the *current*
	// runtime to validate a thinking_level change. Resolve once and reuse.
	targetRuntimeID := existing.RuntimeID
	if req.RuntimeID != nil {
		runtimeUUID, ok := parseUUIDOrBadRequest(w, *req.RuntimeID, "runtime_id")
		if !ok {
			return
		}
		runtime, err := h.Queries.GetAgentRuntimeForWorkspace(r.Context(), db.GetAgentRuntimeForWorkspaceParams{
			ID:          runtimeUUID,
			WorkspaceID: existing.WorkspaceID,
		})
		if err != nil {
			writeError(w, http.StatusBadRequest, "invalid runtime_id")
			return
		}
		if _, ok := h.workspaceMember(w, r, uuidToString(existing.WorkspaceID)); !ok {
			return
		}
		if runtime.ID != existing.RuntimeID {
			currentRuntime, err := h.Queries.GetAgentRuntimeForWorkspace(r.Context(), db.GetAgentRuntimeForWorkspaceParams{
				ID:          existing.RuntimeID,
				WorkspaceID: existing.WorkspaceID,
			})
			if err != nil {
				writeError(w, http.StatusInternalServerError, "failed to load current runtime")
				return
			}
			if !runtimesShareMachine(currentRuntime, runtime) && !agentRuntimeHasCapability(runtime, protocol.DaemonCapabilityMemoryCrossDeviceSync) {
				writeCodedError(w, http.StatusConflict, "daemon_memory_sync_required", "target daemon must upgrade before moving an agent between computers")
				return
			}
		}
		if runtime.ID != existing.RuntimeID && !agentRuntimeHasCapability(runtime, protocol.DaemonCapabilityReminderVersionedCache) {
			var hasActiveReminders bool
			if err := h.DB.QueryRow(r.Context(), `
				SELECT EXISTS (
				  SELECT 1 FROM agent_reminder
				  WHERE agent_id = $1 AND status IN ('scheduled', 'firing')
				)`, existing.ID).Scan(&hasActiveReminders); err != nil {
				writeError(w, http.StatusInternalServerError, "failed to validate reminder runtime capability")
				return
			}
			if hasActiveReminders {
				writeCodedError(w, http.StatusConflict, "daemon_outdated", "target runtime must upgrade before moving an agent with active reminders")
				return
			}
		}
		params.RuntimeID = runtime.ID
		params.RuntimeMode = pgtype.Text{String: runtime.RuntimeMode, Valid: true}
		targetRuntimeID = runtime.ID
	}
	if req.Status != nil {
		params.Status = pgtype.Text{String: *req.Status, Valid: true}
	}
	if req.MaxConcurrentTasks != nil {
		params.MaxConcurrentTasks = pgtype.Int4{Int32: *req.MaxConcurrentTasks, Valid: true}
	}
	if req.Model != nil {
		params.Model = pgtype.Text{String: *req.Model, Valid: true}
	}

	// thinking_level handling (MUL-2339). Tri-state semantics:
	//   - field omitted  → leave column alone (COALESCE narg), but if a
	//     runtime change in this same request would make the *existing*
	//     value literal-invalid for the new provider, reconcile it to the
	//     target model's advertised default when fresh catalog evidence is
	//     available, otherwise clear it. This prevents a runtime switch from
	//     persisting a value the target runtime can never accept.
	//   - field set to "" → explicit clear (run ClearAgentThinkingLevel post-update)
	//   - field set to value → validate against the target runtime's provider
	//     enum; reject literal-invalid with 400. Per-model combination checks
	//     run in the daemon at execution time, not here — see Trump's review
	//     constraint that API behaviour stays consistent across change paths.
	targetModel := existing.Model.String
	if req.Model != nil {
		targetModel = *req.Model
	}
	targetThinkingLevel := existing.ThinkingLevel.String
	shouldClearThinkingLevel := false
	if req.ThinkingLevel != nil {
		value := *req.ThinkingLevel
		targetThinkingLevel = value
		if value == "" {
			shouldClearThinkingLevel = true
		} else {
			// Need the target runtime's provider to validate. Re-fetch only when
			// we haven't already loaded it above (i.e. the request didn't change
			// runtime_id), to keep the no-change path one DB roundtrip.
			provider, ok := h.resolveAgentProvider(r, existing.WorkspaceID, targetRuntimeID)
			if !ok {
				writeError(w, http.StatusInternalServerError, "failed to resolve runtime for thinking_level validation")
				return
			}
			if !agent.IsKnownThinkingValue(provider, value) {
				writeError(w, http.StatusBadRequest, fmt.Sprintf("thinking_level %q is not a recognised value for runtime %q", value, provider))
				return
			}
			params.ThinkingLevel = pgtype.Text{String: value, Valid: true}
		}
	} else if req.RuntimeID != nil && existing.ThinkingLevel.Valid && existing.ThinkingLevel.String != "" {
		// Runtime is changing but the caller did not touch thinking_level.
		// Preserve values the target accepts. For an invalid inherited value,
		// apply the target model's freshly advertised default if one exists;
		// otherwise clear the override so the runtime chooses its own default.
		provider, ok := h.resolveAgentProvider(r, existing.WorkspaceID, targetRuntimeID)
		if !ok {
			writeError(w, http.StatusInternalServerError, "failed to resolve runtime for thinking_level validation")
			return
		}
		if !agent.IsKnownThinkingValue(provider, existing.ThinkingLevel.String) {
			level, err := h.reconciledThinkingLevelFromCatalog(r.Context(), req.ModelCatalogRequestID, targetRuntimeID, targetModel)
			if err != nil {
				writeError(w, http.StatusBadRequest, err.Error())
				return
			}
			targetThinkingLevel = level
			if level == "" {
				shouldClearThinkingLevel = true
			} else {
				params.ThinkingLevel = pgtype.Text{String: level, Valid: true}
			}
		}
	}

	// Profile saves carry the completed discovery request that backed the
	// picker. Validate the final runtime/model/thinking tuple after the
	// runtime-switch reconciliation above, not the stale inherited value.
	// The request is intentionally transient: it proves fresh runtime
	// capability without turning daemon-discovered catalog data into a stale
	// agent setting.
	if req.ModelCatalogRequestID != nil {
		if err := h.validateAgentModelCatalog(r.Context(), *req.ModelCatalogRequestID, targetRuntimeID, targetModel, targetThinkingLevel); err != nil {
			writeError(w, http.StatusBadRequest, err.Error())
			return
		}
	}

	movingRuntime := req.RuntimeID != nil && targetRuntimeID != existing.RuntimeID

	updated, err := h.Queries.UpdateAgent(r.Context(), params)
	if err != nil {
		if isReminderDaemonOutdatedError(err) {
			writeCodedError(w, http.StatusConflict, "daemon_outdated", "target runtime must upgrade before moving an agent with active reminders")
			return
		}
		if identityUniqueViolation(err, "agent_workspace_name_unique") {
			writeError(w, http.StatusConflict, "username is already in use")
			return
		}
		if identityUniqueViolation(err, "agent_avatar_attachment_unique") {
			writeError(w, http.StatusConflict, "avatar attachment is already bound")
			return
		}
		slog.Warn("update agent failed", append(logger.RequestAttrs(r), "error", err, "agent_id", id)...)
		writeError(w, http.StatusInternalServerError, "failed to update agent: "+err.Error())
		return
	}

	// task #38: stamp the transition marker only when this request actually
	// moved the agent onto a different runtime — never on a no-op update
	// that repeats the current runtime_id. EnsureDaemonAgentCredential uses
	// this to give the old runtime's daemon a short silent grace window
	// instead of immediately reporting the terminal agent_reassigned_elsewhere
	// failure. Best-effort: a failure here should not fail the update itself,
	// it only means the grace window doesn't apply to this transition.
	if movingRuntime {
		if err := h.Queries.MarkAgentRuntimeReassigned(r.Context(), updated.ID); err != nil {
			slog.Warn("failed to stamp agent runtime reassignment", append(logger.RequestAttrs(r), "error", err, "agent_id", id)...)
		}
	}

	// mcp_config / thinking_level: null/empty in the request means explicitly
	// clear the field. COALESCE in UpdateAgent cannot set a column to NULL,
	// so we use dedicated clear queries.
	if shouldClearMcpConfig {
		updated, err = h.Queries.ClearAgentMcpConfig(r.Context(), updated.ID)
		if err != nil {
			slog.Warn("clear agent mcp_config failed", append(logger.RequestAttrs(r), "error", err, "agent_id", id)...)
			writeError(w, http.StatusInternalServerError, "failed to clear mcp_config: "+err.Error())
			return
		}
	}
	if shouldClearThinkingLevel {
		updated, err = h.Queries.ClearAgentThinkingLevel(r.Context(), updated.ID)
		if err != nil {
			slog.Warn("clear agent thinking_level failed", append(logger.RequestAttrs(r), "error", err, "agent_id", id)...)
			writeError(w, http.StatusInternalServerError, "failed to clear thinking_level: "+err.Error())
			return
		}
	}
	resp := agentToResponse(updated)
	// agentToResponse always initialises Skills as []; junction-table rows
	// are untouched by the SQL update, so we reload them here to keep the
	// response (and the broadcast that mirrors it) in sync with reality.
	// Without this, callers see "skills": [] after every metadata-only
	// update and assume their bindings were cleared — see #3459.
	if err := h.attachAgentSkills(r.Context(), &resp, updated.ID); err != nil {
		slog.Warn("load agent skills after update failed", append(logger.RequestAttrs(r), "error", err, "agent_id", id)...)
		writeError(w, http.StatusInternalServerError, "failed to load agent skills")
		return
	}
	h.attachAgentRuntimeName(r.Context(), &resp)
	if agentUpdateAffectsEvolutionMatching(req) {
		h.refreshAgentSkillSuggestions(r.Context(), updated)
	}
	slog.Info("agent updated", append(logger.RequestAttrs(r), "agent_id", id, "workspace_id", uuidToString(updated.WorkspaceID))...)
	userID := requestUserID(r)
	actorType, actorID := h.resolveActor(r, userID, uuidToString(updated.WorkspaceID))
	h.publishAgentVisibilityEvent(protocol.EventAgentStatus, uuidToString(updated.WorkspaceID), actorType, actorID, updated, map[string]any{"agent": broadcastAgentResponse(resp)})
	if existing.RuntimeID.Valid && updated.RuntimeID.Valid && existing.RuntimeID != updated.RuntimeID {
		if h.ReminderNotifier != nil {
			h.projectReminderOwnerStop(r.Context(), uuidToString(updated.ID), uuidToString(existing.RuntimeID))
			h.projectReminderOwnerStart(r.Context(), uuidToString(updated.ID), uuidToString(updated.RuntimeID))
		}
		// agent_inbox_event.runtime_id is snapshotted at enqueue and is not
		// rewritten by UpdateAgent itself. Move still-claimable events onto the
		// new runtime so the old daemon cannot lease them and 403 on
		// ensure-credential (LRM-927 / #1628 companion).
		h.reassignClaimableInboxEventsAfterAgentRuntimeMove(r.Context(), updated.ID, existing.RuntimeID, updated.RuntimeID)
	}
	redactAgentResponseForActor(&resp, actorType)
	writeJSON(w, http.StatusOK, resp)
}

// attachAgentSkills populates resp.Skills from the agent_skill junction
// table for the given agent. agentToResponse zeros the field; mutation
// handlers that don't refresh it would otherwise serve a misleading
// empty array on every successful response (#3459).
func (h *Handler) attachAgentSkills(ctx context.Context, resp *AgentResponse, agentID pgtype.UUID) error {
	skills, err := h.Queries.ListAgentSkillSummaries(ctx, agentID)
	if err != nil {
		return err
	}
	if len(skills) == 0 {
		return nil
	}
	out := make([]AgentSkillSummary, len(skills))
	for i, s := range skills {
		out[i] = AgentSkillSummary{
			ID:          uuidToString(s.ID),
			Name:        s.Name,
			Description: s.Description,
		}
	}
	resp.Skills = out
	return nil
}

// resolveAgentProvider returns the provider name for the runtime that
// will own this agent after the in-flight update applies. Used by the
// thinking_level validator so a runtime/model swap and a level swap
// validated in the same request both consult the same provider.
func (h *Handler) resolveAgentProvider(r *http.Request, workspaceID pgtype.UUID, runtimeID pgtype.UUID) (string, bool) {
	rt, err := h.Queries.GetAgentRuntimeForWorkspace(r.Context(), db.GetAgentRuntimeForWorkspaceParams{
		ID:          runtimeID,
		WorkspaceID: workspaceID,
	})
	if err != nil {
		return "", false
	}
	return rt.Provider, true
}

// validateAgentModelCatalog verifies the exact execution-config tuple against
// a completed daemon model-discovery response. A model-list request is bound
// to one runtime and expires with the store's normal retention window, so it
// cannot be replayed for a different runtime or indefinitely after capability
// changes.
func (h *Handler) validateAgentModelCatalog(ctx context.Context, requestID string, runtimeID pgtype.UUID, model, thinkingLevel string) error {
	requestID = strings.TrimSpace(requestID)
	if requestID == "" {
		return fmt.Errorf("model_catalog_request_id is required for execution configuration")
	}
	request, err := h.ModelListStore.Get(ctx, requestID)
	if err != nil {
		return fmt.Errorf("failed to load model catalog")
	}
	if request == nil || request.Status != ModelListCompleted {
		return fmt.Errorf("model catalog is unavailable or expired; refresh runtime models and try again")
	}
	if request.RuntimeID != uuidToString(runtimeID) {
		return fmt.Errorf("model catalog does not belong to the selected runtime")
	}
	if !request.Supported {
		if model != "" || thinkingLevel != "" {
			return fmt.Errorf("the selected runtime does not support per-agent model or thinking configuration")
		}
		return nil
	}
	if model == "" {
		if thinkingLevel != "" {
			return fmt.Errorf("select a model before selecting a thinking level")
		}
		return nil
	}
	for _, entry := range request.Models {
		if entry.ID != model {
			continue
		}
		if thinkingLevel == "" {
			return nil
		}
		if entry.Thinking == nil {
			return fmt.Errorf("the selected model does not support thinking configuration")
		}
		for _, level := range entry.Thinking.SupportedLevels {
			if level.Value == thinkingLevel {
				return nil
			}
		}
		return fmt.Errorf("thinking_level %q is not advertised for model %q", thinkingLevel, model)
	}
	return fmt.Errorf("model %q is not advertised by the selected runtime", model)
}

// reconciledThinkingLevelFromCatalog returns the target model's advertised
// default effort for a runtime-switch repair. A missing catalog/default is not
// an error: clearing the stored override is the only truthful fallback because
// the runtime itself owns the default. When a caller supplied catalog proof,
// however, stale, cross-runtime, or unsupported proof remains a bad request.
func (h *Handler) reconciledThinkingLevelFromCatalog(ctx context.Context, requestID *string, runtimeID pgtype.UUID, model string) (string, error) {
	if requestID == nil || strings.TrimSpace(*requestID) == "" {
		return "", nil
	}
	request, err := h.ModelListStore.Get(ctx, strings.TrimSpace(*requestID))
	if err != nil {
		return "", fmt.Errorf("failed to load model catalog")
	}
	if request == nil || request.Status != ModelListCompleted {
		return "", fmt.Errorf("model catalog is unavailable or expired; refresh runtime models and try again")
	}
	if request.RuntimeID != uuidToString(runtimeID) {
		return "", fmt.Errorf("model catalog does not belong to the selected runtime")
	}
	if !request.Supported || model == "" {
		return "", nil
	}
	for _, entry := range request.Models {
		if entry.ID != model {
			continue
		}
		if entry.Thinking == nil || entry.Thinking.DefaultLevel == "" {
			return "", nil
		}
		for _, level := range entry.Thinking.SupportedLevels {
			if level.Value == entry.Thinking.DefaultLevel {
				return entry.Thinking.DefaultLevel, nil
			}
		}
		return "", fmt.Errorf("model catalog default thinking_level %q is not advertised for model %q", entry.Thinking.DefaultLevel, model)
	}
	return "", fmt.Errorf("model %q is not advertised by the selected runtime", model)
}

func (h *Handler) ArchiveAgent(w http.ResponseWriter, r *http.Request) {
	startedAt := time.Now()
	result := "error"
	defer func() {
		h.Metrics.ObserveAgentDelete(result, time.Since(startedAt).Seconds())
	}()

	id := chi.URLParam(r, "id")
	agent, ok := h.loadAgentForUser(w, r, id)
	if !ok {
		return
	}
	if !h.canManageAgent(w, r, agent) {
		return
	}
	if agent.ArchivedAt.Valid {
		writeError(w, http.StatusConflict, "agent is already archived")
		return
	}

	userID := requestUserID(r)
	archived, err := h.Queries.ArchiveAgent(r.Context(), db.ArchiveAgentParams{
		ID:         agent.ID,
		ArchivedBy: parseUUID(userID),
	})
	if err != nil {
		slog.Warn("archive agent failed", append(logger.RequestAttrs(r), "error", err, "agent_id", id)...)
		writeError(w, http.StatusInternalServerError, "failed to archive agent")
		return
	}

	// Cancel all pending/active tasks for this agent. Discard the returned
	// rows here — the agent:archived event below already triggers a full
	// active-tasks invalidation on every connected client, so per-task
	// task:cancelled events would be redundant noise.
	if cancelled, err := h.Queries.CancelAgentTasksByAgent(r.Context(), agent.ID); err != nil {
		slog.Warn("cancel agent tasks on archive failed", append(logger.RequestAttrs(r), "error", err, "agent_id", id)...)
	} else {
		h.TaskService.CaptureCancelledTasks(r.Context(), cancelled)
	}

	wsID := uuidToString(archived.WorkspaceID)
	slog.Info("agent archived", append(logger.RequestAttrs(r), "agent_id", id, "workspace_id", wsID)...)
	resp := agentToResponse(archived)
	if err := h.attachAgentSkills(r.Context(), &resp, archived.ID); err != nil {
		slog.Warn("load agent skills after archive failed", append(logger.RequestAttrs(r), "error", err, "agent_id", id)...)
		writeError(w, http.StatusInternalServerError, "failed to load agent skills")
		return
	}
	if h.AgentFleetRankService != nil {
		if err := h.AgentFleetRankService.FreezeAgentOnArchive(r.Context(), archived.WorkspaceID, archived.ID); err != nil {
			slog.Warn("freeze agent fleet rank on archive failed", append(logger.RequestAttrs(r), "error", err, "agent_id", id)...)
		} else {
			h.AgentFleetRankService.RefreshWorkspaceAfterArchiveAsync(archived.WorkspaceID)
		}
	}
	h.attachAgentRuntimeName(r.Context(), &resp)
	actorType, actorID := h.resolveActor(r, userID, wsID)
	h.publish(protocol.EventAgentArchived, wsID, actorType, actorID, map[string]any{"agent": broadcastAgentResponse(resp)})
	if archived.RuntimeID.Valid {
		h.projectReminderOwnerStop(r.Context(), uuidToString(archived.ID), uuidToString(archived.RuntimeID))
	}
	redactAgentResponseForActor(&resp, actorType)
	result = "success"
	writeJSON(w, http.StatusOK, resp)
}

func (h *Handler) RestoreAgent(w http.ResponseWriter, r *http.Request) {
	id := chi.URLParam(r, "id")
	agent, ok := h.loadAgentForUser(w, r, id)
	if !ok {
		return
	}
	if !h.canManageAgent(w, r, agent) {
		return
	}
	if !agent.ArchivedAt.Valid {
		writeError(w, http.StatusConflict, "agent is not archived")
		return
	}

	restored, err := h.Queries.RestoreAgent(r.Context(), agent.ID)
	if err != nil {
		slog.Warn("restore agent failed", append(logger.RequestAttrs(r), "error", err, "agent_id", id)...)
		writeError(w, http.StatusInternalServerError, "failed to restore agent")
		return
	}
	if h.AgentFleetRankService != nil {
		if err := h.AgentFleetRankService.RestoreAgent(r.Context(), restored.WorkspaceID, restored.ID); err != nil {
			slog.Warn("restore agent fleet rank failed", append(logger.RequestAttrs(r), "error", err, "agent_id", id)...)
		}
	}
	h.refreshAgentHonor(r.Context(), restored.WorkspaceID, restored.ID, "agent_restored")

	wsID := uuidToString(restored.WorkspaceID)
	slog.Info("agent restored", append(logger.RequestAttrs(r), "agent_id", id, "workspace_id", wsID)...)
	resp := agentToResponse(restored)
	if err := h.attachAgentSkills(r.Context(), &resp, restored.ID); err != nil {
		slog.Warn("load agent skills after restore failed", append(logger.RequestAttrs(r), "error", err, "agent_id", id)...)
		writeError(w, http.StatusInternalServerError, "failed to load agent skills")
		return
	}
	h.attachAgentRuntimeName(r.Context(), &resp)
	userID := requestUserID(r)
	actorType, actorID := h.resolveActor(r, userID, wsID)
	h.publish(protocol.EventAgentRestored, wsID, actorType, actorID, map[string]any{"agent": broadcastAgentResponse(resp)})
	if restored.RuntimeID.Valid {
		h.projectReminderOwnerStart(r.Context(), uuidToString(restored.ID), uuidToString(restored.RuntimeID))
	}
	redactAgentResponseForActor(&resp, actorType)
	writeJSON(w, http.StatusOK, resp)
}

// CancelAgentTasks bulk-cancels every active task (queued/dispatched/running)
// belonging to an agent. Powers the agents-list "Cancel all tasks" row
// action. Same permission gate as archive (canManageAgent — owner or
// workspace admin/owner). Each cancelled row triggers a task:cancelled WS
// event so connected clients clear their live cards immediately.
//
// Note: a `running` task on the daemon side won't actually halt for up to
// ~5 seconds (daemon polls GetTaskStatus on that interval). The DB row is
// marked cancelled instantly, but the child process keeps going briefly;
// see daemon/daemon.go:919-942 for the polling loop. Surface this in the
// confirm-dialog copy so users aren't surprised by trailing transcript
// lines.
type cancelAgentTasksResponse struct {
	Cancelled int `json:"cancelled"`
}

func (h *Handler) CancelAgentTasks(w http.ResponseWriter, r *http.Request) {
	id := chi.URLParam(r, "id")
	agent, ok := h.loadAgentForUser(w, r, id)
	if !ok {
		return
	}
	if !h.canManageAgent(w, r, agent) {
		return
	}

	cancelled, err := h.TaskService.CancelTasksForAgent(r.Context(), parseUUID(id))
	if err != nil {
		slog.Warn("cancel agent tasks failed", append(logger.RequestAttrs(r), "error", err, "agent_id", id)...)
		writeError(w, http.StatusInternalServerError, "failed to cancel tasks")
		return
	}

	slog.Info("agent tasks cancelled",
		append(logger.RequestAttrs(r), "agent_id", id, "count", len(cancelled))...)
	writeJSON(w, http.StatusOK, cancelAgentTasksResponse{Cancelled: len(cancelled)})
}

func (h *Handler) ListAgentTasks(w http.ResponseWriter, r *http.Request) {
	id := chi.URLParam(r, "id")
	agent, ok := h.loadAgentForUser(w, r, id)
	if !ok {
		return
	}
	// Run history ("查看历史会话") is an internal surface under task #908's
	// principle — gated to admin|owner, same predicate as the Activity tab.
	workspaceID := uuidToString(agent.WorkspaceID)
	actorType, actorID := h.resolveActor(r, requestUserID(r), workspaceID)
	if !h.canAccessAgentInternals(r.Context(), agent, actorType, actorID, workspaceID) {
		writeError(w, http.StatusForbidden, "you do not have access to this agent")
		return
	}

	tasks, err := h.Queries.ListAgentTasks(r.Context(), agent.ID)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "failed to list agent tasks")
		return
	}

	items := make([]agentTaskSortItem, 0, len(tasks))
	for _, t := range tasks {
		resp := taskToResponse(t, workspaceID)
		items = append(items, agentTaskSortItem{Task: resp, SortAt: timestampToTime(t.CreatedAt)})
	}
	sort.SliceStable(items, func(i, j int) bool {
		return items[i].SortAt.After(items[j].SortAt)
	})
	actorType, actorID = h.resolveActor(r, requestUserID(r), workspaceID)
	memberRole := ""
	if member, err := h.getWorkspaceMember(r.Context(), actorID, workspaceID); err == nil {
		memberRole = member.Role
	}
	resolver := h.newActorIdentityResolver(r.Context(), workspaceID, actorType, actorID, memberRole)

	resp := make([]AgentTaskResponse, 0, len(items))
	for _, item := range items {
		applyActorIdentityToTask(&item.Task, resolver.resolve("agent", item.Task.AgentID))
		resp = append(resp, item.Task)
	}

	writeJSON(w, http.StatusOK, resp)
}

func timestampToTime(value pgtype.Timestamptz) time.Time {
	if value.Valid {
		return value.Time
	}
	return time.Time{}
}

// AgentActivityBucket is one day-bucketed throughput sample for the
// Agents-list ACTIVITY sparkline. bucket_at is midnight UTC of the day.
type AgentActivityBucket struct {
	AgentID     string `json:"agent_id"`
	BucketAt    string `json:"bucket_at"`
	TaskCount   int32  `json:"task_count"`
	FailedCount int32  `json:"failed_count"`
}

// AgentRunCount is the trailing-30-day total task run count per agent,
// powering the Agents-list RUNS column.
type AgentRunCount struct {
	AgentID  string `json:"agent_id"`
	RunCount int32  `json:"run_count"`
}

// GetWorkspaceAgentRunCounts returns 30-day total run counts for every
// agent in the workspace. Same single-fetch pattern as live-tasks /
// activity to keep the Agents list cheap regardless of agent count.
func (h *Handler) GetWorkspaceAgentRunCounts(w http.ResponseWriter, r *http.Request) {
	workspaceID := h.resolveWorkspaceID(r)
	member, ok := h.workspaceMember(w, r, workspaceID)
	if !ok {
		return
	}

	rows, err := h.Queries.GetWorkspaceAgentRunCounts(r.Context(), parseUUID(workspaceID))
	if err != nil {
		writeError(w, http.StatusInternalServerError, "failed to get agent run counts")
		return
	}

	actorType, actorID := h.resolveActor(r, requestUserID(r), workspaceID)
	allowed, ok := h.accessibleAgentIDs(r.Context(), workspaceID, actorType, actorID, member.Role)
	if !ok {
		writeError(w, http.StatusInternalServerError, "failed to resolve agent access")
		return
	}

	resp := make([]AgentRunCount, 0, len(rows))
	for _, row := range rows {
		agentID := uuidToString(row.AgentID)
		if _, ok := allowed[agentID]; !ok {
			continue
		}
		resp = append(resp, AgentRunCount{
			AgentID:  agentID,
			RunCount: row.RunCount,
		})
	}

	writeJSON(w, http.StatusOK, resp)
}

// GetWorkspaceAgentActivity30d returns per-agent daily task counts for the
// last 30 days, anchored on completed_at. Single workspace-wide read backs
// both the Agents list sparkline (uses the trailing 7 buckets) and the
// agent detail "Last 30 days" panel (uses all 30) — one fetch is cheaper
// than two. Front-end fills missing days with zero; the back-end omits
// empty buckets to keep the response small.
func (h *Handler) GetWorkspaceAgentActivity30d(w http.ResponseWriter, r *http.Request) {
	workspaceID := h.resolveWorkspaceID(r)
	member, ok := h.workspaceMember(w, r, workspaceID)
	if !ok {
		return
	}

	rows, err := h.Queries.GetWorkspaceAgentActivity30d(r.Context(), parseUUID(workspaceID))
	if err != nil {
		writeError(w, http.StatusInternalServerError, "failed to get agent activity")
		return
	}

	actorType, actorID := h.resolveActor(r, requestUserID(r), workspaceID)
	allowed, ok := h.accessibleAgentIDs(r.Context(), workspaceID, actorType, actorID, member.Role)
	if !ok {
		writeError(w, http.StatusInternalServerError, "failed to resolve agent access")
		return
	}

	resp := make([]AgentActivityBucket, 0, len(rows))
	for _, row := range rows {
		agentID := uuidToString(row.AgentID)
		if _, ok := allowed[agentID]; !ok {
			continue
		}
		resp = append(resp, AgentActivityBucket{
			AgentID:     agentID,
			BucketAt:    timestampToString(row.Bucket),
			TaskCount:   row.TaskCount,
			FailedCount: row.FailedCount,
		})
	}

	writeJSON(w, http.StatusOK, resp)
}

// ListWorkspaceAgentTaskSnapshot returns the task data the front-end needs to
// derive each agent's presence: every active task plus each agent's most recent
// OUTCOME task (completed/failed only). Legacy issue/autopilot/quick-create
// tasks still come from agent_inbox_event; chat/channel work now runs through
// agent_inbox_event, so active inbox rows are folded into the same workspace
// snapshot. Cancelled tasks are excluded from the outcome half by design —
// cancel is a procedural signal ("attempt aborted"), not an outcome, so it must
// not mask a prior failure. The front-end picks "active wins, else latest
// outcome"; a failed outcome stays sticky until the user starts a new task or
// one succeeds. Per-agent filtering happens in the front-end against this
// workspace-wide snapshot.
//
// LRM-1261: short TTL + singleflight collapses burst refetches; SQL trims heavy
// blobs; actor resolve is agents-only (snapshot rows are always agent-authored).
func (h *Handler) ListWorkspaceAgentTaskSnapshot(w http.ResponseWriter, r *http.Request) {
	workspaceID := h.resolveWorkspaceID(r)
	member, ok := h.workspaceMember(w, r, workspaceID)
	if !ok {
		return
	}

	if cached, hit := getCachedAgentTaskSnapshot(workspaceID); hit {
		writeJSON(w, http.StatusOK, cached)
		return
	}

	// Detach from the leader request's cancel so coalesced waiters still get a
	// result if the first client disconnects mid-query.
	loadCtx := context.WithoutCancel(r.Context())
	v, err, _ := agentTaskSnapshotCache.group.Do(workspaceID, func() (any, error) {
		if cached, hit := getCachedAgentTaskSnapshot(workspaceID); hit {
			return cached, nil
		}
		tasks, err := h.Queries.ListWorkspaceAgentTaskSnapshot(loadCtx, parseUUID(workspaceID))
		if err != nil {
			return nil, err
		}
		actorType, actorID := h.resolveActor(r, requestUserID(r), workspaceID)
		resolver := h.newAgentOnlyActorIdentityResolver(loadCtx, workspaceID, actorType, actorID, member.Role)
		resp := make([]AgentTaskResponse, 0, len(tasks))
		for _, t := range tasks {
			item := taskToResponse(t, workspaceID)
			applyActorIdentityToTask(&item, resolver.resolve("agent", item.AgentID))
			resp = append(resp, item)
		}
		putCachedAgentTaskSnapshot(workspaceID, resp)
		return resp, nil
	})
	if err != nil {
		writeError(w, http.StatusInternalServerError, "failed to list agent task snapshot")
		return
	}
	resp, _ := v.([]AgentTaskResponse)
	writeJSON(w, http.StatusOK, cloneAgentTaskSnapshot(resp))
}

// AgentTaskFeedItem is one terminal task in the workspace activity feed,
// trimmed to the fields the overview timeline renders. Actor identity is a
// backend snapshot so clients do not invent display fallbacks.
type AgentTaskFeedItem struct {
	ID              string         `json:"id"`
	AgentID         string         `json:"agent_id"`
	ActorID         string         `json:"actor_id,omitempty"`
	ActorType       string         `json:"actor_type,omitempty"`
	DisplayName     string         `json:"display_name,omitempty"`
	AvatarURL       *string        `json:"avatar_url,omitempty"`
	Handle          *string        `json:"handle,omitempty"`
	ActorStatus     string         `json:"actor_status,omitempty"`
	Actor           *ActorIdentity `json:"actor,omitempty"`
	IssueID         string         `json:"issue_id"`
	IssueIdentifier *string        `json:"issue_identifier,omitempty"`
	IssueTitle      *string        `json:"issue_title,omitempty"`
	ChatTitle       *string        `json:"chat_title,omitempty"`
	Status          string         `json:"status"`
	CompletedAt     *string        `json:"completed_at"`
	TriggerSummary  *string        `json:"trigger_summary,omitempty"`
}

// AgentTaskFeedCursor is the opaque composite cursor for the feed: the
// (completed_at, id) of the last returned row. Passed back as
// ?before_completed_at=&before_id= to fetch the next (older) page.
type AgentTaskFeedCursor struct {
	CompletedAt string `json:"completed_at"`
	ID          string `json:"id"`
}

type AgentTaskFeedResponse struct {
	Tasks      []AgentTaskFeedItem  `json:"tasks"`
	HasMore    bool                 `json:"has_more"`
	NextCursor *AgentTaskFeedCursor `json:"next_cursor,omitempty"`
}

// ListAgentTaskFeed returns a workspace-wide, cursor-paginated feed of terminal
// agent tasks (completed / failed / cancelled), newest completion first. One
// row per task — an agent that completed hundreds of tasks contributes hundreds
// of rows; the client renders them with a virtualized infinite-scroll list.
//
// Hand-written raw query rather than sqlc: the feed is read-only, the projection
// is a small fixed set of columns, and avoiding a new generated query keeps the
// change self-contained.
func (h *Handler) ListAgentTaskFeed(w http.ResponseWriter, r *http.Request) {
	workspaceID := h.resolveWorkspaceID(r)
	if _, ok := h.workspaceMember(w, r, workspaceID); !ok {
		return
	}

	limit := 30
	if raw := r.URL.Query().Get("limit"); raw != "" {
		parsed, err := strconv.Atoi(raw)
		if err != nil || parsed < 1 || parsed > 100 {
			writeError(w, http.StatusBadRequest, "invalid limit")
			return
		}
		limit = parsed
	}

	var beforeAt pgtype.Timestamptz
	var beforeID pgtype.UUID
	rawAt := r.URL.Query().Get("before_completed_at")
	rawID := r.URL.Query().Get("before_id")
	if rawAt != "" || rawID != "" {
		if rawAt == "" || rawID == "" {
			writeError(w, http.StatusBadRequest, "invalid cursor")
			return
		}
		ts, err := time.Parse(time.RFC3339Nano, rawAt)
		if err != nil {
			writeError(w, http.StatusBadRequest, "invalid cursor")
			return
		}
		cid, ok := parseUUIDOrBadRequest(w, rawID, "before_id")
		if !ok {
			return
		}
		beforeAt = pgtype.Timestamptz{Time: ts, Valid: true}
		beforeID = cid
	}

	rows, err := h.DB.Query(r.Context(), `
		SELECT atq.id, atq.agent_id, atq.issue_id,
		       CASE
		         WHEN atq.status = 'suppressed' THEN 'cancelled'
		         ELSE COALESCE(atq.terminal_outcome, 'completed')
		       END AS status,
		       atq.completed_at, atq.trigger_summary,
		       i.title, (w.issue_prefix || '-' || i.number::text) AS issue_identifier,
		       NULLIF(cs.title, '') AS chat_title
		FROM agent_inbox_event atq
		JOIN agent a ON a.id = atq.agent_id
		JOIN workspace w ON w.id = a.workspace_id
		LEFT JOIN issue i ON i.id = atq.issue_id
		LEFT JOIN chat_session cs ON cs.id = atq.chat_session_id
		WHERE a.workspace_id = $1
		  AND atq.status IN ('acked', 'suppressed')
		  AND atq.completed_at IS NOT NULL
		  AND ($3::timestamptz IS NULL OR (atq.completed_at, atq.id) < ($3::timestamptz, $4::uuid))
		ORDER BY atq.completed_at DESC, atq.id DESC
		LIMIT $2`,
		parseUUID(workspaceID), int32(limit+1), beforeAt, beforeID)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "failed to list agent tasks")
		return
	}
	defer rows.Close()

	actorType, actorID := h.resolveActor(r, requestUserID(r), workspaceID)
	memberRole := ""
	if member, err := h.getWorkspaceMember(r.Context(), actorID, workspaceID); err == nil {
		memberRole = member.Role
	}
	resolver := h.newActorIdentityResolver(r.Context(), workspaceID, actorType, actorID, memberRole)

	items := make([]AgentTaskFeedItem, 0, limit+1)
	cursors := make([]AgentTaskFeedCursor, 0, limit+1)
	for rows.Next() {
		var (
			id, agentID, issueID pgtype.UUID
			status               string
			completedAt          pgtype.Timestamptz
			triggerSummary       pgtype.Text
			issueTitle           pgtype.Text
			issueIdentifier      pgtype.Text
			chatTitle            pgtype.Text
		)
		if err := rows.Scan(&id, &agentID, &issueID, &status, &completedAt, &triggerSummary, &issueTitle, &issueIdentifier, &chatTitle); err != nil {
			continue
		}
		item := AgentTaskFeedItem{
			ID:              uuidToString(id),
			AgentID:         uuidToString(agentID),
			IssueID:         uuidToString(issueID),
			IssueIdentifier: textToPtr(issueIdentifier),
			IssueTitle:      textToPtr(issueTitle),
			ChatTitle:       textToPtr(chatTitle),
			Status:          status,
			CompletedAt:     timestampToPtr(completedAt),
			TriggerSummary:  textToPtr(triggerSummary),
		}
		applyActorIdentityToFeedItem(&item, resolver.resolve("agent", item.AgentID))
		items = append(items, item)
		cursors = append(cursors, AgentTaskFeedCursor{
			CompletedAt: completedAt.Time.Format(time.RFC3339Nano),
			ID:          uuidToString(id),
		})
	}
	if err := rows.Err(); err != nil {
		writeError(w, http.StatusInternalServerError, "failed to list agent tasks")
		return
	}

	hasMore := len(items) > limit
	if hasMore {
		items = items[:limit]
	}
	var next *AgentTaskFeedCursor
	if hasMore && limit > 0 {
		next = &cursors[limit-1]
	}

	writeJSON(w, http.StatusOK, AgentTaskFeedResponse{Tasks: items, HasMore: hasMore, NextCursor: next})
}

// AgentTaskStatsResponse powers the overview "tasks done" KPI. Counts are over
// ALL agent tasks in the workspace — issue tasks, chat tasks, and channel
// replies alike — so a completed channel reply counts as a finished task,
// matching the agent activity feed.
type AgentTaskStatsResponse struct {
	Completed int `json:"completed"`
	Failed    int `json:"failed"`
	Total     int `json:"total"`
}

// GetAgentTaskStats returns completed / failed / total agent-task counts for
// the workspace.
func (h *Handler) GetAgentTaskStats(w http.ResponseWriter, r *http.Request) {
	workspaceID := h.resolveWorkspaceID(r)
	if _, ok := h.workspaceMember(w, r, workspaceID); !ok {
		return
	}
	var resp AgentTaskStatsResponse
	err := h.DB.QueryRow(r.Context(), `
		SELECT
			COUNT(*) FILTER (WHERE atq.status = 'acked' AND atq.terminal_outcome = 'completed'),
			COUNT(*) FILTER (WHERE atq.status = 'acked' AND atq.terminal_outcome = 'failed'),
			COUNT(*)
		FROM agent_inbox_event atq
		JOIN agent a ON a.id = atq.agent_id
		WHERE a.workspace_id = $1`,
		parseUUID(workspaceID),
	).Scan(&resp.Completed, &resp.Failed, &resp.Total)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "failed to compute task stats")
		return
	}
	writeJSON(w, http.StatusOK, resp)
}
