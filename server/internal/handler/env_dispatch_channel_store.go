package handler

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	db "github.com/multica-ai/multica/server/pkg/db/generated"
)

type envCollaborationTrigger struct {
	AgentID             string  `json:"agent_id"`
	Kind                string  `json:"kind"`
	ChannelID           string  `json:"channel_id"`
	ProjectID           string  `json:"project_id"`
	ChatSessionID       string  `json:"chat_session_id"`
	SourceMessageID     string  `json:"source_message_id"`
	ThreadRootMessageID *string `json:"thread_root_message_id,omitempty"`
	TaskID              string  `json:"task_id"`
	RuntimeID           string  `json:"runtime_id"`
}

type envAgentSandboxBinding struct {
	EnvID, ChannelID, AgentID, Status                                          string
	SandboxInstanceID, RuntimeID, DaemonID, SourceSandboxInstanceID, LastError *string
	SandboxConfig                                                              json.RawMessage
}

type envDispatchChannelStore struct{ db db.DBTX }

func (s envDispatchChannelStore) insertBinding(ctx context.Context, exec db.DBTX, binding envAgentSandboxBinding) error {
	_, err := exec.Exec(ctx, `
		INSERT INTO environment_agent_sandbox (
			env_id, channel_id, agent_id, status, sandbox_instance_id, runtime_id,
			daemon_id, source_sandbox_instance_id, sandbox_config, last_error
		) VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10)`,
		binding.EnvID, binding.ChannelID, binding.AgentID, binding.Status,
		binding.SandboxInstanceID, binding.RuntimeID, binding.DaemonID,
		binding.SourceSandboxInstanceID, binding.SandboxConfig, binding.LastError,
	)
	return err
}

