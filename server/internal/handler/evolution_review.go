package handler

import (
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"strconv"
	"strings"

	"github.com/go-chi/chi/v5"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/multica-ai/multica/server/internal/service"
	db "github.com/multica-ai/multica/server/pkg/db/generated"
)

type EvolutionReviewSubmissionResponse struct {
	ID               string                        `json:"id"`
	WorkspaceID      string                        `json:"workspace_id"`
	SourceAgentID    string                        `json:"source_agent_id"`
	SourceMemberID   string                        `json:"source_member_id,omitempty"`
	UnitType         string                        `json:"unit_type"`
	LocalUnitID      string                        `json:"local_unit_id"`
	Title            string                        `json:"title"`
	Summary          string                        `json:"summary"`
	Content          string                        `json:"content,omitempty"`
	ContentHash      string                        `json:"content_hash"`
	BundleHash       string                        `json:"bundle_hash"`
	BundleRef        string                        `json:"bundle_ref"`
	Sensitivity      string                        `json:"sensitivity"`
	Confidence       string                        `json:"confidence"`
	SuggestedScope   string                        `json:"suggested_scope"`
	Tags             []string                      `json:"tags"`
	Tools            []string                      `json:"tools"`
	TaskTypes        []string                      `json:"task_types"`
	ProjectTypes     []string                      `json:"project_types"`
	Languages        []string                      `json:"languages"`
	Frameworks       []string                      `json:"frameworks"`
	Status           string                        `json:"status"`
	RejectReason     string                        `json:"reject_reason"`
	ReviewDecision   string                        `json:"review_decision"`
	ReviewConfidence *float64                      `json:"review_confidence,omitempty"`
	ReviewRiskLevel  string                        `json:"review_risk_level"`
	ReviewReason     string                        `json:"review_reason"`
	ReviewMetadata   map[string]any                `json:"review_metadata"`
	ReviewedAt       *string                       `json:"reviewed_at,omitempty"`
	PromotedUnitID   *string                       `json:"promoted_unit_id,omitempty"`
	SourceCreatedAt  *string                       `json:"source_created_at,omitempty"`
	CreatedAt        *string                       `json:"created_at,omitempty"`
	UpdatedAt        *string                       `json:"updated_at,omitempty"`
	Files            []EvolutionReviewFileResponse `json:"files,omitempty"`
}

type EvolutionReviewFileResponse struct {
	ID          string  `json:"id"`
	Path        string  `json:"path"`
	Content     string  `json:"content,omitempty"`
	ContentHash string  `json:"content_hash"`
	MimeType    string  `json:"mime_type"`
	SizeBytes   int64   `json:"size_bytes"`
	CreatedAt   *string `json:"created_at,omitempty"`
}

type evolutionReviewDecisionRequest struct {
	Reason                 string `json:"reason"`
	ApplyReviewSuggestions bool   `json:"apply_review_suggestions"`
}

func (h *Handler) ListEvolutionReviewSubmissions(w http.ResponseWriter, r *http.Request) {
	workspaceID, wsUUID, ok := evolutionReviewWorkspace(w, r)
	if !ok {
		return
	}
	status := strings.TrimSpace(r.URL.Query().Get("status"))
	if status == "" {
		status = "needs_review"
	}
	if status != "needs_review" && status != "rejected" && status != "promoted" && status != "candidate" {
		writeError(w, http.StatusBadRequest, "invalid status")
		return
	}
	limit := int32(50)
	if raw := strings.TrimSpace(r.URL.Query().Get("limit")); raw != "" {
		parsed, err := strconv.Atoi(raw)
		if err != nil || parsed <= 0 {
			writeError(w, http.StatusBadRequest, "invalid limit")
			return
		}
		if parsed > 200 {
			parsed = 200
		}
		limit = int32(parsed)
	}
	items, err := h.Queries.ListEvolutionSubmissionsForReview(r.Context(), db.ListEvolutionSubmissionsForReviewParams{WorkspaceID: wsUUID, Status: status, LimitCount: limit})
	if err != nil {
		writeError(w, http.StatusInternalServerError, "failed to list evolution submissions")
		return
	}
	resp := make([]EvolutionReviewSubmissionResponse, 0, len(items))
	for _, item := range items {
		resp = append(resp, evolutionReviewSubmissionResponse(item, nil))
	}
	_ = workspaceID
	writeJSON(w, http.StatusOK, resp)
}

