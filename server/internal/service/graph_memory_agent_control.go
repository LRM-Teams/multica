package service

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/jackc/pgx/v5/pgxpool"
)

const (
	GraphMemoryModeInject = "inject"
	GraphMemoryModeAgent  = "agent"
)

var ErrGraphMemoryAgentUnavailable = errors.New("graph memory agent unavailable")

const graphMemoryAgentDefaultNodeOptions = "--use-openssl-ca"

func mergeGraphMemoryAgentCustomEnv(raw []byte) ([]byte, error) {
	env := map[string]string{}
	trimmed := strings.TrimSpace(string(raw))
	if trimmed != "" && trimmed != "null" {
		if err := json.Unmarshal(raw, &env); err != nil {
			return nil, fmt.Errorf("decode managed graph memory agent custom env: %w", err)
		}
	}
	if _, exists := env["NODE_OPTIONS"]; !exists {
		env["NODE_OPTIONS"] = graphMemoryAgentDefaultNodeOptions
	}
	return json.Marshal(env)
}

// GraphMemoryAgentChannelStatus is the externally observable provisioning state.
type GraphMemoryAgentChannelStatus struct {
	ChannelID     string
	EffectiveMode string
	Status        string
	BlockedReason string
	AgentID       string
	RuntimeID     string
}

// GraphMemoryAgentControlPlane owns the durable per-channel identity and state
// transitions. Callers never mutate graph-memory agent tables directly.
type GraphMemoryAgentControlPlane interface {
	ReconcileChannel(context.Context, string, string) (GraphMemoryAgentChannelStatus, error)
	ObserveActivity(context.Context, string, string, time.Time) error
	ResetState(context.Context, string, string) error
}

type PostgresGraphMemoryAgentControlPlane struct {
	pool *pgxpool.Pool
	now  func() time.Time
}

func NewPostgresGraphMemoryAgentControlPlane(pool *pgxpool.Pool) *PostgresGraphMemoryAgentControlPlane {
	return &PostgresGraphMemoryAgentControlPlane{pool: pool, now: time.Now}
}

// EffectiveGraphMemoryMode resolves channel override over the workspace
// default. Non-graph workspaces never activate the managed agent.
func EffectiveGraphMemoryMode(memoryType, workspaceMode, channelOverride string) string {
	if memoryType != "graph" {
		return GraphMemoryModeInject
	}
	if channelOverride == GraphMemoryModeInject || channelOverride == GraphMemoryModeAgent {
		return channelOverride
	}
	if workspaceMode == GraphMemoryModeInject {
		return GraphMemoryModeInject
	}
	return GraphMemoryModeAgent
}

