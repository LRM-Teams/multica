package handler

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"strings"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/multica-ai/multica/server/internal/researchrun"
	db "github.com/multica-ai/multica/server/pkg/db/generated"
	"github.com/multica-ai/multica/server/pkg/protocol"
)

// Stable UUID namespace shared with LRM-1401 run-v2 graph projection so node
// commands resolve the same canvas IDs FE will show after that PR merges.
var researchNodeCommandGraphNamespace = uuid.MustParse("6b1f0c2e-9a47-4d8f-b3e1-2f5a8c7d9041")

type researchNodeCommandRequest struct {
	Action               string          `json:"action"`
	ClientRequestID      string          `json:"client_request_id"`
	ExpectedStateVersion *int64          `json:"expected_state_version"`
	Objective            string          `json:"objective"`
	GoalPatch            string          `json:"goal_patch"`
	Strategy             string          `json:"strategy"`
	StrategyPatch        string          `json:"strategy_patch"`
	SourceConstraints    json.RawMessage `json:"source_constraints"`
	SourcePatch          json.RawMessage `json:"source_patch"`
	TargetAgentID        string          `json:"target_agent_id"`
}

func (h *Handler) PostResearchNodeCommand(w http.ResponseWriter, r *http.Request) {
	h.postResearchNodeCommand(w, r, false)
}

func (h *Handler) PostAgentResearchNodeCommand(w http.ResponseWriter, r *http.Request) {
	if _, ok := h.requireAgentPrincipal(w, r); !ok {
		return
	}
	h.postResearchNodeCommand(w, r, true)
}

