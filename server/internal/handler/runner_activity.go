package handler

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"strings"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/multica-ai/multica/server/internal/daemonws"
	"github.com/multica-ai/multica/server/internal/util"
	"github.com/multica-ai/multica/server/pkg/protocol"
)

const runnerActivityTimelineLimit = 100

// RunnerActivityResponse exposes lifecycle facts and bounded display text,
// never a Runner fact envelope or provider-specific detail.
type RunnerActivityResponse struct {
	Summary  *protocol.AgentActivitySummary      `json:"summary"`
	Timeline []RunnerActivityTimelineResponseRow `json:"timeline"`
	Timing   *RunnerActivityTimingResponse       `json:"timing,omitempty"`
}

type RunnerActivityTimingResponse struct {
	ColdStartAtMS      int64 `json:"cold_start_at_ms,omitempty"`
	AcceptedAtMS       int64 `json:"accepted_at_ms,omitempty"`
	FirstACPUpdateAtMS int64 `json:"first_acp_update_at_ms,omitempty"`
	DaemonSentAtMS     int64 `json:"daemon_sent_at_ms,omitempty"`
}

type RunnerActivityTimelineResponseRow struct {
	ID           string `json:"id"`
	OccurredAt   string `json:"occurred_at"`
	ActivityKind string `json:"activity_kind"`
	DetailKind   string `json:"detail_kind"`
	Title        string `json:"title"`
	Subtext      string `json:"subtext,omitempty"`
	BodyKind     string `json:"body_kind"`
	Body         string `json:"body,omitempty"`
}

type RunnerActivityRealtimePayload struct {
	AgentID  string                 `json:"agent_id"`
	Activity RunnerActivityResponse `json:"activity"`
}

func runnerActivityTimingResponse(t *protocol.AgentActivityTiming) *RunnerActivityTimingResponse {
	if t == nil {
		return nil
	}
	return &RunnerActivityTimingResponse{ColdStartAtMS: t.ColdStartAtMS, AcceptedAtMS: t.AcceptedAtMS, FirstACPUpdateAtMS: t.FirstACPUpdateAtMS, DaemonSentAtMS: t.DaemonSentAtMS}
}

