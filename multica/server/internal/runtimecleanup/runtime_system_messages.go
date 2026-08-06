package runtimecleanup

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

const ApplyConfirmation = "delete-legacy-runtime-system-messages"

var legacyRuntimeSystemNoticeContents = []string{
	"runtime_outdated",
	"runtime_missing",
	"runtime_disconnected",
	"no_reply",
	"daemon_outdated",
	"daemon_missing",
	"daemon_disconnected",
	"Local daemon is outdated.",
	"Install the Multica CLI and start the daemon.",
	"Local daemon is disconnected.",
	"本地守护进程已过期，需要更新。",
	"需要安装 Multica CLI 并启动守护进程。",
	"本地守护进程未连接。",
}

type CleanupOptions struct {
	WorkspaceID        string
	ChannelID          string
	SampleLimit        int
	MaxDelete          int64
	AllowThreadCascade bool
}

type Summary struct {
	Matched                  int64          `json:"matched"`
	ThreadRootsWithReplies   int64          `json:"thread_roots_with_replies"`
	QuotedByMessages         int64          `json:"quoted_by_messages"`
	MessagesWithAttachments  int64          `json:"messages_with_attachments"`
	ByContent                []ContentCount `json:"by_content"`
	ByChannel                []ChannelCount `json:"by_channel"`
	Samples                  []Sample       `json:"samples"`
	LegacyRuntimeNoticeSet   []string       `json:"legacy_runtime_notice_set"`
	WorkspaceFilterApplied   bool           `json:"workspace_filter_applied"`
	ChannelFilterApplied     bool           `json:"channel_filter_applied"`
	ThreadCascadeAllowed     bool           `json:"thread_cascade_allowed"`
	PhysicalDeleteOnApply    bool           `json:"physical_delete_on_apply"`
	ApplyConfirmationLiteral string         `json:"apply_confirmation_literal"`
}

type ContentCount struct {
	Content string `json:"content"`
	Count   int64  `json:"count"`
}

type ChannelCount struct {
	WorkspaceID   string `json:"workspace_id"`
	WorkspaceSlug string `json:"workspace_slug"`
	ChannelID     string `json:"channel_id"`
	ChannelName   string `json:"channel_name"`
	Count         int64  `json:"count"`
}

type Sample struct {
	ID               string    `json:"id"`
	WorkspaceID      string    `json:"workspace_id"`
	WorkspaceSlug    string    `json:"workspace_slug"`
	ChannelID        string    `json:"channel_id"`
	ChannelName      string    `json:"channel_name"`
	Content          string    `json:"content"`
	CreatedAt        time.Time `json:"created_at"`
	ThreadReplyCount int64     `json:"thread_reply_count"`
	QuoteCount       int64     `json:"quote_count"`
	AttachmentCount  int64     `json:"attachment_count"`
}

func LegacyRuntimeSystemNoticeContents() []string {
	contents := make([]string, len(legacyRuntimeSystemNoticeContents))
	copy(contents, legacyRuntimeSystemNoticeContents)
	return contents
}

func IsLegacyRuntimeSystemNoticeContent(content string) bool {
	trimmed := strings.TrimSpace(content)
	for _, legacy := range legacyRuntimeSystemNoticeContents {
		if trimmed == legacy {
			return true
		}
	}
	return false
}

