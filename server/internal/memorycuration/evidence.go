package memorycuration

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"
)

type EvidenceDB interface {
	Query(ctx context.Context, sql string, args ...any) (pgx.Rows, error)
}

type EvidenceItem struct {
	Kind      string    `json:"kind"`
	ID        string    `json:"id"`
	Title     string    `json:"title"`
	Snippet   string    `json:"snippet"`
	CreatedAt time.Time `json:"created_at"`
}

func (i EvidenceItem) Reference() string {
	if strings.TrimSpace(i.ID) == "" {
		return i.Kind
	}
	return i.Kind + ":" + i.ID
}

func CollectDBEvidence(ctx context.Context, db EvidenceDB, workspaceID, agentID string, since, until time.Time) ([]EvidenceItem, error) {
	if db == nil || workspaceID == "" || agentID == "" {
		return nil, nil
	}
	start := dateOnly(since)
	end := dateOnly(until).AddDate(0, 0, 1)
	collectors := []func(context.Context, EvidenceDB, string, string, time.Time, time.Time) ([]EvidenceItem, error){
		collectChannelEvidence,
		collectIssueEvidence,
		collectTaskEvidence,
		collectActivityEvidence,
		collectEvolutionEvidence,
	}
	var out []EvidenceItem
	for _, collect := range collectors {
		items, err := collect(ctx, db, workspaceID, agentID, start, end)
		if err != nil {
			return out, err
		}
		out = append(out, items...)
	}
	if len(out) > 80 {
		out = out[:80]
	}
	return out, nil
}

func collectChannelEvidence(ctx context.Context, db EvidenceDB, workspaceID, agentID string, start, end time.Time) ([]EvidenceItem, error) {
	rows, err := db.Query(ctx, `
		SELECT DISTINCT cm.id::text,
		       CASE WHEN c.kind = 'dm' THEN 'dm_message' ELSE 'channel_message' END AS kind,
		       COALESCE(ch.name, c.kind) AS title,
		       cm.content,
		       cm.created_at
		  FROM channel_message cm
		  JOIN conversation c ON c.id = cm.conversation_id
		  JOIN channel ch ON ch.id = cm.channel_id
		  LEFT JOIN conversation_member member ON member.conversation_id = c.id AND member.member_type = 'agent' AND member.member_id = $2
		 WHERE cm.workspace_id = $1
		   AND cm.created_at >= $3 AND cm.created_at < $4
		   AND cm.deleted_at IS NULL
		   AND (
		     (cm.author_type = 'agent' AND cm.author_id = $2)
		     OR (c.kind = 'dm' AND member.member_id IS NOT NULL)
		     OR cm.content ILIKE '%' || (SELECT name FROM agent WHERE id = $2) || '%'
		     OR cm.content ILIKE '%' || (SELECT display_name FROM agent WHERE id = $2) || '%'
		   )
		 ORDER BY cm.created_at ASC
		 LIMIT 30
	`, workspaceID, agentID, start, end)
	if err != nil {
		return nil, fmt.Errorf("collect channel evidence: %w", err)
	}
	defer rows.Close()
	return scanEvidenceRows(rows)
}

func collectIssueEvidence(ctx context.Context, db EvidenceDB, workspaceID, agentID string, start, end time.Time) ([]EvidenceItem, error) {
	rows, err := db.Query(ctx, `
		SELECT id::text, 'issue' AS kind, title, COALESCE(description, ''), updated_at
		  FROM issue
		 WHERE workspace_id = $1
		   AND updated_at >= $3 AND updated_at < $4
		   AND assignee_type = 'agent' AND assignee_id = $2
		UNION ALL
		SELECT c.id::text, 'comment' AS kind, i.title, c.content, c.created_at
		  FROM comment c
		  JOIN issue i ON i.id = c.issue_id
		 WHERE i.workspace_id = $1
		   AND c.created_at >= $3 AND c.created_at < $4
		   AND ((c.author_type = 'agent' AND c.author_id = $2) OR (i.assignee_type = 'agent' AND i.assignee_id = $2))
		 ORDER BY 5 ASC
		 LIMIT 30
	`, workspaceID, agentID, start, end)
	if err != nil {
		return nil, fmt.Errorf("collect issue evidence: %w", err)
	}
	defer rows.Close()
	return scanEvidenceRows(rows)
}

