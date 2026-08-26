package handler

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"strings"

	"github.com/jackc/pgx/v5"
	"github.com/multica-ai/multica/server/internal/researchrun"
)

type researchV6AgentLifecycleAdapter struct{ handler *Handler }

func (a *researchV6AgentLifecycleAdapter) CreateAgent(ctx context.Context, workspaceID, runID, idempotencyKey string, spec researchrun.V6AgentSpec) (string, error) {
	if a == nil || a.handler == nil || a.handler.TxStarter == nil || strings.TrimSpace(idempotencyKey) == "" || strings.TrimSpace(runID) == "" {
		return "", researchrun.ErrV6DirectorUnavailable
	}
	tx, err := a.handler.TxStarter.Begin(ctx)
	if err != nil {
		return "", err
	}
	defer tx.Rollback(ctx)
	if _, err = tx.Exec(ctx, `SELECT pg_advisory_xact_lock(hashtextextended($1,0))`, "research-v6-agent:"+workspaceID+":"+runID+":"+idempotencyKey); err != nil {
		return "", err
	}
	var existing string
	if err = tx.QueryRow(ctx, `SELECT resource_id::text FROM research_v6_runtime_effect WHERE workspace_id=$1::uuid AND session_id=$2::uuid AND effect_kind='create_agent' AND idempotency_key=$3`, workspaceID, runID, idempotencyKey).Scan(&existing); err == nil {
		return existing, tx.Commit(ctx)
	} else if !errors.Is(err, pgx.ErrNoRows) {
		return "", err
	}
	// The internal agent name must also be run-scoped: two runs reusing the
	// same director key would otherwise collide on the same derived name.
	sum := sha256.Sum256([]byte(runID + ":" + idempotencyKey))
	name := "ronaldo-" + hex.EncodeToString(sum[:])[:20]
	displayName := strings.TrimSpace(spec.Name)
	if displayName == "" {
		displayName = name
	}
	description := strings.TrimSpace(spec.Capability)
	mission := strings.TrimSpace(spec.MissionPrompt)
	var agentID string
	err = tx.QueryRow(ctx, `
		WITH template AS (
		  SELECT a.* FROM research_session session
		  JOIN research_director_assignment assignment
		    ON assignment.workspace_id=session.workspace_id
		   AND assignment.session_id=session.id
		   AND assignment.id=session.current_director_assignment_id
		  JOIN agent a
		    ON a.workspace_id=session.workspace_id
		   AND a.id=assignment.director_agent_id
		  WHERE session.workspace_id=$1::uuid AND session.id=$7::uuid
		    AND assignment.status='active' AND a.archived_at IS NULL
		)
		INSERT INTO agent(workspace_id,name,display_name,description,runtime_mode,runtime_config,runtime_id,owner_id,instructions,custom_env,custom_args,mcp_config,model,thinking_level,avatar_source)
		SELECT $1::uuid,$2,$3,$4,runtime_mode,runtime_config,runtime_id,owner_id,$5,custom_env,custom_args,
		       mcp_config,
		       COALESCE(NULLIF(NULLIF($6::jsonb->>'model',''),'default'),model),COALESCE(NULLIF($6::jsonb->>'thinking_level',''),thinking_level),'generated'
		FROM template RETURNING id::text`, workspaceID, name, displayName, description, mission, defaultJSONObject(spec.ModelConfig), runID).Scan(&agentID)
	if errors.Is(err, pgx.ErrNoRows) {
		return "", fmt.Errorf("%w: V6 team has no active Director template", researchrun.ErrV6DirectorUnavailable)
	}
	if err != nil {
		return "", fmt.Errorf("create V6 agent: %w", err)
	}
	if _, err = tx.Exec(ctx, `INSERT INTO research_v6_runtime_effect(workspace_id,session_id,effect_kind,idempotency_key,resource_id) VALUES($1::uuid,$2::uuid,'create_agent',$3,$4::uuid)`, workspaceID, runID, idempotencyKey, agentID); err != nil {
		return "", err
	}
	return agentID, tx.Commit(ctx)
}

func (a *researchV6AgentLifecycleAdapter) ArchiveAgent(ctx context.Context, workspaceID, agentID, reason string) error {
	if a == nil || a.handler == nil || a.handler.DB == nil {
		return researchrun.ErrV6DirectorUnavailable
	}
	_, err := a.handler.DB.Exec(ctx, `UPDATE agent SET archived_at=COALESCE(archived_at,now()),archived_by=COALESCE(archived_by,owner_id),description=CASE WHEN archived_at IS NULL THEN description||E'\n\nArchived by Ronaldo: '||$3 ELSE description END,updated_at=now() WHERE workspace_id=$1::uuid AND id=$2::uuid`, workspaceID, agentID, strings.TrimSpace(reason))
	return err
}

type researchV6WorkDispatcher interface {
	Dispatch(context.Context, researchrun.DispatchRequest) (researchrun.DispatchResult, error)
}

type researchV6InboxDispatchAdapter struct{ dispatcher researchV6WorkDispatcher }

func (a *researchV6InboxDispatchAdapter) DispatchV6Work(ctx context.Context, access researchrun.V6AttemptAccess, manifest researchrun.V6WorkManifest, idempotencyKey string) (string, error) {
	if a == nil || a.dispatcher == nil {
		return "", researchrun.ErrV6DirectorUnavailable
	}
	decoded, err := researchrun.DecodeV6Contract(manifest.Bytes, researchrun.V6ContractWorkManifest, nil)
	if err != nil {
		return "", err
	}
	var identity struct {
		ManifestID   string `json:"manifest_id"`
		ManifestHash string `json:"manifest_hash"`
	}
	if err = json.Unmarshal(decoded.Envelope, &identity); err != nil {
		return "", err
	}
	prompt, err := researchrun.BuildV6WorkDispatchPrompt(researchrun.V6WorkManifest{Bytes: decoded.Envelope})
	if err != nil {
		return "", err
	}
	result, err := a.dispatcher.Dispatch(ctx, researchrun.DispatchRequest{
		Run:       researchrun.Run{SessionID: access.RunID, WorkspaceID: access.WorkspaceID, OrchestratorVersion: researchrun.OrchestratorVersionV6},
		AttemptID: access.AttemptID, AgentID: access.AgentID, WorkItemID: access.WorkItemID,
		ManifestID: identity.ManifestID, ManifestHash: identity.ManifestHash,
		Prompt: prompt, Key: idempotencyKey,
	})
	return result.InboxTaskID, err
}

func defaultJSONObject(value json.RawMessage) json.RawMessage {
	if len(value) == 0 || !json.Valid(value) {
		return json.RawMessage(`{}`)
	}
	return value
}
