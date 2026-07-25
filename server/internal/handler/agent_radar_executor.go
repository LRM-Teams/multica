package handler

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"strings"
	"time"

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
	TargetAgentID       string `json:"target_agent_id"`
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

type radarTransactionalExecution struct {
	Result         map[string]any
	Activity       radarActivityTarget
	CancelledTasks []db.AgentInboxEvent
	AfterCommit    func()
}

type radarChannelExecutionTarget struct {
	ChannelID  pgtype.UUID
	Channel    ChannelResponse
	ThreadRoot pgtype.UUID
	ThreadID   *string
	Activity   radarActivityTarget
}

type radarAgentMentionDirective struct {
	TargetAgent db.Agent
	Target      radarChannelExecutionTarget
	Channel     ChannelResponse
	Content     string
	Parts       []protocol.MessagePart
}

type radarIssueCreatePayload struct {
	Title              string   `json:"title"`
	Description        string   `json:"description"`
	ProjectID          string   `json:"project_id"`
	AssigneeType       string   `json:"assignee_type"`
	AssigneeID         string   `json:"assignee_id"`
	AcceptanceCriteria []string `json:"acceptance_criteria"`
	AttachmentIDs      []string `json:"attachment_ids"`
	SourceMessageID    string   `json:"source_message_id"`
}

// Keep the historical create-issue payload name for focused authorization
// tests and any in-package callers while comment_issue uses its own schema.
type radarIssuePayload = radarIssueCreatePayload

type radarIssueCommentPayload struct {
	IssueID       string `json:"issue_id"`
	TargetAgentID string `json:"target_agent_id"`
	Content       string `json:"content"`
}

type radarIssueReworkPayload struct {
	IssueID            string    `json:"issue_id"`
	TargetAgentID      string    `json:"target_agent_id"`
	Content            string    `json:"content"`
	AcceptanceCriteria *[]string `json:"acceptance_criteria"`
}

type radarIssueDirective struct {
	TargetAgent db.Agent
	Issue       db.Issue
	Content     string
}

type radarPlanPayload struct {
	Content string `json:"content"`
}

func (h *Handler) ExecuteAgentRadarPlan(ctx context.Context, run db.AgentRadarRun, agent db.Agent, plan radar.ActionPlan) error {
	if len(plan.Actions) == 0 {
		plan.Actions = []radar.RadarAction{{Type: radar.ActionNoAction, Reason: "agent returned no actions", Confidence: "medium", RiskLevel: "low", TargetKind: "none"}}
	}
	if strings.HasPrefix(run.CooldownKey, "wendy_ambient:") {
		if err := validateAmbientCoordinationPlan(plan); err != nil {
			message := "invalid ambient coordination plan: " + err.Error()
			h.failPersistedAgentRadarRun(ctx, run, message)
			return errors.New(message)
		}
	}
	if run.TriggerKind == "scheduled" {
		if err := h.validateScheduledRadarSupervisor(ctx, run, agent); err != nil {
			_, _ = h.Queries.UpdateAgentRadarRunStatus(ctx, db.UpdateAgentRadarRunStatusParams{
				ID:     run.ID,
				Status: "cancelled",
				Error:  pgtype.Text{String: err.Error(), Valid: true},
			})
			return err
		}
		if len(plan.Actions) > 3 {
			return h.failWorkspaceRadarPlan(ctx, run, "workspace supervisor returned more than 3 actions")
		}
		if len(plan.Actions) > 1 {
			for _, action := range plan.Actions {
				if action.Type == radar.ActionNoAction {
					return h.failWorkspaceRadarPlan(ctx, run, "no_action must be the only workspace supervisor action")
				}
			}
		}
	}
	status := "succeeded"
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
	if err != nil {
		return err
	}
	if status == "succeeded" || status == "no_action" {
		_, err = h.Queries.MarkWorkspaceRadarSucceeded(ctx, run.ID)
	} else {
		_, err = h.Queries.MarkWorkspaceRadarFailedByRunID(ctx, run.ID)
	}
	// Settle the ambient watch (#2): clear dirty precisely on success, re-arm on
	// failure. Never lose a review because the run failed after enqueue.
	if h.WorkGraph != nil && strings.HasPrefix(run.CooldownKey, "wendy_ambient:") {
		reviewSucceeded := status == "succeeded" || status == "no_action"
		if rerr := h.WorkGraph.ReconcileChannelAmbientRun(ctx, run.ID, reviewSucceeded); rerr != nil {
			slog.Warn("reconcile wendy ambient run failed", "run_id", uuidToString(run.ID), "status", status, "error", rerr)
		}
	}
	return err
}

func validateAmbientCoordinationPlan(plan radar.ActionPlan) error {
	if len(plan.Actions) > 5 {
		return errors.New("more than 5 actions")
	}
	allowed := map[string]struct{}{
		radar.ActionNoAction:           {},
		radar.ActionMentionAgent:       {},
		radar.ActionCommentIssue:       {},
		radar.ActionCreateIssue:        {},
		radar.ActionRequestRework:      {},
		radar.ActionPostChannelMessage: {},
	}
	for _, action := range plan.Actions {
		if _, ok := allowed[action.Type]; !ok {
			return fmt.Errorf("action %s is outside the ambient allowlist", action.Type)
		}
		if action.Type == radar.ActionNoAction && len(plan.Actions) != 1 {
			return errors.New("no_action must be the only ambient action")
		}
	}
	return nil
}

func (h *Handler) validateScheduledRadarSupervisor(ctx context.Context, run db.AgentRadarRun, agent db.Agent) error {
	return h.validateScheduledRadarSupervisorWithDB(ctx, h.DB, run, agent, false)
}

func (h *Handler) validateScheduledRadarSupervisorWithDB(ctx context.Context, exec db.DBTX, run db.AgentRadarRun, agent db.Agent, lock bool) error {
	if !radarUUIDsMatch(run.WorkspaceID, agent.WorkspaceID) || !radarUUIDsMatch(run.AgentID, agent.ID) {
		return errors.New("scheduled radar agent does not match the run")
	}
	query := `
		SELECT state.workspace_id
		FROM workspace_radar_state state
		JOIN agent supervisor
		  ON supervisor.workspace_id = state.workspace_id
		 AND supervisor.id = state.supervisor_agent_id
		JOIN member owner_member
		  ON owner_member.workspace_id = state.workspace_id
		 AND owner_member.user_id = supervisor.owner_id
		 AND owner_member.role = 'owner'
		WHERE state.workspace_id = $1
		  AND state.supervisor_agent_id = $2
		  AND state.enabled
		  AND supervisor.archived_at IS NULL`
	if lock {
		query += ` FOR SHARE OF state, supervisor, owner_member`
	}
	var workspaceID pgtype.UUID
	if err := exec.QueryRow(ctx, query, run.WorkspaceID, agent.ID).Scan(&workspaceID); err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return errors.New("scheduled radar supervisor owner is no longer a workspace owner")
		}
		return fmt.Errorf("verify workspace radar supervisor: %w", err)
	}
	return nil
}

func (h *Handler) failWorkspaceRadarPlan(ctx context.Context, run db.AgentRadarRun, message string) error {
	_, updateErr := h.Queries.UpdateAgentRadarRunStatus(ctx, db.UpdateAgentRadarRunStatusParams{
		ID:     run.ID,
		Status: "failed",
		Error:  pgtype.Text{String: message, Valid: true},
	})
	if updateErr == nil {
		_, updateErr = h.Queries.MarkWorkspaceRadarFailedByRunID(ctx, run.ID)
	}
	if updateErr != nil {
		return updateErr
	}
	return errors.New(message)
}

