package researchrun

import (
	"context"
	"encoding/json"
	"errors"
	"sort"
	"strings"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
)

// CreateV6ReportWork freezes the exact report inputs before a Report Agent is
// dispatched. The report envelope, input rows and Work Item are one transaction,
// so an interrupted Director can recover the complete assignment from Postgres.
func (s *PostgresStore) CreateV6ReportWork(ctx context.Context, in CreateV6ReportWorkInput) (V6ReportWork, error) {
	if strings.TrimSpace(in.IdempotencyKey) == "" || strings.TrimSpace(in.Reason) == "" || len(in.Inputs) == 0 || len(in.Inputs) > 1024 {
		return V6ReportWork{}, ErrInvalidContract
	}
	tx, err := s.beginResearchTx(ctx, txOpV6ReportWorkCreate, pgx.TxOptions{})
	if err != nil {
		return V6ReportWork{}, err
	}
	defer tx.Rollback(ctx)
	if err = lockRunForMutation(ctx, tx, in.RunID, in.WorkspaceID); err != nil {
		return V6ReportWork{}, err
	}
	var goalVersion, planVersion int
	var stateVersion, eventSequence int64
	if err = tx.QueryRow(ctx, `SELECT goal_version,plan_version,state_version,COALESCE((SELECT max(sequence) FROM research_run_event WHERE session_id=research_session.id),0) FROM research_session WHERE workspace_id=$1::uuid AND id=$2::uuid AND orchestrator_version='research-run-v6' AND status='running'`, in.WorkspaceID, in.RunID).Scan(&goalVersion, &planVersion, &stateVersion, &eventSequence); err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return V6ReportWork{}, ErrUnsupportedVersion
		}
		return V6ReportWork{}, err
	}
	if goalVersion != in.ExpectedGoalVersion || stateVersion != in.ExpectedStateVersion || eventSequence != in.InputEventSequence {
		return V6ReportWork{}, ErrWorkItemChanged
	}
	var cycleOK bool
	if err = tx.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM research_director_cycle c JOIN research_director_assignment a ON a.id=c.director_assignment_id WHERE c.workspace_id=$1::uuid AND c.session_id=$2::uuid AND c.id=$3::uuid AND a.status='active')`, in.WorkspaceID, in.RunID, in.DirectorCycleID).Scan(&cycleOK); err != nil || !cycleOK {
		if err != nil {
			return V6ReportWork{}, err
		}
		return V6ReportWork{}, ErrV6DirectorUnavailable
	}
	var membershipOK bool
	if err = tx.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM research_team_membership WHERE workspace_id=$1::uuid AND session_id=$2::uuid AND agent_id=$3::uuid AND state IN ('idle','working','offline','retiring'))`, in.WorkspaceID, in.RunID, in.AssigneeAgentID).Scan(&membershipOK); err != nil || !membershipOK {
		if err != nil {
			return V6ReportWork{}, err
		}
		return V6ReportWork{}, ErrAttemptNotAssigned
	}
	inputs := append([]V6ReportInputRef(nil), in.Inputs...)
	sort.Slice(inputs, func(i, j int) bool {
		if inputs[i].BranchID != inputs[j].BranchID {
			return inputs[i].BranchID < inputs[j].BranchID
		}
		return inputs[i].NodeArtifactVersionID < inputs[j].NodeArtifactVersionID
	})
	seen := make(map[string]struct{}, len(inputs))
	for _, input := range inputs {
		if _, duplicate := seen[input.NodeArtifactVersionID]; duplicate {
			return V6ReportWork{}, ErrInvalidContract
		}
		seen[input.NodeArtifactVersionID] = struct{}{}
		if input.InputRole != "branch_xxl" && input.InputRole != "branch_maximum" && input.InputRole != "unresolved_gap" {
			return V6ReportWork{}, ErrInvalidContract
		}
		var storedHash, tier, insightStatus string
		var currentXXL, activeFrontier bool
		err = tx.QueryRow(ctx, `SELECT v.content_hash,COALESCE(iv.tier,''),COALESCE(iv.status,''),COALESCE(b.current_xxl_version_id=iv.id,false),EXISTS(SELECT 1 FROM research_branch_frontier f WHERE f.session_id=$2::uuid AND f.branch_id=$4::uuid AND f.node_artifact_version_id=v.id AND f.removed_by_event_sequence IS NULL) FROM research_artifact_version v JOIN research_artifact_passport p ON p.id=v.artifact_id JOIN research_node_branch nb ON nb.session_id=v.session_id AND nb.node_artifact_version_id=v.id AND nb.branch_id=$4::uuid JOIN research_branch b ON b.id=nb.branch_id LEFT JOIN research_insight_version iv ON iv.artifact_version_id=v.id WHERE v.workspace_id=$1::uuid AND v.session_id=$2::uuid AND v.id=$3::uuid AND p.lifecycle_status='accepted'`, in.WorkspaceID, in.RunID, input.NodeArtifactVersionID, input.BranchID).Scan(&storedHash, &tier, &insightStatus, &currentXXL, &activeFrontier)
		if errors.Is(err, pgx.ErrNoRows) {
			return V6ReportWork{}, ErrWorkItemChanged
		}
		if err != nil {
			return V6ReportWork{}, err
		}
		if storedHash != input.ContentHash {
			return V6ReportWork{}, ErrWorkItemChanged
		}
		switch input.InputRole {
		case "branch_xxl":
			if !currentXXL || tier != "XXL" || insightStatus != "accepted" {
				return V6ReportWork{}, ErrInvalidContract
			}
		case "branch_maximum":
			if !activeFrontier || tier == "" || insightStatus != "accepted" {
				return V6ReportWork{}, ErrInvalidContract
			}
		case "unresolved_gap":
			if !activeFrontier {
				return V6ReportWork{}, ErrInvalidContract
			}
		}
	}
	canonical, err := MarshalArtifactCanonicalJSON(inputs)
	if err != nil {
		return V6ReportWork{}, err
	}
	snapshotHash := ArtifactContentHashFromCanonicalJSON(canonical)
	var replay V6ReportWork
	var replayAssignee, replayCycle, replayTitle, replayReason string
	if err = tx.QueryRow(ctx, `SELECT w.target_id::text,w.id::text,r.input_snapshot_hash,r.revision,w.goal_version,w.assigned_agent_id::text,w.created_by_director_cycle_id::text,r.title,w.reason FROM research_work_item w JOIN research_report r ON r.id=w.target_id WHERE w.workspace_id=$1::uuid AND w.session_id=$2::uuid AND w.goal_version=$3 AND w.idempotency_key=$4 AND w.kind='report'`, in.WorkspaceID, in.RunID, goalVersion, in.IdempotencyKey).Scan(&replay.ReportID, &replay.WorkItemID, &replay.InputSnapshotHash, &replay.Revision, &replay.GoalVersion, &replayAssignee, &replayCycle, &replayTitle, &replayReason); err == nil {
		if replay.InputSnapshotHash != snapshotHash || replayAssignee != in.AssigneeAgentID || replayCycle != in.DirectorCycleID || replayTitle != in.Title || replayReason != in.Reason {
			return V6ReportWork{}, ErrResultConflict
		}
		return replay, nil
	} else if !errors.Is(err, pgx.ErrNoRows) {
		return V6ReportWork{}, err
	}
	result := V6ReportWork{ReportID: uuid.NewString(), WorkItemID: uuid.NewString(), InputSnapshotHash: snapshotHash, GoalVersion: goalVersion}
	if err = tx.QueryRow(ctx, `SELECT COALESCE(max(revision),0)+1 FROM research_report WHERE session_id=$1::uuid`, in.RunID).Scan(&result.Revision); err != nil {
		return V6ReportWork{}, err
	}
	if _, err = tx.Exec(ctx, `INSERT INTO research_report(id,workspace_id,session_id,revision,goal_version,plan_version,status,title,input_snapshot_hash,input_event_sequence) VALUES($1::uuid,$2::uuid,$3::uuid,$4,$5,$6,'draft',$7,$8,$9)`, result.ReportID, in.WorkspaceID, in.RunID, result.Revision, goalVersion, planVersion, in.Title, result.InputSnapshotHash, eventSequence); err != nil {
		return V6ReportWork{}, err
	}
	for ordinal, input := range inputs {
		if _, err = tx.Exec(ctx, `INSERT INTO research_report_input(workspace_id,session_id,report_id,report_revision,branch_id,node_artifact_version_id,input_role,ordinal,content_hash) VALUES($1::uuid,$2::uuid,$3::uuid,$4,$5::uuid,$6::uuid,$7,$8,$9)`, in.WorkspaceID, in.RunID, result.ReportID, result.Revision, input.BranchID, input.NodeArtifactVersionID, input.InputRole, ordinal, input.ContentHash); err != nil {
			return V6ReportWork{}, err
		}
	}
	payload, _ := json.Marshal(map[string]any{"report_id": result.ReportID, "report_revision": result.Revision, "goal_version": goalVersion, "input_snapshot_hash": result.InputSnapshotHash, "input_nodes": inputs, "required_output": "self_contained_html_package"})
	if _, err = tx.Exec(ctx, `INSERT INTO research_work_item(id,workspace_id,session_id,kind,status,target_kind,target_id,client_key,idempotency_key,goal_version,input_state_version,input_event_sequence,created_by_director_cycle_id,assigned_agent_id,priority,max_attempts,payload_schema_id,expected_result_schema_id,payload,state_version,ready_at,reason) VALUES($1::uuid,$2::uuid,$3::uuid,'report','ready','report',$4::uuid,$5,$5,$6,$7,$8,$9::uuid,$10::uuid,0.9,3,'report.package.v1','report_package_submission',$11::jsonb,0,now(),$12)`, result.WorkItemID, in.WorkspaceID, in.RunID, result.ReportID, in.IdempotencyKey, goalVersion, stateVersion, eventSequence, in.DirectorCycleID, in.AssigneeAgentID, payload, in.Reason); err != nil {
		return V6ReportWork{}, err
	}
	if _, err = appendEvent(ctx, tx, in.WorkspaceID, in.RunID, "v6_report_work_created", "v6-report-work:"+in.IdempotencyKey, "system", "", map[string]any{"report_id": result.ReportID, "report_revision": result.Revision, "work_item_id": result.WorkItemID, "input_snapshot_hash": result.InputSnapshotHash, "director_cycle_id": in.DirectorCycleID}); err != nil {
		return V6ReportWork{}, err
	}
	if err = s.commitResearchTx(ctx, txOpV6ReportWorkCreate, tx); err != nil {
		return V6ReportWork{}, err
	}
	return result, nil
}
