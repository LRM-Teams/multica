package handler

import (
	"encoding/json"
	"net/http"
)

// CreateSweLegoIssueRequest is the body of POST /api/v1/swe-lego/issues.
// See the design spec §4.1 for field semantics.
type CreateSweLegoIssueRequest struct {
	RepoURL            string   `json:"repo_url"`
	BaseCommit         string   `json:"base_commit"`
	IssueDate          string   `json:"issue_date"`
	IssueText          string   `json:"issue_text"`
	IssueTitle         string   `json:"issue_title"`
	AcceptanceCriteria string   `json:"acceptance_criteria"`
	FailToPass         []string `json:"fail_to_pass"`
	PassToPass         []string `json:"pass_to_pass"`
	GroupSize          int      `json:"group_size"`
	AgentConfigID      string   `json:"agent_config_id"`
	BaseImage          string   `json:"base_image,omitempty"`
}

// CreateSweLegoIssueResponse is the 201 response (spec §4.1).
type CreateSweLegoIssueResponse struct {
	ProjectID            string   `json:"project_id"`
	IssueID              string   `json:"issue_id"`
	ImageID              string   `json:"image_id"`
	BuildNodeID          string   `json:"build_node_id"`
	BaseSandboxID        string   `json:"base_sandbox_id"`
	BaseSandboxRuntimeID string   `json:"base_sandbox_runtime_id"`
	AgentRunIDs          []string `json:"agent_run_ids"`
}

// CreateSweLegoIssue handles POST /api/v1/swe-lego/issues.
//
// Atomic orchestration: CreateProject + image build + base sandbox boot +
// group_size forks + agent-run enqueue. Either returns 201 with all IDs or
// rolls back (spec §4.1 atomicity contract). The handler is thin; the
// orchestration lives in service.NewSweLegoIssueService (wired in Task 9).
func (h *Handler) CreateSweLegoIssue(w http.ResponseWriter, r *http.Request) {
	userID, ok := requireUserID(w, r)
	if !ok {
		return
	}
	_ = userID // auth-gated; the service layer receives workspace from context

	var req CreateSweLegoIssueRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "malformed request body")
		return
	}
	if req.RepoURL == "" || req.BaseCommit == "" || req.IssueDate == "" {
		writeError(w, http.StatusBadRequest, "repo_url, base_commit, and issue_date are required")
		return
	}
	if req.GroupSize < 1 {
		writeError(w, http.StatusBadRequest, "group_size must be >= 1")
		return
	}

	// The service is wired in Task 9. For now, surface a 501 so the route
	// exists and the auth/body-validation tests pass; Task 9 replaces this
	// body with the real orchestration call.
	writeError(w, http.StatusNotImplemented, "swe-lego issue orchestration not yet wired")
}
