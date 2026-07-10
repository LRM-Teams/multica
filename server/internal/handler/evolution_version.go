package handler

import (
	"encoding/json"
	"errors"
	"net/http"

	"github.com/go-chi/chi/v5"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/multica-ai/multica/server/internal/service"
	db "github.com/multica-ai/multica/server/pkg/db/generated"
)

type EvolutionSkillVersionResponse struct {
	ID                  string   `json:"id"`
	WorkspaceID         string   `json:"workspace_id"`
	UnitID              string   `json:"unit_id"`
	Version             int32    `json:"version"`
	Title               string   `json:"title"`
	Content             string   `json:"content,omitempty"`
	Metadata            any      `json:"metadata"`
	Applies             any      `json:"applies"`
	FailureCases        any      `json:"failure_cases"`
	SourceSubmissionIDs []string `json:"source_submission_ids"`
	ChangeReason        string   `json:"change_reason"`
	CreatedBy           string   `json:"created_by"`
	CreatedAt           *string  `json:"created_at,omitempty"`
	IsCurrent           bool     `json:"is_current"`
}

type EvolutionSkillVersionFileResponse struct {
	ID          string  `json:"id"`
	Path        string  `json:"path"`
	Content     string  `json:"content"`
	ContentHash string  `json:"content_hash"`
	MimeType    string  `json:"mime_type"`
	SizeBytes   int64   `json:"size_bytes"`
	CreatedAt   *string `json:"created_at,omitempty"`
}

type EvolutionSkillVersionDetailResponse struct {
	EvolutionSkillVersionResponse
	Files []EvolutionSkillVersionFileResponse `json:"files"`
	Eval  service.EvolutionVersionEvalSummary `json:"eval"`
}

type evolutionSkillVersionRollbackRequest struct {
	ExpectedCurrentVersionID string `json:"expected_current_version_id,omitempty"`
}

type evolutionSkillVersionRollbackResponse struct {
	Status           string `json:"status"`
	Changed          bool   `json:"changed"`
	UnitID           string `json:"unit_id"`
	CurrentVersionID string `json:"current_version_id"`
	Version          int32  `json:"version"`
	SkillID          string `json:"skill_id"`
}

func (h *Handler) ListEvolutionSkillVersions(w http.ResponseWriter, r *http.Request) {
	workspaceID, unitID, ok := evolutionSkillVersionScope(w, r)
	if !ok {
		return
	}
	items, err := service.NewEvolutionVersionService(h.DB).ListSkillVersions(r.Context(), workspaceID, unitID)
	if err != nil {
		handleEvolutionSkillVersionError(w, err)
		return
	}
	response := make([]EvolutionSkillVersionResponse, 0, len(items))
	for _, item := range items {
		response = append(response, evolutionSkillVersionResponse(item, false))
	}
	writeJSON(w, http.StatusOK, response)
}

func (h *Handler) GetEvolutionSkillVersion(w http.ResponseWriter, r *http.Request) {
	workspaceID, unitID, versionID, ok := evolutionSkillVersionIDs(w, r)
	if !ok {
		return
	}
	svc := service.NewEvolutionVersionService(h.DB)
	item, err := svc.GetSkillVersion(r.Context(), workspaceID, unitID, versionID)
	if err != nil {
		handleEvolutionSkillVersionError(w, err)
		return
	}
	files, err := svc.ListSkillVersionFiles(r.Context(), workspaceID, unitID, versionID)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "failed to load evolution skill version files")
		return
	}
	eval, err := svc.EvalSkillVersion(r.Context(), workspaceID, unitID, versionID)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "failed to evaluate evolution skill version")
		return
	}
	writeJSON(w, http.StatusOK, EvolutionSkillVersionDetailResponse{
		EvolutionSkillVersionResponse: evolutionSkillVersionResponse(item, true),
		Files:                         evolutionSkillVersionFileResponses(files),
		Eval:                          eval,
	})
}

func (h *Handler) GetEvolutionSkillVersionEval(w http.ResponseWriter, r *http.Request) {
	workspaceID, unitID, versionID, ok := evolutionSkillVersionIDs(w, r)
	if !ok {
		return
	}
	svc := service.NewEvolutionVersionService(h.DB)
	if _, err := svc.GetSkillVersion(r.Context(), workspaceID, unitID, versionID); err != nil {
		handleEvolutionSkillVersionError(w, err)
		return
	}
	eval, err := svc.EvalSkillVersion(r.Context(), workspaceID, unitID, versionID)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "failed to evaluate evolution skill version")
		return
	}
	writeJSON(w, http.StatusOK, eval)
}