func (h *Handler) GetEvolutionReviewSubmission(w http.ResponseWriter, r *http.Request) {
	_, wsUUID, ok := evolutionReviewWorkspace(w, r)
	if !ok {
		return
	}
	submission, submissionID, ok := h.loadEvolutionReviewSubmission(w, r, wsUUID)
	if !ok {
		return
	}
	files, err := h.Queries.ListEvolutionSubmissionFiles(r.Context(), db.ListEvolutionSubmissionFilesParams{WorkspaceID: wsUUID, SubmissionID: submissionID})
	if err != nil {
		writeError(w, http.StatusInternalServerError, "failed to list evolution submission files")
		return
	}
	writeJSON(w, http.StatusOK, evolutionReviewSubmissionResponse(submission, files))
}

func (h *Handler) PromoteEvolutionReviewSubmission(w http.ResponseWriter, r *http.Request) {
	_, wsUUID, ok := evolutionReviewWorkspace(w, r)
	if !ok {
		return
	}
	submissionID, ok := parseUUIDOrBadRequest(w, chi.URLParam(r, "submissionId"), "submission id")
	if !ok {
		return
	}
	req, ok := decodeEvolutionReviewDecisionRequest(w, r)
	if !ok {
		return
	}
	unit, err := service.NewEvolutionService(h.Queries).PromoteSubmissionFromReview(r.Context(), wsUUID, submissionID, service.PromoteSubmissionReviewOptions{
		Reason:                 req.Reason,
		ApplyReviewSuggestions: req.ApplyReviewSuggestions,
	})
	if err != nil {
		handleEvolutionReviewDecisionError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"status": "promoted", "unit_id": uuidToString(unit.ID)})
}

func (h *Handler) RejectEvolutionReviewSubmission(w http.ResponseWriter, r *http.Request) {
	_, wsUUID, ok := evolutionReviewWorkspace(w, r)
	if !ok {
		return
	}
	submissionID, ok := parseUUIDOrBadRequest(w, chi.URLParam(r, "submissionId"), "submission id")
	if !ok {
		return
	}
	req, ok := decodeEvolutionReviewDecisionRequest(w, r)
	if !ok {
		return
	}
	submission, err := service.NewEvolutionService(h.Queries).RejectSubmissionFromReview(r.Context(), wsUUID, submissionID, req.Reason)
	if err != nil {
		handleEvolutionReviewDecisionError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, evolutionReviewSubmissionResponse(submission, nil))
}

func evolutionReviewWorkspace(w http.ResponseWriter, r *http.Request) (string, pgtype.UUID, bool) {
	workspaceID := ctxWorkspaceID(r.Context())
	if workspaceID == "" {
		workspaceID = r.URL.Query().Get("workspace_id")
	}
	wsUUID, ok := parseUUIDOrBadRequest(w, workspaceID, "workspace_id")
	return workspaceID, wsUUID, ok
}

func (h *Handler) loadEvolutionReviewSubmission(w http.ResponseWriter, r *http.Request, workspaceID pgtype.UUID) (db.EvolutionUnitSubmission, pgtype.UUID, bool) {
	submissionID, ok := parseUUIDOrBadRequest(w, chi.URLParam(r, "submissionId"), "submission id")
	if !ok {
		return db.EvolutionUnitSubmission{}, pgtype.UUID{}, false
	}
	submission, err := h.Queries.GetEvolutionUnitSubmissionInWorkspace(r.Context(), db.GetEvolutionUnitSubmissionInWorkspaceParams{ID: submissionID, WorkspaceID: workspaceID})
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			writeError(w, http.StatusNotFound, "evolution submission not found")
			return db.EvolutionUnitSubmission{}, pgtype.UUID{}, false
		}
		writeError(w, http.StatusInternalServerError, "failed to load evolution submission")
		return db.EvolutionUnitSubmission{}, pgtype.UUID{}, false
	}
	return submission, submissionID, true
}

