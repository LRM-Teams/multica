package researchrun

import (
	"context"
	"encoding/json"
	"fmt"
	"sort"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
)

type v6IntegrationRoundInput struct {
	ArtifactID, VersionID, ContentHash, AgentID string
}

// reserveReadyV6IntegrationRoundsTx turns a planner-authored integrate Task
// into an executable, immutable comparison round only after every dependency
// has an accepted Result Artifact. The round contract is frozen into the Task
// before dispatch, so an Agent never invents its own round or input set.
func reserveReadyV6IntegrationRoundsTx(ctx context.Context, tx pgx.Tx, state acceptedResultState, throughSequence int64) error {
	rows, err := tx.Query(ctx, `SELECT task.id::text FROM research_task task
		WHERE task.workspace_id=$1::uuid AND task.session_id=$2::uuid AND task.goal_version=$3 AND task.plan_version=$4
		AND task.kind='integrate' AND task.expected_result='research_integration_v6' AND task.status='ready'
		AND NOT EXISTS (SELECT 1 FROM research_integration_round round WHERE round.workspace_id=task.workspace_id AND round.session_id=task.session_id
			AND round.created_by_task_id=task.id AND round.status IN ('pending','running','partially_accepted','accepted'))
		ORDER BY task.id FOR UPDATE OF task`, state.workspaceID, state.run.SessionID, state.run.GoalVersion, state.targetPlan)
	if err != nil {
		return err
	}
	var taskIDs []string
	for rows.Next() {
		var id string
		if err = rows.Scan(&id); err != nil {
			rows.Close()
			return err
		}
		taskIDs = append(taskIDs, id)
	}
	if err = rows.Err(); err != nil {
		rows.Close()
		return err
	}
	rows.Close()
	for _, taskID := range taskIDs {
		inputs, err := loadV6IntegrationRoundInputsTx(ctx, tx, state, taskID)
		if err != nil {
			return err
		}
		agents := map[string]bool{}
		for _, input := range inputs {
			agents[input.AgentID] = true
		}
		// Integration is meaningful only across independent Agent work. Keep
		// the Task ready while more dependencies/results are pending.
		if len(inputs) < 2 || len(agents) < 2 {
			continue
		}
		artifactIDs, versionIDs := make([]string, 0, len(inputs)), make([]string, 0, len(inputs))
		frozen := make([]map[string]string, 0, len(inputs))
		for _, input := range inputs {
			artifactIDs, versionIDs = append(artifactIDs, input.ArtifactID), append(versionIDs, input.VersionID)
			frozen = append(frozen, map[string]string{"artifact_id": input.ArtifactID, "version_id": input.VersionID, "content_hash": input.ContentHash, "agent_id": input.AgentID})
		}
		canonical, err := MarshalArtifactCanonicalJSON(map[string]any{"task_id": taskID, "through_event_sequence": throughSequence, "inputs": frozen})
		if err != nil {
			return err
		}
		inputHash := ArtifactContentHashFromCanonicalJSON(canonical)
		taskNamespace := uuid.MustParse(taskID)
		roundID := uuid.NewSHA1(taskNamespace, []byte(inputHash)).String()
		encodedArtifactIDs, _ := json.Marshal(artifactIDs)
		if _, err = tx.Exec(ctx, `INSERT INTO research_integration_round
			(id,workspace_id,session_id,trigger_kind,input_event_sequence,input_state_hash,input_artifact_ids,goal_version,plan_version,status,created_by_task_id)
			VALUES($1::uuid,$2::uuid,$3::uuid,'result_batch',$4,$5,$6::jsonb,$7,$8,'pending',$9::uuid)`, roundID, state.workspaceID,
			state.run.SessionID, throughSequence, inputHash, encodedArtifactIDs, state.run.GoalVersion, state.targetPlan, taskID); err != nil {
			return err
		}
		criteriaPatch, _ := json.Marshal(map[string]any{"integration_round_id": roundID, "input_event_sequence": throughSequence,
			"input_state_hash": inputHash, "input_artifact_ids": artifactIDs, "input_version_ids": versionIDs})
		if _, err = tx.Exec(ctx, `UPDATE research_task SET acceptance_criteria=acceptance_criteria || $2::jsonb,updated_at=now()
			WHERE id=$1::uuid`, taskID, criteriaPatch); err != nil {
			return err
		}
		content := map[string]any{"trigger_kind": "result_batch", "input_event_sequence": throughSequence, "input_state_hash": inputHash,
			"input_artifact_ids": artifactIDs, "input_version_ids": versionIDs, "goal_version": state.run.GoalVersion, "plan_version": state.targetPlan,
			"status": "pending", "created_by_task_id": taskID}
		roundHash, hashErr := ArtifactContentHash(ArtifactKindIntegrationRound, content)
		if hashErr != nil {
			return hashErr
		}
		goal, plan := int32(state.run.GoalVersion), int32(state.targetPlan)
		if err = registerArtifactPassportTx(ctx, tx, registerArtifactPassportInput{WorkspaceID: state.workspaceID, SessionID: state.run.SessionID,
			EntityID: roundID, Kind: ArtifactKindIntegrationRound, ProvenanceCompleteness: ArtifactProvenanceComplete, GoalVersion: &goal, PlanVersion: &plan,
			AccessLevel: state.outputAccess, HashOrigin: ArtifactHashOriginProduction, ContentHash: roundHash, ProducedByTaskID: taskID,
			SchemaName: string(ArtifactKindIntegrationRound), SchemaVersion: OrchestratorVersionV6}); err != nil {
			return err
		}
		for ordinal, input := range inputs {
			if err = persistTypedArtifactInputReferenceTx(ctx, tx, state.workspaceID, state.run.SessionID, roundID, ArtifactKindIntegrationRound,
				input.ArtifactID, ArtifactKindResultArtifact, "integrates", "integration_round_input", ordinal); err != nil {
				return err
			}
		}
		if _, err = appendEvent(ctx, tx, state.workspaceID, state.run.SessionID, "integration_round_reserved", "integration-round:"+roundID, "system", "",
			map[string]any{"integration_round_id": roundID, "task_id": taskID, "input_event_sequence": throughSequence, "input_state_hash": inputHash,
				"input_artifact_ids": artifactIDs, "input_version_ids": versionIDs}); err != nil {
			return err
		}
	}
	return nil
}

