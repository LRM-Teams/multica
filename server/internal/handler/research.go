package handler

import (
	"encoding/json"
	"errors"
	"log/slog"
	"net/http"
	"strings"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/multica-ai/multica/server/internal/researchrun"
	db "github.com/multica-ai/multica/server/pkg/db/generated"
)

type ResearchSessionResponse struct {
	ID                  string  `json:"id"`
	WorkspaceID         string  `json:"workspace_id"`
	FleetID             string  `json:"fleet_id"`
	CreatedBy           string  `json:"created_by"`
	Title               string  `json:"title"`
	Goal                string  `json:"goal"`
	Status              string  `json:"status"`
	CurrentStage        string  `json:"current_stage"`
	DepthTier           string  `json:"depth_tier"`
	ProductRound        int32   `json:"product_round"`
	ProductRoundBudget  int32   `json:"product_round_budget"`
	UnattendedEnabled   bool    `json:"unattended_enabled"`
	MaxOpenBranches     int32   `json:"max_open_branches"`
	SingleLineConfirmed bool    `json:"single_line_confirmed"`
	UnattendedAutoSteps int32   `json:"unattended_auto_steps"`
	LastUserActivityAt  *string `json:"last_user_activity_at,omitempty"`
	ProjectID           *string `json:"project_id"`
	ChannelID           *string `json:"channel_id"`
	HandoffSummary      *string `json:"handoff_summary"`
	CreatedAt           string  `json:"created_at"`
	UpdatedAt           string  `json:"updated_at"`
}

// ResearchFleetPreviewMember is a list-row avatar stack item (LRM-805).
type ResearchFleetPreviewMember struct {
	AgentID     string  `json:"agent_id"`
	Name        string  `json:"name,omitempty"`
	DisplayName string  `json:"display_name,omitempty"`
	AvatarURL   *string `json:"avatar_url"`
	Role        string  `json:"role,omitempty"`
	IsLead      bool    `json:"is_lead,omitempty"`
}

// ResearchSessionListItem extends the session row with a workspace fleet preview.
type ResearchSessionListItem struct {
	ResearchSessionResponse
	FleetPreview      []ResearchFleetPreviewMember `json:"fleet_preview"`
	ListProgress      *ResearchSessionListProgress `json:"list_progress,omitempty"`
	ActiveAssignments []ResearchActiveAssignment   `json:"active_assignments"`
	LatestOutcomes    []ResearchLatestOutcome      `json:"latest_outcomes"`
}

type ResearchSessionListProgress struct {
	TaskTotal          int64   `json:"task_total"`
	TaskCompleted      int64   `json:"task_completed"`
	TaskRunning        int64   `json:"task_running"`
	TaskBlocked        int64   `json:"task_blocked"`
	EvidenceCount      int64   `json:"evidence_count"`
	TodayEvidenceCount int64   `json:"today_evidence_count"`
	NodeCount          int64   `json:"node_count"`
	OpenQuestionCount  int64   `json:"open_question_count"`
	AwaitingUserAction bool    `json:"awaiting_user_action"`
	AttentionKind      *string `json:"attention_kind,omitempty"`
	Recoverable        bool    `json:"recoverable"`
	LastProgressAt     *string `json:"last_progress_at,omitempty"`
}

type ResearchActiveAssignment struct {
	AgentID   string `json:"agent_id"`
	Role      string `json:"role"`
	TaskID    string `json:"task_id"`
	TaskTitle string `json:"task_title"`
	State     string `json:"state"`
}

type ResearchLatestOutcome struct {
	ID                string  `json:"id"`
	Kind              string  `json:"kind"`
	Title             string  `json:"title"`
	Summary           *string `json:"summary,omitempty"`
	VerificationState string  `json:"verification_state"`
	CreatedAt         string  `json:"created_at"`
}

type ResearchSessionSnapshot struct {
	Session       ResearchSessionResponse        `json:"session"`
	Fleet         ResearchFleetResponse          `json:"fleet"`
	Nodes         []ResearchGraphNodeResp        `json:"nodes"`
	Edges         []ResearchGraphEdgeResp        `json:"edges"`
	Sources       []ResearchSourceResp           `json:"sources"`
	Report        *ResearchReportResp            `json:"report"`
	Evals         []ResearchStageEvalResp        `json:"evals"`
	Messages      []ResearchMessageResp          `json:"messages"`
	ProductRounds []ResearchProductRoundCardResp `json:"product_rounds"`
	// ThoughtStrategies powers the LRM-1306 side panel (LRM-1318). Omit-empty
	// list is always present (possibly length 0); FE must not invent rows.
	ThoughtStrategies []ResearchThoughtStrategyResp `json:"thought_strategies"`
	Run               *researchrun.RunSnapshot      `json:"run,omitempty"`
}