func (h *Handler) executeAgentRadarAction(ctx context.Context, run db.AgentRadarRun, agent db.Agent, action radar.RadarAction) error {
	sourceDedupeKey := strings.TrimSpace(action.DedupeKey)
	if run.TriggerKind == "scheduled" {
		if err := h.validateScheduledRadarSupervisor(ctx, run, agent); err != nil {
			return err
		}
		if action.Type == radar.ActionNoAction {
			// A model-provided no_action key must never reserve the server-derived
			// key of a later visible action in the same review window.
			action.DedupeKey = ""
		} else {
			if !run.ScheduledFor.Valid {
				return errors.New("scheduled radar run scheduled_for is required")
			}
			// Do not trust the model to choose a stable key. A changing key would
			// allow the same target to be nudged repeatedly in one review window.
			// Derive the execution key from the server-validated action target;
			// keep the model key only as diagnostic metadata below.
			action.DedupeKey = radarScheduledOccurrenceDedupeKey(radarScheduledActionDedupeBase(action), run.ScheduledFor.Time)
		}
	}
	createParams := radarActionCreateParams(run, action, "approved")
	if run.TriggerKind == "scheduled" && (action.Type == radar.ActionCommentIssue || action.Type == radar.ActionMentionAgent) {
		createParams.Status = "executing"
		return h.executeScheduledVisibleRadarAction(ctx, run, agent, action, sourceDedupeKey, createParams)
	}

	row, err := h.Queries.CreateAgentRadarAction(ctx, createParams)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return h.recordSkippedRadarAction(ctx, run, action, createParams)
		}
		return err
	}
	result, activityTarget, execErr := h.executeApprovedRadarActionWithTarget(ctx, run, agent, action)
	status := "executed"
	errText := pgtype.Text{}
	if execErr != nil {
		status = "failed"
		errText = pgtype.Text{String: execErr.Error(), Valid: true}
		if result == nil {
			result = map[string]any{}
		}
		result["error"] = execErr.Error()
		if result["comment_id"] != nil || result["channel_message_id"] != nil || result["task_id"] != nil || result["inbox_event_id"] != nil {
			result["partial"] = true
		}
	}
	resultJSON, _ := json.Marshal(result)
	_, updateErr := h.Queries.UpdateAgentRadarActionStatus(ctx, db.UpdateAgentRadarActionStatusParams{
		ID:     row.ID,
		Status: status,
		Result: resultJSON,
		Error:  errText,
	})
	if updateErr != nil {
		return updateErr
	}
	h.recordRadarActionActivity(ctx, run, agent, action, sourceDedupeKey, status, result, activityTarget)
	return execErr
}

func radarActionCreateParams(run db.AgentRadarRun, action radar.RadarAction, status string) db.CreateAgentRadarActionParams {
	evidence, _ := json.Marshal(action.Evidence)
	if len(evidence) == 0 {
		evidence = []byte("[]")
	}
	payload := []byte(action.Payload)
	if len(payload) == 0 {
		payload = []byte("{}")
	}
	targetID := parseOptionalUUID(action.TargetID)
	return db.CreateAgentRadarActionParams{
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
		Status:      status,
	}
}

func (h *Handler) recordRadarActionActivity(ctx context.Context, run db.AgentRadarRun, agent db.Agent, action radar.RadarAction, sourceDedupeKey, status string, result map[string]any, activityTarget radarActivityTarget) {
	activityDetails := map[string]any{
		"radar_run_id":      uuidToString(run.ID),
		"source_dedupe_key": sourceDedupeKey,
		"dedupe_key":        action.DedupeKey,
		"reason":            action.Reason,
		"evidence":          action.Evidence,
		"result":            result,
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
}

func (h *Handler) executeScheduledVisibleRadarAction(ctx context.Context, run db.AgentRadarRun, agent db.Agent, action radar.RadarAction, sourceDedupeKey string, createParams db.CreateAgentRadarActionParams) error {
	if h.TxStarter == nil {
		return errors.New("scheduled radar transaction starter unavailable")
	}
	tx, err := h.TxStarter.Begin(ctx)
	if err != nil {
		return fmt.Errorf("begin scheduled radar action transaction: %w", err)
	}
	defer tx.Rollback(ctx)
	qtx := h.Queries.WithTx(tx)

	// Lock order is workspace -> run -> supervisor state. Enqueue and binding
	// changes use the same workspace-first order, so a stale executor cannot
	// commit a visible nudge after the run has been reconciled or Wendy rebound.
	if err := h.lockScheduledRadarExecutionLease(ctx, tx, run, agent); err != nil {
		return err
	}
	existingID, existingStatus, duplicate, err := h.lockAndFindRecentScheduledRadarTarget(ctx, tx, run, action)
	if err != nil {
		return err
	}
	if duplicate {
		if err := h.recordSkippedRadarActionForExistingWithDB(ctx, qtx, createParams, existingID, existingStatus); err != nil {
			return err
		}
		if err := tx.Commit(ctx); err != nil {
			return fmt.Errorf("commit rolling-window skipped radar action: %w", err)
		}
		return nil
	}
	if _, err := tx.Exec(ctx, `SAVEPOINT scheduled_radar_receipt`); err != nil {
		return fmt.Errorf("create scheduled radar receipt savepoint: %w", err)
	}
	receipt, err := qtx.CreateAgentRadarAction(ctx, createParams)
	if err != nil {
		if isUniqueViolation(err) {
			// A workspace-scoped partial unique index can reject a receipt owned
			// by a previous Wendy. Recover the aborted statement before recording
			// the immutable skipped attempt.
			if _, rollbackErr := tx.Exec(ctx, `ROLLBACK TO SAVEPOINT scheduled_radar_receipt`); rollbackErr != nil {
				return fmt.Errorf("rollback duplicate scheduled radar receipt: %w", rollbackErr)
			}
			if err := h.recordSkippedRadarActionWithDB(ctx, qtx, tx, run, action, createParams); err != nil {
				return err
			}
			if err := tx.Commit(ctx); err != nil {
				return fmt.Errorf("commit skipped scheduled radar action: %w", err)
			}
			return nil
		}
		if errors.Is(err, pgx.ErrNoRows) {
			if err := h.recordSkippedRadarActionWithDB(ctx, qtx, tx, run, action, createParams); err != nil {
				return err
			}
			if err := tx.Commit(ctx); err != nil {
				return fmt.Errorf("commit skipped scheduled radar action: %w", err)
			}
			return nil
		}
		return fmt.Errorf("create scheduled radar action receipt: %w", err)
	}
	if _, err := tx.Exec(ctx, `RELEASE SAVEPOINT scheduled_radar_receipt`); err != nil {
		return fmt.Errorf("release scheduled radar receipt savepoint: %w", err)
	}
	if _, err := tx.Exec(ctx, `SAVEPOINT scheduled_radar_visible_effect`); err != nil {
		return fmt.Errorf("create scheduled radar action savepoint: %w", err)
	}

	var execution radarTransactionalExecution
	switch action.Type {
	case radar.ActionCommentIssue:
		execution, err = h.executeRadarIssueCommentInTx(ctx, qtx, tx, run, agent, action)
	case radar.ActionMentionAgent:
		execution, err = h.executeRadarAgentMentionInTx(ctx, qtx, tx, run, agent, action)
	default:
		err = fmt.Errorf("scheduled workspace radar action %s is not a visible transactional action", action.Type)
	}
	if err != nil {
		execErr := err
		if _, rollbackErr := tx.Exec(ctx, `ROLLBACK TO SAVEPOINT scheduled_radar_visible_effect`); rollbackErr != nil {
			return fmt.Errorf("rollback failed scheduled radar action: %w", rollbackErr)
		}
		result := execution.Result
		if result == nil {
			result = map[string]any{}
		}
		result["error"] = execErr.Error()
		resultJSON, _ := json.Marshal(result)
		if _, updateErr := qtx.UpdateAgentRadarActionStatus(ctx, db.UpdateAgentRadarActionStatusParams{
			ID:     receipt.ID,
			Status: "failed",
			Result: resultJSON,
			Error:  pgtype.Text{String: execErr.Error(), Valid: true},
		}); updateErr != nil {
			return fmt.Errorf("mark scheduled radar action failed: %w", updateErr)
		}
		if commitErr := tx.Commit(ctx); commitErr != nil {
			return fmt.Errorf("commit failed scheduled radar action receipt: %w", commitErr)
		}
		h.recordRadarActionActivity(ctx, run, agent, action, sourceDedupeKey, "failed", result, execution.Activity)
		return execErr
	}

	resultJSON, _ := json.Marshal(execution.Result)
	if _, err := qtx.UpdateAgentRadarActionStatus(ctx, db.UpdateAgentRadarActionStatusParams{
		ID:     receipt.ID,
		Status: "executed",
		Result: resultJSON,
	}); err != nil {
		return fmt.Errorf("mark scheduled radar action executed: %w", err)
	}
	if err := tx.Commit(ctx); err != nil {
		return fmt.Errorf("commit scheduled radar action: %w", err)
	}
	if execution.AfterCommit != nil {
		execution.AfterCommit()
	}
	h.recordRadarActionActivity(ctx, run, agent, action, sourceDedupeKey, "executed", execution.Result, execution.Activity)
	return nil
}

func (h *Handler) lockScheduledRadarExecutionLease(ctx context.Context, tx pgx.Tx, run db.AgentRadarRun, agent db.Agent) error {
	var workspaceID pgtype.UUID
	if err := tx.QueryRow(ctx, `
		SELECT id
		FROM workspace
		WHERE id = $1
		FOR SHARE
	`, run.WorkspaceID).Scan(&workspaceID); err != nil {
		return fmt.Errorf("lock scheduled radar workspace: %w", err)
	}

	var runID pgtype.UUID
	if err := tx.QueryRow(ctx, `
		UPDATE agent_radar_run
		SET updated_at = now()
		WHERE id = $1
		  AND workspace_id = $2
		  AND agent_id = $3
		  AND status = 'executing'
		RETURNING id
	`, run.ID, run.WorkspaceID, agent.ID).Scan(&runID); err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return errors.New("scheduled radar execution lease is no longer active")
		}
		return fmt.Errorf("refresh scheduled radar execution lease: %w", err)
	}
	if err := h.validateScheduledRadarSupervisorWithDB(ctx, tx, run, agent, true); err != nil {
		return err
	}
	return nil
}

