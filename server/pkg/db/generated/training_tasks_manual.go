// SPDX-License-Identifier: Apache-2.0
//
// Manually authored query helpers (not sqlc-generated) for training segment
// lifecycle management (§T9-T10).

package db

import (
	"context"

	"github.com/jackc/pgx/v5/pgtype"
)

// CountChildTasks returns the number of inbox events whose parent_task_id
// matches the given event ID.  A count > 0 means the task delegated to other
// agents (channel @mention handoff).
func (q *Queries) CountChildTasks(ctx context.Context, taskID pgtype.UUID) (int64, error) {
	var count int64
	err := q.db.QueryRow(ctx,
		`SELECT COUNT(*) FROM agent_inbox_event WHERE parent_task_id = $1`,
		taskID,
	).Scan(&count)
	return count, err
}

// CountActiveTrainingTasks returns the number of non-terminal inbox events
// for the given project whose agent is part of the training dispatch.  A count
// of 0 means all trainable agents are idle (all terminal).
func (q *Queries) CountActiveTrainingTasks(ctx context.Context, projectID pgtype.UUID) (int64, error) {
	var count int64
	err := q.db.QueryRow(ctx,
		`SELECT COUNT(*)
		 FROM agent_inbox_event t
		 JOIN training_dispatch d ON d.project_id = $1
		 WHERE t.agent_id = d.train_agent_id
		   AND t.status NOT IN ('completed','failed','cancelled')`,
		projectID,
	).Scan(&count)
	return count, err
}
