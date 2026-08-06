package runtimecleanup

import (
	"context"
	"fmt"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
)

func TestIsLegacyRuntimeSystemNoticeContent(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name    string
		content string
		want    bool
	}{
		{name: "daemon reason token", content: "daemon_outdated", want: true},
		{name: "no reply token", content: "no_reply", want: true},
		{name: "english visible notice", content: "Local daemon is outdated.", want: true},
		{name: "chinese visible notice", content: "本地守护进程已过期，需要更新。", want: true},
		{name: "trim surrounding whitespace", content: "  本地守护进程未连接。\n", want: true},
		{name: "not a current-state notice", content: "Alice joined the channel", want: false},
		{name: "not substring match", content: "warning: Local daemon is outdated.", want: false},
		{name: "empty", content: "", want: false},
	}

	for _, tt := range tests {
		tt := tt
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			if got := IsLegacyRuntimeSystemNoticeContent(tt.content); got != tt.want {
				t.Fatalf("IsLegacyRuntimeSystemNoticeContent(%q) = %v, want %v", tt.content, got, tt.want)
			}
		})
	}
}

func TestLegacyRuntimeSystemNoticeContentsReturnsCopy(t *testing.T) {
	t.Parallel()

	contents := LegacyRuntimeSystemNoticeContents()
	if len(contents) == 0 {
		t.Fatal("expected non-empty legacy runtime notice set")
	}
	contents[0] = "mutated"
	if LegacyRuntimeSystemNoticeContents()[0] == "mutated" {
		t.Fatal("LegacyRuntimeSystemNoticeContents must return a defensive copy")
	}
}

func TestNormalizeOptions(t *testing.T) {
	t.Parallel()

	opts, err := normalizeOptions(CleanupOptions{WorkspaceID: "  ws  ", ChannelID: "\tch\n"})
	if err != nil {
		t.Fatalf("normalizeOptions: %v", err)
	}
	if opts.SampleLimit != 20 {
		t.Fatalf("default sample limit = %d, want 20", opts.SampleLimit)
	}
	if opts.WorkspaceID != "ws" || opts.ChannelID != "ch" {
		t.Fatalf("filters not trimmed: %#v", opts)
	}

	if _, err := normalizeOptions(CleanupOptions{MaxDelete: -1}); err == nil {
		t.Fatal("negative MaxDelete must error")
	}
}