func (h *Handler) lockAndFindRecentScheduledRadarTarget(ctx context.Context, tx pgx.Tx, run db.AgentRadarRun, action radar.RadarAction) (pgtype.UUID, string, bool, error) {
	base := radarScheduledActionDedupeBase(action)
	lockKey := uuidToString(run.WorkspaceID) + "|" + base
	if _, err := tx.Exec(ctx, `SELECT pg_advisory_xact_lock(hashtextextended($1, 0))`, lockKey); err != nil {
		return pgtype.UUID{}, "", false, fmt.Errorf("lock scheduled radar target window: %w", err)
	}

	var existingID pgtype.UUID
	var existingStatus string
	err := tx.QueryRow(ctx, `
		SELECT action.id, action.status
		FROM agent_radar_action action
		JOIN agent_radar_run prior_run ON prior_run.id = action.radar_run_id
		WHERE action.workspace_id = $1
		  AND left(action.dedupe_key, length($2) + 1) = $2 || ':'
		  AND action.status IN ('executing', 'executed')
		  AND prior_run.trigger_kind = 'scheduled'
		  AND prior_run.cooldown_key = 'workspace_supervisor_radar'
		  AND action.created_at > now() - interval '6 hours'
		ORDER BY action.created_at DESC, action.id DESC
		LIMIT 1
	`, run.WorkspaceID, base).Scan(&existingID, &existingStatus)
	if errors.Is(err, pgx.ErrNoRows) {
		return pgtype.UUID{}, "", false, nil
	}
	if err != nil {
		return pgtype.UUID{}, "", false, fmt.Errorf("load recent scheduled radar target: %w", err)
	}
	return existingID, existingStatus, true, nil
}

func radarScheduledOccurrenceDedupeKey(base string, scheduledFor time.Time) string {
	return strings.TrimSpace(base) + ":" + scheduledFor.UTC().Format("20060102T150405.000000000Z")
}

func radarScheduledActionDedupeBase(action radar.RadarAction) string {
	// Scheduled supervision belongs to the workspace, not to whichever Wendy
	// row happens to be bound today. A replacement Wendy must observe the same
	// target receipt in the same review window.
	return "workspace-supervisor:" + radarScheduledActionTargetKey(action)
}

func radarScheduledActionTargetKey(action radar.RadarAction) string {
	switch action.Type {
	case radar.ActionCommentIssue:
		var payload radarIssueCommentPayload
		if json.Unmarshal(action.Payload, &payload) == nil {
			issueID, issueOK := canonicalRadarUUID(payload.IssueID)
			targetAgentID, targetOK := canonicalRadarUUID(payload.TargetAgentID)
			if issueOK && targetOK {
				return strings.Join([]string{radar.ActionCommentIssue, issueID, targetAgentID}, ":")
			}
		}
	case radar.ActionMentionAgent:
		var payload radarChannelPayload
		if json.Unmarshal(action.Payload, &payload) == nil {
			channelID, channelOK := canonicalRadarUUID(payload.ChannelID)
			targetAgentID, targetOK := canonicalRadarUUID(payload.TargetAgentID)
			if channelOK && targetOK {
				return strings.Join([]string{radar.ActionMentionAgent, channelID, targetAgentID}, ":")
			}
		}
	}
	// Invalid payloads are rejected by the executor after their action row is
	// recorded. Keep their dedupe deterministic without relying on model text.
	return action.Type + ":invalid-target"
}

func canonicalRadarUUID(raw string) (string, bool) {
	parsed, err := uuid.Parse(strings.TrimSpace(raw))
	if err != nil {
		return "", false
	}
	return parsed.String(), true
}

func (h *Handler) recordSkippedRadarAction(ctx context.Context, run db.AgentRadarRun, action radar.RadarAction, params db.CreateAgentRadarActionParams) error {
	return h.recordSkippedRadarActionWithDB(ctx, h.Queries, h.DB, run, action, params)
}

func (h *Handler) recordSkippedRadarActionWithDB(ctx context.Context, q *db.Queries, exec db.DBTX, run db.AgentRadarRun, action radar.RadarAction, params db.CreateAgentRadarActionParams) error {
	var existingID pgtype.UUID
	var existingStatus string
	query := `
		SELECT id, status
		FROM agent_radar_action
		WHERE workspace_id = $1
		  AND dedupe_key = $2
		  AND status IN ('proposed', 'approved', 'executing', 'executed')`
	args := []any{run.WorkspaceID, action.DedupeKey}
	if run.TriggerKind != "scheduled" {
		query += ` AND agent_id = $3`
		args = append(args, run.AgentID)
	}
	query += ` ORDER BY created_at DESC, id DESC LIMIT 1`
	if err := exec.QueryRow(ctx, query, args...).Scan(&existingID, &existingStatus); err != nil {
		return fmt.Errorf("load duplicate radar action: %w", err)
	}
	return h.recordSkippedRadarActionForExistingWithDB(ctx, q, params, existingID, existingStatus)
}

func (h *Handler) recordSkippedRadarActionForExistingWithDB(ctx context.Context, q *db.Queries, params db.CreateAgentRadarActionParams, existingID pgtype.UUID, existingStatus string) error {
	params.Status = "skipped"
	skipped, err := q.CreateAgentRadarAction(ctx, params)
	if err != nil {
		return fmt.Errorf("record skipped radar action: %w", err)
	}
	result, _ := json.Marshal(map[string]any{
		"status":             "skipped",
		"reason":             "duplicate_dedupe_key",
		"existing_action_id": uuidToString(existingID),
		"existing_status":    existingStatus,
	})
	_, err = q.UpdateAgentRadarActionStatus(ctx, db.UpdateAgentRadarActionStatusParams{
		ID:     skipped.ID,
		Status: "skipped",
		Result: result,
	})
	return err
}

func (h *Handler) executeApprovedRadarActionWithTarget(ctx context.Context, run db.AgentRadarRun, agent db.Agent, action radar.RadarAction) (map[string]any, radarActivityTarget, error) {
	if run.TriggerKind == "scheduled" && action.Type != radar.ActionNoAction && action.Type != radar.ActionCommentIssue && action.Type != radar.ActionMentionAgent {
		return map[string]any{"blocked": true}, radarActivityTarget{}, fmt.Errorf("scheduled workspace radar action %s is not allowed", action.Type)
	}
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
	case radar.ActionCommentIssue:
		return h.executeRadarIssueCommentWithTarget(ctx, run, agent, action)
	case radar.ActionRequestRework:
		return h.executeRadarRequestReworkWithTarget(ctx, run, agent, action)
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
	if action.Type == radar.ActionMentionAgent {
		directive, err := h.prepareRadarAgentMention(ctx, run, agent, action)
		if err != nil {
			return nil, radarActivityTarget{}, err
		}
		return h.executeRadarAgentMentionAtomic(ctx, run, agent, directive)
	}

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
	content, parts, err := h.finalizeAgentChannelMessage(ctx, target.Channel, content, nil)
	if err != nil {
		return nil, target.Activity, fmt.Errorf("finalize radar channel post: %w", err)
	}
	msg, err := h.insertChannelMessageWithParts(ctx, target.ChannelID, run.WorkspaceID, "agent", agent.ID, agentDisplayName(agent), content, parts, "multica", nil, pgtype.UUID{}, target.ThreadRoot, target.ThreadID, 0)
	if err != nil {
		return nil, target.Activity, err
	}
	messages := []ChannelMessageResponse{msg}
	h.attachChannelMessageAuthorAvatars(ctx, uuidToString(run.WorkspaceID), messages)
	msg = messages[0]
	_, _ = h.DB.Exec(ctx, `UPDATE channel SET updated_at = now() WHERE id = $1`, target.ChannelID)
	h.clearDMHiddenForChannelMembers(ctx, uuidToString(run.WorkspaceID), target.ChannelID)
	h.publishChannelToMembers(ctx, protocol.EventChannelMessage, uuidToString(run.WorkspaceID), "agent", uuidToString(agent.ID), target.ChannelID, msg)
	return map[string]any{"channel_message_id": msg.ID}, target.Activity, nil
}