func PreviewLegacyRuntimeSystemMessages(ctx context.Context, pool *pgxpool.Pool, opts CleanupOptions) (Summary, error) {
	normalized, err := normalizeOptions(opts)
	if err != nil {
		return Summary{}, err
	}

	args := candidateArgs(normalized)
	summary := Summary{
		ByContent:                []ContentCount{},
		ByChannel:                []ChannelCount{},
		Samples:                  []Sample{},
		LegacyRuntimeNoticeSet:   LegacyRuntimeSystemNoticeContents(),
		WorkspaceFilterApplied:   normalized.WorkspaceID != "",
		ChannelFilterApplied:     normalized.ChannelID != "",
		ThreadCascadeAllowed:     normalized.AllowThreadCascade,
		PhysicalDeleteOnApply:    true,
		ApplyConfirmationLiteral: ApplyConfirmation,
	}

	if err := pool.QueryRow(ctx, candidateSummarySQL, args...).Scan(
		&summary.Matched,
		&summary.ThreadRootsWithReplies,
		&summary.QuotedByMessages,
		&summary.MessagesWithAttachments,
	); err != nil {
		return Summary{}, fmt.Errorf("summarize legacy runtime system messages: %w", err)
	}

	contentRows, err := pool.Query(ctx, candidateContentSQL, args...)
	if err != nil {
		return Summary{}, fmt.Errorf("count legacy runtime notices by content: %w", err)
	}
	summary.ByContent, err = scanRows(contentRows, func(rows pgx.Rows) (ContentCount, error) {
		var item ContentCount
		err := rows.Scan(&item.Content, &item.Count)
		return item, err
	})
	if err != nil {
		return Summary{}, err
	}

	channelRows, err := pool.Query(ctx, candidateChannelSQL, args...)
	if err != nil {
		return Summary{}, fmt.Errorf("count legacy runtime notices by channel: %w", err)
	}
	summary.ByChannel, err = scanRows(channelRows, func(rows pgx.Rows) (ChannelCount, error) {
		var item ChannelCount
		err := rows.Scan(&item.WorkspaceID, &item.WorkspaceSlug, &item.ChannelID, &item.ChannelName, &item.Count)
		return item, err
	})
	if err != nil {
		return Summary{}, err
	}

	sampleArgs := append(args, normalized.SampleLimit)
	sampleRows, err := pool.Query(ctx, candidateSamplesSQL, sampleArgs...)
	if err != nil {
		return Summary{}, fmt.Errorf("sample legacy runtime notices: %w", err)
	}
	summary.Samples, err = scanRows(sampleRows, func(rows pgx.Rows) (Sample, error) {
		var item Sample
		err := rows.Scan(
			&item.ID,
			&item.WorkspaceID,
			&item.WorkspaceSlug,
			&item.ChannelID,
			&item.ChannelName,
			&item.Content,
			&item.CreatedAt,
			&item.ThreadReplyCount,
			&item.QuoteCount,
			&item.AttachmentCount,
		)
		return item, err
	})
	if err != nil {
		return Summary{}, err
	}

	return summary, nil
}

func DeleteLegacyRuntimeSystemMessages(ctx context.Context, pool *pgxpool.Pool, opts CleanupOptions) (int64, error) {
	normalized, err := normalizeOptions(opts)
	if err != nil {
		return 0, err
	}

	tx, err := pool.Begin(ctx)
	if err != nil {
		return 0, fmt.Errorf("begin cleanup transaction: %w", err)
	}
	defer func() {
		_ = tx.Rollback(ctx)
	}()

	if _, err := tx.Exec(ctx, `SELECT pg_advisory_xact_lock(hashtext('legacy-runtime-system-message-cleanup'))`); err != nil {
		return 0, fmt.Errorf("acquire cleanup advisory lock: %w", err)
	}
	if _, err := tx.Exec(ctx, `LOCK TABLE channel_message IN SHARE ROW EXCLUSIVE MODE`); err != nil {
		return 0, fmt.Errorf("lock channel messages for cleanup: %w", err)
	}

	args := candidateArgs(normalized)
	var matched, threadRootsWithReplies int64
	if err := tx.QueryRow(ctx, `
		WITH candidates AS (`+candidateSelectSQL+`)
		SELECT count(*), count(*) FILTER (WHERE thread_reply_count > 0)
		FROM candidates
	`, args...).Scan(&matched, &threadRootsWithReplies); err != nil {
		return 0, fmt.Errorf("check cleanup candidates: %w", err)
	}
	if matched == 0 {
		if err := tx.Commit(ctx); err != nil {
			return 0, fmt.Errorf("commit empty cleanup: %w", err)
		}
		return 0, nil
	}
	if normalized.MaxDelete > 0 && matched > normalized.MaxDelete {
		return 0, fmt.Errorf("refusing to delete %d legacy runtime system messages because --max-delete=%d", matched, normalized.MaxDelete)
	}
	if threadRootsWithReplies > 0 && !normalized.AllowThreadCascade {
		return 0, fmt.Errorf("refusing to delete %d legacy runtime system messages because %d are thread roots with replies; review dry-run samples or pass --allow-thread-cascade", matched, threadRootsWithReplies)
	}

	var deleted int64
	if err := tx.QueryRow(ctx, `
		WITH candidates AS (`+candidateSelectSQL+`),
		deleted AS (
			DELETE FROM channel_message m
			USING candidates c
			WHERE m.id = c.id
			RETURNING m.id
		)
		SELECT count(*) FROM deleted
	`, args...).Scan(&deleted); err != nil {
		return 0, fmt.Errorf("delete legacy runtime system messages: %w", err)
	}

	if err := tx.Commit(ctx); err != nil {
		return 0, fmt.Errorf("commit legacy runtime cleanup: %w", err)
	}
	return deleted, nil
}

