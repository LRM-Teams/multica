package handler

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"sort"

	"github.com/jackc/pgx/v5/pgtype"
	"github.com/multica-ai/multica/server/internal/daemonws"
	"github.com/multica-ai/multica/server/pkg/protocol"
)

type runnerDesiredLaunch struct {
	agentID   string
	runtimeID string
	sessionID string
}

type runnerObservedLaunch struct {
	agentID   string
	runtimeID string
	status    string
}

type runnerReconcileAction struct {
	eventType string
	payload   any
}

// reduceRunnerLaunches is the pure desired-vs-observed launch reducer. Its
// interface contains only Raft's placement and residency facts; database,
// websocket, setup, reconnect, and runtime-update details stay in adapters.
func reduceRunnerLaunches(desired []runnerDesiredLaunch, observed []runnerObservedLaunch) []runnerReconcileAction {
	desiredByAgent := make(map[string]runnerDesiredLaunch, len(desired))
	for _, launch := range desired {
		desiredByAgent[launch.agentID] = launch
	}
	observedByAgent := make(map[string]runnerObservedLaunch, len(observed))
	for _, launch := range observed {
		observedByAgent[launch.agentID] = launch
	}
	agentIDs := make(map[string]struct{}, len(desired)+len(observed))
	for agentID := range desiredByAgent {
		agentIDs[agentID] = struct{}{}
	}
	for agentID := range observedByAgent {
		agentIDs[agentID] = struct{}{}
	}
	ordered := make([]string, 0, len(agentIDs))
	for agentID := range agentIDs {
		ordered = append(ordered, agentID)
	}
	sort.Strings(ordered)
	actions := make([]runnerReconcileAction, 0, len(ordered)*2)
	for _, agentID := range ordered {
		want, wanted := desiredByAgent[agentID]
		have, observed := observedByAgent[agentID]
		// ACK / accepted is not a live process, but it still occupies the
		// server launch fence. A mismatched accepted start must therefore be
		// stopped before the replacement can be admitted. A matching accepted
		// start is re-driven below so an upgrade successor can finish startup.
		occupiesFence := observed && (have.status == "accepted" || have.status == protocol.AgentStatusActive)
		running := occupiesFence && have.status == protocol.AgentStatusActive
		mismatched := occupiesFence && (!wanted || have.runtimeID != want.runtimeID)
		if mismatched {
			actions = append(actions, runnerReconcileAction{eventType: protocol.EventDaemonAgentStop, payload: protocol.AgentStopPayload{AgentID: have.agentID}})
		}
		// Runtime replacement is two phase: stop the observed launch first and
		// dispatch the desired start only after an inactive report removes it
		// from the observed set on the next reconcile.
		if wanted && !mismatched && !running {
			actions = append(actions, runnerReconcileAction{eventType: protocol.EventDaemonAgentStart, payload: protocol.AgentStartPayload{AgentID: want.agentID, RuntimeID: want.runtimeID, Config: protocol.AgentStartConfig{SessionID: want.sessionID}}})
		}
	}
	if len(actions) == 0 {
		return nil
	}
	return actions
}

// reconcileWorkspaceDaemonLaunches converges setup, reconnect, daemon restart,
// and runtime moves through one path.
func (h *Handler) reconcileWorkspaceDaemonLaunches(ctx context.Context, identity daemonws.ClientIdentity) error {
	if h == nil || h.DB == nil || h.DaemonHub == nil {
		return errors.New("Workspace Runner reconcile dependencies are unavailable")
	}
	if !h.DaemonHub.WorkspaceDaemonSupportsCapability(identity.DaemonID, identity.WorkspaceID, protocol.DaemonCapabilityWorkspaceDaemonAgentProcess) {
		return nil
	}
	desired, err := h.loadRunnerDesiredLaunches(ctx, identity)
	if err != nil {
		return err
	}
	// Observed residency is the current Computer process, not a durable row.
	// A persisted active launch from a previous daemonInstanceID is leftover
	// cache; treating it as running would skip agent:start after restart.
	daemonInstanceID, live := h.DaemonHub.CurrentWorkspaceDaemonInstance(identity.DaemonID, identity.WorkspaceID)
	if !live {
		return errors.New("current Workspace Runner unavailable during launch reconcile")
	}
	skip := h.restartAgentsOnActiveOperation()
	eligibleDesired := desired[:0]
	for _, launch := range desired {
		if !skip[launch.agentID] {
			eligibleDesired = append(eligibleDesired, launch)
		}
	}
	desired = eligibleDesired
	observed := make([]runnerObservedLaunch, 0)
	for _, obs := range h.observations().listInstance(identity.WorkspaceID, identity.DaemonID, daemonInstanceID) {
		if skip[obs.agentID] {
			continue
		}
		if obs.status != "accepted" && obs.status != protocol.AgentStatusActive {
			continue
		}
		observed = append(observed, runnerObservedLaunch{agentID: obs.agentID, runtimeID: obs.runtimeID, status: obs.status})
	}
	for _, action := range reduceRunnerLaunches(desired, observed) {
		if !h.DaemonHub.NotifyWorkspaceDaemon(identity.DaemonID, identity.WorkspaceID, action.eventType, action.payload) {
			return errors.New("current Workspace Runner unavailable during launch reconcile")
		}
		slog.Debug("Workspace Runner launch reconciled", "workspace_id", identity.WorkspaceID, "daemon_id", identity.DaemonID, "event_type", action.eventType, "outcome", "sent", "reason", "desired_running_mismatch")
	}
	return nil
}