func (h *Handler) prepareRadarAgentMention(ctx context.Context, run db.AgentRadarRun, supervisor db.Agent, action radar.RadarAction) (radarAgentMentionDirective, error) {
	var payload radarChannelPayload
	if err := json.Unmarshal(action.Payload, &payload); err != nil {
		return radarAgentMentionDirective{}, err
	}
	if strings.TrimSpace(payload.ChannelID) == "" || strings.TrimSpace(payload.TargetAgentID) == "" {
		return radarAgentMentionDirective{}, errors.New("channel_id and target_agent_id are required")
	}
	if err := validateRadarDirectiveContent(payload.Content); err != nil {
		return radarAgentMentionDirective{}, err
	}
	targetAgent, err := h.resolveRadarDirectiveAgent(ctx, run, supervisor, payload.TargetAgentID)
	if err != nil {
		return radarAgentMentionDirective{}, err
	}
	if !supervisor.OwnerID.Valid {
		return radarAgentMentionDirective{}, errors.New("radar supervisor has no owner")
	}
	target, err := h.resolveRadarChannelExecutionTarget(ctx, run, supervisor, action.Type, payload)
	if err != nil {
		return radarAgentMentionDirective{}, err
	}
	if !h.channelHasAgentMember(ctx, run.WorkspaceID, target.ChannelID, targetAgent.ID) {
		return radarAgentMentionDirective{}, errors.New("target agent is not a channel member")
	}
	ch, found := h.getChannel(ctx, uuidToString(run.WorkspaceID), target.ChannelID)
	if !found {
		return radarAgentMentionDirective{}, errors.New("channel does not belong to the run workspace")
	}
	content, parts := directedAgentMentionContent(targetAgent, payload.Content)
	return radarAgentMentionDirective{TargetAgent: targetAgent, Target: target, Channel: ch, Content: content, Parts: parts}, nil
}

func directedAgentMentionLabel(handle string) string {
	// Stable handles are unique, but still neutralise Markdown delimiters before
	// placing the visible label into canonical content.
	replacer := strings.NewReplacer(
		"[", "［", "]", "］", "(", "（", ")", "）",
		"\r", " ", "\n", " ", "\t", " ",
	)
	safeHandle := strings.Join(strings.Fields(replacer.Replace(strings.TrimSpace(handle))), " ")
	if safeHandle == "" {
		safeHandle = "agent"
	}
	return "@" + safeHandle
}

func directedAgentMentionContent(target db.Agent, body string) (string, []protocol.MessagePart) {
	label := directedAgentMentionLabel(target.Name)
	// Prefix with the proactive marker so Beckham's directed nudges (a) render as
	// a normal main-timeline message with leading text rather than a bare mention,
	// and (b) get the "主动发现" pill like post_channel_message proactive posts.
	const proactivePrefix = "主动发现："
	content := proactivePrefix + label + " " + strings.TrimSpace(body)
	prefixLen := len(proactivePrefix)
	start, end := contentUTF16Span(content, prefixLen, prefixLen+len(label))
	return content, []protocol.MessagePart{{
		Type:              protocol.MessagePartTypeReference,
		RefType:           "mention",
		RefSubType:        "agent",
		RefID:             uuidToString(target.ID),
		Label:             label,
		ContentStartUTF16: &start,
		ContentEndUTF16:   &end,
	}}
}

// Issue comments do not yet carry MessagePart metadata. Keep their historical
// comment-local representation until that surface has the structured contract;
// channel directives above never use this legacy form.
func formatRadarIssueDirectiveMention(handle, id string) string {
	label := directedAgentMentionLabel(handle)
	return "[" + label + "](mention://agent/" + id + ")"
}

func (h *Handler) executeRadarAgentMentionAtomic(ctx context.Context, run db.AgentRadarRun, supervisor db.Agent, directive radarAgentMentionDirective) (map[string]any, radarActivityTarget, error) {
	if h.TxStarter == nil {
		return nil, directive.Target.Activity, errors.New("channel transaction starter unavailable")
	}

	tx, err := h.TxStarter.Begin(ctx)
	if err != nil {
		return nil, directive.Target.Activity, fmt.Errorf("begin radar directive transaction: %w", err)
	}
	defer tx.Rollback(ctx)
	qtx := h.Queries.WithTx(tx)
	execution, err := h.executePreparedRadarAgentMentionInTx(ctx, qtx, tx, run, supervisor, directive)
	if err != nil {
		return execution.Result, execution.Activity, err
	}
	if err := tx.Commit(ctx); err != nil {
		return nil, execution.Activity, fmt.Errorf("commit radar directive: %w", err)
	}
	if execution.AfterCommit != nil {
		execution.AfterCommit()
	}
	return execution.Result, execution.Activity, nil
}

func (h *Handler) executeRadarAgentMentionInTx(ctx context.Context, qtx *db.Queries, exec db.DBTX, run db.AgentRadarRun, supervisor db.Agent, action radar.RadarAction) (radarTransactionalExecution, error) {
	directive, err := h.prepareRadarAgentMention(ctx, run, supervisor, action)
	if err != nil {
		return radarTransactionalExecution{}, err
	}
	return h.executePreparedRadarAgentMentionInTx(ctx, qtx, exec, run, supervisor, directive)
}

func (h *Handler) executePreparedRadarAgentMentionInTx(ctx context.Context, qtx *db.Queries, exec db.DBTX, run db.AgentRadarRun, supervisor db.Agent, directive radarAgentMentionDirective) (radarTransactionalExecution, error) {
	execution := radarTransactionalExecution{Activity: directive.Target.Activity}
	if run.TriggerKind == "scheduled" {
		if err := h.validateScheduledRadarSupervisorWithDB(ctx, exec, run, supervisor, true); err != nil {
			return execution, err
		}
	}
	if err := h.lockRadarChannelDirectiveTarget(ctx, exec, run.WorkspaceID, directive.Target.ChannelID, directive.TargetAgent.ID, directive.Target.ThreadRoot); err != nil {
		return execution, err
	}
	content, parts, err := h.finalizeAgentChannelMessage(ctx, directive.Channel, directive.Content, directive.Parts)
	if err != nil {
		return execution, fmt.Errorf("finalize radar directive: %w", err)
	}
	if err := validateFinalizedRadarDirectiveMentions(parts, directive.TargetAgent.ID); err != nil {
		return execution, err
	}
	directive.Content = content
	directive.Parts = parts

	inserted, err := insertChannelMessageWithPartsExec(
		ctx, exec, directive.Target.ChannelID, run.WorkspaceID,
		"agent", supervisor.ID, agentDisplayName(supervisor), directive.Content, directive.Parts,
		"multica", nil, nil, pgtype.UUID{}, pgtype.UUID{}, nil,
		directive.Target.ThreadRoot, directive.Target.ThreadID, 0,
	)
	if err != nil {
		return execution, fmt.Errorf("create visible radar directive: %w", err)
	}
	msg := inserted.Message
	if _, err := exec.Exec(ctx, `UPDATE channel SET updated_at = now() WHERE id = $1`, directive.Target.ChannelID); err != nil {
		return execution, fmt.Errorf("update directed channel: %w", err)
	}

	rootID := h.channelThreadRootForTrigger(directive.Channel, msg)
	facilitatorState := h.loadChannelFacilitatorState(ctx, rootID, directive.TargetAgent.ID, msg)
	prompt := h.buildChannelMentionPrompt(ctx, directive.Channel, msg, facilitatorState)
	txResult, err := h.enqueueChannelAgentPromptWithTx(ctx, qtx, exec, directive.Channel, directive.TargetAgent, msg, supervisor.OwnerID, prompt, "mention", 10)
	if err != nil {
		return execution, fmt.Errorf("persist directed wake: %w", err)
	}
	execution.Result = map[string]any{
		"channel_message_id": msg.ID,
		"inbox_event_id":     uuidToString(txResult.Event.ID),
	}
	execution.AfterCommit = func() {
		// Realtime and activity publication must observe committed rows only.
		h.publishRearmedManagedPatrol(ctx, inserted.RearmedManagedPatrol)
		h.clearDMHiddenForChannelMembers(ctx, uuidToString(run.WorkspaceID), directive.Target.ChannelID)
		messages := []ChannelMessageResponse{msg}
		h.attachChannelMessageAuthorAvatars(ctx, uuidToString(run.WorkspaceID), messages)
		msg = messages[0]
		h.publishChannelToMembers(ctx, protocol.EventChannelMessage, uuidToString(run.WorkspaceID), "agent", uuidToString(supervisor.ID), directive.Target.ChannelID, msg)
		h.recordChannelAgentPromptWake(ctx, directive.Channel, directive.TargetAgent, msg, "mention", txResult)
		if msg.ThreadRootMessageID != nil {
			h.followChannelThreadAgent(ctx, directive.Target.ChannelID, parseUUID(*msg.ThreadRootMessageID), directive.TargetAgent.ID)
		}
		// Escalation ladder: if this directive came from the channel's group
		// manager (Beckham), raise the nudged agent's escalation level.
		if mgrID, ok := h.resolveGroupManagerForChannel(ctx, run.WorkspaceID, directive.Target.ChannelID); ok && uuidToString(mgrID) == uuidToString(supervisor.ID) {
			h.incrementNudgeLadder(ctx, run.WorkspaceID, directive.Target.ChannelID, directive.TargetAgent.ID)
		}
	}
	return execution, nil
}

