package handler

import (
	"encoding/json"
	"net/http"
	"strings"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"
	db "github.com/multica-ai/multica/server/pkg/db/generated"
	"github.com/multica-ai/multica/server/pkg/protocol"
)

// BootstrapAgentChannelGoalControlPlaneRequest atomically establishes the
// Project/Git half of a multi-agent Goal's delivery control plane. Issue
// creation stays on the existing Issue API so each deliverable keeps its own
// acceptance criteria, assignee, lifecycle, and audit trail.
type BootstrapAgentChannelGoalControlPlaneRequest struct {
	ProjectTitle      string `json:"project_title"`
	RepositoryURL     string `json:"repository_url"`
	DefaultBranchHint string `json:"default_branch_hint,omitempty"`
}

type BootstrapAgentChannelGoalControlPlaneResponse struct {
	Project  ProjectResponse         `json:"project"`
	Resource ProjectResourceResponse `json:"resource"`
	Created  bool                    `json:"created"`
	Goal     ChannelGoalResponse     `json:"goal"`
}

// BootstrapAgentChannelGoalControlPlane lets only the current channel manager
// create/reuse one Project, attach its canonical GitHub repository, and bind
// the channel in a single transaction. This removes the partially-configured
// window in which peer agents could race into unrelated clones.
func (h *Handler) BootstrapAgentChannelGoalControlPlane(w http.ResponseWriter, r *http.Request) {
	workspaceID, channelID, agentID, ok := h.agentGoalScope(w, r)
	if !ok {
		return
	}
	if !h.agentIsChannelManager(r.Context(), workspaceID, channelID, agentID) {
		writeError(w, http.StatusForbidden, "only a channel manager can bootstrap goal delivery")
		return
	}
	if !h.agentGoalChannelWritable(r.Context(), workspaceID, channelID) {
		writeError(w, http.StatusConflict, "channel is archived")
		return
	}

	var req BootstrapAgentChannelGoalControlPlaneRequest
	decoder := json.NewDecoder(r.Body)
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid request body")
		return
	}
	req.ProjectTitle = strings.TrimSpace(req.ProjectTitle)
	req.RepositoryURL = strings.TrimSpace(req.RepositoryURL)
	req.DefaultBranchHint = strings.TrimSpace(req.DefaultBranchHint)
	if req.ProjectTitle == "" || len(req.ProjectTitle) > 240 {
		writeError(w, http.StatusBadRequest, "project_title is required and must be at most 240 characters")
		return
	}
	ref, err := validateGithubRepoRef(mustMarshalJSON(githubRepoRef{
		URL: req.RepositoryURL, DefaultBranchHint: req.DefaultBranchHint,
	}))
	if err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	tx, err := h.TxStarter.Begin(r.Context())
	if err != nil {
		writeError(w, http.StatusInternalServerError, "failed to start goal bootstrap")
		return
	}
	defer tx.Rollback(r.Context())
	qtx := h.Queries.WithTx(tx)
	var goalID pgtype.UUID
	if err := tx.QueryRow(r.Context(), `
		SELECT id FROM channel_goal
		WHERE workspace_id = $1 AND channel_id = $2
		  AND status IN ('active', 'paused')
		ORDER BY created_at DESC LIMIT 1
		FOR SHARE`, workspaceID, channelID).Scan(&goalID); err != nil {
		if err == pgx.ErrNoRows {
			writeError(w, http.StatusConflict, "channel has no current goal")
		} else {
			writeError(w, http.StatusInternalServerError, "failed to load channel goal")
		}
		return
	}

	var boundProjectID pgtype.UUID
	if err := tx.QueryRow(r.Context(), `
		SELECT project_id FROM channel
		WHERE workspace_id = $1 AND id = $2 AND kind = 'group' AND archived_at IS NULL
		FOR UPDATE`, workspaceID, channelID).Scan(&boundProjectID); err != nil {
		writeError(w, http.StatusNotFound, "channel not found")
		return
	}

	created := false
	var project db.Project
	if boundProjectID.Valid {
		project, err = qtx.GetProjectInWorkspace(r.Context(), db.GetProjectInWorkspaceParams{ID: boundProjectID, WorkspaceID: workspaceID})
		if err != nil {
			writeError(w, http.StatusConflict, "bound project is unavailable")
			return
		}
	} else {
		project, err = qtx.CreateProject(r.Context(), db.CreateProjectParams{
			WorkspaceID: workspaceID, Title: req.ProjectTitle, Status: "in_progress", Priority: "none",
			LeadType: pgtype.Text{String: "agent", Valid: true}, LeadID: agentID,
		})
		if err != nil {
			h.writeProjectWriteError(w, r, err, "create")
			return
		}
		created = true
	}

	resources, err := qtx.ListProjectResources(r.Context(), project.ID)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "failed to inspect project resources")
		return
	}
	var repoResource db.ProjectResource
	resourceCreated := false
	for _, resource := range resources {
		if resource.ResourceType != "github_repo" {
			continue
		}
		var existing githubRepoRef
		_ = json.Unmarshal(resource.ResourceRef, &existing)
		var requested githubRepoRef
		_ = json.Unmarshal(ref, &requested)
		if strings.TrimSuffix(existing.URL, ".git") != strings.TrimSuffix(requested.URL, ".git") {
			writeError(w, http.StatusConflict, "bound project already has a different github_repo")
			return
		}
		repoResource = resource
		break
	}
	if !repoResource.ID.Valid {
		repoResource, err = qtx.CreateProjectResource(r.Context(), db.CreateProjectResourceParams{
			ProjectID: project.ID, WorkspaceID: workspaceID, ResourceType: "github_repo",
			ResourceRef: ref, Position: int32(len(resources)),
		})
		if err != nil {
			if isUniqueViolation(err) {
				writeError(w, http.StatusConflict, "github repository is already attached")
			} else {
				writeError(w, http.StatusInternalServerError, "failed to attach github repository")
			}
			return
		}
		resourceCreated = true
	}
	if !boundProjectID.Valid {
		if _, err := tx.Exec(r.Context(), `UPDATE channel SET project_id=$1, updated_at=now() WHERE workspace_id=$2 AND id=$3`, project.ID, workspaceID, channelID); err != nil {
			writeError(w, http.StatusInternalServerError, "failed to bind project to channel")
			return
		}
	}
	if err := tx.Commit(r.Context()); err != nil {
		writeError(w, http.StatusInternalServerError, "failed to commit goal bootstrap")
		return
	}

	projectResp := projectToResponse(project)
	projectResp.ResourceCount = int64(len(resources))
	if resourceCreated {
		projectResp.ResourceCount++
	}
	resourceResp := projectResourceToResponse(repoResource)
	if created {
		h.publish(protocol.EventProjectCreated, uuidToString(workspaceID), "agent", uuidToString(agentID), map[string]any{"project": projectResp})
	}
	if resourceCreated {
		h.publish(protocol.EventProjectResourceCreated, uuidToString(workspaceID), "agent", uuidToString(agentID), map[string]any{"resource": resourceResp, "project_id": projectResp.ID})
	}
	h.publish(protocol.EventChannelUpdated, uuidToString(workspaceID), "agent", uuidToString(agentID), map[string]any{"id": uuidToString(channelID), "project_id": projectResp.ID})

	goal, err := scanChannelGoal(h.DB.QueryRow(r.Context(), `
		SELECT `+channelGoalColumns+` FROM channel_goal
		WHERE workspace_id = $1 AND channel_id = $2 AND id = $3`, workspaceID, channelID, goalID))
	if err != nil {
		writeError(w, http.StatusInternalServerError, "goal bootstrap completed but goal refresh failed")
		return
	}
	h.hydrateChannelGoalControlPlane(r.Context(), &goal)
	h.publishChannelGoalUpdated(uuidToString(workspaceID), uuidToString(channelID), "agent", uuidToString(agentID), goal)
	writeJSON(w, http.StatusCreated, BootstrapAgentChannelGoalControlPlaneResponse{
		Project: projectResp, Resource: resourceResp, Created: created, Goal: goal,
	})
}

func mustMarshalJSON(value any) json.RawMessage {
	out, _ := json.Marshal(value)
	return out
}
