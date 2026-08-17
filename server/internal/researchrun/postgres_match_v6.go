package researchrun

import (
	"context"
	"errors"
	"sort"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
)

func (s *PostgresStore) RecordV6MatchDecision(ctx context.Context, in RecordV6MatchDecisionInput) (V6MatchDecision, error) {
	tx, err := s.beginResearchTx(ctx, txOpV6MatchDecisionRecord, pgx.TxOptions{})
	if err != nil {
		return V6MatchDecision{}, err
	}
	defer tx.Rollback(ctx)
	if err = lockRunForMutation(ctx, tx, in.RunID, in.WorkspaceID); err != nil {
		return V6MatchDecision{}, err
	}
	var existing V6MatchDecision
	err = tx.QueryRow(ctx, `SELECT id::text,candidate_set_hash,branch_scope_hash,decision,reason_code,reason_detail,goal_version
		FROM research_match_decision WHERE workspace_id=$1::uuid AND session_id=$2::uuid AND candidate_set_hash=$3
		AND goal_version=$4 AND branch_scope_hash=$5 AND invalidated_at IS NULL`, in.WorkspaceID, in.RunID, in.CandidateSetHash,
		in.GoalVersion, in.BranchScopeHash).Scan(&existing.ID, &existing.CandidateSetHash, &existing.BranchScopeHash, &existing.Decision, &existing.ReasonCode, &existing.ReasonDetail, &existing.GoalVersion)
	if err == nil {
		if existing.Decision != in.Decision || existing.ReasonCode != in.ReasonCode || existing.ReasonDetail != in.ReasonDetail {
			return V6MatchDecision{}, ErrV6IdempotencyConflict
		}
		return existing, nil
	}
	if !errors.Is(err, pgx.ErrNoRows) {
		return V6MatchDecision{}, err
	}
	ids := make([]string, len(in.Inputs))
	for i, input := range in.Inputs {
		ids[i] = input.VersionID
	}
	sort.Strings(ids)
	branchIDs := make([]string, len(in.BranchRefs))
	branches := append([]V6BranchRef(nil), in.BranchRefs...)
	sort.Slice(branches, func(i, j int) bool { return branches[i].ID < branches[j].ID })
	for i, branch := range branches {
		branchIDs[i] = branch.ID
		var version int64
		var goal int
		var status string
		if err = tx.QueryRow(ctx, `SELECT state_version,goal_version,status FROM research_branch WHERE workspace_id=$1::uuid AND session_id=$2::uuid AND id=$3::uuid FOR UPDATE`, in.WorkspaceID, in.RunID, branch.ID).Scan(&version, &goal, &status); err != nil || version != branch.StateVersion || goal != in.GoalVersion || status != "active" {
			return V6MatchDecision{}, ErrWorkItemChanged
		}
	}
	for index, id := range ids {
		var hash string
		err = tx.QueryRow(ctx, `SELECT v.content_hash FROM research_artifact_version v
			JOIN research_branch_frontier f ON f.node_artifact_version_id=v.id AND f.removed_by_event_sequence IS NULL
			WHERE v.workspace_id=$1::uuid AND v.session_id=$2::uuid AND v.id=$3::uuid LIMIT 1 FOR UPDATE OF f`, in.WorkspaceID, in.RunID, id).Scan(&hash)
		if err != nil {
			return V6MatchDecision{}, ErrWorkItemChanged
		}
		for _, input := range in.Inputs {
			if input.VersionID == id && input.ContentHash != hash {
				return V6MatchDecision{}, ErrWorkItemChanged
			}
		}
		var inScope bool
		if err = tx.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM research_node_branch WHERE session_id=$1::uuid
			AND node_artifact_version_id=$2::uuid AND branch_id=ANY($3::uuid[]))`, in.RunID, id, branchIDs).Scan(&inScope); err != nil || !inScope {
			return V6MatchDecision{}, ErrWorkItemChanged
		}
		_ = index
	}
	decision := V6MatchDecision{ID: uuid.NewString(), CandidateSetHash: in.CandidateSetHash, BranchScopeHash: in.BranchScopeHash,
		Decision: in.Decision, ReasonCode: in.ReasonCode, ReasonDetail: in.ReasonDetail, GoalVersion: in.GoalVersion}
	if _, err = tx.Exec(ctx, `INSERT INTO research_match_decision(id,workspace_id,session_id,candidate_set_hash,input_artifact_version_ids,
		goal_version,branch_scope_hash,decision,reason_code,reason_detail,decided_by,director_cycle_id)
		VALUES($1::uuid,$2::uuid,$3::uuid,$4,$5::uuid[],$6,$7,$8,$9,$10,$11,NULLIF($12,'')::uuid)`, decision.ID, in.WorkspaceID,
		in.RunID, in.CandidateSetHash, ids, in.GoalVersion, in.BranchScopeHash, in.Decision, in.ReasonCode, in.ReasonDetail,
		in.DecidedBy, in.DirectorCycleID); err != nil {
		return V6MatchDecision{}, err
	}
	if _, err = appendEvent(ctx, tx, in.WorkspaceID, in.RunID, "v6_match_decision_recorded", "v6-match:"+in.CandidateSetHash+":"+in.BranchScopeHash,
		"director", "", map[string]any{"match_decision_id": decision.ID, "decision": in.Decision, "input_artifact_version_ids": ids}); err != nil {
		return V6MatchDecision{}, err
	}
	if err = s.commitResearchTx(ctx, txOpV6MatchDecisionRecord, tx); err != nil {
		return V6MatchDecision{}, err
	}
	return decision, nil
}