func TestPreviewAndDeleteLegacyRuntimeSystemMessagesIntegration(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	pool := openRuntimeCleanupTestPool(t, ctx)
	t.Cleanup(pool.Close)

	suffix := fmt.Sprintf("%d", time.Now().UnixNano())
	name := "Runtime Cleanup Test " + suffix
	email := "runtime-cleanup-" + suffix + "@multica.test"
	slug := "runtime-cleanup-" + suffix
	cleanupRuntimeCleanupFixture(t, ctx, pool, email, slug)
	t.Cleanup(func() {
		cleanupRuntimeCleanupFixture(t, context.Background(), pool, email, slug)
	})

	var userID string
	if err := pool.QueryRow(ctx, `
		INSERT INTO "user" (name, email)
		VALUES ($1, $2)
		RETURNING id
	`, name, email).Scan(&userID); err != nil {
		t.Fatalf("insert user: %v", err)
	}

	var workspaceID string
	if err := pool.QueryRow(ctx, `
		INSERT INTO workspace (name, slug, description, issue_prefix)
		VALUES ('Runtime Cleanup Test', $1, 'cleanup test fixture', 'RCL')
		RETURNING id
	`, slug).Scan(&workspaceID); err != nil {
		t.Fatalf("insert workspace: %v", err)
	}
	if _, err := pool.Exec(ctx, `
		INSERT INTO member (workspace_id, user_id, role)
		VALUES ($1, $2, 'owner')
	`, workspaceID, userID); err != nil {
		t.Fatalf("insert workspace owner: %v", err)
	}

	var channelID string
	if err := pool.QueryRow(ctx, `
		INSERT INTO channel (workspace_id, name, description, created_by)
		VALUES ($1, $2, 'cleanup test channel', $3)
		RETURNING id
	`, workspaceID, "cleanup-"+suffix, userID).Scan(&channelID); err != nil {
		t.Fatalf("insert channel: %v", err)
	}

	candidateID := insertRuntimeCleanupMessage(t, ctx, pool, channelID, workspaceID, "system", "本地守护进程已过期，需要更新。", "", "")
	quotedCandidateID := insertRuntimeCleanupMessage(t, ctx, pool, channelID, workspaceID, "system", "daemon_outdated", "", "")
	rootCandidateID := insertRuntimeCleanupMessage(t, ctx, pool, channelID, workspaceID, "system", "no_reply", "", "")
	_ = candidateID
	quoteID := insertRuntimeCleanupMessage(t, ctx, pool, channelID, workspaceID, "user", "this message quotes the stale notice", quotedCandidateID, "")
	replyID := insertRuntimeCleanupMessage(t, ctx, pool, channelID, workspaceID, "agent", "thread reply should cascade only when allowed", "", rootCandidateID)
	normalSystemID := insertRuntimeCleanupMessage(t, ctx, pool, channelID, workspaceID, "system", "Alice joined the channel", "", "")
	userMentionID := insertRuntimeCleanupMessage(t, ctx, pool, channelID, workspaceID, "user", "daemon_outdated", "", "")
	larkNoticeID := insertRuntimeCleanupMessageWithSourceParts(t, ctx, pool, channelID, workspaceID, "system", "daemon_outdated", "lark", "[]", "", "")
	structuredNoticeID := insertRuntimeCleanupMessageWithSourceParts(t, ctx, pool, channelID, workspaceID, "system", "daemon_outdated", "multica", `[{"type":"text","text":"daemon_outdated"}]`, "", "")

	preview, err := PreviewLegacyRuntimeSystemMessages(ctx, pool, CleanupOptions{WorkspaceID: workspaceID, SampleLimit: 10})
	if err != nil {
		t.Fatalf("preview: %v", err)
	}
	if preview.Matched != 3 {
		t.Fatalf("preview matched = %d, want 3", preview.Matched)
	}
	if preview.ThreadRootsWithReplies != 1 {
		t.Fatalf("thread roots with replies = %d, want 1", preview.ThreadRootsWithReplies)
	}
	if preview.QuotedByMessages != 1 {
		t.Fatalf("quoted by messages = %d, want 1", preview.QuotedByMessages)
	}

	_, err = DeleteLegacyRuntimeSystemMessages(ctx, pool, CleanupOptions{WorkspaceID: workspaceID})
	if err == nil || !strings.Contains(err.Error(), "thread roots with replies") {
		t.Fatalf("delete without thread cascade err = %v, want thread root refusal", err)
	}

	_, err = DeleteLegacyRuntimeSystemMessages(ctx, pool, CleanupOptions{WorkspaceID: workspaceID, AllowThreadCascade: true, MaxDelete: 2})
	if err == nil || !strings.Contains(err.Error(), "--max-delete=2") {
		t.Fatalf("delete above max err = %v, want max-delete refusal", err)
	}

	deleted, err := DeleteLegacyRuntimeSystemMessages(ctx, pool, CleanupOptions{WorkspaceID: workspaceID, AllowThreadCascade: true, MaxDelete: 3})
	if err != nil {
		t.Fatalf("delete with cascade: %v", err)
	}
	if deleted != 3 {
		t.Fatalf("deleted = %d, want 3", deleted)
	}

	after, err := PreviewLegacyRuntimeSystemMessages(ctx, pool, CleanupOptions{WorkspaceID: workspaceID})
	if err != nil {
		t.Fatalf("preview after delete: %v", err)
	}
	if after.Matched != 0 {
		t.Fatalf("after matched = %d, want 0", after.Matched)
	}
	assertRuntimeCleanupMessageExists(t, ctx, pool, normalSystemID)
	assertRuntimeCleanupMessageExists(t, ctx, pool, userMentionID)
	assertRuntimeCleanupMessageExists(t, ctx, pool, larkNoticeID)
	assertRuntimeCleanupMessageExists(t, ctx, pool, structuredNoticeID)
	assertRuntimeCleanupMessageExists(t, ctx, pool, quoteID)
	assertRuntimeCleanupMessageDeleted(t, ctx, pool, replyID)

	var quoteTarget *string
	if err := pool.QueryRow(ctx, `SELECT reply_to_message_id::text FROM channel_message WHERE id = $1`, quoteID).Scan(&quoteTarget); err != nil {
		t.Fatalf("query quote target: %v", err)
	}
	if quoteTarget != nil {
		t.Fatalf("quote target = %v, want nil after quoted candidate delete", *quoteTarget)
	}
}

