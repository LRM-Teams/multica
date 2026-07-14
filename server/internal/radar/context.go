package radar

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/jackc/pgx/v5"
)

const (
	fileSectionMaxBytes     = 2 * 1024
	requiredSectionMaxBytes = 6 * 1024
	optionalSectionMaxBytes = 2 * 1024
	contextMaxBytes         = 48 * 1024
	changedSectionMaxBytes  = 22 * 1024
	changedSectionPageSize  = 20
	changedEntryMaxBytes    = 1000

	// Rotating pages are deliberately smaller than requiredSectionMaxBytes even
	// at the per-entry safety cap. This means appendRowsWithinBudget never drops
	// an item from a selected page; rotation, rather than byte truncation, owns
	// coverage of large workspaces.
	agentSectionPageSize        = 6
	agentSectionEntryMaxBytes   = 760
	issueSectionPageSize        = 4
	issueSectionEntryMaxBytes   = 1200
	taskSectionPageSize         = 4
	taskSectionEntryMaxBytes    = 1250
	channelSectionPageSize      = 3
	channelSectionEntryMaxBytes = 1400
	messageSectionPageSize      = 5
	messageSectionEntryMaxBytes = 900
)

type DBTX interface {
	Query(ctx context.Context, sql string, args ...any) (pgx.Rows, error)
}

type ContextBuilder struct {
	db             DBTX
	workspacesRoot string
}

type Context struct {
	Markdown string
	Scan     WorkspaceScanMetadata
}

type WorkspaceScanMetadata struct {
	ObservedAt                 time.Time
	ObservedChangeVersion      int64
	ChangeCursorThroughVersion int64
	ChangesHasMore             bool
	StaticNextCursors          map[string]string
	StaticWrappedSections      map[string]bool
}

func NewContextBuilder(db DBTX, workspacesRoot string) *ContextBuilder {
	return &ContextBuilder{db: db, workspacesRoot: workspacesRoot}
}

func (b *ContextBuilder) Build(ctx context.Context, workspaceID, agentID string) (Context, error) {
	return b.BuildAt(ctx, workspaceID, agentID, time.Now().UTC())
}

func (b *ContextBuilder) BuildAt(ctx context.Context, workspaceID, agentID string, observedAt time.Time) (Context, error) {
	var out strings.Builder
	out.WriteString("# Wendy Workspace Supervision Context\n\n")
	fmt.Fprintf(&out, "observed_at_utc=%s\n\n", observedAt.UTC().Format(time.RFC3339Nano))
	scan := WorkspaceScanMetadata{
		ObservedAt:            observedAt.UTC(),
		StaticNextCursors:     make(map[string]string),
		StaticWrappedSections: make(map[string]bool),
	}
	if b.db != nil {
		if _, err := querySingleInt64(ctx, b.db, `SELECT refresh_workspace_radar_time_signals($1::uuid, $2::timestamptz)`, workspaceID, observedAt); err != nil {
			return Context{}, err
		}
		state, err := b.loadWorkspaceScanState(ctx, workspaceID)
		if err != nil {
			return Context{}, err
		}
		scan.ObservedChangeVersion = state.changeVersion
		scan.ChangeCursorThroughVersion, scan.ChangesHasMore, err = b.appendPriorityChanges(ctx, &out, workspaceID, state.changeCursor, state.changeVersion)
		if err != nil {
			return Context{}, err
		}
		if err := b.appendDBSections(ctx, &out, workspaceID, agentID, state.staticCursors, &scan); err != nil {
			return Context{}, err
		}
	}
	if b.workspacesRoot != "" {
		root := filepath.Join(b.workspacesRoot, workspaceID, ".multica", "agents", agentID)
		for _, section := range []struct{ title, path string }{
			{"Agent Plan", "notes/agent-plan.md"},
			{"Memory", "memory/MEMORY.md"},
			{"State", "memory/STATE.md"},
			{"Work Log", "notes/work-log.md"},
		} {
			appendFileSectionWithinContext(&out, root, section.title, section.path)
		}
	}
	return Context{Markdown: truncateContext(out.String(), contextMaxBytes), Scan: scan}, nil
}