func loadV6IntegrationRoundInputsTx(ctx context.Context, tx pgx.Tx, state acceptedResultState, taskID string) ([]v6IntegrationRoundInput, error) {
	rows, err := tx.Query(ctx, `SELECT passport.id::text,version.id::text,version.content_hash,
		COALESCE(version.produced_by_agent_id::text,attempt.assigned_agent_id::text,'')
		FROM research_task_dependency dependency
		JOIN research_task input_task ON (input_task.workspace_id,input_task.session_id,input_task.id)=(dependency.workspace_id,dependency.session_id,dependency.depends_on_task_id)
		JOIN research_artifact_version version ON (version.workspace_id,version.session_id,version.produced_by_task_id)=(input_task.workspace_id,input_task.session_id,input_task.id)
		JOIN research_artifact_passport passport ON (passport.workspace_id,passport.session_id,passport.id,passport.current_version)=(version.workspace_id,version.session_id,version.artifact_id,version.version)
		LEFT JOIN research_task_attempt attempt ON (attempt.workspace_id,attempt.session_id,attempt.id)=(version.workspace_id,version.session_id,version.produced_by_attempt_id)
		WHERE dependency.workspace_id=$1::uuid AND dependency.session_id=$2::uuid AND dependency.task_id=$3::uuid
		AND input_task.status='succeeded' AND passport.entity_kind='result_artifact' AND passport.lifecycle_status IN ('registered','accepted')
		ORDER BY passport.id`, state.workspaceID, state.run.SessionID, taskID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var inputs []v6IntegrationRoundInput
	for rows.Next() {
		var input v6IntegrationRoundInput
		if err = rows.Scan(&input.ArtifactID, &input.VersionID, &input.ContentHash, &input.AgentID); err != nil {
			return nil, err
		}
		if input.AgentID == "" {
			return nil, fmt.Errorf("%w: Integration input Result has no Agent provenance", ErrInvalidContract)
		}
		inputs = append(inputs, input)
	}
	sort.Slice(inputs, func(i, j int) bool { return inputs[i].ArtifactID < inputs[j].ArtifactID })
	return inputs, rows.Err()
}
