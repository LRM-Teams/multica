package researchrun

import (
	"context"
	"errors"
	"strings"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
)

func (s *PostgresStore) AssignV6Director(ctx context.Context, in AssignV6DirectorInput) (V6DirectorAssignment, error) {
	tx, err := s.beginResearchTx(ctx, txOpV6DirectorAssign, pgx.TxOptions{})
	if err != nil {
		return V6DirectorAssignment{}, err
	}
	defer tx.Rollback(ctx)
	if err = lockRunForMutation(ctx, tx, in.RunID, in.WorkspaceID); err != nil {
		return V6DirectorAssignment{}, err
	}
	var currentVersion int64
	var orchestrator string
	if err = tx.QueryRow(ctx, `SELECT director_state_version,orchestrator_version FROM research_session WHERE workspace_id=$1::uuid AND id=$2::uuid`, in.WorkspaceID, in.RunID).Scan(&currentVersion, &orchestrator); err != nil {
		return V6DirectorAssignment{}, err
	}
	if orchestrator != OrchestratorVersionV6 {
		return V6DirectorAssignment{}, ErrUnsupportedVersion
	}
	key := "v6-director-assigned:" + in.ClientRequestID
	var replay V6DirectorAssignment
	var replayActor, replayAgent, replayReason string
	err = tx.QueryRow(ctx, `SELECT a.id::text,a.workspace_id::text,a.session_id::text,a.director_agent_id::text,a.status,a.reason,a.generation,
		(e.payload->>'state_version')::bigint,COALESCE(e.actor_id::text,''),e.payload->>'agent_id',COALESCE(e.payload->>'reason','')
		FROM research_run_event e
		JOIN research_director_assignment a ON a.id=(e.payload->>'assignment_id')::uuid
		WHERE e.workspace_id=$1::uuid AND e.session_id=$2::uuid AND e.idempotency_key=$3 AND e.event_type='v6_director_assigned'`, in.WorkspaceID, in.RunID, key).Scan(
		&replay.ID, &replay.WorkspaceID, &replay.RunID, &replay.AgentID, &replay.Status, &replay.Reason, &replay.Generation,
		&replay.StateVersion, &replayActor, &replayAgent, &replayReason,
	)
	if err == nil {
		if replayActor != in.UserID || replayAgent != in.AgentID || replayReason != strings.TrimSpace(in.Reason) {
			return V6DirectorAssignment{}, ErrResultConflict
		}
		return replay, nil
	}
	if !errors.Is(err, pgx.ErrNoRows) {
		return V6DirectorAssignment{}, err
	}
	if currentVersion != in.ExpectedStateVersion {
		return V6DirectorAssignment{}, ErrWorkItemChanged
	}
	var exists bool
	if err = tx.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM agent WHERE id=$1::uuid AND workspace_id=$2::uuid AND archived_at IS NULL)`, in.AgentID, in.WorkspaceID).Scan(&exists); err != nil {
		return V6DirectorAssignment{}, err
	}
	if !exists {
		return V6DirectorAssignment{}, ErrRunNotFound
	}
	var generation int
	if err = tx.QueryRow(ctx, `SELECT COALESCE(max(generation),0)+1 FROM research_director_assignment WHERE workspace_id=$1::uuid AND session_id=$2::uuid`, in.WorkspaceID, in.RunID).Scan(&generation); err != nil {
		return V6DirectorAssignment{}, err
	}
	if _, err = tx.Exec(ctx, `UPDATE research_director_assignment SET status='replaced',ended_at=now() WHERE workspace_id=$1::uuid AND session_id=$2::uuid AND status IN ('active','unavailable')`, in.WorkspaceID, in.RunID); err != nil {
		return V6DirectorAssignment{}, err
	}
	assignment := V6DirectorAssignment{ID: uuid.NewString(), WorkspaceID: in.WorkspaceID, RunID: in.RunID, AgentID: in.AgentID, Status: "active", Reason: strings.TrimSpace(in.Reason), Generation: generation, StateVersion: currentVersion + 1}
	if _, err = tx.Exec(ctx, `INSERT INTO research_director_assignment(id,workspace_id,session_id,director_agent_id,generation,status,assigned_by_user_id,reason)
		VALUES($1::uuid,$2::uuid,$3::uuid,$4::uuid,$5,'active',$6::uuid,$7)`, assignment.ID, in.WorkspaceID, in.RunID, in.AgentID, generation, in.UserID, assignment.Reason); err != nil {
		return V6DirectorAssignment{}, err
	}
	if _, err = tx.Exec(ctx, `UPDATE research_session SET current_director_assignment_id=$3::uuid,director_state_version=director_state_version+1,status=CASE WHEN status='awaiting_director' THEN 'running' ELSE status END,updated_at=now() WHERE workspace_id=$1::uuid AND id=$2::uuid`, in.WorkspaceID, in.RunID, assignment.ID); err != nil {
		return V6DirectorAssignment{}, err
	}
	// A Director is the first and only membership created at Run start. On an
	// explicit replacement, reuse the existing Agent membership or add one.
	var activeMembership bool
	if err = tx.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM research_team_membership WHERE workspace_id=$1::uuid AND session_id=$2::uuid AND agent_id=$3::uuid AND state IN ('idle','working','offline','retiring'))`, in.WorkspaceID, in.RunID, in.AgentID).Scan(&activeMembership); err != nil {
		return V6DirectorAssignment{}, err
	}
	if !activeMembership {
		mission := "Serve as the user-selected Research Director for this Run."
		missionHash := ArtifactContentHashFromCanonicalJSON([]byte(`{"mission":"user-selected Research Director"}`))
		if _, err = tx.Exec(ctx, `INSERT INTO research_team_membership(id,workspace_id,session_id,agent_id,membership_generation,mission_prompt,mission_hash,mission_revision,state)
			VALUES(gen_random_uuid(),$1::uuid,$2::uuid,$3::uuid,COALESCE((SELECT max(membership_generation)+1 FROM research_team_membership WHERE workspace_id=$1::uuid AND session_id=$2::uuid AND agent_id=$3::uuid),1),$4,$5,1,'idle')`, in.WorkspaceID, in.RunID, in.AgentID, mission, missionHash); err != nil {
			return V6DirectorAssignment{}, err
		}
	}
	if _, err = appendEvent(ctx, tx, in.WorkspaceID, in.RunID, "v6_director_assigned", key, "user", in.UserID, map[string]any{"assignment_id": assignment.ID, "agent_id": in.AgentID, "reason": assignment.Reason, "generation": generation, "state_version": assignment.StateVersion}); err != nil {
		return V6DirectorAssignment{}, err
	}
	if err = s.commitResearchTx(ctx, txOpV6DirectorAssign, tx); err != nil {
		return V6DirectorAssignment{}, err
	}
	return assignment, nil
}

