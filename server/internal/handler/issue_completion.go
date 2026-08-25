package handler

import (
	"encoding/json"
	"errors"
	"net/http"

	"github.com/go-chi/chi/v5"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/multica-ai/multica/server/internal/middleware"
	"github.com/multica-ai/multica/server/internal/service"
	"github.com/multica-ai/multica/server/internal/util"
	"github.com/multica-ai/multica/server/pkg/protocol"
)

type submitIssueCompletionRequest struct {
	ExpectedExecutionRevision *int64                               `json:"expected_execution_revision"`
	Summary                   string                               `json:"summary"`
	AcceptanceResults         []service.CompletionAcceptanceResult `json:"acceptance_results"`
	ArtifactRefs              []service.CompletionEvidenceRef      `json:"artifact_refs"`
	Risks                     []string                             `json:"risks"`
}

type reviewIssueCompletionRequest struct {
	ReportID string                           `json:"report_id"`
	Verdict  string                           `json:"verdict"`
	Reason   string                           `json:"reason"`
	Results  []service.CompletionReviewResult `json:"acceptance_results"`
}

type issueCompletionReportResponse struct {
	ID                     string                               `json:"id"`
	IssueID                string                               `json:"issue_id"`
	RunID                  string                               `json:"run_id"`
	IssueExecutionRevision int64                                `json:"issue_execution_revision"`
	SubmittedByAgentID     string                               `json:"submitted_by_agent_id"`
	Summary                string                               `json:"summary"`
	AcceptanceResults      []service.CompletionAcceptanceResult `json:"acceptance_results"`
	ArtifactRefs           []service.CompletionEvidenceRef      `json:"artifact_refs"`
	Risks                  []string                             `json:"risks"`
	VisibleCommentID       *string                              `json:"visible_comment_id,omitempty"`
	ReviewStatus           string                               `json:"review_status"`
	ReviewerType           *string                              `json:"reviewer_type,omitempty"`
	ReviewerID             *string                              `json:"reviewer_id,omitempty"`
	ReviewReason           *string                              `json:"review_reason,omitempty"`
	ReviewResults          []service.CompletionReviewResult     `json:"review_results"`
	ReviewCommentID        *string                              `json:"review_comment_id,omitempty"`
	ReviewedAt             *string                              `json:"reviewed_at,omitempty"`
	CreatedAt              string                               `json:"created_at"`
	UpdatedAt              string                               `json:"updated_at"`
}

func completionReportToResponse(report service.IssueCompletionReport) issueCompletionReportResponse {
	response := issueCompletionReportResponse{
		ID:                     uuidToString(report.ID),
		IssueID:                uuidToString(report.IssueID),
		RunID:                  uuidToString(report.RunID),
		IssueExecutionRevision: report.IssueExecutionRevision,
		SubmittedByAgentID:     uuidToString(report.SubmittedByAgentID),
		Summary:                report.Summary,
		AcceptanceResults:      []service.CompletionAcceptanceResult{},
		ArtifactRefs:           []service.CompletionEvidenceRef{},
		Risks:                  []string{},
		VisibleCommentID:       uuidToPtr(report.VisibleCommentID),
		ReviewStatus:           report.ReviewStatus,
		ReviewerType:           textToPtr(report.ReviewerType),
		ReviewerID:             uuidToPtr(report.ReviewerID),
		ReviewReason:           textToPtr(report.ReviewReason),
		ReviewResults:          []service.CompletionReviewResult{},
		ReviewCommentID:        uuidToPtr(report.ReviewCommentID),
		ReviewedAt:             timestampToPtr(report.ReviewedAt),
		CreatedAt:              timestampToString(report.CreatedAt),
		UpdatedAt:              timestampToString(report.UpdatedAt),
	}
	_ = json.Unmarshal(report.AcceptanceResults, &response.AcceptanceResults)
	_ = json.Unmarshal(report.ArtifactRefs, &response.ArtifactRefs)
	_ = json.Unmarshal(report.Risks, &response.Risks)
	_ = json.Unmarshal(report.ReviewResults, &response.ReviewResults)
	return response
}

func writeIssueCompletionError(w http.ResponseWriter, err error) {
	switch {
	case errors.Is(err, service.ErrIssueCompletionValidation):
		writeError(w, http.StatusBadRequest, err.Error())
	case errors.Is(err, service.ErrIssueCompletionForbidden):
		writeError(w, http.StatusForbidden, "this actor is not allowed to complete or review the Issue")
	case errors.Is(err, service.ErrIssueCompletionConflict):
		writeError(w, http.StatusConflict, err.Error())
	case errors.Is(err, pgx.ErrNoRows):
		writeError(w, http.StatusNotFound, "completion report not found")
	default:
		writeError(w, http.StatusInternalServerError, "failed to update Issue completion review")
	}
}

