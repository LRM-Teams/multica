package handler

import (
	"errors"
	"net/http"
	"strings"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"
	"github.com/multica-ai/multica/server/internal/researchrun"
)

type persistResearchSourceRequest struct {
	Kind                string `json:"kind"`
	ContentHash         string `json:"content_hash"`
	CapturedAt          string `json:"captured_at"`
	Locator             string `json:"locator"`
	Reason              string `json:"reason"`
	CanonicalURL        string `json:"canonical_url"`
	Title               string `json:"title"`
	Text                string `json:"text"`
	TaskID              string `json:"task_id"`
	AttemptID           string `json:"attempt_id"`
	AgentID             string `json:"agent_id"`
	UserID              string `json:"user_id"`
	AttachmentID        string `json:"attachment_id"`
	WorkspaceArtifactID string `json:"workspace_artifact_id"`
	Adapter             string `json:"adapter"`
	DatasetID           string `json:"dataset_id"`
}

func (h *Handler) PostResearchSourceIngestion(w http.ResponseWriter, r *http.Request) {
	workspaceID := h.resolveWorkspaceID(r)
	if _, ok := parseUUIDOrBadRequest(w, workspaceID, "workspace_id"); !ok {
		return
	}
	sessionID, ok := parseUUIDOrBadRequest(w, chi.URLParam(r, "id"), "id")
	if !ok {
		return
	}
	store, ok := h.ResearchRun.(researchrun.ResearchSourceIngestion)
	if !ok || store == nil {
		writeError(w, http.StatusServiceUnavailable, "research source ingestion is unavailable")
		return
	}
	var req persistResearchSourceRequest
	if !decodeResearchJSON(w, r, &req) {
		return
	}
	capturedAt, err := time.Parse(time.RFC3339, strings.TrimSpace(req.CapturedAt))
	if err != nil {
		writeError(w, http.StatusBadRequest, "captured_at must be RFC3339")
		return
	}
	result, err := store.PersistSourceIngestion(r.Context(), researchrun.PersistSourceIngestionInput{
		Intent: researchrun.SourceIngestionIntent{
			PolicyVersion: researchrun.SourceIngestionPolicyVersionV1, Kind: researchrun.SourceIngestionKind(req.Kind),
			WorkspaceID: workspaceID, SessionID: uuidToString(sessionID), SourceSnapshotID: uuid.NewString(),
			ContentHash: req.ContentHash, CapturedAt: capturedAt, Locator: req.Locator, Reason: req.Reason,
			CanonicalURL: req.CanonicalURL, TaskID: req.TaskID, AttemptID: req.AttemptID, AgentID: req.AgentID,
			UserID: req.UserID, AttachmentID: req.AttachmentID, WorkspaceArtifactID: req.WorkspaceArtifactID,
			Adapter: req.Adapter, DatasetID: req.DatasetID,
		},
		Title: req.Title, Text: req.Text,
	})
	if err != nil {
		if errors.Is(err, researchrun.ErrInvalidContract) {
			writeError(w, http.StatusBadRequest, err.Error())
			return
		}
		writeError(w, http.StatusConflict, err.Error())
		return
	}
	writeJSON(w, http.StatusCreated, result)
}

func (h *Handler) GetResearchCanonicalRebuild(w http.ResponseWriter, r *http.Request) {
	workspaceID := h.resolveWorkspaceID(r)
	if _, ok := parseUUIDOrBadRequest(w, workspaceID, "workspace_id"); !ok {
		return
	}
	sessionID, ok := parseUUIDOrBadRequest(w, chi.URLParam(r, "id"), "id")
	if !ok {
		return
	}
	store, ok := h.ResearchRun.(researchrun.ResearchCanonicalRebuild)
	if !ok || store == nil {
		writeError(w, http.StatusServiceUnavailable, "research event rebuild is unavailable")
		return
	}
	rebuilt, err := store.RebuildCanonicalRun(r.Context(), uuidToString(sessionID), workspaceID)
	if err != nil {
		if errors.Is(err, researchrun.ErrIncompleteEventLog) {
			writeJSON(w, http.StatusConflict, map[string]any{
				"code":  "research.v6.incomplete_event_log",
				"error": err.Error(),
			})
			return
		}
		writeError(w, http.StatusInternalServerError, "failed to rebuild canonical run")
		return
	}
	writeJSON(w, http.StatusOK, rebuilt)
}
