package handler

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strings"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/multica-ai/multica/server/internal/activityprojection"
	"github.com/multica-ai/multica/server/internal/daemonws"
	"github.com/multica-ai/multica/server/internal/util"
	"github.com/multica-ai/multica/server/pkg/protocol"
)

const runnerActivityTimelineLimit = 100

const (
	runnerActivityStaleAfter  = 90 * time.Second
	runnerActivityProbeWindow = 5 * time.Second
)

// RunnerActivityResponse is deliberately a presentation boundary. Browser and
// desktop callers receive labels, tones and bounded narrative bodies, never a
// Runner fact envelope or provider-specific detail.
type RunnerActivityResponse struct {
	Summary  *activityprojection.Summary         `json:"summary"`
	Timeline []RunnerActivityTimelineResponseRow `json:"timeline"`
}

type RunnerActivityTimelineResponseRow struct {
	ID         string `json:"id"`
	OccurredAt string `json:"occurred_at"`
	activityprojection.TimelineRow
}

type RunnerActivityRealtimePayload struct {
	AgentID  string                 `json:"agent_id"`
	Activity RunnerActivityResponse `json:"activity"`
}

type runnerActivityTimelineEntry struct {
	ID         pgtype.UUID
	Entry      protocol.AgentActivityEntry
	OccurredAt time.Time
}