// ResearchNodeContentFaces is the four content-face projection (LRM-1317 / LRM-1308).
// Always present on nodes[]; empty strings are neutral — FE must not invent copy.
type ResearchNodeContentFaces struct {
	Goal              string `json:"goal"`
	OperationApproach string `json:"operation_approach"`
	ResearchApproach  string `json:"research_approach"`
	Result            string `json:"result"`
}

type ResearchGraphNodeResp struct {
	ID           string          `json:"id"`
	SessionID    string          `json:"session_id"`
	NodeType     string          `json:"node_type"`
	Title        string          `json:"title"`
	Summary      string          `json:"summary"`
	Status       string          `json:"status"`
	ActorAgentID *string         `json:"actor_agent_id"`
	Payload      json.RawMessage `json:"payload"`
	// Confidence is projected from payload.confidence when present (LRM-806).
	Confidence *float64 `json:"confidence,omitempty"`
	// LRM-1278 tree + quality projection (snapshot authoritative; WS may be partial).
	ParentID        *string  `json:"parent_id"`
	ChildIDs        []string `json:"child_ids"`
	ChildCount      int      `json:"child_count"`
	DescendantCount int      `json:"descendant_count"`
	ThemeKey        string   `json:"theme_key"`
	Phase           string   `json:"phase,omitempty"`
	Assessment      string   `json:"assessment"`
	Reason          *string  `json:"reason,omitempty"`
	EvidenceSummary *string  `json:"evidence_summary,omitempty"`
	// LRM-1317 content faces + abandon reason (payload projection; no migration).
	Content       ResearchNodeContentFaces `json:"content"`
	AbandonReason *string                  `json:"abandon_reason,omitempty"`
	CreatedAt     string                   `json:"created_at"`
	UpdatedAt     string                   `json:"updated_at"`
}

type ResearchGraphEdgeResp struct {
	ID         string `json:"id"`
	SessionID  string `json:"session_id"`
	FromNodeID string `json:"from_node_id"`
	ToNodeID   string `json:"to_node_id"`
	EdgeType   string `json:"edge_type"`
	CreatedAt  string `json:"created_at"`
}

type ResearchSourceResp struct {
	ID                string          `json:"id"`
	SessionID         string          `json:"session_id"`
	URL               string          `json:"url"`
	Title             string          `json:"title"`
	SourceClass       string          `json:"source_class"`
	CredibilityWeight float64         `json:"credibility_weight"`
	Stance            string          `json:"stance"`
	Relevance         float64         `json:"relevance"`
	Summary           string          `json:"summary"`
	Excerpt           string          `json:"excerpt"`
	Payload           json.RawMessage `json:"payload"`
	CreatedAt         string          `json:"created_at"`
	UpdatedAt         string          `json:"updated_at"`
}

type ResearchReportResp struct {
	ID         string          `json:"id"`
	SessionID  string          `json:"session_id"`
	Revision   int32           `json:"revision"`
	ContentMD  string          `json:"content_md"`
	Structured json.RawMessage `json:"structured"`
	CreatedAt  string          `json:"created_at"`
	UpdatedAt  string          `json:"updated_at"`
}

type ResearchStageEvalResp struct {
	ID          string          `json:"id"`
	SessionID   string          `json:"session_id"`
	Stage       string          `json:"stage"`
	Passed      bool            `json:"passed"`
	Score       float64         `json:"score"`
	Findings    json.RawMessage `json:"findings"`
	Remediation string          `json:"remediation"`
	CreatedAt   string          `json:"created_at"`
}

type ResearchMessageResp struct {
	ID            string                 `json:"id"`
	SessionID     string                 `json:"session_id"`
	SenderType    string                 `json:"sender_type"`
	SenderID      *string                `json:"sender_id"`
	TargetAgentID *string                `json:"target_agent_id"`
	Body          string                 `json:"body"`
	CardKind      string                 `json:"card_kind"`
	Meta          json.RawMessage        `json:"meta"`
	MatchDecision *ResearchMatchDecision `json:"match_decision,omitempty"`
	CreatedAt     string                 `json:"created_at"`
}