// HandleWorkspaceDaemonFrame accepts frames only from the current ready
// WorkspaceDaemon. Activity is best-effort observation; unlike lifecycle
// frames, it has no server-owned ordering or replay fence.
func (h *Handler) HandleWorkspaceDaemonFrame(ctx context.Context, identity daemonws.ClientIdentity, daemonInstanceID, eventType string, raw json.RawMessage) error {

	if h == nil || h.DB == nil {
		return errors.New("handler database is unavailable")
	}
	switch eventType {
	case protocol.EventWorkspaceDaemonReady:
		var ready protocol.WorkspaceReadyPayload
		if err := json.Unmarshal(raw, &ready); err != nil {
			return fmt.Errorf("decode Runner ready: %w", err)
		}
		if ready.WorkspaceID != identity.WorkspaceID || ready.DaemonInstanceID != daemonInstanceID {
			return errors.New("Runner ready identity does not match current connection")
		}
		if err := h.persistComputerReadyMetadata(ctx, identity.DaemonID, ready); err != nil {
			return err
		}
		if err := h.recordWorkspaceDaemonReady(ctx, identity, daemonInstanceID, ready.RunningAgents); err != nil {
			return err
		}
		h.publish(protocol.EventComputerStatus, identity.WorkspaceID, "system", "", map[string]any{
			"computer_id": identity.DaemonID,
			"status":      "connected",
			"changed_at":  time.Now().UTC().Format(time.RFC3339Nano),
		})
		// Raft establishes APM ownership before it offers durable deliveries.
		// Channel messages may ACK into the Agent's starting Inbox before the
		// Provider is Running. Standalone chat: (FAB) does not — see §1.5 —
		// so redeliver unacked chat: lines once launches are converging.
		if err := h.reconcileWorkspaceDaemonLaunches(ctx, identity); err != nil {
			return err
		}
		if err := h.redeliverUnacknowledgedComputerAgentMessages(ctx, identity); err != nil {
			return err
		}
		if err := h.redeliverUnacknowledgedStandaloneChat(ctx, identity); err != nil {
			return err
		}
		return nil
	case protocol.EventAgentStatus:
		var status protocol.AgentStatusPayload
		if err := json.Unmarshal(raw, &status); err != nil {
			return fmt.Errorf("decode Runner status: %w", err)
		}
		if err := h.recordRunnerLaunch(ctx, identity, daemonInstanceID, status); err != nil {
			return err
		}
		handled, err := h.advanceAgentRestartFromStatus(ctx, identity, status)
		if err != nil {
			return err
		}
		if handled {
			return nil
		}
		if status.Status == protocol.AgentStatusInactive && h.DaemonHub != nil {
			return h.reconcileDesiredAgentRuntime(ctx, identity.WorkspaceID, status.AgentID)
		}
		return nil
	case protocol.EventAgentStartAck:
		var acknowledgement protocol.AgentStartAckPayload
		if err := json.Unmarshal(raw, &acknowledgement); err != nil {
			return fmt.Errorf("decode Runner start acknowledgement: %w", err)
		}
		if err := h.recordRunnerStartAcknowledgement(ctx, identity, daemonInstanceID, acknowledgement); err != nil {
			return err
		}
		// A live launch can now accept. Offer unacked standalone lines that
		// were rejected_no_process while the Agent was down — same ledger
		// as Runner ready, not a fake ACK.
		return h.redeliverUnacknowledgedStandaloneChat(ctx, identity)
	case protocol.EventAgentResetWorkspaceResult:
		var result protocol.AgentWorkspaceResetResultPayload
		if err := json.Unmarshal(raw, &result); err != nil {
			return fmt.Errorf("decode Agent workspace reset result: %w", err)
		}
		return h.recordAgentWorkspaceResetResult(ctx, identity, result)
	case protocol.EventAgentSession:
		var session protocol.AgentSessionPayload
		if err := json.Unmarshal(raw, &session); err != nil {
			return fmt.Errorf("decode Runner session: %w", err)
		}
		return h.recordRunnerSession(ctx, identity, daemonInstanceID, session)
	case protocol.EventAgentActivity:
		var activity protocol.AgentActivityPayload
		if err := json.Unmarshal(raw, &activity); err != nil {
			return fmt.Errorf("decode Runner Activity: %w", err)
		}
		return h.recordRunnerActivity(ctx, identity, daemonInstanceID, activity)
	case protocol.EventMixedRunActivityTransition:
		var transition protocol.MixedRunActivityTransitionPayload
		if err := json.Unmarshal(raw, &transition); err != nil {
			return fmt.Errorf("decode mixed-run activity transition: %w", err)
		}
		return h.recordMixedRunActivityTransition(ctx, identity, transition)
	case protocol.EventComputerUpgradeProgress:
		var progress protocol.ComputerUpgradeProgressPayload
		if err := json.Unmarshal(raw, &progress); err != nil {
			return fmt.Errorf("decode Computer upgrade progress: %w", err)
		}
		if err := progress.Validate(); err != nil {
			return err
		}
		h.publishComputerUpgradeSocketEvent(identity, protocol.EventComputerUpgradeProgress, map[string]any{
			"computer_id": identity.DaemonID,
			"requestId":   progress.RequestID,
			"phase":       progress.Phase,
			"message":     progress.Message,
			"percent":     progress.Percent,
		})
		return nil
	case protocol.EventComputerUpgradeDone:
		var done protocol.ComputerUpgradeDonePayload
		if err := json.Unmarshal(raw, &done); err != nil {
			return fmt.Errorf("decode Computer upgrade done: %w", err)
		}
		if err := done.Validate(); err != nil {
			return err
		}
		h.publishComputerUpgradeSocketEvent(identity, protocol.EventComputerUpgradeDone, map[string]any{
			"computer_id": identity.DaemonID,
			"requestId":   done.RequestID,
			"ok":          done.OK,
			"newVersion":  done.NewVersion,
			"error":       done.Error,
			"rolledBack":  done.RolledBack,
		})
		return nil
	default:
		return nil
	}
}

func (h *Handler) reconcileDesiredAgentRuntime(ctx context.Context, workspaceID, agentID string) error {
	var runtimeID pgtype.UUID
	err := h.DB.QueryRow(ctx, `
		SELECT runtime_id
		FROM agent
		WHERE workspace_id::text = $1 AND id::text = $2 AND archived_at IS NULL`, workspaceID, agentID).Scan(&runtimeID)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil
	}
	if err != nil {
		return fmt.Errorf("load desired Agent Runtime after inactive: %w", err)
	}
	h.reconcileConnectedRuntime(ctx, workspaceID, runtimeID)
	return nil
}

func (h *Handler) persistComputerReadyMetadata(ctx context.Context, daemonID string, ready protocol.WorkspaceReadyPayload) error {
	_, err := h.DB.Exec(ctx, `
UPDATE computers
   SET device_name = COALESCE(NULLIF($2, ''), device_name),
       os = COALESCE(NULLIF($3, ''), os),
       cli_version = COALESCE(NULLIF($4, ''), cli_version),
       machine_id = COALESCE(NULLIF($5, ''), machine_id)
 WHERE id = $1`,
		strings.TrimSpace(daemonID),
		strings.TrimSpace(ready.DeviceName),
		strings.TrimSpace(ready.OS),
		strings.TrimSpace(ready.CLIVersion),
		strings.TrimSpace(ready.MachineID),
	)
	if err != nil {
		return fmt.Errorf("persist Computer ready metadata: %w", err)
	}
	return nil
}

