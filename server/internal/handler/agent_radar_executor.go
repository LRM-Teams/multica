package handler

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/multica-ai/multica/server/internal/radar"
	"github.com/multica-ai/multica/server/internal/service"
	db "github.com/multica-ai/multica/server/pkg/db/generated"
	"github.com/multica-ai/multica/server/pkg/protocol"
)

type radarChannelPayload struct {
	ChannelID           string `json:"channel_id"`
	Content             string `json:"content"`
	ThreadRootMessageID string `json:"thread_root_message_id"`
	ThreadID            string `json:"thread_id"`
}

type radarIssuePayload struct {
	Title        string `json:"title"`
	Description  string `json:"description"`
	ProjectID    string `json:"project_id"`
	AssigneeType string `json:"assignee_type"`
	AssigneeID   string `json:"assignee_id"`
}

type radarPlanPayload struct {
	Content string `json:"content"`
}

func (h *Handler) ExecuteAgentRadarPlan(ctx context.Context, run db.AgentRadarRun, agent db.Agent, plan radar.ActionPlan) error {
	status := "succeeded"
	if len(plan.Actions) == 0 {
		plan.Actions = []radar.RadarAction{{Type: radar.ActionNoAction, Reason: "agent returned no actions", Confidence: "medium", RiskLevel: "low", TargetKind: "none"}}
	}
	for _, action := range plan.Actions {
		if err := h.executeAgentRadarAction(ctx, run, agent, action); err != nil {
			status = "failed"
		}
	}
	if len(plan.Actions) == 1 && plan.Actions[0].Type == radar.ActionNoAction {
		status = "no_action"
	}
	planJSON, _ := json.Marshal(plan)
	_, err := h.Queries.UpdateAgentRadarRunStatus(ctx, db.UpdateAgentRadarRunStatusParams{
		ID:         run.ID,
		Status:     status,
		ActionPlan: planJSON,
	})
	return err
}

func (h *Handler) executeAgentRadarAction(ctx context.Context, run db.AgentRadarRun, agent db.Agent, action radar.RadarAction) error {
	evidence, _ := json.Marshal(action.Evidence)
	if len(evidence) == 0 {
		evidence = []byte("[]")
	}
	payload := []byte(action.Payload)
	if len(payload) == 0 {
		payload = []byte("{}")
	}
	targetID := parseOptionalUUID(action.TargetID)
	row, err := h.Queries.CreateAgentRadarAction(ctx, db.CreateAgentRadarActionParams{
		RadarRunID:  run.ID,
		WorkspaceID: run.WorkspaceID,
		AgentID:     run.AgentID,
		ActionType:  action.Type,
		RiskLevel:   defaultString(action.RiskLevel, "low"),
		Confidence:  defaultString(action.Confidence, "medium"),
		DedupeKey:   action.DedupeKey,
		TargetKind:  radarDefaultString(action.TargetKind, "none"),
		TargetID:    targetID,
		Reason:      action.Reason,
		Evidence:    evidence,
		Payload:     payload,
		Status:      "approved",
	})
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil // duplicate active dedupe key
		}
		return err
	}
	result, execErr := h.executeApprovedRadarAction(ctx, run, agent, action)
	status := "executed"
	errText := pgtype.Text{}
	if execErr != nil {
		status = "failed"
		errText = pgtype.Text{String: execErr.Error(), Valid: true}
		result = map[string]any{"error": execErr.Error()}
	}
	resultJSON, _ := json.Marshal(result)
	_, updateErr := h.Queries.UpdateAgentRadarActionStatus(ctx, db.UpdateAgentRadarActionStatusParams{
		ID:     row.ID,
		Status: status,
		Result: resultJSON,
		Error:  errText,
	})
	recordAgentActivityEvent(ctx, h.DB,
		run.WorkspaceID, agent.ID, agent.RuntimeID, run.TaskID,
		"platform_decision", "radar_action_"+status, severityForRadarAction(status),
		radarActivityTargetKind(action.TargetKind), targetID, "",
		action.Type, radarActivityMessage(action, status),
		map[string]any{
			"radar_run_id": uuidToString(run.ID),
			"dedupe_key":   action.DedupeKey,
			"reason":       action.Reason,
			"evidence":     action.Evidence,
			"result":       result,
		},
	)
	if updateErr != nil {
		return updateErr
	}
	return execErr
}

func (h *Handler) executeApprovedRadarAction(ctx context.Context, run db.AgentRadarRun, agent db.Agent, action radar.RadarAction) (map[string]any, error) {
	switch action.Type {
	case radar.ActionNoAction:
		return map[string]any{"status": "no_action"}, nil
	case radar.ActionPostChannelMessage, radar.ActionMentionAgent:
		return h.executeRadarChannelPost(ctx, run, agent, action)
	case radar.ActionReplyThread:
		return h.executeRadarChannelPost(ctx, run, agent, action)
	case radar.ActionCreateIssue:
		return h.executeRadarCreateIssue(ctx, run, agent, action)
	case radar.ActionUpdateAgentPlan:
		return h.executeRadarPlanUpdate(ctx, agent, action)
	default:
		return map[string]any{"blocked": true}, fmt.Errorf("radar action %s is not executable yet", action.Type)
	}
}