func decodeEvolutionReviewDecisionRequest(w http.ResponseWriter, r *http.Request) (evolutionReviewDecisionRequest, bool) {
	var req evolutionReviewDecisionRequest
	if r.Body != nil {
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil && !errors.Is(err, io.EOF) {
			writeError(w, http.StatusBadRequest, "invalid request body")
			return evolutionReviewDecisionRequest{}, false
		}
	}
	req.Reason = strings.TrimSpace(req.Reason)
	return req, true
}

func handleEvolutionReviewDecisionError(w http.ResponseWriter, err error) {
	switch {
	case errors.Is(err, pgx.ErrNoRows):
		writeError(w, http.StatusNotFound, "evolution submission not found")
	case errors.Is(err, service.ErrEvolutionSubmissionNotReviewable):
		writeError(w, http.StatusConflict, "evolution submission is not awaiting review")
	default:
		writeError(w, http.StatusInternalServerError, "failed to update evolution submission")
	}
}

func evolutionReviewSubmissionResponse(submission db.EvolutionUnitSubmission, files []db.EvolutionUnitSubmissionFile) EvolutionReviewSubmissionResponse {
	metadata := map[string]any{}
	if len(submission.ReviewMetadata) > 0 {
		_ = json.Unmarshal(submission.ReviewMetadata, &metadata)
	}
	resp := EvolutionReviewSubmissionResponse{
		ID:              uuidToString(submission.ID),
		WorkspaceID:     uuidToString(submission.WorkspaceID),
		SourceAgentID:   uuidToString(submission.SourceAgentID),
		SourceMemberID:  uuidToString(submission.SourceMemberID),
		UnitType:        submission.UnitType,
		LocalUnitID:     submission.LocalUnitID,
		Title:           submission.Title,
		Summary:         submission.Summary,
		Content:         submission.Content,
		ContentHash:     submission.ContentHash,
		BundleHash:      submission.BundleHash,
		BundleRef:       submission.BundleRef,
		Sensitivity:     submission.Sensitivity,
		Confidence:      submission.Confidence,
		SuggestedScope:  submission.SuggestedScope,
		Tags:            safeStringSlice(submission.Tags),
		Tools:           safeStringSlice(submission.Tools),
		TaskTypes:       safeStringSlice(submission.TaskTypes),
		ProjectTypes:    safeStringSlice(submission.ProjectTypes),
		Languages:       safeStringSlice(submission.Languages),
		Frameworks:      safeStringSlice(submission.Frameworks),
		Status:          submission.Status,
		RejectReason:    submission.RejectReason,
		ReviewDecision:  submission.ReviewDecision,
		ReviewRiskLevel: submission.ReviewRiskLevel,
		ReviewReason:    submission.ReviewReason,
		ReviewMetadata:  metadata,
		ReviewedAt:      timestampToPtr(submission.ReviewedAt),
		PromotedUnitID:  uuidToPtr(submission.PromotedUnitID),
		SourceCreatedAt: timestampToPtr(submission.SourceCreatedAt),
		CreatedAt:       timestampToPtr(submission.CreatedAt),
		UpdatedAt:       timestampToPtr(submission.UpdatedAt),
	}
	if submission.ReviewConfidence.Valid {
		resp.ReviewConfidence = &submission.ReviewConfidence.Float64
	}
	if files != nil {
		resp.Files = make([]EvolutionReviewFileResponse, 0, len(files))
		for _, file := range files {
			resp.Files = append(resp.Files, EvolutionReviewFileResponse{
				ID:          uuidToString(file.ID),
				Path:        file.Path,
				Content:     file.Content,
				ContentHash: file.ContentHash,
				MimeType:    file.MimeType,
				SizeBytes:   file.SizeBytes,
				CreatedAt:   timestampToPtr(file.CreatedAt),
			})
		}
	}
	return resp
}

func safeStringSlice(values []string) []string {
	if values == nil {
		return []string{}
	}
	return values
}