func researchSessionToResponse(s db.ResearchSession) ResearchSessionResponse {
	resp := ResearchSessionResponse{
		ID:                  uuidToString(s.ID),
		WorkspaceID:         uuidToString(s.WorkspaceID),
		FleetID:             uuidToString(s.FleetID),
		CreatedBy:           uuidToString(s.CreatedBy),
		Title:               s.Title,
		Goal:                s.Goal,
		Status:              s.Status,
		CurrentStage:        s.CurrentStage,
		DepthTier:           s.DepthTier,
		ProductRound:        s.ProductRound,
		ProductRoundBudget:  s.ProductRoundBudget,
		UnattendedEnabled:   s.UnattendedEnabled,
		MaxOpenBranches:     s.MaxOpenBranches,
		SingleLineConfirmed: s.SingleLineConfirmed,
		UnattendedAutoSteps: s.UnattendedAutoSteps,
		ProjectID:           uuidToPtr(s.ProjectID),
		ChannelID:           uuidToPtr(s.ChannelID),
		HandoffSummary:      textToPtr(s.HandoffSummary),
		CreatedAt:           timestampToString(s.CreatedAt),
		UpdatedAt:           timestampToString(s.UpdatedAt),
	}
	if s.LastUserActivityAt.Valid {
		ts := timestampToString(s.LastUserActivityAt)
		resp.LastUserActivityAt = &ts
	}
	return resp
}

// taskBoundResearchSessionResponse retains the V1-V5 compatibility family but
// projects only fields frozen in the manifested Run representation. Live
// routing, activity, handoff, timestamps, and unattended counters are absent.
func taskBoundResearchSessionResponse(run researchrun.Run) ResearchSessionResponse {
	return ResearchSessionResponse{
		ID:           run.SessionID,
		WorkspaceID:  run.WorkspaceID,
		FleetID:      run.FleetID,
		CreatedBy:    run.CreatedBy,
		Title:        run.Title,
		Goal:         run.Goal,
		Status:       string(run.Status),
		CurrentStage: run.CurrentStage,
		DepthTier:    run.DepthTier,
	}
}

// taskBoundResearchFleetResponse rebuilds the compatibility shape exclusively
// from the hashed principal header. Member-row IDs, mutable Agent profiles,
// runtime configuration, and Fleet timestamps are deliberately unavailable.
func taskBoundResearchFleetResponse(run researchrun.Run, members []researchrun.FleetMember) ResearchFleetResponse {
	response := ResearchFleetResponse{
		ID:          run.FleetID,
		WorkspaceID: run.WorkspaceID,
		Members:     make([]ResearchFleetMemberResp, 0, len(members)),
	}
	seenRoles := make(map[string]struct{}, len(members))
	for _, member := range members {
		if member.Status == "archived" {
			continue
		}
		if _, duplicate := seenRoles[member.Role]; duplicate {
			continue
		}
		seenRoles[member.Role] = struct{}{}
		response.Members = append(response.Members, ResearchFleetMemberResp{
			AgentID: member.AgentID,
			Role:    member.Role,
			Status:  member.Status,
			IsLead:  member.IsLead,
		})
		if member.IsLead && response.LeadAgentID == nil {
			agentID := member.AgentID
			response.LeadAgentID = &agentID
		}
	}
	return response
}

