package radar

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"unicode/utf8"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
)

func TestContextBuilderReadsAgentPlanFiles(t *testing.T) {
	root := t.TempDir()
	workspaceID := "workspace-1"
	agentID := "agent-1"
	agentRoot := filepath.Join(root, workspaceID, ".multica", "agents", agentID)
	for path, content := range map[string]string{
		"notes/agent-plan.md": "# Agent Plan\n\n## Watchlist\n- Watch backend auth\n",
		"memory/MEMORY.md":    "# Memory\n\nOwns backend integrations.\n",
		"memory/STATE.md":     "# State\n\nInvestigating GitHub files.\n",
		"notes/work-log.md":   "# Work Log\n\nFixed compose env.\n" + strings.Repeat("进", 1000),
	} {
		full := filepath.Join(agentRoot, filepath.FromSlash(path))
		if err := os.MkdirAll(filepath.Dir(full), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(full, []byte(content), 0o644); err != nil {
			t.Fatal(err)
		}
	}

	ctx, err := NewContextBuilder(nil, root).Build(t.Context(), workspaceID, agentID)
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{"Watch backend auth", "Owns backend integrations", "Investigating GitHub files", "Fixed compose env"} {
		if !strings.Contains(ctx.Markdown, want) {
			t.Fatalf("context missing %q:\n%s", want, ctx.Markdown)
		}
	}
	if !utf8.ValidString(ctx.Markdown) {
		t.Fatal("file section byte limit split a UTF-8 rune")
	}
}

func TestContextBuilderPreservesWorkspaceSnapshotAndRecentTerminalTasksAtCapacity(t *testing.T) {
	dbURL := os.Getenv("DATABASE_URL")
	if dbURL == "" {
		dbURL = "postgres://multica:multica@localhost:5432/multica?sslmode=disable"
	}
	pool, err := pgxpool.New(t.Context(), dbURL)
	if err != nil {
		t.Skipf("database unavailable: %v", err)
	}
	t.Cleanup(pool.Close)
	if err := pool.Ping(t.Context()); err != nil {
		t.Skipf("database unavailable: %v", err)
	}

	suffix := strings.ReplaceAll(uuid.NewString(), "-", "")
	var workspaceID, userID, runtimeID, supervisorID, peerAgentID string
	if err := pool.QueryRow(t.Context(), `
		INSERT INTO workspace (name, slug, issue_prefix)
		VALUES ($1, $2, 'RAD')
		RETURNING id
	`, "Radar Test", "radar-"+suffix).Scan(&workspaceID); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		_, _ = pool.Exec(context.Background(), `DELETE FROM workspace WHERE id = $1`, workspaceID)
		_, _ = pool.Exec(context.Background(), `DELETE FROM "user" WHERE id = $1`, userID)
	})
	if err := pool.QueryRow(t.Context(), `
		INSERT INTO "user" (name, display_name, email)
		VALUES ($1, 'Radar Owner', $2)
		RETURNING id
	`, "radar-owner-"+suffix, "radar-owner-"+suffix+"@example.test").Scan(&userID); err != nil {
		t.Fatal(err)
	}
	if err := pool.QueryRow(t.Context(), `
		INSERT INTO agent_runtime (workspace_id, name, runtime_mode, provider, status, device_info, metadata)
		VALUES ($1, 'Radar Runtime', 'local', 'claude', 'online', '', '{}'::jsonb)
		RETURNING id
	`, workspaceID).Scan(&runtimeID); err != nil {
		t.Fatal(err)
	}
	if err := pool.QueryRow(t.Context(), `
		INSERT INTO agent (workspace_id, name, display_name, runtime_mode, runtime_config, runtime_id, visibility, max_concurrent_tasks, owner_id)
		VALUES ($1, $2, 'Wendy', 'local', '{}'::jsonb, $3, 'private', 1, $4)
		RETURNING id
	`, workspaceID, "radar-supervisor-"+suffix, runtimeID, userID).Scan(&supervisorID); err != nil {
		t.Fatal(err)
	}
	if err := pool.QueryRow(t.Context(), `
		INSERT INTO agent (workspace_id, name, display_name, description, runtime_mode, runtime_config, runtime_id, visibility, max_concurrent_tasks, owner_id)
		VALUES ($1, $2, 'Backend Engineer', 'Owns backend APIs and database changes', 'local', '{}'::jsonb, $3, 'workspace', 1, $4)
		RETURNING id
	`, workspaceID, "radar-peer-"+suffix, runtimeID, userID).Scan(&peerAgentID); err != nil {
		t.Fatal(err)
	}

	var openIssueID, doneIssueID string
	if err := pool.QueryRow(t.Context(), `
		INSERT INTO issue (workspace_id, number, title, description, status, priority, assignee_type, assignee_id, creator_type, creator_id, position)
		VALUES ($1, 7, 'Investigate radar', 'Trace why scheduled work remains queued', 'todo', 'none', 'agent', $2, 'agent', $3, 0)
		RETURNING id
	`, workspaceID, peerAgentID, supervisorID).Scan(&openIssueID); err != nil {
		t.Fatal(err)
	}
	if err := pool.QueryRow(t.Context(), `
		INSERT INTO issue (workspace_id, number, title, status, priority, creator_type, creator_id, position)
		VALUES ($1, 8, 'Already resolved', 'done', 'none', 'agent', $2, 1)
		RETURNING id
	`, workspaceID, supervisorID).Scan(&doneIssueID); err != nil {
		t.Fatal(err)
	}

	var activeTaskID, completedTaskID, cancelledTaskID, failedTaskID, radarTaskID string
	if err := pool.QueryRow(t.Context(), `
		INSERT INTO agent_inbox_event (
			agent_id, runtime_id, issue_id, status, priority, context, trigger_summary, wait_reason
		)
		VALUES ($1, $2, $3, 'waiting_local_directory', 1, '{"type":"issue"}'::jsonb, 'Implement issue', 'workspace lock busy')
		RETURNING id
	`, peerAgentID, runtimeID, openIssueID).Scan(&activeTaskID); err != nil {
		t.Fatal(err)
	}
	if _, err := pool.Exec(t.Context(), `
		INSERT INTO agent_task_progress_snapshot (task_id, summary, step, total, updated_at)
		VALUES ($1, 'running backend tests', 3, 5, now())
	`, activeTaskID); err != nil {
		t.Fatal(err)
	}
	if err := pool.QueryRow(t.Context(), `
		INSERT INTO agent_inbox_event (
			agent_id, runtime_id, issue_id, status, priority, context, trigger_summary,
			completed_at, result
		)
		VALUES ($1, $2, $3, 'completed', 1, '{"type":"issue"}'::jsonb, 'Completed implementation',
		        now(), '{"outcome":"shipped"}'::jsonb)
		RETURNING id
	`, peerAgentID, runtimeID, openIssueID).Scan(&completedTaskID); err != nil {
		t.Fatal(err)
	}
	if err := pool.QueryRow(t.Context(), `
		INSERT INTO agent_inbox_event (
			agent_id, runtime_id, issue_id, status, priority, context, trigger_summary,
			completed_at, error, failure_reason
		)
		VALUES ($1, $2, $3, 'cancelled', 1, '{"type":"issue"}'::jsonb, 'Cancelled implementation',
		        now(), 'cancelled by owner', 'manual')
		RETURNING id
	`, peerAgentID, runtimeID, openIssueID).Scan(&cancelledTaskID); err != nil {
		t.Fatal(err)
	}
	if err := pool.QueryRow(t.Context(), `
		INSERT INTO agent_inbox_event (
			agent_id, runtime_id, issue_id, status, priority, context, trigger_summary,
			completed_at, error, failure_reason
		)
		VALUES ($1, $2, $3, 'failed', 1, '{"type":"issue"}'::jsonb, 'Failed implementation',
		        now(), 'provider timeout', 'agent_error')
		RETURNING id
	`, peerAgentID, runtimeID, openIssueID).Scan(&failedTaskID); err != nil {
		t.Fatal(err)
	}
	if err := pool.QueryRow(t.Context(), `
		INSERT INTO agent_inbox_event (agent_id, runtime_id, status, priority, context, trigger_summary)
		VALUES ($1, $2, 'queued', 1, '{"type":"agent_radar"}'::jsonb, 'Radar housekeeping')
		RETURNING id
	`, supervisorID, runtimeID).Scan(&radarTaskID); err != nil {
		t.Fatal(err)
	}

	var groupChannelID, dmChannelID string
	if err := pool.QueryRow(t.Context(), `
		INSERT INTO channel (workspace_id, name, created_by, kind)
		VALUES ($1, $2, $3, 'group')
		RETURNING id
	`, workspaceID, "radar-group-"+suffix, userID).Scan(&groupChannelID); err != nil {
		t.Fatal(err)
	}
	if err := pool.QueryRow(t.Context(), `
		INSERT INTO channel (workspace_id, name, created_by, kind)
		VALUES ($1, $2, $3, 'dm')
		RETURNING id
	`, workspaceID, "dm-radar-"+suffix, userID).Scan(&dmChannelID); err != nil {
		t.Fatal(err)
	}
	var dmChatSessionID, dmTaskID, dmReminderID string
	if err := pool.QueryRow(t.Context(), `
		INSERT INTO chat_session (workspace_id, agent_id, creator_id, title, status)
		VALUES ($1, $2, $3, 'private radar sentinel', 'active')
		RETURNING id
	`, workspaceID, peerAgentID, userID).Scan(&dmChatSessionID); err != nil {
		t.Fatal(err)
	}
	if err := pool.QueryRow(t.Context(), `
		INSERT INTO agent_inbox_event (
			agent_id, runtime_id, status, priority, chat_session_id, context,
			trigger_summary, error
		) VALUES ($1, $2, 'running', 1, $3, '{"type":"chat"}'::jsonb,
		          'private DM task trigger', 'private DM task error')
		RETURNING id
	`, peerAgentID, runtimeID, dmChatSessionID).Scan(&dmTaskID); err != nil {
		t.Fatal(err)
	}
	if err := pool.QueryRow(t.Context(), `
		INSERT INTO agent_reminder (
			workspace_id, agent_id, title, anchor_channel_id, fire_at, status
		) VALUES ($1, $2, 'private DM reminder title', $3, now() + interval '1 hour', 'scheduled')
		RETURNING id
	`, workspaceID, peerAgentID, dmChannelID).Scan(&dmReminderID); err != nil {
		t.Fatal(err)
	}

	var groupMessageID, dmMessageID string
	if err := pool.QueryRow(t.Context(), `
		INSERT INTO channel_message (channel_id, workspace_id, author_type, author_id, author_name, content)
		VALUES ($1, $2, 'agent', $3, 'Backend Engineer', 'Group progress update')
		RETURNING id
	`, groupChannelID, workspaceID, peerAgentID).Scan(&groupMessageID); err != nil {
		t.Fatal(err)
	}
	if err := pool.QueryRow(t.Context(), `
		INSERT INTO channel_message (channel_id, workspace_id, author_type, author_id, author_name, content)
		VALUES ($1, $2, 'user', $3, 'Radar Owner', 'private DM secret')
		RETURNING id
	`, dmChannelID, workspaceID, userID).Scan(&dmMessageID); err != nil {
		t.Fatal(err)
	}

	// Force every early section beyond its own byte budget. The group-channel
	// sections that follow must remain present and usable.
	if _, err := pool.Exec(t.Context(), `
		INSERT INTO agent (
			workspace_id, name, display_name, runtime_mode, runtime_config, runtime_id,
			visibility, max_concurrent_tasks, owner_id
		)
		SELECT $1, 'bulk-agent-' || $4 || '-' || series::text, repeat('A', 100),
		       'local', '{}'::jsonb, $2, 'workspace', 1, $3
		FROM generate_series(1, 100) AS series
	`, workspaceID, runtimeID, userID, suffix); err != nil {
		t.Fatal(err)
	}
	if _, err := pool.Exec(t.Context(), `
		INSERT INTO issue (
			workspace_id, number, title, status, priority, creator_type, creator_id, position,
			created_at, updated_at
		)
		SELECT $1, 1000 + series, repeat('large issue context ', 12) || series::text,
		       'backlog', 'none', 'agent', $2, series,
		       now() - interval '1 day', now() - interval '1 day'
		FROM generate_series(1, 50) AS series
	`, workspaceID, supervisorID); err != nil {
		t.Fatal(err)
	}
	if _, err := pool.Exec(t.Context(), `
		INSERT INTO agent_inbox_event (
			agent_id, runtime_id, status, priority, context, trigger_summary, completed_at
		)
		SELECT $1, $2, 'cancelled', 1, '{"type":"issue"}'::jsonb,
		       repeat('bulk terminal task evidence ', 10) || series::text, now()
		FROM generate_series(1, 40) AS series
	`, peerAgentID, runtimeID); err != nil {
		t.Fatal(err)
	}

	ctx, err := NewContextBuilder(pool, "").Build(t.Context(), workspaceID, supervisorID)
	if err != nil {
		t.Fatal(err)
	}

	for _, tc := range []struct {
		title   string
		include []string
		exclude []string
	}{
		{title: "Workspace Agents", include: []string{supervisorID, peerAgentID, "Backend Engineer", "capabilities=Owns backend APIs"}},
		{title: "Open Issues", include: []string{openIssueID, "Investigate radar", "description=Trace why scheduled work remains queued"}, exclude: []string{doneIssueID}},
		{title: "Active Tasks", include: []string{
			activeTaskID, completedTaskID, cancelledTaskID, failedTaskID,
			"wait_reason=workspace lock busy", "error=provider timeout", "failure_reason=agent_error",
			"output={\"outcome\": \"shipped\"}", "progress_summary=running backend tests",
			"progress_step=3", "progress_total=5", "progress_at=",
		}, exclude: []string{radarTaskID}},
		{title: "Group Channels", include: []string{groupChannelID, "radar-group-" + suffix, "latest_message_content=Group progress update"}, exclude: []string{dmChannelID}},
		{title: "Recent Group Messages", include: []string{groupMessageID, "Group progress update"}, exclude: []string{dmMessageID, "private DM secret"}},
	} {
		section := markdownSection(t, ctx.Markdown, tc.title)
		for _, want := range tc.include {
			if !strings.Contains(section, want) {
				t.Fatalf("%s missing exact ID %q:\n%s", tc.title, want, section)
			}
		}
		for _, unwanted := range tc.exclude {
			if strings.Contains(section, unwanted) {
				t.Fatalf("%s unexpectedly contains %q:\n%s", tc.title, unwanted, section)
			}
		}
	}
	for _, title := range []string{"Workspace Agents", "Open Issues", "Active Tasks", "Group Channels", "Recent Group Messages"} {
		section := markdownSection(t, ctx.Markdown, title)
		if !strings.Contains(section, "rotation_page=") {
			t.Fatalf("%s did not report its rotating page:\n%s", title, section)
		}
		if strings.Contains(section, "omitted_count=") {
			t.Fatalf("%s dropped a selected-page row to the section byte budget:\n%s", title, section)
		}
	}
	if len(ctx.Markdown) > contextMaxBytes {
		t.Fatalf("context bytes = %d, want <= %d", len(ctx.Markdown), contextMaxBytes)
	}
	for _, unwanted := range []string{
		doneIssueID, radarTaskID, dmChannelID, dmMessageID, "private DM secret",
		dmTaskID, "private DM task trigger", "private DM task error",
		dmReminderID, "private DM reminder title",
	} {
		if strings.Contains(ctx.Markdown, unwanted) {
			t.Fatalf("supervisor context unexpectedly contains excluded value %q:\n%s", unwanted, ctx.Markdown)
		}
	}
}