func (h *Handler) lockRadarChannelDirectiveTarget(ctx context.Context, exec db.DBTX, workspaceID, channelID, targetAgentID, threadRootID pgtype.UUID) error {
	var lockedAgentID pgtype.UUID
	err := exec.QueryRow(ctx, `
		SELECT target.id
		FROM channel ch
		JOIN channel_member cm
		  ON cm.channel_id = ch.id
		 AND cm.workspace_id = ch.workspace_id
		 AND cm.member_type = 'agent'
		JOIN agent target
		  ON target.id = cm.member_id
		 AND target.workspace_id = ch.workspace_id
		JOIN agent_runtime runtime
		  ON runtime.id = target.runtime_id
		 AND runtime.workspace_id = ch.workspace_id
		WHERE ch.id = $1
		  AND ch.workspace_id = $2
		  AND ch.kind = 'group'
		  AND ch.archived_at IS NULL
		  AND cm.member_id = $3
		  AND target.archived_at IS NULL
		FOR SHARE OF ch, cm, target, runtime
	`, channelID, workspaceID, targetAgentID).Scan(&lockedAgentID)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return errors.New("channel or target agent is no longer eligible for a directed wake")
		}
		return fmt.Errorf("lock radar channel directive target: %w", err)
	}
	if threadRootID.Valid {
		var lockedRootID pgtype.UUID
		if err := exec.QueryRow(ctx, `
			SELECT id
			FROM channel_message
			WHERE id = $1
			  AND channel_id = $2
			  AND workspace_id = $3
			  AND thread_root_message_id IS NULL
			  AND author_type <> 'system'
			  AND deleted_at IS NULL
			FOR SHARE
		`, threadRootID, channelID, workspaceID).Scan(&lockedRootID); err != nil {
			if errors.Is(err, pgx.ErrNoRows) {
				return errors.New("thread root is no longer eligible for a directed wake")
			}
			return fmt.Errorf("lock radar directive thread root: %w", err)
		}
	}
	return nil
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
	if actionType == radar.ActionMentionAgent {
		if channel.Kind != "group" {
			return radarChannelExecutionTarget{}, errors.New("radar directives may only target group channels")
		}
		if run.TriggerKind != "scheduled" && !h.channelHasAgentMember(ctx, run.WorkspaceID, channelID, agent.ID) {
			return radarChannelExecutionTarget{}, errors.New("radar agent is not a channel member")
		}
	} else if !h.channelHasAgentMember(ctx, run.WorkspaceID, channelID, agent.ID) {
		return radarChannelExecutionTarget{}, errors.New("radar agent is not a channel member")
	}

	target := radarChannelExecutionTarget{
		ChannelID: channelID,
		Channel:   channel,
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

func validateRadarDirectiveContent(content string) error {
	trimmed := strings.TrimSpace(content)
	if trimmed == "" {
		return errors.New("directive content is required")
	}
	if len(trimmed) > 8000 {
		return errors.New("directive content is too long")
	}
	if strings.Contains(strings.ToLower(trimmed), "mention://") {
		return errors.New("directive content must not contain mention links")
	}
	return nil
}

func validateFinalizedRadarDirectiveMentions(parts []protocol.MessagePart, targetAgentID pgtype.UUID) error {
	wantID := uuidToString(targetAgentID)
	mentionCount := 0
	for _, part := range parts {
		if part.Type != protocol.MessagePartTypeReference || part.RefType != "mention" {
			continue
		}
		mentionCount++
		if part.RefSubType != "agent" || part.RefID != wantID {
			return errors.New("radar directive may only mention its target agent")
		}
	}
	if mentionCount == 0 {
		return errors.New("radar directive target mention is required")
	}
	return nil
}

func (h *Handler) resolveRadarDirectiveAgent(ctx context.Context, run db.AgentRadarRun, supervisor db.Agent, rawTargetID string) (db.Agent, error) {
	if !radarUUIDsMatch(run.WorkspaceID, supervisor.WorkspaceID) {
		return db.Agent{}, errors.New("radar agent does not belong to the run workspace")
	}
	if run.AgentID.Valid && !radarUUIDsMatch(run.AgentID, supervisor.ID) {
		return db.Agent{}, errors.New("radar agent does not match the run")
	}
	targetID, err := parseUUIDString(strings.TrimSpace(rawTargetID))
	if err != nil {
		return db.Agent{}, errors.New("invalid target_agent_id")
	}
	if radarUUIDsMatch(targetID, supervisor.ID) {
		return db.Agent{}, errors.New("radar supervisor cannot direct itself")
	}
	target, err := h.Queries.GetAgentInWorkspace(ctx, db.GetAgentInWorkspaceParams{
		ID:          targetID,
		WorkspaceID: run.WorkspaceID,
	})
	if err != nil {
		return db.Agent{}, errors.New("target agent does not belong to the run workspace")
	}
	if target.ArchivedAt.Valid {
		return db.Agent{}, errors.New("target agent is archived")
	}
	if !target.RuntimeID.Valid {
		return db.Agent{}, errors.New("target agent has no runtime")
	}
	_, err = h.Queries.GetAgentRuntimeForWorkspace(ctx, db.GetAgentRuntimeForWorkspaceParams{
		ID:          target.RuntimeID,
		WorkspaceID: run.WorkspaceID,
	})
	if err != nil {
		return db.Agent{}, errors.New("target agent runtime does not belong to the run workspace")
	}
	return target, nil
}

func (h *Handler) executeRadarIssueCommentWithTarget(ctx context.Context, run db.AgentRadarRun, supervisor db.Agent, action radar.RadarAction) (map[string]any, radarActivityTarget, error) {
	directive, err := h.prepareRadarIssueDirective(ctx, run, supervisor, action)
	if err != nil {
		return nil, radarActivityTarget{}, err
	}
	activityTarget := radarActivityTarget{Kind: "issue", ID: directive.Issue.ID, Trusted: true}
	if h.TxStarter == nil {
		return nil, activityTarget, errors.New("issue transaction starter unavailable")
	}
	tx, err := h.TxStarter.Begin(ctx)
	if err != nil {
		return nil, activityTarget, fmt.Errorf("begin radar issue directive transaction: %w", err)
	}
	defer tx.Rollback(ctx)
	execution, err := h.executePreparedRadarIssueCommentInTx(ctx, h.Queries.WithTx(tx), tx, run, supervisor, directive)
	if err != nil {
		return execution.Result, execution.Activity, err
	}
	if err := tx.Commit(ctx); err != nil {
		return nil, execution.Activity, fmt.Errorf("commit radar issue directive: %w", err)
	}
	if execution.AfterCommit != nil {
		execution.AfterCommit()
	}
	return execution.Result, execution.Activity, nil
}

func (h *Handler) executeRadarRequestReworkWithTarget(ctx context.Context, run db.AgentRadarRun, supervisor db.Agent, action radar.RadarAction) (map[string]any, radarActivityTarget, error) {
	var payload radarIssueReworkPayload
	if err := json.Unmarshal(action.Payload, &payload); err != nil {
		return nil, radarActivityTarget{}, err
	}
	if strings.TrimSpace(payload.IssueID) == "" || strings.TrimSpace(payload.TargetAgentID) == "" {
		return nil, radarActivityTarget{}, errors.New("issue_id and target_agent_id are required")
	}
	if err := validateRadarDirectiveContent(payload.Content); err != nil {
		return nil, radarActivityTarget{}, err
	}
	if h.TaskService == nil || h.TxStarter == nil {
		return nil, radarActivityTarget{}, errors.New("rework transaction services unavailable")
	}
	targetAgent, err := h.resolveRadarDirectiveAgent(ctx, run, supervisor, payload.TargetAgentID)
	if err != nil {
		return nil, radarActivityTarget{}, err
	}
	issueID, err := parseUUIDString(strings.TrimSpace(payload.IssueID))
	if err != nil {
		return nil, radarActivityTarget{}, errors.New("invalid issue_id")
	}
	issue, err := h.Queries.GetIssueInWorkspace(ctx, db.GetIssueInWorkspaceParams{
		ID:          issueID,
		WorkspaceID: run.WorkspaceID,
	})
	if err != nil {
		return nil, radarActivityTarget{}, errors.New("issue does not belong to the run workspace")
	}
	activity := radarActivityTarget{Kind: "issue", ID: issue.ID, Trusted: true}
	if issue.Status == "cancelled" {
		return nil, activity, errors.New("cancelled issues cannot be reopened by request_rework")
	}
	if issue.AssigneeType.String != "agent" || !radarUUIDsMatch(issue.AssigneeID, targetAgent.ID) {
		return nil, activity, errors.New("target_agent_id must be the issue's current agent assignee")
	}
	projectID, err := h.resolveRadarIssueCreateProject(ctx, run, "")
	if err != nil {
		return nil, activity, err
	}
	if projectID.Valid && !radarUUIDsMatch(projectID, issue.ProjectID) {
		return nil, activity, errors.New("issue is outside the radar task project scope")
	}
	channelID, _, err := h.resolveRadarIssueCreateSource(ctx, run, "")
	if err != nil {
		return nil, activity, err
	}
	if channelID.Valid && !h.channelHasAgentMember(ctx, run.WorkspaceID, channelID, targetAgent.ID) {
		return nil, activity, errors.New("issue assignee is not a member of the radar channel")
	}

	criteriaChanged := payload.AcceptanceCriteria != nil
	var criteriaJSON []byte
	if criteriaChanged {
		criteria := cleanRadarAcceptanceCriteria(*payload.AcceptanceCriteria)
		if len(criteria) == 0 {
			return nil, activity, errors.New("acceptance_criteria must contain at least one non-empty criterion when provided")
		}
		criteriaJSON, err = json.Marshal(criteria)
		if err != nil {
			return nil, activity, fmt.Errorf("encode rework acceptance criteria: %w", err)
		}
	}

	tx, err := h.TxStarter.Begin(ctx)
	if err != nil {
		return nil, activity, fmt.Errorf("begin radar rework transaction: %w", err)
	}
	defer tx.Rollback(ctx)
	qtx := h.Queries.WithTx(tx)

	var previousStatus string
	if err := tx.QueryRow(ctx, `
		SELECT status
		FROM issue
		WHERE id = $1
		  AND workspace_id = $2
		  AND status <> 'cancelled'
		  AND assignee_type = 'agent'
		  AND assignee_id = $3
		FOR UPDATE
	`, issue.ID, run.WorkspaceID, targetAgent.ID).Scan(&previousStatus); err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, activity, errors.New("issue is no longer eligible for request_rework")
		}
		return nil, activity, fmt.Errorf("lock radar rework issue: %w", err)
	}
	if _, err := tx.Exec(ctx, `
		UPDATE issue
		SET status = 'todo',
		    acceptance_criteria = CASE WHEN $2::boolean THEN $3::jsonb ELSE acceptance_criteria END,
		    updated_at = now()
		WHERE id = $1
	`, issue.ID, criteriaChanged, criteriaJSON); err != nil {
		return nil, activity, fmt.Errorf("reopen radar rework issue: %w", err)
	}
	reopened, err := qtx.GetIssueInWorkspace(ctx, db.GetIssueInWorkspaceParams{
		ID:          issue.ID,
		WorkspaceID: run.WorkspaceID,
	})
	if err != nil {
		return nil, activity, fmt.Errorf("reload radar rework issue: %w", err)
	}
	directive := radarIssueDirective{
		TargetAgent: targetAgent,
		Issue:       reopened,
		Content:     formatRadarIssueDirectiveMention(targetAgent.Name, uuidToString(targetAgent.ID)) + " " + strings.TrimSpace(payload.Content),
	}
	execution, err := h.executePreparedRadarIssueCommentInTx(ctx, qtx, tx, run, supervisor, directive)
	if err != nil {
		return execution.Result, activity, err
	}
	if err := tx.Commit(ctx); err != nil {
		return nil, activity, fmt.Errorf("commit radar rework: %w", err)
	}
	commentAfterCommit := execution.AfterCommit
	h.publish(protocol.EventIssueUpdated, uuidToString(reopened.WorkspaceID), "agent", uuidToString(supervisor.ID), map[string]any{
		"issue":                       issueToResponse(reopened, h.getIssuePrefix(ctx, reopened.WorkspaceID)),
		"assignee_changed":            false,
		"status_changed":              previousStatus != reopened.Status,
		"acceptance_criteria_changed": criteriaChanged,
		"prev_status":                 previousStatus,
	})
	if previousStatus != reopened.Status {
		// The targeted issue comment below owns the one wake for this rework.
		// Keep the source-thread status projection factual so it cannot enqueue a
		// second, competing channel task for the same agent.
		h.emitIssueThreadBackflow(ctx, reopened, "agent", uuidToString(supervisor.ID), issueThreadStatusChangedEvent, previousStatus, issueThreadBackflowTarget{})
	}
	h.syncWendyWorkGraphAfterIssueUpdate(ctx, reopened)
	if commentAfterCommit != nil {
		commentAfterCommit()
	}
	return execution.Result, execution.Activity, nil
}

