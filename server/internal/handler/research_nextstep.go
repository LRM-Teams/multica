package handler

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"net/http"
	"os"
	"strconv"
	"strings"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/jackc/pgx/v5/pgtype"
	db "github.com/multica-ai/multica/server/pkg/db/generated"
	"github.com/multica-ai/multica/server/pkg/protocol"
)

// LRM-1076 ResearchNextStep Scheduler v0 defaults (product freeze).
const (
	defaultResearchNextStepMaxPerTick   = 3
	defaultResearchNextStepSessionLimit = 32
	defaultResearchOpenBranchBudget     = 3
	// Quiet period after last user activity before graph/source ops count as
	// unattended silent steps. The product SLO is ≥3 such steps inside a
	// 30-minute silence window; this gate starts counting once the user is quiet.
	defaultResearchUnattendedQuietAfter = 2 * time.Minute
)

type researchNextStepCandidate struct {
	Kind         string
	TargetNodeID pgtype.UUID
	Reason       string
}

func researchNextStepMaxPerTick() int {
	if v := strings.TrimSpace(os.Getenv("RESEARCH_NEXTSTEP_MAX_PER_TICK")); v != "" {
		if n, err := strconv.Atoi(v); err == nil && n > 0 && n <= 16 {
			return n
		}
	}
	return defaultResearchNextStepMaxPerTick
}

func researchUnattendedQuietAfter() time.Duration {
	if v := strings.TrimSpace(os.Getenv("RESEARCH_UNATTENDED_QUIET_AFTER")); v != "" {
		if d, err := time.ParseDuration(v); err == nil && d >= 0 {
			return d
		}
	}
	return defaultResearchUnattendedQuietAfter
}

func researchSessionIsUserQuiet(session db.ResearchSession, now time.Time) bool {
	if !session.UnattendedEnabled {
		return false
	}
	if !session.LastUserActivityAt.Valid {
		return true
	}
	return now.Sub(session.LastUserActivityAt.Time) >= researchUnattendedQuietAfter()
}

// planResearchNextSteps picks ≤max gaps: childless subquestion, finding without
// source link, open conflict. Pure helper for unit tests.
func planResearchNextSteps(
	nodes []db.ResearchGraphNode,
	edges []db.ResearchGraphEdge,
	sources []db.ResearchSource,
	max int,
) []researchNextStepCandidate {
	if max <= 0 {
		return nil
	}
	children := map[string]bool{}
	for _, e := range edges {
		if e.EdgeType != "leads_to" && e.EdgeType != "supports" {
			continue
		}
		children[uuidToString(e.FromNodeID)] = true
	}
	sourceLinkedFindings := map[string]bool{}
	for _, e := range edges {
		if e.EdgeType != "supports" {
			continue
		}
		sourceLinkedFindings[uuidToString(e.ToNodeID)] = true
		sourceLinkedFindings[uuidToString(e.FromNodeID)] = true
	}
	for _, n := range nodes {
		if n.NodeType != "finding" || n.Status != "active" {
			continue
		}
		var payload map[string]any
		if len(n.Payload) > 0 {
			_ = jsonUnmarshalMap(n.Payload, &payload)
		}
		if sid, ok := payload["source_id"].(string); ok && strings.TrimSpace(sid) != "" {
			sourceLinkedFindings[uuidToString(n.ID)] = true
		}
	}
	_ = sources // sources feed evidence gate; gap scan uses graph links + payload

	out := make([]researchNextStepCandidate, 0, max)
	add := func(c researchNextStepCandidate) {
		if len(out) >= max {
			return
		}
		out = append(out, c)
	}

	for _, n := range nodes {
		if len(out) >= max {
			break
		}
		if n.Status != "active" || n.NodeType != "subquestion" {
			continue
		}
		if children[uuidToString(n.ID)] {
			continue
		}
		add(researchNextStepCandidate{
			Kind:         "expand_subquestion",
			TargetNodeID: n.ID,
			Reason:       "subquestion has no child exploration node",
		})
	}
	for _, n := range nodes {
		if len(out) >= max {
			break
		}
		if n.Status != "active" || n.NodeType != "finding" {
			continue
		}
		if sourceLinkedFindings[uuidToString(n.ID)] {
			continue
		}
		add(researchNextStepCandidate{
			Kind:         "evidence_gap",
			TargetNodeID: n.ID,
			Reason:       "active finding lacks linked source evidence",
		})
	}
	for _, n := range nodes {
		if len(out) >= max {
			break
		}
		if n.Status != "active" || n.NodeType != "conflict" {
			continue
		}
		add(researchNextStepCandidate{
			Kind:         "resolve_conflict",
			TargetNodeID: n.ID,
			Reason:       "open conflict still active",
		})
	}
	return out
}

