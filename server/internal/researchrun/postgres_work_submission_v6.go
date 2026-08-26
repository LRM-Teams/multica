package researchrun

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"

	"github.com/jackc/pgx/v5"
)

func (s *PostgresStore) AuthorizeV6Submission(ctx context.Context, access V6AttemptAccess) (V6SubmissionBinding, error) {
	var binding V6SubmissionBinding
	var manifest json.RawMessage
	err := s.pool.QueryRow(ctx, `
		SELECT a.manifest_id::text,a.manifest_hash,a.manifest,
		       a.manifest->>'expected_result_schema',COALESCE(a.manifest->'task_specific_schema','null'::jsonb),
		       COALESCE(w.payload_schema_id,'')
		FROM research_work_item_attempt a
		JOIN research_work_item w ON (w.workspace_id,w.session_id,w.id)=(a.workspace_id,a.session_id,a.work_item_id)
		JOIN research_team_membership m ON (m.workspace_id,m.session_id,m.id)=(a.workspace_id,a.session_id,a.membership_id)
		WHERE a.workspace_id=$1::uuid AND a.session_id=$2::uuid AND a.work_item_id=$3::uuid AND a.id=$4::uuid
		  AND a.assigned_agent_id=$5::uuid AND m.agent_id=$5::uuid AND m.state NOT IN ('archived','failed')
		  AND (
		    (a.status IN ('dispatching','running') AND w.status IN ('dispatching','running','awaiting_input'))
		    OR (
		      a.status IN ('succeeded','failed','cancelled')
		      AND EXISTS (
		        SELECT 1 FROM research_v6_work_submission sub
		        WHERE (sub.workspace_id,sub.session_id,sub.work_item_id,sub.attempt_id)=
		              (a.workspace_id,a.session_id,a.work_item_id,a.id)
		      )
		    )
		  )
		  AND ($6='' OR a.inbox_task_id=$6::uuid)
	`, access.WorkspaceID, access.RunID, access.WorkItemID, access.AttemptID, access.AgentID, access.InboxTaskID).Scan(
		&binding.ManifestID, &binding.ManifestHash, &manifest, &binding.ExpectedKind, &binding.TaskSchema, &binding.TaskSchemaID,
	)
	if errors.Is(err, pgx.ErrNoRows) {
		return V6SubmissionBinding{}, ErrAttemptNotAssigned
	}
	if err != nil {
		return V6SubmissionBinding{}, err
	}
	if _, exists := v6ContractDefinition[binding.ExpectedKind]; !exists || binding.ExpectedKind == V6ContractWorkManifest || binding.ExpectedKind == V6ContractDirectorBrief || binding.ExpectedKind == V6ContractProjectionSnapshot || binding.ExpectedKind == V6ContractProjectionDelta {
		return V6SubmissionBinding{}, fmt.Errorf("%w: unauthorized expected result schema %q", ErrInvalidContract, binding.ExpectedKind)
	}
	return binding, nil
}

// RejectV6DirectorBoundaryAttempt settles an autonomous Director cycle when
// its envelope fails the strict HTTP boundary before a Submission can be
// recorded. Keeping the Attempt active would hide the failure from the event
// trigger and leave the Run waiting for a long execution lease.
func (s *PostgresStore) RejectV6DirectorBoundaryAttempt(ctx context.Context, access V6AttemptAccess, diagnostic string) error {
	tx, err := s.beginResearchTx(ctx, txOpV6DirectorProposalComplete, pgx.TxOptions{})
	if err != nil {
		return err
	}
	defer tx.Rollback(ctx)
	if err = lockRunForMutation(ctx, tx, access.RunID, access.WorkspaceID); err != nil {
		return err
	}
	var membershipID string
	err = tx.QueryRow(ctx, `UPDATE research_work_item_attempt a
		SET status='failed',failure_class='contract_rejected',diagnostics=$6,completed_at=now(),updated_at=now()
		FROM research_work_item w
		WHERE (w.workspace_id,w.session_id,w.id)=(a.workspace_id,a.session_id,a.work_item_id)
		  AND a.workspace_id=$1::uuid AND a.session_id=$2::uuid AND a.work_item_id=$3::uuid AND a.id=$4::uuid
		  AND a.assigned_agent_id=$5::uuid AND a.status IN ('dispatching','running')
		  AND w.kind='director' AND w.expected_result_schema_id='director_action_proposal'
		  AND w.status IN ('dispatching','running','awaiting_input')
		  AND ($7='' OR a.inbox_task_id=$7::uuid)
		RETURNING a.membership_id::text`, access.WorkspaceID, access.RunID, access.WorkItemID, access.AttemptID,
		access.AgentID, diagnostic, access.InboxTaskID).Scan(&membershipID)
	if errors.Is(err, pgx.ErrNoRows) {
		return ErrAttemptNotAssigned
	}
	if err != nil {
		return err
	}
	if _, err = tx.Exec(ctx, `UPDATE research_work_item
		SET status='failed',terminal_reason_code='contract_rejected',terminal_reason_detail=$5,
		    lease_token=NULL,lease_expires_at=NULL,state_version=state_version+1,updated_at=now()
		WHERE workspace_id=$1::uuid AND session_id=$2::uuid AND id=$3::uuid
		  AND assigned_agent_id=$4::uuid AND kind='director' AND status IN ('dispatching','running','awaiting_input')`,
		access.WorkspaceID, access.RunID, access.WorkItemID, access.AgentID, diagnostic); err != nil {
		return err
	}
	if _, err = tx.Exec(ctx, `UPDATE research_director_cycle
		SET status='failed',failure_class='contract_rejected',diagnostics=$4,completed_at=now()
		WHERE workspace_id=$1::uuid AND session_id=$2::uuid AND work_item_id=$3::uuid
		  AND status IN ('pending','running')`, access.WorkspaceID, access.RunID, access.WorkItemID, diagnostic); err != nil {
		return err
	}
	if _, err = tx.Exec(ctx, `UPDATE research_team_membership m SET state='idle'
		WHERE m.workspace_id=$1::uuid AND m.session_id=$2::uuid AND m.id=$3::uuid AND m.state='working'
		  AND NOT EXISTS (
		    SELECT 1 FROM research_work_item_attempt active
		    WHERE active.workspace_id=m.workspace_id AND active.session_id=m.session_id
		      AND active.membership_id=m.id AND active.status IN ('dispatching','running')
		  )`, access.WorkspaceID, access.RunID, membershipID); err != nil {
		return err
	}
	if _, err = appendEvent(ctx, tx, access.WorkspaceID, access.RunID, "v6_work_submission_rejected",
		"v6-director-boundary-rejected:"+access.AttemptID, "system", "", map[string]any{
			"work_item_id": access.WorkItemID, "work_item_attempt_id": access.AttemptID,
			"contract_kind": V6ContractDirectorActionProposal, "failure_class": "contract_rejected",
			"reason": diagnostic, "boundary_rejection": true,
		}); err != nil {
		return err
	}
	return s.commitResearchTx(ctx, txOpV6DirectorProposalComplete, tx)
}

