package handler

import (
	"context"
	"crypto/sha256"
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
		if err := h.recordWorkspaceRunnerReady(ctx, identity, daemonInstanceID); err != nil {
			return err
		}
		if err := h.dispatchPendingRunnerLaunches(ctx, identity); err != nil {
			return err
		}
		return h.dispatchPendingRunnerStops(ctx, identity)
	case protocol.EventAgentStatus:
		var status protocol.AgentStatusPayload
		if err := json.Unmarshal(raw, &status); err != nil {
			return fmt.Errorf("decode Runner status: %w", err)
		}
		return h.recordRunnerLaunch(ctx, identity, daemonInstanceID, status)
	case protocol.EventAgentStartAck:
		var acknowledgement protocol.AgentStartAckPayload
		if err := json.Unmarshal(raw, &acknowledgement); err != nil {
			return fmt.Errorf("decode Runner start acknowledgement: %w", err)
		}
		return h.recordRunnerStartAcknowledgement(ctx, identity, daemonInstanceID, acknowledgement)
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
	case protocol.EventAgentAttachmentReplayReq:
		var request protocol.WorkspaceRunnerAttachmentReplayRequest
		if err := json.Unmarshal(raw, &request); err != nil {
			return fmt.Errorf("decode Attachment replay request: %w", err)
		}
		return h.replayAgentAttachmentCommands(ctx, identity, request)
	case protocol.EventAgentAttached:
		var receipt protocol.WorkspaceRunnerAgentAttachedPayload
		if err := json.Unmarshal(raw, &receipt); err != nil {
			return fmt.Errorf("decode Attachment attach receipt: %w", err)
		}
		return h.acknowledgeAgentAttachmentCommand(ctx, identity, eventType, receipt)
	case protocol.EventAgentDetached:
		var receipt protocol.WorkspaceRunnerAgentDetachedPayload
		if err := json.Unmarshal(raw, &receipt); err != nil {
			return fmt.Errorf("decode Attachment detach receipt: %w", err)
		}
		return h.acknowledgeAgentAttachmentCommand(ctx, identity, eventType, protocol.WorkspaceRunnerAgentAttachedPayload(receipt))
	case protocol.EventAgentAttachmentReplayAck:
		var acknowledgement protocol.WorkspaceRunnerAttachmentReplayAck
		if err := json.Unmarshal(raw, &acknowledgement); err != nil {
			return fmt.Errorf("decode Attachment replay acknowledgement: %w", err)
		}
		return h.acknowledgeAgentAttachmentReplay(ctx, identity, acknowledgement)
	default:
		return nil
	}
}

