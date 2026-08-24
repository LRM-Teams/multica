package researchrun

import (
	"context"
	"time"
)

const workActivityTimelineLimit = 100

type WorkActivityTimelineRow struct {
	ID           string    `json:"id"`
	OccurredAt   time.Time `json:"occurred_at"`
	Title        string    `json:"title"`
	Subtext      string    `json:"subtext,omitempty"`
	ActivityKind string    `json:"activity_kind"`
	DetailKind   string    `json:"detail_kind"`
	BodyKind     string    `json:"body_kind"`
	Body         string    `json:"body,omitempty"`
}

type WorkActivity struct {
	WorkItemID      string                    `json:"work_item_id"`
	AttemptID       string                    `json:"attempt_id"`
	AgentID         string                    `json:"agent_id"`
	AgentName       string                    `json:"agent_name"`
	InboxTaskID     string                    `json:"inbox_task_id"`
	Mission         string                    `json:"mission"`
	Status          string                    `json:"status"`
	Progress        string                    `json:"progress"`
	ProgressStep    int32                     `json:"progress_step"`
	ProgressTotal   int32                     `json:"progress_total"`
	StartedAt       *time.Time                `json:"started_at,omitempty"`
	CompletedAt     *time.Time                `json:"completed_at,omitempty"`
	UpdatedAt       time.Time                 `json:"updated_at"`
	Timeline        []WorkActivityTimelineRow `json:"timeline"`
	TimelineHasMore bool                      `json:"timeline_has_more"`
}

type WorkActivityReader interface {
	ProjectionWorkActivity(context.Context, string, string, string) (WorkActivity, error)
}

func (s *PostgresStore) ProjectionWorkActivity(ctx context.Context, workspaceID, runID, workItemID string) (WorkActivity, error) {
	var activity WorkActivity
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
	activity.Timeline = make([]WorkActivityTimelineRow, 0)
	if activity.AgentID == "" || activity.StartedAt == nil {
		return activity, nil
	}

	rows, err := s.pool.Query(ctx, `
		SELECT entry.id::text, entry.activity_kind, entry.detail_kind, entry.title, entry.subtext,
		       entry.body_kind, entry.body, entry.observed_at
		FROM agent_activity_entry entry
		WHERE entry.workspace_id=$1::uuid
		  AND entry.agent_id=$2::uuid
		  AND entry.observed_at >= $3::timestamptz
		  AND ($4::timestamptz IS NULL OR entry.observed_at <= $4::timestamptz)
		ORDER BY entry.observed_at DESC, entry.id DESC
		LIMIT $5`,
		workspaceID,
		activity.AgentID,
		activity.StartedAt,
		activity.CompletedAt,
		workActivityTimelineLimit+1,
	)
	if err != nil {
		// Runner Activity is presentation-only. Keep the Work mission and
		// canonical status available when its auxiliary timeline is unavailable.
		return activity, nil
	}
	defer rows.Close()
	for rows.Next() {
		var row WorkActivityTimelineRow
		if err := rows.Scan(&row.ID, &row.ActivityKind, &row.DetailKind, &row.Title, &row.Subtext, &row.BodyKind, &row.Body, &row.OccurredAt); err != nil {
			activity.Timeline = activity.Timeline[:0]
			return activity, nil
		}
		if len(activity.Timeline) == workActivityTimelineLimit {
			activity.TimelineHasMore = true
			continue
		}
		activity.Timeline = append(activity.Timeline, row)
	}
	if err := rows.Err(); err != nil {
		activity.Timeline = activity.Timeline[:0]
		activity.TimelineHasMore = false
		return activity, nil
	}
	return activity, nil
}
