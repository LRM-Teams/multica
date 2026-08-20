package researchrun

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"sort"

	"github.com/jackc/pgx/v5"
)

type v6DirectorProposal struct {
	WorkspaceID          string             `json:"workspace_id"`
	RunID                string             `json:"run_id"`
	WorkItemID           string             `json:"work_item_id"`
	AttemptID            string             `json:"attempt_id"`
	ManifestID           string             `json:"manifest_id"`
	ManifestHash         string             `json:"manifest_hash"`
	DirectorAssignmentID string             `json:"director_assignment_id"`
	BriefID              string             `json:"brief_id"`
	BriefHash            string             `json:"brief_hash"`
	DirectorGeneration   int                `json:"director_generation"`
	ReviewedPageCount    int                `json:"reviewed_page_count"`
	ExpectedStateVersion int64              `json:"expected_state_version"`
	ThroughEventSequence int64              `json:"through_event_sequence"`
	Actions              []v6DirectorAction `json:"actions"`
}
type v6DirectorAction struct {
	ActionID             string          `json:"action_id"`
	Kind                 string          `json:"kind"`
	Reason               string          `json:"reason"`
	IdempotencyKey       string          `json:"idempotency_key"`
	PayloadSchema        string          `json:"payload_schema"`
	ExpectedStateVersion int64           `json:"expected_state_version"`
	Payload              json.RawMessage `json:"payload"`
	DependsOnActionIDs   []string        `json:"depends_on_action_ids"`
}

// rejectMaterialV6EventsAfterDirectorBrief separates the frozen research
// watermark from operational events needed to deliver and submit that same
// Director cycle. appendEvent advances the Run sequence for both categories;
// delivery, page-review, and submission bookkeeping must not make the Brief
// stale by construction.
func (s *PostgresStore) rejectMaterialV6EventsAfterDirectorBrief(ctx context.Context, proposal v6DirectorProposal, cycleID string, throughSequence int64) error {
	rows, err := s.pool.Query(ctx, `SELECT event_type,payload FROM research_run_event
		WHERE workspace_id=$1::uuid AND session_id=$2::uuid AND sequence>$3 ORDER BY sequence`,
		proposal.WorkspaceID, proposal.RunID, throughSequence)
	if err != nil {
		return err
	}
	defer rows.Close()
	for rows.Next() {
		var eventType string
		var payload json.RawMessage
		if err = rows.Scan(&eventType, &payload); err != nil {
			return err
		}
		if !isV6DirectorCycleOperationalEvent(eventType, payload, cycleID, proposal.WorkItemID, proposal.BriefID) {
			return ErrWorkItemChanged
		}
	}
	return rows.Err()
}

func isV6DirectorCycleOperationalEvent(eventType string, payload json.RawMessage, cycleID, workItemID, briefID string) bool {
	var identity struct {
		CycleID    string `json:"cycle_id"`
		WorkItemID string `json:"work_item_id"`
		BriefID    string `json:"brief_id"`
	}
	if json.Unmarshal(payload, &identity) != nil {
		return false
	}
	switch eventType {
	case "v6_director_cycle_created":
		return identity.CycleID == cycleID && identity.WorkItemID == workItemID && identity.BriefID == briefID
	case "v6_work_item_dispatch_prepared", "v6_work_item_dispatched", "v6_work_item_recovered", "v6_work_submission_received":
		return identity.WorkItemID == workItemID
	case "v6_director_brief_page_acknowledged":
		return identity.BriefID == briefID
	default:
		return false
	}
}

