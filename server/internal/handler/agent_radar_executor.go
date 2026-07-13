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

type radarActivityTarget struct {
	Kind    string
	ID      pgtype.UUID
	Slug    string
	Trusted bool
}

type radarChannelExecutionTarget struct {
	ChannelID  pgtype.UUID
	ThreadRoot pgtype.UUID
	ThreadID   *string
	Activity   radarActivityTarget
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
	result, activityTarget, execErr := h.executeApprovedRadarActionWithTarget(ctx, run, agent, action)
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
	activityDetails := map[string]any{
		"radar_run_id": uuidToString(run.ID),
		"dedupe_key":   action.DedupeKey,
		"reason":       action.Reason,
		"evidence":     action.Evidence,
		"result":       result,
	}
	if activityTarget.Trusted {
		h.recordAgentActivityEvent(ctx, h.DB,
			run.WorkspaceID, agent.ID, agent.RuntimeID, run.TaskID,
			activityKindCustom, "radar_action_"+status, severityForRadarAction(status),
			activityTarget.Kind, activityTarget.ID, activityTarget.Slug,
			action.Type, radarActivityMessage(action, status), activityDetails,
		)
	} else {
		// A model-provided target is only an assertion. Keep failures without a
		// verified execution target as diagnostics and do not publish them to the
		// workspace realtime stream, where the model-provided reason would leak.
		insertAgentActivityEvent(ctx, h.DB,
			run.WorkspaceID, agent.ID, agent.RuntimeID, run.TaskID,
			activityKindCustom, "radar_action_"+status, severityForRadarAction(status),
			"none", pgtype.UUID{}, "",
			"radar_untrusted_target", "Radar "+status+": action target could not be verified",
			map[string]any{
				"radar_run_id": uuidToString(run.ID),
				"dedupe_key":   action.DedupeKey,
				"action_type":  action.Type,
				"result":       result,
			},
		)
	}
	if updateErr != nil {
		return updateErr
	}
	return execErr
}

func (h *Handler) executeApprovedRadarActionWithTarget(ctx context.Context, run db.AgentRadarRun, agent db.Agent, action radar.RadarAction) (map[string]any, radarActivityTarget, error) {
	switch action.Type {
	case radar.ActionNoAction:
		return map[string]any{"status": "no_action"}, radarActivityTarget{Kind: "none", Trusted: true}, nil
	case radar.ActionPostChannelMessage, radar.ActionMentionAgent:
		return h.executeRadarChannelPostWithTarget(ctx, run, agent, action)
	case radar.ActionReplyThread:
		return h.executeRadarChannelPostWithTarget(ctx, run, agent, action)
	case radar.ActionCreateIssue:
		result, err := h.executeRadarCreateIssue(ctx, run, agent, action)
		if err != nil {
			return result, radarActivityTarget{}, err
		}
		issueID, err := parseUUIDString(stringFromMap(result, "issue_id"))
		if err != nil {
			return result, radarActivityTarget{}, nil
		}
		return result, radarActivityTarget{Kind: "issue", ID: issueID, Trusted: true}, nil
	case radar.ActionUpdateAgentPlan:
		if !radarUUIDsMatch(run.WorkspaceID, agent.WorkspaceID) {
			return nil, radarActivityTarget{}, errors.New("radar agent does not belong to the run workspace")
		}
		if !radarUUIDsMatch(run.AgentID, agent.ID) {
			return nil, radarActivityTarget{}, errors.New("radar agent does not match the run")
		}
		result, err := h.executeRadarPlanUpdate(ctx, agent, action)
		if err != nil {
			return result, radarActivityTarget{}, err
		}
		return result, radarActivityTarget{Kind: "agent", ID: agent.ID, Trusted: true}, nil
	default:
		return map[string]any{"blocked": true}, radarActivityTarget{}, fmt.Errorf("radar action %s is not executable yet", action.Type)
	}
}

func (h *Handler) executeRadarChannelPost(ctx context.Context, run db.AgentRadarRun, agent db.Agent, action radar.RadarAction) (map[string]any, error) {
	result, _, err := h.executeRadarChannelPostWithTarget(ctx, run, agent, action)
	return result, err
}