func (s envDispatchChannelStore) listBindings(ctx context.Context, exec db.DBTX, envID string) ([]envAgentSandboxBinding, error) {
	rows, err := exec.Query(ctx, bindingSelect+` WHERE env_id = $1 ORDER BY agent_id`, envID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	bindings := make([]envAgentSandboxBinding, 0)
	for rows.Next() {
		binding, err := scanEnvAgentSandboxBinding(rows)
		if err != nil {
			return nil, err
		}
		bindings = append(bindings, binding)
	}
	return bindings, rows.Err()
}

func (s envDispatchChannelStore) binding(ctx context.Context, exec db.DBTX, envID, agentID string) (envAgentSandboxBinding, error) {
	return scanEnvAgentSandboxBinding(exec.QueryRow(ctx, bindingSelect+` WHERE env_id = $1 AND agent_id = $2`, envID, agentID))
}

func (s envDispatchChannelStore) claimProvisioning(ctx context.Context, exec db.DBTX, envID, agentID string) (bool, envAgentSandboxBinding, error) {
	binding, err := scanEnvAgentSandboxBinding(exec.QueryRow(ctx, `
		UPDATE environment_agent_sandbox
		SET status = 'provisioning', last_error = NULL, updated_at = now()
		WHERE env_id = $1 AND agent_id = $2 AND status IN ('pending', 'failed')
		RETURNING env_id::text, channel_id::text, agent_id::text, status,
			sandbox_instance_id::text, runtime_id::text, daemon_id::text,
			source_sandbox_instance_id::text, sandbox_config, last_error`, envID, agentID))
	if err == nil {
		return true, binding, nil
	}
	if !errors.Is(err, pgx.ErrNoRows) {
		return false, envAgentSandboxBinding{}, err
	}
	binding, err = scanEnvAgentSandboxBinding(exec.QueryRow(ctx, bindingSelect+` WHERE env_id = $1 AND agent_id = $2`, envID, agentID))
	if err != nil {
		return false, envAgentSandboxBinding{}, err
	}
	return false, binding, nil
}

func (s envDispatchChannelStore) markReady(ctx context.Context, exec db.DBTX, envID, agentID, sandboxInstanceID, runtimeID, daemonID string) error {
	_, err := exec.Exec(ctx, `
		UPDATE environment_agent_sandbox
		SET status = 'ready', sandbox_instance_id = $3, runtime_id = $4, daemon_id = $5,
			last_error = NULL, updated_at = now()
		WHERE env_id = $1 AND agent_id = $2 AND status = 'provisioning'`,
		envID, agentID, sandboxInstanceID, runtimeID, daemonID)
	return err
}

func (s envDispatchChannelStore) markFailed(ctx context.Context, exec db.DBTX, envID, agentID, message string) error {
	_, err := exec.Exec(ctx, `
		UPDATE environment_agent_sandbox
		SET status = 'failed', last_error = $3, updated_at = now()
		WHERE env_id = $1 AND agent_id = $2 AND status = 'provisioning'`, envID, agentID, message)
	return err
}

func (s envDispatchChannelStore) markDeleting(ctx context.Context, exec db.DBTX, envID string) error {
	// Mark pending/failed/ready bindings deleting so no new provisioning claim
	// succeeds (claimProvisioning only claims pending/failed). Provisioning
	// rows are intentionally left alone so an in-flight provisioner can reach a
	// terminal state the cleanup then reclaims; cleanup waits for that.
	_, err := exec.Exec(ctx, `
		UPDATE environment_agent_sandbox
		SET status = 'deleting', updated_at = now()
		WHERE env_id = $1 AND status NOT IN ('deleting', 'provisioning')`, envID)
	return err
}

func (s envDispatchChannelStore) loadTrigger(ctx context.Context, exec db.DBTX, envID, workspaceID string) (envCollaborationTrigger, error) {
	var raw []byte
	if err := exec.QueryRow(ctx, `
		SELECT collaboration_trigger
		FROM environment
		WHERE id = $1 AND workspace_id = $2`, envID, workspaceID).Scan(&raw); err != nil {
		return envCollaborationTrigger{}, err
	}

	var trigger envCollaborationTrigger
	if err := json.Unmarshal(raw, &trigger); err != nil {
		return envCollaborationTrigger{}, fmt.Errorf("decode collaboration trigger: %w", err)
	}
	if err := trigger.validate(); err != nil {
		return envCollaborationTrigger{}, err
	}

	var found bool
	err := exec.QueryRow(ctx, `
		SELECT EXISTS (
			SELECT 1
			FROM environment e
			JOIN project p ON p.env_id = e.id
			JOIN channel c ON c.project_id = p.id
			JOIN channel_member cm ON cm.channel_id = c.id
			WHERE e.id = $1 AND e.workspace_id = $2 AND c.id = $3 AND p.id = $4
				AND cm.member_type = 'agent' AND cm.member_id = $5
		)`, envID, workspaceID, trigger.ChannelID, trigger.ProjectID, trigger.AgentID).Scan(&found)
	if err != nil {
		return envCollaborationTrigger{}, err
	}
	if !found {
		return envCollaborationTrigger{}, errors.New("collaboration trigger does not belong to the environment channel roster")
	}
	return trigger, nil
}

func (s envDispatchChannelStore) saveTrigger(ctx context.Context, exec db.DBTX, envID string, trigger envCollaborationTrigger) error {
	if err := trigger.validate(); err != nil {
		return err
	}
	raw, err := json.Marshal(trigger)
	if err != nil {
		return fmt.Errorf("encode collaboration trigger: %w", err)
	}
	_, err = exec.Exec(ctx, `UPDATE environment SET collaboration_trigger = $2, updated_at = now() WHERE id = $1`, envID, raw)
	return err
}

func (trigger envCollaborationTrigger) validate() error {
	for name, raw := range map[string]string{
		"agent_id": trigger.AgentID, "channel_id": trigger.ChannelID, "project_id": trigger.ProjectID,
		"chat_session_id": trigger.ChatSessionID, "source_message_id": trigger.SourceMessageID,
		"task_id": trigger.TaskID, "runtime_id": trigger.RuntimeID,
	} {
		if _, err := uuid.Parse(raw); err != nil {
			return fmt.Errorf("collaboration trigger %s must be a UUID: %w", name, err)
		}
	}
	if trigger.ThreadRootMessageID != nil {
		if _, err := uuid.Parse(*trigger.ThreadRootMessageID); err != nil {
			return fmt.Errorf("collaboration trigger thread_root_message_id must be a UUID: %w", err)
		}
	}
	switch trigger.Kind {
	case "channel_message", "mention", "handoff", "continuation":
		return nil
	default:
		return fmt.Errorf("unsupported collaboration trigger kind %q", trigger.Kind)
	}
}

const bindingSelect = `
	SELECT env_id::text, channel_id::text, agent_id::text, status,
		sandbox_instance_id::text, runtime_id::text, daemon_id::text,
		source_sandbox_instance_id::text, sandbox_config, last_error
	FROM environment_agent_sandbox`

type envDispatchBindingRowScanner interface{ Scan(...any) error }

func scanEnvAgentSandboxBinding(row envDispatchBindingRowScanner) (envAgentSandboxBinding, error) {
	var binding envAgentSandboxBinding
	err := row.Scan(
		&binding.EnvID, &binding.ChannelID, &binding.AgentID, &binding.Status,
		&binding.SandboxInstanceID, &binding.RuntimeID, &binding.DaemonID,
		&binding.SourceSandboxInstanceID, &binding.SandboxConfig, &binding.LastError,
	)
	return binding, err
}
