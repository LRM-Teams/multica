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
	periodBriefCollectorNamePrefix       = "period-collect-"
	periodBriefCollectorTemplate         = "period-work-collector"
	periodBriefCollectorDisplayLead      = "采集 · "
	periodBriefCollectorCloudDisplayLead = "采集 · 云端 · "
	periodBriefCollectorDaemonSlugLen    = 8
)

var periodBriefCollectorNonSlug = regexp.MustCompile(`[^a-z0-9]+`)

// PeriodBriefCollectorMissingSlot is an owned Computer that still needs a collector.
type PeriodBriefCollectorMissingSlot struct {
	Key       string `json:"key"`
	Label     string `json:"label"`
	MachineID string `json:"machine_id"`
}

// EnsurePeriodBriefCollectorsResponse is returned by
// POST /api/members/agents/period-brief-collectors.
type EnsurePeriodBriefCollectorsResponse struct {
	Agents  []AgentResponse                   `json:"agents"`
	Created []string                          `json:"created"`
	Missing []PeriodBriefCollectorMissingSlot `json:"missing,omitempty"`
}

type ensurePeriodBriefCollectorsRequest struct {
	Model     string `json:"model"`
	RuntimeID string `json:"runtime_id"`
}

type periodBriefCollectorSlot struct {
	key      string // local:<daemon_id> or cloud:<runtime_id>
	slugSeed string
	runtime  db.AgentRuntime
	cloud    bool
}

// EnsurePeriodBriefCollectors probes owned Computers (no runtime_id) or
// creates/repairs one collector for the chosen runtime. It never silently
// creates every missing slot. Another member's machine is never included.
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

	wantRuntimeID := strings.TrimSpace(req.RuntimeID)
	if wantRuntimeID == "" {
		out := make([]AgentResponse, 0, len(slots))
		missing := make([]PeriodBriefCollectorMissingSlot, 0)
		for _, slot := range slots {
			if agent, ok := resolvePeriodBriefCollectorForSlot(agents, agentsByName, runtimeByID, slot); ok {
				resp := agentToResponse(agent)
				redactAgentResponseForActor(&resp, "member")
				out = append(out, resp)
				continue
			}
			missing = append(missing, PeriodBriefCollectorMissingSlot{
				Key:       slot.key,
				Label:     periodBriefCollectorComputerLabel(slot),
				MachineID: periodBriefMachineID(slot.runtime),
			})
		}
		writeJSON(w, http.StatusOK, EnsurePeriodBriefCollectorsResponse{Agents: out, Created: []string{}, Missing: missing})
		return
	}

	var requested *db.AgentRuntime
	for i := range runtimes {
		if uuidToString(runtimes[i].ID) == wantRuntimeID {
			requested = &runtimes[i]
			break
		}
	}
	if requested == nil {
		writeError(w, http.StatusBadRequest, "runtime is not on a computer you own")
		return
	}
	slot, ok := periodBriefCollectorSlotForRuntime(slots, *requested)
	if !ok {
		writeError(w, http.StatusBadRequest, "runtime is not on a computer you own")
		return
	}
	slot.runtime = *requested

	tmpl, found := agentTemplates.Get(periodBriefCollectorTemplate)
	if !found {
		writeError(w, http.StatusInternalServerError, "period-work-collector template missing")
		return
	}

	agent, created, err := h.ensureOnePeriodBriefCollector(r.Context(), wsUUID, ownerID, model, tmpl.Description, tmpl.Instructions, agents, agentsByName, runtimeByID, slot)
	if err != nil {
		if errors.Is(err, errIdentityHandleInvalid) {
			writeError(w, http.StatusBadRequest, "name must be 1-32 lowercase letters, digits, or hyphens")
			return
		}
		if identityUniqueViolation(err, "agent_workspace_name_unique") {
			writeError(w, http.StatusConflict, "name is already in use")
			return
		}
		writeError(w, http.StatusInternalServerError, "failed to create period brief collector: "+err.Error())
		return
	}
	resp := agentToResponse(agent)
	redactAgentResponseForActor(&resp, "member")
	createdIDs := []string{}
	status := http.StatusOK
	if created {
		createdIDs = []string{uuidToString(agent.ID)}
		status = http.StatusCreated
	}
	writeJSON(w, status, EnsurePeriodBriefCollectorsResponse{Agents: []AgentResponse{resp}, Created: createdIDs})
}