func (h *Handler) recordMixedRunActivityTransition(ctx context.Context, identity daemonws.ClientIdentity, transition protocol.MixedRunActivityTransitionPayload) error {
	if err := transition.Validate(); err != nil {
		return err
	}
	workspaceID, err := util.ParseUUID(identity.WorkspaceID)
	if err != nil {
		return errors.New("invalid Runner workspace identity")
	}
	runID, err := util.ParseUUID(transition.RunID)
	if err != nil {
		return errors.New("invalid mixed-run activity run_id")
	}
	runAgentID, err := util.ParseUUID(transition.RunAgentID)
	if err != nil {
		return errors.New("invalid mixed-run activity run_agent_id")
	}
	agentID, err := util.ParseUUID(transition.AgentID)
	if err != nil {
		return errors.New("invalid mixed-run activity agent_id")
	}
	runtimeID, err := util.ParseUUID(transition.RuntimeID)
	if err != nil {
		return errors.New("invalid mixed-run activity runtime_id")
	}
	var counterColumn string
	switch transition.Dimension {
	case protocol.MixedRunActivityActiveTurn:
		counterColumn = "active_turn_count"
	case protocol.MixedRunActivityQueuedMessage:
		counterColumn = "queued_message_count"
	case protocol.MixedRunActivityInflightTool:
		counterColumn = "inflight_tool_count"
	case protocol.MixedRunActivityUnfinishedCaptureBatch:
		counterColumn = "unfinished_capture_batch_count"
	default:
		return errors.New("invalid mixed-run activity dimension")
	}
	if h.TxStarter == nil {
		return errors.New("mixed-run activity transaction unavailable")
	}
	tx, err := h.TxStarter.Begin(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback(ctx)
	var authorized bool
	if err := tx.QueryRow(ctx, `
		SELECT EXISTS (
		  SELECT 1
		  FROM env_dispatch_run_agent run_agent
		  JOIN env_dispatch_run run ON run.run_id = run_agent.run_id
		  JOIN agent_runtime runtime ON runtime.id = run_agent.runtime_id
		  WHERE run.run_id = $1 AND run.workspace_id = $2
		    AND run_agent.run_agent_id = $3
		    AND run_agent.execution_agent_id = $4
		    AND run_agent.runtime_id = $5
		    AND runtime.daemon_id = $6
		)`, runID, workspaceID, runAgentID, agentID, runtimeID, identity.DaemonID).Scan(&authorized); err != nil {
		return fmt.Errorf("authorize mixed-run activity transition: %w", err)
	}
	if !authorized {
		return errors.New("mixed-run activity transition scope mismatch")
	}
	// A terminal run's counters were settled authoritatively by the freeze.
	// Late activity transitions must be acknowledged and dropped: rejecting
	// them would leave a poison entry in the daemon outbox that replays on
	// every reconnect, while applying them would corrupt the frozen run's
	// quiescence bookkeeping.
	var runStatus string
	if err := tx.QueryRow(ctx, `
		SELECT status FROM env_dispatch_run WHERE run_id = $1`, runID).Scan(&runStatus); err != nil {
		return fmt.Errorf("load mixed-run status for activity transition: %w", err)
	}
	if runStatus == "completed" || runStatus == "failed_timeout" {
		return tx.Commit(ctx)
	}
	var inserted bool
	err = tx.QueryRow(ctx, `
		INSERT INTO env_dispatch_activity_transition AS transition (
		  run_id, transition_id, run_agent_id, agent_id, runtime_id, dimension, delta
		) VALUES ($1, $2, $3, $4, $5, $6, $7)
		ON CONFLICT (run_id, transition_id) DO UPDATE
		SET transition_id = EXCLUDED.transition_id
		WHERE transition.run_agent_id = EXCLUDED.run_agent_id
		  AND transition.agent_id = EXCLUDED.agent_id
		  AND transition.runtime_id = EXCLUDED.runtime_id
		  AND transition.dimension = EXCLUDED.dimension
		  AND transition.delta = EXCLUDED.delta
		RETURNING (xmax = 0)`, runID, transition.TransitionID, runAgentID, agentID, runtimeID, transition.Dimension, transition.Delta).Scan(&inserted)
	if errors.Is(err, pgx.ErrNoRows) {
		return errors.New("mixed-run activity transition id conflicts with a different payload")
	}
	if err != nil {
		return fmt.Errorf("persist mixed-run activity transition: %w", err)
	}
	if !inserted {
		return tx.Commit(ctx)
	}
	query := `UPDATE env_dispatch_run AS run
		SET ` + counterColumn + ` = ` + counterColumn + ` + $2,
		    quiet_candidate_since = CASE
		      WHEN $2 > 0 THEN NULL
		      WHEN run.active_turn_count + CASE WHEN '` + counterColumn + `' = 'active_turn_count' THEN $2 ELSE 0 END = 0
		       AND run.pending_delivery_count = 0
		       AND run.queued_message_count + CASE WHEN '` + counterColumn + `' = 'queued_message_count' THEN $2 ELSE 0 END = 0
		       AND run.inflight_tool_count + CASE WHEN '` + counterColumn + `' = 'inflight_tool_count' THEN $2 ELSE 0 END = 0
		       AND run.unfinished_capture_batch_count + CASE WHEN '` + counterColumn + `' = 'unfinished_capture_batch_count' THEN $2 ELSE 0 END = 0
		      THEN now()
		      ELSE NULL
		    END,
		    status = CASE
		      WHEN $2 > 0 AND run.status = 'quiet_candidate' THEN 'running'
		      WHEN $2 < 0
		       AND run.active_turn_count + CASE WHEN '` + counterColumn + `' = 'active_turn_count' THEN $2 ELSE 0 END = 0
		       AND run.pending_delivery_count = 0
		       AND run.queued_message_count + CASE WHEN '` + counterColumn + `' = 'queued_message_count' THEN $2 ELSE 0 END = 0
		       AND run.inflight_tool_count + CASE WHEN '` + counterColumn + `' = 'inflight_tool_count' THEN $2 ELSE 0 END = 0
		       AND run.unfinished_capture_batch_count + CASE WHEN '` + counterColumn + `' = 'unfinished_capture_batch_count' THEN $2 ELSE 0 END = 0
		      THEN 'quiet_candidate'
		      ELSE run.status
		    END,
		    updated_at = now()
		WHERE run_id = $1
		  AND status IN ('preflight', 'running', 'quiet_candidate')
		  AND ` + counterColumn + ` + $2 >= 0`
	tag, err := tx.Exec(ctx, query, runID, transition.Delta)
	if err != nil {
		return fmt.Errorf("apply mixed-run activity transition: %w", err)
	}
	if tag.RowsAffected() != 1 {
		return errors.New("mixed-run activity transition would make counter negative or mutate an inactive run")
	}
	if err := tx.Commit(ctx); err != nil {
		return fmt.Errorf("commit mixed-run activity transition: %w", err)
	}
	return nil
}

// recordWorkspaceDaemonReady fences only live observations from an older
// Computer process. Persisted Activity is best-effort history, not lifecycle.
func (h *Handler) recordWorkspaceDaemonReady(ctx context.Context, identity daemonws.ClientIdentity, daemonInstanceID string, runningAgentIDs []string) error {
	return h.runnerPresenceLocked(func() error {
		if _, err := util.ParseUUID(identity.WorkspaceID); err != nil {
			return errors.New("invalid Runner workspace identity")
		}
		h.observations().forgetOtherInstances(identity.WorkspaceID, identity.DaemonID, daemonInstanceID)
		return nil
	})
}

func (h *Handler) recordRunnerLaunch(ctx context.Context, identity daemonws.ClientIdentity, daemonInstanceID string, status protocol.AgentStatusPayload) error {
	if err := status.Validate(); err != nil {
		return err
	}
	return h.runnerPresenceLocked(func() error {
		source := h.currentRunnerPresenceSource()
		if source == nil || !source.IsCurrentWorkspaceDaemon(identity.DaemonID, identity.WorkspaceID, daemonInstanceID) {
			return errors.New("stale WorkspaceDaemon status")
		}
		_, _, runtimeID, err := h.runnerActivityAgentScope(ctx, identity.WorkspaceID, status.AgentID)
		if err != nil {
			return err
		}
		beforeLaunch, err := h.loadRunnerLaunchPresence(ctx, identity.WorkspaceID, status.AgentID)
		if err != nil {
			return err
		}
		before := h.projectRunnerLaunchPresence(identity.WorkspaceID, beforeLaunch)
		if !h.observations().acceptStatus(identity.WorkspaceID, identity.DaemonID, daemonInstanceID, status.AgentID, util.UUIDToString(runtimeID), status.Status) {
			return errors.New("stale WorkspaceDaemon process status")
		}
		afterLaunch := &runnerLaunchPresence{daemonID: identity.DaemonID, daemonInstanceID: daemonInstanceID, status: status.Status}
		after := h.projectRunnerLaunchPresence(identity.WorkspaceID, afterLaunch)
		h.publishAgentPresenceChange(identity.WorkspaceID, status.AgentID, before, after)
		return nil
	})
}

func (h *Handler) recordRunnerStartAcknowledgement(ctx context.Context, identity daemonws.ClientIdentity, daemonInstanceID string, acknowledgement protocol.AgentStartAckPayload) error {
	if err := acknowledgement.Validate(); err != nil {
		return err
	}
	return h.runnerPresenceLocked(func() error {
		source := h.currentRunnerPresenceSource()
		if source == nil || !source.IsCurrentWorkspaceDaemon(identity.DaemonID, identity.WorkspaceID, daemonInstanceID) {
			return errors.New("stale WorkspaceDaemon start acknowledgement")
		}
		_, _, runtimeID, err := h.runnerActivityAgentScope(ctx, identity.WorkspaceID, acknowledgement.AgentID)
		if err != nil {
			return err
		}
		beforeLaunch, err := h.loadRunnerLaunchPresence(ctx, identity.WorkspaceID, acknowledgement.AgentID)
		if err != nil {
			return err
		}
		before := h.projectRunnerLaunchPresence(identity.WorkspaceID, beforeLaunch)
		status, ok := h.observations().acceptStartAck(identity.WorkspaceID, identity.DaemonID, daemonInstanceID, acknowledgement.AgentID, util.UUIDToString(runtimeID))
		if !ok {
			return errors.New("stale WorkspaceDaemon start acknowledgement")
		}
		after := h.projectRunnerLaunchPresence(identity.WorkspaceID, &runnerLaunchPresence{
			daemonID:         identity.DaemonID,
			daemonInstanceID: daemonInstanceID,
			status:           status,
		})
		h.publishAgentPresenceChange(identity.WorkspaceID, acknowledgement.AgentID, before, after)
		return nil
	})
}

// recoverRunnerObservation rebuilds a missing in-memory observation from the
// Agent's current runtime assignment. The observation store starts empty after
// a server restart, and deployed daemons only replay agent:status through the
// start-ACK round trip — which older daemon builds never complete for an
// already-running Agent. A session or activity frame from the current ready
// connection whose launch matches the desired projection is itself a
// residency report from the live Computer process, so re-admit it with the
// same projection authority the start-ACK path uses instead of rejecting it
// (and re-driving agent:start forever).
func (h *Handler) recoverRunnerObservation(ctx context.Context, identity daemonws.ClientIdentity, daemonInstanceID, agentID string) (runnerObservedAgent, bool) {
	if daemonInstanceID == "" || agentID == "" {
		return runnerObservedAgent{}, false
	}
	// Recovery is only for a true miss. An existing observation — even a
	// conflicting one — keeps full fencing authority.
	if _, ok := h.observations().get(identity.WorkspaceID, agentID); ok {
		return runnerObservedAgent{}, false
	}
	source := h.currentRunnerPresenceSource()
	if source == nil || !source.IsCurrentWorkspaceDaemon(identity.DaemonID, identity.WorkspaceID, daemonInstanceID) {
		return runnerObservedAgent{}, false
	}
	var runtimeID string
	if err := h.DB.QueryRow(ctx, `
		SELECT runtime.id::text
		FROM agent
		JOIN agent_runtime runtime ON runtime.id = agent.runtime_id
		WHERE agent.workspace_id::text = $1 AND agent.id::text = $2
		  AND runtime.daemon_id = $3`,
		identity.WorkspaceID, agentID, identity.DaemonID).Scan(&runtimeID); err != nil {
		return runnerObservedAgent{}, false
	}
	if !h.observations().acceptStatus(identity.WorkspaceID, identity.DaemonID, daemonInstanceID, agentID, runtimeID, protocol.AgentStatusActive) {
		return runnerObservedAgent{}, false
	}
	obs, ok := h.observations().get(identity.WorkspaceID, agentID)
	if !ok {
		return runnerObservedAgent{}, false
	}
	slog.Info("WorkspaceDaemon observation recovered from current runtime assignment",
		"workspace_id", identity.WorkspaceID, "daemon_id", identity.DaemonID,
		"daemon_instance_id", daemonInstanceID, "agent_id", agentID)
	// No observation existed, so the projected presence before recovery was
	// necessarily offline.
	h.publishAgentPresenceChange(identity.WorkspaceID, agentID, AgentPresenceOffline,
		h.projectRunnerLaunchPresence(identity.WorkspaceID, &runnerLaunchPresence{
			daemonID: identity.DaemonID, daemonInstanceID: daemonInstanceID, status: protocol.AgentStatusActive,
		}))
	return obs, true
}

func (h *Handler) recordRunnerSession(ctx context.Context, identity daemonws.ClientIdentity, daemonInstanceID string, session protocol.AgentSessionPayload) error {
	if err := session.Validate(); err != nil {
		return err
	}
	return h.runnerPresenceLocked(func() error {
		source := h.currentRunnerPresenceSource()
		if source == nil || !source.IsCurrentWorkspaceDaemon(identity.DaemonID, identity.WorkspaceID, daemonInstanceID) {
			return errors.New("stale WorkspaceDaemon session")
		}
		if _, _, _, err := h.runnerActivityAgentScope(ctx, identity.WorkspaceID, session.AgentID); err != nil {
			return err
		}
		if !h.observations().acceptSession(identity.WorkspaceID, identity.DaemonID, daemonInstanceID, session.AgentID, session.ProviderSessionID) {
			if _, recovered := h.recoverRunnerObservation(ctx, identity, daemonInstanceID, session.AgentID); !recovered ||
				!h.observations().acceptSession(identity.WorkspaceID, identity.DaemonID, daemonInstanceID, session.AgentID, session.ProviderSessionID) {
				return errors.New("stale or unknown WorkspaceDaemon session")
			}
		}
		command, err := h.DB.Exec(ctx, `
			UPDATE agent
			SET provider_session_id = NULLIF($1, ''), updated_at = now()
			WHERE workspace_id::text = $2 AND id::text = $3
		`, session.ProviderSessionID, identity.WorkspaceID, session.AgentID)
		if err != nil {
			return fmt.Errorf("persist Runner provider session: %w", err)
		}
		if command.RowsAffected() != 1 {
			return errors.New("Runner provider session Agent is no longer current")
		}
		return nil
	})
}

func (h *Handler) recordRunnerActivity(ctx context.Context, identity daemonws.ClientIdentity, daemonInstanceID string, activity protocol.AgentActivityPayload) error {
	if err := activity.Validate(); err != nil {
		return err
	}
	snapshot := activity.Snapshot
	if snapshot.DaemonInstanceID != daemonInstanceID {
		return errors.New("Activity daemon instance does not match current Runner")
	}
	workspaceID, agentID, _, err := h.runnerActivityAgentScope(ctx, identity.WorkspaceID, snapshot.AgentID)
	if err != nil {
		return err
	}
	obs, ok := h.observations().get(identity.WorkspaceID, snapshot.AgentID)
	if !ok {
		obs, ok = h.recoverRunnerObservation(ctx, identity, daemonInstanceID, snapshot.AgentID)
	}
	if !ok || obs.agentID != snapshot.AgentID || obs.daemonID != identity.DaemonID || obs.daemonInstanceID != daemonInstanceID {
		return errors.New("stale or unauthorized Runner Activity")
	}
	if obs.status != protocol.AgentStatusActive {
		return errors.New("stale or unauthorized Runner Activity")
	}
	runtimeID, err := util.ParseUUID(obs.runtimeID)
	if err != nil {
		runtimeID = pgtype.UUID{}
	}
	_, err = h.DB.Exec(ctx, `
		INSERT INTO agent_activity_snapshot (
			workspace_id, agent_id, runtime_id, daemon_id, daemon_instance_id,
			activity_kind, detail_kind, summary_label, observed_at
		) VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9)
		ON CONFLICT (workspace_id, agent_id) DO UPDATE SET
			runtime_id = EXCLUDED.runtime_id,
			daemon_id = EXCLUDED.daemon_id,
			daemon_instance_id = EXCLUDED.daemon_instance_id,
			activity_kind = EXCLUDED.activity_kind,
			detail_kind = EXCLUDED.detail_kind,
			summary_label = EXCLUDED.summary_label,
			observed_at = EXCLUDED.observed_at,
			received_at = now()`,
		workspaceID, agentID, runtimeID, identity.DaemonID, daemonInstanceID,
		snapshot.ActivityKind, snapshot.DetailKind, activity.Summary.Label, snapshot.ObservedAt)
	if err != nil {
		return fmt.Errorf("upsert Runner Activity snapshot: %w", err)
	}
	for _, row := range activity.Timeline {

		_, err := h.DB.Exec(ctx, `
			INSERT INTO agent_activity_entry (
				workspace_id, agent_id, runtime_id, daemon_id, daemon_instance_id,
				activity_kind, detail_kind, title, subtext, body_kind, body, observed_at
			) VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11, $12)`,
			workspaceID, agentID, runtimeID, identity.DaemonID, daemonInstanceID,
			row.ActivityKind, row.DetailKind, row.Title, row.Subtext, row.BodyKind, row.Body, snapshot.ObservedAt)
		if err != nil {
			return fmt.Errorf("insert Runner Activity entry: %w", err)
		}
	}
	if h.Bus != nil {
		projected, err := h.runnerActivityPresentation(ctx, workspaceID, agentID)
		if err != nil {
			return err
		}
		projected.Timing = runnerActivityTimingResponse(activity.Timing)
		h.publish(protocol.EventAgentActivity, identity.WorkspaceID, "system", "", RunnerActivityRealtimePayload{
			AgentID:  snapshot.AgentID,
			Activity: projected,
		})
	}
	return nil
}

// HandleWorkspaceDaemonDisconnect is invoked only for the socket that still
// owns the current ready Runner slot. Exact daemon-instance fencing prevents a
// late teardown from deactivating a replacement Runner's launches.
func (h *Handler) HandleWorkspaceDaemonDisconnect(ctx context.Context, identity daemonws.ClientIdentity, daemonInstanceID string) error {
	err := h.runnerPresenceLocked(func() error {
		if _, err := util.ParseUUID(identity.WorkspaceID); err != nil {
			return errors.New("invalid Runner workspace identity")
		}
		presenceAgentIDs := make([]pgtype.UUID, 0)
		for _, obs := range h.observations().listInstance(identity.WorkspaceID, identity.DaemonID, daemonInstanceID) {
			if obs.status != protocol.AgentStatusActive {
				continue
			}
			agentID, err := util.ParseUUID(obs.agentID)
			if err != nil {
				return fmt.Errorf("scan disconnected Runner launch: %w", err)
			}
			presenceAgentIDs = append(presenceAgentIDs, agentID)
		}

		h.observations().forgetInstance(identity.WorkspaceID, identity.DaemonID, daemonInstanceID)

		for _, agentID := range presenceAgentIDs {
			h.publishAgentPresence(identity.WorkspaceID, util.UUIDToString(agentID), AgentPresenceOffline)
		}
		return nil
	})
	if err != nil {
		return err
	}
	h.publish(protocol.EventComputerStatus, identity.WorkspaceID, "system", "", map[string]any{
		"computer_id": identity.DaemonID,
		"status":      "disconnected",
		"changed_at":  time.Now().UTC().Format(time.RFC3339Nano),
	})
	return nil
}

func (h *Handler) liveRunnerOwnsActivitySnapshot(daemonID, workspaceID string, snapshot protocol.AgentActivitySnapshot) bool {
	if h == nil || daemonID == "" || snapshot.DaemonInstanceID == "" {
		return false
	}
	source := h.currentRunnerPresenceSource()
	return source != nil && source.IsCurrentWorkspaceDaemon(daemonID, workspaceID, snapshot.DaemonInstanceID)
}

// GetRunnerActivity is the Workspace-authorized presentation API for typed
// Runner Activity. It has a distinct path from the removed historical
// /activity endpoints, so no compatibility translation or dual representation
// is needed.
func (h *Handler) GetRunnerActivity(w http.ResponseWriter, r *http.Request) {
	workspaceID, agentID, ok := h.prepareRunnerActivityRead(w, r)
	if !ok {
		return
	}
	response, err := h.runnerActivityPresentation(r.Context(), workspaceID, agentID)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "failed to load runner activity")
		return
	}
	writeJSON(w, http.StatusOK, response)
}