func (s *PostgresStore) ApplyReceivedV6DirectorProposals(ctx context.Context, limit int) (int, error) {
	applied := 0
	for applied < limit {
		var submissionID string
		var envelope json.RawMessage
		tx, err := s.beginResearchTx(ctx, txOpV6DirectorProposalClaim, pgx.TxOptions{})
		if err != nil {
			return applied, err
		}
		err = tx.QueryRow(ctx, `SELECT sub.id::text,sub.envelope FROM research_v6_work_submission sub
			JOIN research_work_item w ON w.id=sub.work_item_id
			WHERE (sub.status='received' OR (sub.status='processing' AND sub.updated_at<now()-interval '1 minute'))
			AND sub.contract_kind='director_action_proposal' AND w.client_key LIKE 'director-cycle:%'
			ORDER BY sub.created_at,sub.id FOR UPDATE OF sub SKIP LOCKED LIMIT 1`).Scan(&submissionID, &envelope)
		if errors.Is(err, pgx.ErrNoRows) {
			_ = tx.Rollback(ctx)
			return applied, nil
		}
		if err == nil {
			_, err = tx.Exec(ctx, `UPDATE research_v6_work_submission SET status='processing',updated_at=now() WHERE id=$1::uuid`, submissionID)
		}
		if err == nil {
			err = s.commitResearchTx(ctx, txOpV6DirectorProposalClaim, tx)
		} else {
			_ = tx.Rollback(ctx)
		}
		if err != nil {
			return applied, err
		}
		applyErr := s.executeV6DirectorProposal(ctx, submissionID, envelope)
		if applyErr != nil {
			if !isTerminalV6SubmissionError(applyErr) {
				return applied, applyErr
			}
			if err = s.rejectV6DirectorProposal(context.WithoutCancel(ctx), submissionID, applyErr.Error()); err != nil {
				return applied, err
			}
		}
		applied++
	}
	return applied, nil
}

func (s *PostgresStore) rejectV6DirectorProposal(ctx context.Context, submissionID, reason string) error {
	tx, err := s.beginResearchTx(ctx, txOpV6DirectorProposalComplete, pgx.TxOptions{})
	if err != nil {
		return err
	}
	defer tx.Rollback(ctx)
	if _, err = tx.Exec(ctx, `UPDATE research_v6_work_submission SET status='rejected',outcome=jsonb_build_object('error',$2),updated_at=now() WHERE id=$1::uuid`, submissionID, reason); err != nil {
		return err
	}
	if _, err = tx.Exec(ctx, `UPDATE research_work_item_attempt a SET status='failed',failure_class='contract_rejected',diagnostics=$2,
		completed_at=now(),updated_at=now() FROM research_v6_work_submission s WHERE s.id=$1::uuid AND a.id=s.attempt_id AND a.status IN ('dispatching','running')`, submissionID, reason); err != nil {
		return err
	}
	if _, err = tx.Exec(ctx, `UPDATE research_work_item w SET status='failed',terminal_reason_code='contract_rejected',terminal_reason_detail=$2,
		lease_token=NULL,lease_expires_at=NULL,updated_at=now() FROM research_v6_work_submission s WHERE s.id=$1::uuid AND w.id=s.work_item_id`, submissionID, reason); err != nil {
		return err
	}
	if _, err = tx.Exec(ctx, `UPDATE research_director_cycle c SET status='failed',failure_class='contract_rejected',diagnostics=$2,completed_at=now()
		FROM research_v6_work_submission s WHERE s.id=$1::uuid AND c.work_item_id=s.work_item_id AND c.status IN ('pending','running')`, submissionID, reason); err != nil {
		return err
	}
	return s.commitResearchTx(ctx, txOpV6DirectorProposalComplete, tx)
}