func (h *Handler) ListResearchSessions(w http.ResponseWriter, r *http.Request) {
	workspaceID := h.resolveWorkspaceID(r)
	wsUUID, ok := parseUUIDOrBadRequest(w, workspaceID, "workspace_id")
	if !ok {
		return
	}
	var preview []ResearchFleetPreviewMember
	if userID := requestUserID(r); userID != "" {
		fleet, members, err := h.ensureResearchFleet(r.Context(), wsUUID, parseUUID(userID))
		if err == nil {
			preview = h.researchFleetPreview(r.Context(), fleet, members, 5)
		}
	} else if fleet, err := h.Queries.GetResearchFleetByWorkspace(r.Context(), wsUUID); err == nil {
		members, _ := h.Queries.ListResearchFleetMembers(r.Context(), db.ListResearchFleetMembersParams{
			FleetID:     fleet.ID,
			WorkspaceID: wsUUID,
		})
		preview = h.researchFleetPreview(r.Context(), fleet, members, 5)
	}
	if preview == nil {
		preview = []ResearchFleetPreviewMember{}
	}
	rows, err := h.Queries.ListResearchSessions(r.Context(), wsUUID)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "failed to list research sessions")
		return
	}
	progressRows, err := h.Queries.ListResearchSessionProgress(r.Context(), wsUUID)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "failed to load research session progress")
		return
	}
	progressBySession := make(map[string]*ResearchSessionListProgress, len(progressRows))
	for _, row := range progressRows {
		sessionID := uuidToString(row.SessionID)
		progress := &ResearchSessionListProgress{
			TaskTotal:          row.TaskTotal,
			TaskCompleted:      row.TaskCompleted,
			TaskRunning:        row.TaskRunning,
			TaskBlocked:        row.TaskBlocked,
			EvidenceCount:      row.EvidenceCount,
			TodayEvidenceCount: row.TodayEvidenceCount,
			NodeCount:          row.NodeCount,
			OpenQuestionCount:  row.OpenQuestionCount,
			AwaitingUserAction: row.AwaitingUserAction,
			Recoverable:        row.Recoverable,
		}
		if row.AttentionKind != "" {
			progress.AttentionKind = &row.AttentionKind
		}
		if row.LastProgressAt.Valid {
			lastProgressAt := timestampToString(row.LastProgressAt)
			progress.LastProgressAt = &lastProgressAt
		}
		progressBySession[sessionID] = progress
	}
	assignmentRows, err := h.Queries.ListResearchActiveAssignments(r.Context(), wsUUID)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "failed to load research assignments")
		return
	}
	assignmentsBySession := make(map[string][]ResearchActiveAssignment)
	for _, row := range assignmentRows {
		sessionID := uuidToString(row.SessionID)
		assignmentsBySession[sessionID] = append(assignmentsBySession[sessionID], ResearchActiveAssignment{
			AgentID: uuidToString(row.AgentID), Role: row.Role, TaskID: uuidToString(row.TaskID), TaskTitle: row.TaskTitle, State: row.State,
		})
	}
	outcomeRows, err := h.Queries.ListResearchLatestOutcomes(r.Context(), wsUUID)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "failed to load research outcomes")
		return
	}
	outcomesBySession := make(map[string][]ResearchLatestOutcome)
	for _, row := range outcomeRows {
		sessionID := uuidToString(row.SessionID)
		outcome := ResearchLatestOutcome{ID: uuidToString(row.ID), Kind: row.Kind, Title: row.Title, VerificationState: row.VerificationState, CreatedAt: timestampToString(row.CreatedAt)}
		if row.Summary != "" {
			outcome.Summary = &row.Summary
		}
		outcomesBySession[sessionID] = append(outcomesBySession[sessionID], outcome)
	}
	out := make([]ResearchSessionListItem, 0, len(rows))
	for _, row := range rows {
		sessionID := uuidToString(row.ID)
		assignments := assignmentsBySession[sessionID]
		if assignments == nil {
			assignments = []ResearchActiveAssignment{}
		}
		outcomes := outcomesBySession[sessionID]
		if outcomes == nil {
			outcomes = []ResearchLatestOutcome{}
		}
		out = append(out, ResearchSessionListItem{
			ResearchSessionResponse: researchSessionToResponse(row),
			FleetPreview:            preview,
			ListProgress:            progressBySession[sessionID],
			ActiveAssignments:       assignments,
			LatestOutcomes:          outcomes,
		})
	}
	writeJSON(w, http.StatusOK, map[string]any{"sessions": out})
}

type createResearchSessionRequest struct {
	Goal          string                 `json:"goal"`
	Title         string                 `json:"title"`
	DepthTier     string                 `json:"depth_tier"` // shallow|standard|deep — LRM-676 product-round caps
	Language      string                 `json:"language"`
	SourceWeights *researchSourceWeights `json:"source_weights"`
}

type researchSourceWeights struct {
	Primary   float64 `json:"primary"`
	Secondary float64 `json:"secondary"`
	Community float64 `json:"community"`
}

