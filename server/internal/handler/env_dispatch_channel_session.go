package handler

import (
	"context"
	"errors"
	"fmt"

	"github.com/jackc/pgx/v5"
)

type envDispatchChannelSessionInput struct {
	WorkspaceID string
	ProjectID   string
	ChannelID   string
	AgentID     string
	CreatorID   string
	RuntimeID   string
}

// ensureEnvDispatchChannelSession creates the canonical chat session for one
// env-dispatch channel/agent pair. Concurrent callers converge on the unique
// channel_agent_session row, and retries reuse it only when its execution
// identity still matches the derived agent and sandbox runtime.
func (h *Handler) ensureEnvDispatchChannelSession(ctx context.Context, in envDispatchChannelSessionInput) (string, bool, error) {
	if in.WorkspaceID == "" || in.ProjectID == "" || in.ChannelID == "" ||
		in.AgentID == "" || in.CreatorID == "" || in.RuntimeID == "" {
		return "", false, fmt.Errorf("validation_failed: env-dispatch channel session identity is required")
	}
	tx, err := h.TxStarter.Begin(ctx)
	if err != nil {
		return "", false, fmt.Errorf("begin env-dispatch channel session: %w", err)
	}
	defer tx.Rollback(ctx) // no-op after Commit

	load := func() (string, error) {
		var sessionID, workspaceID, projectID, agentID, runtimeID string
		err := tx.QueryRow(ctx, `
SELECT session.id::text, session.workspace_id::text,
       COALESCE(session.project_id::text, ''), session.agent_id::text,
       COALESCE(session.runtime_id::text, '')
FROM channel_agent_session binding
JOIN chat_session session ON session.id = binding.chat_session_id
WHERE binding.channel_id = $1 AND binding.agent_id = $2`,
			in.ChannelID, in.AgentID).Scan(&sessionID, &workspaceID, &projectID, &agentID, &runtimeID)
		if err != nil {
			return "", err
		}
		if workspaceID != in.WorkspaceID || projectID != in.ProjectID ||
			agentID != in.AgentID || runtimeID != in.RuntimeID {
			return "", fmt.Errorf("env-dispatch channel session identity mismatch")
		}
		return sessionID, nil
	}

	if sessionID, err := load(); err == nil {
		if err := tx.Commit(ctx); err != nil {
			return "", false, fmt.Errorf("commit existing env-dispatch channel session: %w", err)
		}
		return sessionID, false, nil
	} else if !errors.Is(err, pgx.ErrNoRows) {
		return "", false, err
	}

	var candidateID string
	if err := tx.QueryRow(ctx, `
INSERT INTO chat_session (
    workspace_id, project_id, agent_id, creator_id, title, runtime_id
) VALUES ($1, $2, $3, $4, 'env-dispatch', $5)
RETURNING id::text`,
		in.WorkspaceID, in.ProjectID, in.AgentID, in.CreatorID, in.RuntimeID).Scan(&candidateID); err != nil {
		return "", false, fmt.Errorf("create env-dispatch chat session: %w", err)
	}

	tag, err := tx.Exec(ctx, `
INSERT INTO channel_agent_session (channel_id, agent_id, chat_session_id)
VALUES ($1, $2, $3)
ON CONFLICT (channel_id, agent_id) DO NOTHING`,
		in.ChannelID, in.AgentID, candidateID)
	if err != nil {
		return "", false, fmt.Errorf("create env-dispatch channel session: %w", err)
	}
	if tag.RowsAffected() == 0 {
		if _, err := tx.Exec(ctx, `DELETE FROM chat_session WHERE id = $1`, candidateID); err != nil {
			return "", false, fmt.Errorf("delete losing env-dispatch chat session: %w", err)
		}
		winnerID, err := load()
		if err != nil {
			return "", false, fmt.Errorf("load winning env-dispatch channel session: %w", err)
		}
		if err := tx.Commit(ctx); err != nil {
			return "", false, fmt.Errorf("commit winning env-dispatch channel session: %w", err)
		}
		return winnerID, false, nil
	}

	if err := tx.Commit(ctx); err != nil {
		return "", false, fmt.Errorf("commit env-dispatch channel session: %w", err)
	}
	return candidateID, true, nil
}