func jsonUnmarshalMap(raw []byte, into *map[string]any) error {
	if into == nil {
		return nil
	}
	if len(raw) == 0 {
		*into = map[string]any{}
		return nil
	}
	return json.Unmarshal(raw, into)
}

// researchCompletionBlockers returns reasons completed is not allowed yet.
// Archive early-stop is a separate path and must not use status=completed.
func researchCompletionBlockers(
	session db.ResearchSession,
	nodes []db.ResearchGraphNode,
	edges []db.ResearchGraphEdge,
	sources []db.ResearchSource,
	report *db.ResearchReport,
) []string {
	var blockers []string
	if session.CurrentStage != "s4_delivery" {
		blockers = append(blockers, "current_stage must be s4_delivery")
	}
	if report == nil || strings.TrimSpace(report.ContentMd) == "" {
		blockers = append(blockers, "non-empty research report required")
	}
	activeFindings := 0
	findingHasSource := map[string]bool{}
	for _, e := range edges {
		if e.EdgeType != "supports" {
			continue
		}
		findingHasSource[uuidToString(e.FromNodeID)] = true
		findingHasSource[uuidToString(e.ToNodeID)] = true
	}
	for _, n := range nodes {
		if n.NodeType != "finding" || n.Status != "active" {
			continue
		}
		activeFindings++
		id := uuidToString(n.ID)
		var payload map[string]any
		_ = jsonUnmarshalMap(n.Payload, &payload)
		if sid, ok := payload["source_id"].(string); ok && strings.TrimSpace(sid) != "" {
			findingHasSource[id] = true
		}
		if !findingHasSource[id] {
			blockers = append(blockers, "active finding missing source: "+n.Title)
		}
	}
	if activeFindings == 0 && len(sources) == 0 {
		blockers = append(blockers, "evidence gate: need active findings with sources")
	}
	if activeFindings == 0 && len(sources) > 0 {
		// Sources exist but no active finding nodes — still require at least one finding.
		blockers = append(blockers, "evidence gate: need ≥1 active finding linked to sources")
	}
	return blockers
}

func researchNodeCountsAsBranchExpand(nodeType string) bool {
	switch strings.ToLower(strings.TrimSpace(nodeType)) {
	case "subquestion", "probe", "pivot":
		return true
	default:
		return false
	}
}

// ProcessResearchNextSteps scans running unattended sessions, emits ≤M work
// items + probe/agent_activity graph nodes, and enqueues research wakes.
func (h *Handler) ProcessResearchNextSteps(ctx context.Context, sessionLimit int) (int, error) {
	if h == nil || h.Queries == nil {
		return 0, nil
	}
	if sessionLimit <= 0 {
		sessionLimit = defaultResearchNextStepSessionLimit
	}
	sessions, err := h.Queries.ListRunningUnattendedResearchSessions(ctx, int32(sessionLimit))
	if err != nil {
		return 0, err
	}
	maxPer := researchNextStepMaxPerTick()
	processed := 0
	now := time.Now().UTC()
	for _, session := range sessions {
		durableRun, ownershipErr := h.hasDurableResearchRun(ctx, session.WorkspaceID, session.ID)
		if ownershipErr != nil {
			slog.Warn("research nextstep: ownership inspection failed",
				"session_id", uuidToString(session.ID),
				"error", ownershipErr,
			)
			continue
		}
		if durableRun {
			continue
		}
		n, err := h.processResearchSessionNextSteps(ctx, session, maxPer, now)
		if err != nil {
			slog.Warn("research nextstep: session tick failed",
				"session_id", uuidToString(session.ID),
				"error", err,
			)
			continue
		}
		processed += n
	}
	return processed, nil
}

