package handler

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"

	"github.com/multica-ai/multica/server/internal/service"
	db "github.com/multica-ai/multica/server/pkg/db/generated"
	"github.com/multica-ai/multica/server/pkg/protocol"
)

// Graph memory reviewer profile (design §1 memory_type, adjustment A4):
// per-workspace reviewer configuration that overrides the process-level
// MULTICA_MEMORY_TYPE env default. One row per workspace; an absent row
// means "no workspace override" and the env default applies.
//
// Spec §2/§16: the profile is the business-tunables authority for the
// Dive-Judge era. explore_agents is the saved per-recall TTT concurrency K
// (brief D2). Updates use a config_version compare-and-set contract: stale
// or unversioned writes against an existing row return 409 instead of
// silently overwriting concurrent changes.

const (
	defaultGraphMemoryType = "legacy"
	defaultGraphMemoryMode = "agent"
)

type graphMemoryProfileResponse struct {
	WorkspaceID                         string  `json:"workspace_id"`
	MemoryType                          string  `json:"memory_type"`
	GraphMemoryMode                     string  `json:"graph_memory_mode"`
	MemoryAgentRuntimeID                string  `json:"memory_agent_runtime_id"`
	MemoryAgentModel                    string  `json:"memory_agent_model"`
	MemoryAgentThinking                 string  `json:"memory_agent_thinking"`
	RecallTTTEnabled                    bool    `json:"recall_ttt_enabled"`
	ConsolidationTTTEnabled             bool    `json:"consolidation_ttt_enabled"`
	MemoryAgentIdleGraceSeconds         int32   `json:"memory_agent_idle_grace_seconds"`
	MemoryAgentMaxNodesPerCall          int32   `json:"memory_agent_max_nodes_per_call"`
	MemoryAgentMaxNodesPerMinute        int32   `json:"memory_agent_max_nodes_per_minute"`
	MemoryAgentMaxContinuousTurnSeconds int32   `json:"memory_agent_max_continuous_turn_seconds"`
	MemoryAgentMaxTokensPerHour         int64   `json:"memory_agent_max_tokens_per_hour"`
	ExploreAgents                       int32   `json:"explore_agents"`
	ExploreMaxRounds                    int32   `json:"explore_max_rounds"`
	TTTEnabled                          bool    `json:"ttt_enabled"`
	ExploreNodesPerExpansion            int32   `json:"explore_nodes_per_expansion"`
	MaxHierarchyFanout                  int32   `json:"max_hierarchy_fanout"`
	MaxRelationEdgesPerNode             int32   `json:"max_relation_edges_per_node"`
	DiveMaxRounds                       int32   `json:"dive_max_rounds"`
	DiveMaxViewedNodes                  int32   `json:"dive_max_viewed_nodes"`
	DiveMaxSourceFiles                  int32   `json:"dive_max_source_files"`
	DiveTimeoutSeconds                  int32   `json:"dive_timeout_seconds"`
	WRound                              float64 `json:"w_round"`
	SourceMaxFileBytes                  int64   `json:"source_max_file_bytes"`
	SourceMaxTotalBytes                 int64   `json:"source_max_total_bytes"`
	SourceMaxPDFPages                   int32   `json:"source_max_pdf_pages"`
	SourceMaxAVSeconds                  int32   `json:"source_max_av_seconds"`
	SourceMaxImageMegapixels            int32   `json:"source_max_image_megapixels"`
	DiveModel                           string  `json:"dive_model"`
	DiveProvider                        string  `json:"dive_provider"`
	ConfigVersion                       int64   `json:"config_version"`
	UpdatedAt                           string  `json:"updated_at,omitempty"`
}