func (c *PostgresGraphMemoryAgentControlPlane) ReconcileChannel(ctx context.Context, workspaceID, channelID string) (GraphMemoryAgentChannelStatus, error) {
	if c == nil || c.pool == nil {
		return GraphMemoryAgentChannelStatus{}, ErrGraphMemoryAgentUnavailable
	}
	tx, err := c.pool.BeginTx(ctx, pgx.TxOptions{})
	if err != nil {
		return GraphMemoryAgentChannelStatus{}, err
	}
	defer tx.Rollback(ctx)
	if _, err = tx.Exec(ctx, `SELECT pg_advisory_xact_lock(hashtextextended($1, 0))`, "graph-memory-agent:"+channelID); err != nil {
		return GraphMemoryAgentChannelStatus{}, err
	}

	var channelName, kind, override, memoryType, workspaceMode, memoryAgentModel, memoryAgentThinking string
	var maxSeq int64
	var runtimeID pgtype.UUID
	var archivedAt pgtype.Timestamptz
	err = tx.QueryRow(ctx, `
		SELECT c.name, c.kind, c.graph_memory_mode_override, c.archived_at,
		       COALESCE(p.memory_type, 'legacy'), COALESCE(p.graph_memory_mode, 'agent'), p.memory_agent_runtime_id,
		       COALESCE(p.memory_agent_model,''),COALESCE(p.memory_agent_thinking,''),
		       COALESCE((SELECT max(seq) FROM channel_message WHERE channel_id=c.id), 0)
		FROM channel c
		LEFT JOIN graph_memory_profile p ON p.workspace_id=c.workspace_id
		WHERE c.id=$1::uuid AND c.workspace_id=$2::uuid
		FOR UPDATE OF c`, channelID, workspaceID).Scan(
		&channelName, &kind, &override, &archivedAt, &memoryType, &workspaceMode, &runtimeID, &memoryAgentModel, &memoryAgentThinking, &maxSeq,
	)
	if err != nil {
		return GraphMemoryAgentChannelStatus{}, err
	}
	effective := EffectiveGraphMemoryMode(memoryType, workspaceMode, override)
	status := GraphMemoryAgentChannelStatus{ChannelID: channelID, EffectiveMode: effective}
	var existingAgentID pgtype.UUID
	var previousStatus string
	_ = tx.QueryRow(ctx, `SELECT agent_id, status FROM graph_memory_channel_agent WHERE channel_id=$1::uuid`, channelID).Scan(&existingAgentID, &previousStatus)

	if effective != GraphMemoryModeAgent || kind != "group" || archivedAt.Valid {
		if err = terminalizeGraphMemoryAgentRunTx(ctx, tx, channelID, "cancelled"); err != nil {
			return status, err
		}
		if existingAgentID.Valid {
			if _, err = tx.Exec(ctx, `DELETE FROM channel_member WHERE channel_id=$1::uuid AND member_type='agent' AND member_id=$2`, channelID, existingAgentID); err != nil {
				return status, err
			}
		}
		if _, err = tx.Exec(ctx, `UPDATE graph_memory_channel_agent SET status='inactive', blocked_reason='', updated_at=now(), config_version=config_version+1 WHERE channel_id=$1::uuid`, channelID); err != nil {
			return status, err
		}
		status.Status = "inactive"
		return status, tx.Commit(ctx)
	}

	var sponsorID pgtype.UUID
	if err = tx.QueryRow(ctx, `
		SELECT COALESCE(
		  (SELECT cm.member_id FROM channel_member cm
		   WHERE cm.channel_id=$2::uuid AND cm.workspace_id=$1::uuid
		     AND cm.member_type='user' AND cm.role='owner' LIMIT 1),
		  (SELECT m.user_id FROM member m
		   WHERE m.workspace_id=$1::uuid AND m.role='owner' LIMIT 1)
		)`, workspaceID, channelID).Scan(&sponsorID); err != nil {
		return status, err
	}
	if !runtimeID.Valid {
		err = tx.QueryRow(ctx, `
			SELECT r.id FROM agent a
			JOIN agent_runtime r ON r.id=a.runtime_id AND r.workspace_id=a.workspace_id
			WHERE a.workspace_id=$1::uuid AND a.owner_id=$2 AND a.archived_at IS NULL
			  AND r.provider='pi' AND r.status='online'
			ORDER BY CASE WHEN a.managed_role IS NULL THEN 0 ELSE 1 END, a.created_at
			LIMIT 1`, workspaceID, sponsorID).Scan(&runtimeID)
	}
	if err == nil && runtimeID.Valid {
		err = tx.QueryRow(ctx, `SELECT id FROM agent_runtime WHERE id=$1 AND workspace_id=$2::uuid AND provider='pi' AND status='online'`, runtimeID, workspaceID).Scan(&runtimeID)
	}
	if err != nil || !runtimeID.Valid {
		return c.setBlocked(ctx, tx, status, workspaceID, channelID, channelName, sponsorID, existingAgentID, previousStatus, maxSeq, "eligible Pi runtime with directed steering is unavailable")
	}

	handle := "memory-" + strings.ReplaceAll(channelID, "-", "")
	if len(handle) > 32 {
		handle = handle[:32]
	}
	displayName := "Memory · #" + channelName
	agentID := existingAgentID
	if !agentID.Valid {
		err = tx.QueryRow(ctx, `
			INSERT INTO agent (
			  workspace_id, name, display_name, description, runtime_mode,
			  runtime_config, runtime_id, owner_id, managed_role, instructions,
			  model, thinking_level, avatar_source
			)
			SELECT $1::uuid, $2, $3, 'Managed channel Graph Memory Agent', r.runtime_mode,
			       '{}'::jsonb, r.id, $4, 'graph_memory_channel',
			       'Explore long-term Graph Memory for this channel. Use only delegated channel tools.',
			       $6, NULLIF($7,''), 'generated'
			FROM agent_runtime r WHERE r.id=$5 AND r.workspace_id=$1::uuid
			RETURNING id`, workspaceID, handle, displayName, sponsorID, runtimeID, memoryAgentModel, memoryAgentThinking).Scan(&agentID)
		if err != nil {
			return status, fmt.Errorf("provision graph memory agent: %w", err)
		}
	}
	var customEnvRaw []byte
	if err = tx.QueryRow(ctx, `SELECT custom_env FROM agent WHERE id=$1 FOR UPDATE`, agentID).Scan(&customEnvRaw); err != nil {
		return status, fmt.Errorf("load graph memory agent custom env: %w", err)
	}
	managedCustomEnv, err := mergeGraphMemoryAgentCustomEnv(customEnvRaw)
	if err != nil {
		return status, err
	}
	_, err = tx.Exec(ctx, `
		INSERT INTO graph_memory_channel_agent (
		  channel_id, workspace_id, agent_id, runtime_id, sponsor_user_id,
		  handle, display_name, status, blocked_reason
		) VALUES ($1::uuid,$2::uuid,$3,$4,$5,$6,$7,'active','')
		ON CONFLICT (channel_id) DO UPDATE SET
		  agent_id=EXCLUDED.agent_id, runtime_id=EXCLUDED.runtime_id,
		  sponsor_user_id=EXCLUDED.sponsor_user_id, handle=EXCLUDED.handle,
		  display_name=EXCLUDED.display_name, status='active', blocked_reason='',
		  updated_at=now(), config_version=graph_memory_channel_agent.config_version+1`,
		channelID, workspaceID, agentID, runtimeID, sponsorID, handle, displayName)
	if err != nil {
		return status, err
	}
	if _, err = tx.Exec(ctx, `
		UPDATE agent SET owner_id=$2,runtime_id=$3,model=$4,thinking_level=NULLIF($5,''),custom_env=$6,updated_at=now()
		WHERE id=$1`, agentID, sponsorID, runtimeID, memoryAgentModel, memoryAgentThinking, managedCustomEnv); err != nil {
		return status, err
	}
	if _, err = tx.Exec(ctx, `INSERT INTO channel_member(channel_id,workspace_id,member_type,member_id,role) VALUES($1::uuid,$2::uuid,'agent',$3,'member') ON CONFLICT DO NOTHING`, channelID, workspaceID, agentID); err != nil {
		return status, err
	}
	if _, err = tx.Exec(ctx, `INSERT INTO graph_memory_agent_state(channel_id,consumed_seq) VALUES($1::uuid,$2) ON CONFLICT (channel_id) DO NOTHING`, channelID, maxSeq); err != nil {
		return status, err
	}
	if previousStatus == "blocked" {
		if err = insertGraphMemoryAgentTransitionMessage(ctx, tx, workspaceID, channelID, "Graph Memory Agent recovered and is active."); err != nil {
			return status, err
		}
	}
	if _, err = tx.Exec(ctx, `UPDATE graph_memory_channel_agent SET last_notified_status='active' WHERE channel_id=$1::uuid`, channelID); err != nil {
		return status, err
	}
	status.Status, status.AgentID, status.RuntimeID = "active", graphMemoryAgentUUIDText(agentID), graphMemoryAgentUUIDText(runtimeID)
	return status, tx.Commit(ctx)
}