func (h *Handler) prepareRunnerActivityRead(w http.ResponseWriter, r *http.Request) (pgtype.UUID, pgtype.UUID, bool) {
	if _, ok := requireUserID(w, r); !ok {
		return pgtype.UUID{}, pgtype.UUID{}, false
	}
	workspaceIDText := ctxWorkspaceID(r.Context())
	if workspaceIDText == "" {
		workspaceIDText = h.resolveWorkspaceID(r)
	}
	if workspaceIDText == "" {
		writeError(w, http.StatusBadRequest, "workspace_id is required")
		return pgtype.UUID{}, pgtype.UUID{}, false
	}
	actorType, _ := h.resolveActor(r, requestUserID(r), workspaceIDText)
	if actorType == "agent" {
		writeError(w, http.StatusForbidden, "agent principals may not read runner activity")
		return pgtype.UUID{}, pgtype.UUID{}, false
	}
	// loadAgentForUser preserves the public object contract: an agent outside
	// this Workspace is indistinguishable from one that does not exist (404).
	agent, ok := h.loadAgentForUser(w, r, chi.URLParam(r, "id"))
	if !ok {
		return pgtype.UUID{}, pgtype.UUID{}, false
	}
	if _, ok := h.workspaceMember(w, r, workspaceIDText); !ok {
		return pgtype.UUID{}, pgtype.UUID{}, false
	}
	workspaceID, err := util.ParseUUID(workspaceIDText)
	if err != nil {
		writeError(w, http.StatusBadRequest, "invalid workspace_id")
		return pgtype.UUID{}, pgtype.UUID{}, false
	}
	return workspaceID, agent.ID, true
}

