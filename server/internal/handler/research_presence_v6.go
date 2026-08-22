package handler

import (
	"context"
	"time"

	"github.com/jackc/pgx/v5/pgtype"
	"github.com/multica-ai/multica/server/internal/researchrun"
)

// V6 presence captions. Server-owned zh product copy, consistent with the
// generic titles in research_presence.go.
const (
	researchV6PresenceBriefTitle     = "正在研读任务简报"
	researchV6PresenceCatalogTitle   = "正在核对研究目录"
	researchV6PresenceSubmittedTitle = "已提交结果，等待评审"
	researchV6PresenceDoneTitle      = "已完成当前任务"
	researchV6PresenceFailedTitle    = "任务执行失败"
	researchV6PresenceIdleTitle      = "待命中"
	researchV6PresenceStaleReason    = "lease_expired"
)

// buildResearchV6PresenceRoster derives live presence for a V6 run from the
// run-scoped team roster, each member's latest Work Item, and the latest
// agent-authored Run Event caption (progress notes, catalog/brief
// acknowledgements, submissions). V6 team agents are not workspace fleet
// members, so the V5 fleet-based roster never sees them.
func (h *Handler) buildResearchV6PresenceRoster(
	ctx context.Context, workspaceID, sessionID pgtype.UUID, now time.Time,
) (map[string]ResearchPresenceEntry, error) {
	rows, err := h.DB.Query(ctx, `
		SELECT m.agent_id::text, ag.name, COALESCE(ag.avatar_url,''),
		       EXISTS (
		         SELECT 1 FROM research_director_assignment d
		         WHERE d.workspace_id=m.workspace_id AND d.session_id=m.session_id
		           AND d.director_agent_id=m.agent_id AND d.status='active'
		       ) AS is_director,
		       COALESCE(w.id::text,''), COALESCE(w.status,''),
		       w.started_at, w.updated_at, w.lease_expires_at
		FROM research_team_membership m
		JOIN agent ag ON ag.id=m.agent_id
		LEFT JOIN LATERAL (
			SELECT wi.id, wi.status, wi.started_at, wi.updated_at, wi.lease_expires_at
			FROM research_work_item wi
			WHERE wi.workspace_id=m.workspace_id AND wi.session_id=m.session_id
			  AND wi.assigned_agent_id=m.agent_id
			ORDER BY wi.updated_at DESC LIMIT 1
		) w ON true
		WHERE m.workspace_id=$1 AND m.session_id=$2
		  AND m.state NOT IN ('archived','failed')
	`, workspaceID, sessionID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	type v6RosterRow struct {
		agentID, name, avatarURL     string
		isDirector                   bool
		workItemID, workStatus       string
		startedAt, updatedAt, leaseA pgtype.Timestamptz
	}
	roster := []v6RosterRow{}
	for rows.Next() {
		var row v6RosterRow
		if err := rows.Scan(&row.agentID, &row.name, &row.avatarURL, &row.isDirector,
			&row.workItemID, &row.workStatus, &row.startedAt, &row.updatedAt, &row.leaseA); err != nil {
			return nil, err
		}
		roster = append(roster, row)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	if len(roster) == 0 {
		return map[string]ResearchPresenceEntry{}, nil
	}

	captions, err := h.loadResearchV6PresenceCaptions(ctx, sessionID)
	if err != nil {
		return nil, err
	}

	out := make(map[string]ResearchPresenceEntry, len(roster))
	for _, row := range roster {
		entry := ResearchPresenceEntry{
			Name:      row.name,
			AvatarURL: row.avatarURL,
			Role:      "member",
			Phase:     ResearchPresencePhaseIdle,
			Activity:  researchV6PresenceIdleTitle,
		}
		if row.isDirector {
			entry.Role = "lead"
		}
		switch row.workStatus {
		case "ready":
			entry.Phase = ResearchPresencePhaseQueued
			entry.Activity = researchPresenceGenericDispatchTitle
		case "running":
			entry.Phase = ResearchPresencePhaseRunning
			entry.Activity = researchPresenceGenericStartedTitle
		case "succeeded":
			entry.Phase = ResearchPresencePhaseDone
			entry.Activity = researchV6PresenceDoneTitle
		case "failed", "cancelled":
			entry.Phase = ResearchPresencePhaseFailed
			entry.Activity = researchV6PresenceFailedTitle
		}
		if row.workItemID != "" {
			id := row.workItemID
			entry.TaskID = &id
		}
		if row.updatedAt.Valid {
			entry.UpdatedAt = row.updatedAt.Time.UnixMilli()
		}
		if row.leaseA.Valid {
			expires := row.leaseA.Time.UnixMilli()
			entry.ExpiresAt = &expires
		}

		caption, hasCaption := captions[row.agentID]
		if hasCaption && caption.updatedAt > entry.UpdatedAt {
			entry.UpdatedAt = caption.updatedAt
		}
		// A live caption only narrates in-flight phases; terminal states keep
		// their own titles.
		if hasCaption && (entry.Phase == ResearchPresencePhaseRunning || entry.Phase == ResearchPresencePhaseQueued) {
			entry.Activity = caption.text
		}

		// An expired lease on an in-flight Work Item means the server no
		// longer trusts this attempt — surface it instead of a fake "running".
		if (entry.Phase == ResearchPresencePhaseRunning || entry.Phase == ResearchPresencePhaseQueued) &&
			row.leaseA.Valid && row.leaseA.Time.Before(now) {
			entry.Phase = ResearchPresencePhaseStale
			reason := researchV6PresenceStaleReason
			entry.StaleReason = &reason
		}
		out[row.agentID] = entry
	}
	return out, nil
}

type researchV6PresenceCaption struct {
	text      string
	updatedAt int64
}

// loadResearchV6PresenceCaptions returns the newest agent-authored caption per
// agent: a progress note's own text, or a canned title for protocol
// milestones (brief/catalog acknowledgements, submission received).
func (h *Handler) loadResearchV6PresenceCaptions(
	ctx context.Context, sessionID pgtype.UUID,
) (map[string]researchV6PresenceCaption, error) {
	rows, err := h.DB.Query(ctx, `
		SELECT DISTINCT ON (e.actor_id)
		       e.actor_id::text, e.event_type, COALESCE(e.payload->>'text',''), e.created_at
		FROM research_run_event e
		WHERE e.session_id=$1 AND e.actor_type='agent' AND e.actor_id IS NOT NULL
		  AND e.event_type IN (
		    'v6_work_progress_reported','v6_work_catalog_acknowledged',
		    'v6_director_brief_page_acknowledged','v6_work_submission_received'
		  )
		ORDER BY e.actor_id, e.sequence DESC
	`, sessionID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	out := map[string]researchV6PresenceCaption{}
	for rows.Next() {
		var agentID, eventType, text string
		var createdAt pgtype.Timestamptz
		if err := rows.Scan(&agentID, &eventType, &text, &createdAt); err != nil {
			return nil, err
		}
		caption := researchV6PresenceCaption{}
		switch eventType {
		case researchrun.V6WorkProgressEventType:
			caption.text = text
		case "v6_director_brief_page_acknowledged":
			caption.text = researchV6PresenceBriefTitle
		case "v6_work_catalog_acknowledged":
			caption.text = researchV6PresenceCatalogTitle
		case "v6_work_submission_received":
			caption.text = researchV6PresenceSubmittedTitle
		}
		if caption.text == "" {
			continue
		}
		if createdAt.Valid {
			caption.updatedAt = createdAt.Time.UnixMilli()
		}
		out[agentID] = caption
	}
	return out, rows.Err()
}
