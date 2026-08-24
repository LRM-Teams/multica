package researchrun

import (
	"context"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/multica-ai/multica/server/internal/activityprojection"
	"github.com/multica-ai/multica/server/pkg/protocol"
)

const v6WorkActivityTimelineLimit = 100

type V6WorkActivityTimelineRow struct {
	ID         string    `json:"id"`
	OccurredAt time.Time `json:"occurred_at"`
	activityprojection.TimelineRow
}

type V6WorkActivity struct {
	WorkItemID      string                      `json:"work_item_id"`
	AttemptID       string                      `json:"attempt_id"`
	AgentID         string                      `json:"agent_id"`
	AgentName       string                      `json:"agent_name"`
	InboxTaskID     string                      `json:"inbox_task_id"`
	Mission         string                      `json:"mission"`
	Status          string                      `json:"status"`
	Progress        string                      `json:"progress"`
	ProgressStep    int32                       `json:"progress_step"`
	ProgressTotal   int32                       `json:"progress_total"`
	StartedAt       *time.Time                  `json:"started_at,omitempty"`
	CompletedAt     *time.Time                  `json:"completed_at,omitempty"`
	UpdatedAt       time.Time                   `json:"updated_at"`
	Timeline        []V6WorkActivityTimelineRow `json:"timeline"`
	TimelineHasMore bool                        `json:"timeline_has_more"`
}

type V6WorkActivityReader interface {
	ProjectionV6WorkActivity(context.Context, string, string, string) (V6WorkActivity, error)
}

type V6WorkActivityWriter interface {
	RecordV6WorkActivity(context.Context, string, string, []protocol.TaskMessagePayload) error
}

