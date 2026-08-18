package researchrun

import (
	"context"
	"errors"
	"fmt"

	"github.com/jackc/pgx/v5"
)

func (s *PostgresStore) ClaimV6WorkItem(ctx context.Context, in ClaimV6WorkItemInput) (V6WorkItemLease, error) {
	tx, err := s.beginResearchTx(ctx, txOpV6WorkItemClaim, pgx.TxOptions{})
	if err != nil {
		return V6WorkItemLease{}, err
	}
	defer tx.Rollback(ctx)
	if err = lockRunForMutation(ctx, tx, in.RunID, in.WorkspaceID); err != nil {
		return V6WorkItemLease{}, err
	}
	var item V6WorkItem
	var expiresAt = in.Now.Add(in.LeaseDuration)
	err = tx.QueryRow(ctx, `
		UPDATE research_work_item
		SET status='running', lease_token=$5::uuid, lease_expires_at=$6,
		    state_version=state_version+1, started_at=COALESCE(started_at,$4), updated_at=$4
		WHERE id=$1::uuid AND workspace_id=$2::uuid AND session_id=$3::uuid
		  AND state_version=$7
		  AND (status='ready' OR (status IN ('dispatching','running') AND lease_expires_at <= $4))
		RETURNING id::text, workspace_id::text, session_id::text, target_kind,
		          COALESCE(target_id::text,''), kind, status, COALESCE(goal_version,0),
		          state_version, input_event_sequence
	`, in.WorkItemID, in.WorkspaceID, in.RunID, in.Now, in.LeaseToken, expiresAt, in.ExpectedStateVersion).Scan(
		&item.ID, &item.WorkspaceID, &item.RunID, &item.TargetKind, &item.TargetID,
		&item.Kind, &item.Status, &item.GoalVersion, &item.StateVersion, &item.EventSequence,
	)
	if errors.Is(err, pgx.ErrNoRows) {
		return V6WorkItemLease{}, ErrWorkItemChanged
	}
	if err != nil {
		return V6WorkItemLease{}, err
	}
	if _, err = appendEvent(ctx, tx, item.WorkspaceID, item.RunID, "v6_work_item_claimed",
		fmt.Sprintf("v6-work-item-claimed:%s:%d", item.ID, item.StateVersion), "system", "",
		map[string]any{"work_item_id": item.ID, "state_version": item.StateVersion}); err != nil {
		return V6WorkItemLease{}, err
	}
	if err = s.commitResearchTx(ctx, txOpV6WorkItemClaim, tx); err != nil {
		return V6WorkItemLease{}, err
	}
	return V6WorkItemLease{WorkItem: item, Token: in.LeaseToken, ExpiresAt: expiresAt}, nil
}

func (s *PostgresStore) CompleteV6WorkItem(ctx context.Context, in CompleteV6WorkItemInput) (V6WorkItem, error) {
	tx, err := s.beginResearchTx(ctx, txOpV6WorkItemComplete, pgx.TxOptions{})
	if err != nil {
		return V6WorkItem{}, err
	}
	defer tx.Rollback(ctx)
	if err = lockRunForMutation(ctx, tx, in.RunID, in.WorkspaceID); err != nil {
		return V6WorkItem{}, err
	}
	var item V6WorkItem
	err = tx.QueryRow(ctx, `
		UPDATE research_work_item
		SET status='succeeded', lease_token=NULL, lease_expires_at=NULL,
		    state_version=state_version+1, completed_at=now(), updated_at=now()
		WHERE id=$1::uuid AND workspace_id=$2::uuid AND session_id=$3::uuid
		  AND state_version=$4 AND status='running' AND lease_token=$5::uuid
		  AND lease_expires_at > now()
		RETURNING id::text, workspace_id::text, session_id::text, target_kind,
		          COALESCE(target_id::text,''), kind, status, COALESCE(goal_version,0),
		          state_version, input_event_sequence
	`, in.WorkItemID, in.WorkspaceID, in.RunID, in.ExpectedStateVersion, in.LeaseToken).Scan(
		&item.ID, &item.WorkspaceID, &item.RunID, &item.TargetKind, &item.TargetID,
		&item.Kind, &item.Status, &item.GoalVersion, &item.StateVersion, &item.EventSequence,
	)
	if errors.Is(err, pgx.ErrNoRows) {
		return V6WorkItem{}, ErrWorkItemLeaseLost
	}
	if err != nil {
		return V6WorkItem{}, err
	}
	if _, err = appendEvent(ctx, tx, item.WorkspaceID, item.RunID, "v6_work_item_succeeded",
		"v6-work-item-succeeded:"+item.ID, "system", "", map[string]any{"work_item_id": item.ID}); err != nil {
		return V6WorkItem{}, err
	}
	if err = s.commitResearchTx(ctx, txOpV6WorkItemComplete, tx); err != nil {
		return V6WorkItem{}, err
	}
	return item, nil
}