func (h *Handler) runnerActivityPresentation(ctx context.Context, workspaceID, agentID pgtype.UUID) (RunnerActivityResponse, error) {
	response := RunnerActivityResponse{Timeline: []RunnerActivityTimelineResponseRow{}}
	var snapshot protocol.AgentActivitySnapshot
	var observedAt pgtype.Timestamptz
	var daemonID string
	var summary protocol.AgentActivitySummary
	err := h.DB.QueryRow(ctx, `
		SELECT daemon_id, daemon_instance_id, observed_at,
		       activity_kind, detail_kind, summary_label
		FROM agent_activity_snapshot
		WHERE workspace_id = $1 AND agent_id = $2`, workspaceID, agentID).Scan(
		&daemonID, &snapshot.DaemonInstanceID, &observedAt,
		&summary.ActivityKind, &summary.DetailKind, &summary.Label,
	)
	if errors.Is(err, pgx.ErrNoRows) {
		return response, nil
	}
	if err != nil {
		return RunnerActivityResponse{}, fmt.Errorf("load Runner Activity snapshot: %w", err)
	}
	snapshot.AgentID = util.UUIDToString(agentID)
	snapshot.ObservedAt = observedAt.Time
	if h.liveRunnerOwnsActivitySnapshot(daemonID, util.UUIDToString(workspaceID), snapshot) {
		response.Summary = &summary
	}

	rows, err := h.DB.Query(ctx, `
		SELECT id, activity_kind, detail_kind, title, subtext, body_kind, body, observed_at
		FROM agent_activity_entry
		WHERE workspace_id = $1 AND agent_id = $2
		ORDER BY observed_at DESC, id DESC
		LIMIT $3`, workspaceID, agentID, runnerActivityTimelineLimit)
	if err != nil {
		return RunnerActivityResponse{}, fmt.Errorf("load Runner Activity timeline: %w", err)
	}
	for rows.Next() {
		var id pgtype.UUID
		var row protocol.AgentActivityTimelineRow
		var occurredAt pgtype.Timestamptz
		if err := rows.Scan(&id, &row.ActivityKind, &row.DetailKind, &row.Title, &row.Subtext, &row.BodyKind, &row.Body, &occurredAt); err != nil {
			return RunnerActivityResponse{}, fmt.Errorf("scan Runner Activity timeline: %w", err)
		}
		response.Timeline = append(response.Timeline, RunnerActivityTimelineResponseRow{
			ID: util.UUIDToString(id), OccurredAt: occurredAt.Time.UTC().Format(time.RFC3339Nano),
			ActivityKind: row.ActivityKind, DetailKind: row.DetailKind, Title: row.Title, Subtext: row.Subtext, BodyKind: row.BodyKind, Body: row.Body,
		})
	}
	if err := rows.Err(); err != nil {
		rows.Close()
		return RunnerActivityResponse{}, fmt.Errorf("iterate Runner Activity timeline: %w", err)
	}
	rows.Close()

	return response, nil
}


