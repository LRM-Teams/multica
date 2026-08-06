package handler

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/multica-ai/multica/server/internal/logger"
	"github.com/multica-ai/multica/server/internal/middleware"
	"github.com/multica-ai/multica/server/internal/service"
	db "github.com/multica-ai/multica/server/pkg/db/generated"
)

type EvolutionReviewSubmissionResponse struct {
	ID                string                              `json:"id"`
	WorkspaceID       string                              `json:"workspace_id"`
	SourceAgentID     string                              `json:"source_agent_id"`
	SourceMemberID    string                              `json:"source_member_id,omitempty"`
	UnitType          string                              `json:"unit_type"`
	LocalUnitID       string                              `json:"local_unit_id"`
	Title             string                              `json:"title"`
	Summary           string                              `json:"summary"`
	Content           string                              `json:"content,omitempty"`
	ContentHash       string                              `json:"content_hash"`
	BundleHash        string                              `json:"bundle_hash"`
	BundleRef         string                              `json:"bundle_ref"`
	Sensitivity       string                              `json:"sensitivity"`
	Confidence        string                              `json:"confidence"`
	SuggestedScope    string                              `json:"suggested_scope"`
	Evidence          EvolutionReviewEvidenceResponse     `json:"evidence"`
	Applies           EvolutionReviewAppliesResponse      `json:"applies"`
	Tags              []string                            `json:"tags"`
	Tools             []string                            `json:"tools"`
	TaskTypes         []string                            `json:"task_types"`
	ProjectTypes      []string                            `json:"project_types"`
	Languages         []string                            `json:"languages"`
	Frameworks        []string                            `json:"frameworks"`
	Status            string                              `json:"status"`
	RejectReason      string                              `json:"reject_reason"`
	ReviewDecision    string                              `json:"review_decision"`
	ReviewConfidence  *float64                            `json:"review_confidence,omitempty"`
	ReviewRiskLevel   string                              `json:"review_risk_level"`
	ReviewReason      string                              `json:"review_reason"`
	ReviewMetadata    map[string]any                      `json:"review_metadata"`
	ReviewedAt        *string                             `json:"reviewed_at,omitempty"`
	PromotedUnitID    *string                             `json:"promoted_unit_id,omitempty"`
	MaterializedSkill *EvolutionMaterializedSkillResponse `json:"materialized_skill,omitempty"`
	SourceCreatedAt   *string                             `json:"source_created_at,omitempty"`
	CreatedAt         *string                             `json:"created_at,omitempty"`
	UpdatedAt         *string                             `json:"updated_at,omitempty"`
	Files             []EvolutionReviewFileResponse       `json:"files,omitempty"`
}

type EvolutionReviewEvidenceResponse struct {
	Source       string   `json:"source,omitempty"`
	SourceDate   string   `json:"source_date,omitempty"`
	EvidenceRefs []string `json:"evidence_refs"`
}

type EvolutionReviewAppliesResponse struct {
	Scope        string   `json:"scope,omitempty"`
	Tags         []string `json:"tags"`
	Tools        []string `json:"tools"`
	TaskTypes    []string `json:"task_types"`
	ProjectTypes []string `json:"project_types"`
	Languages    []string `json:"languages"`
	Frameworks   []string `json:"frameworks"`
}