// updateGraphMemoryProfileRequest is a full-profile write guarded by
// config_version. Tunable pointers left null preserve the current (or
// default, on create) values so knob-only updates stay ergonomic; the
// concurrency guard — not field coverage — is the CAS contract.
type updateGraphMemoryProfileRequest struct {
	MemoryType                          string   `json:"memory_type"`
	GraphMemoryMode                     *string  `json:"graph_memory_mode"`
	MemoryAgentRuntimeID                *string  `json:"memory_agent_runtime_id"`
	MemoryAgentModel                    *string  `json:"memory_agent_model"`
	MemoryAgentThinking                 *string  `json:"memory_agent_thinking"`
	RecallTTTEnabled                    *bool    `json:"recall_ttt_enabled"`
	ConsolidationTTTEnabled             *bool    `json:"consolidation_ttt_enabled"`
	MemoryAgentIdleGraceSeconds         *int32   `json:"memory_agent_idle_grace_seconds"`
	MemoryAgentMaxNodesPerCall          *int32   `json:"memory_agent_max_nodes_per_call"`
	MemoryAgentMaxNodesPerMinute        *int32   `json:"memory_agent_max_nodes_per_minute"`
	MemoryAgentMaxContinuousTurnSeconds *int32   `json:"memory_agent_max_continuous_turn_seconds"`
	MemoryAgentMaxTokensPerHour         *int64   `json:"memory_agent_max_tokens_per_hour"`
	ExploreAgents                       int32    `json:"explore_agents"`
	ExploreMaxRounds                    int32    `json:"explore_max_rounds"`
	ConfirmEmptyStart                   bool     `json:"confirm_empty_start"`
	ConfigVersion                       *int64   `json:"config_version"`
	TTTEnabled                          *bool    `json:"ttt_enabled"`
	ExploreNodesPerExpansion            *int32   `json:"explore_nodes_per_expansion"`
	MaxHierarchyFanout                  *int32   `json:"max_hierarchy_fanout"`
	MaxRelationEdgesPerNode             *int32   `json:"max_relation_edges_per_node"`
	DiveMaxRounds                       *int32   `json:"dive_max_rounds"`
	DiveMaxViewedNodes                  *int32   `json:"dive_max_viewed_nodes"`
	DiveMaxSourceFiles                  *int32   `json:"dive_max_source_files"`
	DiveTimeoutSeconds                  *int32   `json:"dive_timeout_seconds"`
	WRound                              *float64 `json:"w_round"`
	SourceMaxFileBytes                  *int64   `json:"source_max_file_bytes"`
	SourceMaxTotalBytes                 *int64   `json:"source_max_total_bytes"`
	SourceMaxPDFPages                   *int32   `json:"source_max_pdf_pages"`
	SourceMaxAVSeconds                  *int32   `json:"source_max_av_seconds"`
	SourceMaxImageMegapixels            *int32   `json:"source_max_image_megapixels"`
	DiveModel                           *string  `json:"dive_model"`
	DiveProvider                        *string  `json:"dive_provider"`
}

func validGraphMemoryType(t string) bool {
	return t == "legacy" || t == "graph"
}

func validGraphMemoryMode(mode string) bool {
	return mode == "inject" || mode == "agent"
}

func (h *Handler) graphMemoryLimits() service.GraphMemoryLimits {
	if h.GraphMemoryLimits.Ceilings.TTTConcurrency == 0 {
		return service.LoadGraphMemoryLimits(func(string) string { return "" })
	}
	return h.GraphMemoryLimits
}