func (h *Handler) processResearchSessionNextSteps(
	ctx context.Context,
	session db.ResearchSession,
	maxPer int,
	now time.Time,
) (int, error) {
	if !researchSessionIsUserQuiet(session, now) {
		return 0, nil
	}
	openCount, err := h.Queries.CountOpenResearchWorkItems(ctx, db.CountOpenResearchWorkItemsParams{
		SessionID:   session.ID,
		WorkspaceID: session.WorkspaceID,
	})
	if err != nil {
		return 0, err
	}
	budget := maxPer - int(openCount)
	if budget <= 0 {
		return 0, nil
	}

	nodes, err := h.Queries.ListResearchGraphNodes(ctx, db.ListResearchGraphNodesParams{
		SessionID:   session.ID,
		WorkspaceID: session.WorkspaceID,
	})
	if err != nil {
		return 0, err
	}
	edges, err := h.Queries.ListResearchGraphEdges(ctx, db.ListResearchGraphEdgesParams{
		SessionID:   session.ID,
		WorkspaceID: session.WorkspaceID,
	})
	if err != nil {
		return 0, err
	}
	sources, err := h.Queries.ListResearchSources(ctx, db.ListResearchSourcesParams{
		SessionID:   session.ID,
		WorkspaceID: session.WorkspaceID,
	})
	if err != nil {
		return 0, err
	}

	candidates := planResearchNextSteps(nodes, edges, sources, budget)
	if len(candidates) == 0 {
		// Still nudge the lead with a standby probe so silent windows stay alive.
		candidates = []researchNextStepCandidate{{
			Kind:   "advance_probe",
			Reason: "no concrete gap; advance exploration probe",
		}}
	}

	fleet, ferr := h.Queries.GetResearchFleetByWorkspace(ctx, session.WorkspaceID)
	if ferr != nil {
		return 0, ferr
	}
	assignee := fleet.LeadAgentID
	workspaceID := uuidToString(session.WorkspaceID)
	emitted := 0

	for _, c := range candidates {
		item, cerr := h.Queries.CreateResearchWorkItem(ctx, db.CreateResearchWorkItemParams{
			WorkspaceID:     session.WorkspaceID,
			SessionID:       session.ID,
			Kind:            c.Kind,
			TargetNodeID:    c.TargetNodeID,
			AssigneeAgentID: assignee,
			Status:          "pending",
			Reason:          c.Reason,
			Payload: marshalJSONRaw(map[string]any{
				"unattended": true,
				"stage":      session.CurrentStage,
			}),
		})
		if cerr != nil {
			slog.Warn("research nextstep: create work item failed", "error", cerr)
			continue
		}

		title := fmt.Sprintf("NextStep · %s", c.Kind)
		summary := c.Reason
		probe, _, perr := h.createResearchGraphNodePublished(ctx, workspaceID, session.WorkspaceID, session.ID, "system", "", db.CreateResearchGraphNodeParams{
			WorkspaceID:  session.WorkspaceID,
			SessionID:    session.ID,
			NodeType:     "probe",
			Title:        title,
			Summary:      summary,
			Status:       "active",
			ActorAgentID: assignee,
			Payload: marshalJSONRaw(map[string]any{
				"unattended":   true,
				"work_item_id": uuidToString(item.ID),
				"kind":         c.Kind,
				"target_node":  uuidToString(c.TargetNodeID),
			}),
		}, c.TargetNodeID, "leads_to")
		if perr != nil {
			slog.Warn("research nextstep: probe node failed", "error", perr)
		} else {
			_ = probe
			// Scheduler-authored graph-append counts as a silent auto step.
			if _, ierr := h.Queries.IncrementResearchUnattendedAutoSteps(ctx, db.IncrementResearchUnattendedAutoStepsParams{
				ID:                  session.ID,
				WorkspaceID:         session.WorkspaceID,
				UnattendedAutoSteps: 1,
			}); ierr != nil {
				slog.Warn("research nextstep: auto-step increment failed", "error", ierr)
			}
		}

		_, _, _ = h.createResearchGraphNodePublished(ctx, workspaceID, session.WorkspaceID, session.ID, "system", "", db.CreateResearchGraphNodeParams{
			WorkspaceID:  session.WorkspaceID,
			SessionID:    session.ID,
			NodeType:     "agent_activity",
			Title:        "无人值守 NextStep 已派发",
			Summary:      summary,
			Status:       "active",
			ActorAgentID: assignee,
			Payload: marshalJSONRaw(map[string]any{
				"unattended":   true,
				"work_item_id": uuidToString(item.ID),
				"kind":         c.Kind,
			}),
		}, pgtype.UUID{}, "leads_to")

		wakeBody := fmt.Sprintf(
			"ResearchNextStep (unattended): kind=%s reason=%s work_item=%s target_node=%s. "+
				"Continue exploration with multica research graph-append / source-upsert. "+
				"Do NOT treat chat alone as truth; write graph+sources. Session=%s",
			c.Kind, c.Reason, uuidToString(item.ID), uuidToString(c.TargetNodeID), uuidToString(session.ID),
		)
		if assignee.Valid {
			if werr := h.enqueueResearchAgentWake(ctx, session.WorkspaceID, session, assignee, session.CreatedBy, wakeBody, "system", true); werr != nil {
				slog.Warn("research nextstep: wake failed", "error", werr)
				_, _ = h.Queries.UpdateResearchWorkItemStatus(ctx, db.UpdateResearchWorkItemStatusParams{
					ID:          item.ID,
					WorkspaceID: session.WorkspaceID,
					Status:      "failed",
				})
				_, _ = h.Queries.CreateResearchSchedulerEvent(ctx, db.CreateResearchSchedulerEventParams{
					WorkspaceID: session.WorkspaceID,
					SessionID:   session.ID,
					EventType:   "nextstep_wake_failed",
					Detail:      marshalJSONRaw(map[string]any{"work_item_id": uuidToString(item.ID), "error": werr.Error()}),
				})
				continue
			}
		}
		_, _ = h.Queries.UpdateResearchWorkItemStatus(ctx, db.UpdateResearchWorkItemStatusParams{
			ID:          item.ID,
			WorkspaceID: session.WorkspaceID,
			Status:      "enqueued",
		})
		_, _ = h.Queries.CreateResearchSchedulerEvent(ctx, db.CreateResearchSchedulerEventParams{
			WorkspaceID: session.WorkspaceID,
			SessionID:   session.ID,
			EventType:   "nextstep_enqueued",
			Detail: marshalJSONRaw(map[string]any{
				"work_item_id": uuidToString(item.ID),
				"kind":         c.Kind,
				"unattended":   true,
			}),
		})
		h.emitResearchProcessCard(ctx, workspaceID, session.WorkspaceID, session.ID, "system", "", researchProcessEvent{
			Op:    "research_nextstep",
			Title: title,
			Body:  summary,
			Meta: map[string]any{
				"work_item_id": uuidToString(item.ID),
				"kind":         c.Kind,
				"unattended":   true,
			},
		})
		emitted++
	}
	return emitted, nil
}

