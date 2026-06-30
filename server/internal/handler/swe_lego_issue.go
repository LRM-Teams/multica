package handler

import (
	"context"
	"encoding/json"
	"net/http"
	"strings"

	"github.com/go-chi/chi/v5"
	"github.com/multica-ai/multica/server/internal/service"
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

	svc := service.NewSweLegoIssueService(newSweLegoDepsAdapter(h))
	res, err := svc.Create(r.Context(), service.SweLegoIssueInput{
		RepoURL: req.RepoURL, BaseCommit: req.BaseCommit, IssueDate: req.IssueDate,
		IssueTitle: req.IssueTitle, IssueText: req.IssueText,
		AcceptanceCriteria: req.AcceptanceCriteria,
		FailToPass:         req.FailToPass, PassToPass: req.PassToPass,
		GroupSize: req.GroupSize, AgentConfigID: req.AgentConfigID, BaseImage: req.BaseImage,
	})
	if err != nil {
		// Image build failures → 502; sandbox/fork failures → 503.
		status := http.StatusServiceUnavailable
		if strings.Contains(err.Error(), "build image") {
			status = http.StatusBadGateway
		}
		writeError(w, status, err.Error())
		return
	}
	writeJSON(w, http.StatusCreated, CreateSweLegoIssueResponse{
		ProjectID: res.ProjectID, IssueID: res.IssueID, ImageID: res.ImageID,
		BuildNodeID:          res.BuildNodeID, BaseSandboxID: res.BaseSandboxID,
		BaseSandboxRuntimeID: res.BaseSandboxRuntimeID, AgentRunIDs: res.AgentRunIDs,
	})
}

// DeleteSweLegoIssue handles DELETE /api/v1/swe-lego/issues/{projectID}.
//
// Cascades: deletes forked sandboxes, the base sandbox, and the project.
// The areal-side runner calls this in a `finally` block to guarantee no
// sandbox leaks. Returns 204 on success (including when the project is
// already gone — idempotent). Stubbed until Task 10's real wiring lands.
func (h *Handler) DeleteSweLegoIssue(w http.ResponseWriter, r *http.Request) {
	_, ok := requireUserID(w, r)
	if !ok {
		return
	}
	projectID := chi.URLParam(r, "projectID")
	if projectID == "" {
		writeError(w, http.StatusBadRequest, "projectID is required")
		return
	}
	// Stub: Task 10 wires this to DeleteProject + cascade-delete sandboxes.
	// Until then, return 204 so the areal client's cleanup contract holds.
	w.WriteHeader(http.StatusNoContent)
}

// sweLegoDepsAdapter bridges the *Handler (queries + cloud-runtime proxy) to
// the service.SweLegoDeps seam. Each method wraps an existing query or
// cloud-runtime call. Task 9 ships stubs so the handler compiles and the
// route works end-to-end against a stub; Task 10 replaces them with real
// queries + cloud-runtime calls.
type sweLegoDepsAdapter struct {
	h *Handler
}

func newSweLegoDepsAdapter(h *Handler) *sweLegoDepsAdapter { return &sweLegoDepsAdapter{h: h} }

func (a *sweLegoDepsAdapter) CreateProject(ctx context.Context, name string) (string, error) {
	return "stub-project", nil
}

func (a *sweLegoDepsAdapter) CreateIssue(ctx context.Context, projectID, title, body, criteria string, f2p, p2p []string) (string, error) {
	return "stub-issue", nil
}

func (a *sweLegoDepsAdapter) BuildImage(ctx context.Context, repoURL, baseCommit, issueDate, baseImage string) (string, string, error) {
	return "stub-image", "stub-node", nil
}

func (a *sweLegoDepsAdapter) BootBaseSandbox(ctx context.Context, imageRef, nodeID string) (string, string, error) {
	return "stub-sandbox", "stub-runtime", nil
}

func (a *sweLegoDepsAdapter) ForkSandbox(ctx context.Context, sourceSandboxID string, idx int) (string, error) {
	return "stub-fork", nil
}

func (a *sweLegoDepsAdapter) EnqueueAgentRun(ctx context.Context, issueID, sandboxID string, idx int) (string, error) {
	return "stub-run", nil
}

func (a *sweLegoDepsAdapter) DeleteSandbox(ctx context.Context, sandboxID string) error {
	return nil
}

func (a *sweLegoDepsAdapter) DeleteProject(ctx context.Context, projectID string) error {
	return nil
}