func (s *PostgresStore) RecordV6Submission(ctx context.Context, access V6AttemptAccess, decoded DecodedV6Contract, requestID string) (V6SubmissionOutcome, error) {
	tx, err := s.beginResearchTx(ctx, txOpV6SubmissionRecord, pgx.TxOptions{})
	if err != nil {
		return V6SubmissionOutcome{}, err
	}
	defer tx.Rollback(ctx)
	if err = lockRunForMutation(ctx, tx, access.RunID, access.WorkspaceID); err != nil {
		return V6SubmissionOutcome{}, err
	}
	outcome := V6SubmissionOutcome{ClientRequestID: requestID, ContentHash: decoded.ContentHash, Kind: decoded.Kind, Status: "received"}
	err = tx.QueryRow(ctx, `
		INSERT INTO research_v6_work_submission (
		  workspace_id,session_id,work_item_id,attempt_id,client_request_id,contract_kind,content_hash,envelope
		)
		SELECT a.workspace_id,a.session_id,a.work_item_id,a.id,$5::uuid,$6,$7,$8::jsonb
		FROM research_work_item_attempt a
		JOIN research_team_membership m ON (m.workspace_id,m.session_id,m.id)=(a.workspace_id,a.session_id,a.membership_id)
		WHERE a.workspace_id=$1::uuid AND a.session_id=$2::uuid AND a.work_item_id=$3::uuid AND a.id=$4::uuid
		  AND a.assigned_agent_id=$9::uuid AND m.agent_id=$9::uuid AND m.state NOT IN ('archived','failed')
		  AND a.status IN ('dispatching','running') AND ($10='' OR a.inbox_task_id=$10::uuid)
		ON CONFLICT (workspace_id,session_id,client_request_id) DO NOTHING
		RETURNING id::text,status
	`, access.WorkspaceID, access.RunID, access.WorkItemID, access.AttemptID, requestID, decoded.Kind, decoded.ContentHash, decoded.Canonical, access.AgentID, access.InboxTaskID).Scan(&outcome.SubmissionID, &outcome.Status)
	if errors.Is(err, pgx.ErrNoRows) {
		var storedHash, storedKind string
		err = tx.QueryRow(ctx, `SELECT id::text,content_hash,contract_kind,status FROM research_v6_work_submission
			WHERE workspace_id=$1::uuid AND session_id=$2::uuid AND client_request_id=$3::uuid`,
			access.WorkspaceID, access.RunID, requestID).Scan(&outcome.SubmissionID, &storedHash, &storedKind, &outcome.Status)
		if errors.Is(err, pgx.ErrNoRows) {
			return V6SubmissionOutcome{}, ErrAttemptNotAssigned
		}
		if err != nil {
			return V6SubmissionOutcome{}, err
		}
		if storedHash != decoded.ContentHash || storedKind != string(decoded.Kind) {
			return V6SubmissionOutcome{}, ErrV6IdempotencyConflict
		}
		outcome.Replayed = true
	} else if err != nil {
		return V6SubmissionOutcome{}, err
	}
	event, err := appendEvent(ctx, tx, access.WorkspaceID, access.RunID, "v6_work_submission_received",
		"v6-submission:"+requestID, "agent", access.AgentID,
		map[string]any{"submission_id": outcome.SubmissionID, "work_item_id": access.WorkItemID, "work_item_attempt_id": access.AttemptID, "contract_kind": decoded.Kind, "content_hash": decoded.ContentHash})
	if err != nil {
		return V6SubmissionOutcome{}, err
	}
	outcome.StateVersion = event.Sequence
	outcome.ThroughEventSequence = event.Sequence
	if err = s.commitResearchTx(ctx, txOpV6SubmissionRecord, tx); err != nil {
		return V6SubmissionOutcome{}, err
	}
	return outcome, nil
}