func normalizeOptions(opts CleanupOptions) (CleanupOptions, error) {
	if opts.SampleLimit <= 0 {
		opts.SampleLimit = 20
	}
	opts.WorkspaceID = strings.TrimSpace(opts.WorkspaceID)
	opts.ChannelID = strings.TrimSpace(opts.ChannelID)
	if opts.MaxDelete < 0 {
		return CleanupOptions{}, errors.New("--max-delete must be non-negative")
	}
	return opts, nil
}

func candidateArgs(opts CleanupOptions) []any {
	return []any{
		legacyRuntimeSystemNoticeContents,
		nilIfEmpty(opts.WorkspaceID),
		nilIfEmpty(opts.ChannelID),
	}
}

func nilIfEmpty(value string) any {
	if value == "" {
		return nil
	}
	return value
}

func scanRows[T any](rows pgx.Rows, scan func(pgx.Rows) (T, error)) ([]T, error) {
	defer rows.Close()
	items := []T{}
	for rows.Next() {
		item, err := scan(rows)
		if err != nil {
			return nil, err
		}
		items = append(items, item)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	return items, nil
}

const candidateSelectSQL = `
	SELECT
		m.id,
		m.workspace_id,
		COALESCE(w.slug, '') AS workspace_slug,
		m.channel_id,
		COALESCE(c.name, '') AS channel_name,
		m.content,
		m.created_at,
		(
			SELECT count(*)
			FROM channel_message reply
			WHERE reply.thread_root_message_id = m.id
		) AS thread_reply_count,
		(
			SELECT count(*)
			FROM channel_message quote
			WHERE quote.reply_to_message_id = m.id
		) AS quote_count,
		(
			SELECT count(*)
			FROM channel_message_attachment reference
			WHERE reference.channel_message_id = m.id
		) AS attachment_count
	FROM channel_message m
	JOIN channel c ON c.id = m.channel_id
	LEFT JOIN workspace w ON w.id = m.workspace_id
	WHERE m.author_type = 'system'
	  AND m.source = 'multica'
	  AND btrim(m.content) = ANY($1::text[])
	  AND m.parts = '[]'::jsonb
	  AND ($2::uuid IS NULL OR m.workspace_id = $2::uuid)
	  AND ($3::uuid IS NULL OR m.channel_id = $3::uuid)
`

const candidateSummarySQL = `
	WITH candidates AS (` + candidateSelectSQL + `)
	SELECT
		count(*) AS matched,
		count(*) FILTER (WHERE thread_reply_count > 0) AS thread_roots_with_replies,
		COALESCE(sum(quote_count), 0) AS quoted_by_messages,
		count(*) FILTER (WHERE attachment_count > 0) AS messages_with_attachments
	FROM candidates
`

const candidateContentSQL = `
	WITH candidates AS (` + candidateSelectSQL + `)
	SELECT content, count(*)
	FROM candidates
	GROUP BY content
	ORDER BY count(*) DESC, content ASC
`

const candidateChannelSQL = `
	WITH candidates AS (` + candidateSelectSQL + `)
	SELECT workspace_id::text, workspace_slug, channel_id::text, channel_name, count(*)
	FROM candidates
	GROUP BY workspace_id, workspace_slug, channel_id, channel_name
	ORDER BY count(*) DESC, workspace_slug ASC, channel_name ASC
`

const candidateSamplesSQL = `
	WITH candidates AS (` + candidateSelectSQL + `)
	SELECT
		id::text,
		workspace_id::text,
		workspace_slug,
		channel_id::text,
		channel_name,
		content,
		created_at,
		thread_reply_count,
		quote_count,
		attachment_count
	FROM candidates
	ORDER BY created_at DESC, id DESC
	LIMIT $4
`