func (h *Handler) postResearchNodeCommand(w http.ResponseWriter, r *http.Request, agentPath bool) {
	if h.ResearchRun == nil {
		writeError(w, http.StatusServiceUnavailable, "research run engine is unavailable")
		return
	}
	workspaceID := h.resolveWorkspaceID(r)
	wsUUID, ok := parseUUIDOrBadRequest(w, workspaceID, "workspace_id")
	if !ok {
		return
	}
	sessionID, ok := parseUUIDOrBadRequest(w, chi.URLParam(r, "id"), "id")
	if !ok {
		return
	}
	nodeIDRaw := strings.TrimSpace(chi.URLParam(r, "nodeId"))
	if nodeIDRaw == "" {
		writeResearchNodeCommandDenied(w, researchrun.DenyNodeCommand(
			researchrun.NodeCmdCodeNodeStale, "节点已失效，请刷新画布后重试"))
		return
	}

	actorType := "user"
	actorID := ""
	if agentPath {
		member, memberOK := h.requireActiveFleetMember(w, r, wsUUID)
		if !memberOK {
			return
		}
		actorType = "agent"
		actorID = uuidToString(member.AgentID)
	} else {
		userID, userOK := requireUserID(w, r)
		if !userOK {
			return
		}
		actorID = userID
	}

	session, err := h.Queries.GetResearchSession(r.Context(), db.GetResearchSessionParams{
		ID: sessionID, WorkspaceID: wsUUID,
	})
	if err != nil {
		writeError(w, http.StatusNotFound, "research session not found")
		return
	}
	_ = session

	var req researchNodeCommandRequest
	if !decodeResearchJSON(w, r, &req) {
		return
	}
	req.Action = strings.TrimSpace(strings.ToLower(req.Action))
	req.ClientRequestID = strings.TrimSpace(req.ClientRequestID)

	anchor, resolveErr := h.resolveResearchNodeCommandAnchor(r.Context(), wsUUID, sessionID, workspaceID, nodeIDRaw)
	if resolveErr != nil {
		var denied *researchrun.NodeCommandDenied
		if errors.As(resolveErr, &denied) {
			writeResearchNodeCommandDenied(w, denied)
			return
		}
		writeError(w, http.StatusInternalServerError, "failed to resolve research node")
		return
	}

	outcome, cmdErr := h.ResearchRun.NodeCommand(r.Context(), researchrun.NodeCommandInput{
		SessionID:            uuidToString(sessionID),
		WorkspaceID:          workspaceID,
		NodeID:               nodeIDRaw,
		Action:               req.Action,
		ClientRequestID:      req.ClientRequestID,
		ExpectedStateVersion: req.ExpectedStateVersion,
		ActorType:            actorType,
		ActorID:              actorID,
		Objective:            strings.TrimSpace(req.Objective),
		GoalPatch:            strings.TrimSpace(req.GoalPatch),
		Strategy:             strings.TrimSpace(req.Strategy),
		StrategyPatch:        strings.TrimSpace(req.StrategyPatch),
		SourceConstraints:    req.SourceConstraints,
		SourcePatch:          req.SourcePatch,
		TargetAgentID:        strings.TrimSpace(req.TargetAgentID),
		AnchorKind:           anchor.Kind,
		AnchorQuestionID:     anchor.QuestionID,
		AnchorTaskID:         anchor.TaskID,
		AnchorTitle:          anchor.Title,
	})
	if cmdErr != nil {
		var denied *researchrun.NodeCommandDenied
		if errors.As(cmdErr, &denied) {
			writeResearchNodeCommandDenied(w, denied)
			return
		}
		if errors.Is(cmdErr, researchrun.ErrRunNotFound) {
			writeError(w, http.StatusNotFound, "research session not found")
			return
		}
		writeError(w, http.StatusInternalServerError, "failed to apply node command")
		return
	}

	actorUUID := pgtype.UUID{}
	if parsed, ok := parseUUIDQuiet(actorID); ok {
		actorUUID = parsed
	}
	h.emitResearchProcessCard(r.Context(), workspaceID, wsUUID, sessionID, actorType, actorID, researchProcessEvent{
		Op:      "node_command_" + req.Action,
		Title:   nodeCommandTitle(req.Action, outcome),
		Body:    nodeCommandBody(req.Action, outcome),
		ActorID: actorUUID,
		Meta: map[string]any{
			"command_id":         outcome.CommandID,
			"action":             outcome.Action,
			"client_request_id":  outcome.ClientRequestID,
			"source_node_id":     nodeIDRaw,
			"question_id":        optionalID(outcome.Question),
			"task_id":            optionalTaskID(outcome.Task),
			"parent_question_id": outcome.ParentLineage.ParentQuestionID,
			"parent_task_id":     outcome.ParentLineage.ParentTaskID,
			"queued":             outcome.Queued,
			"replayed":           outcome.Replayed,
		},
	})
	h.publish(protocol.EventResearchSessionStatusChanged, workspaceID, actorType, actorID, map[string]any{
		"session_id":  uuidToString(sessionID),
		"op":          "node_command",
		"action":      outcome.Action,
		"command_id":  outcome.CommandID,
		"task_id":     optionalTaskID(outcome.Task),
		"question_id": optionalID(outcome.Question),
	})

	status := http.StatusOK
	if !outcome.Replayed {
		status = http.StatusCreated
	}
	writeJSON(w, status, map[string]any{
		"command_id":         outcome.CommandID,
		"action":             outcome.Action,
		"client_request_id":  outcome.ClientRequestID,
		"replayed":           outcome.Replayed,
		"state_version":      outcome.StateVersion,
		"question":           outcome.Question,
		"task":               outcome.Task,
		"attempt":            outcome.Attempt,
		"parent_lineage":     outcome.ParentLineage,
		"retry_lineage":      outcome.RetryLineage,
		"reassign":           outcome.Reassign,
		"assigned":           outcome.Assigned,
		"queued":             outcome.Queued,
	})
}

type researchNodeAnchor struct {
	Kind       string
	QuestionID string
	TaskID     string
	Title      string
}

