package researchrun

import (
	"context"
	"errors"

	"github.com/jackc/pgx/v5"
)

func (s *PostgresStore) LoadV6WorkCatalog(ctx context.Context, in V6CatalogRequest) (V6CatalogPage, error) {
	var page V6CatalogPage
	err := s.pool.QueryRow(ctx, `
		SELECT p.page_key,p.content_hash,p.page,COALESCE(p.next_cursor,''),p.next_cursor IS NOT NULL
		FROM research_work_catalog_page p
		JOIN research_work_item_attempt a ON (a.workspace_id,a.session_id,a.id)=(p.workspace_id,p.session_id,p.work_item_attempt_id)
		JOIN research_team_membership m ON (m.workspace_id,m.session_id,m.id)=(a.workspace_id,a.session_id,a.membership_id)
		WHERE p.workspace_id=$1::uuid AND p.session_id=$2::uuid AND a.work_item_id=$3::uuid AND a.id=$4::uuid
		  AND a.assigned_agent_id=$5::uuid AND m.agent_id=$5::uuid AND a.status IN ('dispatching','running')
		  AND p.catalog_view=$6 AND ($7='' AND p.reviewed_at IS NULL OR p.page_key=$7)
		  AND ($8='' OR a.inbox_task_id=$8::uuid)
		ORDER BY p.through_event_sequence,p.ordinal LIMIT 1
	`, in.WorkspaceID, in.RunID, in.WorkItemID, in.AttemptID, in.AgentID, in.View, in.Cursor, in.InboxTaskID).Scan(
		&page.PageKey, &page.PageHash, &page.Bytes, &page.NextCursor, &page.HasMore,
	)
	if errors.Is(err, pgx.ErrNoRows) {
		return V6CatalogPage{}, ErrRunNotFound
	}
	return page, err
}

func (s *PostgresStore) AcknowledgeV6WorkCatalog(ctx context.Context, in AcknowledgeV6CatalogInput) error {
	tx, err := s.beginResearchTx(ctx, txOpV6CatalogAcknowledge, pgx.TxOptions{})
	if err != nil {
		return err
	}
	defer tx.Rollback(ctx)
	if err = lockRunForMutation(ctx, tx, in.RunID, in.WorkspaceID); err != nil {
		return err
	}
	command, err := tx.Exec(ctx, `
		UPDATE research_work_catalog_page p SET reviewed_at=COALESCE(reviewed_at,now())
		FROM research_work_item_attempt a, research_team_membership m
		WHERE (a.workspace_id,a.session_id,a.id)=(p.workspace_id,p.session_id,p.work_item_attempt_id)
		  AND (m.workspace_id,m.session_id,m.id)=(a.workspace_id,a.session_id,a.membership_id)
		  AND p.workspace_id=$1::uuid AND p.session_id=$2::uuid AND a.work_item_id=$3::uuid AND a.id=$4::uuid
		  AND a.assigned_agent_id=$5::uuid AND m.agent_id=$5::uuid AND p.page_key=$6 AND p.content_hash=$7
		  AND ($8='' OR a.inbox_task_id=$8::uuid)
	`, in.WorkspaceID, in.RunID, in.WorkItemID, in.AttemptID, in.AgentID, in.PageKey, in.PageHash, in.InboxTaskID)
	if err != nil {
		return err
	}
	if command.RowsAffected() != 1 {
		return ErrResultConflict
	}
	if _, err = appendEvent(ctx, tx, in.WorkspaceID, in.RunID, "v6_work_catalog_acknowledged",
		"v6-catalog-ack:"+in.ClientRequestID, "agent", in.AgentID,
		map[string]any{"work_item_attempt_id": in.AttemptID, "page_key": in.PageKey, "page_hash": in.PageHash}); err != nil {
		return err
	}
	return s.commitResearchTx(ctx, txOpV6CatalogAcknowledge, tx)
}