func (h *Handler) CreateResearchSession(w http.ResponseWriter, r *http.Request) {
	if h.ResearchRun == nil {
		writeError(w, http.StatusServiceUnavailable, "research run engine is unavailable")
		return
	}
	workspaceID := h.resolveWorkspaceID(r)
	wsUUID, ok := parseUUIDOrBadRequest(w, workspaceID, "workspace_id")
	if !ok {
		return
	}
	userID, userOK := requireUserID(w, r)
	if !userOK {
		return
	}
	var req createResearchSessionRequest
	if !decodeResearchJSON(w, r, &req) {
		return
	}
	req.Goal = strings.TrimSpace(req.Goal)
	if req.Goal == "" || len(req.Goal) > 32<<10 {
		writeError(w, http.StatusBadRequest, "goal is required")
		return
	}
	title := strings.TrimSpace(req.Title)
	if title == "" {
		title = truncateRunes(req.Goal, 80)
	}
	if len(title) > 1024 {
		writeError(w, http.StatusBadRequest, "title exceeds 1024 bytes")
		return
	}
	language := strings.TrimSpace(req.Language)
	if len(language) > 64 {
		writeError(w, http.StatusBadRequest, "language exceeds 64 bytes")
		return
	}
	if language == "" {
		language = "follow the user's language"
	}
	sourcePolicy := map[string]any{
		"require_snapshot":                 true,
		"prefer_primary":                   true,
		"require_independent_verification": true,
	}
	if req.SourceWeights != nil {
		weights := *req.SourceWeights
		if weights.Primary < 0 || weights.Primary > 1 || weights.Secondary < 0 || weights.Secondary > 1 || weights.Community < 0 || weights.Community > 1 {
			writeError(w, http.StatusBadRequest, "source weights must be in [0,1]")
			return
		}
		sourcePolicy["weights"] = weights
	}
	sourcePolicyJSON, err := json.Marshal(sourcePolicy)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "failed to resolve source policy")
		return
	}
	fleet, members, err := h.ensureResearchFleet(r.Context(), wsUUID, parseUUID(userID))
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	depthTier := normalizeResearchDepthTier(req.DepthTier)
	createdRun, createErr := h.ResearchRun.Create(r.Context(), researchrun.StartInput{
		WorkspaceID:        workspaceID,
		FleetID:            uuidToString(fleet.ID),
		CreatedBy:          userID,
		LeadAgentID:        uuidToString(fleet.LeadAgentID),
		Goal:               req.Goal,
		Title:              title,
		DepthTier:          depthTier,
		Language:           language,
		SourcePolicy:       sourcePolicyJSON,
		ProductRound:       1,
		ProductRoundBudget: productRoundBudgetForTier(depthTier),
	})
	if createErr != nil && createdRun.InitializedAt == nil {
		writeError(w, http.StatusInternalServerError, "failed to initialize research run")
		return
	}
	if createErr != nil {
		slog.Warn("research run initial dispatch failed", "session_id", createdRun.SessionID, "will_retry", researchRunStartWillRetry(createErr), "error", createErr)
	}
	sessionID := parseUUID(createdRun.SessionID)
	session, err := h.Queries.GetResearchSession(r.Context(), db.GetResearchSessionParams{ID: sessionID, WorkspaceID: wsUUID})
	if err != nil {
		writeError(w, http.StatusInternalServerError, "failed to load initialized research session")
		return
	}
	runSnapshot, snapshotErr := h.ResearchRun.Snapshot(r.Context(), createdRun.SessionID, workspaceID)
	if snapshotErr != nil {
		writeError(w, http.StatusInternalServerError, "failed to load initialized research run")
		return
	}

	// Return a fresh snapshot so the client can paint the kickoff graph without waiting on WS.
	// Durable run sessions use run-v2 ledger projection for nodes/edges (LRM-1401).
	dbNodes, _ := h.Queries.ListResearchGraphNodes(r.Context(), db.ListResearchGraphNodesParams{SessionID: session.ID, WorkspaceID: wsUUID})
	dbEdges, _ := h.Queries.ListResearchGraphEdges(r.Context(), db.ListResearchGraphEdgesParams{SessionID: session.ID, WorkspaceID: wsUUID})
	messages, _ := h.Queries.ListResearchMessages(r.Context(), db.ListResearchMessagesParams{SessionID: session.ID, WorkspaceID: wsUUID})
	graphNodes, graphEdges := projectRunV2Graph(runSnapshot)
	if len(graphNodes) == 0 {
		graphNodes = mapGraphNodes(dbNodes, dbEdges)
		graphEdges = mapEdges(dbEdges)
	}
	response := map[string]any{
		"session":  researchSessionToResponse(session),
		"fleet":    h.researchFleetToResponse(r.Context(), fleet, members),
		"nodes":    graphNodes,
		"edges":    graphEdges,
		"messages": mapMessages(messages),
		"run":      runSnapshot,
	}
	if createErr != nil {
		if researchRunStartWillRetry(createErr) {
			response["warning"] = "research run was persisted but immediate dispatch failed; the reconciler will retry"
		} else {
			response["warning"] = "research run was created but initial dispatch failed and will not be retried automatically; review run diagnostics before retrying"
		}
	}
	writeJSON(w, http.StatusCreated, response)
}

func researchRunStartWillRetry(err error) bool {
	if err == nil || errors.Is(err, researchrun.ErrCapabilityUnavailable) {
		return false
	}
	var classified interface{ Retryable() bool }
	if errors.As(err, &classified) {
		return classified.Retryable()
	}
	return true
}

func truncateRunes(s string, n int) string {
	r := []rune(s)
	if len(r) <= n {
		return s
	}
	return string(r[:n]) + "…"
}

func (h *Handler) GetResearchSessionSnapshot(w http.ResponseWriter, r *http.Request) {
	h.getResearchSessionSnapshot(w, r, false)
}