func (s *PostgresStore) RecordV6WorkActivity(ctx context.Context, workspaceID, inboxTaskID string, messages []protocol.TaskMessagePayload) error {
	if len(messages) == 0 {
		return nil
	}
	tx, err := s.beginResearchTx(ctx, txOpV6WorkActivityRecord, pgx.TxOptions{})
	if err != nil {
		return fmt.Errorf("begin V6 work activity transaction: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()
	for _, message := range messages {
		if message.TaskID != "" && message.TaskID != inboxTaskID {
			return fmt.Errorf("task message %d belongs to inbox task %q, want %q", message.Seq, message.TaskID, inboxTaskID)
		}
		row, visible := activityprojection.ProjectTaskMessage(message)
		if !visible {
			continue
		}
		observedAt := time.Now().UTC()
		if message.CreatedAt != "" {
			parsed, parseErr := time.Parse(time.RFC3339Nano, message.CreatedAt)
			if parseErr != nil {
				return fmt.Errorf("parse task message %d created_at: %w", message.Seq, parseErr)
			}
			observedAt = parsed.UTC()
		}
		tag, err := tx.Exec(ctx, `
			INSERT INTO research_work_item_activity_entry (
				id, workspace_id, session_id, work_item_id, work_item_attempt_id,
				inbox_task_id, message_sequence, title, subtext, tone, body_kind,
				body, observed_at, received_at
			)
			SELECT gen_random_uuid(), attempt.workspace_id, attempt.session_id,
			       attempt.work_item_id, attempt.id, attempt.inbox_task_id,
			       $3, $4, $5, $6, $7, $8, $9, now()
			FROM research_work_item_attempt attempt
			WHERE attempt.workspace_id=$1::uuid
			  AND attempt.inbox_task_id=$2::uuid
			ON CONFLICT (work_item_attempt_id, message_sequence) DO NOTHING`,
			workspaceID, inboxTaskID, message.Seq, row.Title, row.Subtext,
			row.Tone, row.BodyKind, row.Body, observedAt,
		)
		if err != nil {
			return fmt.Errorf("persist V6 work activity message %d: %w", message.Seq, err)
		}
		if tag.RowsAffected() == 0 {
			var persisted activityprojection.TimelineRow
			if err := tx.QueryRow(ctx, `
				SELECT entry.title,entry.subtext,entry.tone,entry.body_kind,entry.body
				FROM research_work_item_activity_entry entry
				JOIN research_work_item_attempt attempt ON attempt.id=entry.work_item_attempt_id
				WHERE attempt.workspace_id=$1::uuid
				  AND attempt.inbox_task_id=$2::uuid
				  AND entry.message_sequence=$3`, workspaceID, inboxTaskID, message.Seq).Scan(
				&persisted.Title,
				&persisted.Subtext,
				&persisted.Tone,
				&persisted.BodyKind,
				&persisted.Body,
			); err != nil {
				return fmt.Errorf("verify V6 work activity message %d replay: %w", message.Seq, err)
			}
			if persisted != row {
				return fmt.Errorf("V6 work activity message %d replay changed presentation fields", message.Seq)
			}
		}
	}
	if err := s.commitResearchTx(ctx, txOpV6WorkActivityRecord, tx); err != nil {
		return fmt.Errorf("commit V6 work activity transaction: %w", err)
	}
	return nil
}

func (s *PostgresStore) ProjectionV6WorkActivity(ctx context.Context, workspaceID, runID, workItemID string) (V6WorkActivity, error) {
	var activity V6WorkActivity
	err := s.pool.QueryRow(ctx, `
		SELECT work.id::text,
		       COALESCE(attempt.id::text,''),
		       COALESCE(attempt.assigned_agent_id::text,''),
		       COALESCE(NULLIF(agent.display_name,''),agent.name,''),
		       COALESCE(attempt.inbox_task_id::text,''),
		       COALESCE(NULLIF(work.payload->>'mission_prompt',''),NULLIF(work.reason,''),NULLIF(membership.mission_prompt,''),''),
		       work.status,
		       COALESCE(NULLIF(v6_progress.summary,''),progress.summary,''),
		       COALESCE(progress.step,0),
		       COALESCE(progress.total,0),
		       inbox.started_at,
		       inbox.completed_at,
		       GREATEST(
		         work.updated_at,
		         COALESCE(attempt.updated_at, work.updated_at),
		         COALESCE(inbox.updated_at, work.updated_at),
		         COALESCE(inbox.started_at, work.updated_at),
		         COALESCE(inbox.completed_at, work.updated_at),
		         COALESCE(progress.updated_at, work.updated_at),
		         COALESCE(v6_progress.updated_at, work.updated_at)
		       )
		FROM research_work_item work
		LEFT JOIN LATERAL (
			SELECT candidate.*
			FROM research_work_item_attempt candidate
			WHERE candidate.workspace_id=work.workspace_id
			  AND candidate.session_id=work.session_id
			  AND candidate.work_item_id=work.id
			ORDER BY candidate.attempt_number DESC
			LIMIT 1
		) attempt ON true
		LEFT JOIN agent ON agent.id=attempt.assigned_agent_id
		LEFT JOIN research_team_membership membership
		  ON membership.workspace_id=attempt.workspace_id
		 AND membership.session_id=attempt.session_id
		 AND membership.id=attempt.membership_id
		LEFT JOIN agent_inbox_event inbox
		  ON inbox.id=attempt.inbox_task_id
		 AND inbox.agent_id=attempt.assigned_agent_id
		LEFT JOIN agent_task_progress_snapshot progress ON progress.task_id=attempt.inbox_task_id
		LEFT JOIN LATERAL (
			SELECT COALESCE(event.payload->>'text','') summary,event.created_at updated_at
			FROM research_run_event event
			WHERE event.workspace_id=work.workspace_id
			  AND event.session_id=work.session_id
			  AND event.event_type='v6_work_progress_reported'
			  AND event.payload->>'work_item_id'=work.id::text
			  AND event.payload->>'attempt_id'=attempt.id::text
			ORDER BY event.sequence DESC
			LIMIT 1
		) v6_progress ON true
		WHERE work.workspace_id=$1::uuid AND work.session_id=$2::uuid AND work.id=$3::uuid`,
		workspaceID, runID, workItemID,
	).Scan(
		&activity.WorkItemID,
		&activity.AttemptID,
		&activity.AgentID,
		&activity.AgentName,
		&activity.InboxTaskID,
		&activity.Mission,
		&activity.Status,
		&activity.Progress,
		&activity.ProgressStep,
		&activity.ProgressTotal,
		&activity.StartedAt,
		&activity.CompletedAt,
		&activity.UpdatedAt,
	)
	if err != nil {
		return activity, err
	}
	activity.Timeline = make([]V6WorkActivityTimelineRow, 0)
	if activity.AttemptID == "" || activity.InboxTaskID == "" {
		return activity, nil
	}

	rows, err := s.pool.Query(ctx, `
		SELECT entry.id::text, entry.observed_at, entry.title, entry.subtext,
		       entry.tone, entry.body_kind, entry.body
		FROM research_work_item_activity_entry entry
		WHERE entry.workspace_id=$1::uuid
		  AND entry.session_id=$2::uuid
		  AND entry.work_item_attempt_id=$3::uuid
		  AND entry.inbox_task_id=$4::uuid
		ORDER BY entry.message_sequence DESC, entry.id DESC
		LIMIT $5`,
		workspaceID,
		runID,
		activity.AttemptID,
		activity.InboxTaskID,
		v6WorkActivityTimelineLimit+1,
	)
	if err != nil {
		return V6WorkActivity{}, err
	}
	defer rows.Close()
	for rows.Next() {
		var row V6WorkActivityTimelineRow
		if err := rows.Scan(
			&row.ID,
			&row.OccurredAt,
			&row.Title,
			&row.Subtext,
			&row.Tone,
			&row.BodyKind,
			&row.Body,
		); err != nil {
			return V6WorkActivity{}, err
		}
		if len(activity.Timeline) == v6WorkActivityTimelineLimit {
			activity.TimelineHasMore = true
			continue
		}
		activity.Timeline = append(activity.Timeline, row)
	}
	if err := rows.Err(); err != nil {
		return V6WorkActivity{}, err
	}
	return activity, nil
}
