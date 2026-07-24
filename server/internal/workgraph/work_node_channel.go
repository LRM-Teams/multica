package workgraph

import (
	"context"
	"fmt"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"
)

// SetPrimaryChannel records the group where a work node is discussed.
func (s *Store) SetPrimaryChannel(ctx context.Context, nodeID, channelID pgtype.UUID) error {
	tag, err := s.pool.Exec(ctx, `
		UPDATE work_node
		SET primary_channel_id = $2, updated_at = now()
		WHERE id = $1
	`, nodeID, channelID)
	if err != nil {
		return fmt.Errorf("set work node primary channel: %w", err)
	}
	if tag.RowsAffected() == 0 {
		return pgx.ErrNoRows
	}
	return nil
}