func (h *Handler) runnerActivityAgentScope(ctx context.Context, workspaceIDText, agentIDText string) (pgtype.UUID, pgtype.UUID, pgtype.UUID, error) {
	workspaceID, err := util.ParseUUID(workspaceIDText)
	if err != nil {
		return pgtype.UUID{}, pgtype.UUID{}, pgtype.UUID{}, errors.New("invalid Runner workspace identity")
	}
	agentID, err := util.ParseUUID(agentIDText)
	if err != nil {
		return pgtype.UUID{}, pgtype.UUID{}, pgtype.UUID{}, errors.New("invalid Runner Agent identity")
	}
	var runtimeID pgtype.UUID
	err = h.DB.QueryRow(ctx, `SELECT runtime_id FROM agent WHERE id = $1 AND workspace_id = $2`, agentID, workspaceID).Scan(&runtimeID)
	if errors.Is(err, pgx.ErrNoRows) {
		return pgtype.UUID{}, pgtype.UUID{}, pgtype.UUID{}, errors.New("Runner Agent not found in workspace")
	}
	if err != nil {
		return pgtype.UUID{}, pgtype.UUID{}, pgtype.UUID{}, fmt.Errorf("load Runner Agent scope: %w", err)
	}
	return workspaceID, agentID, runtimeID, nil
}
