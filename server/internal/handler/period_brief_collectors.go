package handler

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"regexp"
	"strings"

	"github.com/jackc/pgx/v5/pgtype"

	"github.com/multica-ai/multica/server/internal/service"
	db "github.com/multica-ai/multica/server/pkg/db/generated"
)

const (
	periodBriefCollectorNamePrefix        = "period-collect-"
	periodBriefCollectorTemplate          = "period-work-collector"
	periodBriefCollectorDisplayLead       = "采集 · "
	periodBriefCollectorCloudDisplayLead  = "采集 · 云端 · "
	periodBriefCollectorDaemonSlugLen     = 8
)

var periodBriefCollectorNonSlug = regexp.MustCompile(`[^a-z0-9]+`)

// EnsurePeriodBriefCollectorsResponse is returned by
// POST /api/members/agents/period-brief-collectors.
type EnsurePeriodBriefCollectorsResponse struct {
	Agents  []AgentResponse `json:"agents"`
	Created []string        `json:"created"`
}

type ensurePeriodBriefCollectorsRequest struct {
	Model string `json:"model"`
}

type periodBriefCollectorSlot struct {
	key      string // local:<daemon_id> or cloud:<runtime_id>
	slugSeed string
	runtime  db.AgentRuntime
	cloud    bool
}

// EnsurePeriodBriefCollectors idempotently provisions one Period Work collector
// Agent per Computer the caller owns (local Computers share a daemon_id; each
// cloud runtime is its own Computer). Another member's machine is never
// included — even when that runtime is workspace-public or the caller is admin.
func (h *Handler) EnsurePeriodBriefCollectors(w http.ResponseWriter, r *http.Request) {
	if rejectAgentOnHumanRoute(w, r, "EnsurePeriodBriefCollectors") {
		return
	}
	ownerID, ok := requireUserID(w, r)
	if !ok {
		return
	}
	workspaceID := h.resolveWorkspaceID(r)
	wsUUID, ok := parseUUIDOrBadRequest(w, workspaceID, "workspace id")
	if !ok {
		return
	}
	member, ok := h.requireManageAgents(w, r, workspaceID, "workspace not found")
	if !ok {
		return
	}

	var req ensurePeriodBriefCollectorsRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid request body")
		return
	}
	model := strings.TrimSpace(req.Model)
	if err := service.RequireAgentModel(model); err != nil {
		writeError(w, http.StatusBadRequest, "model is required")
		return
	}

	// Period Work collection is Computer-owner-only: never provision collectors
	// onto another member's machine, even when that runtime is workspace-public
	// or the caller is a workspace admin.
	runtimes, err := h.Queries.ListAgentRuntimesByOwner(r.Context(), db.ListAgentRuntimesByOwnerParams{
		WorkspaceID: wsUUID,
		UserID:      parseUUID(ownerID),
	})
	if err != nil {
		writeError(w, http.StatusInternalServerError, "failed to list runtimes")
		return
	}

	agents, err := h.Queries.ListAgents(r.Context(), wsUUID)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "failed to list agents")
		return
	}
	agentsByName := make(map[string]db.Agent, len(agents))
	for _, agent := range agents {
		agentsByName[agent.Name] = agent
	}
	runtimeByID := make(map[string]db.AgentRuntime, len(runtimes))
	for _, rt := range runtimes {
		runtimeByID[uuidToString(rt.ID)] = rt
	}

	slots := make(map[string]periodBriefCollectorSlot)
	for _, rt := range runtimes {
		rtOwnerID, _ := h.resolveRuntimeOwnerQuery(r.Context(), rt)
		if !canOwnRuntime(member, rt, rtOwnerID) {
			continue
		}
		mode := strings.ToLower(strings.TrimSpace(rt.RuntimeMode))
		switch mode {
		case "local":
			daemonID := strings.TrimSpace(rt.DaemonID.String)
			if !rt.DaemonID.Valid || daemonID == "" {
				continue
			}
			key := "local:" + strings.ToLower(daemonID)
			existing, ok := slots[key]
			if !ok {
				slots[key] = periodBriefCollectorSlot{key: key, slugSeed: daemonID, runtime: rt, cloud: false}
				continue
			}
			if existing.runtime.Status != "online" && rt.Status == "online" {
				slots[key] = periodBriefCollectorSlot{key: key, slugSeed: daemonID, runtime: rt, cloud: false}
			}
		case "cloud":
			runtimeID := uuidToString(rt.ID)
			if runtimeID == "" {
				continue
			}
			key := "cloud:" + runtimeID
			slots[key] = periodBriefCollectorSlot{key: key, slugSeed: runtimeID, runtime: rt, cloud: true}
		default:
			continue
		}
	}

	tmpl, found := agentTemplates.Get(periodBriefCollectorTemplate)
	if !found {
		writeError(w, http.StatusInternalServerError, "period-work-collector template missing")
		return
	}

	out := make([]AgentResponse, 0, len(slots))
	createdIDs := make([]string, 0)
	for _, slot := range slots {
		name := periodBriefCollectorNameForSeed(slot.slugSeed)
		displayName := periodBriefCollectorDisplayName(slot.runtime, slot.slugSeed, slot.cloud)
		if agent, ok := agentsByName[name]; ok {
			resp := agentToResponse(agent)
			redactAgentResponseForActor(&resp, "member")
			out = append(out, resp)
			continue
		}
		if agent, ok := findPeriodBriefCollectorForSlot(agents, runtimeByID, slot); ok {
			resp := agentToResponse(agent)
			redactAgentResponseForActor(&resp, "member")
			out = append(out, resp)
			continue
		}

		createParams := db.CreateAgentParams{
			WorkspaceID:        wsUUID,
			Name:               name,
			DisplayName:        displayName,
			Description:        tmpl.Description,
			Instructions:       tmpl.Instructions,
			RuntimeMode:        slot.runtime.RuntimeMode,
			RuntimeConfig:      []byte("{}"),
			RuntimeID:          slot.runtime.ID,
			MaxConcurrentTasks: 4,
			OwnerID:            parseUUID(ownerID),
			CustomEnv:          []byte("{}"),
			CustomArgs:         []byte("[]"),
			Model:              pgtype.Text{String: model, Valid: true},
		}
		applyCreateAgentAvatar(&createParams, resolvedAgentAvatar{})

		created, err := h.createAgentManagedCommit(r.Context(), wsUUID, createParams, displayName)
		if err != nil {
			if identityUniqueViolation(err, "agent_workspace_name_unique") {
				if agent, found, findErr := h.findAgentByName(r.Context(), wsUUID, name); findErr == nil && found {
					resp := agentToResponse(agent)
					redactAgentResponseForActor(&resp, "member")
					out = append(out, resp)
					continue
				}
			}
			if errors.Is(err, errIdentityHandleInvalid) {
				writeError(w, http.StatusBadRequest, "name must be 1-32 lowercase letters, digits, or hyphens")
				return
			}
			writeError(w, http.StatusInternalServerError, "failed to create period brief collector: "+err.Error())
			return
		}
		agentsByName[name] = created
		createdIDs = append(createdIDs, uuidToString(created.ID))
		resp := agentToResponse(created)
		redactAgentResponseForActor(&resp, "member")
		out = append(out, resp)
	}

	status := http.StatusOK
	if len(createdIDs) > 0 {
		status = http.StatusCreated
	}
	writeJSON(w, status, EnsurePeriodBriefCollectorsResponse{Agents: out, Created: createdIDs})
}

