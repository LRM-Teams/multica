// SPDX-License-Identifier: Apache-2.0

package handler

import (
	"net/http"

	"github.com/jackc/pgx/v5/pgtype"

	"github.com/multica-ai/multica/server/internal/memorygraph"
	"github.com/multica-ai/multica/server/internal/service"
	"github.com/multica-ai/multica/server/internal/util"
)

// Graph memory corrections (plan Task 15 Step 3, spec §11): owner/admin
// retracts a wrong project node immediately; ordinary members and agents
// submit correction candidates; supersede pairs a retraction with a new
// durable-evidence promotion published through the Task 14 coordinator.

type graphMemoryCorrectionEvidence struct {
	Kind  string `json:"kind"`
	RefID string `json:"ref_id"`
}

type graphMemoryCorrectionReplacement struct {
	Body     string                          `json:"body"`
	Evidence []graphMemoryCorrectionEvidence `json:"evidence"`
}

type graphMemoryCorrectionRequest struct {
	Action      string                            `json:"action"` // retract | correct | supersede
	NodeID      string                            `json:"node_id"`
	Reason      string                            `json:"reason"`
	ProjectID   string                            `json:"project_id"`
	Replacement *graphMemoryCorrectionReplacement `json:"replacement"`
}

// GraphMemoryCorrection serves POST /api/workspaces/{id}/memory/graph/corrections.
func (h *Handler) GraphMemoryCorrection(w http.ResponseWriter, r *http.Request) {
	workspaceID := workspaceIDFromURL(r, "id")
	member, ok := h.workspaceMember(w, r, workspaceID)
	if !ok {
		return
	}
	if h.GraphMemoryCorrections == nil {
		writeError(w, http.StatusServiceUnavailable, "graph memory corrections are not configured")
		return
	}
	var request graphMemoryCorrectionRequest
	if !decodeJSONBody(w, r, &request) {
		return
	}
	if request.NodeID == "" {
		writeError(w, http.StatusBadRequest, "node_id is required")
		return
	}
	workspaceUUID, err := util.ParseUUID(workspaceID)
	if err != nil {
		writeError(w, http.StatusBadRequest, "invalid workspace id")
		return
	}
	actor := "user:" + member.UserID.String()
	privileged := roleAllowed(member.Role, "owner", "admin")

	switch request.Action {
	case "retract", "":
		if privileged {
			if err := h.GraphMemoryCorrections.Retract(r.Context(), workspaceUUID, request.NodeID, actor, request.Reason); err != nil {
				writeError(w, http.StatusInternalServerError, "retract failed")
				return
			}
			writeJSON(w, http.StatusOK, map[string]any{"status": "retracted", "node_id": request.NodeID})
			return
		}
		// Candidate flow: non-privileged callers never hide content.
		if err := h.GraphMemoryCorrections.Submit(r.Context(), workspaceUUID, request.NodeID, actor, request.Reason); err != nil {
			writeError(w, http.StatusInternalServerError, "correction submission failed")
			return
		}
		writeJSON(w, http.StatusAccepted, map[string]any{"status": "candidate", "node_id": request.NodeID})
	case "correct":
		if err := h.GraphMemoryCorrections.Submit(r.Context(), workspaceUUID, request.NodeID, actor, request.Reason); err != nil {
			writeError(w, http.StatusInternalServerError, "correction submission failed")
			return
		}
		writeJSON(w, http.StatusAccepted, map[string]any{"status": "candidate", "node_id": request.NodeID})
	case "supersede":
		if !privileged {
			// Superseding publishes a replacement; only owner/admin may.
			if err := h.GraphMemoryCorrections.Submit(r.Context(), workspaceUUID, request.NodeID, actor, request.Reason); err != nil {
				writeError(w, http.StatusInternalServerError, "correction submission failed")
				return
			}
			writeJSON(w, http.StatusAccepted, map[string]any{"status": "candidate", "node_id": request.NodeID})
			return
		}
		h.supersedeGraphNode(w, r, workspaceUUID, request, actor)
	default:
		writeError(w, http.StatusBadRequest, "action must be retract, correct, or supersede")
	}
}

// supersedeGraphNode retracts the wrong node and publishes the replacement
// through the promotion policy + publication coordinator. A refused policy
// decision leaves the old node untouched.
func (h *Handler) supersedeGraphNode(w http.ResponseWriter, r *http.Request, workspaceUUID pgtype.UUID, request graphMemoryCorrectionRequest, actor string) {
	if h.GraphMemoryPromotion == nil || h.GraphMemoryPromotionPublish == nil {
		writeError(w, http.StatusServiceUnavailable, "graph memory promotion is not configured")
		return
	}
	if request.ProjectID == "" || request.Replacement == nil || request.Replacement.Body == "" || len(request.Replacement.Evidence) == 0 {
		writeError(w, http.StatusBadRequest, "supersede requires project_id and a replacement body with durable evidence")
		return
	}
	projectUUID, err := util.ParseUUID(request.ProjectID)
	if err != nil {
		writeError(w, http.StatusBadRequest, "invalid project id")
		return
	}
	evidence := make([]service.PromotionEvidence, 0, len(request.Replacement.Evidence))
	for _, e := range request.Replacement.Evidence {
		evidence = append(evidence, service.PromotionEvidence{
			Kind: e.Kind, RefID: e.RefID, PolicyVersion: service.PromotionPolicyVersion,
		})
	}
	decision, err := h.GraphMemoryPromotion.Evaluate(r.Context(), service.PromotionRequest{
		WorkspaceID: workspaceUUID, ProjectID: projectUUID,
		ProposedNode: &memorygraph.Node{NodeID: request.NodeID + "-supersede", Body: request.Replacement.Body},
		Evidence:     evidence, ProposedBy: actor,
	})
	if err != nil {
		writeError(w, http.StatusInternalServerError, "promotion evaluation failed")
		return
	}
	if !decision.Allowed {
		writeJSON(w, http.StatusUnprocessableEntity, map[string]any{"error": "promotion_refused", "reason": decision.Reason})
		return
	}
	if _, err := h.GraphMemoryPromotionPublish.PublishPromotion(r.Context(), service.PromotionRequest{
		WorkspaceID: workspaceUUID, ProjectID: projectUUID,
		ProposedNode: &memorygraph.Node{NodeID: request.NodeID + "-supersede", Body: request.Replacement.Body},
		Evidence:     evidence, ProposedBy: actor,
	}, decision); err != nil {
		writeError(w, http.StatusInternalServerError, "promotion publication failed")
		return
	}
	if err := h.GraphMemoryCorrections.Retract(r.Context(), workspaceUUID, request.NodeID, actor, request.Reason); err != nil {
		writeError(w, http.StatusInternalServerError, "retract failed after promotion")
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"status": "superseded", "node_id": request.NodeID})
}