func (h *Handler) SubmitAgentIssueCompletion(w http.ResponseWriter, r *http.Request) {
	principal, ok := h.requireAgentPrincipal(w, r)
	if !ok {
		return
	}
	issue, ok := h.loadIssueForUser(w, r, chiURLParam(r, "id"))
	if !ok {
		return
	}
	var request submitIssueCompletionRequest
	if err := json.NewDecoder(r.Body).Decode(&request); err != nil || request.ExpectedExecutionRevision == nil {
		writeError(w, http.StatusBadRequest, "expected_execution_revision and a valid completion report are required")
		return
	}
	agentID, err := util.ParseUUID(principal.AgentID)
	if err != nil {
		writeError(w, http.StatusForbidden, "invalid Agent principal")
		return
	}
	runID, err := util.ParseUUID(principal.TaskID)
	if err != nil {
		writeError(w, http.StatusForbidden, "completion requires a task-scoped Agent credential")
		return
	}
	outcome, err := h.IssueExecution.SubmitCompletion(r.Context(), service.SubmitIssueCompletionInput{
		WorkspaceID: issue.WorkspaceID, IssueID: issue.ID, RunID: runID, AgentID: agentID,
		ExpectedExecutionRevision: *request.ExpectedExecutionRevision,
		Summary:                   request.Summary, AcceptanceResults: request.AcceptanceResults,
		ArtifactRefs: request.ArtifactRefs, Risks: request.Risks,
	})
	if err != nil {
		writeIssueCompletionError(w, err)
		return
	}
	status := http.StatusCreated
	if outcome.Replayed {
		status = http.StatusOK
	} else {
		h.publishIssueCompletionOutcome(r, outcome, "agent", principal.AgentID)
	}
	writeJSON(w, status, map[string]any{
		"report": completionReportToResponse(outcome.Report),
		"issue":  issueToResponse(outcome.Issue, h.getIssuePrefix(r.Context(), issue.WorkspaceID)),
	})
}

func (h *Handler) reviewIssueCompletion(w http.ResponseWriter, r *http.Request, agentPrincipal *middleware.AgentPrincipal) {
	issue, ok := h.loadIssueForUser(w, r, chiURLParam(r, "id"))
	if !ok {
		return
	}
	var request reviewIssueCompletionRequest
	if err := json.NewDecoder(r.Body).Decode(&request); err != nil {
		writeError(w, http.StatusBadRequest, "invalid review request")
		return
	}
	reportID, ok := parseUUIDOrBadRequest(w, request.ReportID, "report_id")
	if !ok {
		return
	}
	actorType := "member"
	actorID, err := util.ParseUUID(requestUserID(r))
	actorRunID := pgtype.UUID{}
	if agentPrincipal != nil {
		actorType = "agent"
		actorID, err = util.ParseUUID(agentPrincipal.AgentID)
		if err == nil {
			actorRunID, err = util.ParseUUID(agentPrincipal.TaskID)
		}
	}
	if err != nil {
		writeError(w, http.StatusForbidden, "invalid reviewer principal")
		return
	}
	outcome, err := h.IssueExecution.ReviewCompletion(r.Context(), service.ReviewIssueCompletionInput{
		WorkspaceID: issue.WorkspaceID, IssueID: issue.ID, ReportID: reportID,
		ActorType: actorType, ActorID: actorID, ActorRunID: actorRunID,
		Verdict: request.Verdict, Reason: request.Reason, Results: request.Results,
	})
	if err != nil {
		writeIssueCompletionError(w, err)
		return
	}
	if !outcome.Replayed {
		h.publishIssueCompletionOutcome(r, outcome, actorType, uuidToString(actorID))
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"report": completionReportToResponse(outcome.Report),
		"issue":  issueToResponse(outcome.Issue, h.getIssuePrefix(r.Context(), issue.WorkspaceID)),
	})
}

func (h *Handler) ReviewIssueCompletion(w http.ResponseWriter, r *http.Request) {
	h.reviewIssueCompletion(w, r, nil)
}

func (h *Handler) ReviewAgentIssueCompletion(w http.ResponseWriter, r *http.Request) {
	principal, ok := h.requireAgentPrincipal(w, r)
	if !ok {
		return
	}
	h.reviewIssueCompletion(w, r, &principal)
}

func (h *Handler) listIssueCompletionReports(w http.ResponseWriter, r *http.Request) {
	issue, ok := h.loadIssueForUser(w, r, chiURLParam(r, "id"))
	if !ok {
		return
	}
	reports, err := h.IssueExecution.ListCompletionReports(r.Context(), issue.WorkspaceID, issue.ID)
	if err != nil {
		writeIssueCompletionError(w, err)
		return
	}
	response := make([]issueCompletionReportResponse, 0, len(reports))
	for _, report := range reports {
		response = append(response, completionReportToResponse(report))
	}
	writeJSON(w, http.StatusOK, map[string]any{"reports": response})
}

func (h *Handler) ListIssueCompletionReports(w http.ResponseWriter, r *http.Request) {
	h.listIssueCompletionReports(w, r)
}

func (h *Handler) ListAgentIssueCompletionReports(w http.ResponseWriter, r *http.Request) {
	if _, ok := h.requireAgentPrincipal(w, r); !ok {
		return
	}
	h.listIssueCompletionReports(w, r)
}

func (h *Handler) publishIssueCompletionOutcome(r *http.Request, outcome service.IssueCompletionOutcome, actorType, actorID string) {
	workspaceID := uuidToString(outcome.Issue.WorkspaceID)
	comment := commentToResponse(outcome.Comment, nil, nil)
	h.publish(protocol.EventCommentCreated, workspaceID, actorType, actorID, map[string]any{
		"comment": comment, "issue_title": outcome.Issue.Title,
		"issue_assignee_type": textToPtr(outcome.Issue.AssigneeType),
		"issue_assignee_id":   uuidToPtr(outcome.Issue.AssigneeID), "issue_status": outcome.Issue.Status,
	})
	h.publish(protocol.EventIssueUpdated, workspaceID, actorType, actorID, map[string]any{
		"issue":          issueToResponse(outcome.Issue, h.getIssuePrefix(r.Context(), outcome.Issue.WorkspaceID)),
		"status_changed": true,
	})
}

// chiURLParam is isolated so the completion surface cannot accidentally trust
// a request body Issue ID over the router's canonical entity.
func chiURLParam(r *http.Request, key string) string {
	return chi.URLParam(r, key)
}