func graphMemoryProfileFromRow(row db.GraphMemoryProfile) graphMemoryProfileResponse {
	resp := graphMemoryProfileResponse{
		WorkspaceID:                         uuidToString(row.WorkspaceID),
		MemoryType:                          row.MemoryType,
		GraphMemoryMode:                     row.GraphMemoryMode,
		MemoryAgentModel:                    row.MemoryAgentModel,
		MemoryAgentThinking:                 row.MemoryAgentThinking,
		RecallTTTEnabled:                    row.RecallTttEnabled,
		ConsolidationTTTEnabled:             row.ConsolidationTttEnabled,
		MemoryAgentIdleGraceSeconds:         row.MemoryAgentIdleGraceSeconds,
		MemoryAgentMaxNodesPerCall:          row.MemoryAgentMaxNodesPerCall,
		MemoryAgentMaxNodesPerMinute:        row.MemoryAgentMaxNodesPerMinute,
		MemoryAgentMaxContinuousTurnSeconds: row.MemoryAgentMaxContinuousTurnSeconds,
		MemoryAgentMaxTokensPerHour:         row.MemoryAgentMaxTokensPerHour,
		ExploreAgents:                       row.ExploreAgents,
		ExploreMaxRounds:                    row.ExploreMaxRounds,
		TTTEnabled:                          row.TttEnabled,
		ExploreNodesPerExpansion:            row.ExploreNodesPerExpansion,
		MaxHierarchyFanout:                  row.MaxHierarchyFanout,
		MaxRelationEdgesPerNode:             row.MaxRelationEdgesPerNode,
		DiveMaxRounds:                       row.DiveMaxRounds,
		DiveMaxViewedNodes:                  row.DiveMaxViewedNodes,
		DiveMaxSourceFiles:                  row.DiveMaxSourceFiles,
		DiveTimeoutSeconds:                  row.DiveTimeoutSeconds,
		WRound:                              row.WRound,
		SourceMaxFileBytes:                  row.SourceMaxFileBytes,
		SourceMaxTotalBytes:                 row.SourceMaxTotalBytes,
		SourceMaxPDFPages:                   row.SourceMaxPdfPages,
		SourceMaxAVSeconds:                  row.SourceMaxAvSeconds,
		SourceMaxImageMegapixels:            row.SourceMaxImageMegapixels,
		DiveModel:                           row.DiveModel,
		DiveProvider:                        row.DiveProvider,
		ConfigVersion:                       row.ConfigVersion,
	}
	if row.MemoryAgentRuntimeID.Valid {
		resp.MemoryAgentRuntimeID = uuidToString(row.MemoryAgentRuntimeID)
	}
	if row.UpdatedAt.Valid {
		resp.UpdatedAt = row.UpdatedAt.Time.UTC().Format(time.RFC3339)
	}
	return resp
}

func (h *Handler) defaultGraphMemoryProfile(workspaceID string) graphMemoryProfileResponse {
	defaults := h.graphMemoryLimits().Defaults
	return graphMemoryProfileResponse{
		WorkspaceID:                         workspaceID,
		MemoryType:                          defaultGraphMemoryType,
		GraphMemoryMode:                     defaultGraphMemoryMode,
		MemoryAgentIdleGraceSeconds:         120,
		MemoryAgentMaxNodesPerCall:          4,
		MemoryAgentMaxNodesPerMinute:        30,
		MemoryAgentMaxContinuousTurnSeconds: 600,
		MemoryAgentMaxTokensPerHour:         200000,
		ExploreAgents:                       int32(defaults.TTTConcurrency),
		ExploreMaxRounds:                    6,
		ExploreNodesPerExpansion:            int32(defaults.ExploreNodesPerExpansion),
		MaxHierarchyFanout:                  int32(defaults.MaxHierarchyFanout),
		MaxRelationEdgesPerNode:             int32(defaults.MaxRelationEdgesPerNode),
		DiveMaxRounds:                       int32(defaults.DiveMaxRounds),
		DiveMaxViewedNodes:                  int32(defaults.DiveMaxViewedNodes),
		DiveMaxSourceFiles:                  int32(defaults.DiveMaxSourceFiles),
		DiveTimeoutSeconds:                  int32(defaults.DiveTimeoutSeconds),
		WRound:                              defaults.WRound,
		SourceMaxFileBytes:                  defaults.SourceMaxFileBytes,
		SourceMaxTotalBytes:                 defaults.SourceMaxTotalBytes,
		SourceMaxPDFPages:                   int32(defaults.SourceMaxPDFPages),
		SourceMaxAVSeconds:                  int32(defaults.SourceMaxAVSeconds),
		SourceMaxImageMegapixels:            int32(defaults.SourceMaxImageMegapixels),
	}
}

