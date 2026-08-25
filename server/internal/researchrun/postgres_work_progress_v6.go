package researchrun

import (
	"context"
	"errors"
	"fmt"

	"github.com/jackc/pgx/v5"
)

// ReportV6WorkProgress appends an operational progress note for an active
// attempt. The note becomes a `v6_work_progress_reported` Run Event whose
// payload feeds live presence. A report is also a liveness heartbeat: it
// slides the work item lease forward so a long but visibly active turn is not
// recovered as expired mid-work.
func (s *PostgresStore) ReportV6WorkProgress(ctx context.Context, in ReportV6WorkProgressInput) error {
	tx, err := s.beginResearchTx(ctx, txOpV6WorkProgressReport, pgx.TxOptions{})
	if err != nil {
		return err
	}
	defer tx.Rollback(ctx)
	if err = lockRunForMutation(ctx, tx, in.RunID, in.WorkspaceID); err != nil {
		return err
	}
	// Same task fence as the read/acknowledge endpoints: the attempt must be
	// in flight, assigned to this Agent, and bound to its Inbox task.
	var noteCount int
	err = tx.QueryRow(ctx, `
		SELECT (
			SELECT count(*)::int FROM research_run_event e
			WHERE e.session_id=$2::uuid AND e.event_type=$7
			  AND e.payload->>'attempt_id'=$4
		)
		FROM research_work_item_attempt a
		JOIN research_team_membership m
		  ON (m.workspace_id,m.session_id,m.id)=(a.workspace_id,a.session_id,a.membership_id)
		WHERE a.workspace_id=$1::uuid AND a.session_id=$2::uuid AND a.work_item_id=$3::uuid AND a.id=$4::uuid
		  AND a.assigned_agent_id=$5::uuid AND m.agent_id=$5::uuid
		  AND a.status IN ('dispatching','running')
		  AND ($6='' OR a.inbox_task_id IS NULL OR a.inbox_task_id=$6::uuid)
	`, in.WorkspaceID, in.RunID, in.WorkItemID, in.AttemptID, in.AgentID, in.InboxTaskID,
		V6WorkProgressEventType).Scan(&noteCount)
	if errors.Is(err, pgx.ErrNoRows) {
		return ErrAttemptNotAssigned
	}
	if err != nil {
		return err
	}
	if noteCount >= maxV6WorkProgressNotesPerAttempt {
		return fmt.Errorf("%w: progress note budget exhausted for this attempt", ErrInvalidContract)
	}
	// Heartbeat: extend the lease only forward, never shorten it. The fence
	// above already proved the reporting attempt is the in-flight one.
	if _, err = tx.Exec(ctx, `UPDATE research_work_item SET lease_expires_at=GREATEST(lease_expires_at, now()+interval '20 minutes'),updated_at=now()
		WHERE id=$1::uuid AND status IN ('dispatching','running') AND lease_expires_at IS NOT NULL`, in.WorkItemID); err != nil {
		return err
	}
	payload := map[string]any{
		"work_item_id": in.WorkItemID,
		"attempt_id":   in.AttemptID,
		"agent_id":     in.AgentID,
		"text":         in.Text,
	}
	if in.Stage != "" {
		payload["stage"] = in.Stage
	}
	if _, err = appendEvent(ctx, tx, in.WorkspaceID, in.RunID, V6WorkProgressEventType,
		"v6-progress:"+in.ClientRequestID, "agent", in.AgentID, payload); err != nil {
		return err
	}
	return s.commitResearchTx(ctx, txOpV6WorkProgressReport, tx)
}
