package researchrun

import (
	"context"
	"errors"
	"fmt"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
)

// ensureV6BackingTaskTx gives an atomic Research Work Item the Task identity
// required by the frozen V6 result contract. The Work Item remains the V6
// execution authority; the Task is its one-to-one research/provenance record.
func ensureV6BackingTaskTx(ctx context.Context, tx pgx.Tx, workItemID string) (string, error) {
	parsedWorkID, err := uuid.Parse(workItemID)
	if err != nil {
		return "", fmt.Errorf("%w: invalid V6 Work Item ID", ErrInvalidContract)
	}
	taskID := uuid.NewSHA1(parsedWorkID, []byte("research-run-v6/backing-task")).String()
	var insertedID string
	err = tx.QueryRow(ctx, `INSERT INTO research_task(
		id,workspace_id,session_id,client_key,kind,objective,required_capability,expected_result,
		acceptance_criteria,priority,status,assigned_agent_id,goal_version,plan_version,max_attempts,
		timeout_seconds,ready_at,started_at,completed_at,terminal_reason,
		task_type,task_schema_id,task_payload,required_capabilities,
		expected_result_schema_id,work_item_id)
		SELECT $2::uuid,w.workspace_id,w.session_id,'v6-work:'||w.id::text,'custom',
			COALESCE(NULLIF(w.payload->>'mission_prompt',''),NULLIF(w.reason,''),w.kind),w.kind,
			w.expected_result_schema_id,w.payload,w.priority,research_v6_task_status_for_work_item(w.status),w.assigned_agent_id,w.goal_version,
			s.plan_version,LEAST(w.max_attempts,10),
			LEAST(GREATEST(COALESCE((s.run_config->>'task_timeout_seconds')::int,1800),30),86400),
			w.ready_at,w.started_at,w.completed_at,
			COALESCE(NULLIF(w.terminal_reason_detail,''),NULLIF(w.terminal_reason_code,''),''),
			w.kind,w.payload_schema_id,w.payload,jsonb_build_array(w.kind),w.expected_result_schema_id,w.id
		FROM research_work_item w JOIN research_session s ON s.id=w.session_id
		WHERE w.id=$1::uuid AND s.orchestrator_version='research-run-v6'
			AND w.expected_result_schema_id='atomic_result_submission'
		ON CONFLICT (session_id,goal_version,plan_version,client_key) DO NOTHING
		RETURNING id::text`, workItemID, taskID).Scan(&insertedID)
	if err != nil && !errors.Is(err, pgx.ErrNoRows) {
		return "", err
	}
	if errors.Is(err, pgx.ErrNoRows) {
		err = tx.QueryRow(ctx, `SELECT id::text FROM research_task WHERE work_item_id=$1::uuid`, workItemID).Scan(&taskID)
		if errors.Is(err, pgx.ErrNoRows) {
			return "", fmt.Errorf("%w: atomic V6 Work Item has no backing Task", ErrInvalidContract)
		}
		if err != nil {
			return "", err
		}
	} else {
		taskID = insertedID
	}
	var passportExists bool
	if err = tx.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM research_artifact_passport
		WHERE workspace_id=(SELECT workspace_id FROM research_task WHERE id=$1::uuid)
			AND session_id=(SELECT session_id FROM research_task WHERE id=$1::uuid) AND id=$1::uuid)`, taskID).Scan(&passportExists); err != nil {
		return "", err
	}
	if !passportExists {
		var workspaceID, runID string
		if err = tx.QueryRow(ctx, `SELECT workspace_id::text,session_id::text FROM research_task WHERE id=$1::uuid`, taskID).Scan(&workspaceID, &runID); err != nil {
			return "", err
		}
		if err = registerProductionTaskPassportTx(ctx, tx, workspaceID, runID, taskID, "", ArtifactAccessRaw); err != nil {
			return "", err
		}
	}
	return taskID, nil
}