func (c *PostgresGraphMemoryAgentControlPlane) setBlocked(ctx context.Context, tx pgx.Tx, status GraphMemoryAgentChannelStatus, workspaceID, channelID, channelName string, sponsorID, agentID pgtype.UUID, previousStatus string, maxSeq int64, reason string) (GraphMemoryAgentChannelStatus, error) {
	if err := terminalizeGraphMemoryAgentRunTx(ctx, tx, channelID, "checkpointed"); err != nil {
		return status, err
	}
	handle := "memory-" + strings.ReplaceAll(channelID, "-", "")
	if len(handle) > 32 {
		handle = handle[:32]
	}
	_, err := tx.Exec(ctx, `
		INSERT INTO graph_memory_channel_agent(channel_id,workspace_id,agent_id,sponsor_user_id,handle,display_name,status,blocked_reason)
		VALUES($1::uuid,$2::uuid,$3,$4,$5,$6,'blocked',$7)
		ON CONFLICT(channel_id) DO UPDATE SET sponsor_user_id=EXCLUDED.sponsor_user_id,status='blocked',blocked_reason=EXCLUDED.blocked_reason,updated_at=now(),config_version=graph_memory_channel_agent.config_version+1`,
		channelID, workspaceID, agentID, sponsorID, handle, "Memory · #"+channelName, reason)
	if err != nil {
		return status, err
	}
	if agentID.Valid {
		if _, err = tx.Exec(ctx, `UPDATE agent SET owner_id=$2,updated_at=now() WHERE id=$1`, agentID, sponsorID); err != nil {
			return status, err
		}
	}
	if _, err = tx.Exec(ctx, `INSERT INTO graph_memory_agent_state(channel_id,consumed_seq) VALUES($1::uuid,$2) ON CONFLICT(channel_id) DO NOTHING`, channelID, maxSeq); err != nil {
		return status, err
	}
	if previousStatus != "blocked" {
		if err = insertGraphMemoryAgentTransitionMessage(ctx, tx, workspaceID, channelID, "Graph Memory Agent is blocked: "+reason); err != nil {
			return status, err
		}
	}
	_, err = tx.Exec(ctx, `UPDATE graph_memory_channel_agent SET last_notified_status='blocked' WHERE channel_id=$1::uuid`, channelID)
	if err != nil {
		return status, err
	}
	status.Status, status.BlockedReason, status.AgentID = "blocked", reason, graphMemoryAgentUUIDText(agentID)
	return status, tx.Commit(ctx)
}