func (s *PostgresStore) executeV6DirectorProposal(ctx context.Context, submissionID string, envelope json.RawMessage) error {
	var proposal v6DirectorProposal
	if err := json.Unmarshal(envelope, &proposal); err != nil {
		return fmt.Errorf("%w: director proposal", ErrInvalidContract)
	}
	for _, value := range []string{proposal.WorkspaceID, proposal.RunID, proposal.WorkItemID, proposal.AttemptID, proposal.ManifestID, proposal.DirectorAssignmentID, proposal.BriefID} {
		if !validV6ActionUUID(value) {
			return fmt.Errorf("%w: director proposal UUID", ErrInvalidContract)
		}
	}
	order, err := validateV6DirectorActionDAG(proposal.Actions)
	if err != nil {
		return err
	}
	var cycleID string
	var goalVersion, generation, pageCount, reviewedCount int
	var briefStateVersion, liveStateVersion, throughSequence int64
	err = s.pool.QueryRow(ctx, `SELECT c.id::text,w.goal_version,c.director_generation,c.page_count,
		(SELECT count(*)::int FROM research_director_brief_page p WHERE p.director_cycle_id=c.id AND p.reviewed_at IS NOT NULL),
		w.input_state_version,s.state_version,w.input_event_sequence
		FROM research_v6_work_submission sub JOIN research_work_item_attempt a ON a.id=sub.attempt_id
		JOIN research_work_item w ON w.id=a.work_item_id JOIN research_director_cycle c ON c.work_item_id=w.id
		JOIN research_session s ON s.id=w.session_id WHERE sub.id=$1::uuid AND sub.workspace_id=$2::uuid AND sub.session_id=$3::uuid
		AND sub.work_item_id=$4::uuid AND sub.attempt_id=$5::uuid AND a.manifest_id=$6::uuid AND a.manifest_hash=$7
		AND c.director_assignment_id=$8::uuid AND c.brief_id=$9::uuid AND c.brief_hash=$10 AND a.status='running'`, submissionID,
		proposal.WorkspaceID, proposal.RunID, proposal.WorkItemID, proposal.AttemptID, proposal.ManifestID, proposal.ManifestHash,
		proposal.DirectorAssignmentID, proposal.BriefID, proposal.BriefHash).Scan(&cycleID, &goalVersion, &generation, &pageCount, &reviewedCount, &briefStateVersion, &liveStateVersion, &throughSequence)
	if errors.Is(err, pgx.ErrNoRows) {
		return ErrAttemptNotAssigned
	}
	if err != nil {
		return err
	}
	if proposal.DirectorGeneration != generation || proposal.ReviewedPageCount != pageCount || reviewedCount != pageCount ||
		proposal.ExpectedStateVersion != briefStateVersion || proposal.ThroughEventSequence != throughSequence {
		return ErrWorkItemChanged
	}
	if err = s.rejectMaterialV6EventsAfterDirectorBrief(ctx, proposal, cycleID, throughSequence); err != nil {
		return err
	}
	results := make([]map[string]any, 0, len(order))
	for _, index := range order {
		action := proposal.Actions[index]
		if action.ExpectedStateVersion != 0 && action.ExpectedStateVersion != briefStateVersion {
			return ErrWorkItemChanged
		}
		switch action.Kind {
		case "no_op":
			var payload struct {
				MessageID string `json:"message_id"`
				Reason    string `json:"reason"`
			}
			if action.PayloadSchema != "no_op.v1" || json.Unmarshal(action.Payload, &payload) != nil {
				return ErrInvalidContract
			}
			if payload.MessageID != "" {
				_, err = s.ApplyV6SteeringAssessment(ctx, ApplyV6SteeringAssessmentInput{WorkspaceID: proposal.WorkspaceID, RunID: proposal.RunID,
					MessageID: payload.MessageID, DirectorCycleID: cycleID, AssessmentKind: "no_op", Interpretation: action.Reason,
					Reason: payload.Reason, ExpectedGoalVersion: goalVersion, ExpectedStateVersion: liveStateVersion, AcceptedActionIDs: []string{action.ActionID}})
			} else {
				err = s.recordV6DirectorNoOp(ctx, proposal, cycleID, action, liveStateVersion, payload.Reason)
			}
		case "record_decision":
			if action.PayloadSchema != "steering_assessment.v1" {
				return ErrInvalidContract
			}
			var payload struct {
				MessageID           string             `json:"message_id"`
				AssessmentKind      string             `json:"assessment_kind"`
				Interpretation      string             `json:"interpretation"`
				Reason              string             `json:"reason"`
				RevisedGoal         string             `json:"revised_goal"`
				Impacts             []V6SteeringImpact `json:"impacts"`
				RevisedScope        json.RawMessage    `json:"revised_scope"`
				RevisedSourcePolicy json.RawMessage    `json:"revised_source_policy"`
				RevisedLimits       json.RawMessage    `json:"revised_limits"`
				RevisedAudience     string             `json:"revised_audience"`
				RevisedFreshness    string             `json:"revised_freshness"`
				RevisedLanguage     string             `json:"revised_language"`
			}
			if json.Unmarshal(action.Payload, &payload) != nil {
				return ErrInvalidContract
			}
			_, err = (steeringV6Module{store: s}).Apply(ctx, ApplyV6SteeringAssessmentInput{WorkspaceID: proposal.WorkspaceID, RunID: proposal.RunID,
				MessageID: payload.MessageID, DirectorCycleID: cycleID, AssessmentKind: payload.AssessmentKind, Interpretation: payload.Interpretation,
				Reason: payload.Reason, ExpectedGoalVersion: goalVersion, ExpectedStateVersion: liveStateVersion, Impacts: payload.Impacts,
				AcceptedActionIDs: []string{action.ActionID}, RevisedGoal: payload.RevisedGoal, RevisedScope: payload.RevisedScope,
				RevisedSourcePolicy: payload.RevisedSourcePolicy, RevisedLimits: payload.RevisedLimits, RevisedAudience: payload.RevisedAudience,
				RevisedFreshness: payload.RevisedFreshness, RevisedLanguage: payload.RevisedLanguage})
		case "create_agent":
			err = s.executeV6CreateAgentAction(ctx, proposal, cycleID, action, liveStateVersion)
		case "create_work_item", "create_task":
			err = s.executeV6CreateWorkAction(ctx, proposal, cycleID, action, liveStateVersion)
		case "create_match", "open_discussion", "create_dispute", "create_integration", "create_review":
			collaborationKind := map[string]string{"create_match": "match", "open_discussion": "discussion", "create_dispute": "resolve_conflict", "create_integration": "integration", "create_review": "review"}[action.Kind]
			action.Payload = withV6ActionKind(action.Payload, collaborationKind)
			err = s.executeV6CreateWorkAction(ctx, proposal, cycleID, action, liveStateVersion)
		case "create_branch":
			err = s.executeV6CreateBranchAction(ctx, proposal, cycleID, action, liveStateVersion)
		case "create_report":
			err = s.executeV6CreateReportAction(ctx, proposal, cycleID, action, goalVersion, liveStateVersion)
		case "revise_report", "reject_report", "publish_report":
			err = s.executeV6ReportReviewAction(ctx, proposal, cycleID, action, liveStateVersion)
		case "challenge_node", "terminate_node":
			err = s.executeV6NodeDecisionAction(ctx, proposal, action, liveStateVersion)
		case "assign_steward":
			err = s.executeV6AssignStewardAction(ctx, proposal, action, liveStateVersion)
		case "revise_goal":
			err = s.executeV6ReviseGoalAction(ctx, proposal, action, liveStateVersion)
		case "adjudicate_discussion":
			action.Payload = withV6ActionKind(action.Payload, "discussion")
			err = s.executeV6CreateWorkAction(ctx, proposal, cycleID, action, liveStateVersion)
		case "cancel_work_item", "retry_work_item", "reassign_work_item":
			err = s.executeV6WorkLifecycleAction(ctx, proposal, action, liveStateVersion)
		case "update_agent", "archive_agent":
			err = s.executeV6AgentLifecycleAction(ctx, proposal, cycleID, action, liveStateVersion)
		case "update_branch", "pause_branch", "terminate_branch", "split_branch", "merge_branch":
			if action.Kind == "split_branch" {
				err = s.executeV6SplitBranchAction(ctx, proposal, cycleID, action, liveStateVersion)
			} else {
				err = s.executeV6BranchLifecycleAction(ctx, proposal, action, liveStateVersion)
			}
		case "pause_run", "resume_run", "complete_run", "fail_run":
			err = s.executeV6RunLifecycleAction(ctx, proposal, action, liveStateVersion)
		default:
			return fmt.Errorf("%w: unsupported Director action %q", ErrInvalidContract, action.Kind)
		}
		if err != nil {
			return err
		}
		results = append(results, map[string]any{"action_id": action.ActionID, "status": "accepted"})
		liveStateVersion++
	}
	return s.completeV6DirectorProposal(ctx, submissionID, cycleID, proposal, envelope, results)
}