func (h *Handler) getResearchSessionSnapshot(w http.ResponseWriter, r *http.Request, agentAttemptScoped bool) {
	workspaceID := h.resolveWorkspaceID(r)
	wsUUID, ok := parseUUIDOrBadRequest(w, workspaceID, "workspace_id")
	if !ok {
		return
	}
	sessionID, ok := parseUUIDOrBadRequest(w, chi.URLParam(r, "id"), "id")
	if !ok {
		return
	}
	if !agentAttemptScoped && strings.TrimSpace(r.URL.Query().Get("attempt_id")) != "" {
		writeError(w, http.StatusBadRequest, "attempt_id is only available on the Agent research route")
		return
	}
	var sessionResponse ResearchSessionResponse
	var fleetResponse ResearchFleetResponse
	var nodes []db.ResearchGraphNode
	var edges []db.ResearchGraphEdge
	var sources []db.ResearchSource
	var evals []db.ResearchStageEval
	var messages []db.ResearchMessage
	var productRounds []db.ResearchProductRoundCard
	var report *ResearchReportResp
	if !agentAttemptScoped {
		session, err := h.Queries.GetResearchSession(r.Context(), db.GetResearchSessionParams{ID: sessionID, WorkspaceID: wsUUID})
		if err != nil {
			writeError(w, http.StatusNotFound, "research session not found")
			return
		}
		fleet, err := h.Queries.GetResearchFleetByWorkspace(r.Context(), wsUUID)
		if err != nil {
			writeError(w, http.StatusInternalServerError, "research fleet missing")
			return
		}
		members, _ := h.Queries.ListResearchFleetMembers(r.Context(), db.ListResearchFleetMembersParams{FleetID: fleet.ID, WorkspaceID: wsUUID})
		sessionResponse = researchSessionToResponse(session)
		fleetResponse = h.researchFleetToResponse(r.Context(), fleet, members)
		nodes, _ = h.Queries.ListResearchGraphNodes(r.Context(), db.ListResearchGraphNodesParams{SessionID: sessionID, WorkspaceID: wsUUID})
		edges, _ = h.Queries.ListResearchGraphEdges(r.Context(), db.ListResearchGraphEdgesParams{SessionID: sessionID, WorkspaceID: wsUUID})
		sources, _ = h.Queries.ListResearchSources(r.Context(), db.ListResearchSourcesParams{SessionID: sessionID, WorkspaceID: wsUUID})
		evals, _ = h.Queries.ListResearchStageEvals(r.Context(), db.ListResearchStageEvalsParams{SessionID: sessionID, WorkspaceID: wsUUID})
		messages, _ = h.Queries.ListResearchMessages(r.Context(), db.ListResearchMessagesParams{SessionID: sessionID, WorkspaceID: wsUUID})
		productRounds, _ = h.Queries.ListResearchProductRoundCards(r.Context(), db.ListResearchProductRoundCardsParams{SessionID: sessionID, WorkspaceID: wsUUID})
		if rep, reportErr := h.Queries.GetLatestResearchReport(r.Context(), db.GetLatestResearchReportParams{SessionID: sessionID, WorkspaceID: wsUUID}); reportErr == nil {
			rr := researchReportToResp(rep)
			report = &rr
		}
	}
	var loadedRun *researchrun.RunSnapshot
	durableRun := agentAttemptScoped
	if !agentAttemptScoped {
		var ownershipErr error
		durableRun, ownershipErr = h.hasDurableResearchRun(r.Context(), wsUUID, sessionID)
		if ownershipErr != nil {
			writeError(w, http.StatusInternalServerError, "failed to inspect research run ownership")
			return
		}
	}
	if durableRun {
		if h.ResearchRun == nil {
			writeError(w, http.StatusServiceUnavailable, "research run engine is unavailable")
			return
		}
		attemptID := strings.TrimSpace(r.URL.Query().Get("attempt_id"))
		var snapshot researchrun.RunSnapshot
		var runErr error
		if attemptID != "" {
			if _, ok := parseUUIDOrBadRequest(w, attemptID, "attempt_id"); !ok {
				return
			}
			snapshot, runErr = h.ResearchRun.SnapshotForAttempt(r.Context(), uuidToString(sessionID), workspaceID, attemptID)
		} else {
			snapshot, runErr = h.ResearchRun.Snapshot(r.Context(), uuidToString(sessionID), workspaceID)
		}
		if runErr != nil {
			if errors.Is(runErr, researchrun.ErrRunNotFound) {
				writeError(w, http.StatusNotFound, "research session not found")
				return
			}
			writeError(w, http.StatusInternalServerError, "failed to load research run state")
			return
		}
		loadedRun = &snapshot
		if agentAttemptScoped {
			sessionResponse = taskBoundResearchSessionResponse(snapshot.Run)
			fleetResponse = taskBoundResearchFleetResponse(snapshot.Run, snapshot.PrincipalHeader)
		}
	}

	graphNodes := mapGraphNodes(nodes, edges)
	graphEdges := mapEdges(edges)
	if loadedRun != nil {
		// Durable run-v2: canvas truth is the deterministic ledger projection.
		// Event-log graph rows remain audit data and are not returned as the research map.
		graphNodes, graphEdges = projectRunV2Graph(*loadedRun)
	}

	mappedSources := mapSources(sources)
	mappedEvals := mapEvals(evals)
	mappedMessages := mapMessages(messages)
	mappedProductRounds := mapProductRoundCards(productRounds)
	mappedThoughtStrategies := mapThoughtStrategies(nodes)
	if agentAttemptScoped {
		// Durable Agent reads consume only the manifest-filtered canonical Run.
		// Legacy presentation rows are session-wide and are not authorized inputs.
		mappedSources = []ResearchSourceResp{}
		mappedEvals = []ResearchStageEvalResp{}
		mappedMessages = []ResearchMessageResp{}
		mappedProductRounds = []ResearchProductRoundCardResp{}
		mappedThoughtStrategies = []ResearchThoughtStrategyResp{}
		report = nil
	}

	writeJSON(w, http.StatusOK, ResearchSessionSnapshot{
		Session:           sessionResponse,
		Fleet:             fleetResponse,
		Nodes:             graphNodes,
		Edges:             graphEdges,
		Sources:           mappedSources,
		Report:            report,
		Evals:             mappedEvals,
		Messages:          mappedMessages,
		ProductRounds:     mappedProductRounds,
		ThoughtStrategies: mappedThoughtStrategies,
		Run:               loadedRun,
	})
}