func collectTaskEvidence(ctx context.Context, db EvidenceDB, workspaceID, agentID string, start, end time.Time) ([]EvidenceItem, error) {
	rows, err := db.Query(ctx, `
		SELECT atq.id::text,
		       'task' AS kind,
		       COALESCE(i.title, cs.title, atq.status) AS title,
		       COALESCE(atq.error, atq.result::text, '') AS snippet,
		       COALESCE(atq.completed_at, atq.started_at, atq.dispatched_at, atq.created_at) AS event_at
		  FROM agent_inbox_event atq
		  JOIN agent a ON a.id = atq.agent_id
		  LEFT JOIN issue i ON i.id = atq.issue_id
		  LEFT JOIN chat_session cs ON cs.id = atq.chat_session_id
		 WHERE a.workspace_id = $1
		   AND atq.agent_id = $2
		   AND COALESCE(atq.completed_at, atq.started_at, atq.dispatched_at, atq.created_at) >= $3
		   AND COALESCE(atq.completed_at, atq.started_at, atq.dispatched_at, atq.created_at) < $4
		 ORDER BY event_at ASC
		 LIMIT 25
	`, workspaceID, agentID, start, end)
	if err != nil {
		return nil, fmt.Errorf("collect task evidence: %w", err)
	}
	defer rows.Close()
	return scanEvidenceRows(rows)
}

func collectActivityEvidence(ctx context.Context, db EvidenceDB, workspaceID, agentID string, start, end time.Time) ([]EvidenceItem, error) {
	rows, err := db.Query(ctx, `
		SELECT id::text, 'agent_activity_event' AS kind, event_type AS title, message AS snippet, created_at
		  FROM agent_activity_event
		 WHERE workspace_id = $1
		   AND agent_id = $2
		   AND created_at >= $3 AND created_at < $4
		 ORDER BY created_at ASC
		 LIMIT 25
	`, workspaceID, agentID, start, end)
	if err != nil {
		return nil, fmt.Errorf("collect activity evidence: %w", err)
	}
	defer rows.Close()
	return scanEvidenceRows(rows)
}

func collectEvolutionEvidence(ctx context.Context, db EvidenceDB, workspaceID, agentID string, start, end time.Time) ([]EvidenceItem, error) {
	rows, err := db.Query(ctx, `
		SELECT id::text, 'evolution_submission' AS kind, title, COALESCE(summary, content, review_reason, ''), updated_at
		  FROM evolution_unit_submission
		 WHERE workspace_id = $1
		   AND source_agent_id = $2
		   AND updated_at >= $3 AND updated_at < $4
		 ORDER BY updated_at ASC
		 LIMIT 25
	`, workspaceID, agentID, start, end)
	if err != nil {
		return nil, fmt.Errorf("collect evolution evidence: %w", err)
	}
	defer rows.Close()
	return scanEvidenceRows(rows)
}

func scanEvidenceRows(rows pgx.Rows) ([]EvidenceItem, error) {
	var out []EvidenceItem
	for rows.Next() {
		var item EvidenceItem
		if err := rows.Scan(&item.ID, &item.Kind, &item.Title, &item.Snippet, &item.CreatedAt); err != nil {
			return out, err
		}
		item.Title = truncateForEvidence(item.Title, 96)
		item.Snippet = truncateForEvidence(item.Snippet, 180)
		out = append(out, item)
	}
	return out, rows.Err()
}

func truncateForEvidence(s string, max int) string {
	s = strings.Join(strings.Fields(strings.TrimSpace(s)), " ")
	if len([]rune(s)) <= max {
		return s
	}
	r := []rune(s)
	return string(r[:max]) + "..."
}