func openRuntimeCleanupTestPool(t *testing.T, ctx context.Context) *pgxpool.Pool {
	t.Helper()

	dbURL := os.Getenv("DATABASE_URL")
	if dbURL == "" {
		dbURL = "postgres://multica:multica@localhost:5432/multica?sslmode=disable"
	}
	pool, err := pgxpool.New(ctx, dbURL)
	if err != nil {
		t.Skipf("database unavailable: %v", err)
	}
	if err := pool.Ping(ctx); err != nil {
		pool.Close()
		t.Skipf("database unreachable: %v", err)
	}
	return pool
}

func cleanupRuntimeCleanupFixture(t *testing.T, ctx context.Context, pool *pgxpool.Pool, email, slug string) {
	t.Helper()
	if _, err := pool.Exec(ctx, `DELETE FROM workspace WHERE slug = $1`, slug); err != nil {
		t.Fatalf("cleanup workspace: %v", err)
	}
	if _, err := pool.Exec(ctx, `DELETE FROM "user" WHERE email = $1`, email); err != nil {
		t.Fatalf("cleanup user: %v", err)
	}
}

func insertRuntimeCleanupMessage(t *testing.T, ctx context.Context, pool *pgxpool.Pool, channelID, workspaceID, authorType, content, replyToID, threadRootID string) string {
	return insertRuntimeCleanupMessageWithSourceParts(t, ctx, pool, channelID, workspaceID, authorType, content, "multica", "[]", replyToID, threadRootID)
}

func insertRuntimeCleanupMessageWithSourceParts(t *testing.T, ctx context.Context, pool *pgxpool.Pool, channelID, workspaceID, authorType, content, source, parts, replyToID, threadRootID string) string {
	t.Helper()
	var replyTo any
	if replyToID != "" {
		replyTo = replyToID
	}
	var threadRoot any
	var threadID any
	if threadRootID != "" {
		threadRoot = threadRootID
		threadID = "cleanup-thread"
	}
	var id string
	if err := pool.QueryRow(ctx, `
		INSERT INTO channel_message (channel_id, workspace_id, author_type, author_name, content, source, parts, reply_to_message_id, thread_root_message_id, thread_id)
		VALUES ($1, $2, $3, 'Runtime Cleanup Test', $4, $5, $6::jsonb, $7, $8, $9)
		RETURNING id
	`, channelID, workspaceID, authorType, content, source, parts, replyTo, threadRoot, threadID).Scan(&id); err != nil {
		t.Fatalf("insert %s message %q: %v", authorType, content, err)
	}
	return id
}

func assertRuntimeCleanupMessageExists(t *testing.T, ctx context.Context, pool *pgxpool.Pool, id string) {
	t.Helper()
	var exists bool
	if err := pool.QueryRow(ctx, `SELECT EXISTS (SELECT 1 FROM channel_message WHERE id = $1)`, id).Scan(&exists); err != nil {
		t.Fatalf("check message exists: %v", err)
	}
	if !exists {
		t.Fatalf("message %s should still exist", id)
	}
}

func assertRuntimeCleanupMessageDeleted(t *testing.T, ctx context.Context, pool *pgxpool.Pool, id string) {
	t.Helper()
	var exists bool
	if err := pool.QueryRow(ctx, `SELECT EXISTS (SELECT 1 FROM channel_message WHERE id = $1)`, id).Scan(&exists); err != nil {
		t.Fatalf("check message deleted: %v", err)
	}
	if exists {
		t.Fatalf("message %s should have been deleted", id)
	}
}