func (h *Handler) maybeRecordResearchUnattendedMutation(
	ctx context.Context,
	session db.ResearchSession,
	op string,
) {
	if !researchSessionIsUserQuiet(session, time.Now().UTC()) {
		return
	}
	updated, err := h.Queries.IncrementResearchUnattendedAutoSteps(ctx, db.IncrementResearchUnattendedAutoStepsParams{
		ID:                  session.ID,
		WorkspaceID:         session.WorkspaceID,
		UnattendedAutoSteps: 1,
	})
	if err != nil {
		slog.Warn("research unattended step metric failed", "op", op, "error", err)
		return
	}
	_, _ = h.Queries.CreateResearchSchedulerEvent(ctx, db.CreateResearchSchedulerEventParams{
		WorkspaceID: session.WorkspaceID,
		SessionID:   session.ID,
		EventType:   "unattended_auto_step",
		Detail: marshalJSONRaw(map[string]any{
			"op":                    op,
			"unattended_auto_steps": updated.UnattendedAutoSteps,
		}),
	})
}

func (h *Handler) enforceResearchOpenBranchBudget(
	ctx context.Context,
	w http.ResponseWriter,
	session db.ResearchSession,
	nodeType string,
) bool {
	if !researchNodeCountsAsBranchExpand(nodeType) {
		return true
	}
	budget := session.MaxOpenBranches
	if budget <= 0 {
		budget = defaultResearchOpenBranchBudget
	}
	open, err := h.Queries.CountResearchOpenBranches(ctx, db.CountResearchOpenBranchesParams{
		SessionID:   session.ID,
		WorkspaceID: session.WorkspaceID,
	})
	if err != nil {
		slog.Warn("research branch budget count failed", "error", err)
		return true
	}
	if open < budget {
		return true
	}
	_, _ = h.Queries.CreateResearchSchedulerEvent(ctx, db.CreateResearchSchedulerEventParams{
		WorkspaceID: session.WorkspaceID,
		SessionID:   session.ID,
		EventType:   "open_branch_budget_rejected",
		Detail: marshalJSONRaw(map[string]any{
			"open_branches": open,
			"budget":        budget,
			"node_type":     nodeType,
		}),
	})
	h.emitResearchProcessCard(ctx, uuidToString(session.WorkspaceID), session.WorkspaceID, session.ID, "system", "", researchProcessEvent{
		Op:    "open_branch_budget_rejected",
		Title: "并行分支已达软预算",
		Body:  fmt.Sprintf("open_branches=%d ≥ budget=%d；拒绝扩枝。可确认「单线足够」或收敛后再扩。", open, budget),
		Meta:  map[string]any{"open_branches": open, "budget": budget},
	})
	if w != nil {
		writeError(w, http.StatusConflict, fmt.Sprintf("open branch soft budget exceeded (%d/%d)", open, budget))
	}
	return false
}