func (h *Handler) RollbackEvolutionSkillVersion(w http.ResponseWriter, r *http.Request) {
	workspaceID, unitID, versionID, ok := evolutionSkillVersionIDs(w, r)
	if !ok {
		return
	}
	userID, ok := requireUserID(w, r)
	if !ok {
		return
	}
	actorUserID, ok := parseUUIDOrBadRequest(w, userID, "user id")
	if !ok {
		return
	}
	var req evolutionSkillVersionRollbackRequest
	if r.Body == nil {
		writeError(w, http.StatusBadRequest, "request body is required")
		return
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid request body")
		return
	}
	if req.ExpectedCurrentVersionID == "" {
		writeError(w, http.StatusBadRequest, "expected_current_version_id is required")
		return
	}
	expectedCurrentVersionID, parsed := parseUUIDOrBadRequest(w, req.ExpectedCurrentVersionID, "expected_current_version_id")
	if !parsed {
		return
	}

	tx, err := h.TxStarter.Begin(r.Context())
	if err != nil {
		writeError(w, http.StatusInternalServerError, "failed to begin evolution skill rollback")
		return
	}
	defer tx.Rollback(r.Context())
	result, err := service.NewEvolutionVersionService(tx).ApplySkillVersionRollback(
		r.Context(), workspaceID, unitID, versionID, expectedCurrentVersionID, actorUserID,
	)
	if err != nil {
		handleEvolutionSkillVersionError(w, err)
		return
	}
	if err := tx.Commit(r.Context()); err != nil {
		writeError(w, http.StatusInternalServerError, "failed to commit evolution skill rollback")
		return
	}
	writeJSON(w, http.StatusOK, evolutionSkillVersionRollbackResponse{
		Status:           "ok",
		Changed:          result.Changed,
		UnitID:           uuidToString(result.Unit.ID),
		CurrentVersionID: uuidToString(result.Unit.CurrentVersionID),
		Version:          result.Version.Version,
		SkillID:          uuidToString(result.Skill.ID),
	})
}

func evolutionSkillVersionScope(w http.ResponseWriter, r *http.Request) (pgtype.UUID, pgtype.UUID, bool) {
	workspaceID, ok := parseUUIDOrBadRequest(w, ctxWorkspaceID(r.Context()), "workspace_id")
	if !ok {
		return pgtype.UUID{}, pgtype.UUID{}, false
	}
	unitID, ok := parseUUIDOrBadRequest(w, chi.URLParam(r, "unitId"), "unit id")
	if !ok {
		return pgtype.UUID{}, pgtype.UUID{}, false
	}
	return workspaceID, unitID, true
}

func evolutionSkillVersionIDs(w http.ResponseWriter, r *http.Request) (pgtype.UUID, pgtype.UUID, pgtype.UUID, bool) {
	workspaceID, unitID, ok := evolutionSkillVersionScope(w, r)
	if !ok {
		return pgtype.UUID{}, pgtype.UUID{}, pgtype.UUID{}, false
	}
	versionID, ok := parseUUIDOrBadRequest(w, chi.URLParam(r, "versionId"), "version id")
	if !ok {
		return pgtype.UUID{}, pgtype.UUID{}, pgtype.UUID{}, false
	}
	return workspaceID, unitID, versionID, true
}

func evolutionSkillVersionResponse(item service.EvolutionSkillUnitVersion, includeContent bool) EvolutionSkillVersionResponse {
	metadata := map[string]any{}
	applies := map[string]any{}
	failureCases := []any{}
	_ = json.Unmarshal(item.Version.Metadata, &metadata)
	_ = json.Unmarshal(item.Version.Applies, &applies)
	_ = json.Unmarshal(item.Version.FailureCases, &failureCases)
	sourceIDs := make([]string, 0, len(item.Version.SourceSubmissionIds))
	for _, id := range item.Version.SourceSubmissionIds {
		sourceIDs = append(sourceIDs, uuidToString(id))
	}
	response := EvolutionSkillVersionResponse{
		ID:                  uuidToString(item.Version.ID),
		WorkspaceID:         uuidToString(item.Version.WorkspaceID),
		UnitID:              uuidToString(item.Version.UnitID),
		Version:             item.Version.Version,
		Title:               item.Version.Title,
		Metadata:            metadata,
		Applies:             applies,
		FailureCases:        failureCases,
		SourceSubmissionIDs: sourceIDs,
		ChangeReason:        item.Version.ChangeReason,
		CreatedBy:           item.Version.CreatedBy,
		CreatedAt:           timestampToPtr(item.Version.CreatedAt),
		IsCurrent:           item.IsCurrent,
	}
	if includeContent {
		response.Content = item.Version.Content
	}
	return response
}

func evolutionSkillVersionFileResponses(files []db.SharedEvolutionUnitFile) []EvolutionSkillVersionFileResponse {
	response := make([]EvolutionSkillVersionFileResponse, 0, len(files))
	for _, file := range files {
		response = append(response, EvolutionSkillVersionFileResponse{
			ID:          uuidToString(file.ID),
			Path:        file.Path,
			Content:     file.Content,
			ContentHash: file.ContentHash,
			MimeType:    file.MimeType,
			SizeBytes:   file.SizeBytes,
			CreatedAt:   timestampToPtr(file.CreatedAt),
		})
	}
	return response
}

func handleEvolutionSkillVersionError(w http.ResponseWriter, err error) {
	switch {
	case errors.Is(err, service.ErrEvolutionSkillUnitNotFound), errors.Is(err, service.ErrEvolutionSkillVersionNotFound):
		writeError(w, http.StatusNotFound, "evolution skill version not found")
	case errors.Is(err, service.ErrEvolutionSkillVersionConflict):
		writeError(w, http.StatusConflict, "evolution skill current version changed")
	case errors.Is(err, service.ErrEvolutionSkillNotMaterialized):
		writeError(w, http.StatusConflict, "evolution skill is not materialized")
	case errors.Is(err, service.ErrEvolutionSkillVersionIncomplete):
		writeError(w, http.StatusConflict, "evolution skill version has no SKILL.md")
	case errors.Is(err, service.ErrEvolutionSkillVersionSnapshot):
		writeError(w, http.StatusConflict, "evolution skill version has no complete matcher snapshot")
	case errors.Is(err, service.ErrEvolutionSkillMaterializedDrift):
		writeError(w, http.StatusConflict, "evolution skill materialized state conflicts with current version")
	default:
		writeError(w, http.StatusInternalServerError, "failed to manage evolution skill version")
	}
}
