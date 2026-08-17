package researchrun

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
)

func normalizedV6JSON(raw json.RawMessage, fallback string) json.RawMessage {
	if len(raw) == 0 || !json.Valid(raw) {
		return json.RawMessage(fallback)
	}
	return raw
}

func (s *PostgresStore) AddV6TeamMember(ctx context.Context, in AddV6TeamMemberInput) (V6TeamMember, error) {
	tx, err := s.beginResearchTx(ctx, txOpV6TeamMemberAdd, pgx.TxOptions{})
	if err != nil {
		return V6TeamMember{}, err
	}
	defer tx.Rollback(ctx)
	if err = lockRunForMutation(ctx, tx, in.RunID, in.WorkspaceID); err != nil {
		return V6TeamMember{}, err
	}
	var orchestrator string
	var activeCount int
	if err = tx.QueryRow(ctx, `SELECT orchestrator_version,(SELECT count(*)::int FROM research_team_membership m
		WHERE m.workspace_id=s.workspace_id AND m.session_id=s.id AND m.state IN ('idle','working','offline','retiring'))
		FROM research_session s WHERE s.workspace_id=$1::uuid AND s.id=$2::uuid`, in.WorkspaceID, in.RunID).Scan(&orchestrator, &activeCount); err != nil {
		return V6TeamMember{}, err
	}
	if orchestrator != OrchestratorVersionV6 {
		return V6TeamMember{}, ErrUnsupportedVersion
	}
	if activeCount >= 50 {
		return V6TeamMember{}, ErrV6TeamLimit
	}
	if activeCount >= 20 && strings.TrimSpace(in.CapacityReason) == "" {
		return V6TeamMember{}, ErrV6CapacityReasonMissing
	}
	var generation int
	if err = tx.QueryRow(ctx, `SELECT COALESCE(max(membership_generation),0)+1 FROM research_team_membership
		WHERE workspace_id=$1::uuid AND session_id=$2::uuid AND agent_id=$3::uuid`, in.WorkspaceID, in.RunID, in.AgentID).Scan(&generation); err != nil {
		return V6TeamMember{}, err
	}
	member := V6TeamMember{ID: uuid.NewString(), WorkspaceID: in.WorkspaceID, RunID: in.RunID, AgentID: in.AgentID,
		DirectorCycleID: in.DirectorCycleID, Generation: generation, MissionRevision: 1, MissionPrompt: strings.TrimSpace(in.MissionPrompt), State: V6TeamIdle,
		ModelConfig: normalizedV6JSON(in.ModelConfig, `{}`), ToolConfig: normalizedV6JSON(in.ToolConfig, `{}`), PermissionConfig: normalizedV6JSON(in.PermissionConfig, `{}`)}
	missionCanonical, err := marshalV6CanonicalJSON(map[string]any{"mission_prompt": member.MissionPrompt, "model_config": json.RawMessage(member.ModelConfig), "tool_config": json.RawMessage(member.ToolConfig), "permission_config": json.RawMessage(member.PermissionConfig)})
	if err != nil {
		return V6TeamMember{}, err
	}
	member.MissionHash = ArtifactContentHashFromCanonicalJSON(missionCanonical)
	_, err = tx.Exec(ctx, `INSERT INTO research_team_membership (
		id,workspace_id,session_id,agent_id,formation_decision_id,director_cycle_id,membership_generation,
		mission_prompt,mission_hash,mission_revision,model_config,tool_config,permission_config,state
	) VALUES ($1::uuid,$2::uuid,$3::uuid,$4::uuid,NULLIF($5,'')::uuid,NULLIF($6,'')::uuid,$7,$8,$9,1,$10::jsonb,$11::jsonb,$12::jsonb,'idle')`,
		member.ID, in.WorkspaceID, in.RunID, in.AgentID, in.FormationDecisionID, in.DirectorCycleID, generation, member.MissionPrompt, member.MissionHash, member.ModelConfig, member.ToolConfig, member.PermissionConfig)
	if err != nil {
		return V6TeamMember{}, err
	}
	if _, err = appendEvent(ctx, tx, in.WorkspaceID, in.RunID, "v6_team_member_joined", "v6-team-member:"+member.ID, "director", "",
		map[string]any{"membership_id": member.ID, "agent_id": member.AgentID, "generation": generation, "active_team_count": activeCount + 1, "capacity_reason": strings.TrimSpace(in.CapacityReason)}); err != nil {
		return V6TeamMember{}, err
	}
	if err = s.commitResearchTx(ctx, txOpV6TeamMemberAdd, tx); err != nil {
		return V6TeamMember{}, err
	}
	return member, nil
}

func (s *PostgresStore) ArchiveV6TeamMember(ctx context.Context, in ArchiveV6TeamMemberInput) (V6TeamMember, error) {
	tx, err := s.beginResearchTx(ctx, txOpV6TeamMemberArchive, pgx.TxOptions{})
	if err != nil {
		return V6TeamMember{}, err
	}
	defer tx.Rollback(ctx)
	if err = lockRunForMutation(ctx, tx, in.RunID, in.WorkspaceID); err != nil {
		return V6TeamMember{}, err
	}
	var member V6TeamMember
	err = tx.QueryRow(ctx, `UPDATE research_team_membership SET state='archived',left_at=now(),terminal_reason=$4
		WHERE workspace_id=$1::uuid AND session_id=$2::uuid AND id=$3::uuid AND state IN ('idle','working','offline','retiring')
		RETURNING id::text,workspace_id::text,session_id::text,agent_id::text,membership_generation,mission_prompt,mission_hash,mission_revision,model_config,tool_config,permission_config,state`,
		in.WorkspaceID, in.RunID, in.MembershipID, strings.TrimSpace(in.Reason)).Scan(&member.ID, &member.WorkspaceID, &member.RunID, &member.AgentID, &member.Generation, &member.MissionPrompt, &member.MissionHash, &member.MissionRevision, &member.ModelConfig, &member.ToolConfig, &member.PermissionConfig, &member.State)
	if errors.Is(err, pgx.ErrNoRows) {
		return V6TeamMember{}, ErrInvalidTransition
	}
	if err != nil {
		return V6TeamMember{}, err
	}
	if _, err = appendEvent(ctx, tx, in.WorkspaceID, in.RunID, "v6_team_member_archived", fmt.Sprintf("v6-team-member-archived:%s:%d", member.ID, member.Generation), "director", "", map[string]any{"membership_id": member.ID, "reason": in.Reason}); err != nil {
		return V6TeamMember{}, err
	}
	if err = s.commitResearchTx(ctx, txOpV6TeamMemberArchive, tx); err != nil {
		return V6TeamMember{}, err
	}
	return member, nil
}