func (h *Handler) researchS2ParallelBranchOK(ctx context.Context, session db.ResearchSession) (bool, string) {
	if session.SingleLineConfirmed {
		return true, "single_line_confirmed"
	}
	open, err := h.Queries.CountResearchOpenBranches(ctx, db.CountResearchOpenBranchesParams{
		SessionID:   session.ID,
		WorkspaceID: session.WorkspaceID,
	})
	if err != nil {
		return true, "count_error_fail_open"
	}
	if open >= 2 {
		return true, "parallel_branches"
	}
	return false, fmt.Sprintf("need ≥2 open branches before leaving S2 (have %d), or set single_line_confirmed", open)
}

// ArchiveResearchSession early-stops without claiming completed (LRM-1076).
func (h *Handler) ArchiveResearchSession(w http.ResponseWriter, r *http.Request) {
	workspaceID := h.resolveWorkspaceID(r)
	wsUUID, ok := parseUUIDOrBadRequest(w, workspaceID, "workspace_id")
	if !ok {
		return
	}
	userID, userOK := requireUserID(w, r)
	if !userOK {
		return
	}
	sessionID, ok := parseUUIDOrBadRequest(w, chi.URLParam(r, "id"), "id")
	if !ok {
		return
	}
	session, err := h.Queries.GetResearchSession(r.Context(), db.GetResearchSessionParams{ID: sessionID, WorkspaceID: wsUUID})
	if err != nil {
		writeError(w, http.StatusNotFound, "research session not found")
		return
	}
	switch session.Status {
	case "archived":
		writeJSON(w, http.StatusOK, researchSessionToResponse(session))
		return
	case "completed":
		writeError(w, http.StatusBadRequest, "completed sessions cannot be archived; they already finished")
		return
	}
	durableRun, ownershipErr := h.hasDurableResearchRun(r.Context(), wsUUID, sessionID)
	if ownershipErr != nil {
		writeError(w, http.StatusInternalServerError, "failed to inspect research run ownership")
		return
	}
	if durableRun {
		if h.ResearchRun == nil {
			writeError(w, http.StatusServiceUnavailable, "research run engine is unavailable")
			return
		}
		if _, err = h.ResearchRun.Archive(r.Context(), uuidToString(sessionID), workspaceID, userID, "research session archived by user"); err != nil {
			writeError(w, http.StatusInternalServerError, "failed to archive research run")
			return
		}
		if err = h.stopResearchSessionWakes(r.Context(), wsUUID, sessionID); err != nil {
			writeError(w, http.StatusInternalServerError, "research run archived but wake cancellation failed")
			return
		}
		updated, loadErr := h.Queries.GetResearchSession(r.Context(), db.GetResearchSessionParams{ID: sessionID, WorkspaceID: wsUUID})
		if loadErr != nil {
			writeError(w, http.StatusInternalServerError, "failed to reload archived research run")
			return
		}
		writeJSON(w, http.StatusOK, researchSessionToResponse(updated))
		return
	}
	if err = h.stopResearchSessionWakes(r.Context(), wsUUID, sessionID); err != nil {
		writeError(w, http.StatusInternalServerError, "failed to cancel active research tasks")
		return
	}
	updated, err := h.Queries.UpdateResearchSession(r.Context(), db.UpdateResearchSessionParams{
		ID:          sessionID,
		WorkspaceID: wsUUID,
		Status:      pgtype.Text{String: "archived", Valid: true},
	})
	if err != nil {
		writeError(w, http.StatusInternalServerError, "failed to archive session")
		return
	}
	_, _ = h.Queries.CreateResearchSchedulerEvent(r.Context(), db.CreateResearchSchedulerEventParams{
		WorkspaceID: wsUUID,
		SessionID:   sessionID,
		EventType:   "session_archived_early_stop",
		Detail:      marshalJSONRaw(map[string]any{"from_status": session.Status}),
	})
	h.publish(protocol.EventResearchSessionStatusChanged, workspaceID, "user", userID, map[string]any{
		"session": researchSessionToResponse(updated),
	})
	h.emitResearchProcessCard(r.Context(), workspaceID, wsUUID, sessionID, "user", userID, researchProcessEvent{
		Op:    "session_archived",
		Title: "调研已归档（早停）",
		Body:  "显式 archive，不计入 completed；图+源仍保留为真相面。",
		Meta:  map[string]any{"status": "archived"},
	})
	writeJSON(w, http.StatusOK, researchSessionToResponse(updated))
}