func (h *Handler) GetGraphMemoryProfile(w http.ResponseWriter, r *http.Request) {
	workspaceID := workspaceIDFromURL(r, "id")
	if _, ok := h.workspaceMember(w, r, workspaceID); !ok {
		return
	}
	profile, err := h.Queries.GetGraphMemoryProfile(r.Context(), parseUUID(workspaceID))
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			writeJSON(w, http.StatusOK, h.defaultGraphMemoryProfile(workspaceID))
			return
		}
		writeError(w, http.StatusInternalServerError, "failed to load graph memory profile")
		return
	}
	writeJSON(w, http.StatusOK, graphMemoryProfileFromRow(profile))
}

func (h *Handler) UpdateGraphMemoryProfile(w http.ResponseWriter, r *http.Request) {
	workspaceID := workspaceIDFromURL(r, "id")
	member, ok := h.workspaceMember(w, r, workspaceID)
	if !ok {
		return
	}
	if !roleAllowed(member.Role, "owner", "admin") {
		writeError(w, http.StatusForbidden, "only workspace owner/admin can configure the graph memory profile")
		return
	}

	var req updateGraphMemoryProfileRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid request body")
		return
	}
	req.MemoryType = strings.ToLower(strings.TrimSpace(req.MemoryType))
	if req.MemoryType == "" {
		req.MemoryType = defaultGraphMemoryType
	}
	if !validGraphMemoryType(req.MemoryType) {
		writeError(w, http.StatusBadRequest, "memory_type must be 'legacy' or 'graph'")
		return
	}
	if req.GraphMemoryMode != nil {
		mode := strings.ToLower(strings.TrimSpace(*req.GraphMemoryMode))
		if !validGraphMemoryMode(mode) {
			writeError(w, http.StatusBadRequest, "graph_memory_mode must be 'inject' or 'agent'")
			return
		}
		req.GraphMemoryMode = &mode
	}

	current, err := h.Queries.GetGraphMemoryProfile(r.Context(), parseUUID(workspaceID))
	if err != nil && !errors.Is(err, pgx.ErrNoRows) {
		writeError(w, http.StatusInternalServerError, "failed to load graph memory profile")
		return
	}
	exists := err == nil

	// Switching TO graph requires explicit admin confirmation of the
	// empty-start and no-fallback contract (spec §11). Knob updates and
	// graph->legacy switches do not.
	if req.MemoryType == "graph" && !req.ConfirmEmptyStart {
		if !exists || current.MemoryType != "graph" {
			writeError(w, http.StatusBadRequest, "confirm_empty_start_required: switching to graph memory starts with empty graphs and never falls back to legacy project/channel/daily memory; resend with confirm_empty_start=true")
			return
		}
	}

	// CAS contract (spec §16): updates to an existing row must carry the
	// current config_version; creates must not claim one.
	if exists {
		if req.ConfigVersion == nil || *req.ConfigVersion != current.ConfigVersion {
			writeError(w, http.StatusConflict, "config_version conflict: reload the profile and retry the write")
			return
		}
	} else if req.ConfigVersion != nil && *req.ConfigVersion != 0 {
		writeError(w, http.StatusConflict, "config_version conflict: profile does not exist yet")
		return
	}

	limits := h.graphMemoryLimits()
	tunables := graphMemoryTunablesFromRequest(limits.Defaults, current, exists, req)
	if err := limits.Validate(tunables); err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	diveModel, diveProvider := "", ""
	tttEnabled := false
	if exists {
		diveModel, diveProvider = current.DiveModel, current.DiveProvider
		tttEnabled = current.TttEnabled
	}
	if req.TTTEnabled != nil {
		tttEnabled = *req.TTTEnabled
	}
	if req.DiveModel != nil {
		diveModel = strings.TrimSpace(*req.DiveModel)
	}
	if req.DiveProvider != nil {
		diveProvider = strings.TrimSpace(*req.DiveProvider)
	}
	if err := limits.ValidateDiveOverride(diveProvider, diveModel); err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}

	graphMemoryMode := defaultGraphMemoryMode
	memoryAgentRuntimeID := pgtype.UUID{}
	memoryAgentModel, memoryAgentThinking := "", ""
	recallTTTEnabled, consolidationTTTEnabled := tttEnabled, tttEnabled
	idleGraceSeconds, maxNodesPerCall, maxNodesPerMinute := int32(120), int32(4), int32(30)
	maxContinuousTurnSeconds, maxTokensPerHour := int32(600), int64(200000)
	if exists {
		graphMemoryMode = current.GraphMemoryMode
		memoryAgentRuntimeID = current.MemoryAgentRuntimeID
		memoryAgentModel = current.MemoryAgentModel
		memoryAgentThinking = current.MemoryAgentThinking
		recallTTTEnabled = current.RecallTttEnabled
		consolidationTTTEnabled = current.ConsolidationTttEnabled
		idleGraceSeconds = current.MemoryAgentIdleGraceSeconds
		maxNodesPerCall = current.MemoryAgentMaxNodesPerCall
		maxNodesPerMinute = current.MemoryAgentMaxNodesPerMinute
		maxContinuousTurnSeconds = current.MemoryAgentMaxContinuousTurnSeconds
		maxTokensPerHour = current.MemoryAgentMaxTokensPerHour
	}
	if req.GraphMemoryMode != nil {
		graphMemoryMode = *req.GraphMemoryMode
	}
	if req.MemoryAgentRuntimeID != nil {
		runtimeID := strings.TrimSpace(*req.MemoryAgentRuntimeID)
		if runtimeID == "" {
			memoryAgentRuntimeID = pgtype.UUID{}
		} else if parsed, ok := parseUUIDOrBadRequest(w, runtimeID, "memory_agent_runtime_id"); ok {
			memoryAgentRuntimeID = parsed
		} else {
			return
		}
	}
	if req.MemoryAgentModel != nil {
		memoryAgentModel = strings.TrimSpace(*req.MemoryAgentModel)
	}
	if req.MemoryAgentThinking != nil {
		memoryAgentThinking = strings.TrimSpace(*req.MemoryAgentThinking)
	}
	if req.TTTEnabled != nil {
		recallTTTEnabled = *req.TTTEnabled
		consolidationTTTEnabled = *req.TTTEnabled
	}
	if req.RecallTTTEnabled != nil {
		recallTTTEnabled = *req.RecallTTTEnabled
	}
	if req.ConsolidationTTTEnabled != nil {
		consolidationTTTEnabled = *req.ConsolidationTTTEnabled
	}
	if req.MemoryAgentIdleGraceSeconds != nil {
		idleGraceSeconds = *req.MemoryAgentIdleGraceSeconds
	}
	if req.MemoryAgentMaxNodesPerCall != nil {
		maxNodesPerCall = *req.MemoryAgentMaxNodesPerCall
	}
	if req.MemoryAgentMaxNodesPerMinute != nil {
		maxNodesPerMinute = *req.MemoryAgentMaxNodesPerMinute
	}
	if req.MemoryAgentMaxContinuousTurnSeconds != nil {
		maxContinuousTurnSeconds = *req.MemoryAgentMaxContinuousTurnSeconds
	}
	if req.MemoryAgentMaxTokensPerHour != nil {
		maxTokensPerHour = *req.MemoryAgentMaxTokensPerHour
	}
	if idleGraceSeconds < 30 || idleGraceSeconds > 3600 || maxNodesPerCall < 1 || maxNodesPerCall > 16 ||
		maxNodesPerMinute < 1 || maxNodesPerMinute > 600 || maxContinuousTurnSeconds < 30 ||
		maxContinuousTurnSeconds > 3600 || maxTokensPerHour < 1000 || maxTokensPerHour > 10000000 {
		writeError(w, http.StatusBadRequest, "memory agent quota is outside server limits")
		return
	}

	params := graphMemoryProfileParams(parseUUID(workspaceID), req.MemoryType, tunables, recallTTTEnabled, diveModel, diveProvider)
	params.GraphMemoryMode = graphMemoryMode
	params.MemoryAgentRuntimeID = memoryAgentRuntimeID
	params.MemoryAgentModel = memoryAgentModel
	params.MemoryAgentThinking = memoryAgentThinking
	params.RecallTttEnabled = recallTTTEnabled
	params.ConsolidationTttEnabled = consolidationTTTEnabled
	params.MemoryAgentIdleGraceSeconds = idleGraceSeconds
	params.MemoryAgentMaxNodesPerCall = maxNodesPerCall
	params.MemoryAgentMaxNodesPerMinute = maxNodesPerMinute
	params.MemoryAgentMaxContinuousTurnSeconds = maxContinuousTurnSeconds
	params.MemoryAgentMaxTokensPerHour = maxTokensPerHour
	if !exists {
		row, err := h.Queries.CreateGraphMemoryProfile(r.Context(), db.CreateGraphMemoryProfileParams(params))
		if err != nil {
			writeError(w, http.StatusInternalServerError, "failed to save graph memory profile")
			return
		}
		h.reconcileGraphMemoryWorkspaceChannels(r.Context(), workspaceID)
		writeJSON(w, http.StatusOK, graphMemoryProfileFromRow(db.GraphMemoryProfile(row)))
		return
	}
	casParams := db.UpdateGraphMemoryProfileCASParams{
		WorkspaceID:                         params.WorkspaceID,
		ConfigVersion:                       current.ConfigVersion,
		MemoryType:                          params.MemoryType,
		ExploreAgents:                       params.ExploreAgents,
		ExploreMaxRounds:                    params.ExploreMaxRounds,
		TttEnabled:                          params.TttEnabled,
		RecallTttEnabled:                    params.RecallTttEnabled,
		ConsolidationTttEnabled:             params.ConsolidationTttEnabled,
		GraphMemoryMode:                     params.GraphMemoryMode,
		MemoryAgentRuntimeID:                params.MemoryAgentRuntimeID,
		MemoryAgentModel:                    params.MemoryAgentModel,
		MemoryAgentThinking:                 params.MemoryAgentThinking,
		MemoryAgentIdleGraceSeconds:         params.MemoryAgentIdleGraceSeconds,
		MemoryAgentMaxNodesPerCall:          params.MemoryAgentMaxNodesPerCall,
		MemoryAgentMaxNodesPerMinute:        params.MemoryAgentMaxNodesPerMinute,
		MemoryAgentMaxContinuousTurnSeconds: params.MemoryAgentMaxContinuousTurnSeconds,
		MemoryAgentMaxTokensPerHour:         params.MemoryAgentMaxTokensPerHour,
		ExploreNodesPerExpansion:            params.ExploreNodesPerExpansion,
		MaxHierarchyFanout:                  params.MaxHierarchyFanout,
		MaxRelationEdgesPerNode:             params.MaxRelationEdgesPerNode,
		DiveMaxRounds:                       params.DiveMaxRounds,
		DiveMaxViewedNodes:                  params.DiveMaxViewedNodes,
		DiveMaxSourceFiles:                  params.DiveMaxSourceFiles,
		DiveTimeoutSeconds:                  params.DiveTimeoutSeconds,
		WRound:                              params.WRound,
		SourceMaxFileBytes:                  params.SourceMaxFileBytes,
		SourceMaxTotalBytes:                 params.SourceMaxTotalBytes,
		SourceMaxPdfPages:                   params.SourceMaxPdfPages,
		SourceMaxAvSeconds:                  params.SourceMaxAvSeconds,
		SourceMaxImageMegapixels:            params.SourceMaxImageMegapixels,
		DiveModel:                           params.DiveModel,
		DiveProvider:                        params.DiveProvider,
	}
	row, err := h.Queries.UpdateGraphMemoryProfileCAS(r.Context(), casParams)
	if errors.Is(err, pgx.ErrNoRows) {
		// Lost a concurrent-update race between the read and the CAS write.
		writeError(w, http.StatusConflict, "config_version conflict: reload the profile and retry the write")
		return
	}
	if err != nil {
		writeError(w, http.StatusInternalServerError, "failed to save graph memory profile")
		return
	}
	h.reconcileGraphMemoryWorkspaceChannels(r.Context(), workspaceID)
	writeJSON(w, http.StatusOK, graphMemoryProfileFromRow(db.GraphMemoryProfile(row)))
}