func validateV6DirectorActionDAG(actions []v6DirectorAction) ([]int, error) {
	if len(actions) == 0 {
		return nil, ErrInvalidContract
	}
	if len(actions) > 1 {
		for _, a := range actions {
			if a.Kind == "no_op" {
				return nil, ErrInvalidContract
			}
		}
	}
	indices, indegree, edges := map[string]int{}, make([]int, len(actions)), make([][]int, len(actions))
	for i, action := range actions {
		if action.ActionID == "" || action.IdempotencyKey == "" || action.PayloadSchema == "" {
			return nil, ErrInvalidContract
		}
		if _, ok := indices[action.ActionID]; ok {
			return nil, ErrInvalidContract
		}
		indices[action.ActionID] = i
	}
	for i, action := range actions {
		for _, dependency := range action.DependsOnActionIDs {
			j, ok := indices[dependency]
			if !ok || j == i {
				return nil, ErrInvalidContract
			}
			edges[j] = append(edges[j], i)
			indegree[i]++
		}
	}
	ready := []int{}
	for i, degree := range indegree {
		if degree == 0 {
			ready = append(ready, i)
		}
	}
	sort.Slice(ready, func(i, j int) bool { return actions[ready[i]].ActionID < actions[ready[j]].ActionID })
	order := []int{}
	for len(ready) > 0 {
		current := ready[0]
		ready = ready[1:]
		order = append(order, current)
		for _, next := range edges[current] {
			indegree[next]--
			if indegree[next] == 0 {
				ready = append(ready, next)
				sort.Slice(ready, func(i, j int) bool { return actions[ready[i]].ActionID < actions[ready[j]].ActionID })
			}
		}
	}
	if len(order) != len(actions) {
		return nil, ErrInvalidContract
	}
	return order, nil
}