// HandleWorkspaceRunnerFrame accepts frames only from the current ready Runner.
// It adds durable Agent, launch, daemon-instance, fact, and sequence fencing
// before persistence.
func (h *Handler) HandleWorkspaceRunnerFrame(ctx context.Context, identity daemonws.ClientIdentity, daemonInstanceID, eventType string, raw json.RawMessage) error {
	if h == nil || h.DB == nil {
		return errors.New("handler database is unavailable")
	}
	switch eventType {
	case protocol.EventWorkspaceRunnerReady:
		var ready protocol.WorkspaceRunnerReadyPayload
		if err := json.Unmarshal(raw, &ready); err != nil {
			return fmt.Errorf("decode Runner ready: %w", err)
		}
		if ready.WorkspaceID != identity.WorkspaceID || ready.DaemonInstanceID != daemonInstanceID {
			return errors.New("Runner ready identity does not match current connection")
		}
		if err := h.recordWorkspaceRunnerReady(ctx, identity, daemonInstanceID, ready.RunningAgents); err != nil {
			return err
		}
		// Raft establishes APM ownership before it offers durable deliveries.
		// The Computer can then accept messages into the Agent's starting Inbox
		// and ACK them without requiring the Provider to be ready yet.
		if err := h.reconcileWorkspaceRunnerLaunches(ctx, identity); err != nil {
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
			return h.reconcileWorkspaceRunnerLaunches(ctx, identity)
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
		var result protocol.WorkspaceRunnerAgentResetWorkspaceResultPayload
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

// recordWorkspaceRunnerReady forgets Activity owned by an older Computer
// process. Launch status is not durable residency: a leftover active row must
// not block the replacement process from reporting agent:status, so this
// handler only retires snapshots/probes from a previous daemonInstanceID.
func (h *Handler) recordWorkspaceRunnerReady(ctx context.Context, identity daemonws.ClientIdentity, daemonInstanceID string, runningAgentIDs []string) error {
	return h.runnerPresenceLocked(func() error {
		workspaceID, err := util.ParseUUID(identity.WorkspaceID)
		if err != nil {
			return errors.New("invalid Runner workspace identity")
		}
		if h.TxStarter == nil {
			return errors.New("Runner transaction store is unavailable")
		}
		tx, err := h.TxStarter.Begin(ctx)
		if err != nil {
			return fmt.Errorf("begin Runner ready fence: %w", err)
		}
		defer tx.Rollback(ctx)

		rows, err := tx.Query(ctx, `
		UPDATE agent_activity_snapshot
		SET activity_kind = 'offline', detail_kind = 'computer_restarted',
			observed_at = now(), received_at = now()
		WHERE workspace_id = $1 AND daemon_id = $2 AND daemon_instance_id <> $3
		RETURNING agent_id`, workspaceID, identity.DaemonID, daemonInstanceID)
		if err != nil {
			return fmt.Errorf("fence prior Runner snapshots: %w", err)
		}
		var activityAgentIDs []pgtype.UUID
		for rows.Next() {
			var agentID pgtype.UUID
			if err := rows.Scan(&agentID); err != nil {
				rows.Close()
				return fmt.Errorf("scan fenced Runner Agent: %w", err)
			}
			activityAgentIDs = append(activityAgentIDs, agentID)
		}
		if err := rows.Err(); err != nil {
			rows.Close()
			return fmt.Errorf("iterate fenced Runner Agents: %w", err)
		}
		rows.Close()
		if _, err := tx.Exec(ctx, `DELETE FROM agent_activity_probe
		WHERE workspace_id = $1 AND daemon_id = $2 AND daemon_instance_id <> $3`, workspaceID, identity.DaemonID, daemonInstanceID); err != nil {
			return fmt.Errorf("clear prior Runner probes: %w", err)
		}
		if err := tx.Commit(ctx); err != nil {
			return fmt.Errorf("commit Runner ready fence: %w", err)
		}
		h.activityCursor().forgetOtherInstances(identity.WorkspaceID, identity.DaemonID, daemonInstanceID)
		h.observations().forgetOtherInstances(identity.WorkspaceID, identity.DaemonID, daemonInstanceID)

		if h.Bus != nil {
			for _, agentID := range activityAgentIDs {
				projected, err := h.runnerActivityPresentation(ctx, workspaceID, agentID)
				if err != nil {
					return err
				}
				h.publish(protocol.EventAgentActivity, identity.WorkspaceID, "system", "", RunnerActivityRealtimePayload{
					AgentID: util.UUIDToString(agentID), Activity: projected,
				})
			}
		}
		return nil
	})
}

func (h *Handler) recordRunnerLaunch(ctx context.Context, identity daemonws.ClientIdentity, daemonInstanceID string, status protocol.AgentStatusPayload) error {
	if err := status.Validate(); err != nil {
		return err
	}
	return h.runnerPresenceLocked(func() error {
		source := h.currentRunnerPresenceSource()
		if source == nil || !source.IsCurrentWorkspaceRunner(identity.DaemonID, identity.WorkspaceID, daemonInstanceID) {
			return errors.New("stale Workspace Runner status")
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
		if !h.observations().acceptStatus(identity.WorkspaceID, identity.DaemonID, daemonInstanceID, status.AgentID, util.UUIDToString(runtimeID), status.LaunchID, status.Status) {
			return errors.New("stale Workspace Runner launch status")
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
		if source == nil || !source.IsCurrentWorkspaceRunner(identity.DaemonID, identity.WorkspaceID, daemonInstanceID) {
			return errors.New("stale Workspace Runner start acknowledgement")
		}
		workspaceID, agentID, runtimeID, err := h.runnerActivityAgentScope(ctx, identity.WorkspaceID, acknowledgement.AgentID)
		if err != nil {
			return err
		}
		beforeLaunch, err := h.loadRunnerLaunchPresence(ctx, identity.WorkspaceID, acknowledgement.AgentID)
		if err != nil {
			return err
		}
		before := h.projectRunnerLaunchPresence(identity.WorkspaceID, beforeLaunch)
		var desiredLaunchID, desiredStartDispatchID string
		if err := h.DB.QueryRow(ctx, `
			SELECT launch_id::text, start_dispatch_id::text FROM agent_runner_launch_projection
			WHERE workspace_id = $1 AND agent_id = $2 AND runtime_id = $3`, workspaceID, agentID, runtimeID).Scan(&desiredLaunchID, &desiredStartDispatchID); err != nil {
			return fmt.Errorf("load desired Runner launch: %w", err)
		}
		if desiredLaunchID != acknowledgement.LaunchID || desiredStartDispatchID != acknowledgement.StartDispatchID {
			return errors.New("stale Workspace Runner start acknowledgement")
		}
		status, ok := h.observations().acceptStartAck(identity.WorkspaceID, identity.DaemonID, daemonInstanceID, acknowledgement.AgentID, util.UUIDToString(runtimeID), acknowledgement.LaunchID)
		if !ok {
			return errors.New("stale Workspace Runner start acknowledgement")
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

func (h *Handler) recordRunnerSession(ctx context.Context, identity daemonws.ClientIdentity, daemonInstanceID string, session protocol.AgentSessionPayload) error {
	if err := session.Validate(); err != nil {
		return err
	}
	return h.runnerPresenceLocked(func() error {
		source := h.currentRunnerPresenceSource()
		if source == nil || !source.IsCurrentWorkspaceRunner(identity.DaemonID, identity.WorkspaceID, daemonInstanceID) {
			return errors.New("stale Workspace Runner session")
		}
		if _, _, _, err := h.runnerActivityAgentScope(ctx, identity.WorkspaceID, session.AgentID); err != nil {
			return err
		}
		if !h.observations().acceptSession(identity.WorkspaceID, identity.DaemonID, daemonInstanceID, session.AgentID, session.LaunchID, session.ProviderSessionID) {
			return errors.New("stale or unknown Workspace Runner session")
		}
		return nil
	})
}

func (h *Handler) recordRunnerActivity(ctx context.Context, identity daemonws.ClientIdentity, daemonInstanceID string, activity protocol.AgentActivityPayload) error {
	if err := activity.Validate(); err != nil {
		return err
	}
	snapshot := activity.Snapshot
	// Raft's agent:activity wire carries execution facts, not presentation
	// state. Reduce the fact on the server before fencing, persistence, and
	// realtime projection so a daemon can never overwrite UI lifecycle state.
	snapshot.ActivityKind = activityprojection.ActivityKindFromDetailKind(snapshot.DetailKind)
	activity.Snapshot = snapshot
	if snapshot.DaemonInstanceID != daemonInstanceID {
		return errors.New("Activity daemon instance does not match current Runner")
	}
	workspaceID, agentID, _, err := h.runnerActivityAgentScope(ctx, identity.WorkspaceID, snapshot.AgentID)
	if err != nil {
		return err
	}
	terminalStopped := snapshot.ActivityKind == protocol.ActivityKindOffline && snapshot.DetailKind == "stopped" && snapshot.ProbeID == ""
	obs, ok := h.observations().get(identity.WorkspaceID, snapshot.AgentID)
	if !ok || obs.daemonID != identity.DaemonID || obs.daemonInstanceID != daemonInstanceID || obs.launchID != snapshot.LaunchID {
		return errors.New("stale or unauthorized Runner Activity")
	}
	if obs.status != protocol.AgentStatusActive && !terminalStopped {
		return errors.New("stale or unauthorized Runner Activity")
	}
	if !h.activityCursor().accept(runnerActivityCursorKey{
		workspaceID: identity.WorkspaceID, agentID: snapshot.AgentID,
		daemonID: identity.DaemonID, daemonInstanceID: daemonInstanceID, launchID: snapshot.LaunchID,
	}, snapshot.ClientSequence, snapshot.ProducerFactID) {
		return errors.New("stale or unauthorized Runner Activity")
	}
	if snapshot.ProbeID != "" {
		var pending int
		if err := h.DB.QueryRow(ctx, `
			SELECT count(*) FROM agent_activity_probe
			WHERE workspace_id = $1 AND agent_id = $2 AND daemon_id = $3
			  AND daemon_instance_id = $4 AND launch_id = $5 AND probe_id = $6`,
			workspaceID, agentID, identity.DaemonID, daemonInstanceID, snapshot.LaunchID, snapshot.ProbeID).Scan(&pending); err != nil {
			return fmt.Errorf("load Runner Activity probe: %w", err)
		}
		if pending == 0 {
			return errors.New("stale or unauthorized Runner Activity")
		}
	} else {
		var pending int
		if err := h.DB.QueryRow(ctx, `SELECT count(*) FROM agent_activity_probe WHERE workspace_id = $1 AND agent_id = $2`, workspaceID, agentID).Scan(&pending); err != nil {
			return fmt.Errorf("load Runner Activity probe: %w", err)
		}
		if pending != 0 && !terminalStopped {
			return errors.New("stale or unauthorized Runner Activity")
		}
	}
	runtimeID, err := util.ParseUUID(obs.runtimeID)
	if err != nil {
		runtimeID = pgtype.UUID{}
	}
	if terminalStopped {
		if _, err := h.DB.Exec(ctx, `DELETE FROM agent_activity_probe
			WHERE workspace_id = $1 AND agent_id = $2 AND daemon_id = $3 AND daemon_instance_id = $4 AND launch_id = $5`,
			workspaceID, agentID, identity.DaemonID, daemonInstanceID, snapshot.LaunchID); err != nil {
			return fmt.Errorf("clear terminal Runner Activity probe: %w", err)
		}
	} else if snapshot.ProbeID != "" {
		if _, err := h.DB.Exec(ctx, `DELETE FROM agent_activity_probe
			WHERE workspace_id = $1 AND agent_id = $2 AND daemon_id = $3 AND daemon_instance_id = $4 AND launch_id = $5 AND probe_id = $6`,
			workspaceID, agentID, identity.DaemonID, daemonInstanceID, snapshot.LaunchID, snapshot.ProbeID); err != nil {
			return fmt.Errorf("clear Runner Activity probe: %w", err)
		}
	}
	_, err = h.DB.Exec(ctx, `
		INSERT INTO agent_activity_snapshot (
			workspace_id, agent_id, runtime_id, daemon_id, daemon_instance_id, launch_id,
			process_instance_id, client_sequence, producer_fact_id, probe_id,
			activity_kind, detail_kind, observed_at
		) VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11, $12, $13)
		ON CONFLICT (workspace_id, agent_id) DO UPDATE SET
			runtime_id = EXCLUDED.runtime_id,
			daemon_id = EXCLUDED.daemon_id,
			daemon_instance_id = EXCLUDED.daemon_instance_id,
			launch_id = EXCLUDED.launch_id,
			process_instance_id = EXCLUDED.process_instance_id,
			client_sequence = EXCLUDED.client_sequence,
			producer_fact_id = EXCLUDED.producer_fact_id,
			probe_id = EXCLUDED.probe_id,
			activity_kind = EXCLUDED.activity_kind,
			detail_kind = EXCLUDED.detail_kind,
			observed_at = EXCLUDED.observed_at,
			received_at = now()`,
		workspaceID, agentID, runtimeID, identity.DaemonID, daemonInstanceID, snapshot.LaunchID,
		snapshot.ProcessInstanceID, snapshot.ClientSequence, snapshot.ProducerFactID, snapshot.ProbeID,
		snapshot.ActivityKind, snapshot.DetailKind, snapshot.ObservedAt)
	if err != nil {
		return fmt.Errorf("upsert Runner Activity snapshot: %w", err)
	}
	if activity.IsHeartbeat {
		if h.Bus != nil {
			projected, err := h.runnerActivityPresentation(ctx, workspaceID, agentID)
			if err != nil {
				return err
			}
			h.publish(protocol.EventAgentActivity, identity.WorkspaceID, "system", "", RunnerActivityRealtimePayload{
				AgentID:  snapshot.AgentID,
				Activity: projected,
			})
		}
		return nil
	}
	for _, entry := range activity.Entries {
		_, err := h.DB.Exec(ctx, `
			INSERT INTO agent_activity_entry (
				workspace_id, agent_id, runtime_id, daemon_id, daemon_instance_id, launch_id,
				process_instance_id, client_sequence, producer_fact_id, entry_position,
				entry_kind, entry_body, observed_at
			) VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11, $12, $13)
			ON CONFLICT (workspace_id, agent_id, launch_id, producer_fact_id, entry_position) DO NOTHING`,
			workspaceID, agentID, runtimeID, identity.DaemonID, daemonInstanceID, snapshot.LaunchID,
			snapshot.ProcessInstanceID, snapshot.ClientSequence, snapshot.ProducerFactID, entry.Position,
			entry.Kind, entry.Body, snapshot.ObservedAt)
		if err != nil {
			return fmt.Errorf("insert Runner Activity entry: %w", err)
		}
	}
	// This publish remains inert until the coordinated cutover: no current
	// Runner emits these frames. Keeping the projection at the server boundary
	// ensures clients never need a runtime/provider semantic fallback once it is
	// activated.
	if h.Bus != nil {
		projected, err := h.runnerActivityPresentation(ctx, workspaceID, agentID)
		if err != nil {
			return err
		}
		h.publish(protocol.EventAgentActivity, identity.WorkspaceID, "system", "", RunnerActivityRealtimePayload{
			AgentID:  snapshot.AgentID,
			Activity: projected,
		})
	}
	return nil
}

// ReapStaleRunnerActivity drives timeout behavior from its explicit now
// argument, which keeps the 90-second probe and five-second close deterministic
// in integration tests and avoids treating a client-provided observed_at as a
// trusted clock.
func (h *Handler) ReapStaleRunnerActivity(ctx context.Context, now time.Time) error {
	if h == nil || h.DB == nil {
		return errors.New("handler database is unavailable")
	}
	if err := h.timeoutRunnerActivityProbes(ctx, now); err != nil {
		return err
	}
	return h.sendRunnerActivityProbes(ctx, now)
}

func (h *Handler) sendRunnerActivityProbes(ctx context.Context, now time.Time) error {
	type staleRunnerActivity struct {
		workspaceID      pgtype.UUID
		agentID          pgtype.UUID
		daemonID         string
		daemonInstanceID string
		launchID         string
	}
	rows, err := h.DB.Query(ctx, `
		SELECT s.workspace_id, s.agent_id, s.daemon_id, s.daemon_instance_id, s.launch_id
		FROM agent_activity_snapshot s
		LEFT JOIN agent_activity_probe p ON p.workspace_id = s.workspace_id AND p.agent_id = s.agent_id
		WHERE s.activity_kind IN ('working', 'thinking') AND s.received_at <= $1
			AND p.agent_id IS NULL`, now.Add(-runnerActivityStaleAfter))
	if err != nil {
		return fmt.Errorf("list stale Runner Activity: %w", err)
	}
	var stale []staleRunnerActivity
	for rows.Next() {
		var candidate staleRunnerActivity
		if err := rows.Scan(&candidate.workspaceID, &candidate.agentID, &candidate.daemonID, &candidate.daemonInstanceID, &candidate.launchID); err != nil {
			rows.Close()
			return fmt.Errorf("scan stale Runner Activity: %w", err)
		}
		stale = append(stale, candidate)
	}
	if err := rows.Err(); err != nil {
		rows.Close()
		return fmt.Errorf("iterate stale Runner Activity: %w", err)
	}
	rows.Close()
	for _, candidate := range stale {
		probeID := randomID()
		obs, ok := h.observations().get(util.UUIDToString(candidate.workspaceID), util.UUIDToString(candidate.agentID))
		if !ok || obs.status != protocol.AgentStatusActive || obs.daemonID != candidate.daemonID || obs.daemonInstanceID != candidate.daemonInstanceID || obs.launchID != candidate.launchID {
			continue
		}
		command, err := h.DB.Exec(ctx, `
			INSERT INTO agent_activity_probe (workspace_id, agent_id, daemon_id, daemon_instance_id, launch_id, probe_id, sent_at, deadline_at)
			SELECT $1, $2, $3, $4, $5, $6, $7, $8
			WHERE EXISTS (
				SELECT 1 FROM agent_activity_snapshot s
				WHERE s.workspace_id = $1 AND s.agent_id = $2 AND s.daemon_id = $3 AND s.daemon_instance_id = $4 AND s.launch_id = $5
					AND s.activity_kind IN ('working', 'thinking') AND s.received_at <= $9
			)
			ON CONFLICT (workspace_id, agent_id) DO NOTHING`, candidate.workspaceID, candidate.agentID, candidate.daemonID, candidate.daemonInstanceID, candidate.launchID, probeID, now, now.Add(runnerActivityProbeWindow), now.Add(-runnerActivityStaleAfter))
		if err != nil {
			return fmt.Errorf("record Runner Activity probe: %w", err)
		}
		if command.RowsAffected() != 1 {
			continue
		}
		if h.DaemonHub == nil || !h.DaemonHub.NotifyWorkspaceRunner(candidate.daemonID, util.UUIDToString(candidate.workspaceID), protocol.EventAgentActivityProbe, protocol.AgentActivityProbePayload{AgentID: util.UUIDToString(candidate.agentID), LaunchID: candidate.launchID, ProbeID: probeID}) {
			if err := h.markRunnerActivityOfflineForComputerDisconnect(ctx, candidate.workspaceID, candidate.agentID, candidate.daemonID, candidate.daemonInstanceID, candidate.launchID); err != nil {
				return err
			}
		}
	}
	return nil
}

func (h *Handler) timeoutRunnerActivityProbes(ctx context.Context, now time.Time) error {
	type timedOutRunnerActivityProbe struct {
		workspaceID      pgtype.UUID
		agentID          pgtype.UUID
		daemonID         string
		daemonInstanceID string
		launchID         string
	}
	rows, err := h.DB.Query(ctx, `SELECT workspace_id, agent_id, daemon_id, daemon_instance_id, launch_id FROM agent_activity_probe WHERE deadline_at <= $1`, now)
	if err != nil {
		return fmt.Errorf("list timed out Runner Activity probes: %w", err)
	}
	var timedOut []timedOutRunnerActivityProbe
	for rows.Next() {
		var candidate timedOutRunnerActivityProbe
		if err := rows.Scan(&candidate.workspaceID, &candidate.agentID, &candidate.daemonID, &candidate.daemonInstanceID, &candidate.launchID); err != nil {
			rows.Close()
			return fmt.Errorf("scan timed out Runner Activity probe: %w", err)
		}
		timedOut = append(timedOut, candidate)
	}
	if err := rows.Err(); err != nil {
		rows.Close()
		return fmt.Errorf("iterate timed out Runner Activity probes: %w", err)
	}
	rows.Close()
	for _, candidate := range timedOut {
		if h.DaemonHub != nil {
			h.DaemonHub.CloseWorkspaceRunner(candidate.daemonID, util.UUIDToString(candidate.workspaceID), candidate.daemonInstanceID)
		}
		if err := h.markRunnerActivityOfflineForComputerDisconnect(ctx, candidate.workspaceID, candidate.agentID, candidate.daemonID, candidate.daemonInstanceID, candidate.launchID); err != nil {
			return err
		}
	}
	return nil
}

// HandleWorkspaceRunnerDisconnect is invoked only for the socket that still
// owns the current ready Runner slot. Exact daemon-instance fencing prevents a
// late teardown from deactivating a replacement Runner's launches.
func (h *Handler) HandleWorkspaceRunnerDisconnect(ctx context.Context, identity daemonws.ClientIdentity, daemonInstanceID string) error {
	return h.runnerPresenceLocked(func() error {
		workspaceID, err := util.ParseUUID(identity.WorkspaceID)
		if err != nil {
			return errors.New("invalid Runner workspace identity")
		}
		if h.TxStarter == nil {
			return errors.New("Runner transaction store is unavailable")
		}
		tx, err := h.TxStarter.Begin(ctx)
		if err != nil {
			return fmt.Errorf("begin Runner disconnect fence: %w", err)
		}
		defer tx.Rollback(ctx)

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

		activityAgentIDs := make([]pgtype.UUID, 0)
		rows, err := tx.Query(ctx, `
			UPDATE agent_activity_snapshot
			SET activity_kind = 'offline', detail_kind = 'machine_disconnected', received_at = now()
			WHERE workspace_id = $1 AND daemon_id = $2 AND daemon_instance_id = $3
			RETURNING agent_id`, workspaceID, identity.DaemonID, daemonInstanceID)
		if err != nil {
			return fmt.Errorf("project disconnected Runner Activity: %w", err)
		}
		for rows.Next() {
			var agentID pgtype.UUID
			if err := rows.Scan(&agentID); err != nil {
				rows.Close()
				return fmt.Errorf("scan disconnected Runner Activity: %w", err)
			}
			activityAgentIDs = append(activityAgentIDs, agentID)
		}
		if err := rows.Err(); err != nil {
			rows.Close()
			return fmt.Errorf("iterate disconnected Runner Activity: %w", err)
		}
		rows.Close()
		if _, err := tx.Exec(ctx, `DELETE FROM agent_activity_probe
			WHERE workspace_id = $1 AND daemon_id = $2 AND daemon_instance_id = $3`, workspaceID, identity.DaemonID, daemonInstanceID); err != nil {
			return fmt.Errorf("clear disconnected Runner probes: %w", err)
		}
		if err := tx.Commit(ctx); err != nil {
			return fmt.Errorf("commit Runner disconnect fence: %w", err)
		}
		h.activityCursor().forgetInstance(identity.WorkspaceID, identity.DaemonID, daemonInstanceID)
		h.observations().forgetInstance(identity.WorkspaceID, identity.DaemonID, daemonInstanceID)

		for _, agentID := range presenceAgentIDs {
			h.publishAgentPresence(identity.WorkspaceID, util.UUIDToString(agentID), AgentPresenceOffline)
		}
		if h.Bus != nil {
			for _, agentID := range activityAgentIDs {
				projected, err := h.runnerActivityPresentation(ctx, workspaceID, agentID)
				if err != nil {
					return err
				}
				h.publish(protocol.EventAgentActivity, identity.WorkspaceID, "system", "", RunnerActivityRealtimePayload{
					AgentID: util.UUIDToString(agentID), Activity: projected,
				})
			}
		}
		return nil
	})
}

func (h *Handler) markRunnerActivityOfflineForComputerDisconnect(ctx context.Context, workspaceID, agentID pgtype.UUID, daemonID, daemonInstanceID, launchID string) error {
	return h.runnerPresenceLocked(func() error {
		deactivated := h.observations().deactivate(util.UUIDToString(workspaceID), daemonID, daemonInstanceID, util.UUIDToString(agentID), launchID)
		if _, err := h.DB.Exec(ctx, `UPDATE agent_activity_snapshot SET activity_kind = 'offline', detail_kind = 'machine_disconnected', received_at = now()
			WHERE workspace_id = $1 AND agent_id = $2 AND daemon_id = $3 AND daemon_instance_id = $4 AND launch_id = $5`, workspaceID, agentID, daemonID, daemonInstanceID, launchID); err != nil {
			return fmt.Errorf("project Runner disconnect: %w", err)
		}
		if _, err := h.DB.Exec(ctx, `DELETE FROM agent_activity_probe WHERE workspace_id = $1 AND agent_id = $2 AND daemon_id = $3 AND daemon_instance_id = $4 AND launch_id = $5`, workspaceID, agentID, daemonID, daemonInstanceID, launchID); err != nil {
			return fmt.Errorf("clear stale Runner Activity probe: %w", err)
		}
		if deactivated {
			h.publishAgentPresence(util.UUIDToString(workspaceID), util.UUIDToString(agentID), AgentPresenceOffline)
		}
		if h.Bus != nil {
			projected, err := h.runnerActivityPresentation(ctx, workspaceID, agentID)
			if err != nil {
				return err
			}
			h.publish(protocol.EventAgentActivity, util.UUIDToString(workspaceID), "system", "", RunnerActivityRealtimePayload{AgentID: util.UUIDToString(agentID), Activity: projected})
		}
		return nil
	})
}

func (h *Handler) liveRunnerOwnsActivitySnapshot(daemonID, workspaceID string, snapshot protocol.AgentActivitySnapshot) bool {
	if h == nil || daemonID == "" || snapshot.DaemonInstanceID == "" {
		return false
	}
	source := h.currentRunnerPresenceSource()
	return source != nil && source.IsCurrentWorkspaceRunner(daemonID, workspaceID, snapshot.DaemonInstanceID)
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
	err := h.DB.QueryRow(ctx, `
		SELECT daemon_id, daemon_instance_id, launch_id, client_sequence, producer_fact_id,
			observed_at, activity_kind, detail_kind, probe_id, process_instance_id
		FROM agent_activity_snapshot
		WHERE workspace_id = $1 AND agent_id = $2`, workspaceID, agentID).Scan(
		&daemonID, &snapshot.DaemonInstanceID, &snapshot.LaunchID, &snapshot.ClientSequence, &snapshot.ProducerFactID,
		&observedAt, &snapshot.ActivityKind, &snapshot.DetailKind, &snapshot.ProbeID, &snapshot.ProcessInstanceID,
	)
	if errors.Is(err, pgx.ErrNoRows) {
		return response, nil
	}
	if err != nil {
		return RunnerActivityResponse{}, fmt.Errorf("load Runner Activity snapshot: %w", err)
	}
	snapshot.AgentID = util.UUIDToString(agentID)
	snapshot.ObservedAt = observedAt.Time
	summary := activityprojection.ProjectSummary(snapshot)
	if h.liveRunnerOwnsActivitySnapshot(daemonID, util.UUIDToString(workspaceID), snapshot) {
		response.Summary = &summary
	}

	rows, err := h.DB.Query(ctx, `
		SELECT id, entry_kind, entry_body, observed_at
		FROM agent_activity_entry
		WHERE workspace_id = $1 AND agent_id = $2
		ORDER BY observed_at DESC, id DESC
		LIMIT $3`, workspaceID, agentID, runnerActivityTimelineLimit)
	if err != nil {
		return RunnerActivityResponse{}, fmt.Errorf("load Runner Activity timeline: %w", err)
	}
	entries := make([]runnerActivityTimelineEntry, 0, runnerActivityTimelineLimit)
	for rows.Next() {
		var id pgtype.UUID
		var entry protocol.AgentActivityEntry
		var occurredAt pgtype.Timestamptz
		if err := rows.Scan(&id, &entry.Kind, &entry.Body, &occurredAt); err != nil {
			return RunnerActivityResponse{}, fmt.Errorf("scan Runner Activity timeline: %w", err)
		}
		entries = append(entries, runnerActivityTimelineEntry{ID: id, Entry: entry, OccurredAt: occurredAt.Time})
	}
	if err := rows.Err(); err != nil {
		rows.Close()
		return RunnerActivityResponse{}, fmt.Errorf("iterate Runner Activity timeline: %w", err)
	}
	rows.Close()

	issueTitles, err := h.runnerActivityIssueTitles(ctx, workspaceID, entries)
	if err != nil {
		return RunnerActivityResponse{}, err
	}
	for _, entry := range entries {
		response.Timeline = append(response.Timeline, RunnerActivityTimelineResponseRow{
			ID:          util.UUIDToString(entry.ID),
			OccurredAt:  entry.OccurredAt.UTC().Format(time.RFC3339Nano),
			TimelineRow: projectRunnerActivityTimelineEntry(entry.Entry, summary, issueTitles),
		})
	}
	if summary.Tone == "error" {
		for _, row := range response.Timeline {
			if row.Title == "Error" && row.Subtext != "" {
				summary = runnerActivitySummaryWithError(summary, row.Subtext)
				break
			}
		}
	}
	return response, nil
}

func runnerActivitySummaryWithError(summary activityprojection.Summary, errorText string) activityprojection.Summary {
	errorText = strings.TrimSpace(errorText)
	if summary.Tone == "error" && errorText != "" {
		summary.Label = "Error: " + truncateRunnerActivitySummary(errorText, 240)
	}
	return summary
}

func truncateRunnerActivitySummary(value string, maxRunes int) string {
	runes := []rune(value)
	if len(runes) <= maxRunes {
		return value
	}
	return string(runes[:maxRunes]) + "…"
}

// runnerActivityIssueTitles resolves every concrete issue UUID in one
// workspace-scoped query. Raw UUIDs, shell variables, and guessed identifiers
// never reach the presentation response.
func (h *Handler) runnerActivityIssueTitles(ctx context.Context, workspaceID pgtype.UUID, entries []runnerActivityTimelineEntry) (map[string]string, error) {
	idsByString := make(map[string]pgtype.UUID)
	for _, entry := range entries {
		ref, ok := runnerActivityIssueReference(entry.Entry)
		if !ok {
			continue
		}
		id, err := util.ParseUUID(ref)
		if err != nil {
			continue
		}
		idsByString[util.UUIDToString(id)] = id
	}
	if len(idsByString) == 0 {
		return map[string]string{}, nil
	}
	ids := make([]pgtype.UUID, 0, len(idsByString))
	for _, id := range idsByString {
		ids = append(ids, id)
	}
	rows, err := h.DB.Query(ctx, `
		SELECT id, title
		FROM issue
		WHERE workspace_id = $1 AND id = ANY($2::uuid[])`, workspaceID, ids)
	if err != nil {
		return nil, fmt.Errorf("resolve Runner Activity issue titles: %w", err)
	}
	defer rows.Close()
	titles := make(map[string]string, len(ids))
	for rows.Next() {
		var id pgtype.UUID
		var title string
		if err := rows.Scan(&id, &title); err != nil {
			return nil, fmt.Errorf("scan Runner Activity issue title: %w", err)
		}
		titles[util.UUIDToString(id)] = title
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate Runner Activity issue titles: %w", err)
	}
	return titles, nil
}

func projectRunnerActivityTimelineEntry(entry protocol.AgentActivityEntry, summary activityprojection.Summary, issueTitles map[string]string) activityprojection.TimelineRow {
	row := activityprojection.ProjectTimelineEntry(entry, summary)
	ref, ok := runnerActivityIssueReference(entry)
	if !ok {
		return row
	}
	row.Subtext = ""
	if id, err := util.ParseUUID(ref); err == nil {
		row.Subtext = issueTitles[util.UUIDToString(id)]
	}
	return row
}

func runnerActivityIssueReference(entry protocol.AgentActivityEntry) (string, bool) {
	if entry.Kind != "narrative" {
		return "", false
	}
	var body protocol.AgentActivityNarrativeBody
	if json.Unmarshal(entry.Body, &body) != nil {
		return "", false
	}
	switch body.DetailKind {
	case "getting_issue", "listing_issue_comments", "commenting_issue", "deleting_issue_comment":
		return body.Text, true
	default:
		return "", false
	}
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