func TestContextBuilderRotatesPastFirstPageOnlyAfterSuccessfulScheduledReview(t *testing.T) {
	dbURL := os.Getenv("DATABASE_URL")
	if dbURL == "" {
		dbURL = "postgres://multica:multica@localhost:5432/multica?sslmode=disable"
	}
	pool, err := pgxpool.New(t.Context(), dbURL)
	if err != nil {
		t.Skipf("database unavailable: %v", err)
	}
	t.Cleanup(pool.Close)
	if err := pool.Ping(t.Context()); err != nil {
		t.Skipf("database unavailable: %v", err)
	}

	suffix := strings.ReplaceAll(uuid.NewString(), "-", "")
	var workspaceID, userID, runtimeID, supervisorID string
	if err := pool.QueryRow(t.Context(), `
		INSERT INTO workspace (name, slug, issue_prefix)
		VALUES ($1, $2, 'ROT')
		RETURNING id
	`, "Radar Rotation Test", "radar-rotation-"+suffix).Scan(&workspaceID); err != nil {
		t.Fatal(err)
	}
	if err := pool.QueryRow(t.Context(), `
		INSERT INTO "user" (name, display_name, email)
		VALUES ($1, 'Rotation Owner', $2)
		RETURNING id
	`, "rotation-owner-"+suffix, "rotation-owner-"+suffix+"@example.test").Scan(&userID); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		_, _ = pool.Exec(context.Background(), `DELETE FROM workspace WHERE id = $1`, workspaceID)
		_, _ = pool.Exec(context.Background(), `DELETE FROM "user" WHERE id = $1`, userID)
	})
	if _, err := pool.Exec(t.Context(), `
		INSERT INTO member (workspace_id, user_id, role)
		VALUES ($1, $2, 'owner')
	`, workspaceID, userID); err != nil {
		t.Fatal(err)
	}
	if err := pool.QueryRow(t.Context(), `
		INSERT INTO agent_runtime (
			workspace_id, owner_id, name, runtime_mode, provider, status,
			device_info, metadata, last_seen_at
		) VALUES ($1, $2, 'Rotation Runtime', 'local', 'claude', 'online', '', '{}'::jsonb, now())
		RETURNING id
	`, workspaceID, userID).Scan(&runtimeID); err != nil {
		t.Fatal(err)
	}
	if err := pool.QueryRow(t.Context(), `
		INSERT INTO agent (
			workspace_id, name, display_name, description, runtime_mode,
			runtime_config, runtime_id, visibility, max_concurrent_tasks, owner_id,
			created_at
		) VALUES ($1, $2, 'Wendy', 'Workspace supervisor', 'local', '{}'::jsonb,
		          $3, 'private', 1, $4, TIMESTAMPTZ '2026-01-01 00:00:00+00')
		RETURNING id
	`, workspaceID, "rotation-wendy-"+suffix, runtimeID, userID).Scan(&supervisorID); err != nil {
		t.Fatal(err)
	}
	if _, err := pool.Exec(t.Context(), `
		INSERT INTO workspace_radar_state (
			workspace_id, supervisor_agent_id, last_success_at, last_full_review_at, next_due_at
		) VALUES ($1, $2, TIMESTAMPTZ '2000-01-01 00:00:00+00',
		          TIMESTAMPTZ '2000-01-01 00:00:00+00', now())
	`, workspaceID, supervisorID); err != nil {
		t.Fatal(err)
	}

	var workerID, overflowAgentID string
	for index := 1; index <= agentSectionPageSize; index++ {
		var agentID string
		if err := pool.QueryRow(t.Context(), `
			INSERT INTO agent (
				workspace_id, name, display_name, description, runtime_mode,
				runtime_config, runtime_id, visibility, max_concurrent_tasks, owner_id,
				created_at
			) VALUES ($1, $2, $3, 'Rotation worker', 'local', '{}'::jsonb,
			          $4, 'workspace', 1, $5,
			          TIMESTAMPTZ '2026-01-01 00:00:00+00' + ($6::int * interval '1 minute'))
			RETURNING id
		`, workspaceID, "rotation-agent-"+suffix+"-"+fmt.Sprint(index), "Rotation Agent "+fmt.Sprint(index), runtimeID, userID, index).Scan(&agentID); err != nil {
			t.Fatal(err)
		}
		if index == 1 {
			workerID = agentID
		}
		if index == agentSectionPageSize {
			overflowAgentID = agentID
		}
	}

	issueIDs := make([]string, 0, issueSectionPageSize+1)
	for index := 1; index <= issueSectionPageSize+1; index++ {
		var issueID string
		if err := pool.QueryRow(t.Context(), `
			INSERT INTO issue (
				workspace_id, number, title, description, status, priority,
				assignee_type, assignee_id, creator_type, creator_id, position,
				updated_at
			) VALUES ($1, $2, $3, 'Rotation issue', 'todo', 'none', 'agent', $4,
			          'agent', $5, $6,
			          TIMESTAMPTZ '2026-01-02 00:00:00+00' - ($7::int * interval '1 minute'))
			RETURNING id
		`, workspaceID, index, "Rotation Issue "+fmt.Sprint(index), workerID, supervisorID, float64(index), index).Scan(&issueID); err != nil {
			t.Fatal(err)
		}
		issueIDs = append(issueIDs, issueID)
	}
	overflowIssueID := issueIDs[len(issueIDs)-1]

	var overflowTaskID string
	for index := 1; index <= taskSectionPageSize+1; index++ {
		var taskID string
		if err := pool.QueryRow(t.Context(), `
			INSERT INTO agent_inbox_event (
				agent_id, runtime_id, issue_id, status, priority, context,
				trigger_summary, created_at
			) VALUES ($1, $2, $3, 'queued', 1, '{"type":"issue"}'::jsonb,
			          $4, TIMESTAMPTZ '2026-01-03 00:00:00+00' + ($5::int * interval '1 minute'))
			RETURNING id
		`, workerID, runtimeID, issueIDs[index-1], "Rotation Task "+fmt.Sprint(index), index).Scan(&taskID); err != nil {
			t.Fatal(err)
		}
		if index == taskSectionPageSize+1 {
			overflowTaskID = taskID
		}
	}

	channelIDs := make([]string, 0, channelSectionPageSize+1)
	for index := 1; index <= channelSectionPageSize+1; index++ {
		var channelID string
		if err := pool.QueryRow(t.Context(), `
			INSERT INTO channel (workspace_id, name, created_by, kind, updated_at)
			VALUES ($1, $2, $3, 'group',
			        TIMESTAMPTZ '2026-01-04 00:00:00+00' - ($4::int * interval '1 hour'))
			RETURNING id
		`, workspaceID, "rotation-group-"+suffix+"-"+fmt.Sprint(index), userID, index).Scan(&channelID); err != nil {
			t.Fatal(err)
		}
		channelIDs = append(channelIDs, channelID)
	}
	overflowChannelID := channelIDs[len(channelIDs)-1]
	if _, err := pool.Exec(t.Context(), `
		INSERT INTO channel_member (channel_id, workspace_id, member_type, member_id)
		VALUES ($1, $2, 'agent', $3)
	`, channelIDs[1], workspaceID, workerID); err != nil {
		t.Fatal(err)
	}

	for index := 1; index <= messageSectionPageSize; index++ {
		if _, err := pool.Exec(t.Context(), `
			INSERT INTO channel_message (
				channel_id, workspace_id, author_type, author_id, author_name, content
			) VALUES ($1, $2, 'agent', $3, 'Rotation Agent', $4)
		`, channelIDs[0], workspaceID, workerID, "First page message "+fmt.Sprint(index)); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := pool.Exec(t.Context(), `
		INSERT INTO channel_message (
			channel_id, workspace_id, author_type, author_name, content
		) VALUES ($1, $2, 'system', 'system', 'Ordinary system work update')
	`, channelIDs[0], workspaceID); err != nil {
		t.Fatal(err)
	}
	var overflowMessageID string
	if err := pool.QueryRow(t.Context(), `
		INSERT INTO channel_message (
			channel_id, workspace_id, author_type, author_id, author_name, content
		) VALUES ($1, $2, 'agent', $3, 'Rotation Agent', 'Second page message')
		RETURNING id
	`, channelIDs[1], workspaceID, workerID).Scan(&overflowMessageID); err != nil {
		t.Fatal(err)
	}
	var canonicalMembershipRows int
	if err := pool.QueryRow(t.Context(), `
		SELECT count(*)
		FROM channel_message
		WHERE workspace_id = $1
		  AND membership_generation_id IS NOT NULL
		  AND deleted_at IS NULL
	`, workspaceID).Scan(&canonicalMembershipRows); err != nil {
		t.Fatal(err)
	}
	if canonicalMembershipRows == 0 {
		t.Fatal("expected canonical membership rows to remain persisted and channel-visible")
	}

	builder := NewContextBuilder(pool, "")
	first, err := builder.Build(t.Context(), workspaceID, supervisorID)
	if err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct {
		title      string
		overflowID string
	}{
		{title: "Workspace Agents", overflowID: overflowAgentID},
		{title: "Open Issues", overflowID: overflowIssueID},
		{title: "Active Tasks", overflowID: overflowTaskID},
		{title: "Group Channels", overflowID: overflowChannelID},
		{title: "Recent Group Messages", overflowID: overflowMessageID},
	} {
		section := markdownSection(t, first.Markdown, tc.title)
		if !strings.Contains(section, "rotation_page=1/2") {
			t.Fatalf("%s first snapshot page metadata is wrong:\n%s", tc.title, section)
		}
		if strings.Contains(section, tc.overflowID) {
			t.Fatalf("%s first page unexpectedly contained overflow row %s:\n%s", tc.title, tc.overflowID, section)
		}
		if strings.Contains(section, "omitted_count=") {
			t.Fatalf("%s first selected page was byte-truncated:\n%s", tc.title, section)
		}
	}
	groupChannelsSection := markdownSection(t, first.Markdown, "Group Channels")
	if !strings.Contains(groupChannelsSection, "latest_message_content=Ordinary system work update") {
		t.Fatalf("ordinary system work event was excluded from group latest projection:\n%s", groupChannelsSection)
	}
	messageSection := markdownSection(t, first.Markdown, "Recent Group Messages")
	if !strings.Contains(messageSection, "content=Ordinary system work update") {
		t.Fatalf("ordinary system work event was excluded from recent message projection:\n%s", messageSection)
	}
	if strings.Contains(groupChannelsSection, "joined this channel") || strings.Contains(messageSection, "joined this channel") {
		t.Fatalf("canonical membership rows leaked into Radar work narrative:\n%s\n%s", groupChannelsSection, messageSection)
	}

	if _, err := pool.Exec(t.Context(), `
		INSERT INTO agent_radar_run (
			workspace_id, agent_id, runtime_id, trigger_kind, trigger_ref,
			status, cooldown_key, context_summary, scheduled_for, finished_at
		) VALUES (
			$1, $2, $3, 'scheduled', 'failed-review',
			'failed', 'workspace_supervisor_radar', 'failed review', now(), now()
		)
	`, workspaceID, supervisorID, runtimeID); err != nil {
		t.Fatal(err)
	}
	failedRetry, err := builder.Build(t.Context(), workspaceID, supervisorID)
	if err != nil {
		t.Fatal(err)
	}
	for _, title := range []string{"Workspace Agents", "Open Issues", "Active Tasks", "Group Channels", "Recent Group Messages"} {
		section := markdownSection(t, failedRetry.Markdown, title)
		if !strings.Contains(section, "rotation_page=1/2") {
			t.Fatalf("%s advanced after a failed review:\n%s", title, section)
		}
	}

	if _, err := pool.Exec(t.Context(), `
		INSERT INTO agent_radar_run (
			workspace_id, agent_id, runtime_id, trigger_kind, trigger_ref,
			status, cooldown_key, context_summary, scheduled_for, finished_at
		) VALUES (
			$1, $2, $3, 'scheduled', 'successful-review',
			'succeeded', 'workspace_supervisor_radar', 'successful review', now(), now()
		)
	`, workspaceID, supervisorID, runtimeID); err != nil {
		t.Fatal(err)
	}
	second, err := builder.Build(t.Context(), workspaceID, supervisorID)
	if err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct {
		title      string
		overflowID string
	}{
		{title: "Workspace Agents", overflowID: overflowAgentID},
		{title: "Open Issues", overflowID: overflowIssueID},
		{title: "Active Tasks", overflowID: overflowTaskID},
		{title: "Group Channels", overflowID: overflowChannelID},
		{title: "Recent Group Messages", overflowID: overflowMessageID},
	} {
		section := markdownSection(t, second.Markdown, tc.title)
		if !strings.Contains(section, "rotation_page=2/2") {
			t.Fatalf("%s second snapshot page metadata is wrong:\n%s", tc.title, section)
		}
		if !strings.Contains(section, tc.overflowID) {
			t.Fatalf("%s did not rotate to overflow row %s:\n%s", tc.title, tc.overflowID, section)
		}
		if strings.Contains(section, "omitted_count=") {
			t.Fatalf("%s second selected page was byte-truncated:\n%s", tc.title, section)
		}
	}
	messageSection = markdownSection(t, second.Markdown, "Recent Group Messages")
	for _, want := range []string{"channel_name=rotation-group-" + suffix + "-2", "agent_members=" + workerID + ":Rotation Agent 1"} {
		if !strings.Contains(messageSection, want) {
			t.Fatalf("rotated message lacks channel targeting context %q:\n%s", want, messageSection)
		}
	}
	if len(first.Markdown) > contextMaxBytes || len(failedRetry.Markdown) > contextMaxBytes || len(second.Markdown) > contextMaxBytes {
		t.Fatalf(
			"rotating contexts exceeded max bytes: first=%d failed_retry=%d second=%d max=%d",
			len(first.Markdown), len(failedRetry.Markdown), len(second.Markdown), contextMaxBytes,
		)
	}
}

func markdownSection(t *testing.T, markdown, title string) string {
	t.Helper()
	marker := "## " + title + "\n"
	start := strings.Index(markdown, marker)
	if start < 0 {
		t.Fatalf("context missing %q section:\n%s", title, markdown)
	}
	body := markdown[start+len(marker):]
	if end := strings.Index(body, "\n## "); end >= 0 {
		body = body[:end]
	}
	return body
}