func (h *Handler) prepareRadarIssueDirective(ctx context.Context, run db.AgentRadarRun, supervisor db.Agent, action radar.RadarAction) (radarIssueDirective, error) {
	var payload radarIssueCommentPayload
	if err := json.Unmarshal(action.Payload, &payload); err != nil {
		return radarIssueDirective{}, err
	}
	if strings.TrimSpace(payload.IssueID) == "" || strings.TrimSpace(payload.TargetAgentID) == "" {
		return radarIssueDirective{}, errors.New("issue_id and target_agent_id are required")
	}
	if err := validateRadarDirectiveContent(payload.Content); err != nil {
		return radarIssueDirective{}, err
	}
	if h.TaskService == nil {
		return radarIssueDirective{}, errors.New("task service unavailable")
	}
	targetAgent, err := h.resolveRadarDirectiveAgent(ctx, run, supervisor, payload.TargetAgentID)
	if err != nil {
		return radarIssueDirective{}, err
	}
	issueID, err := parseUUIDString(strings.TrimSpace(payload.IssueID))
	if err != nil {
		return radarIssueDirective{}, errors.New("invalid issue_id")
	}
	issue, err := h.Queries.GetIssueInWorkspace(ctx, db.GetIssueInWorkspaceParams{
		ID:          issueID,
		WorkspaceID: run.WorkspaceID,
	})
	if err != nil {
		return radarIssueDirective{}, errors.New("issue does not belong to the run workspace")
	}
	if issue.Status == "done" || issue.Status == "cancelled" {
		return radarIssueDirective{}, errors.New("cannot direct work on a terminal issue")
	}
	content := formatRadarIssueDirectiveMention(targetAgent.Name, uuidToString(targetAgent.ID)) + " " + strings.TrimSpace(payload.Content)
	return radarIssueDirective{TargetAgent: targetAgent, Issue: issue, Content: content}, nil
}

func (h *Handler) executeRadarIssueCommentInTx(ctx context.Context, qtx *db.Queries, exec db.DBTX, run db.AgentRadarRun, supervisor db.Agent, action radar.RadarAction) (radarTransactionalExecution, error) {
	directive, err := h.prepareRadarIssueDirective(ctx, run, supervisor, action)
	if err != nil {
		return radarTransactionalExecution{}, err
	}
	return h.executePreparedRadarIssueCommentInTx(ctx, qtx, exec, run, supervisor, directive)
}

