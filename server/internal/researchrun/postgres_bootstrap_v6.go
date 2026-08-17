package researchrun

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
)

func (s *PostgresStore) BootstrapV6(ctx context.Context, in V6BootstrapInput, cfg RunConfig) (Run, int64, error) {
	if strings.TrimSpace(in.WorkspaceID) == "" || strings.TrimSpace(in.CreatedBy) == "" || strings.TrimSpace(in.DirectorAgentID) == "" {
		return Run{}, 0, fmt.Errorf("%w: V6 bootstrap identity is incomplete", ErrInvalidContract)
	}
	start := StartInput{WorkspaceID: in.WorkspaceID, CreatedBy: in.CreatedBy, LeadAgentID: in.DirectorAgentID, Goal: in.Goal, Title: in.Title, DepthTier: in.DepthTier, Language: in.Language, SourcePolicy: in.SourcePolicy}
	sourcePolicy, configJSON, err := prepareRunInitialization(start, cfg)
	if err != nil {
		return Run{}, 0, err
	}
	tx, err := s.beginResearchTx(ctx, txOpV6Bootstrap, pgx.TxOptions{})
	if err != nil {
		return Run{}, 0, err
	}
	defer tx.Rollback(ctx)
	var directorExists bool
	if err = tx.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM agent WHERE workspace_id=$1::uuid AND id=$2::uuid AND archived_at IS NULL)`, in.WorkspaceID, in.DirectorAgentID).Scan(&directorExists); err != nil || !directorExists {
		if err == nil {
			err = ErrRunNotFound
		}
		return Run{}, 0, err
	}
	runID, assignmentID, membershipID := uuid.NewString(), uuid.NewString(), uuid.NewString()
	if _, err = tx.Exec(ctx, `INSERT INTO research_session(id,workspace_id,fleet_id,created_by,title,goal,status,current_stage,depth_tier,orchestrator_version,run_config,run_initialized_at,last_progress_at,next_reconcile_at,director_state_version,state_version)
		VALUES($1::uuid,$2::uuid,NULL,$3::uuid,$4,$5,'running','s1_plan',$6,$7,$8::jsonb,now(),now(),now(),1,1)`, runID, in.WorkspaceID, in.CreatedBy, strings.TrimSpace(in.Title), strings.TrimSpace(in.Goal), in.DepthTier, OrchestratorVersionV6, configJSON); err != nil {
		return Run{}, 0, err
	}
	if _, err = tx.Exec(ctx, `INSERT INTO research_contract_revision(workspace_id,session_id,goal_version,goal,language,source_policy,run_limits,authored_by,reason)
		VALUES($1::uuid,$2::uuid,1,$3,$4,$5::jsonb,$6::jsonb,$7::uuid,'v6_bootstrap')`, in.WorkspaceID, runID, strings.TrimSpace(in.Goal), strings.TrimSpace(in.Language), sourcePolicy, configJSON, in.CreatedBy); err != nil {
		return Run{}, 0, err
	}
	if _, err = tx.Exec(ctx, `INSERT INTO research_director_assignment(id,workspace_id,session_id,director_agent_id,generation,status,assigned_by_user_id,reason)
		VALUES($1::uuid,$2::uuid,$3::uuid,$4::uuid,1,'active',$5::uuid,'v6_bootstrap')`, assignmentID, in.WorkspaceID, runID, in.DirectorAgentID, in.CreatedBy); err != nil {
		return Run{}, 0, err
	}
	if _, err = tx.Exec(ctx, `UPDATE research_session SET current_director_assignment_id=$2::uuid WHERE id=$1::uuid`, runID, assignmentID); err != nil {
		return Run{}, 0, err
	}
	mission := "Serve as Ronaldo, the Research Director. Build and supervise the research team, preserve evidence lineage, and publish only a reviewed report."
	missionPayload, _ := json.Marshal(map[string]string{"mission": mission})
	missionHash := ArtifactContentHashFromCanonicalJSON(missionPayload)
	if _, err = tx.Exec(ctx, `INSERT INTO research_team_membership(id,workspace_id,session_id,agent_id,membership_generation,mission_prompt,mission_hash,mission_revision,state)
		VALUES($1::uuid,$2::uuid,$3::uuid,$4::uuid,1,$5,$6,1,'idle')`, membershipID, in.WorkspaceID, runID, in.DirectorAgentID, mission, missionHash); err != nil {
		return Run{}, 0, err
	}
	if _, err = tx.Exec(ctx, `INSERT INTO research_branch(workspace_id,session_id,client_key,objective,entry_conditions,exit_conditions,budget_share,status,goal_version,scope,state_version)
		VALUES($1::uuid,$2::uuid,'root',$3,'[]'::jsonb,'[]'::jsonb,1,'active',1,'{}'::jsonb,1)`, in.WorkspaceID, runID, strings.TrimSpace(in.Goal)); err != nil {
		return Run{}, 0, err
	}
	event, err := appendEvent(ctx, tx, in.WorkspaceID, runID, "v6_run_bootstrapped", "v6-bootstrap", "user", in.CreatedBy, map[string]any{
		"orchestrator_version": OrchestratorVersionV6, "director_assignment_id": assignmentID, "director_agent_id": in.DirectorAgentID, "membership_id": membershipID,
	})
	if err != nil {
		return Run{}, 0, err
	}
	run, err := loadRun(ctx, tx, runID, in.WorkspaceID, false)
	if err != nil {
		return Run{}, 0, err
	}
	if err = s.commitResearchTx(ctx, txOpV6Bootstrap, tx); err != nil {
		return Run{}, 0, err
	}
	return run, event.Sequence, nil
}