// graphMemoryTunablesFromRequest resolves the effective tunables for a write:
// env/built-in defaults on create, the persisted row on update, with non-nil
// request fields applied on top.
func graphMemoryTunablesFromRequest(defaults service.GraphMemoryTunables, current db.GraphMemoryProfile, exists bool, req updateGraphMemoryProfileRequest) service.GraphMemoryTunables {
	t := defaults
	if exists {
		t = service.GraphMemoryTunables{
			TTTConcurrency:           int(current.ExploreAgents),
			ExploreMaxRounds:         int(current.ExploreMaxRounds),
			ExploreNodesPerExpansion: int(current.ExploreNodesPerExpansion),
			MaxHierarchyFanout:       int(current.MaxHierarchyFanout),
			MaxRelationEdgesPerNode:  int(current.MaxRelationEdgesPerNode),
			DiveMaxRounds:            int(current.DiveMaxRounds),
			DiveMaxViewedNodes:       int(current.DiveMaxViewedNodes),
			DiveMaxSourceFiles:       int(current.DiveMaxSourceFiles),
			DiveTimeoutSeconds:       int(current.DiveTimeoutSeconds),
			WRound:                   current.WRound,
			SourceMaxFileBytes:       current.SourceMaxFileBytes,
			SourceMaxTotalBytes:      current.SourceMaxTotalBytes,
			SourceMaxPDFPages:        int(current.SourceMaxPdfPages),
			SourceMaxAVSeconds:       int(current.SourceMaxAvSeconds),
			SourceMaxImageMegapixels: int(current.SourceMaxImageMegapixels),
		}
	}
	// The pre-existing explore knobs stay required request fields.
	t.TTTConcurrency = int(req.ExploreAgents)
	t.ExploreMaxRounds = int(req.ExploreMaxRounds)
	if req.ExploreNodesPerExpansion != nil {
		t.ExploreNodesPerExpansion = int(*req.ExploreNodesPerExpansion)
	}
	if req.MaxHierarchyFanout != nil {
		t.MaxHierarchyFanout = int(*req.MaxHierarchyFanout)
	}
	if req.MaxRelationEdgesPerNode != nil {
		t.MaxRelationEdgesPerNode = int(*req.MaxRelationEdgesPerNode)
	}
	if req.DiveMaxRounds != nil {
		t.DiveMaxRounds = int(*req.DiveMaxRounds)
	}
	if req.DiveMaxViewedNodes != nil {
		t.DiveMaxViewedNodes = int(*req.DiveMaxViewedNodes)
	}
	if req.DiveMaxSourceFiles != nil {
		t.DiveMaxSourceFiles = int(*req.DiveMaxSourceFiles)
	}
	if req.DiveTimeoutSeconds != nil {
		t.DiveTimeoutSeconds = int(*req.DiveTimeoutSeconds)
	}
	if req.WRound != nil {
		t.WRound = *req.WRound
	}
	if req.SourceMaxFileBytes != nil {
		t.SourceMaxFileBytes = *req.SourceMaxFileBytes
	}
	if req.SourceMaxTotalBytes != nil {
		t.SourceMaxTotalBytes = *req.SourceMaxTotalBytes
	}
	if req.SourceMaxPDFPages != nil {
		t.SourceMaxPDFPages = int(*req.SourceMaxPDFPages)
	}
	if req.SourceMaxAVSeconds != nil {
		t.SourceMaxAVSeconds = int(*req.SourceMaxAVSeconds)
	}
	if req.SourceMaxImageMegapixels != nil {
		t.SourceMaxImageMegapixels = int(*req.SourceMaxImageMegapixels)
	}
	return t
}