// recordWorkspaceRunnerReady fences launches owned by an older daemon process.
// A Computer may already be connected while none of its prior Agent processes
// have restarted; those Agents stay Offline until a new launch is reported.
func (h *Handler) recordWorkspaceRunnerReady(ctx context.Context, identity daemonws.ClientIdentity, daemonInstanceID string) error {
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
		UPDATE agent_activity_snapshot s
		SET activity_kind = 'offline', detail_kind = 'computer_restarted',
			observed_at = now(), received_at = now()
		FROM agent_activity_launch l
		WHERE l.workspace_id = s.workspace_id AND l.agent_id = s.agent_id
		  AND l.workspace_id = $1 AND l.daemon_id = $2
		  AND l.daemon_instance_id <> $3 AND l.status = 'active'
		RETURNING s.agent_id`, workspaceID, identity.DaemonID, daemonInstanceID)
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
		rows, err = tx.Query(ctx, `
		UPDATE agent_activity_launch
		SET status = 'inactive', updated_at = now()
		WHERE workspace_id = $1 AND daemon_id = $2
		  AND daemon_instance_id <> $3 AND status = 'active'
		RETURNING agent_id`, workspaceID, identity.DaemonID, daemonInstanceID)
		if err != nil {
			return fmt.Errorf("fence prior Runner launches: %w", err)
		}
		var presenceAgentIDs []pgtype.UUID
		for rows.Next() {
			var agentID pgtype.UUID
			if err := rows.Scan(&agentID); err != nil {
				rows.Close()
				return fmt.Errorf("scan fenced Runner launch: %w", err)
			}
			presenceAgentIDs = append(presenceAgentIDs, agentID)
		}
		if err := rows.Err(); err != nil {
			rows.Close()
			return fmt.Errorf("iterate fenced Runner launches: %w", err)
		}
		rows.Close()
		if _, err := tx.Exec(ctx, `DELETE FROM agent_activity_probe
		WHERE workspace_id = $1 AND daemon_id = $2 AND daemon_instance_id <> $3`, workspaceID, identity.DaemonID, daemonInstanceID); err != nil {
			return fmt.Errorf("clear prior Runner probes: %w", err)
		}
		if err := tx.Commit(ctx); err != nil {
			return fmt.Errorf("commit Runner ready fence: %w", err)
		}
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

func (h *Handler) recordRunnerLaunch(ctx context.Context, identity daemonws.ClientIdentity, daemonInstanceID string, status protocol.AgentStatusPayload) error {
	if err := status.Validate(); err != nil {
		return err
	}
	return h.runnerPresenceLocked(func() error {
		source := h.currentRunnerPresenceSource()
		if source == nil || !source.IsCurrentWorkspaceRunner(identity.DaemonID, identity.WorkspaceID, daemonInstanceID) {
			return errors.New("stale Workspace Runner status")
		}
		workspaceID, agentID, runtimeID, err := h.runnerActivityAgentScope(ctx, identity.WorkspaceID, status.AgentID)
		if err != nil {
			return err
		}
		beforeLaunch, err := h.loadRunnerLaunchPresence(ctx, workspaceID, agentID)
		if err != nil {
			return err
		}
		before := h.projectRunnerLaunchPresence(identity.WorkspaceID, beforeLaunch)
		command, err := h.DB.Exec(ctx, `
			INSERT INTO agent_activity_launch (
				workspace_id, agent_id, runtime_id, daemon_id, daemon_instance_id, launch_id, status
			) VALUES ($1, $2, $3, $4, $5, $6, $7)
			ON CONFLICT (workspace_id, agent_id) DO UPDATE SET
				runtime_id = EXCLUDED.runtime_id,
				daemon_id = EXCLUDED.daemon_id,
				daemon_instance_id = EXCLUDED.daemon_instance_id,
				launch_id = EXCLUDED.launch_id,
				status = EXCLUDED.status,
				last_client_sequence = 0,
				last_producer_fact_id = '',
				last_activity_fingerprint = '',
				updated_at = now()
			WHERE agent_activity_launch.start_dispatch_id = ''
			   OR agent_activity_launch.status = 'inactive'
			   OR (agent_activity_launch.daemon_id = EXCLUDED.daemon_id
			       AND agent_activity_launch.daemon_instance_id = EXCLUDED.daemon_instance_id
			       AND agent_activity_launch.launch_id = EXCLUDED.launch_id)`, workspaceID, agentID, runtimeID, identity.DaemonID, daemonInstanceID, status.LaunchID, status.Status)
		if err != nil {
			return fmt.Errorf("upsert Runner launch: %w", err)
		}
		if command.RowsAffected() != 1 {
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
		command, err := h.DB.Exec(ctx, `
			INSERT INTO agent_activity_launch (
				workspace_id, agent_id, runtime_id, daemon_id, daemon_instance_id, launch_id,
				status, start_dispatch_id, queue_state, queue_depth, queue_age_ms, accepted_at
			) VALUES ($1, $2, $3, $4, $5, $6, 'accepted', $7, $8, $9, $10, now())
			ON CONFLICT (workspace_id, agent_id) DO UPDATE SET
				runtime_id = EXCLUDED.runtime_id, daemon_id = EXCLUDED.daemon_id,
				daemon_instance_id = EXCLUDED.daemon_instance_id, launch_id = EXCLUDED.launch_id,
				status = 'accepted', start_dispatch_id = EXCLUDED.start_dispatch_id,
				queue_state = EXCLUDED.queue_state, queue_depth = EXCLUDED.queue_depth,
				queue_age_ms = EXCLUDED.queue_age_ms, accepted_at = COALESCE(agent_activity_launch.accepted_at, now()),
				last_client_sequence = 0, last_producer_fact_id = '', last_activity_fingerprint = '', updated_at = now()
			WHERE agent_activity_launch.status = 'inactive'
			   OR (agent_activity_launch.daemon_id = EXCLUDED.daemon_id
			       AND agent_activity_launch.daemon_instance_id = EXCLUDED.daemon_instance_id
			       AND agent_activity_launch.launch_id = EXCLUDED.launch_id
			       AND agent_activity_launch.start_dispatch_id = EXCLUDED.start_dispatch_id)`,
			workspaceID, agentID, runtimeID, identity.DaemonID, daemonInstanceID, acknowledgement.LaunchID,
			acknowledgement.StartDispatchID, acknowledgement.QueueState, acknowledgement.QueueDepth, acknowledgement.QueueAgeMS)
		if err != nil {
			return fmt.Errorf("persist Runner start acknowledgement: %w", err)
		}
		if command.RowsAffected() != 1 {
			return errors.New("stale Workspace Runner start acknowledgement")
		}
		if _, err := h.DB.Exec(ctx, `
			UPDATE agent_start_intent
			SET status = CASE WHEN $4 = 'queued' THEN 'queued' ELSE 'accepted' END,
			    lifecycle_seq = GREATEST(lifecycle_seq, 1), accepted_at = COALESCE(accepted_at, now()),
			    reported_at = now(), updated_at = now()
			WHERE start_dispatch_id::text = $1 AND agent_id = $2 AND runtime_id = $3
			  AND status = 'pending'`, acknowledgement.StartDispatchID, agentID, runtimeID, acknowledgement.QueueState); err != nil {
			return fmt.Errorf("record accepted Runner launch intent: %w", err)
		}
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
		workspaceID, agentID, _, err := h.runnerActivityAgentScope(ctx, identity.WorkspaceID, session.AgentID)
		if err != nil {
			return err
		}
		command, err := h.DB.Exec(ctx, `
			UPDATE agent_activity_launch
			SET provider_session_id = $6, provider_turn_id = $7, runtime_generation = $8, updated_at = now()
			WHERE workspace_id = $1 AND agent_id = $2 AND daemon_id = $3
			  AND daemon_instance_id = $4 AND launch_id = $5 AND status IN ('accepted', 'active')`,
			workspaceID, agentID, identity.DaemonID, daemonInstanceID, session.LaunchID,
			session.ProviderSessionID, session.TurnID, session.RuntimeGeneration)
		if err != nil {
			return fmt.Errorf("persist Runner session: %w", err)
		}
		if command.RowsAffected() != 1 {
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
	if snapshot.DaemonInstanceID != daemonInstanceID {
		return errors.New("Activity daemon instance does not match current Runner")
	}
	workspaceID, agentID, runtimeID, err := h.runnerActivityAgentScope(ctx, identity.WorkspaceID, snapshot.AgentID)
	if err != nil {
		return err
	}
	fingerprint, err := runnerActivityFingerprint(activity)
	if err != nil {
		return err
	}
	command, err := h.DB.Exec(ctx, `
		UPDATE agent_activity_launch
		SET last_client_sequence = $6, last_producer_fact_id = $7, last_activity_fingerprint = $8, updated_at = now()
		WHERE workspace_id = $1 AND agent_id = $2
		  AND daemon_id = $3 AND daemon_instance_id = $4 AND launch_id = $5
		  AND status = 'active'
		  AND (last_client_sequence < $6 OR (last_client_sequence = $6 AND last_producer_fact_id = $7 AND last_activity_fingerprint = $8))
		  AND (
			($9 = '' AND NOT EXISTS (
				SELECT 1 FROM agent_activity_probe p WHERE p.workspace_id = $1 AND p.agent_id = $2
			)) OR
			($9 <> '' AND EXISTS (
				SELECT 1 FROM agent_activity_probe p WHERE p.workspace_id = $1 AND p.agent_id = $2
					AND p.daemon_id = $3 AND p.daemon_instance_id = $4 AND p.launch_id = $5 AND p.probe_id = $9
			))
		  )`,
		workspaceID, agentID, identity.DaemonID, daemonInstanceID, snapshot.LaunchID, snapshot.ClientSequence, snapshot.ProducerFactID, fingerprint, snapshot.ProbeID)
	if err != nil {
		return fmt.Errorf("advance Runner Activity fence: %w", err)
	}
	if command.RowsAffected() != 1 {
		return errors.New("stale or unauthorized Runner Activity")
	}
	if snapshot.ProbeID != "" {
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
		JOIN agent_activity_launch l ON l.workspace_id = s.workspace_id AND l.agent_id = s.agent_id
		LEFT JOIN agent_activity_probe p ON p.workspace_id = s.workspace_id AND p.agent_id = s.agent_id
		WHERE s.activity_kind IN ('working', 'thinking') AND s.received_at <= $1
			AND l.status = 'active' AND p.agent_id IS NULL`, now.Add(-runnerActivityStaleAfter))
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
		command, err := h.DB.Exec(ctx, `
			INSERT INTO agent_activity_probe (workspace_id, agent_id, daemon_id, daemon_instance_id, launch_id, probe_id, sent_at, deadline_at)
			SELECT $1, $2, $3, $4, $5, $6, $7, $8
			WHERE EXISTS (
				SELECT 1 FROM agent_activity_snapshot s JOIN agent_activity_launch l ON l.workspace_id = s.workspace_id AND l.agent_id = s.agent_id
				WHERE s.workspace_id = $1 AND s.agent_id = $2 AND s.daemon_id = $3 AND s.daemon_instance_id = $4 AND s.launch_id = $5
					AND s.activity_kind IN ('working', 'thinking') AND s.received_at <= $9 AND l.status = 'active'
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
		rows, err := tx.Query(ctx, `
			UPDATE agent_activity_launch
			SET status = 'inactive', updated_at = now()
			WHERE workspace_id = $1 AND daemon_id = $2 AND daemon_instance_id = $3
			  AND status = 'active'
			RETURNING agent_id`, workspaceID, identity.DaemonID, daemonInstanceID)
		if err != nil {
			return fmt.Errorf("deactivate disconnected Runner launches: %w", err)
		}
		for rows.Next() {
			var agentID pgtype.UUID
			if err := rows.Scan(&agentID); err != nil {
				rows.Close()
				return fmt.Errorf("scan disconnected Runner launch: %w", err)
			}
			presenceAgentIDs = append(presenceAgentIDs, agentID)
		}
		if err := rows.Err(); err != nil {
			rows.Close()
			return fmt.Errorf("iterate disconnected Runner launches: %w", err)
		}
		rows.Close()

		activityAgentIDs := make([]pgtype.UUID, 0)
		rows, err = tx.Query(ctx, `
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
		command, err := h.DB.Exec(ctx, `UPDATE agent_activity_launch SET status = 'inactive', updated_at = now()
			WHERE workspace_id = $1 AND agent_id = $2 AND daemon_id = $3 AND daemon_instance_id = $4 AND launch_id = $5 AND status = 'active'`, workspaceID, agentID, daemonID, daemonInstanceID, launchID)
		if err != nil {
			return fmt.Errorf("deactivate stale Runner launch: %w", err)
		}
		if _, err := h.DB.Exec(ctx, `UPDATE agent_activity_snapshot SET activity_kind = 'offline', detail_kind = 'machine_disconnected', received_at = now()
			WHERE workspace_id = $1 AND agent_id = $2 AND daemon_id = $3 AND daemon_instance_id = $4 AND launch_id = $5`, workspaceID, agentID, daemonID, daemonInstanceID, launchID); err != nil {
			return fmt.Errorf("project Runner disconnect: %w", err)
		}
		if _, err := h.DB.Exec(ctx, `DELETE FROM agent_activity_probe WHERE workspace_id = $1 AND agent_id = $2 AND daemon_id = $3 AND daemon_instance_id = $4 AND launch_id = $5`, workspaceID, agentID, daemonID, daemonInstanceID, launchID); err != nil {
			return fmt.Errorf("clear stale Runner Activity probe: %w", err)
		}
		if command.RowsAffected() == 1 {
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

func runnerActivityFingerprint(activity protocol.AgentActivityPayload) (string, error) {
	// ProbeID correlates a server liveness request; it is not producer fact
	// content. A valid probe reply intentionally reuses the last observation's
	// sequence and fact ID, so including ProbeID here would reject that reply as
	// a conflicting same-sequence fact.
	activity.Snapshot.ProbeID = ""
	encoded, err := json.Marshal(activity)
	if err != nil {
		return "", fmt.Errorf("encode Runner Activity fact: %w", err)
	}
	digest := sha256.Sum256(encoded)
	return fmt.Sprintf("%x", digest[:]), nil
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
	err := h.DB.QueryRow(ctx, `
		SELECT daemon_instance_id, launch_id, client_sequence, producer_fact_id,
			observed_at, activity_kind, detail_kind, probe_id, process_instance_id
		FROM agent_activity_snapshot
		WHERE workspace_id = $1 AND agent_id = $2`, workspaceID, agentID).Scan(
		&snapshot.DaemonInstanceID, &snapshot.LaunchID, &snapshot.ClientSequence, &snapshot.ProducerFactID,
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
	response.Summary = &summary

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