func appendFileSectionWithinContext(out *strings.Builder, root, title, rel string) {
	if out.Len() >= contextMaxBytes-256 {
		return
	}
	var section strings.Builder
	appendFileSection(&section, root, title, rel)
	if section.Len() == 0 {
		return
	}
	remaining := contextMaxBytes - out.Len()
	out.WriteString(truncateUTF8(section.String(), remaining))
}

func appendFileSection(out *strings.Builder, root, title, rel string) {
	data, err := os.ReadFile(filepath.Join(root, filepath.FromSlash(rel)))
	if err != nil {
		return
	}
	fmt.Fprintf(out, "## %s\n\n", title)
	out.WriteString(strings.TrimSpace(truncateUTF8(string(data), fileSectionMaxBytes)))
	out.WriteString("\n\n")
}

type workspaceScanState struct {
	changeVersion int64
	changeCursor  int64
	staticCursors map[string]string
}

func (b *ContextBuilder) loadWorkspaceScanState(ctx context.Context, workspaceID string) (workspaceScanState, error) {
	rows, err := b.db.Query(ctx, `
		SELECT change_version, change_cursor_version, static_scan_cursors::text
		FROM workspace_radar_state
		WHERE workspace_id = $1::uuid
	`, workspaceID)
	if err != nil {
		return workspaceScanState{}, err
	}
	defer rows.Close()
	if !rows.Next() {
		return workspaceScanState{staticCursors: make(map[string]string)}, nil
	}
	var state workspaceScanState
	var raw string
	if err := rows.Scan(&state.changeVersion, &state.changeCursor, &raw); err != nil {
		return workspaceScanState{}, err
	}
	state.staticCursors = make(map[string]string)
	if err := json.Unmarshal([]byte(raw), &state.staticCursors); err != nil {
		return workspaceScanState{}, fmt.Errorf("decode static Radar cursors: %w", err)
	}
	return state, rows.Err()
}

func (b *ContextBuilder) appendPriorityChanges(ctx context.Context, out *strings.Builder, workspaceID string, cursor, observed int64) (int64, bool, error) {
	rows, err := b.db.Query(ctx, `
		SELECT change_version,
		  'change_version=' || change_version::text
		  || ' occurred_at=' || occurred_at::text
		  || ' kind=' || entity_kind
		  || ' entity_id=' || entity_id::text
		  || ' target_kind=' || target_kind
		  || ' target_id=' || COALESCE(target_id::text, 'none')
		  || ' details=' || payload::text
		FROM workspace_radar_change
		WHERE workspace_id = $1::uuid
		  AND change_version > $2
		  AND change_version <= $3
		ORDER BY change_version ASC
		LIMIT $4
	`, workspaceID, cursor, observed, changedSectionPageSize+1)
	if err != nil {
		return cursor, false, err
	}
	defer rows.Close()
	type changeLine struct {
		version int64
		line    string
	}
	changes := make([]changeLine, 0, changedSectionPageSize+1)
	for rows.Next() {
		var item changeLine
		if err := rows.Scan(&item.version, &item.line); err != nil {
			return cursor, false, err
		}
		changes = append(changes, item)
	}
	if err := rows.Err(); err != nil {
		return cursor, false, err
	}
	hasMore := len(changes) > changedSectionPageSize
	if hasMore {
		changes = changes[:changedSectionPageSize]
	}
	through := observed
	if len(changes) > 0 {
		through = changes[len(changes)-1].version
	}
	lines := []string{fmt.Sprintf(
		"cursor_before=%d observed_change_version=%d cursor_through=%d has_more=%t",
		cursor, observed, through, hasMore,
	)}
	for _, item := range changes {
		lines = append(lines, truncateUTF8(strings.TrimSpace(item.line), changedEntryMaxBytes))
	}
	if err := appendPreparedRowsWithinBudget(out, "Priority Workspace Changes", changedSectionMaxBytes, true, lines); err != nil {
		return cursor, false, err
	}
	return through, hasMore, nil
}