func mapFrozenLegacySources(rows []researchrun.FrozenLegacySource) []ResearchSourceResp {
	out := make([]ResearchSourceResp, 0, len(rows))
	for _, row := range rows {
		out = append(out, ResearchSourceResp{ID: row.ID, SessionID: row.SessionID, URL: row.URL, Title: row.Title, SourceClass: row.SourceClass, CredibilityWeight: row.CredibilityWeight, Stance: row.Stance, Relevance: row.Relevance, Summary: row.Summary, Excerpt: row.Excerpt, Payload: row.Payload, CreatedAt: row.CreatedAt.UTC().Format(time.RFC3339Nano), UpdatedAt: row.UpdatedAt.UTC().Format(time.RFC3339Nano)})
	}
	return out
}

func mapFrozenResearchMessages(rows []researchrun.FrozenResearchMessage) []ResearchMessageResp {
	out := make([]ResearchMessageResp, 0, len(rows))
	for _, row := range rows {
		cardKind := row.CardKind
		if cardKind == "" {
			cardKind = "chat"
		}
		meta := row.Meta
		if len(meta) == 0 {
			meta = json.RawMessage(`{}`)
		}
		out = append(out, ResearchMessageResp{ID: row.ID, SessionID: row.SessionID, SenderType: row.SenderType, SenderID: optionalFrozenString(row.SenderID), TargetAgentID: optionalFrozenString(row.TargetAgentID), Body: row.Body, CardKind: cardKind, Meta: meta, MatchDecision: extractMatchDecisionFromMeta(meta, row.ID), CreatedAt: row.CreatedAt.UTC().Format(time.RFC3339Nano)})
	}
	return out
}

func mapFrozenProductRounds(rows []researchrun.FrozenProductRound) []ResearchProductRoundCardResp {
	out := make([]ResearchProductRoundCardResp, 0, len(rows))
	for _, row := range rows {
		gaps := row.CoverageGaps
		if len(gaps) == 0 {
			gaps = json.RawMessage(`[]`)
		}
		out = append(out, ResearchProductRoundCardResp{ID: row.ID, SessionID: row.SessionID, RoundNumber: row.RoundNumber, Decision: row.Decision, CoverageGaps: gaps, ConfidenceNote: row.ConfidenceNote, BudgetUsed: row.BudgetUsed, BudgetRemaining: row.BudgetRemaining, GoalPatchProposal: optionalFrozenString(row.GoalPatchProposal), NextRoundFocus: optionalFrozenString(row.NextRoundFocus), DecidedByAgentID: optionalFrozenString(row.DecidedByAgentID), CreatedAt: row.CreatedAt.UTC().Format(time.RFC3339Nano)})
	}
	return out
}