type EvolutionMaterializedSkillResponse struct {
	ID          string `json:"id"`
	Name        string `json:"name"`
	Description string `json:"description"`
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

type evolutionSourceSkillRequest struct {
	Enabled bool `json:"enabled"`
}

type EvolutionCandidateRerunResponse struct {
	SubmissionID string  `json:"submission_id"`
	Status       string  `json:"status"`
	UnitID       *string `json:"unit_id,omitempty"`
	Matched      bool    `json:"matched"`
	Idempotent   bool    `json:"idempotent"`
}

const evolutionCandidateRerunActivity = "evolution_candidate_rerun"

type evolutionCandidateRerunAudit struct {
	SubmissionID       string                          `json:"submission_id"`
	IdempotencyKeyHash string                          `json:"idempotency_key_hash"`
	Response           EvolutionCandidateRerunResponse `json:"response"`
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
	resp := evolutionReviewSubmissionResponse(submission, files)
	if submission.PromotedUnitID.Valid && submission.UnitType == "skill" {
		skill, err := h.Queries.GetSkillBySourceEvolutionUnit(r.Context(), db.GetSkillBySourceEvolutionUnitParams{
			WorkspaceID:           wsUUID,
			SourceEvolutionUnitID: submission.PromotedUnitID,
		})
		if err != nil && !errors.Is(err, pgx.ErrNoRows) {
			writeError(w, http.StatusInternalServerError, "failed to load materialized skill")
			return
		}
		if err == nil {
			resp.MaterializedSkill = &EvolutionMaterializedSkillResponse{
				ID:          uuidToString(skill.ID),
				Name:        skill.Name,
				Description: skill.Description,
			}
		}
	}
	writeJSON(w, http.StatusOK, resp)
}

// RerunEvolutionCandidate re-evaluates one candidate under a per-submission
// transaction lock. A successful audit row is the idempotency record, so the
// state transition and its retriable response commit together.
func (h *Handler) RerunEvolutionCandidate(w http.ResponseWriter, r *http.Request) {
	workspaceID, wsUUID, ok := evolutionReviewWorkspace(w, r)
	if !ok {
		return
	}
	member, ok := middleware.MemberFromContext(r.Context())
	if !ok || !roleAllowed(member.Role, "owner", "admin") {
		writeError(w, http.StatusForbidden, "insufficient permissions")
		return
	}
	actorType, _ := h.resolveActor(r, requestUserID(r), workspaceID)
	if actorType == "agent" {
		writeError(w, http.StatusForbidden, "agents may not rerun evolution candidates")
		return
	}
	submissionID, ok := parseUUIDOrBadRequest(w, chi.URLParam(r, "submissionId"), "submission id")
	if !ok {
		return
	}
	idempotencyKey := strings.TrimSpace(r.Header.Get("Idempotency-Key"))
	if idempotencyKey == "" {
		writeError(w, http.StatusBadRequest, "Idempotency-Key is required")
		return
	}
	if len(idempotencyKey) > 128 {
		writeError(w, http.StatusBadRequest, "Idempotency-Key is too long")
		return
	}

	keyHash, auditID := evolutionCandidateRerunIdentity(workspaceID, uuidToString(submissionID), idempotencyKey)
	if response, found, err := h.loadEvolutionCandidateRerun(r.Context(), wsUUID, submissionID, keyHash, auditID); err != nil {
		writeError(w, http.StatusConflict, "Idempotency-Key is already bound to another operation")
		return
	} else if found {
		response.Idempotent = true
		writeJSON(w, http.StatusOK, response)
		return
	}

	hook := func(ctx context.Context, tx pgx.Tx, result service.EvolutionCandidateRerunResult) error {
		response := EvolutionCandidateRerunResponse{
			SubmissionID: uuidToString(submissionID),
			Status:       result.Status,
			UnitID:       uuidToPtr(result.UnitID),
			Matched:      result.Matched,
		}
		details, _ := json.Marshal(evolutionCandidateRerunAudit{SubmissionID: response.SubmissionID, IdempotencyKeyHash: keyHash, Response: response})
		_, err := tx.Exec(ctx, `
			INSERT INTO activity_log (id, workspace_id, actor_type, actor_id, action, details)
			VALUES ($1, $2, 'member', $3, $4, $5)
		`, auditID, wsUUID, member.UserID, evolutionCandidateRerunActivity, details)
		return err
	}
	result, err := service.NewEvolutionServiceWithReviewerAndTx(h.Queries, h.TxStarter, h.cfg.EvolutionReviewer, h.cfg.EvolutionReviewEnabled).RerunCandidate(r.Context(), wsUUID, submissionID, hook)
	if errors.Is(err, service.ErrEvolutionCandidateClaimed) {
		for range 20 {
			time.Sleep(50 * time.Millisecond)
			response, found, replayErr := h.loadEvolutionCandidateRerun(r.Context(), wsUUID, submissionID, keyHash, auditID)
			if replayErr != nil {
				writeError(w, http.StatusConflict, "Idempotency-Key is already bound to another operation")
				return
			}
			if found {
				response.Idempotent = true
				writeJSON(w, http.StatusOK, response)
				return
			}
		}
	}
	if err != nil {
		if response, found, replayErr := h.loadEvolutionCandidateRerun(r.Context(), wsUUID, submissionID, keyHash, auditID); replayErr == nil && found {
			response.Idempotent = true
			writeJSON(w, http.StatusOK, response)
			return
		}
		switch {
		case errors.Is(err, pgx.ErrNoRows):
			writeError(w, http.StatusNotFound, "evolution submission not found")
		case errors.Is(err, service.ErrEvolutionSubmissionNotCandidate), errors.Is(err, service.ErrEvolutionCandidateClaimed), errors.Is(err, service.ErrEvolutionCandidateChanged):
			writeError(w, http.StatusConflict, err.Error())
		default:
			slog.Error("evolution candidate rerun failed", append(logger.RequestAttrs(r), "error", err, "submission_id", uuidToString(submissionID))...)
			writeError(w, http.StatusInternalServerError, "failed to rerun evolution candidate")
		}
		return
	}
	writeJSON(w, http.StatusOK, EvolutionCandidateRerunResponse{SubmissionID: uuidToString(submissionID), Status: result.Status, UnitID: uuidToPtr(result.UnitID), Matched: result.Matched})
}

func evolutionCandidateRerunIdentity(workspaceID, submissionID, key string) (string, pgtype.UUID) {
	keyDigest := sha256.Sum256([]byte("multica:evolution-candidate-rerun:idempotency-key:v1\x00" + key))
	keyHash := "sha256:" + hex.EncodeToString(keyDigest[:])
	recordDigest := sha256.Sum256([]byte("multica:evolution-candidate-rerun:audit-id:v1\x00" + workspaceID + "\x00" + submissionID + "\x00" + keyHash))
	var id pgtype.UUID
	copy(id.Bytes[:], recordDigest[:16])
	id.Bytes[6] = (id.Bytes[6] & 0x0f) | 0x50
	id.Bytes[8] = (id.Bytes[8] & 0x3f) | 0x80
	id.Valid = true
	return keyHash, id
}

func (h *Handler) loadEvolutionCandidateRerun(ctx context.Context, workspaceID, submissionID pgtype.UUID, keyHash string, auditID pgtype.UUID) (EvolutionCandidateRerunResponse, bool, error) {
	activity, err := h.Queries.GetActivity(ctx, auditID)
	if errors.Is(err, pgx.ErrNoRows) {
		return EvolutionCandidateRerunResponse{}, false, nil
	}
	if err != nil {
		return EvolutionCandidateRerunResponse{}, false, err
	}
	var audit evolutionCandidateRerunAudit
	if activity.WorkspaceID != workspaceID || activity.Action != evolutionCandidateRerunActivity || json.Unmarshal(activity.Details, &audit) != nil || audit.SubmissionID != uuidToString(submissionID) || audit.IdempotencyKeyHash != keyHash {
		return EvolutionCandidateRerunResponse{}, false, errors.New("idempotency audit collision")
	}
	return audit.Response, true, nil
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
	unit, err := service.NewTransactionalEvolutionService(h.Queries, h.TxStarter).PromoteSubmissionFromReview(r.Context(), wsUUID, submissionID, service.PromoteSubmissionReviewOptions{
		Reason:                 req.Reason,
		ApplyReviewSuggestions: req.ApplyReviewSuggestions,
	})
	if err != nil {
		handleEvolutionReviewDecisionError(w, err)
		return
	}
	if submission, loadErr := h.Queries.GetEvolutionUnitSubmissionInWorkspace(
		r.Context(),
		db.GetEvolutionUnitSubmissionInWorkspaceParams{ID: submissionID, WorkspaceID: wsUUID},
	); loadErr == nil && submission.SourceAgentID.Valid {
		h.refreshAgentHonor(r.Context(), wsUUID, submission.SourceAgentID, "evolution_promoted")
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
	submission, err := service.NewTransactionalEvolutionService(h.Queries, h.TxStarter).RejectSubmissionFromReview(r.Context(), wsUUID, submissionID, req.Reason)
	if err != nil {
		handleEvolutionReviewDecisionError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, evolutionReviewSubmissionResponse(submission, nil))
}

func (h *Handler) SetEvolutionSourceSkillAssignment(w http.ResponseWriter, r *http.Request) {
	_, workspaceID, ok := evolutionReviewWorkspace(w, r)
	if !ok {
		return
	}
	submission, _, ok := h.loadEvolutionReviewSubmission(w, r, workspaceID)
	if !ok {
		return
	}
	if submission.Status != "promoted" || submission.UnitType != "skill" || !submission.SourceAgentID.Valid || !submission.PromotedUnitID.Valid {
		writeError(w, http.StatusConflict, "evolution skill is not materialized")
		return
	}
	var req evolutionSourceSkillRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid request body")
		return
	}
	skill, err := h.Queries.GetSkillBySourceEvolutionUnit(r.Context(), db.GetSkillBySourceEvolutionUnitParams{
		WorkspaceID:           workspaceID,
		SourceEvolutionUnitID: submission.PromotedUnitID,
	})
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			writeError(w, http.StatusNotFound, "materialized skill not found")
			return
		}
		writeError(w, http.StatusInternalServerError, "failed to load materialized skill")
		return
	}
	result := "unchanged"
	if req.Enabled {
		tag, execErr := h.DB.Exec(r.Context(), `
			INSERT INTO agent_skill (agent_id, skill_id, source)
			VALUES ($1, $2, 'evolution')
			ON CONFLICT (agent_id, skill_id) DO NOTHING
		`, submission.SourceAgentID, skill.ID)
		err = execErr
		if err == nil && tag.RowsAffected() == 1 {
			result = "enabled"
		} else if err == nil {
			result = "already_assigned"
		}
	} else {
		var source string
		var deleted bool
		err = h.DB.QueryRow(r.Context(), `
			WITH deleted AS (
				DELETE FROM agent_skill
				WHERE agent_id = $1 AND skill_id = $2 AND source = 'evolution'
				RETURNING source
			), existing AS (
				SELECT source FROM agent_skill WHERE agent_id = $1 AND skill_id = $2
			)
			SELECT COALESCE((SELECT source FROM deleted), (SELECT source FROM existing), ''),
			       EXISTS(SELECT 1 FROM deleted)
		`, submission.SourceAgentID, skill.ID).Scan(&source, &deleted)
		switch {
		case err != nil:
		case deleted:
			result = "disabled"
		case source != "":
			result = "preserved_non_evolution"
		default:
			result = "already_unassigned"
		}
	}
	if err != nil {
		writeError(w, http.StatusInternalServerError, "failed to update evolution skill assignment")
		return
	}
	actualEnabled := result != "disabled" && result != "already_unassigned"
	writeJSON(w, http.StatusOK, map[string]any{"enabled": actualEnabled, "result": result})
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
	metadata := jsonMap(submission.ReviewMetadata)
	evidence := safeEvolutionReviewEvidence(submission.Evidence)
	applies := safeEvolutionReviewApplies(submission)
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
		Evidence:        evidence,
		Applies:         applies,
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