func (b *ContextBuilder) appendDBSections(ctx context.Context, out *strings.Builder, workspaceID, agentID string, cursors map[string]string, scan *WorkspaceScanMetadata) error {
	// Static unchanged details keep their existing low-frequency rotation. The
	// durable change journal above is the authoritative lossless path.
	scanSequence, err := querySingleInt64(ctx, b.db, `
		SELECT count(*)::bigint
		FROM agent_radar_run run
		WHERE run.workspace_id = $1::uuid
		  AND run.trigger_kind = 'scheduled'
		  AND run.cooldown_key = 'workspace_supervisor_radar'
		  AND run.status IN ('succeeded', 'no_action')
	`, workspaceID)
	if err != nil {
		return err
	}
	_ = cursors
	maxPages, err := querySingleInt64(ctx, b.db, `
		SELECT GREATEST(
		  1,
		  CEIL((SELECT count(*) FROM agent WHERE workspace_id = $1::uuid AND archived_at IS NULL)::numeric / $2)::bigint,
		  CEIL((SELECT count(*) FROM issue WHERE workspace_id = $1::uuid AND status NOT IN ('done', 'cancelled'))::numeric / $3)::bigint,
		  CEIL((SELECT count(*) FROM agent_task_queue task JOIN agent a ON a.id = task.agent_id
		        WHERE a.workspace_id = $1::uuid AND task.chat_session_id IS NULL
		          AND COALESCE(task.context->>'type', '') <> 'agent_radar'
		          AND (task.status IN ('queued', 'dispatched', 'running', 'waiting_local_directory')
		               OR (task.status IN ('completed', 'cancelled', 'failed') AND task.completed_at > now() - interval '24 hours')))::numeric / $4)::bigint,
		  CEIL((SELECT count(*) FROM channel WHERE workspace_id = $1::uuid AND kind = 'group' AND archived_at IS NULL)::numeric / $5)::bigint,
		  CEIL((SELECT count(*)
		        FROM channel ch
		        CROSS JOIN LATERAL (
		          SELECT 1 FROM channel_message message
		          WHERE message.channel_id = ch.id
		            AND message.workspace_id = ch.workspace_id
		            AND message.deleted_at IS NULL
		          ORDER BY message.seq DESC LIMIT 5
		        ) recent_message
		        WHERE ch.workspace_id = $1::uuid AND ch.kind = 'group' AND ch.archived_at IS NULL)::numeric / $6)::bigint
		)
	`, workspaceID, agentSectionPageSize, issueSectionPageSize, taskSectionPageSize, channelSectionPageSize, messageSectionPageSize)
	if err != nil {
		return err
	}
	scan.StaticWrappedSections["all"] = (scanSequence+1)%maxPages == 0
	if err := appendRotatingRequiredRows(ctx, out, b.db, rotatingSection{
		title:         "Workspace Agents",
		pageSize:      agentSectionPageSize,
		entryMaxBytes: agentSectionEntryMaxBytes,
		countArgCount: 1,
		countQuery: `
			SELECT count(*)::bigint
			FROM agent a
			WHERE a.workspace_id = $1::uuid
			  AND a.archived_at IS NULL
		`,
		pageQuery: `
			SELECT
			  'agent_id=' || a.id::text
			  || ' name=' || left(regexp_replace(COALESCE(NULLIF(a.display_name, ''), a.name), E'[\\n\\r]+', ' ', 'g'), 60)
			  || ' status=' || a.status
			  || ' runtime_status=' || COALESCE(runtime.status, 'unconfigured')
			  || ' capabilities=' || left(regexp_replace(COALESCE(NULLIF(a.description, ''), 'not described'), E'[\\n\\r]+', ' ', 'g'), 100)
			  || ' active_tasks=' || (
			    SELECT count(*)::text
		    FROM agent_task_queue task
			    WHERE task.agent_id = a.id
		      AND task.chat_session_id IS NULL
		      AND task.status IN ('queued', 'dispatched', 'running', 'waiting_local_directory')
		      AND COALESCE(task.context->>'type', '') <> 'agent_radar'
		  )
		FROM agent a
		LEFT JOIN agent_runtime runtime
		  ON runtime.id = a.runtime_id
		 AND runtime.workspace_id = a.workspace_id
			WHERE a.workspace_id = $1::uuid
			  AND a.archived_at IS NULL
			ORDER BY (a.id = $2::uuid) DESC, a.created_at ASC, a.id ASC
			LIMIT %d OFFSET %d
		`,
	}, scanSequence, workspaceID, agentID); err != nil {
		return err
	}
	if err := appendRotatingRequiredRows(ctx, out, b.db, rotatingSection{
		title:         "Open Issues",
		pageSize:      issueSectionPageSize,
		entryMaxBytes: issueSectionEntryMaxBytes,
		countQuery: `
			SELECT count(*)::bigint
			FROM issue
			WHERE workspace_id = $1::uuid
			  AND status NOT IN ('done', 'cancelled')
		`,
		pageQuery: `
			SELECT
			  left(workspace.issue_prefix, 12) || '-' || issue.number::text
			  || ' ' || left(regexp_replace(issue.title, E'[\\n\\r]+', ' ', 'g'), 80)
			  || ' [' || issue.status || ']'
			  || ' issue_id=' || issue.id::text
			  || ' priority=' || issue.priority
			  || ' description=' || left(regexp_replace(COALESCE(issue.description, ''), E'[\\n\\r]+', ' ', 'g'), 100)
		  || ' assignee_type=' || COALESCE(issue.assignee_type, 'none')
		  || ' assignee_id=' || COALESCE(issue.assignee_id::text, 'none')
		  || ' due=' || COALESCE(issue.due_date::text, 'none')
		  || ' updated_at=' || issue.updated_at::text
		  || COALESCE(' latest_comment_at=' || latest_comment.created_at::text || ' latest_comment=' || latest_comment.summary, '')
		FROM issue
		JOIN workspace ON workspace.id = issue.workspace_id
		LEFT JOIN LATERAL (
			  SELECT comment.created_at,
			    comment.author_type || ':' || comment.author_id::text || ' '
			    || left(regexp_replace(comment.content, E'[\\n\\r]+', ' ', 'g'), 80) AS summary
		  FROM comment
		  WHERE comment.issue_id = issue.id
		  ORDER BY comment.created_at DESC, comment.id DESC
		  LIMIT 1
		) latest_comment ON true
		WHERE issue.workspace_id = $1::uuid
		  AND issue.status NOT IN ('done', 'cancelled')
		ORDER BY
		  CASE issue.priority
		    WHEN 'urgent' THEN 0 WHEN 'high' THEN 1 WHEN 'medium' THEN 2
		    WHEN 'low' THEN 3 ELSE 4
		  END,
			  issue.due_date ASC NULLS LAST,
			  issue.updated_at DESC,
			  issue.id ASC
			LIMIT %d OFFSET %d
		`,
	}, scanSequence, workspaceID); err != nil {
		return err
	}
	if err := appendRotatingRequiredRows(ctx, out, b.db, rotatingSection{
		title:         "Active Tasks",
		pageSize:      taskSectionPageSize,
		entryMaxBytes: taskSectionEntryMaxBytes,
		countQuery: `
			SELECT count(*)::bigint
			FROM agent_task_queue task
			JOIN agent task_agent ON task_agent.id = task.agent_id
			WHERE task_agent.workspace_id = $1::uuid
			  AND task.chat_session_id IS NULL
			  AND COALESCE(task.context->>'type', '') <> 'agent_radar'
			  AND (
			    task.status IN ('queued', 'dispatched', 'running', 'waiting_local_directory')
			    OR (
			      task.status IN ('completed', 'cancelled', 'failed')
			      AND task.completed_at > now() - interval '24 hours'
			    )
			  )
		`,
		pageQuery: `
			SELECT
			  'task_id=' || task.id::text
			  || ' agent_id=' || task.agent_id::text
			  || ' agent=' || left(regexp_replace(COALESCE(NULLIF(task_agent.display_name, ''), task_agent.name), E'[\\n\\r]+', ' ', 'g'), 30)
		  || ' issue_id=' || COALESCE(task.issue_id::text, 'none')
		  || ' status=' || task.status
		  || ' created_at=' || task.created_at::text
		  || ' started_at=' || COALESCE(task.started_at::text, 'none')
		  || ' completed_at=' || COALESCE(task.completed_at::text, 'none')
			  || ' wait_reason=' || left(regexp_replace(COALESCE(task.wait_reason, ''), E'[\\n\\r]+', ' ', 'g'), 24)
			  || ' failure_reason=' || left(regexp_replace(COALESCE(task.failure_reason, ''), E'[\\n\\r]+', ' ', 'g'), 24)
			  || ' error=' || left(regexp_replace(COALESCE(task.error, ''), E'[\\n\\r]+', ' ', 'g'), 32)
			  || ' output=' || left(regexp_replace(COALESCE(task.result::text, ''), E'[\\n\\r]+', ' ', 'g'), 40)
			  || ' trigger_summary=' || left(regexp_replace(COALESCE(task.trigger_summary, ''), E'[\\n\\r]+', ' ', 'g'), 32)
			  || ' progress_at=' || COALESCE(progress.updated_at::text, 'none')
			  || ' progress_summary=' || left(regexp_replace(COALESCE(progress.summary, ''), E'[\\n\\r]+', ' ', 'g'), 40)
		  || ' progress_step=' || COALESCE(progress.step::text, 'none')
		  || ' progress_total=' || COALESCE(progress.total::text, 'none')
		FROM agent_task_queue task
		JOIN agent task_agent ON task_agent.id = task.agent_id
		LEFT JOIN agent_task_progress_snapshot progress ON progress.task_id = task.id
		WHERE task_agent.workspace_id = $1::uuid
		  AND task.chat_session_id IS NULL
		  AND COALESCE(task.context->>'type', '') <> 'agent_radar'
		  AND (
		    task.status IN ('queued', 'dispatched', 'running', 'waiting_local_directory')
		    OR (
		      task.status IN ('completed', 'cancelled', 'failed')
		      AND task.completed_at > now() - interval '24 hours'
		    )
		  )
		ORDER BY
		  CASE task.status
		    WHEN 'waiting_local_directory' THEN 0 WHEN 'failed' THEN 1
		    WHEN 'running' THEN 2 WHEN 'dispatched' THEN 3 WHEN 'queued' THEN 4
		    WHEN 'completed' THEN 5 ELSE 6
			  END,
			  task.created_at ASC,
			  task.id ASC
			LIMIT %d OFFSET %d
		`,
	}, scanSequence, workspaceID); err != nil {
		return err
	}
	if err := appendRotatingRequiredRows(ctx, out, b.db, rotatingSection{
		title:         "Group Channels",
		pageSize:      channelSectionPageSize,
		entryMaxBytes: channelSectionEntryMaxBytes,
		countQuery: `
			SELECT count(*)::bigint
			FROM channel
			WHERE workspace_id = $1::uuid
			  AND kind = 'group'
			  AND archived_at IS NULL
		`,
		pageQuery: `
			SELECT
			  'channel_id=' || channel.id::text
			  || ' name=' || left(regexp_replace(channel.name, E'[\\n\\r]+', ' ', 'g'), 60)
		  || ' updated_at=' || channel.updated_at::text
		  || ' latest_message_at=' || COALESCE(latest_message.created_at::text, 'none')
		  || ' latest_message_author=' || COALESCE(
		       latest_message.author_type || ':' || COALESCE(latest_message.author_id::text, 'none')
			       || ':' || left(regexp_replace(latest_message.author_name, E'[\\n\\r]+', ' ', 'g'), 40),
			       'none'
			     )
			  || ' latest_message_content=' || left(regexp_replace(COALESCE(latest_message.content, ''), E'[\\n\\r]+', ' ', 'g'), 100)
		  || ' agent_members=' || left(COALESCE((
		    SELECT string_agg(
		      member.member_id::text || ':' || COALESCE(NULLIF(member_agent.display_name, ''), member_agent.name),
		      ', ' ORDER BY member.created_at, member.member_id
		    )
		    FROM channel_member member
		    JOIN agent member_agent ON member_agent.id = member.member_id
		    WHERE member.channel_id = channel.id
		      AND member.workspace_id = channel.workspace_id
		      AND member.member_type = 'agent'
		      AND member_agent.archived_at IS NULL
			  ), 'none'), 120)
		FROM channel
		LEFT JOIN LATERAL (
		  SELECT message.created_at, message.author_type, message.author_id, message.author_name, message.content
		  FROM channel_message message
		  WHERE message.channel_id = channel.id
		    AND message.workspace_id = channel.workspace_id
		    AND message.deleted_at IS NULL
		  ORDER BY message.seq DESC
		  LIMIT 1
		) latest_message ON true
		WHERE channel.workspace_id = $1::uuid
			  AND channel.kind = 'group'
			  AND channel.archived_at IS NULL
			ORDER BY channel.updated_at DESC, channel.created_at ASC, channel.id ASC
			LIMIT %d OFFSET %d
		`,
	}, scanSequence, workspaceID); err != nil {
		return err
	}
	if err := appendRotatingRequiredRows(ctx, out, b.db, rotatingSection{
		title:         "Recent Group Messages",
		pageSize:      messageSectionPageSize,
		entryMaxBytes: messageSectionEntryMaxBytes,
		countQuery: `
			SELECT count(*)::bigint
			FROM channel
			CROSS JOIN LATERAL (
			  SELECT 1
			  FROM channel_message
			  WHERE channel_message.channel_id = channel.id
			    AND channel_message.workspace_id = channel.workspace_id
			    AND channel_message.deleted_at IS NULL
			  ORDER BY channel_message.seq DESC
			  LIMIT 5
			) message
			WHERE channel.workspace_id = $1::uuid
			  AND channel.kind = 'group'
			  AND channel.archived_at IS NULL
		`,
		pageQuery: `
			SELECT
			  'channel_id=' || channel.id::text
		  || ' channel_name=' || left(regexp_replace(channel.name, E'[\\n\\r]+', ' ', 'g'), 60)
		  || ' message_id=' || message.id::text
		  || ' seq=' || message.seq::text
		  || ' occurred_at=' || message.created_at::text
		  || ' author=' || message.author_type || ':' || COALESCE(message.author_id::text, 'none')
			  || ' name=' || left(regexp_replace(message.author_name, E'[\\n\\r]+', ' ', 'g'), 40)
			  || ' content=' || left(regexp_replace(message.content, E'[\\n\\r]+', ' ', 'g'), 120)
		  || ' agent_members=' || left(COALESCE((
		    SELECT string_agg(
		      member.member_id::text || ':' || COALESCE(NULLIF(member_agent.display_name, ''), member_agent.name),
		      ', ' ORDER BY member.created_at, member.member_id
		    )
		    FROM channel_member member
		    JOIN agent member_agent ON member_agent.id = member.member_id
		    WHERE member.channel_id = channel.id
		      AND member.workspace_id = channel.workspace_id
		      AND member.member_type = 'agent'
		      AND member_agent.archived_at IS NULL
		  ), 'none'), 160)
		FROM channel
		CROSS JOIN LATERAL (
		  SELECT channel_message.*
		  FROM channel_message
		  WHERE channel_message.channel_id = channel.id
		    AND channel_message.workspace_id = channel.workspace_id
		    AND channel_message.deleted_at IS NULL
		  ORDER BY channel_message.seq DESC
		  LIMIT 5
		) message
		WHERE channel.workspace_id = $1::uuid
			  AND channel.kind = 'group'
			  AND channel.archived_at IS NULL
			ORDER BY channel.updated_at DESC, channel.id ASC, message.seq DESC
			LIMIT %d OFFSET %d
		`,
	}, scanSequence, workspaceID); err != nil {
		return err
	}
	if err := appendOptionalRows(ctx, out, b.db, "Scheduled Reminders", `
		SELECT
		  'reminder_id=' || reminder.id::text
		  || ' agent_id=' || reminder.agent_id::text
		  || ' channel_id=' || reminder.anchor_channel_id::text
		  || ' fire_at=' || reminder.fire_at::text
		  || ' title=' || left(regexp_replace(reminder.title, E'[\\n\\r]+', ' ', 'g'), 200)
		FROM agent_reminder reminder
		JOIN channel reminder_channel
		  ON reminder_channel.id = reminder.anchor_channel_id
		 AND reminder_channel.workspace_id = reminder.workspace_id
		 AND reminder_channel.kind = 'group'
		WHERE reminder.workspace_id = $1::uuid
		  AND reminder.status IN ('scheduled', 'firing')
		ORDER BY reminder.fire_at ASC, reminder.id ASC
		LIMIT 30
	`, workspaceID); err != nil {
		return err
	}
	return appendOptionalRows(ctx, out, b.db, "GitHub Repositories", `
		SELECT p.title || ': ' || (pr.resource_ref->>'url')
		FROM project_resource pr
		JOIN project p ON p.id = pr.project_id
		WHERE p.workspace_id = $1::uuid
		  AND pr.resource_type = 'github_repo'
		ORDER BY pr.created_at DESC
		LIMIT 10
	`, workspaceID)
}

