package handler

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"strings"
	"time"

	"github.com/jackc/pgx/v5/pgtype"
	"github.com/multica-ai/multica/server/internal/radar"
	"github.com/multica-ai/multica/server/internal/service"
	"github.com/multica-ai/multica/server/internal/util"
	"github.com/multica-ai/multica/server/internal/workgraph"
	db "github.com/multica-ai/multica/server/pkg/db/generated"
)

const (
	wendyAmbientDispatchLimit      = int32(5)
	beckhamContextModeCoordination = "coordination"
)

func (h *Handler) DispatchDueWendyAmbientReviews(ctx context.Context, limit int32) (int, error) {
	if h.WorkGraph == nil || h.TaskService == nil || limit <= 0 {
		return 0, nil
	}
	if limit > wendyAmbientDispatchLimit {
		limit = wendyAmbientDispatchLimit
	}
	// Re-arm reviews whose run never completed (daemon gone, crash) so a review
	// is not lost forever (#2).
	if reclaimed, err := h.WorkGraph.ReclaimStaleChannelAmbient(ctx); err != nil {
		slog.Warn("Wendy ambient stale reclaim failed", "error", err)
	} else if reclaimed > 0 {
		slog.Info("Wendy ambient reclaimed stale reviews", "count", reclaimed)
	}
	watches, claimToken, err := h.WorkGraph.ClaimDueChannelAmbient(ctx, limit)
	if err != nil {
		return 0, err
	}
	enqueued := 0
	for _, watch := range watches {
		ok, err := h.dispatchClaimedWendyAmbient(ctx, watch, claimToken)
		if err != nil {
			slog.Warn("Wendy ambient dispatch failed",
				"channel_id", uuidToString(watch.ChannelID),
				"error", err,
			)
			_ = h.WorkGraph.ReturnChannelAmbientForRetry(ctx, watch.ChannelID, claimToken)
			continue
		}
		if ok {
			enqueued++
		}
	}
	return enqueued, nil
}

func (h *Handler) dispatchClaimedWendyAmbient(ctx context.Context, watch workgraph.ChannelAmbientWatch, claimToken pgtype.UUID) (bool, error) {
	channel, found := h.getChannel(ctx, uuidToString(watch.WorkspaceID), watch.ChannelID)
	if !found || channel.Kind != "group" || channel.ArchivedAt != nil {
		return false, h.WorkGraph.CancelChannelAmbientClaim(ctx, watch.ChannelID, claimToken, "channel unavailable")
	}
	if !h.channelHasAgentMember(ctx, watch.WorkspaceID, watch.ChannelID, watch.WendyAgentID) {
		return false, h.WorkGraph.CancelChannelAmbientClaim(ctx, watch.ChannelID, claimToken, "Wendy left channel")
	}
	supervisor, err := h.Queries.GetAgentInWorkspace(ctx, db.GetAgentInWorkspaceParams{
		ID:          watch.WendyAgentID,
		WorkspaceID: watch.WorkspaceID,
	})
	if err != nil || supervisor.ArchivedAt.Valid || !supervisor.RuntimeID.Valid {
		return false, h.WorkGraph.CancelChannelAmbientClaim(ctx, watch.ChannelID, claimToken, "Wendy unavailable")
	}
	// Opportunistically bring an existing Beckham's persona/avatar up to date.
	supervisor = h.refreshGroupManagerIfStale(ctx, supervisor)

	markdown, err := h.buildWendyAmbientChannelMarkdown(ctx, watch, channel)
	if err != nil {
		return false, err
	}
	prompt := radar.BuildAmbientChannelPrompt(markdown)
	triggerRef := fmt.Sprintf("ambient:%s:%d", uuidToString(watch.ChannelID), watch.LastHumanMessageAt.Unix())
	run, _, err := h.TaskService.EnqueueAgentRadarRun(ctx, service.EnqueueAgentRadarRunParams{
		WorkspaceID:    watch.WorkspaceID,
		AgentID:        watch.WendyAgentID,
		ChannelID:      watch.ChannelID,
		ProjectID:      ambientChannelProjectID(channel),
		ContextMode:    beckhamContextModeCoordination,
		TriggerKind:    "event",
		TriggerRef:     triggerRef,
		CooldownKey:    "wendy_ambient:" + uuidToString(watch.ChannelID),
		ContextSummary: "Beckham ambient review for channel " + channel.Name,
		ScheduledFor:   time.Now().UTC(),
		Prompt:         prompt,
	})
	if err != nil {
		if errors.Is(err, service.ErrAgentRadarRunActive) || errors.Is(err, service.ErrAgentRadarNotReady) {
			return false, h.WorkGraph.ReturnChannelAmbientForRetry(ctx, watch.ChannelID, claimToken)
		}
		return false, err
	}
	if err := h.WorkGraph.MarkChannelAmbientRunning(ctx, watch.ChannelID, claimToken, watch.LastHumanMessageAt, run.ID); err != nil {
		return false, err
	}
	return true, nil
}

