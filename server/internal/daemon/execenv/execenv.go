// Package execenv manages execution environments for the daemon.
package execenv

import (
	"encoding/json"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"strings"

	"github.com/multica-ai/multica/server/pkg/protocol"
)

type ManagerChannelContextForEnv struct {
	ID   string `json:"id"`
	Name string `json:"name"`
}

// TaskContextForEnv is the subset of task context used for writing context files.
type TaskContextForEnv struct {
	IssueID             string
	TriggerCommentID    string // comment that triggered this task (empty for on_assign)
	TriggerThreadID     string // root comment ID for the triggering thread; falls back to TriggerCommentID when empty
	NewCommentCount     int    // issue-wide comments since this agent's last run (excludes its own and the injected trigger)
	NewCommentsSince    string // RFC3339 anchor (last run's started_at) the count is measured from; empty on cold start
	PriorSessionResumed bool   // true when the daemon will resume an existing provider session for this task
	AssignmentSnapshot  *protocol.IssueAssignmentSnapshot
	// FreshSessionNoticeReason is non-empty when the canonical provider
	// session is intentionally new (for example, during cutover or after an
	// explicit reset). The reason is a transport signal; the rendered brief
	// uses one stable user-facing explanation for every reason.
	FreshSessionNoticeReason string

	AgentID           string // unique ID of the dispatched agent
	AgentName         string
	ManagedRole       string // structural platform-managed role; never inferred from display name
	AgentInstructions string // agent identity/persona instructions, injected into CLAUDE.md
	AgentRoot         string // the sole durable Agent workspace and cwd
	AgentSkills       []SkillContextForEnv
	AgentMemories     []MemoryContextForEnv
	ProjectID         string // issue's project, when present
	ChannelID         string // exact DM/channel surface, when present
	ChatSessionID     string // standalone FAB/bubble conversation, when present
	ProjectTitle      string // human-readable project title
	// MessageDelivery marks the durable Agent runtime that handles canonical
	// Message Deliveries. It is process configuration only: no Task, lease,
	// execution, session, or current-message identity appears here.
	MessageDelivery         bool
	AutopilotRunID          string // non-empty for autopilot run_only tasks
	AutopilotID             string
	AutopilotTitle          string
	AutopilotDescription    string
	AutopilotSource         string
	AutopilotTriggerPayload string
	QuickCreatePrompt       string // non-empty for quick-create tasks
	QuickCreateSource       *protocol.QuickCreateSourceContext
	AgentRadarPrompt        string // non-empty for proactive radar tasks
	// WorkspaceContext is the workspace-level system prompt (workspace.context
	// in the DB). Rendered into the brief as `## Workspace Context` when
	// non-empty so every agent in the workspace sees the same shared context,
	// regardless of issue / chat / autopilot / quick-create.
	WorkspaceContext string
	// RequestingUserName + RequestingUserProfileDescription describe the
	// human the agent is acting on behalf of. v1 sources them from the
	// runtime owner (the user who registered the daemon). Rendered into the
	// brief as the `## Requesting User` section only when description is
	// non-empty — empty means the user opted out of injecting profile
	// context and the agent stays anonymous-user mode.
	RequestingUserName               string
	RequestingUserProfileDescription string
	// Initiator* identify the actor who triggered THIS task (the real
	// requester) as distinct from the runtime owner. Rendered into the brief
	// as `## Task Initiator` when a name is present; InitiatorEmail is shown
	// only for member initiators. Empty for on-assign / autopilot /
	// quick-create tasks, which have no attributable human initiator. See
	// MUL-2645.
	InitiatorType  string
	InitiatorID    string
	InitiatorName  string
	InitiatorEmail string
	// SkillDirSlugByName is materialize-only: maps logical skill name → actual
	// on-disk directory slug after collision resolution. Brief skill index,
	// writer, and receipt must share these values (Barry resolved-plan gate).
	SkillDirSlugByName map[string]string
}

type MemoryContextForEnv struct {
	Name        string
	Content     string
	Scope       string
	SubjectType string
	SubjectID   string
}

// SkillContextForEnv represents a skill to be written into the execution environment.
type SkillContextForEnv struct {
	Name        string
	Description string
	Content     string
	Files       []SkillFileContextForEnv
}

// SkillFileContextForEnv represents a supporting file within a skill.
type SkillFileContextForEnv struct {
	Path    string
	Content string
}

// Environment represents a prepared, isolated execution environment.
type Environment struct {
	// AgentRoot is both the durable workspace and the subprocess cwd.
	AgentRoot string
	// CodexHome is the provider-private CODEX_HOME below AgentRoot.
	CodexHome string

	logger *slog.Logger // for cleanup logging
}

// ReuseParams describes the inputs used to refresh a durable Agent workspace.
type ReuseParams struct {
	AgentRoot    string
	Provider     string
	CodexVersion string // only used when Provider == "codex"
	// McpConfig is the agent's saved `mcp_config` JSON. Reused on reuse so a
	// freshly-saved managed set re-materialises into the wrapper before the
	// task starts — without this a stale wrapper from a prior run would keep
	// the old MCP set in play.
	McpConfig json.RawMessage
	Task      TaskContextForEnv // refreshed context files / skills
}