func (h *Handler) executePreparedRadarIssueCommentInTx(ctx context.Context, qtx *db.Queries, exec db.DBTX, run db.AgentRadarRun, supervisor db.Agent, directive radarIssueDirective) (radarTransactionalExecution, error) {
	execution := radarTransactionalExecution{Activity: radarActivityTarget{Kind: "issue", ID: directive.Issue.ID, Trusted: true}}
	if run.TriggerKind == "scheduled" {
		if err := h.validateScheduledRadarSupervisorWithDB(ctx, exec, run, supervisor, true); err != nil {
			return execution, err
		}
	}
	if err := h.lockRadarIssueDirectiveTarget(ctx, exec, run.WorkspaceID, directive.Issue.ID, directive.TargetAgent.ID); err != nil {
		return execution, err
	}
	issue, err := qtx.GetIssueInWorkspace(ctx, db.GetIssueInWorkspaceParams{
		ID:          directive.Issue.ID,
		WorkspaceID: run.WorkspaceID,
	})
	if err != nil {
		return execution, fmt.Errorf("reload locked radar issue: %w", err)
	}
	existingTask, pendingTaskFound, err := loadLockedPendingRadarTask(ctx, qtx, exec, issue.ID, directive.TargetAgent.ID)
	if err != nil {
		return execution, err
	}

	comment, err := qtx.CreateComment(ctx, db.CreateCommentParams{
		IssueID:     issue.ID,
		WorkspaceID: issue.WorkspaceID,
		AuthorType:  "agent",
		AuthorID:    supervisor.ID,
		Content:     directive.Content,
		Type:        "comment",
	})
	if err != nil {
		return execution, fmt.Errorf("create visible radar comment: %w", err)
	}
	var task db.AgentInboxEvent
	taskReused := false
	if pendingTaskFound && (existingTask.Status == "pending" || existingTask.Status == "failed") {
		summary := h.TaskService.BuildCommentTriggerSummaryForTask(ctx, qtx, comment.ID)
		tag, updateErr := exec.Exec(ctx, `
			UPDATE agent_inbox_event
			SET trigger_comment_id = $2,
			    trigger_summary = $3
			WHERE id = $1
			  AND status IN ('pending', 'failed')
		`, existingTask.ID, comment.ID, summary)
		if updateErr != nil {
			return execution, fmt.Errorf("retarget queued radar directive task: %w", updateErr)
		}
		if tag.RowsAffected() != 1 {
			return execution, errors.New("queued radar directive task changed while locked")
		}
		task, err = qtx.GetAgentTask(ctx, existingTask.ID)
		if err != nil {
			return execution, fmt.Errorf("reload retargeted radar directive task: %w", err)
		}
		taskReused = true
	} else {
		if pendingTaskFound && existingTask.Status == "draining" && !existingTask.StartedAt.Valid {
			tag, cancelErr := exec.Exec(ctx, `
				UPDATE agent_inbox_event
				SET status = 'suppressed',
				    terminal_outcome = 'cancelled',
				    terminal_at = COALESCE(terminal_at, now()),
				    acked_at = COALESCE(acked_at, now()),
				    completed_at = COALESCE(completed_at, now()),
				    error = COALESCE(NULLIF(error, ''), 'Superseded by a visible Wendy follow-up'),
				    failure_reason = 'radar_followup_interrupt'
				WHERE id = $1
				  AND status = 'draining'
			`, existingTask.ID)
			if cancelErr != nil {
				return execution, fmt.Errorf("cancel dispatched radar directive task: %w", cancelErr)
			}
			if tag.RowsAffected() != 1 {
				return execution, errors.New("dispatched radar directive task changed while locked")
			}
			cancelled, loadErr := qtx.GetAgentTask(ctx, existingTask.ID)
			if loadErr != nil {
				return execution, fmt.Errorf("reload cancelled radar directive task: %w", loadErr)
			}
			execution.CancelledTasks = append(execution.CancelledTasks, cancelled)
		}
		task, err = h.TaskService.CreateMentionTaskRow(ctx, qtx, issue, directive.TargetAgent.ID, comment.ID)
		if err != nil {
			return execution, fmt.Errorf("create radar directive task: %w", err)
		}
	}
	execution.Result = map[string]any{
		"comment_id":  uuidToString(comment.ID),
		"task_id":     uuidToString(task.ID),
		"task_reused": taskReused,
	}
	execution.AfterCommit = func() {
		// Both events describe committed artifacts. The visible comment is
		// published before the task notification can wake the target runtime.
		commentResponse := commentToResponse(comment, nil, nil)
		h.publish(protocol.EventCommentCreated, uuidToString(issue.WorkspaceID), "agent", uuidToString(supervisor.ID), map[string]any{
			"comment":             commentResponse,
			"issue_title":         issue.Title,
			"issue_assignee_type": textToPtr(issue.AssigneeType),
			"issue_assignee_id":   uuidToPtr(issue.AssigneeID),
			"issue_status":        issue.Status,
		})
		if len(execution.CancelledTasks) > 0 {
			h.TaskService.BroadcastCancelledTasks(ctx, execution.CancelledTasks)
		}
		if taskReused {
			h.TaskService.NotifyTaskEnqueued(ctx, task)
		} else if run.TriggerKind == "scheduled" {
			// Wendy's supervision adds visible follow-up work but must not abort a
			// task the target agent is already executing.
			h.TaskService.PublishMentionTaskQueuedPreservingInFlight(ctx, task, issue, comment.ID)
		} else {
			h.TaskService.PublishMentionTaskQueued(ctx, task, issue, comment.ID)
		}
	}
	return execution, nil
}

func loadLockedPendingRadarTask(ctx context.Context, qtx *db.Queries, exec db.DBTX, issueID, agentID pgtype.UUID) (db.AgentInboxEvent, bool, error) {
	var taskID pgtype.UUID
	err := exec.QueryRow(ctx, `
		SELECT id
		FROM agent_inbox_event
		WHERE issue_id = $1
		  AND agent_id = $2
		  AND status IN ('pending', 'draining', 'failed')
		ORDER BY created_at DESC, id DESC
		LIMIT 1
		FOR UPDATE
	`, issueID, agentID).Scan(&taskID)
	if errors.Is(err, pgx.ErrNoRows) {
		return db.AgentInboxEvent{}, false, nil
	}
	if err != nil {
		return db.AgentInboxEvent{}, false, fmt.Errorf("lock target pending task: %w", err)
	}
	task, err := qtx.GetAgentTask(ctx, taskID)
	if err != nil {
		return db.AgentInboxEvent{}, false, fmt.Errorf("load locked target pending task: %w", err)
	}
	return task, true, nil
}

func (h *Handler) lockRadarIssueDirectiveTarget(ctx context.Context, exec db.DBTX, workspaceID, issueID, targetAgentID pgtype.UUID) error {
	var lockedIssueID pgtype.UUID
	if err := exec.QueryRow(ctx, `
		SELECT id
		FROM issue
		WHERE id = $1
		  AND workspace_id = $2
		  AND status NOT IN ('done', 'cancelled')
		FOR UPDATE
	`, issueID, workspaceID).Scan(&lockedIssueID); err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return errors.New("issue is no longer eligible for a directed task")
		}
		return fmt.Errorf("lock radar directive issue: %w", err)
	}

	var lockedAgentID pgtype.UUID
	if err := exec.QueryRow(ctx, `
		SELECT target.id
		FROM agent target
		JOIN agent_runtime runtime
		  ON runtime.id = target.runtime_id
		 AND runtime.workspace_id = target.workspace_id
		WHERE target.id = $1
		  AND target.workspace_id = $2
		  AND target.archived_at IS NULL
		FOR SHARE OF target, runtime
	`, targetAgentID, workspaceID).Scan(&lockedAgentID); err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return errors.New("target agent is no longer eligible for a directed task")
		}
		return fmt.Errorf("lock radar issue directive target: %w", err)
	}
	return nil
}

func (h *Handler) executeRadarCreateIssue(ctx context.Context, run db.AgentRadarRun, agent db.Agent, action radar.RadarAction) (map[string]any, error) {
	var payload radarIssueCreatePayload
	if err := json.Unmarshal(action.Payload, &payload); err != nil {
		return nil, err
	}
	if strings.TrimSpace(payload.Title) == "" {
		return nil, errors.New("title is required")
	}
	if strings.TrimSpace(payload.Description) == "" {
		return nil, errors.New("description is required")
	}
	criteria := cleanRadarAcceptanceCriteria(payload.AcceptanceCriteria)
	if len(criteria) == 0 {
		return nil, errors.New("acceptance_criteria must contain at least one non-empty criterion")
	}
	var assigneeType pgtype.Text
	var assigneeID pgtype.UUID
	if strings.TrimSpace(payload.AssigneeID) != "" {
		parsed, err := parseUUIDString(payload.AssigneeID)
		if err != nil {
			return nil, err
		}
		assigneeID = parsed
		assigneeType = pgtype.Text{String: "agent", Valid: true}
		if strings.TrimSpace(payload.AssigneeType) != "" {
			assigneeType.String = strings.TrimSpace(payload.AssigneeType)
		}
	} else if strings.TrimSpace(payload.AssigneeType) != "" {
		return nil, errors.New("assignee_id is required when assignee_type is set")
	}
	if strings.HasPrefix(run.CooldownKey, "wendy_ambient:") && assigneeType.Valid && assigneeType.String != "agent" {
		return nil, errors.New("ambient issue creation may only assign a qualified channel agent")
	}
	projectID, err := h.resolveRadarIssueCreateProject(ctx, run, payload.ProjectID)
	if err != nil {
		return nil, err
	}
	if err := h.validateRadarIssueCreateWorkspaceTargets(ctx, run, agent, assigneeType, assigneeID, projectID); err != nil {
		return nil, err
	}
	sourceChannelID, sourceMessageID, err := h.resolveRadarIssueCreateSource(ctx, run, payload.SourceMessageID)
	if err != nil {
		return nil, err
	}
	attachmentIDs, err := h.resolveRadarIssueCreateAttachments(ctx, run, sourceChannelID, payload.AttachmentIDs)
	if err != nil {
		return nil, err
	}
	if err := h.validateRadarIssueCreateAssigneeForChannel(ctx, run, agent, sourceChannelID, assigneeType, assigneeID); err != nil {
		return nil, err
	}
	result, err := h.IssueService.Create(ctx, service.IssueCreateParams{
		WorkspaceID:        run.WorkspaceID,
		Title:              strings.TrimSpace(payload.Title),
		Description:        pgtype.Text{String: strings.TrimSpace(payload.Description), Valid: true},
		Status:             "todo",
		Priority:           "none",
		AssigneeType:       assigneeType,
		AssigneeID:         assigneeID,
		CreatorType:        "agent",
		CreatorID:          agent.ID,
		ProjectID:          projectID,
		SourceChannelID:    sourceChannelID,
		SourceMessageID:    sourceMessageID,
		AcceptanceCriteria: criteria,
		AttachmentIDs:      attachmentIDs,
		AllowDuplicate:     false,
	}, service.IssueCreateOpts{
		ActorID:          uuidToString(agent.ID),
		AnalyticsAgentID: uuidToString(assigneeID),
		Platform:         "radar",
		BroadcastPayload: func(issue db.Issue, attachments []db.Attachment) map[string]any {
			response := issueToResponse(issue, h.getIssuePrefix(ctx, issue.WorkspaceID))
			if len(attachments) > 0 {
				response.Attachments = make([]AttachmentResponse, len(attachments))
				for i, attachment := range attachments {
					response.Attachments[i] = h.attachmentToResponse(attachment)
				}
			}
			return map[string]any{"issue": response}
		},
	})
	if err != nil {
		return nil, err
	}
	h.syncWendyWorkGraphAfterIssueCreate(ctx, result.Issue)
	h.emitIssueThreadBackflow(ctx, result.Issue, "agent", uuidToString(agent.ID), issueThreadCreatedEvent, "", issueThreadBackflowTarget{})
	return map[string]any{"issue_id": uuidToString(result.Issue.ID)}, nil
}