func graphMemoryProfileParams(workspaceID pgtype.UUID, memoryType string, t service.GraphMemoryTunables, tttEnabled bool, diveModel, diveProvider string) db.CreateGraphMemoryProfileParams {
	return db.CreateGraphMemoryProfileParams{
		WorkspaceID:                         workspaceID,
		MemoryType:                          memoryType,
		ExploreAgents:                       int32(t.TTTConcurrency),
		ExploreMaxRounds:                    int32(t.ExploreMaxRounds),
		TttEnabled:                          tttEnabled,
		RecallTttEnabled:                    tttEnabled,
		ConsolidationTttEnabled:             tttEnabled,
		GraphMemoryMode:                     defaultGraphMemoryMode,
		MemoryAgentIdleGraceSeconds:         120,
		MemoryAgentMaxNodesPerCall:          4,
		MemoryAgentMaxNodesPerMinute:        30,
		MemoryAgentMaxContinuousTurnSeconds: 600,
		MemoryAgentMaxTokensPerHour:         200000,
		ExploreNodesPerExpansion:            int32(t.ExploreNodesPerExpansion),
		MaxHierarchyFanout:                  int32(t.MaxHierarchyFanout),
		MaxRelationEdgesPerNode:             int32(t.MaxRelationEdgesPerNode),
		DiveMaxRounds:                       int32(t.DiveMaxRounds),
		DiveMaxViewedNodes:                  int32(t.DiveMaxViewedNodes),
		DiveMaxSourceFiles:                  int32(t.DiveMaxSourceFiles),
		DiveTimeoutSeconds:                  int32(t.DiveTimeoutSeconds),
		WRound:                              t.WRound,
		SourceMaxFileBytes:                  t.SourceMaxFileBytes,
		SourceMaxTotalBytes:                 t.SourceMaxTotalBytes,
		SourceMaxPdfPages:                   int32(t.SourceMaxPDFPages),
		SourceMaxAvSeconds:                  int32(t.SourceMaxAVSeconds),
		SourceMaxImageMegapixels:            int32(t.SourceMaxImageMegapixels),
		DiveModel:                           diveModel,
		DiveProvider:                        diveProvider,
	}
}