func (h *Handler) findAgentByName(ctx context.Context, workspaceID pgtype.UUID, name string) (db.Agent, bool, error) {
	agents, err := h.Queries.ListAgents(ctx, workspaceID)
	if err != nil {
		return db.Agent{}, false, err
	}
	for _, agent := range agents {
		if agent.Name == name {
			return agent, true, nil
		}
	}
	return db.Agent{}, false, nil
}

func findPeriodBriefCollectorForSlot(
	agents []db.Agent,
	runtimeByID map[string]db.AgentRuntime,
	slot periodBriefCollectorSlot,
) (db.Agent, bool) {
	for _, agent := range agents {
		if !isPeriodBriefCollectorAgentName(agent.Name) {
			continue
		}
		rt, ok := runtimeByID[uuidToString(agent.RuntimeID)]
		if !ok {
			continue
		}
		if slot.cloud {
			if uuidToString(rt.ID) == uuidToString(slot.runtime.ID) {
				return agent, true
			}
			continue
		}
		if !rt.DaemonID.Valid {
			continue
		}
		if strings.EqualFold(strings.TrimSpace(rt.DaemonID.String), strings.TrimSpace(slot.slugSeed)) {
			return agent, true
		}
	}
	return db.Agent{}, false
}

func isPeriodBriefCollectorAgentName(name string) bool {
	return strings.HasPrefix(strings.TrimSpace(name), periodBriefCollectorNamePrefix)
}

func periodBriefCollectorNameForSeed(seed string) string {
	return periodBriefCollectorNamePrefix + periodBriefCollectorDaemonSlug(seed)
}

// periodBriefCollectorNameForDaemon keeps the historical helper name for tests.
func periodBriefCollectorNameForDaemon(daemonID string) string {
	return periodBriefCollectorNameForSeed(daemonID)
}

func periodBriefCollectorDaemonSlug(daemonID string) string {
	cleaned := periodBriefCollectorNonSlug.ReplaceAllString(strings.ToLower(strings.TrimSpace(daemonID)), "")
	if cleaned == "" {
		cleaned = "computer"
	}
	// Prefer the tail so UUID / "pc-daemon-<suffix>" variants stay distinct.
	if len(cleaned) > periodBriefCollectorDaemonSlugLen {
		cleaned = cleaned[len(cleaned)-periodBriefCollectorDaemonSlugLen:]
	}
	for len(cleaned) < periodBriefCollectorDaemonSlugLen {
		cleaned += "0"
	}
	return cleaned
}

func periodBriefCollectorDisplayName(rt db.AgentRuntime, seed string, cloud bool) string {
	label := strings.TrimSpace(rt.DisplayName)
	if label == "" {
		label = strings.TrimSpace(rt.Name)
	}
	if label == "" {
		label = strings.TrimSpace(seed)
	}
	if label == "" {
		if cloud {
			label = "Cloud"
		} else {
			label = "Computer"
		}
	}
	if cloud {
		return periodBriefCollectorCloudDisplayLead + label
	}
	return periodBriefCollectorDisplayLead + label
}
