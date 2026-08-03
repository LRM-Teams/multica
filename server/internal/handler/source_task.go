package handler

import (
	"encoding/json"
	"errors"
	"net/http"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/multica-ai/multica/server/internal/service"
	db "github.com/multica-ai/multica/server/pkg/db/generated"
)

type createSourceTaskRequest struct {
	Type    string          `json:"type"`
	Payload json.RawMessage `json:"payload"`
}

type sourceTaskResponse struct {
	SourceTaskID string          `json:"source_task_id"`
	Type         string          `json:"type"`
	Payload      json.RawMessage `json:"payload,omitempty"`
	CreatedAt    string          `json:"created_at"`
}

// CreateSourceTask registers an immutable workspace-scoped dataset task. The
// database upsert reuses its content hash, never an agent task or local target.
func (h *Handler) CreateSourceTask(w http.ResponseWriter, r *http.Request) {
	if _, ok := requireUserID(w, r); !ok {
		return
	}
	workspaceID := ctxWorkspaceID(r.Context())
	if workspaceID == "" {
		writeError(w, http.StatusBadRequest, "workspace ID required")
		return
	}
	var request createSourceTaskRequest
	if err := json.NewDecoder(r.Body).Decode(&request); err != nil {
		writeError(w, http.StatusBadRequest, "malformed request body")
		return
	}
	input, err := service.ParseSourceTask(request.Type, request.Payload)
	if err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	row, err := h.Queries.UpsertSourceTask(r.Context(), db.UpsertSourceTaskParams{
		WorkspaceID: parseUUID(workspaceID),
		Type:        string(input.Type),
		Payload:     input.Payload,
		ContentHash: input.ContentHash,
	})
	if err != nil {
		writeError(w, http.StatusInternalServerError, "create source task")
		return
	}
	writeJSON(w, http.StatusCreated, sourceTaskResponse{
		SourceTaskID: uuidToString(row.ID),
		Type:         row.Type,
		CreatedAt:    sourceTaskCreatedAt(row.CreatedAt),
	})
}

// GetSourceTask returns a source task only when it belongs to the request's
// workspace. A cross-workspace ID is indistinguishable from a missing ID.
func (h *Handler) GetSourceTask(w http.ResponseWriter, r *http.Request) {
	if _, ok := requireUserID(w, r); !ok {
		return
	}
	workspaceID := ctxWorkspaceID(r.Context())
	if workspaceID == "" {
		writeError(w, http.StatusBadRequest, "workspace ID required")
		return
	}
	sourceTaskID := chi.URLParam(r, "sourceTaskID")
	if _, ok := parseUUIDOrBadRequest(w, sourceTaskID, "source_task_id"); !ok {
		return
	}
	row, err := h.Queries.GetSourceTaskForWorkspace(r.Context(), db.GetSourceTaskForWorkspaceParams{
		ID:          parseUUID(sourceTaskID),
		WorkspaceID: parseUUID(workspaceID),
	})
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			writeError(w, http.StatusNotFound, "source task not found")
			return
		}
		writeError(w, http.StatusInternalServerError, "get source task")
		return
	}
	writeJSON(w, http.StatusOK, sourceTaskResponse{
		SourceTaskID: uuidToString(row.ID),
		Type:         row.Type,
		Payload:      row.Payload,
		CreatedAt:    sourceTaskCreatedAt(row.CreatedAt),
	})
}

func sourceTaskCreatedAt(createdAt pgtype.Timestamptz) string {
	if createdAt.Valid {
		return createdAt.Time.UTC().Format(time.RFC3339Nano)
	}
	return ""
}
