// Package avatarbackfill rewrites denormalized avatar URLs that still point at
// LocalStorage-style "/uploads/..." paths after attachments have been migrated
// to S3 (see migrate-uploads-to-s3).
//
// migrate-uploads-to-s3 only updates attachment.url. agent / user / workspace /
// channel keep a denormalized avatar_url copy; if those stay on "/uploads/",
// message APIs that join author_avatar_url keep serving broken links once the
// app runs with S3Storage (no /uploads/* static route).
package avatarbackfill

import (
	"context"
	"fmt"
	"log/slog"
	"os"
	"strings"

	"github.com/jackc/pgx/v5/pgxpool"
)

const DefaultPublicBaseURL = "https://leagent.s3.oss-cn-beijing.aliyuncs.com"

// PublicBaseURL returns S3_PUBLIC_BASE_URL with trailing slashes trimmed, or the
// Aliyun deploy default when the env var is unset.
func PublicBaseURL() string {
	base := strings.TrimRight(os.Getenv("S3_PUBLIC_BASE_URL"), "/")
	if base == "" {
		return DefaultPublicBaseURL
	}
	return base
}

// RewriteUploadsPath turns a site-relative "/uploads/<key>" avatar URL into a
// public OSS object URL. Returns ok=false when the input is not an uploads path.
func RewriteUploadsPath(publicBase, avatarURL string) (string, bool) {
	const prefix = "/uploads/"
	if !strings.HasPrefix(avatarURL, prefix) {
		return "", false
	}
	key := strings.TrimPrefix(avatarURL, prefix)
	if key == "" {
		return "", false
	}
	return strings.TrimRight(publicBase, "/") + "/" + key, true
}

// ResolveAvatarURL prefers an already-migrated attachment URL when the
// denormalized avatar is still on /uploads/; otherwise rewrites the avatar path
// against publicBase. Used by unit tests and mirrors the SQL cascade order.
func ResolveAvatarURL(publicBase, avatarURL, attachmentURL string) string {
	if strings.HasPrefix(avatarURL, "/uploads/") && attachmentURL != "" && !strings.HasPrefix(attachmentURL, "/uploads/") {
		return attachmentURL
	}
	if rewritten, ok := RewriteUploadsPath(publicBase, avatarURL); ok {
		return rewritten
	}
	return avatarURL
}

type StepResult struct {
	Name       string
	Candidates int64
	Updated    int64
}

type Result struct {
	PublicBase string
	DryRun     bool
	Steps      []StepResult
	Total      int64
}

type step struct {
	name  string
	count string
	exec  string
}

func steps(publicBase string) []step {
	base := strings.ReplaceAll(publicBase, "'", "''")
	return []step{
		{
			name: "agent from attachment",
			count: `
				SELECT count(*) FROM agent a
				JOIN attachment att ON a.avatar_attachment_id = att.id
				WHERE a.avatar_source = 'uploaded'
				  AND a.avatar_url LIKE '/uploads/%'
				  AND att.url NOT LIKE '/uploads/%'
			`,
			exec: `
				UPDATE agent a
				SET avatar_url = att.url
				FROM attachment att
				WHERE a.avatar_attachment_id = att.id
				  AND a.avatar_source = 'uploaded'
				  AND a.avatar_url LIKE '/uploads/%'
				  AND att.url NOT LIKE '/uploads/%'
			`,
		},
		{
			name:  "agent rewrite",
			count: `SELECT count(*) FROM agent WHERE avatar_url LIKE '/uploads/workspaces/%'`,
			exec: fmt.Sprintf(`
				UPDATE agent
				SET avatar_url = '%s/' || substring(avatar_url from '/uploads/(.*)')
				WHERE avatar_url LIKE '/uploads/workspaces/%%'
			`, base),
		},
		{
			name:  "user rewrite",
			count: `SELECT count(*) FROM "user" WHERE avatar_url LIKE '/uploads/workspaces/%'`,
			exec: fmt.Sprintf(`
				UPDATE "user"
				SET avatar_url = '%s/' || substring(avatar_url from '/uploads/(.*)')
				WHERE avatar_url LIKE '/uploads/workspaces/%%'
			`, base),
		},
		{
			name:  "workspace rewrite",
			count: `SELECT count(*) FROM workspace WHERE avatar_url LIKE '/uploads/workspaces/%'`,
			exec: fmt.Sprintf(`
				UPDATE workspace
				SET avatar_url = '%s/' || substring(avatar_url from '/uploads/(.*)')
				WHERE avatar_url LIKE '/uploads/workspaces/%%'
			`, base),
		},
		{
			name:  "channel rewrite",
			count: `SELECT count(*) FROM channel WHERE avatar_url LIKE '/uploads/workspaces/%'`,
			exec: fmt.Sprintf(`
				UPDATE channel
				SET avatar_url = '%s/' || substring(avatar_url from '/uploads/(.*)')
				WHERE avatar_url LIKE '/uploads/workspaces/%%'
			`, base),
		},
	}
}

// Run counts (and optionally updates) stale denormalized avatar URLs.
// Safe to re-run: only rows still starting with "/uploads/" are selected.
func Run(ctx context.Context, pool *pgxpool.Pool, publicBase string, dryRun bool) (Result, error) {
	publicBase = strings.TrimRight(publicBase, "/")
	if publicBase == "" {
		publicBase = DefaultPublicBaseURL
	}

	out := Result{PublicBase: publicBase, DryRun: dryRun}
	for _, s := range steps(publicBase) {
		var n int64
		if err := pool.QueryRow(ctx, s.count).Scan(&n); err != nil {
			return out, fmt.Errorf("count %s: %w", s.name, err)
		}
		sr := StepResult{Name: s.name, Candidates: n}
		slog.Info("avatar backfill candidates", "step", s.name, "count", n, "dry_run", dryRun)
		if n == 0 {
			out.Steps = append(out.Steps, sr)
			continue
		}
		out.Total += n
		if dryRun {
			out.Steps = append(out.Steps, sr)
			continue
		}
		tag, err := pool.Exec(ctx, s.exec)
		if err != nil {
			return out, fmt.Errorf("update %s: %w", s.name, err)
		}
		sr.Updated = tag.RowsAffected()
		slog.Info("avatar backfill updated", "step", s.name, "rows", sr.Updated)
		out.Steps = append(out.Steps, sr)
	}
	slog.Info("avatar backfill done", "total_candidates", out.Total, "dry_run", dryRun, "public_base", publicBase)
	return out, nil
}