func (h *Handler) resolveResearchNodeCommandAnchor(
	ctx context.Context,
	wsUUID, sessionID pgtype.UUID,
	workspaceID, nodeID string,
) (researchNodeAnchor, error) {
	sessionKey := uuidToString(sessionID)

	// 1) Legacy / persisted canvas node.
	if nodeUUID, ok := parseUUIDQuiet(nodeID); ok {
		if node, err := h.Queries.GetResearchGraphNode(ctx, db.GetResearchGraphNodeParams{
			ID: nodeUUID, WorkspaceID: wsUUID,
		}); err == nil {
			if uuidToString(node.SessionID) != sessionKey {
				return researchNodeAnchor{}, researchrun.DenyNodeCommand(
					researchrun.NodeCmdCodeNodeStale, "节点已失效或不属于当前研究图，请刷新后重试")
			}
			anchor := researchNodeAnchor{Kind: "legacy", Title: strings.TrimSpace(node.Title)}
			fillAnchorFromPayload(&anchor, node.Payload)
			if anchor.QuestionID == "" && anchor.TaskID == "" {
				switch strings.ToLower(node.NodeType) {
				case "subquestion", "question", "contradiction", "gap":
					// Soft resolve: match open question text.
					if h.ResearchRun != nil {
						if qs, qerr := h.ResearchRun.Snapshot(ctx, sessionKey, workspaceID); qerr == nil {
							for _, q := range qs.Questions {
								if strings.TrimSpace(q.Question) == strings.TrimSpace(node.Title) {
									anchor.QuestionID = q.ID
									anchor.Kind = "question"
									break
								}
							}
						}
					}
				}
			}
			if anchor.QuestionID != "" || anchor.TaskID != "" {
				return anchor, nil
			}
			return researchNodeAnchor{}, researchrun.DenyNodeCommand(
				researchrun.NodeCmdCodeNodeStale, "该画布节点尚未绑定研究问题，无法续研或分叉")
		}
	}

	// 2) Run-v2 projected IDs + raw question/task UUIDs.
	if h.ResearchRun == nil {
		return researchNodeAnchor{}, researchrun.DenyNodeCommand(
			researchrun.NodeCmdCodeNodeStale, "节点已失效或不属于当前研究图，请刷新后重试")
	}
	snap, err := h.ResearchRun.Snapshot(ctx, sessionKey, workspaceID)
	if err != nil {
		if errors.Is(err, researchrun.ErrRunNotFound) {
			return researchNodeAnchor{}, researchrun.DenyNodeCommand(
				researchrun.NodeCmdCodeNodeStale, "节点已失效或不属于当前研究图，请刷新后重试")
		}
		return researchNodeAnchor{}, err
	}

	rootID := researchProjectedNodeID(sessionKey, "root", sessionKey)
	if nodeID == rootID {
		return researchNodeAnchor{Kind: "root", Title: strings.TrimSpace(snap.Run.Title)}, nil
	}
	for _, q := range snap.Questions {
		if q.ID == nodeID || researchProjectedNodeID(sessionKey, "question", q.ID) == nodeID {
			return researchNodeAnchor{
				Kind: "question", QuestionID: q.ID, Title: strings.TrimSpace(q.Question),
			}, nil
		}
	}
	for _, t := range snap.Tasks {
		if t.ID == nodeID || researchProjectedNodeID(sessionKey, "task", t.ID) == nodeID {
			return researchNodeAnchor{
				Kind: "task", QuestionID: t.QuestionID, TaskID: t.ID, Title: strings.TrimSpace(t.Objective),
			}, nil
		}
	}
	for _, a := range snap.Attempts {
		if a.ID == nodeID || researchProjectedNodeID(sessionKey, "attempt", a.ID) == nodeID {
			for _, t := range snap.Tasks {
				if t.ID == a.TaskID {
					return researchNodeAnchor{
						Kind: "attempt", QuestionID: t.QuestionID, TaskID: t.ID,
						Title: strings.TrimSpace(t.Objective),
					}, nil
				}
			}
		}
	}
	return researchNodeAnchor{}, researchrun.DenyNodeCommand(
		researchrun.NodeCmdCodeNodeStale, "节点已失效或不属于当前研究图，请刷新后重试")
}

