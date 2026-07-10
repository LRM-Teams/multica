package radar

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/jackc/pgx/v5"
)

const fileSectionMaxBytes = 32 * 1024

type DBTX interface {
	Query(ctx context.Context, sql string, args ...any) (pgx.Rows, error)
}

type ContextBuilder struct {
	db             DBTX
	workspacesRoot string
}

type Context struct {
	Markdown string
}

func NewContextBuilder(db DBTX, workspacesRoot string) *ContextBuilder {
	return &ContextBuilder{db: db, workspacesRoot: workspacesRoot}
}

func (b *ContextBuilder) Build(ctx context.Context, workspaceID, agentID string) (Context, error) {
	var out strings.Builder
	out.WriteString("# Agent Radar Context\n\n")
	if b.workspacesRoot != "" {
		root := filepath.Join(b.workspacesRoot, workspaceID, ".multica", "agents", agentID)
		for _, section := range []struct {
			title string
			path  string
		}{
			{"Agent Plan", "notes/agent-plan.md"},
			{"Memory", "memory/MEMORY.md"},
			{"State", "memory/STATE.md"},
			{"Work Log", "notes/work-log.md"},
		} {
			appendFileSection(&out, root, section.title, section.path)
		}
	}
	if b.db != nil {
		if err := b.appendDBSections(ctx, &out, workspaceID, agentID); err != nil {
			return Context{}, err
		}
	}
	return Context{Markdown: out.String()}, nil
}

func appendFileSection(out *strings.Builder, root, title, rel string) {
	data, err := os.ReadFile(filepath.Join(root, filepath.FromSlash(rel)))
	if err != nil {
		return
	}
	if len(data) > fileSectionMaxBytes {
		data = data[:fileSectionMaxBytes]
	}
	fmt.Fprintf(out, "## %s\n\n", title)
	out.WriteString(strings.TrimSpace(string(data)))
	out.WriteString("\n\n")
}

func (b *ContextBuilder) appendDBSections(ctx context.Context, out *strings.Builder, workspaceID, agentID string) error {
	if err := appendRows(ctx, out, b.db, "Assigned Issues", `
		SELECT w.issue_prefix || '-' || i.number::text || ' ' || i.title || ' [' || i.status || ']'
		FROM issue i
		JOIN workspace w ON w.id = i.workspace_id
		WHERE i.workspace_id = $1::uuid
		  AND i.assignee_type = 'agent'
		  AND i.assignee_id = $2::uuid
		  AND i.status NOT IN ('done', 'cancelled')
		ORDER BY i.updated_at DESC
		LIMIT 10
	`, workspaceID, agentID); err != nil {
		return err
	}
	if err := appendRows(ctx, out, b.db, "Recent Failed Tasks", `
		SELECT left(id::text, 8) || ' status=' || status || ' reason=' || coalesce(failure_reason, '') || ' summary=' || coalesce(trigger_summary, '')
		FROM agent_task_queue
		WHERE agent_id = $1::uuid
		  AND status = 'failed'
		ORDER BY updated_at DESC
		LIMIT 10
	`, agentID); err != nil {
		return err
	}
	if err := appendRows(ctx, out, b.db, "Recent Channel Discussion", `
		SELECT c.name || ': ' || cm.author_name || ' - ' || left(cm.content, 240)
		FROM channel_message cm
		JOIN channel c ON c.id = cm.channel_id
		JOIN channel_member m ON m.channel_id = c.id AND m.workspace_id = c.workspace_id
		WHERE c.workspace_id = $1::uuid
		  AND m.member_type = 'agent'
		  AND m.member_id = $2::uuid
		  AND cm.deleted_at IS NULL
		ORDER BY cm.created_at DESC
		LIMIT 20
	`, workspaceID, agentID); err != nil {
		return err
	}
	return appendRows(ctx, out, b.db, "GitHub Repositories", `
		SELECT p.title || ': ' || pr.resource_ref->>'url'
		FROM project_resource pr
		JOIN project p ON p.id = pr.project_id
		WHERE p.workspace_id = $1::uuid
		  AND pr.resource_type = 'github_repo'
		ORDER BY pr.created_at DESC
		LIMIT 10
	`, workspaceID)
}

func appendRows(ctx context.Context, out *strings.Builder, db DBTX, title, query string, args ...any) error {
	rows, err := db.Query(ctx, query, args...)
	if err != nil {
		return err
	}
	defer rows.Close()
	var lines []string
	for rows.Next() {
		var line string
		if err := rows.Scan(&line); err != nil {
			return err
		}
		line = strings.TrimSpace(line)
		if line != "" {
			lines = append(lines, line)
		}
	}
	if err := rows.Err(); err != nil {
		return err
	}
	if len(lines) == 0 {
		return nil
	}
	fmt.Fprintf(out, "## %s\n\n", title)
	for _, line := range lines {
		fmt.Fprintf(out, "- %s\n", line)
	}
	out.WriteString("\n")
	return nil
}