func ambientChannelProjectID(channel ChannelResponse) pgtype.UUID {
	if channel.ProjectID == nil {
		return pgtype.UUID{}
	}
	projectID, err := util.ParseUUID(*channel.ProjectID)
	if err != nil {
		return pgtype.UUID{}
	}
	return projectID
}

func (h *Handler) buildWendyAmbientChannelMarkdown(ctx context.Context, watch workgraph.ChannelAmbientWatch, channel ChannelResponse) (string, error) {
	var b strings.Builder
	projectID := ambientChannelProjectID(channel)
	fmt.Fprintf(&b, "## Channel\n\n- channel_id=%s\n- name=%s\n- context_mode=%s\n", channel.ID, channel.Name, beckhamContextModeCoordination)
	if projectID.Valid {
		fmt.Fprintf(&b, "- project_id=%s\n\n", uuidToString(projectID))
	} else {
		b.WriteString("- project_id=(unbound)\n\n")
	}

	b.WriteString("## Project\n\n")
	if !projectID.Valid {
		b.WriteString("- (channel is not bound to a project; issue sections use a workspace fallback)\n")
	} else {
		project, err := h.Queries.GetProjectInWorkspace(ctx, db.GetProjectInWorkspaceParams{
			ID:          projectID,
			WorkspaceID: watch.WorkspaceID,
		})
		if err != nil {
			return "", fmt.Errorf("load ambient project: %w", err)
		}
		fmt.Fprintf(&b, "- project_id=%s title=%q status=%s", uuidToString(project.ID), project.Title, project.Status)
		if project.LeadType.Valid || project.LeadID.Valid {
			fmt.Fprintf(&b, " lead_type=%s lead_id=%s", project.LeadType.String, uuidToString(project.LeadID))
		}
		b.WriteString("\n")
		if project.Description.Valid && strings.TrimSpace(project.Description.String) != "" {
			fmt.Fprintf(&b, "- description=%q\n", trimAmbientContent(project.Description.String))
		}

		b.WriteString("\n## Project Resources\n\n")
		resources := h.listProjectResourcesForProject(ctx, projectID)
		if len(resources) == 0 {
			b.WriteString("- (none)\n")
		}
		for _, resource := range resources {
			label := ""
			if resource.Label.Valid {
				label = resource.Label.String
			}
			fmt.Fprintf(&b, "- resource_id=%s type=%s label=%q resource_ref=%s\n",
				uuidToString(resource.ID), resource.ResourceType, label, trimAmbientJSON(resource.ResourceRef))
		}
	}

	b.WriteString("\n")
	b.WriteString("## Open Work Nodes In Channel\n\n")
	rows, err := h.DB.Query(ctx, `
		SELECT node.id, node.title, node.status, node.owner_type, node.owner_id, node.linked_issue_id
		FROM work_node node
		LEFT JOIN issue linked_issue
		  ON linked_issue.id = node.linked_issue_id
		 AND linked_issue.workspace_id = node.workspace_id
		WHERE node.workspace_id = $1
		  AND node.primary_channel_id = $2
		  AND node.status NOT IN ('done', 'cancelled')
		  AND (
		    $3::uuid IS NULL
		    OR node.linked_issue_id IS NULL
		    OR linked_issue.project_id = $3
		  )
		ORDER BY node.updated_at DESC
		LIMIT 30
	`, watch.WorkspaceID, watch.ChannelID, projectID)
	if err != nil {
		return "", fmt.Errorf("list ambient work nodes: %w", err)
	}
	nodeCount := 0
	for rows.Next() {
		var id, ownerID, issueID pgtype.UUID
		var title, status, ownerType string
		if err := rows.Scan(&id, &title, &status, &ownerType, &ownerID, &issueID); err != nil {
			rows.Close()
			return "", err
		}
		fmt.Fprintf(&b, "- work_node_id=%s title=%q status=%s owner_type=%s owner_id=%s linked_issue_id=%s\n",
			uuidToString(id), title, status, ownerType, uuidToString(ownerID), uuidToString(issueID))
		nodeCount++
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return "", err
	}
	if nodeCount == 0 {
		b.WriteString("- (none)\n")
	}

	b.WriteString("\n## Channel Agents\n\n")
	agentRows, err := h.DB.Query(ctx, `
		SELECT a.id, a.name
		FROM channel_member cm
		JOIN agent a ON a.id = cm.member_id AND a.workspace_id = cm.workspace_id
		WHERE cm.workspace_id = $1
		  AND cm.channel_id = $2
		  AND cm.member_type = 'agent'
		  AND a.archived_at IS NULL
		ORDER BY a.name ASC
		LIMIT 50
	`, watch.WorkspaceID, watch.ChannelID)
	if err != nil {
		return "", fmt.Errorf("list ambient channel agents: %w", err)
	}
	ladder := h.channelNudgeLadder(ctx, watch.ChannelID)
	for agentRows.Next() {
		var id pgtype.UUID
		var name string
		if err := agentRows.Scan(&id, &name); err != nil {
			agentRows.Close()
			return "", err
		}
		if n := ladder[uuidToString(id)]; n > 0 {
			fmt.Fprintf(&b, "- agent_id=%s name=%q nudged_without_progress=%d\n", uuidToString(id), name, n)
		} else {
			fmt.Fprintf(&b, "- agent_id=%s name=%q\n", uuidToString(id), name)
		}
	}
	agentRows.Close()
	if err := agentRows.Err(); err != nil {
		return "", err
	}

	// Human members — so escalation to a person (@a human owner) can target a real
	// member when an agent has been nudged repeatedly without progress.
	b.WriteString("\n## Human Members\n\n")
	humanRows, err := h.DB.Query(ctx, `
		SELECT u.id, COALESCE(NULLIF(u.display_name, ''), u.name, u.email, '')
		FROM channel_member cm
		JOIN "user" u ON u.id = cm.member_id
		WHERE cm.workspace_id = $1 AND cm.channel_id = $2 AND cm.member_type = 'user'
		ORDER BY u.created_at ASC
		LIMIT 50
	`, watch.WorkspaceID, watch.ChannelID)
	if err != nil {
		return "", fmt.Errorf("list ambient channel humans: %w", err)
	}
	humanCount := 0
	for humanRows.Next() {
		var id pgtype.UUID
		var name string
		if err := humanRows.Scan(&id, &name); err != nil {
			humanRows.Close()
			return "", err
		}
		fmt.Fprintf(&b, "- member_id=%s name=%q\n", uuidToString(id), name)
		humanCount++
	}
	humanRows.Close()
	if err := humanRows.Err(); err != nil {
		return "", err
	}
	if humanCount == 0 {
		b.WriteString("- (none)\n")
	}

	issueScopeLabel := "Project"
	issueScopePredicate := "AND i.project_id = $2"
	issueArgs := []any{watch.WorkspaceID, projectID}
	if !projectID.Valid {
		issueScopeLabel = "Workspace Fallback"
		issueScopePredicate = ""
		issueArgs = []any{watch.WorkspaceID}
	}
	b.WriteString("\n## Open " + issueScopeLabel + " Issues\n\n")
	// Issue-backed work_nodes carry no primary_channel_id. Surface the bound
	// project's issues with enough contract data to review and target them. An
	// unbound channel keeps the historical workspace-wide fallback.
	issueRows, err := h.DB.Query(ctx, `
		SELECT i.id, w.issue_prefix, i.number, i.title, COALESCE(i.description, ''), i.status,
		       COALESCE(i.assignee_type, '') AS assignee_type, i.assignee_id,
		       COALESCE(a.name, '') AS agent_name, i.parent_issue_id,
		       i.acceptance_criteria,
		       COALESCE((
		         SELECT jsonb_agg(jsonb_build_object(
		           'type', dep_link.type,
		           'issue_id', dependency.id,
		           'identifier', w.issue_prefix || '-' || dependency.number::text,
		           'status', dependency.status
		         ) ORDER BY dependency.number)
		         FROM issue_dependency dep_link
		         JOIN issue dependency ON dependency.id = dep_link.depends_on_issue_id
		         WHERE dep_link.issue_id = i.id
		       ), '[]'::jsonb) AS dependencies,
		       COALESCE(latest_comment.content, '') AS latest_comment_content,
		       COALESCE(latest_comment.type, '') AS latest_comment_type,
		       COALESCE(latest_comment.author_type, '') AS latest_comment_author_type,
		       latest_comment.author_id AS latest_comment_author_id,
		       latest_comment.created_at AS latest_comment_at,
		       i.updated_at
		FROM issue i
		JOIN workspace w ON w.id = i.workspace_id
		LEFT JOIN agent a
		  ON a.id = i.assignee_id
		 AND i.assignee_type = 'agent'
		LEFT JOIN LATERAL (
		  SELECT c.content, c.type, c.author_type, c.author_id, c.created_at
		  FROM comment c
		  WHERE c.issue_id = i.id
		  ORDER BY c.created_at DESC, c.id DESC
		  LIMIT 1
		) latest_comment ON true
		WHERE i.workspace_id = $1
		  `+issueScopePredicate+`
		  AND i.status NOT IN ('done', 'cancelled', 'closed')
		ORDER BY i.updated_at DESC
		LIMIT 30
	`, issueArgs...)
	if err != nil {
		b.WriteString("- (issues unavailable)\n")
		slog.Warn("ambient markdown: list open issues failed", "workspace_id", uuidToString(watch.WorkspaceID), "error", err)
	} else {
		issueCount := 0
		for issueRows.Next() {
			var number int64
			var issueID, assigneeID, parentIssueID, latestCommentAuthorID pgtype.UUID
			var title, description, status, assigneeType, agentName, issuePrefix string
			var latestComment, latestCommentType, latestCommentAuthorType string
			var acceptanceCriteria, dependencies []byte
			var latestCommentAt pgtype.Timestamptz
			var updatedAt time.Time
			if err := issueRows.Scan(
				&issueID, &issuePrefix, &number, &title, &description, &status,
				&assigneeType, &assigneeID, &agentName, &parentIssueID,
				&acceptanceCriteria, &dependencies,
				&latestComment, &latestCommentType, &latestCommentAuthorType, &latestCommentAuthorID, &latestCommentAt,
				&updatedAt,
			); err != nil {
				issueRows.Close()
				return "", err
			}
			assignee := "unassigned"
			if assigneeType == "agent" && agentName != "" {
				assignee = "agent:" + agentName
			} else if assigneeType != "" && assigneeID.Valid {
				assignee = assigneeType + ":" + uuidToString(assigneeID)
			}
			fmt.Fprintf(&b, "- issue_id=%s identifier=%s-%d title=%q status=%s assignee=%s parent_issue_id=%s updated_at=%s\n",
				uuidToString(issueID), issuePrefix, number, trimAmbientContent(title), status, assignee,
				uuidToString(parentIssueID), updatedAt.UTC().Format(time.RFC3339))
			if strings.TrimSpace(description) != "" {
				fmt.Fprintf(&b, "  description=%q\n", trimAmbientContent(description))
			}
			fmt.Fprintf(&b, "  acceptance_criteria=%s\n", trimAmbientJSON(acceptanceCriteria))
			fmt.Fprintf(&b, "  dependencies=%s\n", trimAmbientJSON(dependencies))
			if strings.TrimSpace(latestComment) != "" {
				fmt.Fprintf(&b, "  latest_comment=%q type=%s author_type=%s author_id=%s",
					trimAmbientContent(latestComment), latestCommentType, latestCommentAuthorType, uuidToString(latestCommentAuthorID))
				if latestCommentAt.Valid {
					fmt.Fprintf(&b, " at=%s", latestCommentAt.Time.UTC().Format(time.RFC3339))
				}
				b.WriteString("\n")
			}
			issueCount++
		}
		issueRows.Close()
		if err := issueRows.Err(); err != nil {
			return "", err
		}
		if issueCount == 0 {
			b.WriteString("- (none)\n")
		}
	}

	b.WriteString("\n## Recently Completed " + issueScopeLabel + " Issues\n\n")
	doneRows, err := h.DB.Query(ctx, `
		SELECT i.id, w.issue_prefix, i.number, i.title,
		       COALESCE(i.assignee_type, '') AS assignee_type, i.assignee_id,
		       COALESCE(a.name, '') AS agent_name, i.acceptance_criteria,
		       COALESCE(latest_comment.content, '') AS latest_comment_content,
		       COALESCE(latest_comment.type, '') AS latest_comment_type,
		       COALESCE(latest_comment.author_type, '') AS latest_comment_author_type,
		       latest_comment.author_id AS latest_comment_author_id,
		       latest_comment.created_at AS latest_comment_at,
		       completed_activity.completed_at,
		       i.updated_at
		FROM issue i
		JOIN workspace w ON w.id = i.workspace_id
		LEFT JOIN agent a
		  ON a.id = i.assignee_id
		 AND i.assignee_type = 'agent'
		LEFT JOIN LATERAL (
		  SELECT c.content, c.type, c.author_type, c.author_id, c.created_at
		  FROM comment c
		  WHERE c.issue_id = i.id
		  ORDER BY c.created_at DESC, c.id DESC
		  LIMIT 1
		) latest_comment ON true
		LEFT JOIN LATERAL (
		  SELECT activity.created_at AS completed_at
		  FROM activity_log activity
		  WHERE activity.issue_id = i.id
		    AND activity.action = 'status_changed'
		    AND activity.details->>'to' = 'done'
		  ORDER BY activity.created_at DESC, activity.id DESC
		  LIMIT 1
		) completed_activity ON true
		WHERE i.workspace_id = $1
		  `+issueScopePredicate+`
		  AND i.status = 'done'
		ORDER BY i.updated_at DESC
		LIMIT 10
	`, issueArgs...)
	if err != nil {
		b.WriteString("- (recent completions unavailable)\n")
		slog.Warn("ambient markdown: list recently completed issues failed", "workspace_id", uuidToString(watch.WorkspaceID), "error", err)
	} else {
		doneCount := 0
		for doneRows.Next() {
			var number int64
			var issueID, assigneeID, latestCommentAuthorID pgtype.UUID
			var issuePrefix, title, assigneeType, agentName, latestComment, latestCommentType, latestCommentAuthorType string
			var acceptanceCriteria []byte
			var latestCommentAt, completedAt pgtype.Timestamptz
			var updatedAt time.Time
			if err := doneRows.Scan(
				&issueID, &issuePrefix, &number, &title, &assigneeType, &assigneeID,
				&agentName, &acceptanceCriteria,
				&latestComment, &latestCommentType, &latestCommentAuthorType, &latestCommentAuthorID, &latestCommentAt,
				&completedAt, &updatedAt,
			); err != nil {
				doneRows.Close()
				return "", err
			}
			assignee := "unassigned"
			if assigneeType == "agent" && agentName != "" {
				assignee = "agent:" + agentName
			} else if assigneeType != "" && assigneeID.Valid {
				assignee = assigneeType + ":" + uuidToString(assigneeID)
			}
			fmt.Fprintf(&b, "- issue_id=%s identifier=%s-%d title=%q assignee=%s",
				uuidToString(issueID), issuePrefix, number, trimAmbientContent(title), assignee)
			if completedAt.Valid {
				fmt.Fprintf(&b, " completed_at=%s", completedAt.Time.UTC().Format(time.RFC3339))
			} else {
				fmt.Fprintf(&b, " last_updated_at=%s", updatedAt.UTC().Format(time.RFC3339))
			}
			b.WriteString("\n")
			fmt.Fprintf(&b, "  acceptance_criteria=%s\n", trimAmbientJSON(acceptanceCriteria))
			if strings.TrimSpace(latestComment) != "" {
				fmt.Fprintf(&b, "  latest_comment=%q type=%s author_type=%s author_id=%s",
					trimAmbientContent(latestComment), latestCommentType, latestCommentAuthorType, uuidToString(latestCommentAuthorID))
				if latestCommentAt.Valid {
					fmt.Fprintf(&b, " at=%s", latestCommentAt.Time.UTC().Format(time.RFC3339))
				}
				b.WriteString("\n")
			}
			doneCount++
		}
		doneRows.Close()
		if err := doneRows.Err(); err != nil {
			return "", err
		}
		if doneCount == 0 {
			b.WriteString("- (none)\n")
		}
	}

	b.WriteString("\n## Recent Group Messages\n\n")
	msgRows, err := h.DB.Query(ctx, `
		SELECT id, author_type, author_id, author_name, content, created_at
		FROM channel_message
		WHERE workspace_id = $1 AND channel_id = $2
		ORDER BY created_at DESC
		LIMIT 40
	`, watch.WorkspaceID, watch.ChannelID)
	if err != nil {
		return "", fmt.Errorf("list ambient channel messages: %w", err)
	}
	type ambientMsg struct {
		id, authorID                    pgtype.UUID
		authorType, authorName, content string
		createdAt                       time.Time
	}
	var messages []ambientMsg
	for msgRows.Next() {
		var msg ambientMsg
		if err := msgRows.Scan(&msg.id, &msg.authorType, &msg.authorID, &msg.authorName, &msg.content, &msg.createdAt); err != nil {
			msgRows.Close()
			return "", err
		}
		messages = append(messages, msg)
	}
	msgRows.Close()
	if err := msgRows.Err(); err != nil {
		return "", err
	}
	for i := len(messages) - 1; i >= 0; i-- {
		msg := messages[i]
		fmt.Fprintf(&b, "- message_id=%s at=%s author_type=%s author_id=%s author_name=%q content=%q\n",
			uuidToString(msg.id), msg.createdAt.UTC().Format(time.RFC3339), msg.authorType, uuidToString(msg.authorID), msg.authorName, trimAmbientContent(msg.content))
	}
	if len(messages) == 0 {
		b.WriteString("- (none)\n")
	}
	return b.String(), nil
}

