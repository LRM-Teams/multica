package handler

import (
	"fmt"
	"net/http"
	"strconv"
	"strings"

	"github.com/go-chi/chi/v5"
	"github.com/jackc/pgx/v5/pgtype"
	db "github.com/multica-ai/multica/server/pkg/db/generated"
)

// ChannelIssuesResponse is the group Tasks projection. It deliberately
// contains the same issue records as the global Issues API: a group is a
// discussion context, not a second issue owner or a copied task list.
type ChannelIssuesResponse struct {
	Issues []IssueResponse `json:"issues"`
	Total  int64           `json:"total"`
}

// ListChannelSourceIssues returns the issues that were created from a message
// in this group channel. `issue_source_message` remains provenance/return-link
// state; this endpoint is only its group-local read projection.
func (h *Handler) ListChannelSourceIssues(w http.ResponseWriter, r *http.Request) {
	userID, ok := requireUserID(w, r)
	if !ok {
		return
	}
	ctx := r.Context()
	workspaceID := ctxWorkspaceID(ctx)
	workspaceUUID := parseUUID(workspaceID)
	channelID, ok := parseUUIDOrBadRequest(w, chi.URLParam(r, "channelId"), "channel id")
	if !ok {
		return
	}
	if !h.requireChannelUserMember(w, ctx, workspaceID, channelID, parseUUID(userID)) {
		return
	}
	if !h.requireGroupChannel(w, ctx, workspaceID, channelID) {
		return
	}

	limit, offset := 100, 0
	if raw := r.URL.Query().Get("limit"); raw != "" {
		if value, err := strconv.Atoi(raw); err == nil && value > 0 {
			limit = value
		}
	}
	if limit > 100 {
		limit = 100
	}
	if raw := r.URL.Query().Get("offset"); raw != "" {
		if value, err := strconv.Atoi(raw); err == nil && value >= 0 {
			offset = value
		}
	}

	where := []string{
		"i.workspace_id = $1",
		"src.workspace_id = $1",
		"src.channel_id = $2",
	}
	args := []any{workspaceUUID, channelID}
	addArg := func(value any) string {
		args = append(args, value)
		return "$" + strconv.Itoa(len(args))
	}
	if status := r.URL.Query().Get("status"); status != "" {
		where = append(where, fmt.Sprintf("i.status = %s", addArg(status)))
	}
	if assignee := r.URL.Query().Get("assignee_id"); assignee != "" {
		assigneeID, valid := parseUUIDOrBadRequest(w, assignee, "assignee_id")
		if !valid {
			return
		}
		where = append(where, fmt.Sprintf("i.assignee_id = %s::uuid", addArg(assigneeID)))
	}
	whereSQL := strings.Join(where, " AND ")

	countQuery := fmt.Sprintf(`SELECT COUNT(*)
		FROM issue i
		JOIN issue_source_message src ON src.issue_id = i.id
		WHERE %s`, whereSQL)
	var total int64
	if err := h.DB.QueryRow(ctx, countQuery, args...).Scan(&total); err != nil {
		writeError(w, http.StatusInternalServerError, "failed to count channel issues")
		return
	}

	queryArgs := append(append([]any{}, args...), int64(limit), int64(offset))
	query := fmt.Sprintf(`SELECT i.id, i.workspace_id, i.title, i.description, i.status, i.priority,
	       i.assignee_type, i.assignee_id, i.creator_type, i.creator_id,
	       i.parent_issue_id, i.position, i.start_date, i.due_date, i.created_at, i.updated_at,
	       i.number, i.project_id, i.metadata
		FROM issue i
		JOIN issue_source_message src ON src.issue_id = i.id
		WHERE %s
		ORDER BY i.position ASC, i.created_at DESC
		LIMIT $%d OFFSET $%d`, whereSQL, len(args)+1, len(args)+2)
	rows, err := h.DB.Query(ctx, query, queryArgs...)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "failed to list channel issues")
		return
	}
	defer rows.Close()

	issueRows := make([]db.ListIssuesRow, 0)
	for rows.Next() {
		var issue db.ListIssuesRow
		if err := rows.Scan(
			&issue.ID, &issue.WorkspaceID, &issue.Title, &issue.Description,
			&issue.Status, &issue.Priority, &issue.AssigneeType, &issue.AssigneeID,
			&issue.CreatorType, &issue.CreatorID, &issue.ParentIssueID, &issue.Position,
			&issue.StartDate, &issue.DueDate, &issue.CreatedAt, &issue.UpdatedAt,
			&issue.Number, &issue.ProjectID, &issue.Metadata,
		); err != nil {
			writeError(w, http.StatusInternalServerError, "failed to read channel issues")
			return
		}
		issueRows = append(issueRows, issue)
	}
	if err := rows.Err(); err != nil {
		writeError(w, http.StatusInternalServerError, "failed to list channel issues")
		return
	}

	issueIDs := make([]pgtype.UUID, len(issueRows))
	for i, issue := range issueRows {
		issueIDs[i] = issue.ID
	}
	labelsByIssue := h.labelsByIssue(ctx, workspaceUUID, issueIDs)
	prefix := h.getIssuePrefix(ctx, workspaceUUID)
	issues := make([]IssueResponse, 0, len(issueRows))
	for _, issue := range issueRows {
		response := issueListRowToResponse(issue, prefix)
		labels := labelsByIssue[response.ID]
		if labels == nil {
			labels = []LabelResponse{}
		}
		response.Labels = &labels
		issues = append(issues, response)
	}
	writeJSON(w, http.StatusOK, ChannelIssuesResponse{Issues: issues, Total: total})
}