func safeEvolutionReviewEvidence(raw []byte) EvolutionReviewEvidenceResponse {
	value := jsonMap(raw)
	return EvolutionReviewEvidenceResponse{
		Source:       safeEvidenceSource(value["source"]),
		SourceDate:   safeJSONScalar(value["source_date"], 40),
		EvidenceRefs: safeEvidenceRefs(value["evidence_refs"]),
	}
}

func safeEvolutionReviewApplies(submission db.EvolutionUnitSubmission) EvolutionReviewAppliesResponse {
	value := jsonMap(submission.Applies)
	return EvolutionReviewAppliesResponse{
		Scope:        safeJSONScalar(value["scope"], 80),
		Tags:         safeStringSlice(submission.Tags),
		Tools:        safeStringSlice(submission.Tools),
		TaskTypes:    safeStringSlice(submission.TaskTypes),
		ProjectTypes: safeStringSlice(submission.ProjectTypes),
		Languages:    safeStringSlice(submission.Languages),
		Frameworks:   safeStringSlice(submission.Frameworks),
	}
}

func safeEvidenceSource(value any) string {
	source := safeJSONScalar(value, 80)
	switch source {
	case "memory_curation_l3", "memory_curation_l2", "runtime", "curator", "manual", "system":
		return source
	default:
		return ""
	}
}