func (h *Handler) executeRadarChannelPost(ctx context.Context, run db.AgentRadarRun, agent db.Agent, action radar.RadarAction) (map[string]any, error) {
	var payload radarChannelPayload
	if err := json.Unmarshal(action.Payload, &payload); err != nil {
		return nil, err
	}
	if strings.TrimSpace(payload.ChannelID) == "" || strings.TrimSpace(payload.Content) == "" {
		return nil, errors.New("channel_id and content are required")
	}
	channelID, err := parseUUIDString(payload.ChannelID)
	if err != nil {
		return nil, err
	}
	var threadRoot pgtype.UUID
	if payload.ThreadRootMessageID != "" {
		threadRoot, err = parseUUIDString(payload.ThreadRootMessageID)
		if err != nil {
			return nil, err
		}
	}
	content := "主动发现：" + strings.TrimSpace(payload.Content)
	msg, err := h.insertChannelMessage(ctx, channelID, run.WorkspaceID, "agent", agent.ID, agentDisplayName(agent), content, "multica", nil, pgtype.UUID{}, threadRoot, strPtr(payload.ThreadID), 0)
	if err != nil {
		return nil, err
	}
	return map[string]any{"channel_message_id": msg.ID}, nil
}

func (h *Handler) executeRadarCreateIssue(ctx context.Context, run db.AgentRadarRun, agent db.Agent, action radar.RadarAction) (map[string]any, error) {
	var payload radarIssuePayload
	if err := json.Unmarshal(action.Payload, &payload); err != nil {
		return nil, err
	}
	if strings.TrimSpace(payload.Title) == "" {
		return nil, errors.New("title is required")
	}
	assigneeType := pgtype.Text{String: "agent", Valid: true}
	assigneeID := agent.ID
	if payload.AssigneeID != "" {
		parsed, err := parseUUIDString(payload.AssigneeID)
		if err != nil {
			return nil, err
		}
		assigneeID = parsed
	}
	if payload.AssigneeType != "" {
		assigneeType = pgtype.Text{String: payload.AssigneeType, Valid: true}
	}
	var projectID pgtype.UUID
	if payload.ProjectID != "" {
		parsed, err := parseUUIDString(payload.ProjectID)
		if err != nil {
			return nil, err
		}
		projectID = parsed
	}
	result, err := h.IssueService.Create(ctx, service.IssueCreateParams{
		WorkspaceID:    run.WorkspaceID,
		Title:          strings.TrimSpace(payload.Title),
		Description:    pgtype.Text{String: payload.Description, Valid: strings.TrimSpace(payload.Description) != ""},
		Status:         "todo",
		Priority:       "none",
		AssigneeType:   assigneeType,
		AssigneeID:     assigneeID,
		CreatorType:    "agent",
		CreatorID:      agent.ID,
		ProjectID:      projectID,
		AllowDuplicate: false,
	}, service.IssueCreateOpts{
		ActorID:          uuidToString(agent.ID),
		AnalyticsAgentID: uuidToString(agent.ID),
		Platform:         "radar",
	})
	if err != nil {
		return nil, err
	}
	return map[string]any{"issue_id": uuidToString(result.Issue.ID)}, nil
}

func (h *Handler) executeRadarPlanUpdate(ctx context.Context, agent db.Agent, action radar.RadarAction) (map[string]any, error) {
	var payload radarPlanPayload
	if err := json.Unmarshal(action.Payload, &payload); err != nil {
		return nil, err
	}
	if strings.TrimSpace(payload.Content) == "" {
		return nil, errors.New("content is required")
	}
	if h.DaemonHub == nil {
		return nil, errors.New("runtime offline")
	}
	resp, err := h.DaemonHub.RequestWriteFile(ctx, protocol.WriteWorkdirFileRequestPayload{
		RequestID: uuid.NewString(),
		RuntimeID: uuidToString(agent.RuntimeID),
		RelPath:   agentRootRelPath(agent),
		FilePath:  "notes/agent-plan.md",
		Content:   payload.Content,
	})
	if err != nil {
		return nil, err
	}
	if resp.Error != "" {
		return nil, errors.New(resp.Error)
	}
	return map[string]any{"content_hash": resp.ContentHash}, nil
}

func parseOptionalUUID(raw string) pgtype.UUID {
	if raw == "" {
		return pgtype.UUID{}
	}
	id, err := parseUUIDString(raw)
	if err != nil {
		return pgtype.UUID{}
	}
	return id
}

func parseUUIDString(raw string) (pgtype.UUID, error) {
	parsed, err := uuid.Parse(raw)
	if err != nil {
		return pgtype.UUID{}, err
	}
	return pgtype.UUID{Bytes: parsed, Valid: true}, nil
}

func severityForRadarAction(status string) string {
	if status == "failed" {
		return "warning"
	}
	return "info"
}

func radarActivityMessage(action radar.RadarAction, status string) string {
	if action.Reason != "" {
		return "Radar " + status + ": " + action.Reason
	}
	return "Radar " + status + ": " + action.Type
}

func radarDefaultString(v, fallback string) string {
	if strings.TrimSpace(v) == "" {
		return fallback
	}
	return v
}

func radarActivityTargetKind(kind string) string {
	switch kind {
	case "issue", "dm", "channel", "thread", "agent", "none":
		return kind
	default:
		return "none"
	}
}