// ConfirmResearchSingleLine records explicit「单线足够」 before leaving S2.
func (h *Handler) ConfirmResearchSingleLine(w http.ResponseWriter, r *http.Request) {
	workspaceID := h.resolveWorkspaceID(r)
	wsUUID, ok := parseUUIDOrBadRequest(w, workspaceID, "workspace_id")
	if !ok {
		return
	}
	userID, userOK := requireUserID(w, r)
	if !userOK {
		return
	}
	sessionID, ok := parseUUIDOrBadRequest(w, chi.URLParam(r, "id"), "id")
	if !ok {
		return
	}
	if h.rejectLegacyResearchMutation(w, r, wsUUID, sessionID) {
		return
	}
	updated, err := h.Queries.UpdateResearchSession(r.Context(), db.UpdateResearchSessionParams{
		ID:                  sessionID,
		WorkspaceID:         wsUUID,
		SingleLineConfirmed: pgtype.Bool{Bool: true, Valid: true},
	})
	if err != nil {
		writeError(w, http.StatusNotFound, "research session not found")
		return
	}
	_, _ = h.Queries.CreateResearchSchedulerEvent(r.Context(), db.CreateResearchSchedulerEventParams{
		WorkspaceID: wsUUID,
		SessionID:   sessionID,
		EventType:   "single_line_confirmed",
		Detail:      marshalJSONRaw(map[string]any{"by": userID}),
	})
	writeJSON(w, http.StatusOK, researchSessionToResponse(updated))
}
