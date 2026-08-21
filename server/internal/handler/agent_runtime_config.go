package handler

import (
	"net/http"
	"regexp"
	"strings"

	"time"

	"github.com/go-chi/chi/v5"
	"github.com/jackc/pgx/v5/pgtype"

	db "github.com/multica-ai/multica/server/pkg/db/generated"
)

// AgentRuntimeConfigResponse is the assembled answer to "what is this agent
// configured to run on", served by GET /api/agents/{id}/runtime-config.
//
// It exists because the inspector used to compose this row itself: look the
// agent's runtime_id up in GET /api/runtimes, take the machine name off the
// runtime row, take connectivity off either that row or the computers list.
// That composition was wrong twice over. GET /api/runtimes means "the
// computers I can manage" and drops another member's private runtime with no
// owner/admin override, so the lookup missed and every other viewer was told
// the agent had no computer at all. And the machine's name and liveness are
// Computer-level facts (the daemon), not runtime-level ones — the runtime is
// the provider process the daemon core hosts.
//
// So the server composes it, along the real hierarchy: Computer (daemon) →
// runtime (provider). Visibility only exists at the runtime level, and it
// gates the management plane, not the fact that an agent runs there — so this
// endpoint is scoped by "can you see the agent", nothing else. Choosing a
// runtime is a separate question with a separate source (GET /api/computers).
type AgentRuntimeConfigResponse struct {
	// Computer is nil when the agent has no runtime bound, or when its
	// runtime's machine is no longer bound to this workspace.
	Computer *AgentRuntimeConfigComputer `json:"computer"`
	Runtime  *AgentRuntimeConfigRuntime  `json:"runtime"`
	// Model and Thinking are the agent's own stored values, echoed as-is.
	// Empty means "unset — the provider decides at launch"; the server cannot
	// resolve what that will be, because the model catalog is reported by the
	// daemon (ReportModelListResult), not known statically here.
	Model    string `json:"model,omitempty"`
	Thinking string `json:"thinking,omitempty"`
}

type AgentRuntimeConfigComputer struct {
	DaemonID string `json:"daemon_id"`
	// Name is already resolved for display — callers must not re-derive it.
	Name string `json:"name"`
	// Connected is WS truth: a live DaemonCore Workspace Runner socket, the
	// same source the computers list uses. Runtime-level status/last_seen is
	// deliberately absent — liveness is a Computer-level fact.
	Connected  bool   `json:"connected"`
	CLIVersion string `json:"cli_version,omitempty"`
	OS         string `json:"os,omitempty"`
	OwnerID    string `json:"owner_id,omitempty"`
}

type AgentRuntimeConfigRuntime struct {
	ID       string `json:"id"`
	Provider string `json:"provider"`
}

// hostnameInRuntimeName pulls "s144" out of a legacy "Cursor (s144)" runtime
// name. Mirrors splitRuntimeName on the client, which this endpoint replaces
// as the place that decision is made.
var hostnameInRuntimeName = regexp.MustCompile(`^(.+?)\s+\(([^)]+)\)$`)

// resolveComputerName picks the machine label in the order a human would:
// the Computer's own device_name first (the entity that owns the identity),
// then the runtime's user-set label, then a hostname still glued into a
// legacy runtime name, and finally a short daemon id. It never returns the
// "Provider (host)" string as-is — that names a code agent, not a machine.
func resolveComputerName(deviceName, runtimeDisplayName, runtimeName, daemonID string) string {
	if name := strings.TrimSpace(deviceName); name != "" {
		return name
	}
	if name := strings.TrimSpace(runtimeDisplayName); name != "" {
		return name
	}
	if m := hostnameInRuntimeName.FindStringSubmatch(strings.TrimSpace(runtimeName)); m != nil {
		if host := strings.TrimSpace(m[2]); host != "" {
			return host
		}
	}
	if name := strings.TrimSpace(runtimeName); name != "" {
		return name
	}
	if id := strings.TrimSpace(daemonID); id != "" {
		if len(id) > 8 {
			return id[:8]
		}
		return id
	}
	return ""
}

func (h *Handler) GetAgentRuntimeConfig(w http.ResponseWriter, r *http.Request) {
	agent, ok := h.loadAgentForUser(w, r, chi.URLParam(r, "id"))
	if !ok {
		return
	}

	resp := AgentRuntimeConfigResponse{
		Model:    agent.Model.String,
		Thinking: agent.ThinkingLevel.String,
	}
	if !agent.RuntimeID.Valid {
		writeJSON(w, http.StatusOK, resp)
		return
	}

	runtime, err := h.Queries.GetAgentRuntime(r.Context(), agent.RuntimeID)
	if err != nil {
		// The binding points at a runtime that no longer exists; the agent
		// genuinely has no computer rather than one we failed to load.
		writeJSON(w, http.StatusOK, resp)
		return
	}
	resp.Runtime = &AgentRuntimeConfigRuntime{
		ID:       uuidToString(runtime.ID),
		Provider: runtime.Provider,
	}

	daemonID := strings.TrimSpace(runtime.DaemonID.String)
	if daemonID == "" {
		writeJSON(w, http.StatusOK, resp)
		return
	}

	workspaceID := uuidToString(agent.WorkspaceID)
	var deviceName, osName, cliVersion, ownerID string
	var lastSeen pgtype.Timestamptz
	// Computer identity comes from the Computer, not from the runtime row.
	// A revoked binding takes its agents with it (workspace_revoke archives
	// them and drops the runtimes), so a live agent always has its row here.
	err = h.DB.QueryRow(r.Context(), `
SELECT COALESCE(o.device_name, ''), COALESCE(o.os, ''), COALESCE(o.cli_version, ''),
       b.user_id::text, h.last_seen_at
FROM computer_workspace_bindings b
LEFT JOIN daemon_heartbeat h
  ON h.workspace_id = b.workspace_id AND h.daemon_id = b.daemon_id
LEFT JOIN computers o
  ON o.id = b.daemon_id
WHERE b.workspace_id = $1 AND b.daemon_id = $2 AND b.active = TRUE`,
		parseUUID(workspaceID), daemonID).
		Scan(&deviceName, &osName, &cliVersion, &ownerID, &lastSeen)
	if err != nil {
		// No active binding: the machine is not part of this workspace
		// anymore. Report the runtime without a computer rather than
		// inventing one.
		writeJSON(w, http.StatusOK, resp)
		return
	}

	resp.Computer = &AgentRuntimeConfigComputer{
		DaemonID:   daemonID,
		Name:       resolveComputerName(deviceName, runtime.DisplayName, runtime.Name, daemonID),
		Connected:  h.computerConnectedByRunner(daemonID, workspaceID, &db.DaemonHeartbeat{LastSeenAt: lastSeen}, time.Now()),
		CLIVersion: cliVersion,
		OS:         osName,
		OwnerID:    ownerID,
	}
	writeJSON(w, http.StatusOK, resp)
}