func trimAmbientContent(content string) string {
	content = strings.TrimSpace(content)
	runes := []rune(content)
	if len(runes) > 400 {
		return string(runes[:400]) + "…"
	}
	return content
}

// trimAmbientJSON keeps the prompt's structured fields syntactically valid.
// Arbitrarily cutting JSON makes the remainder look like corrupt source data
// and leaves the model unable to distinguish truncation from a broken record.
func trimAmbientJSON(raw []byte) string {
	trimmed := strings.TrimSpace(string(raw))
	if trimmed == "" {
		return "null"
	}
	if len([]rune(trimmed)) <= 400 && json.Valid([]byte(trimmed)) {
		return trimmed
	}
	preview := trimAmbientContent(trimmed)
	envelope, err := json.Marshal(map[string]any{
		"truncated": true,
		"preview":   preview,
	})
	if err != nil {
		return `{"truncated":true}`
	}
	return string(envelope)
}

func (h *Handler) touchWendyChannelAmbient(ctx context.Context, ch ChannelResponse, msg ChannelMessageResponse) {
	if h.WorkGraph == nil || ch.Kind != "group" {
		return
	}
	managerID, ok := h.resolveGroupManagerForChannel(ctx, parseUUID(ch.WorkspaceID), parseUUID(ch.ID))
	if !ok {
		return
	}
	messageAt := time.Now()
	if msg.CreatedAt != "" {
		if parsed, parseErr := time.Parse(time.RFC3339, msg.CreatedAt); parseErr == nil {
			messageAt = parsed
		} else if parsed, parseErr := time.Parse(time.RFC3339Nano, msg.CreatedAt); parseErr == nil {
			messageAt = parsed
		}
	}
	var messageID pgtype.UUID
	if msg.ID != "" {
		if parsed, parseErr := util.ParseUUID(msg.ID); parseErr == nil {
			messageID = parsed
		}
	}
	if err := h.WorkGraph.TouchChannelAmbient(ctx, parseUUID(ch.WorkspaceID), parseUUID(ch.ID), managerID, messageID, messageAt); err != nil {
		slog.Warn("touch group manager channel ambient failed", "channel_id", ch.ID, "message_id", msg.ID, "error", err)
	}
}

// resolveWendyAmbientAgentForChannel was removed: ambient watching now belongs to
// the per-group manager (Beckham) resolved via resolveGroupManagerForChannel.
