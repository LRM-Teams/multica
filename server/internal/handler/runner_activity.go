package handler

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
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

// HandleWorkspaceRunnerFrame is dormant until the hard cut. The daemonws hub
// invokes it only for a current ready Runner; this method adds durable Agent,
// launch, daemon-instance, fact, and sequence fencing before persistence.
func (h *Handler) HandleWorkspaceRunnerFrame(ctx context.Context, identity daemonws.ClientIdentity, daemonInstanceID, eventType string, raw json.RawMessage) error {
	if h == nil || h.DB == nil {
		return errors.New("handler database is unavailable")
	}
	switch eventType {
	case protocol.EventAgentStatus:
		var status protocol.AgentStatusPayload
		if err := json.Unmarshal(raw, &status); err != nil {
			return fmt.Errorf("decode Runner status: %w", err)
		}
		return h.recordRunnerLaunch(ctx, identity, daemonInstanceID, status)
	case protocol.EventAgentActivity:
		var activity protocol.AgentActivityPayload
		if err := json.Unmarshal(raw, &activity); err != nil {
			return fmt.Errorf("decode Runner Activity: %w", err)
		}
		return h.recordRunnerActivity(ctx, identity, daemonInstanceID, activity)
	default:
		return nil
	}
}

func (h *Handler) recordRunnerLaunch(ctx context.Context, identity daemonws.ClientIdentity, daemonInstanceID string, status protocol.AgentStatusPayload) error {
	if err := status.Validate(); err != nil {
		return err
	}
	workspaceID, agentID, runtimeID, err := h.runnerActivityAgentScope(ctx, identity.WorkspaceID, status.AgentID)
	if err != nil {
		return err
	}
	_, err = h.DB.Exec(ctx, `
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
		updated_at = now()`, workspaceID, agentID, runtimeID, identity.DaemonID, daemonInstanceID, status.LaunchID, status.Status)
	if err != nil {
		return fmt.Errorf("upsert Runner launch: %w", err)
	}
	return nil
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
	defer rows.Close()
	for rows.Next() {
		var workspaceID, agentID pgtype.UUID
		var daemonID, daemonInstanceID, launchID string
		if err := rows.Scan(&workspaceID, &agentID, &daemonID, &daemonInstanceID, &launchID); err != nil {
			return fmt.Errorf("scan stale Runner Activity: %w", err)
		}
		probeID := randomID()
		command, err := h.DB.Exec(ctx, `
			INSERT INTO agent_activity_probe (workspace_id, agent_id, daemon_id, daemon_instance_id, launch_id, probe_id, sent_at, deadline_at)
			SELECT $1, $2, $3, $4, $5, $6, $7, $8
			WHERE EXISTS (
				SELECT 1 FROM agent_activity_snapshot s JOIN agent_activity_launch l ON l.workspace_id = s.workspace_id AND l.agent_id = s.agent_id
				WHERE s.workspace_id = $1 AND s.agent_id = $2 AND s.daemon_id = $3 AND s.daemon_instance_id = $4 AND s.launch_id = $5
					AND s.activity_kind IN ('working', 'thinking') AND s.received_at <= $9 AND l.status = 'active'
			)
			ON CONFLICT (workspace_id, agent_id) DO NOTHING`, workspaceID, agentID, daemonID, daemonInstanceID, launchID, probeID, now, now.Add(runnerActivityProbeWindow), now.Add(-runnerActivityStaleAfter))
		if err != nil {
			return fmt.Errorf("record Runner Activity probe: %w", err)
		}
		if command.RowsAffected() != 1 {
			continue
		}
		if h.DaemonHub == nil || !h.DaemonHub.NotifyWorkspaceRunner(daemonID, util.UUIDToString(workspaceID), protocol.EventAgentActivityProbe, protocol.AgentActivityProbePayload{AgentID: util.UUIDToString(agentID), LaunchID: launchID, ProbeID: probeID}) {
			if err := h.markRunnerActivityDisconnected(ctx, workspaceID, agentID, daemonID, daemonInstanceID, launchID); err != nil {
				return err
			}
		}
	}
	return rows.Err()
}