func periodBriefCollectorSlotForRuntime(
	slots map[string]periodBriefCollectorSlot,
	rt db.AgentRuntime,
) (periodBriefCollectorSlot, bool) {
	mode := strings.ToLower(strings.TrimSpace(rt.RuntimeMode))
	switch mode {
	case "local":
		daemonID := strings.TrimSpace(rt.DaemonID.String)
		if !rt.DaemonID.Valid || daemonID == "" {
			return periodBriefCollectorSlot{}, false
		}
		slot, ok := slots["local:"+strings.ToLower(daemonID)]
		return slot, ok
	case "cloud":
		runtimeID := uuidToString(rt.ID)
		if runtimeID == "" {
			return periodBriefCollectorSlot{}, false
		}
		slot, ok := slots["cloud:"+runtimeID]
		return slot, ok
	default:
		return periodBriefCollectorSlot{}, false
	}
}

func resolvePeriodBriefCollectorForSlot(
	agents []db.Agent,
	agentsByName map[string]db.Agent,
	runtimeByID map[string]db.AgentRuntime,
	slot periodBriefCollectorSlot,
) (db.Agent, bool) {
	name := periodBriefCollectorNameForSeed(slot.slugSeed)
	if agent, ok := agentsByName[name]; ok && periodBriefCollectorBelongsToSlot(agent, runtimeByID, slot) {
		return agent, true
	}
	return findPeriodBriefCollectorForSlot(agents, runtimeByID, slot)
}

func periodBriefCollectorBelongsToSlot(
	agent db.Agent,
	runtimeByID map[string]db.AgentRuntime,
	slot periodBriefCollectorSlot,
) bool {
	rt, ok := runtimeByID[uuidToString(agent.RuntimeID)]
	if !ok {
		return false
	}
	if slot.cloud {
		return uuidToString(rt.ID) == uuidToString(slot.runtime.ID)
	}
	if !rt.DaemonID.Valid {
		return false
	}
	return strings.EqualFold(strings.TrimSpace(rt.DaemonID.String), strings.TrimSpace(slot.slugSeed))
}

func periodBriefCollectorComputerLabel(slot periodBriefCollectorSlot) string {
	label := strings.TrimSpace(slot.runtime.DisplayName)
	if label == "" {
		label = strings.TrimSpace(slot.runtime.Name)
	}
	if label == "" {
		label = strings.TrimSpace(slot.slugSeed)
	}
	if label == "" {
		if slot.cloud {
			return "Cloud"
		}
		return "Computer"
	}
	return label
}

func periodBriefMachineID(rt db.AgentRuntime) string {
	mode := strings.ToLower(strings.TrimSpace(rt.RuntimeMode))
	if mode == "" {
		mode = "local"
	}
	if rt.DaemonID.Valid {
		daemon := strings.TrimSpace(rt.DaemonID.String)
		if daemon != "" {
			return mode + ":" + daemon
		}
	}
	return mode + ":runtime:" + uuidToString(rt.ID)
}

func (h *Handler) rebindPeriodBriefCollector(
	ctx context.Context,
	agent db.Agent,
	runtime db.AgentRuntime,
	model string,
) (db.Agent, error) {
	if uuidToString(agent.RuntimeID) == uuidToString(runtime.ID) &&
		agent.Model.Valid && agent.Model.String == model {
		return agent, nil
	}
	updated, err := h.Queries.UpdateAgent(ctx, db.UpdateAgentParams{
		ID:          agent.ID,
		RuntimeID:   runtime.ID,
		RuntimeMode: pgtype.Text{String: runtime.RuntimeMode, Valid: true},
		Model:       pgtype.Text{String: model, Valid: true},
	})
	if err != nil {
		return agent, err
	}
	if agent.RuntimeID != runtime.ID {
		_ = h.Queries.MarkAgentRuntimeReassigned(ctx, agent.ID)
	}
	return updated, nil
}