func fillAnchorFromPayload(anchor *researchNodeAnchor, payload json.RawMessage) {
	if len(payload) == 0 || anchor == nil {
		return
	}
	var obj map[string]any
	if json.Unmarshal(payload, &obj) != nil {
		return
	}
	if v, ok := obj["question_id"].(string); ok && strings.TrimSpace(v) != "" {
		anchor.QuestionID = strings.TrimSpace(v)
		anchor.Kind = "question"
	}
	if v, ok := obj["task_id"].(string); ok && strings.TrimSpace(v) != "" {
		anchor.TaskID = strings.TrimSpace(v)
		if anchor.Kind == "legacy" || anchor.Kind == "" {
			anchor.Kind = "task"
		}
	}
	if details, ok := obj["details"].(map[string]any); ok {
		if v, ok := details["question_id"].(string); ok && strings.TrimSpace(v) != "" && anchor.QuestionID == "" {
			anchor.QuestionID = strings.TrimSpace(v)
		}
		if v, ok := details["task_id"].(string); ok && strings.TrimSpace(v) != "" && anchor.TaskID == "" {
			anchor.TaskID = strings.TrimSpace(v)
		}
	}
}

func researchProjectedNodeID(sessionID, kind, entityID string) string {
	return uuid.NewSHA1(researchNodeCommandGraphNamespace, []byte("node|"+sessionID+"|"+kind+"|"+entityID)).String()
}

func writeResearchNodeCommandDenied(w http.ResponseWriter, denied *researchrun.NodeCommandDenied) {
	if denied == nil {
		writeError(w, http.StatusConflict, "操作被拒绝")
		return
	}
	status := denied.HTTPStatus
	if status == 0 {
		status = http.StatusConflict
	}
	if denied.MachineCode == researchrun.NodeCmdCodePermissionDenied {
		status = http.StatusForbidden
	}
	writeJSON(w, status, map[string]any{
		"machine_code": denied.MachineCode,
		"message_key":  denied.MessageKey,
		"message":      denied.Message,
		"error":        denied.Message,
	})
}

func parseUUIDQuiet(raw string) (pgtype.UUID, bool) {
	id, err := uuid.Parse(strings.TrimSpace(raw))
	if err != nil {
		return pgtype.UUID{}, false
	}
	return pgtype.UUID{Bytes: id, Valid: true}, true
}

func optionalID(q *researchrun.Question) string {
	if q == nil {
		return ""
	}
	return q.ID
}

func optionalTaskID(t *researchrun.Task) string {
	if t == nil {
		return ""
	}
	return t.ID
}

func nodeCommandTitle(action string, outcome researchrun.NodeCommandOutcome) string {
	switch action {
	case researchrun.NodeActionFork:
		return "从此分叉"
	case researchrun.NodeActionRetry:
		return "重试任务"
	case researchrun.NodeActionReassign:
		return "改派执行者"
	default:
		return "继续调研"
	}
}

func nodeCommandBody(action string, outcome researchrun.NodeCommandOutcome) string {
	obj := ""
	if outcome.Task != nil {
		obj = strings.TrimSpace(outcome.Task.Objective)
	}
	if obj == "" && outcome.Question != nil {
		obj = strings.TrimSpace(outcome.Question.Question)
	}
	switch action {
	case researchrun.NodeActionFork:
		if obj == "" {
			return "已创建分叉问题并排队新任务"
		}
		return "已分叉：" + truncateForCard(obj, 80)
	case researchrun.NodeActionRetry:
		prev := ""
		if outcome.RetryLineage != nil && outcome.RetryLineage.PreviousAttemptID != "" {
			prev = "（保留原尝试）"
		}
		if obj == "" {
			return "已重新排队失败任务" + prev
		}
		return "已重试：" + truncateForCard(obj, 80) + prev
	case researchrun.NodeActionReassign:
		to := ""
		if outcome.Reassign != nil {
			to = outcome.Reassign.ToAgentID
		}
		if to == "" {
			return "已改派并重新排队"
		}
		return "已改派至 " + truncateForCard(to, 36)
	default:
		if obj == "" {
			return "已沿当前问题追加任务并排队"
		}
		return "已续研：" + truncateForCard(obj, 80)
	}
}

func truncateForCard(s string, max int) string {
	runes := []rune(s)
	if len(runes) <= max {
		return s
	}
	return string(runes[:max]) + "…"
}