func terminalizeGraphMemoryAgentRunTx(ctx context.Context, tx pgx.Tx, channelID, runStatus string) error {
	if runStatus != "checkpointed" && runStatus != "cancelled" {
		return fmt.Errorf("invalid graph memory agent run status %q", runStatus)
	}
	if _, err := tx.Exec(ctx, `
		UPDATE graph_memory_agent_trajectory trajectory
		SET status='checkpointed',state_patch=jsonb_build_object(
		  'objective',state.objective,'observations',state.observations,
		  'rejected_branches',state.rejected_branches,'open_questions',state.open_questions,
		  'candidate_node_ids',state.candidate_node_ids,'viewed_node_ids',state.viewed_node_ids,
		  'pending_targets',state.pending_targets,'next_hint',state.next_hint
		),finished_at=now()
		FROM graph_memory_agent_run run,graph_memory_agent_state state
		WHERE trajectory.run_id=run.id AND run.channel_id=$1::uuid
		  AND state.channel_id=run.channel_id AND run.status='running' AND trajectory.status='active'`, channelID); err != nil {
		return err
	}
	if _, err := tx.Exec(ctx, `UPDATE graph_memory_agent_run SET status=$2,finished_at=now() WHERE channel_id=$1::uuid AND status='running'`, channelID, runStatus); err != nil {
		return err
	}
	_, err := tx.Exec(ctx, `
		UPDATE graph_memory_agent_state SET active_run_id=NULL,lease_expires_at=NULL,
		 state_version=state_version+1,updated_at=now()
		WHERE channel_id=$1::uuid AND active_run_id IS NOT NULL`, channelID)
	return err
}

func insertGraphMemoryAgentTransitionMessage(ctx context.Context, tx pgx.Tx, workspaceID, channelID, content string) error {
	_, err := tx.Exec(ctx, `INSERT INTO channel_message(channel_id,workspace_id,author_type,author_name,content,kind) VALUES($1::uuid,$2::uuid,'system','System',$3,'system')`, channelID, workspaceID, content)
	return err
}

// ObserveActivity renews a lease without moving the durable message cursor.
func (c *PostgresGraphMemoryAgentControlPlane) ObserveActivity(ctx context.Context, workspaceID, channelID string, at time.Time) error {
	if c == nil || c.pool == nil {
		return ErrGraphMemoryAgentUnavailable
	}
	tag, err := c.pool.Exec(ctx, `
		UPDATE graph_memory_agent_state s SET
		  lease_expires_at=$3 + make_interval(secs => p.memory_agent_idle_grace_seconds), updated_at=now()
		FROM graph_memory_channel_agent a
		JOIN graph_memory_profile p ON p.workspace_id=a.workspace_id
		WHERE s.channel_id=a.channel_id AND a.channel_id=$1::uuid AND a.workspace_id=$2::uuid AND a.status='active'`, channelID, workspaceID, at.UTC())
	if err != nil {
		return err
	}
	if tag.RowsAffected() == 0 {
		return ErrGraphMemoryAgentUnavailable
	}
	return nil
}

func (c *PostgresGraphMemoryAgentControlPlane) ResetState(ctx context.Context, workspaceID, channelID string) error {
	if c == nil || c.pool == nil {
		return ErrGraphMemoryAgentUnavailable
	}
	tx, err := c.pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback(ctx)
	if _, err = tx.Exec(ctx, `UPDATE graph_memory_agent_run SET status='state_reset',finished_at=now() WHERE channel_id=$1::uuid AND workspace_id=$2::uuid AND status='running'`, channelID, workspaceID); err != nil {
		return err
	}
	tag, err := tx.Exec(ctx, `
		UPDATE graph_memory_agent_state SET
		 consumed_seq=COALESCE((SELECT max(seq) FROM channel_message WHERE channel_id=$1::uuid),0),
		 graph_version=0,objective='',observations='[]',rejected_branches='[]',open_questions='[]',
		 candidate_node_ids='[]',viewed_node_ids='[]',pending_targets='[]',posted_fingerprints='[]',
		 next_hint='',lease_expires_at=NULL,active_run_id=NULL,state_version=state_version+1,updated_at=now()
		WHERE channel_id=$1::uuid AND EXISTS(SELECT 1 FROM graph_memory_channel_agent WHERE channel_id=$1::uuid AND workspace_id=$2::uuid)`, channelID, workspaceID)
	if err != nil {
		return err
	}
	if tag.RowsAffected() == 0 {
		return ErrGraphMemoryAgentUnavailable
	}
	return tx.Commit(ctx)
}

func graphMemoryAgentUUIDText(id pgtype.UUID) string {
	if !id.Valid {
		return ""
	}
	return fmt.Sprintf("%x-%x-%x-%x-%x", id.Bytes[0:4], id.Bytes[4:6], id.Bytes[6:8], id.Bytes[8:10], id.Bytes[10:16])
}