func (s *PostgresStore) completeV6DirectorProposal(ctx context.Context, submissionID, cycleID string, proposal v6DirectorProposal, envelope json.RawMessage, results []map[string]any) error {
	tx, err := s.beginResearchTx(ctx, txOpV6DirectorProposalComplete, pgx.TxOptions{})
	if err != nil {
		return err
	}
	defer tx.Rollback(ctx)
	resultRaw, _ := json.Marshal(results)
	_, err = tx.Exec(ctx, `UPDATE research_director_cycle SET status='applied',proposal=$2::jsonb,proposal_hash=$3,execution_result=$4::jsonb,
		completed_at=now(),state_version=state_version+1 WHERE id=$1::uuid AND status IN ('pending','running')`, cycleID, envelope, ArtifactContentHashFromCanonicalJSON(envelope), resultRaw)
	if err == nil {
		_, err = tx.Exec(ctx, `UPDATE research_work_item_attempt SET status='succeeded',result_kind='director_action_proposal',result_entity_id=$2::uuid,
		result_hash=$3,result_submitted_at=now(),completed_at=now(),updated_at=now() WHERE id=$1::uuid AND status='running'`, proposal.AttemptID, cycleID, ArtifactContentHashFromCanonicalJSON(envelope))
	}
	if err == nil {
		_, err = tx.Exec(ctx, `UPDATE research_work_item SET status='succeeded',completed_at=now(),lease_token=NULL,lease_expires_at=NULL,updated_at=now() WHERE id=$1::uuid`, proposal.WorkItemID)
	}
	if err == nil {
		_, err = tx.Exec(ctx, `UPDATE research_team_membership m SET state='idle' FROM research_work_item_attempt a
			WHERE a.id=$1::uuid AND m.id=a.membership_id AND m.state='working'`, proposal.AttemptID)
	}
	if err == nil {
		_, err = tx.Exec(ctx, `UPDATE research_v6_work_submission SET status='accepted',outcome=$2::jsonb,updated_at=now() WHERE id=$1::uuid`, submissionID, resultRaw)
	}
	if err != nil {
		return err
	}
	return s.commitResearchTx(ctx, txOpV6DirectorProposalComplete, tx)
}
