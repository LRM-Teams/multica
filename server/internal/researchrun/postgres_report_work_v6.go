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

// CreateV6ReportWork freezes the exact report inputs before a Report Agent is
// dispatched. The report envelope, input rows and Work Item are one transaction,
// so an interrupted Director can recover the complete assignment from Postgres.
func (s *PostgresStore) CreateV6ReportWork(ctx context.Context, in CreateV6ReportWorkInput) (V6ReportWork, error) {
	if strings.TrimSpace(in.IdempotencyKey) == "" || strings.TrimSpace(in.Title) == "" || strings.TrimSpace(in.Reason) == "" {
		return V6ReportWork{}, fmt.Errorf("%w: report intent requires idempotency key, title, and reason", ErrInvalidContract)
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
	var replay V6ReportWork
	var replayCycle, replayTitle, replayReason string
	if err = tx.QueryRow(ctx, `SELECT w.target_id::text,w.id::text,r.input_snapshot_hash,r.revision,w.goal_version,w.created_by_director_cycle_id::text,r.title,w.reason FROM research_work_item w JOIN research_report r ON r.id=w.target_id WHERE w.workspace_id=$1::uuid AND w.session_id=$2::uuid AND w.goal_version=$3 AND w.idempotency_key=$4 AND w.kind='report'`, in.WorkspaceID, in.RunID, in.ExpectedGoalVersion, in.IdempotencyKey).Scan(&replay.ReportID, &replay.WorkItemID, &replay.InputSnapshotHash, &replay.Revision, &replay.GoalVersion, &replayCycle, &replayTitle, &replayReason); err == nil {
		if replayCycle != in.DirectorCycleID || replayTitle != in.Title || replayReason != in.Reason {
			return V6ReportWork{}, ErrResultConflict
		}
		return replay, nil
	} else if !errors.Is(err, pgx.ErrNoRows) {
		return V6ReportWork{}, err
	}
	if goalVersion != in.ExpectedGoalVersion || stateVersion != in.ExpectedStateVersion {
		return V6ReportWork{}, ErrWorkItemChanged
	}
	var cycleOK bool
	if err = tx.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM research_director_cycle c JOIN research_director_assignment a ON a.id=c.director_assignment_id WHERE c.workspace_id=$1::uuid AND c.session_id=$2::uuid AND c.id=$3::uuid AND a.status='active')`, in.WorkspaceID, in.RunID, in.DirectorCycleID).Scan(&cycleOK); err != nil || !cycleOK {
		if err != nil {
			return V6ReportWork{}, err
		}
		return V6ReportWork{}, ErrV6DirectorUnavailable
	}
	var reporterAgentID string
	if err = tx.QueryRow(ctx, `SELECT agent_id::text FROM research_team_membership WHERE workspace_id=$1::uuid AND session_id=$2::uuid AND role='reporter' AND state IN ('idle','working','offline','retiring') ORDER BY joined_at,id LIMIT 1`, in.WorkspaceID, in.RunID).Scan(&reporterAgentID); err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return V6ReportWork{}, ErrAttemptNotAssigned
		}
		return V6ReportWork{}, err
	}
	selection, err := selectV6ReportInputs(ctx, tx, in.WorkspaceID, in.RunID)
	if err != nil {
		return V6ReportWork{}, err
	}
	if len(selection.Inputs) == 0 || len(selection.Inputs) > 1024 {
		return V6ReportWork{}, ErrWorkItemChanged
	}
	inputs := selection.Inputs
	seen := make(map[string]struct{}, len(inputs))
	for _, input := range inputs {
		if _, duplicate := seen[input.NodeArtifactVersionID]; duplicate {
			return V6ReportWork{}, fmt.Errorf("%w: server report selection contains duplicate node %s", ErrInvalidContract, input.NodeArtifactVersionID)
		}
		seen[input.NodeArtifactVersionID] = struct{}{}
		if input.InputRole != "branch_xxl" && input.InputRole != "branch_maximum" && input.InputRole != "unresolved_gap" {
			return V6ReportWork{}, fmt.Errorf("%w: server report selection contains unsupported role %q", ErrInvalidContract, input.InputRole)
		}
		var storedHash string
		err = tx.QueryRow(ctx, `SELECT v.content_hash FROM research_artifact_version v JOIN research_artifact_passport p ON p.id=v.artifact_id JOIN research_node_branch nb ON nb.session_id=v.session_id AND nb.node_artifact_version_id=v.id AND nb.branch_id=$4::uuid WHERE v.workspace_id=$1::uuid AND v.session_id=$2::uuid AND v.id=$3::uuid AND p.lifecycle_status='accepted'`, in.WorkspaceID, in.RunID, input.NodeArtifactVersionID, input.BranchID).Scan(&storedHash)
		if errors.Is(err, pgx.ErrNoRows) {
			return V6ReportWork{}, ErrWorkItemChanged
		}
		if err != nil {
			return V6ReportWork{}, err
		}
		if storedHash != input.ContentHash {
			return V6ReportWork{}, ErrWorkItemChanged
		}
	}
	snapshotHash := selection.Hash
	var activeOther bool
	var latestSnapshotHash string
	if err = tx.QueryRow(ctx, `SELECT
		EXISTS(SELECT 1 FROM research_work_item WHERE workspace_id=$1::uuid AND session_id=$2::uuid AND kind='report'
			AND status IN ('ready','dispatching','enqueued','running','awaiting_input') AND idempotency_key<>$3),
		COALESCE((SELECT input_snapshot_hash FROM research_report WHERE workspace_id=$1::uuid AND session_id=$2::uuid
			AND package_hash IS NOT NULL ORDER BY revision DESC LIMIT 1),'')`, in.WorkspaceID, in.RunID, in.IdempotencyKey).Scan(&activeOther, &latestSnapshotHash); err != nil {
		return V6ReportWork{}, err
	}
	if activeOther || latestSnapshotHash == snapshotHash {
		return V6ReportWork{}, ErrWorkItemChanged
	}
	result := V6ReportWork{ReportID: uuid.NewString(), WorkItemID: uuid.NewString(), InputSnapshotHash: snapshotHash, GoalVersion: goalVersion}
	if err = tx.QueryRow(ctx, `SELECT COALESCE(max(revision),0)+1 FROM research_report WHERE session_id=$1::uuid`, in.RunID).Scan(&result.Revision); err != nil {
		return V6ReportWork{}, err
	}
	var parentReportID string
	var parentRevision int
	var parentSummary, parentPlainText, parentPackageHash, parentDesignDossier string
	parentErr := tx.QueryRow(ctx, `SELECT id::text,revision,summary,plain_text,COALESCE(package_hash,''),design_dossier FROM research_report WHERE workspace_id=$1::uuid AND session_id=$2::uuid AND goal_version=$3 AND package_hash IS NOT NULL ORDER BY revision DESC LIMIT 1`, in.WorkspaceID, in.RunID, goalVersion).Scan(&parentReportID, &parentRevision, &parentSummary, &parentPlainText, &parentPackageHash, &parentDesignDossier)
	if parentErr != nil && !errors.Is(parentErr, pgx.ErrNoRows) {
		return V6ReportWork{}, parentErr
	}
	coverageJSON, _ := json.Marshal(selection.Directions)
	if _, err = tx.Exec(ctx, `INSERT INTO research_report(id,workspace_id,session_id,revision,goal_version,plan_version,status,title,input_snapshot_hash,input_event_sequence,parent_report_id,parent_revision,maturity,direction_coverage) VALUES($1::uuid,$2::uuid,$3::uuid,$4,$5,$6,'draft',$7,$8,$9,NULLIF($10,'')::uuid,NULLIF($11,0),$12,$13::jsonb)`, result.ReportID, in.WorkspaceID, in.RunID, result.Revision, goalVersion, planVersion, in.Title, result.InputSnapshotHash, eventSequence, parentReportID, parentRevision, selection.Maturity, coverageJSON); err != nil {
		return V6ReportWork{}, err
	}
	if err = registerDraftReportRevisionPassportTx(ctx, tx, in.WorkspaceID, in.RunID, result.ReportID); err != nil {
		return V6ReportWork{}, err
	}
	for ordinal, input := range inputs {
		if _, err = tx.Exec(ctx, `INSERT INTO research_report_input(workspace_id,session_id,report_id,report_revision,branch_id,node_artifact_version_id,input_role,ordinal,content_hash) VALUES($1::uuid,$2::uuid,$3::uuid,$4,$5::uuid,$6::uuid,$7,$8,$9)`, in.WorkspaceID, in.RunID, result.ReportID, result.Revision, input.BranchID, input.NodeArtifactVersionID, input.InputRole, ordinal, input.ContentHash); err != nil {
			return V6ReportWork{}, err
		}
	}
	artifactVersionIDs := make([]string, len(selection.InputNodes))
	for index := range selection.InputNodes {
		artifactVersionIDs[index] = selection.InputNodes[index].VersionID
	}
	reportContext := map[string]any{
		"report_id": result.ReportID, "report_revision": result.Revision, "goal_version": goalVersion,
		"input_snapshot_hash": result.InputSnapshotHash, "input_nodes": selection.InputNodes,
		"input_documents": selection.Documents, "direction_coverage": selection.Directions, "maturity": selection.Maturity,
		"required_output": "self_contained_html_package",
		"supersedes":      map[string]any{"report_id": parentReportID, "revision": parentRevision, "summary": parentSummary, "plain_text": parentPlainText, "package_hash": parentPackageHash, "design_dossier": parentDesignDossier},
	}
	payload, _ := json.Marshal(map[string]any{
		"mission_prompt": "维护本次调研的动态交付报告。只使用每个一级研究方向当前最高层级的冻结节点；同一方向已被高层节点吸收的 M、L、XL 不得重复展开。报告仍在进行时，必须显著标注阶段性成果、正在收敛的方向和未覆盖缺口。修订既有报告时延续其视觉语言并更新内容，而不是另写一份互不相关的报告。",
		"report_id":      result.ReportID, "report_revision": result.Revision, "goal_version": goalVersion,
		"input_snapshot_hash": result.InputSnapshotHash, "input_nodes": selection.InputNodes,
		"artifact_version_ids": artifactVersionIDs, "required_output": "self_contained_html_package",
		"task_specific_schema": map[string]any{"report_context": reportContext},
	})
	if _, err = tx.Exec(ctx, `INSERT INTO research_work_item(id,workspace_id,session_id,kind,status,target_kind,target_id,client_key,idempotency_key,goal_version,input_state_version,input_event_sequence,created_by_director_cycle_id,assigned_agent_id,priority,max_attempts,payload_schema_id,expected_result_schema_id,payload,state_version,ready_at,reason) VALUES($1::uuid,$2::uuid,$3::uuid,'report','ready','report',$4::uuid,$5,$5,$6,$7,$8,$9::uuid,$10::uuid,0.9,3,'report.package.v1','report_package_submission',$11::jsonb,1,now(),$12)`, result.WorkItemID, in.WorkspaceID, in.RunID, result.ReportID, in.IdempotencyKey, goalVersion, stateVersion, eventSequence, in.DirectorCycleID, reporterAgentID, payload, in.Reason); err != nil {
		return V6ReportWork{}, err
	}
	if _, err = appendEvent(ctx, tx, in.WorkspaceID, in.RunID, "v6_report_work_created", "v6-report-work:"+in.IdempotencyKey, "system", "", v6ReportWorkCreatedEventPayload(result, in.DirectorCycleID)); err != nil {
		return V6ReportWork{}, err
	}
	if err = s.commitResearchTx(ctx, txOpV6ReportWorkCreate, tx); err != nil {
		return V6ReportWork{}, err
	}
	return result, nil
}
func v6ReportWorkCreatedEventPayload(result V6ReportWork, directorCycleID string) map[string]any {
	// A draft report has a passport but no artifact version until its package is
	// accepted. Referencing its ID here would falsely declare exact-version
	// lineage and make event materialization reject the entire transaction.
	return map[string]any{
		"report_revision":     result.Revision,
		"work_item_id":        result.WorkItemID,
		"input_snapshot_hash": result.InputSnapshotHash,
		"director_cycle_id":   directorCycleID,
	}
}