func (h *Handler) executeRadarChannelPostWithTarget(ctx context.Context, run db.AgentRadarRun, agent db.Agent, action radar.RadarAction) (map[string]any, radarActivityTarget, error) {
	var payload radarChannelPayload
	if err := json.Unmarshal(action.Payload, &payload); err != nil {
		return nil, radarActivityTarget{}, err
	}
	if strings.TrimSpace(payload.ChannelID) == "" || strings.TrimSpace(payload.Content) == "" {
		return nil, radarActivityTarget{}, errors.New("channel_id and content are required")
	}
	target, err := h.resolveRadarChannelExecutionTarget(ctx, run, agent, action.Type, payload)
	if err != nil {
		return nil, radarActivityTarget{}, err
	}
	content := "主动发现：" + strings.TrimSpace(payload.Content)
	msg, err := h.insertChannelMessage(ctx, target.ChannelID, run.WorkspaceID, "agent", agent.ID, agentDisplayName(agent), content, "multica", nil, pgtype.UUID{}, target.ThreadRoot, target.ThreadID, 0)
	if err != nil {
		return nil, target.Activity, err
	}
	_, _ = h.DB.Exec(ctx, `UPDATE channel SET updated_at = now() WHERE id = $1`, target.ChannelID)
	h.clearDMHiddenForChannelMembers(ctx, uuidToString(run.WorkspaceID), target.ChannelID)
	h.publishChannelToMembers(ctx, protocol.EventChannelMessage, uuidToString(run.WorkspaceID), "agent", uuidToString(agent.ID), target.ChannelID, msg)
	if action.Type == radar.ActionMentionAgent {
		if ch, found := h.getChannel(ctx, uuidToString(run.WorkspaceID), target.ChannelID); found {
			h.dispatchChannelMentions(ctx, ch, msg, pgtype.UUID{})
		}
	}
	return map[string]any{"channel_message_id": msg.ID}, target.Activity, nil
}

func (h *Handler) resolveRadarChannelExecutionTarget(ctx context.Context, run db.AgentRadarRun, agent db.Agent, actionType string, payload radarChannelPayload) (radarChannelExecutionTarget, error) {
	if !radarUUIDsMatch(run.WorkspaceID, agent.WorkspaceID) {
		return radarChannelExecutionTarget{}, errors.New("radar agent does not belong to the run workspace")
	}
	if run.AgentID.Valid && !radarUUIDsMatch(run.AgentID, agent.ID) {
		return radarChannelExecutionTarget{}, errors.New("radar agent does not match the run")
	}

	channelID, err := parseUUIDString(strings.TrimSpace(payload.ChannelID))
	if err != nil {
		return radarChannelExecutionTarget{}, errors.New("invalid channel_id")
	}
	channel, found := h.getChannel(ctx, uuidToString(run.WorkspaceID), channelID)
	if !found {
		return radarChannelExecutionTarget{}, errors.New("channel does not belong to the run workspace")
	}
	if channel.ArchivedAt != nil {
		return radarChannelExecutionTarget{}, errors.New("channel is archived")
	}
	if !h.channelHasAgentMember(ctx, run.WorkspaceID, channelID, agent.ID) {
		return radarChannelExecutionTarget{}, errors.New("radar agent is not a channel member")
	}

	target := radarChannelExecutionTarget{
		ChannelID: channelID,
		Activity: radarActivityTarget{
			Kind:    "channel",
			ID:      channelID,
			Trusted: true,
		},
	}
	if threadID := strings.TrimSpace(payload.ThreadID); threadID != "" {
		target.ThreadID = &threadID
	}

	threadRootRaw := strings.TrimSpace(payload.ThreadRootMessageID)
	if actionType == radar.ActionReplyThread && threadRootRaw == "" {
		return radarChannelExecutionTarget{}, errors.New("thread_root_message_id is required for a thread reply")
	}
	if threadRootRaw == "" {
		return target, nil
	}
	threadRoot, err := parseUUIDString(threadRootRaw)
	if err != nil {
		return radarChannelExecutionTarget{}, errors.New("invalid thread_root_message_id")
	}
	var rootExists bool
	err = h.DB.QueryRow(ctx, `
		SELECT EXISTS (
			SELECT 1
			FROM channel_message
			WHERE id = $1
			  AND channel_id = $2
			  AND workspace_id = $3
			  AND thread_root_message_id IS NULL
			  AND author_type <> 'system'
			  AND deleted_at IS NULL
		)
	`, threadRoot, channelID, run.WorkspaceID).Scan(&rootExists)
	if err != nil {
		return radarChannelExecutionTarget{}, fmt.Errorf("verify thread root: %w", err)
	}
	if !rootExists {
		return radarChannelExecutionTarget{}, errors.New("thread root does not belong to the target channel")
	}

	target.ThreadRoot = threadRoot
	target.Activity = radarActivityTarget{
		Kind:    "thread",
		ID:      threadRoot,
		Slug:    uuidToString(channelID),
		Trusted: true,
	}
	return target, nil
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
	if err := h.validateRadarIssueCreateWorkspaceTargets(ctx, run, agent, assigneeType, assigneeID, projectID); err != nil {
		return nil, err
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

func radarUUIDsMatch(left, right pgtype.UUID) bool {
	return left.Valid && right.Valid && left.Bytes == right.Bytes
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
