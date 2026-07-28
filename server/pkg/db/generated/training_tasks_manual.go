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
// taking part in the given project's rollout.  A count of 0 means all agents
// on the rollout are idle (all terminal).
//
// Membership is resolved through interaction_dag_session_run, which binds each
// RL session to {project_id, agent_run_id = agent_inbox_event.id}. Matching on
// training_dispatch.train_agent_id instead would never hit: rollout tasks run
// under per-project derived agents, and agent_inbox_event carries no project
// column to scope them by.
//
// Terminal is acked/suppressed (see service.isTerminalAgentTaskStatus); the
// status enum is pending/draining/acked/failed/suppressed, so treating
// completed/cancelled as terminal counted every finished task as active and
// the idle sweep never fired.
func (q *Queries) CountActiveTrainingTasks(ctx context.Context, projectID pgtype.UUID) (int64, error) {
	var count int64
	err := q.db.QueryRow(ctx,
		`SELECT COUNT(*)
		 FROM interaction_dag_session_run r
		 JOIN agent_inbox_event t ON t.id = r.agent_run_id::uuid
		 WHERE r.project_id = $1::text
		   AND t.status NOT IN ('acked','suppressed')`,
		projectID,
	).Scan(&count)
	return count, err
}