// Reuse wraps an existing workdir into an Environment and refreshes context files.
// Returns nil if the workdir does not exist (caller should fall back to Prepare).
func Reuse(params ReuseParams, logger *slog.Logger) *Environment {
	agentRoot := strings.TrimSpace(params.AgentRoot)
	if agentRoot == "" {
		return nil
	}
	if _, err := os.Stat(agentRoot); err != nil {
		return nil
	}
	env := &Environment{
		AgentRoot: agentRoot,
		logger:    logger,
	}

	// Roll back the previous dispatch's sidecar writes before refreshing.
	// On reuse the workdir still holds the prior run's issue_context.md and
	// skill directories; without clearing them first, writeSkillFiles sees
	// its own earlier output occupying the canonical slug and falls back to
	// a collision-free sibling (issue-review, issue-review-multica,
	// issue-review-multica-2, …), accumulating a fresh duplicate on every
	// re-dispatch to the same issue. allocateCollisionFreeSkillDir exists to
	// avoid user-owned skill dirs, not our own prior writes, so we undo them
	// via the prior manifest first and let the
	// refresh below re-create each skill at its natural slug. This also brings
	// the standard providers in line with the Codex path, where
	// hydrateCodexSkills already wipes its skills dir before re-hydrating.
	//
	// Two steps, in order:
	//   1. removeReusedManagedSkillDirs reclaims the platform's own skill
	//      directories even when a prior-run agent left a file inside one.
	//      CleanupSidecars alone can't do this — it preserves any recorded dir
	//      the agent populated,
	//      which would otherwise keep the canonical slug occupied and push the
	//      refresh back to issue-review-multica.
	//   2. CleanupSidecars rolls back the remaining context files and the
	//      manifest itself.
	//
	// No-op when no prior manifest exists.
	if err := removeReusedManagedSkillDirs(agentRoot, skillsDirPath(agentRoot, params.Provider)); err != nil {
		logger.Warn("execenv: reclaim managed skill dirs on reuse failed", "error", err)
	}
	if err := CleanupSidecars(agentRoot); err != nil {
		logger.Warn("execenv: roll back prior sidecars on reuse failed", "error", err)
	}

	// Refresh context files (issue_context.md, skills). Reuse tracks a
	// fresh manifest under AgentRoot so a later CleanupSidecars sees
	// the up-to-date list of writes (an old manifest from a prior run
	// would otherwise reference files this Reuse no longer creates). For
	manifest := &sidecarManifest{}
	if err := writeContextFiles(agentRoot, params.Provider, params.Task, manifest); err != nil {
		logger.Warn("execenv: refresh context files failed", "error", err)
	}
	if err := writeSidecarManifest(agentRoot, manifest); err != nil {
		logger.Warn("execenv: refresh sidecar manifest failed", "error", err)
	}
	if err := mirrorBoundSkillsToAgentEnabled(params.Task.AgentRoot, params.Task.AgentSkills); err != nil {
		logger.Warn("execenv: refresh bound-skill mirror failed (non-fatal)", "error", err)
	}

	// Refresh the provider-private Codex home below AgentRoot to ensure
	// config (especially sandbox/network access) is up to date.
	if params.Provider == "codex" {
		codexHome := filepath.Join(agentRoot, "codex-home")
		if err := prepareCodexHomeWithOpts(codexHome, CodexHomeOptions{CodexVersion: params.CodexVersion, WritableRoots: codexWritableRoots(params.Task)}, logger); err != nil {
			logger.Warn("execenv: refresh codex-home failed", "error", err)
		} else {
			env.CodexHome = codexHome
			if err := hydrateCodexSkills(codexHome, params.Task.AgentSkills, logger); err != nil {
				logger.Warn("execenv: refresh codex skills failed", "error", err)
			}
		}
	}

	logger.Info("execenv: reusing agent workspace", "agent_root", agentRoot)
	return env
}

// hydrateCodexSkills populates the Agent-scoped CODEX_HOME/skills directory with
// both user-installed skills (from the shared ~/.codex/skills/) and
// workspace-assigned skills. Workspace skills win on name conflict — they are
// written last and seedUserCodexSkills already pre-filters their names.
//
// The skills directory is wiped first so two stale-state classes that the
// Reuse path would otherwise leak are gone:
//
//   - A name now claimed by a workspace skill that previously held only a
//     user-seeded copy — support files from the user version would otherwise
//     linger under the workspace skill's directory.
//   - A user skill removed from the shared ~/.codex/skills/ since the last
//     run — its old contents would otherwise remain visible to the codex
//     CLI.
//
// Codex is the only runtime that needs this two-stage hydration because the
// daemon sets CODEX_HOME below AgentRoot, isolating the CLI from the
// user's real ~/.codex/. Other runtimes leave HOME untouched and discover
// user-level skills natively (see context.go for the workdir-local paths
// they use for workspace skills).
func codexWritableRoots(task TaskContextForEnv) []string {
	root := strings.TrimSpace(task.AgentRoot)
	if root == "" {
		return nil
	}
	return []string{root}
}

func hydrateCodexSkills(codexHome string, workspaceSkills []SkillContextForEnv, logger *slog.Logger) error {
	skillsDir := filepath.Join(codexHome, "skills")
	if err := os.RemoveAll(skillsDir); err != nil {
		return fmt.Errorf("clear codex skills dir: %w", err)
	}
	if err := seedUserCodexSkills(codexHome, workspaceSkills, logger); err != nil {
		logger.Warn("execenv: seed user codex skills failed", "error", err)
	}
	if len(workspaceSkills) == 0 {
		return nil
	}
	// Codex skills live under AgentRoot/codex-home and do not need sidecar
	// manifest tracking because this directory is fully daemon-managed.
	return writeSkillFiles(skillsDir, workspaceSkills, nil)
}
