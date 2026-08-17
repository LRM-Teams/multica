package researchrun

import (
	"context"
	"fmt"

	"github.com/jackc/pgx/v5"
)

func applyV6TerminalPropagationTx(ctx context.Context, tx pgx.Tx, workspaceID, runID, branchID, reason string) error {
	// The recursive branch set is authoritative. Completed facts remain immutable;
	// only live control work and discussions are moved to terminal/stale states.
	command, err := tx.Exec(ctx, `WITH RECURSIVE affected AS (
		SELECT id FROM research_branch WHERE workspace_id=$1::uuid AND session_id=$2::uuid AND id=$3::uuid
		UNION ALL SELECT b.id FROM research_branch b JOIN affected a ON b.parent_branch_id=a.id
	), changed AS (
		UPDATE research_branch b SET status='terminated',termination_reason=$4,reason_code='steering_terminated',reason_detail=$4,
			state_version=state_version+1,updated_at=now() FROM affected a WHERE b.id=a.id AND b.status IN ('proposed','active','paused') RETURNING b.id
	) SELECT count(*) FROM changed`, workspaceID, runID, branchID, reason)
	if err != nil {
		return err
	}
	if command.RowsAffected() == 0 {
		// pgx reports the SELECT command tag here, so entity existence is checked by
		// the caller's version lock instead of interpreting RowsAffected.
	}
	if _, err = tx.Exec(ctx, `WITH RECURSIVE affected AS (
		SELECT id FROM research_branch WHERE workspace_id=$1::uuid AND session_id=$2::uuid AND id=$3::uuid
		UNION ALL SELECT b.id FROM research_branch b JOIN affected a ON b.parent_branch_id=a.id
	), impacted_work AS (
		SELECT DISTINCT w.id FROM research_work_item w
		WHERE w.workspace_id=$1::uuid AND w.session_id=$2::uuid AND w.status IN ('pending','ready','dispatching','running','awaiting_input')
		AND (w.target_id IN (SELECT id FROM affected) OR EXISTS (
			SELECT 1 FROM jsonb_array_elements(COALESCE(w.payload->'branch_refs','[]'::jsonb)) ref
			WHERE (ref->>'id')::uuid IN (SELECT id FROM affected)))
	) UPDATE research_work_item w SET status='cancelled',terminal_reason_code='steering_terminal',terminal_reason_detail=$4,
		cancelled_at=now(),lease_token=NULL,lease_expires_at=NULL,updated_at=now() FROM impacted_work i WHERE w.id=i.id`, workspaceID, runID, branchID, reason); err != nil {
		return err
	}
	if _, err = tx.Exec(ctx, `UPDATE research_work_item_attempt a SET status='cancelled',failure_class='steering_terminal',diagnostics=$4,
		cancellation_completed_at=now(),completed_at=now(),updated_at=now() FROM research_work_item w
		WHERE a.work_item_id=w.id AND a.workspace_id=$1::uuid AND a.session_id=$2::uuid AND a.status IN ('dispatching','running')
		AND w.status='cancelled' AND w.terminal_reason_code='steering_terminal'`, workspaceID, runID, branchID, reason); err != nil {
		return err
	}
	if _, err = tx.Exec(ctx, `WITH RECURSIVE affected AS (
		SELECT id FROM research_branch WHERE workspace_id=$1::uuid AND session_id=$2::uuid AND id=$3::uuid
		UNION ALL SELECT b.id FROM research_branch b JOIN affected a ON b.parent_branch_id=a.id
	) UPDATE research_discussion d SET status='stale_input',stale_reason=$4,updated_at=now()
		WHERE d.workspace_id=$1::uuid AND d.session_id=$2::uuid AND d.status='active' AND EXISTS (
			SELECT 1 FROM research_discussion_input di JOIN research_node_branch nb ON nb.node_artifact_version_id=di.node_artifact_version_id
			WHERE di.discussion_id=d.id AND nb.branch_id IN (SELECT id FROM affected))`, workspaceID, runID, branchID, reason); err != nil {
		return err
	}
	if _, err = tx.Exec(ctx, `WITH RECURSIVE affected AS (
		SELECT id FROM research_branch WHERE workspace_id=$1::uuid AND session_id=$2::uuid AND id=$3::uuid
		UNION ALL SELECT b.id FROM research_branch b JOIN affected a ON b.parent_branch_id=a.id
	) UPDATE research_match_decision m SET invalidated_at=now(),invalidated_reason=$4
		WHERE m.workspace_id=$1::uuid AND m.session_id=$2::uuid AND m.invalidated_at IS NULL AND EXISTS (
			SELECT 1 FROM unnest(m.input_artifact_version_ids) input_id JOIN research_node_branch nb ON nb.node_artifact_version_id=input_id
			WHERE nb.branch_id IN (SELECT id FROM affected))`, workspaceID, runID, branchID, reason); err != nil {
		return fmt.Errorf("invalidate branch match decisions: %w", err)
	}
	return nil
}
