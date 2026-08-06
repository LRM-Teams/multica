package handler

import (
	"context"
	"errors"
	"fmt"

	"github.com/jackc/pgx/v5"

	"github.com/multica-ai/multica/server/internal/service"
	db "github.com/multica-ai/multica/server/pkg/db/generated"
)

// envDispatchCloneAdapter is the production service.CloneDeps: it performs the
// derived-agent clone as raw SQL (the CreateDerivedEnvDispatchAgent sqlc method
// is absent pending the deferred pkg/db/generated regen). All methods run
// against the same db.DBTX, so CloneEnvDispatchAgentTx makes the clone atomic -
// a failure in any step rolls back the derived agent, skills copy, channel
// member swap, and binding update.
type envDispatchCloneAdapter struct {
	h  *Handler
	tx db.DBTX // nil = use h.DB; set = run inside CloneEnvDispatchAgentTx's tx
}

// Compile-time check: *envDispatchCloneAdapter satisfies service.CloneDeps.
var _ service.CloneDeps = (*envDispatchCloneAdapter)(nil)

func (a *envDispatchCloneAdapter) exec() db.DBTX {
	if a.tx != nil {
		return a.tx
	}
	return a.h.DB
}

// LoadSourceAgent reads the source agent's workspace membership and name. It is
// read-only; the source is never mutated. The approved executable configuration
// is copied server-side by CreateDerivedAgent (INSERT...SELECT), so it never
// crosses into Go memory - secrets/task state on the source are not touched.
func (a *envDispatchCloneAdapter) LoadSourceAgent(ctx context.Context, workspaceID, sourceAgentID string) (service.SourceAgent, error) {
	var s service.SourceAgent
	err := a.exec().QueryRow(ctx, `
SELECT id::text, workspace_id::text, name
FROM agent
WHERE id::text = $1 AND workspace_id::text = $2`,
		sourceAgentID, workspaceID).Scan(&s.ID, &s.WorkspaceID, &s.Name)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return service.SourceAgent{}, fmt.Errorf("source agent not found in workspace")
		}
		return service.SourceAgent{}, fmt.Errorf("load source agent: %w", err)
	}
	return s, nil
}

// CreateDerivedAgent inserts a new global agent that copies the source's approved
// executable configuration (name, instructions, runtime_config, model, etc.)
// server-side via INSERT...SELECT, then overrides runtime_id (the discovered
// online runtime) and source_agent_id (lineage). The source's id, runtime_id,
// and source_agent_id are never copied; the agent row carries no secret. The
// (workspace_id, source_agent_id) foreign key is satisfied because both come
// from the source row. Returns the derived agent ID.
func (a *envDispatchCloneAdapter) CreateDerivedAgent(ctx context.Context, in service.CreateDerivedAgentInput) (string, error) {
	var derivedID string
	err := a.exec().QueryRow(ctx, `
INSERT INTO agent (
    workspace_id, name, display_name, description, avatar_url, runtime_mode,
    runtime_config, runtime_id, visibility, max_concurrent_tasks, owner_id,
    instructions, custom_env, custom_args, mcp_config, model, thinking_level,
    source_agent_id
)
SELECT workspace_id, $3, display_name, description, avatar_url, runtime_mode,
       runtime_config, $1, visibility, max_concurrent_tasks, owner_id,
       instructions, custom_env, custom_args, mcp_config, model, thinking_level,
       $2
FROM agent
WHERE id::text = $4 AND workspace_id::text = $5
ON CONFLICT (workspace_id, name) DO UPDATE
SET runtime_id = EXCLUDED.runtime_id, updated_at = now()
WHERE agent.source_agent_id = EXCLUDED.source_agent_id
RETURNING id::text`,
		in.RuntimeID, in.SourceAgentID, in.Name, in.SourceAgentID, in.WorkspaceID).Scan(&derivedID)
	if err != nil {
		return "", fmt.Errorf("create derived agent: %w", err)
	}
	return derivedID, nil
}

// CopyApprovedSkills copies the source's agent_skill rows to the derived agent.
// Source skills are read, never mutated. ON CONFLICT makes it idempotent for
// retries.
func (a *envDispatchCloneAdapter) CopyApprovedSkills(ctx context.Context, _, sourceAgentID, derivedAgentID string) error {
	if _, err := a.exec().Exec(ctx, `
INSERT INTO agent_skill (agent_id, skill_id)
SELECT $1, skill_id FROM agent_skill WHERE agent_id = $2
ON CONFLICT DO NOTHING`,
		derivedAgentID, sourceAgentID); err != nil {
		return fmt.Errorf("copy approved skills: %w", err)
	}
	return nil
}

// ReplaceDispatchChannelMember swaps an existing source channel_agent_session
// row to the derived agent, preserving the chat_session. channel_member stays
// keyed by the source agent as the stable user-facing mention alias; the
// env-dispatch router resolves that source binding to the derived execution
// agent. If no source session exists yet this is a no-op, and provisioning
// creates the derived session directly.
func (a *envDispatchCloneAdapter) ReplaceDispatchChannelMember(ctx context.Context, channelID, sourceAgentID, derivedAgentID string) error {
	if _, err := a.exec().Exec(ctx, `
UPDATE channel_agent_session
SET agent_id = $1, created_at = now()
WHERE channel_id::text = $2 AND agent_id::text = $3`,
		derivedAgentID, channelID, sourceAgentID); err != nil {
		return fmt.Errorf("replace dispatch channel member: %w", err)
	}
	return nil
}

// SetBindingDerivedAgent persists the derived agent ID on the claimed
// env-dispatch binding. A zero rows-affected (binding gone) is an error so the
// orchestration layer can compensate.
func (a *envDispatchCloneAdapter) SetBindingDerivedAgent(ctx context.Context, bindingID, derivedAgentID string) error {
	ct, err := a.exec().Exec(ctx, `
UPDATE environment_agent_sandbox
SET derived_agent_id = $1, updated_at = now()
WHERE id::text = $2`,
		derivedAgentID, bindingID)
	if err != nil {
		return fmt.Errorf("set binding derived agent: %w", err)
	}
	if ct.RowsAffected() == 0 {
		return fmt.Errorf("binding not found: %s", bindingID)
	}
	return nil
}

// CloneEnvDispatchAgentTx runs service.CloneEnvDispatchAgent inside a single
// database transaction so the derived agent, skills copy, channel member swap,
// and binding update commit atomically (or roll back on any error). It is the
// production entry point for the derived-agent clone during env-dispatch
// first-address provisioning.
func CloneEnvDispatchAgentTx(ctx context.Context, h *Handler, in service.CloneEnvDispatchAgentInput) (string, error) {
	tx, err := h.TxStarter.Begin(ctx)
	if err != nil {
		return "", fmt.Errorf("begin derived-agent clone tx: %w", err)
	}
	defer tx.Rollback(ctx) // no-op after Commit
	derivedID, err := service.CloneEnvDispatchAgent(ctx, &envDispatchCloneAdapter{h: h, tx: tx}, in)
	if err != nil {
		return "", err
	}
	if err := tx.Commit(ctx); err != nil {
		return "", fmt.Errorf("commit derived-agent clone tx: %w", err)
	}
	return derivedID, nil
}