func safeEvidenceRefs(value any) []string {
	items, ok := value.([]any)
	if !ok {
		return []string{}
	}
	refs := make([]string, 0, len(items))
	for _, item := range items {
		ref, ok := item.(string)
		if !ok {
			continue
		}
		parts := strings.SplitN(strings.TrimSpace(ref), ":", 2)
		if len(parts) != 2 || parts[1] == "" {
			continue
		}
		switch parts[0] {
		case "dm_message":
			// DM identifiers could be used to cross participant boundaries.
			continue
		case "channel_message", "issue", "comment", "task", "activity", "evolution_feedback":
			refs = append(refs, parts[0])
		}
		if len(refs) == 20 {
			break
		}
	}
	return refs
}

func safeJSONScalar(value any, limit int) string {
	text, ok := value.(string)
	if !ok {
		return ""
	}
	return truncateReviewValue(strings.TrimSpace(text), limit)
}

func truncateReviewValue(value string, limit int) string {
	if len(value) <= limit {
		return value
	}
	return value[:limit]
}

func jsonMap(raw []byte) map[string]any {
	value := map[string]any{}
	if len(raw) > 0 {
		_ = json.Unmarshal(raw, &value)
	}
	return value
}

func safeStringSlice(values []string) []string {
	if values == nil {
		return []string{}
	}
	return values
}