func truncateContext(value string, maxBytes int) string {
	if maxBytes <= 0 || len(value) <= maxBytes {
		return value
	}
	return strings.TrimSpace(truncateUTF8(value, maxBytes)) + "\n\n[Context truncated at the configured byte limit.]\n"
}

func truncateUTF8(value string, maxBytes int) string {
	if maxBytes <= 0 || len(value) <= maxBytes {
		return value
	}
	cut := maxBytes
	for cut > 0 && !utf8.RuneStart(value[cut]) {
		cut--
	}
	return value[:cut]
}

type rotatingSection struct {
	title         string
	pageSize      int
	entryMaxBytes int
	countArgCount int
	countQuery    string
	pageQuery     string
}

func querySingleInt64(ctx context.Context, db DBTX, query string, args ...any) (int64, error) {
	rows, err := db.Query(ctx, query, args...)
	if err != nil {
		return 0, err
	}
	defer rows.Close()
	if !rows.Next() {
		if err := rows.Err(); err != nil {
			return 0, err
		}
		return 0, fmt.Errorf("query returned no rows")
	}
	var value int64
	if err := rows.Scan(&value); err != nil {
		return 0, err
	}
	if err := rows.Err(); err != nil {
		return 0, err
	}
	return value, nil
}

func appendRotatingRequiredRows(
	ctx context.Context,
	out *strings.Builder,
	db DBTX,
	section rotatingSection,
	scanSequence int64,
	args ...any,
) error {
	if section.pageSize <= 0 || section.entryMaxBytes <= 0 {
		return fmt.Errorf("invalid rotating page configuration for %s", section.title)
	}
	// 512 bytes covers the heading, rotation metadata, bullets, newlines, and
	// maximum-width integer fields. Keep this executable guard next to the
	// constants so a future page-size change cannot silently reintroduce
	// section_byte_budget omissions inside a selected page.
	if section.pageSize*(section.entryMaxBytes+3)+512 > requiredSectionMaxBytes {
		return fmt.Errorf("rotating page for %s exceeds section byte budget", section.title)
	}
	countArgs := args
	if section.countArgCount > 0 {
		if section.countArgCount > len(args) {
			return fmt.Errorf("count %s rotation arguments: have %d, need %d", section.title, len(args), section.countArgCount)
		}
		countArgs = args[:section.countArgCount]
	}
	total, err := querySingleInt64(ctx, db, section.countQuery, countArgs...)
	if err != nil {
		return fmt.Errorf("count %s rotation rows: %w", section.title, err)
	}
	if total < 0 {
		return fmt.Errorf("negative rotation row count for %s", section.title)
	}
	pageCount := (total + int64(section.pageSize) - 1) / int64(section.pageSize)
	if pageCount == 0 {
		pageCount = 1
	}
	pageIndex := scanSequence % pageCount
	if pageIndex < 0 {
		pageIndex += pageCount
	}
	offset := pageIndex * int64(section.pageSize)
	pageQuery := fmt.Sprintf(section.pageQuery, section.pageSize, offset)
	metadata := fmt.Sprintf(
		"rotation_page=%d/%d total_count=%d page_size=%d scan_sequence=%d",
		pageIndex+1, pageCount, total, section.pageSize, scanSequence,
	)
	return appendRowsWithinBudget(
		ctx, out, db, section.title, requiredSectionMaxBytes, true,
		[]string{metadata}, section.entryMaxBytes, pageQuery, args...,
	)
}

