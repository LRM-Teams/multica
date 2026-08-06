package handler

import (
	"context"
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
	command, err := h.DB.Exec(ctx, `
		UPDATE agent_activity_launch
		SET last_client_sequence = $6, last_producer_fact_id = $7, updated_at = now()
		WHERE workspace_id = $1 AND agent_id = $2
		  AND daemon_id = $3 AND daemon_instance_id = $4 AND launch_id = $5
		  AND status = 'active'
		  AND (last_client_sequence < $6 OR (last_client_sequence = $6 AND last_producer_fact_id = $7))`,
		workspaceID, agentID, identity.DaemonID, daemonInstanceID, snapshot.LaunchID, snapshot.ClientSequence, snapshot.ProducerFactID)
	if err != nil {
		return fmt.Errorf("advance Runner Activity fence: %w", err)
	}
	if command.RowsAffected() != 1 {
		return errors.New("stale or unauthorized Runner Activity")
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