func (s *PostgresStore) MarkV6DirectorUnavailable(ctx context.Context, in MarkV6DirectorUnavailableInput) (V6DirectorAssignment, error) {
	tx, err := s.beginResearchTx(ctx, txOpV6DirectorUnavailable, pgx.TxOptions{})
	if err != nil {
		return V6DirectorAssignment{}, err
	}
	defer tx.Rollback(ctx)
	if err = lockRunForMutation(ctx, tx, in.RunID, in.WorkspaceID); err != nil {
		return V6DirectorAssignment{}, err
	}
	key := "v6-director-unavailable:" + in.ClientRequestID
	var replay V6DirectorAssignment
	var replayFailure, replayDiagnostics string
	err = tx.QueryRow(ctx, `SELECT a.id::text,a.workspace_id::text,a.session_id::text,a.director_agent_id::text,a.status,a.reason,a.generation,s.director_state_version,e.payload->>'failure_class',COALESCE(e.payload->>'diagnostics','')
		FROM research_run_event e
		JOIN research_director_assignment a ON a.id=(e.payload->>'assignment_id')::uuid
		JOIN research_session s ON s.workspace_id=e.workspace_id AND s.id=e.session_id
		WHERE e.workspace_id=$1::uuid AND e.session_id=$2::uuid AND e.idempotency_key=$3 AND e.event_type='v6_director_unavailable'`, in.WorkspaceID, in.RunID, key).Scan(&replay.ID, &replay.WorkspaceID, &replay.RunID, &replay.AgentID, &replay.Status, &replay.Reason, &replay.Generation, &replay.StateVersion, &replayFailure, &replayDiagnostics)
	if err == nil {
		if replay.ID != in.AssignmentID || replayFailure != in.FailureClass || replayDiagnostics != in.Diagnostics {
			return V6DirectorAssignment{}, ErrResultConflict
		}
		return replay, nil
	}
	if !errors.Is(err, pgx.ErrNoRows) {
		return V6DirectorAssignment{}, err
	}
	var assignment V6DirectorAssignment
	err = tx.QueryRow(ctx, `UPDATE research_director_assignment a SET status='unavailable'
		FROM research_session s WHERE a.workspace_id=$1::uuid AND a.session_id=$2::uuid AND a.id=$3::uuid AND a.status='active'
		AND s.workspace_id=a.workspace_id AND s.id=a.session_id AND s.director_state_version=$4
		RETURNING a.id::text,a.workspace_id::text,a.session_id::text,a.director_agent_id::text,a.status,a.reason,a.generation,s.director_state_version+1`, in.WorkspaceID, in.RunID, in.AssignmentID, in.ExpectedStateVersion).Scan(&assignment.ID, &assignment.WorkspaceID, &assignment.RunID, &assignment.AgentID, &assignment.Status, &assignment.Reason, &assignment.Generation, &assignment.StateVersion)
	if errors.Is(err, pgx.ErrNoRows) {
		return V6DirectorAssignment{}, ErrWorkItemChanged
	}
	if err != nil {
		return V6DirectorAssignment{}, err
	}
	if _, err = tx.Exec(ctx, `UPDATE research_session SET status='awaiting_director',director_state_version=director_state_version+1,updated_at=now() WHERE workspace_id=$1::uuid AND id=$2::uuid`, in.WorkspaceID, in.RunID); err != nil {
		return V6DirectorAssignment{}, err
	}
	if _, err = tx.Exec(ctx, `INSERT INTO research_v6_outbox(workspace_id,session_id,kind,idempotency_key,payload) VALUES($1::uuid,$2::uuid,'notify_user',$3,jsonb_build_object('assignment_id',$4,'failure_class',$5,'diagnostics',$6)) ON CONFLICT DO NOTHING`, in.WorkspaceID, in.RunID, key, assignment.ID, in.FailureClass, in.Diagnostics); err != nil {
		return V6DirectorAssignment{}, err
	}
	if _, err = appendEvent(ctx, tx, in.WorkspaceID, in.RunID, "v6_director_unavailable", key, "system", "", map[string]any{"assignment_id": assignment.ID, "failure_class": in.FailureClass, "diagnostics": in.Diagnostics}); err != nil {
		return V6DirectorAssignment{}, err
	}
	if err = s.commitResearchTx(ctx, txOpV6DirectorUnavailable, tx); err != nil {
		return V6DirectorAssignment{}, err
	}
	return assignment, nil
}