func (h *Handler) loadRunnerDesiredLaunches(ctx context.Context, identity daemonws.ClientIdentity) ([]runnerDesiredLaunch, error) {
	rows, err := h.DB.Query(ctx, `
		SELECT desired.id::text, desired.runtime_id::text,
		       COALESCE(desired.provider_session_id, '')
		FROM agent desired
		JOIN agent_runtime runtime ON runtime.id = desired.runtime_id
		WHERE desired.workspace_id::text = $1 AND desired.archived_at IS NULL
		  AND runtime.daemon_id = $2
		ORDER BY desired.id`, identity.WorkspaceID, identity.DaemonID)
	if err != nil {
		return nil, fmt.Errorf("load desired Runner launches: %w", err)
	}
	desired := make([]runnerDesiredLaunch, 0)
	for rows.Next() {
		var launch runnerDesiredLaunch
		if err := rows.Scan(&launch.agentID, &launch.runtimeID, &launch.sessionID); err != nil {
			rows.Close()
			return nil, fmt.Errorf("scan desired Runner launch: %w", err)
		}
		desired = append(desired, launch)
	}
	if err := rows.Err(); err != nil {
		rows.Close()
		return nil, err
	}
	rows.Close()
	return desired, nil
}

func (h *Handler) restartAgentsOnActiveOperation() map[string]bool {
	skip := map[string]bool{}
	for _, agentID := range h.restarts().agentIDs() {
		skip[agentID] = true
	}
	return skip
}

func (h *Handler) reconcileConnectedRuntime(ctx context.Context, workspaceID string, runtimeID pgtype.UUID) {
	h.reconcileConnectedRuntimes(ctx, workspaceID, runtimeID)
}

// reconcileConnectedRuntimes resolves mutable Runtime placement into the
// immutable Workspace Runner identity and deduplicates by daemon. A move
// within one Computer therefore emits one stop/start sequence; a move across
// Computers reconciles the old and new Runners independently.
func (h *Handler) reconcileConnectedRuntimes(ctx context.Context, workspaceID string, runtimeIDs ...pgtype.UUID) {
	if h == nil || h.DB == nil || h.DaemonHub == nil {
		return
	}
	identities := make(map[string]daemonws.ClientIdentity)
	for _, runtimeID := range runtimeIDs {
		if !runtimeID.Valid {
			continue
		}
		var daemonID string
		if err := h.DB.QueryRow(ctx, `SELECT COALESCE(daemon_id::text, '') FROM agent_runtime WHERE id = $1`, runtimeID).Scan(&daemonID); err != nil || daemonID == "" {
			continue
		}
		if identity, connected := h.DaemonHub.WorkspaceDaemonIdentity(daemonID, workspaceID); connected {
			identities[daemonID] = identity
		}
	}
	daemonIDs := make([]string, 0, len(identities))
	for daemonID := range identities {
		daemonIDs = append(daemonIDs, daemonID)
	}
	sort.Strings(daemonIDs)
	for _, daemonID := range daemonIDs {
		if err := h.reconcileWorkspaceDaemonLaunches(ctx, identities[daemonID]); err != nil {
			slog.Warn("Workspace Runner launch reconcile after placement change failed", "workspace_id", workspaceID, "daemon_id", daemonID, "error", err)
		}
	}
}