func (h *Handler) ensureOnePeriodBriefCollector(
	ctx context.Context,
	wsUUID pgtype.UUID,
	ownerID string,
	model string,
	description string,
	instructions string,
	agents []db.Agent,
	agentsByName map[string]db.Agent,
	runtimeByID map[string]db.AgentRuntime,
	slot periodBriefCollectorSlot,
) (db.Agent, bool, error) {
	name := periodBriefCollectorNameForSeed(slot.slugSeed)
	displayName := periodBriefCollectorDisplayName(slot.runtime, slot.slugSeed, slot.cloud)
	if agent, ok := agentsByName[name]; ok {
		updated, err := h.rebindPeriodBriefCollector(ctx, agent, slot.runtime, model)
		return updated, false, err
	}
	if agent, ok := findPeriodBriefCollectorForSlot(agents, runtimeByID, slot); ok {
		updated, err := h.rebindPeriodBriefCollector(ctx, agent, slot.runtime, model)
		return updated, false, err
	}
	// UNIQUE (workspace_id, name) includes archived rows. Soft-delete leaves
	// the canonical collector name occupied; ListAgents cannot see it, so a
	// fresh INSERT 23505s. Restore + rebind, same as 笔记助手.
	if restored, okRestore, restoreErr := h.restoreArchivedPeriodBriefCollector(ctx, wsUUID, name, slot.runtime, model); restoreErr != nil {
		return db.Agent{}, false, restoreErr
	} else if okRestore {
		return restored, true, nil
	}

	createParams := db.CreateAgentParams{
		WorkspaceID:   wsUUID,
		Name:          name,
		DisplayName:   displayName,
		Description:   description,
		Instructions:  instructions,
		RuntimeMode:   slot.runtime.RuntimeMode,
		RuntimeConfig: []byte("{}"),
		RuntimeID:     slot.runtime.ID,
		OwnerID:       parseUUID(ownerID),
		CustomEnv:     []byte("{}"),
		CustomArgs:    []byte("[]"),
		Model:         pgtype.Text{String: model, Valid: true},
	}
	applyCreateAgentAvatar(&createParams, resolvedAgentAvatar{})

	created, err := h.createAgentManagedCommit(ctx, wsUUID, createParams, displayName)
	if err != nil {
		if identityUniqueViolation(err, "agent_workspace_name_unique") {
			if restored, okRestore, restoreErr := h.restoreArchivedPeriodBriefCollector(ctx, wsUUID, name, slot.runtime, model); restoreErr == nil && okRestore {
				return restored, true, nil
			}
			if agent, found, findErr := h.findAgentByName(ctx, wsUUID, name); findErr == nil && found {
				updated, rebindErr := h.rebindPeriodBriefCollector(ctx, agent, slot.runtime, model)
				return updated, false, rebindErr
			}
		}
		return db.Agent{}, false, err
	}
	return created, true, nil
}

func (h *Handler) findPeriodBriefCollectorByNameIncludingArchived(
	ctx context.Context,
	workspaceID pgtype.UUID,
	name string,
) (db.Agent, bool, error) {
	agents, err := h.Queries.ListAllAgents(ctx, workspaceID)
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

// restoreArchivedPeriodBriefCollector un-archives the collector that still
// holds the canonical name (if any) and rebinds it to the requested
// runtime/model. Returns ok=false when none archived.
func (h *Handler) restoreArchivedPeriodBriefCollector(
	ctx context.Context,
	workspaceID pgtype.UUID,
	name string,
	runtime db.AgentRuntime,
	model string,
) (db.Agent, bool, error) {
	agent, found, err := h.findPeriodBriefCollectorByNameIncludingArchived(ctx, workspaceID, name)
	if err != nil || !found || !agent.ArchivedAt.Valid {
		return db.Agent{}, false, err
	}
	restored, err := h.Queries.RestoreAgent(ctx, agent.ID)
	if err != nil {
		return db.Agent{}, false, err
	}
	updated, rebindErr := h.rebindPeriodBriefCollector(ctx, restored, runtime, model)
	if rebindErr != nil {
		return restored, true, rebindErr
	}
	return updated, true, nil
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