func mapFrozenResearchReport(row *researchrun.FrozenResearchReport) *ResearchReportResp {
	if row == nil {
		return nil
	}
	return &ResearchReportResp{ID: row.ID, SessionID: row.SessionID, Revision: row.Revision, ContentMD: row.ContentMD, Structured: row.Structured, CreatedAt: row.CreatedAt.UTC().Format(time.RFC3339Nano), UpdatedAt: row.UpdatedAt.UTC().Format(time.RFC3339Nano)}
}

func optionalFrozenString(value string) *string {
	value = strings.TrimSpace(value)
	if value == "" {
		return nil
	}
	return &value
}

func confidenceFromPayload(payload json.RawMessage) *float64 {
	if len(payload) == 0 {
		return nil
	}
	var obj map[string]any
	if err := json.Unmarshal(payload, &obj); err != nil {
		return nil
	}
	raw, ok := obj["confidence"]
	if !ok || raw == nil {
		return nil
	}
	switch v := raw.(type) {
	case float64:
		return &v
	case json.Number:
		f, err := v.Float64()
		if err != nil {
			return nil
		}
		return &f
	default:
		return nil
	}
}

func mapEdges(rows []db.ResearchGraphEdge) []ResearchGraphEdgeResp {
	out := make([]ResearchGraphEdgeResp, 0, len(rows))
	for _, e := range rows {
		out = append(out, ResearchGraphEdgeResp{
			ID:         uuidToString(e.ID),
			SessionID:  uuidToString(e.SessionID),
			FromNodeID: uuidToString(e.FromNodeID),
			ToNodeID:   uuidToString(e.ToNodeID),
			EdgeType:   e.EdgeType,
			CreatedAt:  timestampToString(e.CreatedAt),
		})
	}
	return out
}

func mapSources(rows []db.ResearchSource) []ResearchSourceResp {
	out := make([]ResearchSourceResp, 0, len(rows))
	for _, s := range rows {
		out = append(out, ResearchSourceResp{
			ID:                uuidToString(s.ID),
			SessionID:         uuidToString(s.SessionID),
			URL:               s.Url,
			Title:             s.Title,
			SourceClass:       s.SourceClass,
			CredibilityWeight: s.CredibilityWeight,
			Stance:            s.Stance,
			Relevance:         s.Relevance,
			Summary:           s.Summary,
			Excerpt:           s.Excerpt,
			Payload:           json.RawMessage(s.Payload),
			CreatedAt:         timestampToString(s.CreatedAt),
			UpdatedAt:         timestampToString(s.UpdatedAt),
		})
	}
	return out
}

func researchReportToResp(r db.ResearchReport) ResearchReportResp {
	return ResearchReportResp{
		ID:         uuidToString(r.ID),
		SessionID:  uuidToString(r.SessionID),
		Revision:   r.Revision,
		ContentMD:  r.ContentMd,
		Structured: json.RawMessage(r.Structured),
		CreatedAt:  timestampToString(r.CreatedAt),
		UpdatedAt:  timestampToString(r.UpdatedAt),
	}
}

func mapEvals(rows []db.ResearchStageEval) []ResearchStageEvalResp {
	out := make([]ResearchStageEvalResp, 0, len(rows))
	for _, e := range rows {
		out = append(out, ResearchStageEvalResp{
			ID:          uuidToString(e.ID),
			SessionID:   uuidToString(e.SessionID),
			Stage:       e.Stage,
			Passed:      e.Passed,
			Score:       e.Score,
			Findings:    json.RawMessage(e.Findings),
			Remediation: e.Remediation,
			CreatedAt:   timestampToString(e.CreatedAt),
		})
	}
	return out
}

func mapMessages(rows []db.ResearchMessage) []ResearchMessageResp {
	out := make([]ResearchMessageResp, 0, len(rows))
	for _, m := range rows {
		cardKind := m.CardKind
		if cardKind == "" {
			cardKind = "chat"
		}
		meta := json.RawMessage(m.Meta)
		if len(meta) == 0 {
			meta = json.RawMessage(`{}`)
		}
		msgID := uuidToString(m.ID)
		out = append(out, ResearchMessageResp{
			ID:            msgID,
			SessionID:     uuidToString(m.SessionID),
			SenderType:    m.SenderType,
			SenderID:      uuidToPtr(m.SenderID),
			TargetAgentID: uuidToPtr(m.TargetAgentID),
			Body:          m.Body,
			CardKind:      cardKind,
			Meta:          meta,
			MatchDecision: extractMatchDecisionFromMeta(meta, msgID),
			CreatedAt:     timestampToString(m.CreatedAt),
		})
	}
	return out
}