func appendRequiredRows(ctx context.Context, out *strings.Builder, db DBTX, title, query string, args ...any) error {
	return appendRowsWithinBudget(ctx, out, db, title, requiredSectionMaxBytes, true, nil, 0, query, args...)
}

func appendOptionalRows(ctx context.Context, out *strings.Builder, db DBTX, title, query string, args ...any) error {
	return appendRowsWithinBudget(ctx, out, db, title, optionalSectionMaxBytes, false, nil, 0, query, args...)
}

func appendRowsWithinBudget(
	ctx context.Context,
	out *strings.Builder,
	db DBTX,
	title string,
	maxBytes int,
	required bool,
	prefixLines []string,
	entryMaxBytes int,
	query string,
	args ...any,
) error {
	rows, err := db.Query(ctx, query, args...)
	if err != nil {
		return err
	}
	defer rows.Close()
	lines := append([]string(nil), prefixLines...)
	for rows.Next() {
		var line string
		if err := rows.Scan(&line); err != nil {
			return err
		}
		line = strings.TrimSpace(line)
		if line != "" {
			if entryMaxBytes > 0 {
				line = truncateUTF8(line, entryMaxBytes)
			}
			lines = append(lines, line)
		}
	}
	if err := rows.Err(); err != nil {
		return err
	}
	if len(lines) == 0 && !required {
		return nil
	}

	var section strings.Builder
	fmt.Fprintf(&section, "## %s\n\n", title)
	if len(lines) == 0 {
		section.WriteString("- none\n\n")
		out.WriteString(section.String())
		return nil
	}

	included := 0
	for index, line := range lines {
		entry := "- " + line + "\n"
		remainingAfterEntry := len(lines) - index - 1
		reserved := 1
		if remainingAfterEntry > 0 {
			reserved += len(fmt.Sprintf("- omitted_count=%d reason=section_byte_budget\n", remainingAfterEntry))
		}
		if maxBytes > 0 && section.Len()+len(entry)+reserved > maxBytes {
			break
		}
		section.WriteString(entry)
		included++
	}
	if omitted := len(lines) - included; omitted > 0 {
		fmt.Fprintf(&section, "- omitted_count=%d reason=section_byte_budget\n", omitted)
	}
	section.WriteString("\n")
	out.WriteString(section.String())
	return nil
}

func appendPreparedRowsWithinBudget(out *strings.Builder, title string, maxBytes int, required bool, lines []string) error {
	if len(lines) == 0 && !required {
		return nil
	}
	var section strings.Builder
	fmt.Fprintf(&section, "## %s\n\n", title)
	if len(lines) == 0 {
		section.WriteString("- none\n\n")
	} else {
		for index, line := range lines {
			entry := "- " + strings.TrimSpace(line) + "\n"
			if maxBytes > 0 && section.Len()+len(entry)+1 > maxBytes {
				return fmt.Errorf("prepared %s row %d exceeds section budget", title, index)
			}
			section.WriteString(entry)
		}
		section.WriteString("\n")
	}
	if out.Len()+section.Len() > contextMaxBytes {
		return fmt.Errorf("prepared %s section exceeds context budget", title)
	}
	out.WriteString(section.String())
	return nil
}
