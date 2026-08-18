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
		  AND a.status IN ('dispatching','running') AND w.status IN ('dispatching','running','awaiting_input')
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