func cleanRadarAcceptanceCriteria(criteria []string) []string {
	out := make([]string, 0, len(criteria))
	for _, criterion := range criteria {
		if trimmed := strings.TrimSpace(criterion); trimmed != "" {
			out = append(out, trimmed)
		}
	}
	return out
}

func (h *Handler) resolveRadarIssueCreateSource(ctx context.Context, run db.AgentRadarRun, rawMessageID string) (pgtype.UUID, pgtype.UUID, error) {
	taskContext, err := h.loadRadarTaskContext(ctx, run)
	if err != nil {
		return pgtype.UUID{}, pgtype.UUID{}, err
	}
	if strings.TrimSpace(taskContext.ChannelID) == "" {
		if strings.TrimSpace(rawMessageID) != "" {
			return pgtype.UUID{}, pgtype.UUID{}, errors.New("source_message_id requires a channel-scoped radar task")
		}
		return pgtype.UUID{}, pgtype.UUID{}, nil
	}
	channelID, err := parseUUIDString(strings.TrimSpace(taskContext.ChannelID))
	if err != nil {
		return pgtype.UUID{}, pgtype.UUID{}, errors.New("radar task contains an invalid channel_id")
	}
	channel, found := h.getChannel(ctx, uuidToString(run.WorkspaceID), channelID)
	if !found || channel.Kind != "group" || channel.ArchivedAt != nil {
		return pgtype.UUID{}, pgtype.UUID{}, errors.New("radar task channel is no longer available")
	}
	if strings.TrimSpace(taskContext.ProjectID) != "" {
		if channel.ProjectID == nil || strings.TrimSpace(*channel.ProjectID) != strings.TrimSpace(taskContext.ProjectID) {
			return pgtype.UUID{}, pgtype.UUID{}, errors.New("radar task channel no longer matches its project scope")
		}
	}
	if strings.TrimSpace(rawMessageID) == "" {
		return channelID, pgtype.UUID{}, nil
	}
	messageID, err := parseUUIDString(strings.TrimSpace(rawMessageID))
	if err != nil {
		return pgtype.UUID{}, pgtype.UUID{}, errors.New("invalid source_message_id")
	}
	var threadRoot pgtype.UUID
	if err := h.DB.QueryRow(ctx, `
		SELECT thread_root_message_id
		FROM channel_message
		WHERE id = $1
		  AND workspace_id = $2
		  AND channel_id = $3
		  AND deleted_at IS NULL
	`, messageID, run.WorkspaceID, channelID).Scan(&threadRoot); err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return pgtype.UUID{}, pgtype.UUID{}, errors.New("source message does not belong to the radar channel")
		}
		return pgtype.UUID{}, pgtype.UUID{}, fmt.Errorf("load radar source message: %w", err)
	}
	if threadRoot.Valid {
		messageID = threadRoot
	}
	return channelID, messageID, nil
}

func (h *Handler) resolveRadarIssueCreateAttachments(ctx context.Context, run db.AgentRadarRun, channelID pgtype.UUID, rawIDs []string) ([]pgtype.UUID, error) {
	ids := make([]pgtype.UUID, 0, len(rawIDs))
	seen := make(map[string]struct{}, len(rawIDs))
	for _, rawID := range rawIDs {
		id, err := parseUUIDString(strings.TrimSpace(rawID))
		if err != nil {
			return nil, errors.New("invalid attachment_id")
		}
		key := uuidToString(id)
		if _, exists := seen[key]; exists {
			continue
		}
		seen[key] = struct{}{}
		attachment, err := h.Queries.GetAttachment(ctx, db.GetAttachmentParams{ID: id, WorkspaceID: run.WorkspaceID})
		if err != nil {
			return nil, errors.New("attachment does not belong to the radar workspace")
		}
		if !channelID.Valid {
			return nil, errors.New("attachment is not visible in the radar channel")
		}
		var messageVisible bool
		if err := h.DB.QueryRow(ctx, `
			SELECT EXISTS (
				SELECT 1
				FROM channel_message_attachment reference
				JOIN channel_message message ON message.id = reference.channel_message_id
				WHERE reference.attachment_id = $1
				  AND reference.workspace_id = $2
				  AND message.channel_id = $3
				  AND message.deleted_at IS NULL
			)
		`, attachment.ID, run.WorkspaceID, channelID).Scan(&messageVisible); err != nil {
			return nil, fmt.Errorf("verify radar attachment message: %w", err)
		}
		if !messageVisible {
			return nil, errors.New("attachment message is deleted or outside the radar channel")
		}
		ids = append(ids, id)
	}
	return ids, nil
}

func (h *Handler) loadRadarTaskContext(ctx context.Context, run db.AgentRadarRun) (service.AgentRadarContext, error) {
	if !run.TaskID.Valid {
		return service.AgentRadarContext{}, nil
	}
	task, err := h.Queries.GetAgentTask(ctx, run.TaskID)
	if err != nil {
		return service.AgentRadarContext{}, fmt.Errorf("load radar task context: %w", err)
	}
	if !radarUUIDsMatch(task.AgentID, run.AgentID) {
		return service.AgentRadarContext{}, errors.New("radar task does not belong to the run agent")
	}
	var taskContext service.AgentRadarContext
	if err := json.Unmarshal(task.Context, &taskContext); err != nil {
		return service.AgentRadarContext{}, fmt.Errorf("decode radar task context: %w", err)
	}
	if taskContext.Type != service.AgentRadarContextType || taskContext.RadarRunID != uuidToString(run.ID) {
		return service.AgentRadarContext{}, errors.New("radar task context does not match the run")
	}
	return taskContext, nil
}

// resolveRadarIssueCreateProject makes the persisted Radar task scope
// authoritative. The model may omit a project or echo one from its prompt,
// but it cannot redirect an action to another same-workspace project.
// Historical/manual runs without a task-scoped project retain their payload
// behavior for compatibility.
func (h *Handler) resolveRadarIssueCreateProject(ctx context.Context, run db.AgentRadarRun, rawPayloadProjectID string) (pgtype.UUID, error) {
	var payloadProjectID pgtype.UUID
	if strings.TrimSpace(rawPayloadProjectID) != "" {
		parsed, err := parseUUIDString(strings.TrimSpace(rawPayloadProjectID))
		if err != nil {
			return pgtype.UUID{}, errors.New("invalid project_id")
		}
		payloadProjectID = parsed
	}
	if !run.TaskID.Valid {
		return payloadProjectID, nil
	}

	taskContext, err := h.loadRadarTaskContext(ctx, run)
	if err != nil {
		return pgtype.UUID{}, err
	}
	if strings.TrimSpace(taskContext.ProjectID) == "" {
		return payloadProjectID, nil
	}

	canonicalProjectID, err := parseUUIDString(strings.TrimSpace(taskContext.ProjectID))
	if err != nil {
		return pgtype.UUID{}, errors.New("radar task contains an invalid project_id")
	}
	if payloadProjectID.Valid && !radarUUIDsMatch(payloadProjectID, canonicalProjectID) {
		return pgtype.UUID{}, errors.New("project_id conflicts with the radar task project scope")
	}
	return canonicalProjectID, nil
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