// graphMemoryProfileValues is the effective per-workspace graph memory
// profile delivered to runtimes. A zero value means "no workspace profile":
// every field then falls back to the process env default (spec §10).
type graphMemoryProfileValues struct {
	memoryType       string
	exploreAgents    int32
	exploreMaxRounds int32
}

// graphMemoryProfileForWorkspace loads the workspace's profile row. Lookup
// errors fail open to the zero value so a transient DB hiccup never flips a
// workspace's memory pipeline.
// applyGraphMemoryProfileToDelivery stamps the workspace's effective graph
// memory profile onto an outgoing Agent delivery (spec §10): the daemon
// caches it per workspace for the resident-message memory path. A workspace
// without a profile row leaves the payload untouched (daemon env defaults
// apply).
func (h *Handler) applyGraphMemoryProfileToDelivery(ctx context.Context, workspaceID string, delivery *protocol.AgentDeliverPayload) {
	if delivery == nil {
		return
	}
	if profile := h.graphMemoryProfileForWorkspace(ctx, parseUUID(workspaceID)); profile.memoryType != "" {
		delivery.MemoryType = profile.memoryType
		delivery.ExploreAgents = int(profile.exploreAgents)
		delivery.ExploreMaxRounds = int(profile.exploreMaxRounds)
	}
}

func (h *Handler) graphMemoryProfileForWorkspace(ctx context.Context, workspaceID pgtype.UUID) graphMemoryProfileValues {
	profile, err := h.Queries.GetGraphMemoryProfile(ctx, workspaceID)
	if err != nil || !validGraphMemoryType(profile.MemoryType) {
		return graphMemoryProfileValues{}
	}
	return graphMemoryProfileValues{
		memoryType:       profile.MemoryType,
		exploreAgents:    profile.ExploreAgents,
		exploreMaxRounds: profile.ExploreMaxRounds,
	}
}