func (h *Handler) timeoutRunnerActivityProbes(ctx context.Context, now time.Time) error {
	rows, err := h.DB.Query(ctx, `SELECT workspace_id, agent_id, daemon_id, daemon_instance_id, launch_id FROM agent_activity_probe WHERE deadline_at <= $1`, now)
	if err != nil {
		return fmt.Errorf("list timed out Runner Activity probes: %w", err)
	}
	defer rows.Close()
	for rows.Next() {
		var workspaceID, agentID pgtype.UUID
		var daemonID, daemonInstanceID, launchID string
		if err := rows.Scan(&workspaceID, &agentID, &daemonID, &daemonInstanceID, &launchID); err != nil {
			return fmt.Errorf("scan timed out Runner Activity probe: %w", err)
		}
		if h.DaemonHub != nil {
			h.DaemonHub.CloseWorkspaceRunner(daemonID, util.UUIDToString(workspaceID), daemonInstanceID)
		}
		if err := h.markRunnerActivityDisconnected(ctx, workspaceID, agentID, daemonID, daemonInstanceID, launchID); err != nil {
			return err
		}
	}
	return rows.Err()
}

func (h *Handler) markRunnerActivityDisconnected(ctx context.Context, workspaceID, agentID pgtype.UUID, daemonID, daemonInstanceID, launchID string) error {
	if _, err := h.DB.Exec(ctx, `UPDATE agent_activity_launch SET status = 'inactive', updated_at = now()
		WHERE workspace_id = $1 AND agent_id = $2 AND daemon_id = $3 AND daemon_instance_id = $4 AND launch_id = $5`, workspaceID, agentID, daemonID, daemonInstanceID, launchID); err != nil {
		return fmt.Errorf("deactivate stale Runner launch: %w", err)
	}
	if _, err := h.DB.Exec(ctx, `UPDATE agent_activity_snapshot SET activity_kind = 'offline', detail_kind = 'machine_disconnected', received_at = now()
		WHERE workspace_id = $1 AND agent_id = $2 AND daemon_id = $3 AND daemon_instance_id = $4 AND launch_id = $5`, workspaceID, agentID, daemonID, daemonInstanceID, launchID); err != nil {
		return fmt.Errorf("project Runner disconnect: %w", err)
	}
	if _, err := h.DB.Exec(ctx, `DELETE FROM agent_activity_probe WHERE workspace_id = $1 AND agent_id = $2 AND daemon_id = $3 AND daemon_instance_id = $4 AND launch_id = $5`, workspaceID, agentID, daemonID, daemonInstanceID, launchID); err != nil {
		return fmt.Errorf("clear stale Runner Activity probe: %w", err)
	}
	if h.Bus != nil {
		projected, err := h.runnerActivityPresentation(ctx, workspaceID, agentID)
		if err != nil {
			return err
		}
		h.publish(protocol.EventAgentActivity, util.UUIDToString(workspaceID), "system", "", RunnerActivityRealtimePayload{AgentID: util.UUIDToString(agentID), Activity: projected})
	}
	return nil
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

// GetRunnerActivity is the dormant Workspace-authorized presentation API used
// by the coordinated Activity cut. It intentionally has a distinct path from
// the historical /activity endpoints so no compatibility translation or dual
// representation is needed at activation.
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
	defer rows.Close()
	for rows.Next() {
		var id pgtype.UUID
		var entry protocol.AgentActivityEntry
		var occurredAt pgtype.Timestamptz
		if err := rows.Scan(&id, &entry.Kind, &entry.Body, &occurredAt); err != nil {
			return RunnerActivityResponse{}, fmt.Errorf("scan Runner Activity timeline: %w", err)
		}
		response.Timeline = append(response.Timeline, RunnerActivityTimelineResponseRow{
			ID:          util.UUIDToString(id),
			OccurredAt:  occurredAt.Time.UTC().Format(time.RFC3339Nano),
			TimelineRow: activityprojection.ProjectTimelineEntry(entry, summary),
		})
	}
	if err := rows.Err(); err != nil {
		return RunnerActivityResponse{}, fmt.Errorf("iterate Runner Activity timeline: %w", err)
	}
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
