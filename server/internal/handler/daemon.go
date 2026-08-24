package handler

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/multica-ai/multica/server/internal/analytics"
	"github.com/multica-ai/multica/server/internal/auth"
	"github.com/multica-ai/multica/server/internal/daemonws"
	"github.com/multica-ai/multica/server/internal/logger"
	"github.com/multica-ai/multica/server/internal/messageparts"
	obsmetrics "github.com/multica-ai/multica/server/internal/metrics"
	"github.com/multica-ai/multica/server/internal/middleware"
	"github.com/multica-ai/multica/server/internal/util"
	db "github.com/multica-ai/multica/server/pkg/db/generated"
	"github.com/multica-ai/multica/server/pkg/protocol"
	"github.com/multica-ai/multica/server/pkg/redact"
)

const chatResumeRecentMessageLimit = 10
const daemonRegisterTokenTTL = 24 * time.Hour

var errInvalidTaskMessageSince = errors.New("invalid since parameter")

// ---------------------------------------------------------------------------
// Daemon workspace ownership helpers
// ---------------------------------------------------------------------------

// requireDaemonWorkspaceAccess verifies the caller has access to the given workspace.
// For daemon tokens (mdt_), compares the token's workspace ID directly.
// For PAT/JWT fallback, verifies user membership in the workspace.
func (h *Handler) requireDaemonWorkspaceAccess(w http.ResponseWriter, r *http.Request, workspaceID string) bool {
	if workspaceID == "" {
		writeError(w, http.StatusNotFound, "not found")
		return false
	}

	// Daemon token: workspace must match.
	if daemonWsID := middleware.DaemonWorkspaceIDFromContext(r.Context()); daemonWsID != "" {
		if daemonWsID != workspaceID {
			writeError(w, http.StatusNotFound, "not found")
			return false
		}
		return true
	}

	// PAT/JWT fallback: check membership cache before hitting DB.
	userID := requestUserID(r)
	if userID != "" {
		if h.MembershipCache.Get(r.Context(), userID, workspaceID) {
			return true
		}
	}

	_, ok := h.requireWorkspaceMember(w, r, workspaceID, "not found")
	if ok && userID != "" {
		h.MembershipCache.Set(r.Context(), userID, workspaceID)
	}
	return ok
}

// requireDaemonRuntimeAccess looks up a runtime and verifies the caller owns its workspace.
//
// Only pgx.ErrNoRows is treated as a real "runtime gone" 404 — the daemon uses
// that response to drop the stale runtime from its in-memory map and re-register,
// so collapsing transient DB errors into the same 404 would force the daemon to
// self-cleanup on a hiccup. Other DB errors become 500.
func (h *Handler) requireDaemonRuntimeAccess(w http.ResponseWriter, r *http.Request, runtimeID string) (db.AgentRuntime, bool) {
	runtimeUUID, ok := parseUUIDOrBadRequest(w, runtimeID, "runtime_id")
	if !ok {
		return db.AgentRuntime{}, false
	}
	rt, err := h.Queries.GetAgentRuntime(r.Context(), runtimeUUID)
	if err != nil {
		if isNotFound(err) {
			writeError(w, http.StatusNotFound, "runtime not found")
			return db.AgentRuntime{}, false
		}
		slog.Warn("get agent runtime failed", "runtime_id", runtimeID, "error", err)
		writeError(w, http.StatusInternalServerError, "failed to load runtime")
		return db.AgentRuntime{}, false
	}
	if !h.requireDaemonWorkspaceAccess(w, r, uuidToString(rt.WorkspaceID)) {
		return db.AgentRuntime{}, false
	}
	return rt, true
}

// requireDaemonTaskAccess looks up a task and verifies the caller owns its workspace.
func (h *Handler) requireDaemonTaskAccess(w http.ResponseWriter, r *http.Request, taskID string) (db.AgentInboxEvent, bool) {
	task, _, ok := h.requireDaemonTaskAccessWithWorkspace(w, r, taskID)
	return task, ok
}

// requireDaemonTaskAccessWithWorkspace is the workspace-aware variant of
// requireDaemonTaskAccess. It returns the resolved workspace ID alongside
// the task row so callers that need to forward workspace_id into
// taskToResponse (powering RelativeWorkDir) don't have to repeat the
// ResolveTaskWorkspaceID lookup. The two helpers share their entire
// implementation; the simpler one is preserved for ergonomic call sites
// that genuinely don't need workspace_id.
func (h *Handler) requireDaemonTaskAccessWithWorkspace(w http.ResponseWriter, r *http.Request, taskID string) (db.AgentInboxEvent, string, bool) {
	taskUUID, ok := parseUUIDOrBadRequest(w, taskID, "task_id")
	if !ok {
		return db.AgentInboxEvent{}, "", false
	}
	task, err := h.Queries.GetAgentTask(r.Context(), taskUUID)
	if err != nil {
		// Only treat pgx.ErrNoRows as a real "task gone" signal — daemon
		// uses this 404 to interrupt the running agent, so a transient DB
		// error must not be reported as a deletion.
		if isNotFound(err) {
			writeError(w, http.StatusNotFound, "task not found")
			return db.AgentInboxEvent{}, "", false
		}
		slog.Warn("get agent task failed", "task_id", taskID, "error", err)
		writeError(w, http.StatusInternalServerError, "failed to load task")
		return db.AgentInboxEvent{}, "", false
	}

	wsID := h.TaskService.ResolveTaskWorkspaceID(r.Context(), task)
	if wsID == "" {
		writeError(w, http.StatusNotFound, "task not found")
		return db.AgentInboxEvent{}, "", false
	}

	if !h.requireDaemonWorkspaceAccess(w, r, wsID) {
		return db.AgentInboxEvent{}, "", false
	}
	return task, wsID, true
}

// verifyDaemonWorkspaceAccess checks workspace access without writing an HTTP error.
// Used in loops where individual items may be skipped silently.
func (h *Handler) verifyDaemonWorkspaceAccess(r *http.Request, workspaceID string) bool {
	if workspaceID == "" {
		return false
	}
	if daemonWsID := middleware.DaemonWorkspaceIDFromContext(r.Context()); daemonWsID != "" {
		return daemonWsID == workspaceID
	}
	userID := requestUserID(r)
	if userID == "" {
		return false
	}
	if h.MembershipCache.Get(r.Context(), userID, workspaceID) {
		return true
	}
	_, err := h.getWorkspaceMember(r.Context(), userID, workspaceID)
	if err != nil {
		return false
	}
	h.MembershipCache.Set(r.Context(), userID, workspaceID)
	return true
}

// ---------------------------------------------------------------------------
// Daemon Registration & Heartbeat
// ---------------------------------------------------------------------------

type DaemonRegisterRequest struct {
	WorkspaceID string `json:"workspace_id"`
	DaemonID    string `json:"daemon_id"`
	// LegacyDaemonIDs lists prior hostname-derived daemon_ids this machine
	// may have registered under before switching to a persistent UUID. The
	// handler merges any matching runtime rows into the new row so agents
	// and tasks keep working without manual intervention.
	LegacyDaemonIDs []string `json:"legacy_daemon_ids"`
	DeviceName      string   `json:"device_name"`
	// MachineID is the OS-level machine fingerprint reported by the daemon
	// (e.g. /etc/machine-id). It is an attribute of the physical machine,
	// independent of daemon.id, and is the authoritative same-machine proof
	// used for identity-rebuild convergence (LRM-1570).
	MachineID         string                            `json:"machine_id,omitempty"`
	OS                string                            `json:"os"`
	CLIVersion        string                            `json:"cli_version"` // multica CLI version
	LaunchedBy        string                            `json:"launched_by"` // "desktop" when spawned by the Electron app
	Capabilities      []string                          `json:"capabilities"`
	SandboxInstanceID string                            `json:"sandbox_instance_id,omitempty"` // daemon-enabled env-dispatch sandboxes forward MULTICA_SANDBOX_INSTANCE_ID so the runtime row carries it for discovery
	UpdateObservation *protocol.DaemonUpdateObservation `json:"auto_update,omitempty"`
	// PinnedVersion mirrors the daemon's MULTICA_PINNED_VERSION (task #81);
	// empty means not pinned. Sent unconditionally on every register so
	// unpinning a machine clears the stale value server-side too.
	PinnedVersion string `json:"pinned_version,omitempty"`
	Runtimes      []struct {
		Name    string `json:"name"`
		Type    string `json:"type"`
		Version string `json:"version"` // agent CLI version (claude/codex)
		Status  string `json:"status"`
	} `json:"runtimes"`
}

type preparedDaemonRegisterToken struct {
	raw       string
	hash      string
	expiresAt time.Time
}

func (h *Handler) prepareDaemonRegisterToken(ctx context.Context, workspaceID pgtype.UUID, daemonID string) (preparedDaemonRegisterToken, error) {
	rawToken, err := auth.GenerateDaemonToken()
	if err != nil {
		return preparedDaemonRegisterToken{}, err
	}
	expiresAt := time.Now().Add(daemonRegisterTokenTTL)
	hash := auth.HashToken(rawToken)
	if _, err := h.Queries.CreateDaemonToken(ctx, db.CreateDaemonTokenParams{
		TokenHash:   hash,
		WorkspaceID: workspaceID,
		DaemonID:    daemonID,
		ExpiresAt:   pgtype.Timestamptz{Time: expiresAt, Valid: true},
	}); err != nil {
		return preparedDaemonRegisterToken{}, err
	}
	return preparedDaemonRegisterToken{raw: rawToken, hash: hash, expiresAt: expiresAt}, nil
}

func (h *Handler) cacheDaemonRegisterToken(ctx context.Context, token preparedDaemonRegisterToken, workspaceID pgtype.UUID, daemonID string) {
	h.DaemonTokenCache.Set(ctx, token.hash, auth.DaemonTokenIdentity{
		WorkspaceID: uuidToString(workspaceID),
		DaemonID:    daemonID,
	}, auth.TTLForExpiry(time.Now(), token.expiresAt))
}

func (h *Handler) issueDaemonRegisterToken(ctx context.Context, workspaceID pgtype.UUID, daemonID string) (string, time.Time, error) {
	token, err := h.prepareDaemonRegisterToken(ctx, workspaceID, daemonID)
	if err != nil {
		return "", time.Time{}, err
	}
	h.cacheDaemonRegisterToken(ctx, token, workspaceID, daemonID)
	return token.raw, token.expiresAt, nil
}

func (h *Handler) DaemonRegister(w http.ResponseWriter, r *http.Request) {
	var req DaemonRegisterRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid request body")
		return
	}

	req.WorkspaceID = strings.TrimSpace(req.WorkspaceID)
	req.DaemonID = strings.TrimSpace(req.DaemonID)
	req.DeviceName = strings.TrimSpace(req.DeviceName)

	if req.DaemonID == "" {
		writeError(w, http.StatusBadRequest, "daemon_id is required")
		return
	}
	if req.WorkspaceID == "" {
		writeError(w, http.StatusBadRequest, "workspace_id is required")
		return
	}
	if len(req.Runtimes) == 0 {
		writeError(w, http.StatusBadRequest, "at least one runtime is required")
		return
	}
	wsUUID, ok := parseUUIDOrBadRequest(w, req.WorkspaceID, "workspace_id")
	if !ok {
		return
	}
	req.WorkspaceID = uuidToString(wsUUID)

	// Verify workspace access and resolve the user who connected this Computer
	// to the Workspace. PAT/JWT requests carry that user directly. Daemon-token
	// reconnects recover the same user from the durable Computer binding below.
	var ownerID pgtype.UUID
	daemonTokenRequest := false
	if daemonWsID := middleware.DaemonWorkspaceIDFromContext(r.Context()); daemonWsID != "" {
		if daemonWsID != req.WorkspaceID {
			writeError(w, http.StatusNotFound, "workspace not found")
			return
		}
		daemonTokenRequest = true
	} else {
		member, ok := h.requireWorkspaceMember(w, r, req.WorkspaceID, "workspace not found")
		if !ok {
			return
		}
		ownerID = member.UserID
	}
	// Live ownership is the current connect socket. Register no longer
	// claims or fences a cloud Computer generation.

	// Registration and permanent removal share one workspace+daemon advisory
	// lock. Holding it across the tombstone check, runtime upserts, and token
	// issue closes the race where a heartbeat could check "not removed", wait
	// behind the delete, then recreate the runtime after deletion commits.
	registrationLock, err := h.TxStarter.Begin(r.Context())
	if err != nil {
		writeError(w, http.StatusInternalServerError, "failed to lock computer registration")
		return
	}
	defer registrationLock.Rollback(context.Background())
	if err := lockDaemonRegistration(r.Context(), registrationLock, req.WorkspaceID, req.DaemonID); err != nil {
		writeError(w, http.StatusInternalServerError, "failed to lock computer registration")
		return
	}
	registrationHandler := *h
	registrationHandler.Queries = h.Queries.WithTx(registrationLock)
	registrationHandler.DB = registrationLock
	// LRM-1570: ownership is machine-level. A member-registered Computer
	// establishes it here (computers + active binding) — the old
	// agent_runtime.owner_id write is gone, and every owner gate (visibility,
	// edit, restart, upgrade, period-brief collectors) now resolves the owner
	// from the active binding row.
	if ownerID.Valid && !daemonTokenRequest {
		if _, err := registrationHandler.DB.Exec(r.Context(), `
WITH owner AS (
    INSERT INTO computers (id, user_id)
    VALUES ($1, $2)
    ON CONFLICT (id) DO UPDATE SET user_id = computers.user_id
    WHERE computers.user_id = EXCLUDED.user_id
    RETURNING id
)
INSERT INTO computer_workspace_bindings (daemon_id, workspace_id, user_id, execution_token_hash, active, revoked_at)
SELECT $1, $3, $2, 'register-' || gen_random_uuid()::text, TRUE, NULL
  FROM owner
ON CONFLICT (daemon_id, workspace_id)
DO UPDATE SET user_id = EXCLUDED.user_id, active = TRUE, revoked_at = NULL
WHERE computer_workspace_bindings.user_id = EXCLUDED.user_id`, req.DaemonID, ownerID, wsUUID); err != nil {
			slog.Warn("register: establish machine ownership binding failed", "daemon", req.DaemonID, "error", err)
		}

	}

	if daemonTokenRequest {
		err := registrationLock.QueryRow(r.Context(), `
			SELECT user_id
			FROM computer_workspace_bindings
			WHERE daemon_id = $1 AND workspace_id = $2
			  AND active = TRUE AND revoked_at IS NULL
		`, req.DaemonID, wsUUID).Scan(&ownerID)
		if err != nil && !errors.Is(err, pgx.ErrNoRows) {
			writeError(w, http.StatusInternalServerError, "failed to resolve Computer connection owner")
			return
		}
	}

	tombstoned, err := registrationHandler.Queries.IsDaemonRegistrationTombstoned(r.Context(), db.IsDaemonRegistrationTombstonedParams{
		WorkspaceID: wsUUID,
		DaemonID:    strings.ToLower(req.DaemonID),
	})
	if err != nil {
		writeError(w, http.StatusInternalServerError, "failed to verify computer registration")
		return
	}
	if tombstoned {
		writeCodedError(w, http.StatusGone, "daemon_removed", "this computer was permanently removed from the workspace")
		return
	}

	ws, err := registrationHandler.Queries.GetWorkspace(r.Context(), wsUUID)
	if err != nil {
		writeError(w, http.StatusNotFound, "workspace not found")
		return
	}
	capabilities := normalizeDaemonCapabilities(req.Capabilities)
	if err := registrationHandler.registerDaemonUpdateObservation(r.Context(), wsUUID, req.DaemonID, req.UpdateObservation); err != nil {
		if errors.Is(err, errDaemonUpdateObservationConflict) {
			writeError(w, http.StatusConflict, "auto_update revision conflicts with stored payload")
			return
		}
		var validationErr *daemonUpdateObservationValidationError
		if errors.As(err, &validationErr) {
			writeError(w, http.StatusBadRequest, validationErr.Error())
			return
		}
		writeError(w, http.StatusInternalServerError, "failed to persist auto-update observation")
		return
	}

	// Persist the machine fingerprint on the Computer identity at register time
	// (not only via the WS ready path), so identity-rebuild convergence can
	// resolve the same-machine proof for this daemon on the very first
	// registration. Scoped to the owner; shared machines with multiple OS
	// users never mix identities.
	if mid := strings.TrimSpace(req.MachineID); mid != "" && ownerID.Valid {
		if _, err := registrationHandler.DB.Exec(r.Context(), `
INSERT INTO computers (id, user_id, machine_id)
VALUES ($1, $2, $3)
ON CONFLICT (id) DO UPDATE SET
    machine_id = CASE WHEN computers.user_id = EXCLUDED.user_id
                      THEN EXCLUDED.machine_id ELSE computers.machine_id END
`, req.DaemonID, ownerID, mid); err != nil {
			slog.Warn("persist machine_id on Computer identity failed", "daemon", req.DaemonID, "error", err)
		}
	}

	resp := make([]AgentRuntimeResponse, 0, len(req.Runtimes))
	type registeredRuntime struct {
		runtime  db.AgentRuntime
		provider string
		version  string
		inserted bool
	}
	registeredRuntimes := make([]registeredRuntime, 0, len(req.Runtimes))
	for _, runtime := range req.Runtimes {
		provider := strings.TrimSpace(runtime.Type)
		if provider == "" {
			provider = "unknown"
		}
		name := strings.TrimSpace(runtime.Name)
		if name == "" {
			name = provider
			if req.DeviceName != "" {
				name = fmt.Sprintf("%s (%s)", provider, req.DeviceName)
			}
		}
		deviceInfo := strings.TrimSpace(req.DeviceName)
		if runtime.Version != "" && deviceInfo != "" {
			deviceInfo = fmt.Sprintf("%s · %s", deviceInfo, runtime.Version)
		} else if runtime.Version != "" {
			deviceInfo = runtime.Version
		}
		status := "online"
		if runtime.Status == "offline" {
			status = "offline"
		}
		metadataMap := map[string]any{
			"version":      runtime.Version,
			"cli_version":  req.CLIVersion,
			"launched_by":  req.LaunchedBy,
			"capabilities": capabilities,
		}
		// Persist the structured device_name the daemon already sends so the
		// API can expose it without re-parsing the glued device_info string.
		if req.DeviceName != "" {
			metadataMap["device_name"] = req.DeviceName
		}
		if osName := strings.ToLower(strings.TrimSpace(req.OS)); osName != "" {
			metadataMap["os"] = osName
		}
		// machine_id is a Computer (machine) attribute, not a runtime one, but
		// recording it on each runtime row lets API consumers and future
		// convergence reads resolve same-machine provenance without a join.
		if mid := strings.TrimSpace(req.MachineID); mid != "" {
			metadataMap["machine_id"] = mid
		}
		// sandbox_instance_id is forwarded only by daemon-enabled env-dispatch
		// sandboxes; recording it on the runtime row lets env-dispatch discover
		// the online runtime by (workspace, daemon_id, sandbox_instance_id).
		if sid := strings.TrimSpace(req.SandboxInstanceID); sid != "" {
			metadataMap["sandbox_instance_id"] = sid
		}
		metadata, _ := json.Marshal(metadataMap)

		row, err := registrationHandler.Queries.UpsertAgentRuntime(r.Context(), db.UpsertAgentRuntimeParams{
			WorkspaceID:   wsUUID,
			DaemonID:      strToText(req.DaemonID),
			Name:          name,
			RuntimeMode:   "local",
			Provider:      provider,
			Status:        status,
			DeviceInfo:    deviceInfo,
			Metadata:      metadata,
			PinnedVersion: req.PinnedVersion,
		})
		if err != nil {
			if isReminderDaemonOutdatedError(err) {
				writeCodedError(w, http.StatusConflict, "daemon_outdated", "runtime must keep reminder support while active reminders exist")
				return
			}
			obsmetrics.RecordEvent(h.Analytics, h.Metrics, analytics.RuntimeFailed(
				uuidToString(ownerID),
				req.WorkspaceID,
				req.DaemonID,
				provider,
				"registration_failed",
				"db_error",
				true,
			))
			writeError(w, http.StatusInternalServerError, "failed to register runtime: "+err.Error())
			return
		}

		registered := db.AgentRuntime{
			ID:             row.ID,
			WorkspaceID:    row.WorkspaceID,
			DaemonID:       row.DaemonID,
			Name:           row.Name,
			RuntimeMode:    row.RuntimeMode,
			Provider:       row.Provider,
			Status:         row.Status,
			DeviceInfo:     row.DeviceInfo,
			Metadata:       row.Metadata,
			LastSeenAt:     row.LastSeenAt,
			CreatedAt:      row.CreatedAt,
			UpdatedAt:      row.UpdatedAt,
			LegacyDaemonID: row.LegacyDaemonID,
			Visibility:     row.Visibility,
			DisplayName:    row.DisplayName,
			StartingSince:  row.StartingSince,
		}

		// Seamless migration from the previous hostname-derived identity. The
		// daemon sends every legacy daemon_id it may have registered under
		// (e.g. "host.local", "host", "host-staging"); for each match we
		// reassign agents + tasks onto the new UUID-keyed row, then delete
		// the stale row so there's only ever one runtime per machine.
		registrationHandler.mergeLegacyRuntimes(r, registered, provider, req.LegacyDaemonIDs)
		// Heal agents orphaned by a daemon identity re-establishment (e.g.
		// ~/.multica wiped and the daemon re-registered under a fresh id).
		registrationHandler.convergeOrphanedRuntime(r.Context(), registered, row.Inserted)

		resp = append(resp, registrationHandler.runtimeToResponse(r.Context(), registered))
		registeredRuntimes = append(registeredRuntimes, registeredRuntime{
			runtime:  registered,
			provider: provider,
			version:  runtime.Version,
			inserted: row.Inserted,
		})
	}

	daemonToken, err := registrationHandler.prepareDaemonRegisterToken(r.Context(), wsUUID, req.DaemonID)
	if err != nil {
		slog.Error("daemon register: issue daemon token failed",
			append(logger.RequestAttrs(r), "error", err, "workspace_id", req.WorkspaceID, "daemon_id", req.DaemonID)...)
		writeError(w, http.StatusInternalServerError, "failed to issue daemon token")
		return
	}

	if err := registrationLock.Commit(r.Context()); err != nil {
		writeError(w, http.StatusInternalServerError, "failed to finish computer registration")
		return
	}

	h.cacheDaemonRegisterToken(r.Context(), daemonToken, wsUUID, req.DaemonID)
	for _, registered := range registeredRuntimes {
		h.completeRuntimeUpdateOnTargetRegister(r, registered.runtime, req.CLIVersion)
		// Inserted is false for normal daemon reconnects/upserts, so
		// runtime_ready is a first-ready-per-runtime-row signal.
		if registered.inserted {
			obsmetrics.RecordEvent(h.Analytics, h.Metrics, analytics.RuntimeRegistered(
				uuidToString(ownerID),
				req.WorkspaceID,
				uuidToString(registered.runtime.ID),
				req.DaemonID,
				registered.provider,
				registered.version,
				req.CLIVersion,
			))
			if registered.runtime.Status == "online" {
				obsmetrics.RecordEvent(h.Analytics, h.Metrics, analytics.RuntimeReady(
					uuidToString(ownerID),
					req.WorkspaceID,
					uuidToString(registered.runtime.ID),
					req.DaemonID,
					registered.provider,
					0,
				))
			}
		}
	}

	slog.Info("daemon registered", "workspace_id", req.WorkspaceID, "daemon_id", req.DaemonID, "runtimes_count", len(resp))
	h.publish(protocol.EventDaemonRegister, req.WorkspaceID, "system", "", map[string]any{
		"runtimes": resp,
	})

	writeJSON(w, http.StatusOK, map[string]any{
		"runtimes":                resp,
		"settings":                json.RawMessage(ws.Settings),
		"daemon_token":            daemonToken.raw,
		"daemon_token_expires_at": daemonToken.expiresAt.UTC().Format(time.RFC3339Nano),
		"server_capabilities":     negotiatedDaemonCapabilities(capabilities),
	})
}

func negotiatedDaemonCapabilities(capabilities []string) []string {
	negotiated := make([]string, 0, 2)
	for _, capability := range capabilities {
		switch capability {
		case protocol.DaemonCapabilityReminderVersionedCache, protocol.DaemonCapabilityReminderFireRequest, protocol.DaemonCapabilityMachineUpgrade:
			negotiated = append(negotiated, capability)
		}
	}
	return negotiated
}

func (h *Handler) completeRuntimeUpdateOnTargetRegister(r *http.Request, rt db.AgentRuntime, cliVersion string) {
	if h == nil || h.UpdateStore == nil {
		return
	}
	version := strings.TrimSpace(cliVersion)
	if version == "" {
		return
	}
	runtimeID := uuidToString(rt.ID)
	update, err := h.UpdateStore.LatestForRuntime(r.Context(), runtimeID)
	if err != nil {
		slog.Warn("failed to load runtime update during register", "error", err, "runtime_id", runtimeID)
		return
	}
	if update == nil || (update.Status != UpdateRunning && update.Status != UpdateReady && update.Status != UpdateCompleted) {
		return
	}
	target := update.TargetVersion
	if !runtimeVersionAtLeastTarget(&version, &target) {
		return
	}
	if err := h.UpdateStore.Complete(r.Context(), update.ID, "Daemon registered updated CLI "+version); err != nil {
		slog.Warn("failed to complete runtime update during register", "error", err, "runtime_id", runtimeID, "update_id", update.ID)
	}
}

func normalizeDaemonCapabilities(capabilities []string) []string {
	if len(capabilities) == 0 {
		return []string{}
	}
	seen := make(map[string]struct{}, len(capabilities))
	normalized := make([]string, 0, len(capabilities))
	for _, capability := range capabilities {
		capability = strings.TrimSpace(capability)
		if capability == "" {
			continue
		}
		if _, ok := seen[capability]; ok {
			continue
		}
		seen[capability] = struct{}{}
		normalized = append(normalized, capability)
	}
	if normalized == nil {
		return []string{}
	}
	return normalized
}

// mergeLegacyRuntimes folds every runtime row keyed on a prior hostname-derived
// daemon_id into the newly registered UUID-keyed row. For each legacy id the
// lookup is case-insensitive and returns *all* matching rows — case-only drift
// may have already minted duplicates historically (e.g. `Foo.local` AND
// `foo.local` coexisting), and we need to consolidate every one of them, not
// just the first. Per match we reassign agents and tasks, record the legacy
// id on the new row for audit, then delete the stale row.
//
// Scoping by (workspace_id, provider) is sufficient since provider is single-
// runtime-per-daemon; `unique (workspace_id, daemon_id, provider)` prevents
// any two *exact* matches but the `LOWER(...)` comparison crosses that bound
// precisely when case-duplicate rows exist — which is the bug we're fixing.
// We also dedupe across legacy ids so overlapping candidates (e.g. `foo` and
// `foo.local` both resolving to the same stored row) don't double-process.
func (h *Handler) mergeLegacyRuntimes(r *http.Request, registered db.AgentRuntime, provider string, legacyIDs []string) {
	newID := uuidToString(registered.ID)
	merged := make(map[string]struct{})

	for _, legacyID := range legacyIDs {
		legacyID = strings.TrimSpace(legacyID)
		if legacyID == "" {
			continue
		}

		matches, err := h.Queries.FindLegacyRuntimesByDaemonID(r.Context(), db.FindLegacyRuntimesByDaemonIDParams{
			WorkspaceID: registered.WorkspaceID,
			Provider:    provider,
			DaemonID:    legacyID,
		})
		if err != nil {
			slog.Warn("legacy runtime merge: lookup failed", "legacy_daemon_id", legacyID, "error", err)
			continue
		}
		for _, old := range matches {
			oldID := uuidToString(old.ID)
			if oldID == newID {
				continue
			}
			if _, seen := merged[oldID]; seen {
				continue
			}
			merged[oldID] = struct{}{}

			agents, err := h.Queries.ReassignAgentsToRuntime(r.Context(), db.ReassignAgentsToRuntimeParams{
				NewRuntimeID: registered.ID,
				OldRuntimeID: old.ID,
			})
			if err != nil {
				slog.Warn("legacy runtime merge: reassign agents failed", "legacy_daemon_id", legacyID, "old_runtime_id", oldID, "new_runtime_id", newID, "error", err)
				continue
			}
			tasks, err := h.Queries.ReassignTasksToRuntime(r.Context(), db.ReassignTasksToRuntimeParams{
				NewRuntimeID: registered.ID,
				OldRuntimeID: old.ID,
			})
			if err != nil {
				slog.Warn("legacy runtime merge: reassign tasks failed", "legacy_daemon_id", legacyID, "old_runtime_id", oldID, "new_runtime_id", newID, "error", err)
				continue
			}
			if err := h.Queries.RecordRuntimeLegacyDaemonID(r.Context(), db.RecordRuntimeLegacyDaemonIDParams{
				ID:             registered.ID,
				LegacyDaemonID: strToText(legacyID),
			}); err != nil {
				slog.Warn("legacy runtime merge: record legacy daemon_id failed", "legacy_daemon_id", legacyID, "error", err)
			}
			// Fail incomplete memory curation runs on the old runtime before it is
			// deleted (runtime_id FK is ON DELETE SET NULL and would otherwise
			// strand queued runs). Best-effort to match the surrounding merge path.
			if _, err := h.DB.Exec(r.Context(), `
				UPDATE memory_curation_run
				   SET status = 'failed', error = 'runtime merged', finished_at = now()
				 WHERE runtime_id = $1 AND status IN ('queued', 'waiting_runtime', 'running')
			`, old.ID); err != nil {
				slog.Warn("legacy runtime merge: fail curation runs failed", "old_runtime_id", oldID, "error", err)
			}
			if err := h.Queries.DeleteAgentRuntime(r.Context(), old.ID); err != nil {
				slog.Warn("legacy runtime merge: delete old runtime failed", "old_runtime_id", oldID, "error", err)
				continue
			}

			slog.Info("legacy runtime merged",
				"legacy_daemon_id", legacyID,
				"old_runtime_id", oldID,
				"new_runtime_id", newID,
				"provider", provider,
				"agents_reassigned", agents,
				"tasks_reassigned", tasks,
			)
		}
	}
}

// orphanDeadWindow is how long a daemon may stay silent before it is treated as
// gone for identity-reestablishment convergence purposes, when the live Runner
// socket (Hub) is unavailable.
const orphanDeadWindow = 5 * time.Minute

// daemonAliveByRunner is the one daemon-liveness judgment used by orphan
// convergence. A current DaemonCore Workspace Runner socket is authoritative;
// the HTTP heartbeat window is only the fallback when Hub or identity is
// unavailable (legacy / test composition), mirroring computerConnectedByRunner.
func (h *Handler) daemonAliveByRunner(ctx context.Context, daemonID, workspaceID string, beats []db.DaemonHeartbeat) bool {
	if h != nil && h.DaemonHub != nil && strings.TrimSpace(daemonID) != "" && strings.TrimSpace(workspaceID) != "" {
		return h.DaemonHub.HasWorkspaceDaemon(daemonID, workspaceID)
	}
	now := time.Now()
	for _, hb := range beats {
		if hb.DaemonID == daemonID && hb.LastSeenAt.Valid && now.Sub(hb.LastSeenAt.Time) < orphanDeadWindow {
			return true
		}
	}
	return false
}

// runtimeDeviceName extracts the structured device_name the daemon reports (its
// hostname), falling back to the DeviceInfo "host · version" prefix when the
// metadata field is absent.
func runtimeDeviceName(rt db.AgentRuntime) string {
	if len(rt.Metadata) > 0 {
		var m map[string]any
		if err := json.Unmarshal(rt.Metadata, &m); err == nil {
			if s, ok := m["device_name"].(string); ok && strings.TrimSpace(s) != "" {
				return strings.TrimSpace(s)
			}
		}
	}
	if i := strings.Index(rt.DeviceInfo, " · "); i >= 0 {
		return strings.TrimSpace(rt.DeviceInfo[:i])
	}
	return strings.TrimSpace(rt.DeviceInfo)
}

// computerMachineID resolves the Computer identity's persistent machine
// fingerprint for a daemon, scoped to the owning user. A physical machine may
// be shared by many OS users, each with their own identity, so the lookup is
// always keyed on (daemon_id, user_id) — never machine_id or daemon_id alone.
// Returns "" when the machine has no recorded fingerprint yet.
func (h *Handler) computerMachineID(ctx context.Context, daemonID, ownerID string) string {
	if daemonID == "" || ownerID == "" {
		return ""
	}
	var id string
	err := h.DB.QueryRow(ctx, `
SELECT machine_id FROM computers
 WHERE id = $1 AND user_id = $2 AND machine_id IS NOT NULL AND machine_id <> ''
`, daemonID, ownerID).Scan(&id)
	if err != nil {
		if !errors.Is(err, pgx.ErrNoRows) {
			slog.Warn("machine_id lookup failed", "daemon", daemonID, "error", err)
		}
		return ""
	}
	return strings.TrimSpace(id)
}

// convergeOrphanedRuntime heals the daemon identity-reestablishment case. A
// machine whose identity is rebuilt (e.g. `~/.multica` wiped → a fresh
// daemon_id is minted) re-registers its runtimes under a brand-new daemon_id;
// the new daemon has no way to report the old identity, so agents still pinned
// to the old identity's runtimes would otherwise silently go offline.
//
// The predecessor is detected server-side and its agents reassigned onto the
// newly registered same-provider runtime, but ONLY when the match is
// unambiguous. A predecessor candidate is another daemon's runtime with the
// SAME workspace, provider, owner and device_name whose daemon is dead (no
// recent heartbeat). Because device_name is just the hostname and is not unique
// across machines, whenever more than one candidate matches we refuse to guess
// and skip — never misroute an agent onto another machine.
//
// It runs only on first insertion of a runtime row; normal restarts re-register
// an existing row (inserted=false) and are left untouched, matching the
// "no remap on the happy restart path" contract.
func (h *Handler) convergeOrphanedRuntime(ctx context.Context, registered db.AgentRuntime, inserted bool) {
	if !inserted {
		return
	}
	newRuntimeID := uuidToString(registered.ID)
	newDaemon := strings.TrimSpace(registered.DaemonID.String)
	if newDaemon == "" {
		return
	}
	registeredOwner, err := h.resolveRuntimeOwnerQuery(ctx, registered)
	if err != nil {
		return
	}
	ownerID := uuidToString(registeredOwner)
	provider := registered.Provider
	workspaceID := registered.WorkspaceID
	// machine_id is the authoritative same-machine proof. It is resolved from
	// the Computer identity (computers), never from runtime
	// metadata. When neither side has one we fall back to hostname+uniqueness.
	newMachineID := h.computerMachineID(ctx, newDaemon, ownerID)
	device := runtimeDeviceName(registered)
	if newMachineID == "" && device == "" {
		return
	}

	all, err := h.Queries.ListAgentRuntimes(ctx, workspaceID)
	if err != nil {
		slog.Warn("orphan convergence: list runtimes failed", "workspace", uuidToString(workspaceID), "error", err)
		return
	}
	beats, err := h.Queries.GetDaemonHeartbeatsForWorkspace(ctx, workspaceID)
	if err != nil {
		slog.Warn("orphan convergence: heartbeat lookup failed", "workspace", uuidToString(workspaceID), "error", err)
		return
	}

	var candidates []db.AgentRuntime
	for _, rt := range all {
		rtDaemon := strings.TrimSpace(rt.DaemonID.String)
		if rtDaemon == "" || rtDaemon == newDaemon {
			continue
		}
		if uuidToString(rt.ID) == newRuntimeID {
			continue
		}
		if rt.Provider != provider {
			continue
		}
		rtOwner, err := h.resolveRuntimeOwnerQuery(ctx, rt)
		if err != nil || uuidToString(rtOwner) != ownerID {
			continue
		}
		// Live daemon check: a current DaemonCore Workspace Runner socket is
		// authoritative liveness. Only a dead predecessor may be converged — a
		// live same-owner/same-device daemon is a second machine, never a
		// re-established identity. Falls back to the heartbeat window when the
		// Hub is unavailable (legacy / test composition), mirroring
		// computerConnectedByRunner.
		if h.daemonAliveByRunner(ctx, rtDaemon, uuidToString(workspaceID), beats) {
			continue
		}
		// Same-machine proof: prefer machine_id (ironclad), fall back to
		// hostname only when there is no machine_id on either side.
		if newMachineID != "" {
			candidateMachineID := h.computerMachineID(ctx, rtDaemon, ownerID)
			if candidateMachineID == "" || candidateMachineID != newMachineID {
				continue
			}
		} else if runtimeDeviceName(rt) != device {
			continue
		}
		candidates = append(candidates, rt)
	}
	if len(candidates) != 1 {
		if len(candidates) > 1 {
			slog.Info("orphan convergence: ambiguous predecessor, skipping",
				"workspace", uuidToString(workspaceID), "daemon", newDaemon, "provider", provider, "candidates", len(candidates))
		}
		return
	}
	old := candidates[0]
	oldID := uuidToString(old.ID)
	agents, err := h.Queries.ReassignAgentsToRuntime(ctx, db.ReassignAgentsToRuntimeParams{
		NewRuntimeID: registered.ID,
		OldRuntimeID: old.ID,
	})
	if err != nil {
		slog.Warn("orphan convergence: reassign agents failed", "old_runtime", oldID, "new_runtime", newRuntimeID, "error", err)
		return
	}
	tasks, err := h.Queries.ReassignTasksToRuntime(ctx, db.ReassignTasksToRuntimeParams{
		NewRuntimeID: registered.ID,
		OldRuntimeID: old.ID,
	})
	if err != nil {
		slog.Warn("orphan convergence: reassign tasks failed", "old_runtime", oldID, "new_runtime", newRuntimeID, "error", err)
		return
	}
	// Fail incomplete memory curation runs on the stale runtime before deleting
	// it (runtime_id FK is ON DELETE SET NULL and would otherwise strand queued
	// runs). Mirrors the surrounding legacy-merge path.
	if _, err := h.DB.Exec(ctx, `
		UPDATE memory_curation_run
		   SET status = 'failed', error = 'runtime merged', finished_at = now()
		 WHERE runtime_id = $1 AND status IN ('queued', 'waiting_runtime', 'running')
	`, old.ID); err != nil {
		slog.Warn("orphan convergence: fail curation runs failed", "old_runtime", oldID, "error", err)
	}
	if err := h.Queries.DeleteAgentRuntime(ctx, old.ID); err != nil {
		slog.Warn("orphan convergence: delete stale runtime failed", "old_runtime", oldID, "error", err)
		return
	}
	slog.Info("orphan runtime converged",
		"old_daemon", strings.TrimSpace(old.DaemonID.String), "old_runtime", oldID,
		"new_daemon", newDaemon, "new_runtime", newRuntimeID,
		"provider", provider, "device", device, "agents_reassigned", agents, "tasks_reassigned", tasks)
}

// DaemonDeregister marks runtimes as offline when the daemon shuts down.
func (h *Handler) DaemonDeregister(w http.ResponseWriter, r *http.Request) {
	var req struct {
		RuntimeIDs []string `json:"runtime_ids"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid request body")
		return
	}

	if len(req.RuntimeIDs) == 0 {
		writeError(w, http.StatusBadRequest, "runtime_ids is required")
		return
	}
	runtimeUUIDs, ok := parseUUIDSliceOrBadRequest(w, req.RuntimeIDs, "runtime_ids")
	if !ok {
		return
	}

	// Track affected workspaces for WS notifications.
	affectedWorkspaces := make(map[string]bool)

	for i, rid := range req.RuntimeIDs {
		// Look up the runtime and verify ownership.
		rt, err := h.Queries.GetAgentRuntime(r.Context(), runtimeUUIDs[i])
		if err != nil {
			slog.Warn("deregister: runtime not found", "runtime_id", rid, "error", err)
			continue
		}

		wsID := uuidToString(rt.WorkspaceID)
		if !h.verifyDaemonWorkspaceAccess(r, wsID) {
			slog.Warn("deregister: workspace mismatch", "runtime_id", rid)
			continue
		}

		if err := h.Queries.SetAgentRuntimeOffline(r.Context(), db.SetAgentRuntimeOfflineParams{
			ID:            rt.ID,
			OfflineReason: pgtype.Text{String: "daemon_deregistered", Valid: true},
		}); err != nil {
			slog.Warn("deregister: failed to set offline", "runtime_id", rid, "error", err)
			continue
		}
		h.recordRuntimeHealthEventForActiveAgents(r.Context(), rt, agentHealthEventLivenessProbe, agentHealthStateSuspectedDisconnect, "daemon_deregistered", "runtime deregistered by daemon shutdown", map[string]any{
			"source": "daemon_deregister",
		})
		rtOwnerForAnalytics, _ := h.resolveRuntimeOwnerQuery(r.Context(), rt)
		obsmetrics.RecordEvent(h.Analytics, h.Metrics, analytics.RuntimeOffline(
			uuidToString(rtOwnerForAnalytics),
			wsID,
			uuidToString(rt.ID),
			rt.DaemonID.String,
			rt.Provider,
		))

		affectedWorkspaces[wsID] = true
	}

	// Notify frontend clients so they re-fetch runtime list.
	for wsID := range affectedWorkspaces {
		h.publish(protocol.EventDaemonRegister, wsID, "system", "", map[string]any{
			"action": "deregister",
		})
	}

	slog.Info("daemon deregistered", "runtime_ids", req.RuntimeIDs)
	writeJSON(w, http.StatusOK, map[string]string{"status": "ok"})
}

type DaemonHeartbeatRequest struct {
	RuntimeID                 string                            `json:"runtime_id"`
	SupportsBatchImport       bool                              `json:"supports_batch_import,omitempty"`
	SupportsMemoryCuration    bool                              `json:"supports_memory_curation,omitempty"`
	ActiveMemoryCurationRunID string                            `json:"active_memory_curation_run_id,omitempty"`
	UpdateObservation         *protocol.DaemonUpdateObservation `json:"auto_update,omitempty"`
}

// heartbeatHasPendingTimeout bounds the cheap HasPending probe on the
// heartbeat hot path. Probes are read-only (ZCARD in Redis) so a timeout is
// ack-safe: the worst case is "we didn't find out if anything was queued this
// tick" and the next heartbeat (default 15s later) will try again.
//
// PopPending is deliberately NOT bounded this way — its Redis implementation
// runs a Lua claim script whose ZREM + SET-running side effects cannot be
// cleanly un-run from the client side if the context expires mid-script. We
// therefore only invoke PopPending after HasPending confirms there is work
// to claim, so we never start a claim we might have to abort.
const heartbeatHasPendingTimeout = 1 * time.Second

// maxLocalSkillImportBatch is how many pending import requests the heartbeat
// handler pops per cycle. Higher values let the daemon process more imports
// in parallel but increase per-heartbeat latency.
//
// Timeout invariant: IMPORT_CONCURRENCY (views/.../runtime-local-skill-import-panel.tsx)
// × heartbeat period (~15s) must stay within runtimeLocalSkillPendingTimeout
// (runtime_local_skills.go), and IMPORT_POLL_TIMEOUT_MS (core/runtimes/local-skills.ts)
// must exceed pendingTimeout + runningTimeout.
const maxLocalSkillImportBatch = 10

// runtimeLivenessTTL is how long a Redis liveness record stays valid before
// expiring. The daemon refreshes it every heartbeat (~15s), so this just
// needs to be a few heartbeats long — the value (90s) tolerates ~6 missed
// beats before Redis declares the runtime dead.
//
// It is intentionally shorter than the sweeper's stale threshold (150s in
// cmd/server/runtime_sweeper.go). That ordering is safe and desirable:
// Redis can declare a runtime dead before the DB stale window opens, and
// the sweeper will simply ignore it until the DB column also crosses the
// threshold. The unsafe direction would be the opposite (Redis claiming
// "alive" past the DB stale window, masking a truly dead runtime when the
// sweeper consults Redis as the source of truth) — that cannot happen here.
const runtimeLivenessTTL = 90 * time.Second

// runtimeHeartbeatDBFlushInterval is the maximum staleness we tolerate on
// agent_runtime.last_seen_at while Redis is the active liveness source. When
// last_seen_at gets older than this, the heartbeat path schedules a DB write
// so (a) the UI's "last seen" display stays bounded and (b) the sweeper's
// DB-only fallback path (used when an IsAliveBatch call to Redis errors) does
// not false-positive on alive-but-Redis-only runtimes.
//
// Load-bearing invariant: this must be strictly less than the sweeper's
// stale threshold (150s in cmd/server/runtime_sweeper.go) MINUS one daemon
// heartbeat cycle (~15s) MINUS the BatchedHeartbeatScheduler tick interval
// (~30s). Worst-case DB age for an alive runtime is therefore bounded by
// flush + heartbeat + batchTick = 60 + 15 + 30 = 105s, leaving a 45s buffer
// below the 150s stale window. If you tune any of these constants, recompute
// the chain and keep at least a one-tick buffer.
//
// We intentionally keep the per-runtime flush throttle at 60s (rather than
// pushing it higher) so a crashed runtime is detected within ~150s instead
// of ~10 minutes. The bulk of the DB-pressure win comes from batched
// coalescing in HeartbeatScheduler — at 70 online runtimes that collapses
// ~17 single-row UPDATE/s into ~0.03 bulk UPDATE/s (one per batch tick),
// independent of how the per-runtime throttle is tuned.
const runtimeHeartbeatDBFlushInterval = 60 * time.Second

func (h *Handler) DaemonHeartbeat(w http.ResponseWriter, r *http.Request) {
	start := time.Now()
	authPath := middleware.DaemonAuthPathFromContext(r.Context())
	var (
		outcome                                                                                            = "unauth"
		runtimeID                                                                                          string
		decodeMs, runtimeLookupMs, workspaceCheckMs                                                        int64
		authMs, updateMs, probeModelMs, popModelMs, probeSkillsMs, popSkillsMs, probeImportMs, popImportMs int64
		probeModelTimedOut, probeSkillsTimedOut, probeImportTimedOut                                       bool
	)
	defer func() {
		logHeartbeatEndpointSlow(runtimeID, outcome, authPath, start, decodeMs, runtimeLookupMs, workspaceCheckMs, authMs, updateMs, probeModelMs, popModelMs, probeSkillsMs, popSkillsMs, probeImportMs, popImportMs, probeModelTimedOut, probeSkillsTimedOut, probeImportTimedOut)
	}()

	decodeStart := time.Now()
	var req DaemonHeartbeatRequest
	decodeErr := json.NewDecoder(r.Body).Decode(&req)
	decodeMs = time.Since(decodeStart).Milliseconds()
	if decodeErr != nil {
		outcome = "bad_body"
		writeError(w, http.StatusBadRequest, "invalid request body")
		return
	}

	if req.RuntimeID == "" {
		outcome = "missing_runtime_id"
		writeError(w, http.StatusBadRequest, "runtime_id is required")
		return
	}
	runtimeID = req.RuntimeID

	// Inlined and instrumented version of requireDaemonRuntimeAccess so we
	// can attribute the runtime-lookup and workspace-check sub-stages
	// independently in slow-logs. Together with the auth_path label set by
	// DaemonAuth middleware, this lets us tell whether prod heartbeat tail
	// latency is in pgx pool acquisition (runtime_lookup_ms), in the PAT
	// fallback workspace-membership query (workspace_check_ms), or upstream.
	runtimeUUID, ok := parseUUIDOrBadRequest(w, req.RuntimeID, "runtime_id")
	if !ok {
		outcome = "bad_runtime_id"
		return
	}
	lookupStart := time.Now()
	rt, lookupErr := h.Queries.GetAgentRuntime(r.Context(), runtimeUUID)
	runtimeLookupMs = time.Since(lookupStart).Milliseconds()
	if lookupErr != nil {
		// Only pgx.ErrNoRows means the runtime row is gone. Daemon reads this
		// 404 as a signal to drop the stale runtime locally; treating a
		// transient DB error the same way would force daemons to self-cleanup
		// on a hiccup.
		if isNotFound(lookupErr) {
			outcome = "runtime_not_found"
			writeError(w, http.StatusNotFound, "runtime not found")
			return
		}
		outcome = "runtime_lookup_error"
		slog.Warn("get agent runtime failed", "runtime_id", req.RuntimeID, "error", lookupErr)
		writeError(w, http.StatusInternalServerError, "failed to load runtime")
		return
	}
	wsCheckStart := time.Now()
	wsOK := h.requireDaemonWorkspaceAccess(w, r, uuidToString(rt.WorkspaceID))
	workspaceCheckMs = time.Since(wsCheckStart).Milliseconds()
	if !wsOK {
		outcome = "workspace_denied"
		return
	}
	authMs = time.Since(start).Milliseconds()

	ack, m, err := h.processHeartbeat(r.Context(), rt, req.SupportsBatchImport, req.SupportsMemoryCuration, req.ActiveMemoryCurationRunID, req.UpdateObservation)
	updateMs = m.UpdateMs
	probeModelMs = m.ProbeModelMs
	popModelMs = m.PopModelMs
	probeSkillsMs = m.ProbeSkillsMs
	popSkillsMs = m.PopSkillsMs
	probeImportMs = m.ProbeImportMs
	popImportMs = m.PopImportMs
	probeModelTimedOut = m.ProbeModelTimedOut
	probeSkillsTimedOut = m.ProbeSkillsTimedOut
	probeImportTimedOut = m.ProbeImportTimedOut
	if err != nil {
		outcome = "error_update"
		writeError(w, http.StatusInternalServerError, "heartbeat failed")
		return
	}

	outcome = "ok"
	// Preserve the existing HTTP response shape: the runtime_id field is new
	// in the WS path and would be redundant noise on the HTTP path where the
	// caller already knows which runtime it asked about.
	resp := map[string]any{"status": ack.Status}
	if ack.PendingUpdate != nil {
		resp["pending_update"] = ack.PendingUpdate
	}
	if ack.PendingModelList != nil {
		resp["pending_model_list"] = ack.PendingModelList
	}
	if ack.PendingLocalSkills != nil {
		resp["pending_local_skills"] = ack.PendingLocalSkills
	}
	if ack.PendingLocalSkillImport != nil {
		resp["pending_local_skill_import"] = ack.PendingLocalSkillImport
	}
	if len(ack.PendingLocalSkillImports) > 0 {
		resp["pending_local_skill_imports"] = ack.PendingLocalSkillImports
	}
	if ack.PendingMemoryCuration != nil {
		resp["pending_memory_curation"] = ack.PendingMemoryCuration
	}
	if ack.ReleaseManifestBaseURL != "" {
		resp["release_manifest_base_url"] = ack.ReleaseManifestBaseURL
	}
	writeJSON(w, http.StatusOK, resp)
}

// HandleDaemonWSHeartbeat is the daemonws.HeartbeatHandler entry point: it
// resolves the runtime, verifies the connection's workspace owns it, and
// returns the ack payload. It is the WebSocket-side mirror of DaemonHeartbeat.
//
// Workspace authorization is re-checked on every heartbeat instead of trusted
// from the upgrade-time check because runtime ownership can change (e.g. a
// runtime is reassigned to another workspace mid-connection).
//
// When the runtime row is missing (pgx.ErrNoRows), the function returns a
// successful ack with Status=HeartbeatStatusRuntimeGone and RuntimeGone=true
// instead of an error. That keeps the hub from logging every beat at Warn,
// and tells the daemon to drop the stale runtime and re-register. Other DB
// errors still propagate as errors so they keep their existing Warn logging
// and the daemon does not mistake a hiccup for a deletion.
func (h *Handler) HandleDaemonWSHeartbeat(ctx context.Context, identity daemonws.ClientIdentity, payload protocol.DaemonHeartbeatRequestPayload) (*protocol.DaemonHeartbeatAckPayload, error) {
	runtimeID := payload.RuntimeID
	runtimeUUID, err := util.ParseUUID(runtimeID)
	if err != nil {
		return nil, fmt.Errorf("invalid runtime_id: %w", err)
	}
	rt, err := h.Queries.GetAgentRuntime(ctx, runtimeUUID)
	if err != nil {
		if isNotFound(err) {
			return &protocol.DaemonHeartbeatAckPayload{
				RuntimeID:   runtimeID,
				Status:      protocol.HeartbeatStatusRuntimeGone,
				RuntimeGone: true,
			}, nil
		}
		return nil, fmt.Errorf("get agent runtime: %w", err)
	}
	if identity.WorkspaceID != "" && identity.WorkspaceID != uuidToString(rt.WorkspaceID) {
		return nil, fmt.Errorf("runtime not in connection workspace")
	}
	if rt.DaemonID.Valid {
		dynamicRunner := len(identity.RuntimeIDs) == 0 && identity.WorkspaceID != ""
		if (dynamicRunner && identity.DaemonID != rt.DaemonID.String) ||
			(!dynamicRunner && identity.DaemonID != "" && identity.DaemonID != rt.DaemonID.String) {
			return nil, fmt.Errorf("runtime not assigned to connection Computer")
		}
	}
	ack, _, err := h.processHeartbeat(ctx, rt, payload.SupportsBatchImport, payload.SupportsMemoryCuration, payload.ActiveMemoryCurationRunID, payload.UpdateObservation)
	if err != nil || ack == nil {
		return ack, err
	}
	// A capable current Workspace Runner receives managed launch commands on
	// its fenced socket. Do not also return the legacy first-start envelope on
	// the heartbeat: both paths share the same durable intent, but only one may
	// be active for a given connection generation.
	if h.DaemonHub != nil && h.DaemonHub.WorkspaceDaemonSupportsCapability(identity.DaemonID, identity.WorkspaceID, protocol.DaemonCapabilityWorkspaceDaemonAgentProcess) {
		if err := h.reconcileWorkspaceDaemonLaunches(ctx, identity); err != nil {
			return nil, err
		}
	}
	return ack, nil
}

// recordHeartbeat marks the runtime as alive. When LivenessStore is available
// (Redis configured and reachable) it writes a TTL'd liveness key and skips
// the DB row write on most beats — the DB is only updated on the
// offline→online transition or once per runtimeHeartbeatDBFlushInterval to
// keep last_seen_at fresh enough for the UI and the DB-fallback sweeper.
//
// When LivenessStore is unavailable (no Redis configured) or any Touch call
// errors, recordHeartbeat falls back to writing the DB on every beat — that
// is the original behavior and keeps the sweeper's DB-only path correct.
//
// The actual DB write is delegated to h.HeartbeatScheduler so production can
// coalesce many runtimes' bumps into one bulk UPDATE per tick. See
// heartbeat_scheduler.go for the two implementations.
func (h *Handler) recordHeartbeat(ctx context.Context, rt db.AgentRuntime) error {
	now := time.Now()

	// Daemon-level heartbeat (task #58): recorded unconditionally, separate
	// from the per-runtime liveness bookkeeping below. One physical daemon
	// can host several agent_runtime rows sharing the same daemon_id, and
	// "is the computer connected" must not be derived by aggregating those
	// rows' individual last_seen_at values — that answers "is some runtime
	// on this machine alive", a different question that disagrees with
	// real connectivity whenever the daemon is up but has no live runtime.
	// This is a single tiny UPSERT (one row per daemon_id), so it isn't
	// worth gating behind the same Redis-debounce path as the runtime row.
	if daemonID := strings.TrimSpace(rt.DaemonID.String); rt.DaemonID.Valid && daemonID != "" {
		if err := h.Queries.RecordDaemonHeartbeat(ctx, db.RecordDaemonHeartbeatParams{
			WorkspaceID: rt.WorkspaceID,
			DaemonID:    daemonID,
		}); err != nil {
			slog.Warn("record daemon heartbeat failed", "daemon_id", daemonID, "error", err)
		}
	}

	// Decide whether the DB row needs a write *before* touching Redis, so a
	// Touch failure can simply force needDBWrite=true without re-evaluating
	// the structural reasons.
	needDBWrite := !h.LivenessStore.Available() ||
		rt.Status != "online" ||
		!rt.LastSeenAt.Valid ||
		now.Sub(rt.LastSeenAt.Time) >= runtimeHeartbeatDBFlushInterval

	if h.LivenessStore.Available() {
		if err := h.LivenessStore.Touch(ctx, uuidToString(rt.ID), runtimeLivenessTTL); err != nil {
			// Redis hiccup: degrade transparently to the DB-only path for
			// this beat. The sweeper falls back to its DB threshold the
			// same way when IsAliveBatch fails, so end-to-end correctness
			// is preserved.
			slog.Warn("liveness touch failed; falling back to DB heartbeat",
				"runtime_id", uuidToString(rt.ID), "error", err)
			needDBWrite = true
		}
	}

	if !needDBWrite {
		return nil
	}

	// Either bumps last_seen_at on an already-online row (Touch + race
	// fallback) or flips status from offline to online. The scheduler
	// chooses sync vs batched per case; see HeartbeatScheduler doc.
	if err := h.HeartbeatScheduler.Schedule(ctx, rt); err != nil {
		return err
	}
	switch {
	case rt.Status != "online":
		h.recordRuntimeHealthEventForActiveAgents(ctx, rt, agentHealthEventTransportRecover, agentHealthStateRecovered, "transport_reconnected", "runtime transport reconnected", map[string]any{
			"previous_status": rt.Status,
			"last_seen_at":    timestampToString(rt.LastSeenAt),
		})
	case !rt.LastSeenAt.Valid:
		h.recordRuntimeHealthEventForActiveAgents(ctx, rt, agentHealthEventServerPing, agentHealthStateOnline, "heartbeat_received", "runtime heartbeat received", nil)
	}
	return nil
}

// heartbeatMetrics carries per-stage timings out of processHeartbeat so the
// HTTP slow-log can stay structured. The WS path discards them.
type heartbeatMetrics struct {
	UpdateMs, ProbeModelMs, PopModelMs, ProbeSkillsMs, PopSkillsMs, ProbeImportMs, PopImportMs int64
	ProbeModelTimedOut, ProbeSkillsTimedOut, ProbeImportTimedOut                               bool
}

// processHeartbeat does the work shared by HTTP POST /api/daemon/heartbeat and
// the WebSocket daemon:heartbeat path: records liveness and pulls any pending
// actions queued for the runtime. Auth and request decoding live in the
// caller because they differ between transports.
func (h *Handler) processHeartbeat(
	ctx context.Context,
	rt db.AgentRuntime,
	supportsBatchImport, supportsMemoryCuration bool,
	activeMemoryCurationRunID string,
	updateObservation *protocol.DaemonUpdateObservation,
) (*protocol.DaemonHeartbeatAckPayload, heartbeatMetrics, error) {
	var m heartbeatMetrics
	runtimeID := uuidToString(rt.ID)

	updateStart := time.Now()
	if err := h.recordHeartbeat(ctx, rt); err != nil {
		m.UpdateMs = time.Since(updateStart).Milliseconds()
		return nil, m, err
	}
	m.UpdateMs = time.Since(updateStart).Milliseconds()
	h.advanceDaemonUpdateObservation(ctx, rt, updateObservation)

	slog.Debug("daemon heartbeat", "runtime_id", runtimeID)

	ack := &protocol.DaemonHeartbeatAckPayload{
		RuntimeID:              runtimeID,
		Status:                 "ok",
		ReleaseManifestBaseURL: serverDispatchedReleaseManifestBaseURL(),
	}
	if supportsMemoryCuration && agentRuntimeHasCapability(rt, protocol.DaemonCapabilityMemoryCuration) {
		pendingCuration, err := h.claimPendingMemoryCurationRun(ctx, rt, activeMemoryCurationRunID)
		if err != nil {
			slog.Warn("memory curation heartbeat claim failed", "runtime_id", runtimeID, "error", err)
			return nil, m, err
		}
		ack.PendingMemoryCuration = pendingCuration
	}
	// A heartbeat arriving IS the runtime being reachable right now — this is
	// the fix for the 2026-08-01/02 incident where InitiateUpdate's old
	// 120-second delivery window meant a sleeping laptop simply missed its
	// update with no retry. If there's a live UpdateIntent (durable, created
	// by InitiateUpdate) and no attempt already in flight, materialize it
	// into a real attempt right here so the HasPending/PopPending block below
	// picks it up and delivers it in this same heartbeat response.
	h.maybeMaterializeUpdateIntent(ctx, rt)

	_, runtimePinned := runtimePinnedVersion(rt)
	probeUpdateCtx, cancelProbeUpdate := context.WithTimeout(ctx, heartbeatHasPendingTimeout)
	hasUpdate, probeUpdateErr := h.UpdateStore.HasPending(probeUpdateCtx, runtimeID)
	cancelProbeUpdate()
	switch {
	case probeUpdateErr == nil && hasUpdate && runtimePinned:
		// Pin wins (task #81). Deliberately do NOT call PopPending here —
		// popping claims the row (it stops being "pending"), which would
		// permanently strand it the moment this heartbeat's ack doesn't
		// deliver it. Leaving HasPending's answer untouched means this same
		// pending update is offered again on every future heartbeat until
		// the pin is lifted, instead of being silently consumed once.
		slog.Info("skipping pending update delivery: runtime pinned", "runtime_id", runtimeID)
	case probeUpdateErr == nil && hasUpdate:
		pending, popUpdateErr := h.UpdateStore.PopPending(ctx, runtimeID)
		if popUpdateErr != nil {
			slog.Warn("update PopPending failed", "error", popUpdateErr, "runtime_id", runtimeID)
		} else if pending != nil {
			ack.PendingUpdate = &protocol.DaemonHeartbeatPendingUpdate{
				ID:                   pending.ID,
				TargetVersion:        pending.TargetVersion,
				SupportsReadyToApply: true,
			}
			h.publish(protocol.EventDaemonRuntimeUpdated, uuidToString(rt.WorkspaceID), "system", "", map[string]any{
				"runtime": h.runtimeToResponse(ctx, rt),
			})
		}
	case probeUpdateErr != nil:
		if errors.Is(probeUpdateErr, context.DeadlineExceeded) || errors.Is(probeUpdateErr, context.Canceled) {
			slog.Warn("update HasPending timed out", "runtime_id", runtimeID)
		} else {
			slog.Warn("update HasPending failed", "error", probeUpdateErr, "runtime_id", runtimeID)
		}
	}

	probeRestartCtx, cancelProbeRestart := context.WithTimeout(ctx, heartbeatHasPendingTimeout)
	hasRestart, probeRestartErr := h.RestartStore.HasPending(probeRestartCtx, runtimeID)
	cancelProbeRestart()
	switch {
	case probeRestartErr == nil && hasRestart:
		pendingRestart, popRestartErr := h.RestartStore.PopPending(ctx, runtimeID)
		if popRestartErr != nil {
			slog.Warn("restart PopPending failed", "error", popRestartErr, "runtime_id", runtimeID)
		} else if pendingRestart != nil {
			ack.PendingRestart = &protocol.DaemonHeartbeatPendingRestart{ID: pendingRestart.ID}
		}
	case probeRestartErr != nil:
		if errors.Is(probeRestartErr, context.DeadlineExceeded) || errors.Is(probeRestartErr, context.Canceled) {
			slog.Warn("restart HasPending timed out", "runtime_id", runtimeID)
		} else {
			slog.Warn("restart HasPending failed", "error", probeRestartErr, "runtime_id", runtimeID)
		}
	}

	// Probe then claim the model list queue. Same pattern as the local-skill
	// queues below — a slow shared store cannot stall the heartbeat on
	// empty-queue ticks, but the claim itself runs unbounded because its
	// Lua side effects cannot be safely aborted mid-script.
	probeModelStart := time.Now()
	probeModelCtx, cancelProbeModel := context.WithTimeout(ctx, heartbeatHasPendingTimeout)
	hasModel, probeModelErr := h.ModelListStore.HasPending(probeModelCtx, runtimeID)
	cancelProbeModel()
	m.ProbeModelMs = time.Since(probeModelStart).Milliseconds()
	switch {
	case probeModelErr == nil && hasModel:
		popStart := time.Now()
		pendingModel, popErr := h.ModelListStore.PopPending(ctx, runtimeID)
		m.PopModelMs = time.Since(popStart).Milliseconds()
		if popErr != nil {
			slog.Warn("model list PopPending failed", "error", popErr, "runtime_id", runtimeID)
		} else if pendingModel != nil {
			ack.PendingModelList = &protocol.DaemonHeartbeatPendingModelList{ID: pendingModel.ID}
		}
	case probeModelErr != nil:
		if errors.Is(probeModelErr, context.DeadlineExceeded) || errors.Is(probeModelErr, context.Canceled) {
			m.ProbeModelTimedOut = true
			slog.Warn("model list HasPending timed out", "runtime_id", runtimeID, "elapsed_ms", m.ProbeModelMs)
		} else {
			slog.Warn("model list HasPending failed", "error", probeModelErr, "runtime_id", runtimeID)
		}
	}

	// Probe then claim the local-skill list queue. The probe is bounded so a
	// slow shared store cannot stall the heartbeat on empty-queue ticks; the
	// claim runs unbounded (it inherits only ctx) because its Lua side
	// effects cannot be safely aborted mid-script.
	probeSkillsStart := time.Now()
	probeSkillsCtx, cancelProbeSkills := context.WithTimeout(ctx, heartbeatHasPendingTimeout)
	hasSkills, probeErr := h.LocalSkillListStore.HasPending(probeSkillsCtx, runtimeID)
	cancelProbeSkills()
	m.ProbeSkillsMs = time.Since(probeSkillsStart).Milliseconds()
	switch {
	case probeErr == nil && hasSkills:
		popStart := time.Now()
		pendingSkills, popErr := h.LocalSkillListStore.PopPending(ctx, runtimeID)
		m.PopSkillsMs = time.Since(popStart).Milliseconds()
		if popErr != nil {
			slog.Warn("local skill list PopPending failed", "error", popErr, "runtime_id", runtimeID)
		} else if pendingSkills != nil {
			ack.PendingLocalSkills = &protocol.DaemonHeartbeatPendingLocalSkills{ID: pendingSkills.ID}
		}
	case probeErr != nil:
		if errors.Is(probeErr, context.DeadlineExceeded) || errors.Is(probeErr, context.Canceled) {
			m.ProbeSkillsTimedOut = true
			slog.Warn("local skill list HasPending timed out", "runtime_id", runtimeID, "elapsed_ms", m.ProbeSkillsMs)
		} else {
			slog.Warn("local skill list HasPending failed", "error", probeErr, "runtime_id", runtimeID)
		}
	}

	probeImportStart := time.Now()
	probeImportCtx, cancelProbeImport := context.WithTimeout(ctx, heartbeatHasPendingTimeout)
	hasImport, probeErr := h.LocalSkillImportStore.HasPending(probeImportCtx, runtimeID)
	cancelProbeImport()
	m.ProbeImportMs = time.Since(probeImportStart).Milliseconds()
	switch {
	case probeErr == nil && hasImport:
		popStart := time.Now()
		if supportsBatchImport {
			pendingImports, popErr := h.LocalSkillImportStore.PopPendingBatch(ctx, runtimeID, maxLocalSkillImportBatch)
			m.PopImportMs = time.Since(popStart).Milliseconds()
			if popErr != nil {
				slog.Warn("local skill import PopPendingBatch failed", "error", popErr, "runtime_id", runtimeID, "claimed", len(pendingImports))
			}
			// Always dispatch whatever was claimed — even on partial
			// failure the claimed requests have already transitioned to
			// running in the store. Dropping them here would leave them
			// stranded until the running timeout.
			if len(pendingImports) > 0 {
				// Backwards compat: singular field carries the first item so
				// old daemons that don't know the plural field still get one.
				ack.PendingLocalSkillImport = &protocol.DaemonHeartbeatPendingLocalSkillImport{
					ID:       pendingImports[0].ID,
					SkillKey: pendingImports[0].SkillKey,
				}
				batch := make([]protocol.DaemonHeartbeatPendingLocalSkillImport, 0, len(pendingImports))
				for _, p := range pendingImports {
					batch = append(batch, protocol.DaemonHeartbeatPendingLocalSkillImport{
						ID:       p.ID,
						SkillKey: p.SkillKey,
					})
				}
				ack.PendingLocalSkillImports = batch
			}
		} else {
			pendingImport, popErr := h.LocalSkillImportStore.PopPending(ctx, runtimeID)
			m.PopImportMs = time.Since(popStart).Milliseconds()
			if popErr != nil {
				slog.Warn("local skill import PopPending failed", "error", popErr, "runtime_id", runtimeID)
			} else if pendingImport != nil {
				ack.PendingLocalSkillImport = &protocol.DaemonHeartbeatPendingLocalSkillImport{
					ID:       pendingImport.ID,
					SkillKey: pendingImport.SkillKey,
				}
			}
		}
	case probeErr != nil:
		if errors.Is(probeErr, context.DeadlineExceeded) || errors.Is(probeErr, context.Canceled) {
			m.ProbeImportTimedOut = true
			slog.Warn("local skill import HasPending timed out", "runtime_id", runtimeID, "elapsed_ms", m.ProbeImportMs)
		} else {
			slog.Warn("local skill import HasPending failed", "error", probeErr, "runtime_id", runtimeID)
		}
	}

	return ack, m, nil
}

// logHeartbeatEndpointSlow emits one structured log when /api/daemon/heartbeat
// exceeds 500ms, splitting auth / update / probe / pop phases for both queues
// so the prod tail can be attributed without flooding logs at normal rates.
// auth_ms is further decomposed into decode_ms, runtime_lookup_ms, and
// workspace_check_ms; auth_path labels which token kind authenticated the
// request ("daemon_token", "pat", or "jwt").
func logHeartbeatEndpointSlow(runtimeID, outcome, authPath string, start time.Time, decodeMs, runtimeLookupMs, workspaceCheckMs, authMs, updateMs, probeModelMs, popModelMs, probeSkillsMs, popSkillsMs, probeImportMs, popImportMs int64, probeModelTimedOut, probeSkillsTimedOut, probeImportTimedOut bool) {
	totalMs := time.Since(start).Milliseconds()
	if totalMs < 500 && !probeModelTimedOut && !probeSkillsTimedOut && !probeImportTimedOut {
		return
	}
	slog.Info("heartbeat_endpoint slow",
		"runtime_id", runtimeID,
		"outcome", outcome,
		"auth_path", authPath,
		"total_ms", totalMs,
		"auth_ms", authMs,
		"decode_ms", decodeMs,
		"runtime_lookup_ms", runtimeLookupMs,
		"workspace_check_ms", workspaceCheckMs,
		"update_ms", updateMs,
		"probe_model_ms", probeModelMs,
		"pop_model_ms", popModelMs,
		"probe_skills_ms", probeSkillsMs,
		"pop_skills_ms", popSkillsMs,
		"probe_import_ms", probeImportMs,
		"pop_import_ms", popImportMs,
		"probe_model_timed_out", probeModelTimedOut,
		"probe_skills_timed_out", probeSkillsTimedOut,
		"probe_import_timed_out", probeImportTimedOut,
	)
}

func trailingUserMessages(msgs []db.ChatMessage) []db.ChatMessage {
	start := 0
	for i := len(msgs) - 1; i >= 0; i-- {
		if msgs[i].Role != "user" {
			start = i + 1
			break
		}
	}
	return msgs[start:]
}

// inboxPromptMessages returns only the synthetic channel prompt written for
// the inbox event currently being drained. Each channel prompt already
// contains a bounded conversation excerpt; using any other unanswered prompt
// can both repeat a large failed backlog and execute the wrong user request.
// Retry creation copies the original prompt and binds the copy to the new event
// ID, so a missing exact task_id link is invalid rather than a reason to guess.
func inboxPromptMessages(msgs []db.ChatMessage, eventID pgtype.UUID) []db.ChatMessage {
	if !eventID.Valid {
		return nil
	}
	current := make([]db.ChatMessage, 0, 1)
	for _, msg := range msgs {
		if msg.Role == "user" && msg.TaskID.Valid && msg.TaskID == eventID {
			current = append(current, msg)
		}
	}
	return current
}

func hasPriorChatContext(msgs []db.ChatMessage, currentTaskID pgtype.UUID) bool {
	if len(msgs) == 0 {
		return false
	}
	for _, m := range msgs {
		if !m.TaskID.Valid || !currentTaskID.Valid || m.TaskID != currentTaskID {
			return true
		}
	}
	return false
}

func shouldIncludeChatContextSummary(msgs []db.ChatMessage) bool {
	if len(msgs) <= 1 || !isShortChatConfirmation(msgs[len(msgs)-1]) {
		return false
	}
	prev := previousAssistantMessage(msgs)
	return prev != nil && isAssistantFollowupPrompt(*prev)
}

func previousAssistantMessage(msgs []db.ChatMessage) *db.ChatMessage {
	if len(msgs) < 2 {
		return nil
	}
	for i := len(msgs) - 2; i >= 0; i-- {
		if msgs[i].Role != "user" {
			return &msgs[i]
		}
	}
	return nil
}

func isShortChatConfirmation(m db.ChatMessage) bool {
	if m.Role != "user" {
		return false
	}
	text := strings.TrimSpace(m.Content)
	if text == "" || len([]rune(text)) > 8 {
		return false
	}
	text = strings.Trim(text, " \t\r\n.,!?;:，。！？；：~～")
	switch strings.ToLower(text) {
	case "行", "继续", "可以", "确认", "嗯", "好", "好的", "ok", "okay", "yes", "y", "go", "继续吧", "可以的", "没问题":
		return true
	default:
		return false
	}
}

func isAssistantFollowupPrompt(m db.ChatMessage) bool {
	if m.Role == "user" {
		return false
	}
	text := strings.TrimSpace(m.Content)
	if text == "" {
		return false
	}
	lower := strings.ToLower(text)
	if strings.ContainsAny(text, "?？") {
		return true
	}
	for _, marker := range []string{"确认", "继续", "可以", "是否", "要不要", "选择", "哪个", "哪一个", "need me", "should i"} {
		if strings.Contains(lower, marker) {
			return true
		}
	}
	return false
}

func chatFailureResumeUnsafe(reason string) bool {
	switch reason {
	case "iteration_limit", "agent_fallback_message", "api_invalid_request", "codex_semantic_inactivity", "grok_first_turn_no_progress", "grok_tool_permission_failure", "agent_error.context_overflow":
		return true
	default:
		return false
	}
}

func (h *Handler) latestChatTaskFailureReason(ctx context.Context, chatSessionID, currentTaskID pgtype.UUID) (string, bool) {
	var status, reason string
	err := h.DB.QueryRow(ctx, `
		SELECT COALESCE(terminal_outcome, status), COALESCE(failure_reason, '')
		FROM agent_inbox_event
		WHERE chat_session_id = $1
		  AND id <> $2
		  AND status IN ('acked', 'suppressed')
		ORDER BY COALESCE(completed_at, started_at, dispatched_at, created_at) DESC
		LIMIT 1`, chatSessionID, currentTaskID).Scan(&status, &reason)
	if err != nil || status != "failed" || strings.TrimSpace(reason) == "" {
		return "", false
	}
	return reason, true
}

func (h *Handler) chatSessionTokenTotal(ctx context.Context, chatSessionID pgtype.UUID) int64 {
	var total int64
	err := h.DB.QueryRow(ctx, `
		SELECT COALESCE(SUM(au.input_tokens + au.output_tokens + au.cache_read_tokens + au.cache_write_tokens), 0)::bigint
		FROM agent_usage au
		JOIN agent_execution ae ON ae.id = au.execution_id
		WHERE ae.chat_session_id = $1
		  AND au.source = 'chat'`, chatSessionID).Scan(&total)
	if err != nil {
		return 0
	}
	return total
}

func recentChatMessages(msgs []db.ChatMessage, limit int) []db.ChatMessage {
	if limit <= 0 || len(msgs) <= limit {
		return msgs
	}
	return msgs[len(msgs)-limit:]
}

func compactChatLine(s string) string {
	line := strings.Join(strings.Fields(s), " ")
	const max = 500
	if len(line) <= max {
		return line
	}
	return line[:max] + "..."
}

// ListPendingTasksByRuntime returns queued/dispatched tasks for a runtime.
func (h *Handler) ListPendingTasksByRuntime(w http.ResponseWriter, r *http.Request) {
	runtimeID := chi.URLParam(r, "runtimeId")

	// Verify the caller owns this runtime's workspace.
	runtime, ok := h.requireDaemonRuntimeAccess(w, r, runtimeID)
	if !ok {
		return
	}
	workspaceID := uuidToString(runtime.WorkspaceID)

	tasks, err := h.Queries.ListPendingTasksByRuntime(r.Context(), parseUUID(runtimeID))
	if err != nil {
		writeError(w, http.StatusInternalServerError, "failed to list pending tasks")
		return
	}

	resp := make([]AgentTaskResponse, len(tasks))
	for i, t := range tasks {
		resp[i] = taskToResponse(t, workspaceID)
	}

	writeJSON(w, http.StatusOK, resp)
}

// ---------------------------------------------------------------------------
// Task Lifecycle (called by daemon)
// ---------------------------------------------------------------------------

// ReportTaskProgress broadcasts a progress update.
type TaskProgressRequest struct {
	Summary string `json:"summary"`
	Step    int    `json:"step"`
	Total   int    `json:"total"`
}

func (h *Handler) ReportTaskProgress(w http.ResponseWriter, r *http.Request) {
	taskID := chi.URLParam(r, "taskId")

	var req TaskProgressRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid request body")
		return
	}
	// Verify ownership and resolve workspace ID.
	task, ok := h.requireDaemonTaskAccess(w, r, taskID)
	if !ok {
		return
	}

	workspaceID := ""
	if task.IssueID.Valid {
		if issue, err := h.Queries.GetIssue(r.Context(), task.IssueID); err == nil {
			workspaceID = uuidToString(issue.WorkspaceID)
		}
	}

	h.TaskService.ReportProgress(r.Context(), taskID, workspaceID, req.Summary, req.Step, req.Total)
	writeJSON(w, http.StatusOK, map[string]string{"status": "ok"})
}

// CompleteTask marks a running task as completed.
type TaskCompleteRequest struct {
	PRURL                  string                        `json:"pr_url"`
	Output                 string                        `json:"output"`
	Action                 string                        `json:"action"`
	Target                 string                        `json:"target"`
	Type                   string                        `json:"type"`
	Parts                  []protocol.MessagePart        `json:"parts"`
	Reaction               *protocol.ChatReactionPayload `json:"reaction"`
	OutputSuppressedReason string                        `json:"output_suppressed_reason,omitempty"`
	TransportAttempted     bool                          `json:"transport_attempted,omitempty"`
	SessionID              string                        `json:"session_id"` // Claude session ID for future resumption
	WorkDir                string                        `json:"work_dir"`   // working directory used during execution
	RuntimeStats           *protocol.RuntimeTokenStats   `json:"runtime_stats,omitempty"`
	InternalOutput         json.RawMessage               `json:"internal_output,omitempty"`
}

func (h *Handler) persistChatRuntimeTokenStats(ctx context.Context, chatSessionID pgtype.UUID, stats *protocol.RuntimeTokenStats) {
	if stats == nil || !chatSessionID.Valid {
		return
	}
	copy := *stats
	copy.UpdatedAt = time.Now().UTC().Format(time.RFC3339)
	raw, err := json.Marshal(copy)
	if err != nil {
		return
	}
	if _, err := h.DB.Exec(ctx, `UPDATE chat_session SET runtime_token_stats = $2, updated_at = now() WHERE id = $1`, chatSessionID, raw); err != nil {
		slog.Warn("persist chat runtime token stats failed", "chat_session_id", uuidToString(chatSessionID), "error", err)
	}
}

func (h *Handler) normalizeTaskCompleteOutput(ctx context.Context, task db.AgentInboxEvent, req *TaskCompleteRequest) error {
	channelTask := h.isChannelAgentTask(ctx, task)
	explicitAction := strings.TrimSpace(req.Action) != ""

	output, parts, err := messageparts.Normalize(req.Output, req.Parts)
	if err != nil {
		return fmt.Errorf("invalid message parts: %w", err)
	}
	if channelTask && !explicitAction && isLegacyChannelProtocolOutput(output, parts) {
		slog.Warn("complete task: suppressing protocol-shaped final text output", "task_id", uuidToString(task.ID), "agent_id", uuidToString(task.AgentID), "output_suppressed_reason", protocol.ChannelOutputSuppressedReasonLegacyProtocolOutput)
		h.suppressChannelTaskCompleteOutput(ctx, task, req, protocol.ChannelOutputSuppressedReasonLegacyProtocolOutput)
		return nil
	}
	if channelTask && strings.TrimSpace(req.Action) == "" {
		legacyType, legacyErr := protocol.NormalizeChatOutputType(req.Type, strings.TrimSpace(output) != "" || len(parts) > 0, req.Reaction != nil)
		if legacyErr == nil {
			switch legacyType {
			case protocol.ChatOutputKindReaction:
				req.Action = protocol.ChatOutputActionMessageReact
				req.Type = protocol.ChatOutputKindReaction
				output = ""
				parts = nil
				explicitAction = true
			case protocol.ChatOutputKindNoReply:
				req.Action = protocol.ChatOutputActionNoReply
				req.Type = protocol.ChatOutputKindNoReply
				output = ""
				parts = nil
				explicitAction = true
			}
		}
	}

	outputType := ""
	if strings.TrimSpace(req.Action) != "" {
		normalizedAction, actionErr := protocol.NormalizeChatOutputAction(req.Action)
		if actionErr != nil {
			if channelTask {
				slog.Warn("complete task: suppressing invalid channel output action", "task_id", uuidToString(task.ID), "action", req.Action, "error", actionErr)
				h.suppressChannelTaskCompleteOutput(ctx, task, req, protocol.ChannelOutputSuppressedReasonInvalidAction)
				return nil
			}
			return actionErr
		}
		req.Action = normalizedAction
		outputType, err = protocol.ChatOutputTypeForAction(normalizedAction)
	} else {
		outputType, err = protocol.NormalizeChatOutputType(req.Type, strings.TrimSpace(output) != "" || len(parts) > 0, req.Reaction != nil)
	}
	if err != nil {
		if channelTask {
			slog.Warn("complete task: suppressing invalid channel output type", "task_id", uuidToString(task.ID), "output_type", req.Type, "error", err)
			h.suppressChannelTaskCompleteOutput(ctx, task, req, protocol.ChannelOutputSuppressedReasonInvalidType)
			return nil
		}
		return err
	}
	// Channel visibility is owned by the agent transport (the same explicit
	// message-send boundary exposed to agents as Raft). A task completion is a
	// terminal status report, never a second message-writing path. In
	// particular, neither a plain final string nor completion-level
	// message_send/message_react may be bridged into the channel.
	if channelTask && (outputType == protocol.ChatOutputKindMessage || outputType == protocol.ChatOutputKindReaction) {
		slog.Warn("complete task: suppressing channel output without agent transport send", "task_id", uuidToString(task.ID), "agent_id", uuidToString(task.AgentID), "action", req.Action, "output_suppressed_reason", protocol.ChannelOutputSuppressedReasonUnsentFinalOutput)
		h.suppressChannelTaskCompleteOutput(ctx, task, req, protocol.ChannelOutputSuppressedReasonUnsentFinalOutput)
		return nil
	}
	if outputType != protocol.ChatOutputKindMessage {
		output = ""
		parts = nil
	}
	if channelTask && outputType == protocol.ChatOutputKindMessage && strings.TrimSpace(output) == "" && len(parts) == 0 {
		slog.Warn("complete task: suppressing empty channel send action", "task_id", uuidToString(task.ID), "agent_id", uuidToString(task.AgentID))
		h.suppressChannelTaskCompleteOutput(ctx, task, req, protocol.ChannelOutputSuppressedReasonEmptyMessage)
		return nil
	}
	if channelTask && outputType == protocol.ChatOutputKindReaction && (req.Reaction == nil || strings.TrimSpace(req.Reaction.Emoji) == "") {
		slog.Warn("complete task: suppressing invalid channel message react action", "task_id", uuidToString(task.ID), "agent_id", uuidToString(task.AgentID))
		h.suppressChannelTaskCompleteOutput(ctx, task, req, protocol.ChannelOutputSuppressedReasonInvalidReaction)
		return nil
	}
	req.Output = output
	req.Type = outputType
	req.Parts = parts
	req.Target = strings.TrimSpace(req.Target)
	if channelTask && outputType == protocol.ChatOutputKindMessage {
		if err := h.validateChatOutputTarget(ctx, task, req.Target); err != nil {
			slog.Warn("complete task: suppressing invalid channel output target", "task_id", uuidToString(task.ID), "agent_id", uuidToString(task.AgentID), "target", req.Target, "error", err)
			h.suppressChannelTaskCompleteOutput(ctx, task, req, protocol.ChannelOutputSuppressedReasonInvalidTarget)
			return nil
		}
	} else if strings.TrimSpace(req.Target) != "" {
		req.Target = ""
	}
	return nil
}

func agentRuntimeHasCapability(rt db.AgentRuntime, capability string) bool {
	for _, candidate := range runtimeCapabilities(runtimeMetadata(rt)) {
		if candidate == capability {
			return true
		}
	}
	return false
}

func isLegacyChannelProtocolOutput(output string, parts []protocol.MessagePart) bool {
	if _, _, unwrapped, err := messageparts.UnwrapStructuredMessageSend(output, parts); unwrapped || err != nil {
		return true
	}
	trimmed := strings.TrimSpace(output)
	if trimmed == "" {
		return false
	}
	if isLegacyChannelCLIOutput(trimmed) {
		return true
	}
	var envelope struct {
		Action   string                        `json:"action"`
		Target   string                        `json:"target"`
		Type     string                        `json:"type"`
		Parts    []protocol.MessagePart        `json:"parts"`
		Reaction *protocol.ChatReactionPayload `json:"reaction"`
	}
	if strings.HasPrefix(trimmed, "{") && json.Unmarshal([]byte(trimmed), &envelope) == nil {
		return strings.TrimSpace(envelope.Action) != "" ||
			strings.TrimSpace(envelope.Target) != "" ||
			strings.TrimSpace(envelope.Type) != "" ||
			len(envelope.Parts) > 0 ||
			envelope.Reaction != nil
	}
	lower := strings.ToLower(trimmed)
	return strings.Contains(lower, `{"action":"message_send"`) ||
		strings.Contains(lower, `{"action":"send"`) ||
		strings.Contains(lower, `{"action":"message_react"`) ||
		strings.Contains(lower, `{"action":"react"`) ||
		strings.Contains(lower, `{"action":"no_reply"`)
}

func isLegacyChannelCLIOutput(output string) bool {
	lower := strings.ToLower(strings.TrimSpace(output))
	for _, prefix := range []string{
		"multica send",
		"multica react",
		"multica message send",
		"multica message react",
		"multica channel send",
		"multica channel react",
	} {
		if lower == prefix || strings.HasPrefix(lower, prefix+" ") {
			return true
		}
	}
	return false
}

func (h *Handler) suppressTaskCompleteOutput(req *TaskCompleteRequest, reason string) {
	req.Action = protocol.ChatOutputActionNoReply
	req.Type = protocol.ChatOutputKindNoReply
	req.Output = ""
	req.Parts = nil
	req.Target = ""
	req.Reaction = nil
	req.OutputSuppressedReason = reason
	if h.Metrics != nil {
		h.Metrics.RecordChannelOutputSuppressed(reason)
	}
}

// suppressChannelTaskCompleteOutput normalizes completion-only channel output
// to no_reply.
func (h *Handler) suppressChannelTaskCompleteOutput(_ context.Context, _ db.AgentInboxEvent, req *TaskCompleteRequest, reason string) {
	h.suppressTaskCompleteOutput(req, reason)
}

func (h *Handler) isChannelAgentTask(ctx context.Context, task db.AgentInboxEvent) bool {
	// LRM-1079 / LRM-1080: product surface is channel_id. A wake that already
	// binds a channel is a channel task even when chat_session_id is absent
	// (ambient / role-changed / future session-free paths).
	if task.ChannelID.Valid {
		return true
	}
	if !task.ChatSessionID.Valid {
		return false
	}
	var exists bool
	if err := h.DB.QueryRow(ctx, `
		SELECT EXISTS (
			SELECT 1
			FROM channel_agent_session
			WHERE chat_session_id = $1
		)
	`, task.ChatSessionID).Scan(&exists); err != nil {
		if !isNotFound(err) {
			slog.Warn("complete task: failed to resolve channel task", "task_id", uuidToString(task.ID), "chat_session_id", uuidToString(task.ChatSessionID), "error", err)
		}
		return false
	}
	return exists
}

// emitIssueExecutedOnFirstCompletion atomically flips issue.first_executed_at
// and fires the issue_executed analytics event iff this is the first task on
// the issue to reach terminal done. Retries / re-assignments / comment-
// triggered follow-ups hit the WHERE first_executed_at IS NULL clause and
// no-op, so the funnel counts unique issues, not tasks.
func (h *Handler) emitIssueExecutedOnFirstCompletion(r *http.Request, task *db.AgentInboxEvent) {
	if task == nil {
		return
	}
	marked, err := h.Queries.MarkIssueFirstExecuted(r.Context(), task.IssueID)
	if err != nil {
		if !isNotFound(err) {
			slog.Warn("analytics: mark issue first-executed failed", "issue_id", uuidToString(task.IssueID), "error", err)
		}
		return
	}
	var durationMS int64
	if task.StartedAt.Valid && task.CompletedAt.Valid {
		durationMS = task.CompletedAt.Time.Sub(task.StartedAt.Time).Milliseconds()
	}
	taskContext := h.TaskService.AnalyticsContextForTask(r.Context(), *task)
	// distinct_id prefers the human creator so agent-driven events flow into
	// the issue-author's person profile (same place signup and
	// workspace_created land). Agent-created issues keep the agent id with a
	// prefix so PostHog doesn't merge them into a user by accident.
	distinct := uuidToString(marked.CreatorID)
	if marked.CreatorType == "agent" {
		distinct = "agent:" + distinct
	}
	obsmetrics.RecordEvent(h.Analytics, h.Metrics, analytics.IssueExecuted(
		distinct,
		uuidToString(marked.WorkspaceID),
		uuidToString(marked.ID),
		uuidToString(task.ID),
		uuidToString(task.AgentID),
		taskContext.Source,
		taskContext.RuntimeMode,
		taskContext.Provider,
		durationMS,
	))
}

// AgentUsagePayload is the token ledger payload reported by a daemon after an
// inbox execution.
type AgentUsagePayload struct {
	Provider         string `json:"provider"`
	Model            string `json:"model"`
	InputTokens      int64  `json:"input_tokens"`
	OutputTokens     int64  `json:"output_tokens"`
	CacheReadTokens  int64  `json:"cache_read_tokens"`
	CacheWriteTokens int64  `json:"cache_write_tokens"`
}

// GetTaskStatus returns the current status of a task.
// Used by the daemon to detect terminal/interruption signals (cancelled,
// failed, completed) while a task is executing mid-flight.
func (h *Handler) GetTaskStatus(w http.ResponseWriter, r *http.Request) {
	taskID := chi.URLParam(r, "taskId")

	// Verify the caller owns this task's workspace.
	task, ok := h.requireDaemonTaskAccess(w, r, taskID)
	if !ok {
		return
	}

	status := task.Status
	switch task.Status {
	case "pending", "failed":
		status = "queued"
	case "draining":
		status = "running"
	case "suppressed":
		status = "cancelled"
	case "acked":
		status = "completed"
		if task.TerminalOutcome.Valid && task.TerminalOutcome.String == "failed" {
			status = "failed"
		}
	}
	writeJSON(w, http.StatusOK, map[string]string{"status": status})
}

// ---------------------------------------------------------------------------
// Task Messages (live agent output)
// ---------------------------------------------------------------------------

type TaskMessageRequest struct {
	Seq     int            `json:"seq"`
	Type    string         `json:"type"`
	Tool    string         `json:"tool,omitempty"`
	CallID  string         `json:"call_id,omitempty"`
	Content string         `json:"content,omitempty"`
	Lineage string         `json:"lineage,omitempty"`
	Input   map[string]any `json:"input,omitempty"`
	Output  string         `json:"output,omitempty"`
}

type TaskMessageBatchRequest struct {
	Messages []TaskMessageRequest `json:"messages"`
}

// ReportTaskMessages receives a batch of agent execution messages from the daemon.
func (h *Handler) ReportTaskMessages(w http.ResponseWriter, r *http.Request) {
	taskID := chi.URLParam(r, "taskId")

	var req TaskMessageBatchRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid request body")
		return
	}
	if len(req.Messages) == 0 {
		writeJSON(w, http.StatusOK, map[string]string{"status": "ok"})
		return
	}

	// Verify the caller owns this task's workspace.
	task, ok := h.requireDaemonTaskAccess(w, r, taskID)
	if !ok {
		return
	}

	workspaceID := ""
	if task.IssueID.Valid {
		if issue, err := h.Queries.GetIssue(r.Context(), task.IssueID); err == nil {
			workspaceID = uuidToString(issue.WorkspaceID)
		}
	}
	if workspaceID == "" && task.ChatSessionID.Valid {
		if cs, err := h.Queries.GetChatSession(r.Context(), task.ChatSessionID); err == nil {
			workspaceID = uuidToString(cs.WorkspaceID)
		}
	}

	for _, msg := range req.Messages {
		// Redact sensitive information before persisting or broadcasting.
		msg.Content = redact.Text(msg.Content)
		msg.Output = redact.Text(msg.Output)
		msg.Input = redact.InputMap(msg.Input)
		if canonicalTool, known := taskMessageCanonicalToolName(msg.Tool, msg.Input); known {
			msg.Tool = canonicalTool
		}
		visibility := taskMessageRequestVisibility(msg)

		var inputJSON []byte
		if msg.Input != nil {
			inputJSON, _ = json.Marshal(msg.Input)
		}
		created, createErr := h.Queries.CreateTaskMessage(r.Context(), db.CreateTaskMessageParams{
			TaskID:     parseUUID(taskID),
			Seq:        int32(msg.Seq),
			Type:       msg.Type,
			Tool:       pgtype.Text{String: msg.Tool, Valid: msg.Tool != ""},
			Content:    pgtype.Text{String: msg.Content, Valid: msg.Content != ""},
			Input:      inputJSON,
			Output:     pgtype.Text{String: msg.Output, Valid: msg.Output != ""},
			Visibility: visibility,
		})
		if createErr != nil {
			slog.Error("failed to create task message", "task_id", taskID, "seq", msg.Seq, "error", createErr)
			writeError(w, http.StatusInternalServerError, "failed to persist task message")
			return
		}

		if workspaceID != "" {
			if visibility == "user_facing" {
				h.publishTask(protocol.EventTaskMessage, workspaceID, "system", "", taskID,
					taskMessageToPayload(created, taskID, uuidToString(task.IssueID)))
			}
		}
	}

	writeJSON(w, http.StatusOK, map[string]string{"status": "ok"})
}

func taskMessageToPayload(m db.TaskMessage, taskID, issueID string) protocol.TaskMessagePayload {
	var input map[string]any
	if m.Input != nil {
		json.Unmarshal(m.Input, &input)
	}
	createdAt := ""
	if m.CreatedAt.Valid {
		createdAt = m.CreatedAt.Time.UTC().Format(time.RFC3339Nano)
	}
	visibility := strings.TrimSpace(m.Visibility)
	if visibility == "" {
		visibility = "user_facing"
	}
	return protocol.TaskMessagePayload{
		TaskID:     taskID,
		IssueID:    issueID,
		Seq:        int(m.Seq),
		Type:       m.Type,
		Tool:       m.Tool.String,
		Content:    m.Content.String,
		Input:      input,
		Output:     m.Output.String,
		Visibility: visibility,
		CreatedAt:  createdAt,
	}
}

type inboxEventTaskMessageRow struct {
	Seq        int32
	Type       string
	Tool       string
	Content    string
	Input      []byte
	Output     string
	Visibility string
	CreatedAt  pgtype.Timestamptz
}

func inboxEventTaskMessageToPayload(row inboxEventTaskMessageRow, taskID string) protocol.TaskMessagePayload {
	var input map[string]any
	if len(row.Input) > 0 {
		json.Unmarshal(row.Input, &input)
	}
	createdAt := ""
	if row.CreatedAt.Valid {
		createdAt = row.CreatedAt.Time.UTC().Format(time.RFC3339Nano)
	}
	visibility := strings.TrimSpace(row.Visibility)
	if visibility == "" {
		visibility = "user_facing"
	}
	return protocol.TaskMessagePayload{
		TaskID:     taskID,
		Seq:        int(row.Seq),
		Type:       row.Type,
		Tool:       row.Tool,
		Content:    row.Content,
		Input:      input,
		Output:     row.Output,
		Visibility: visibility,
		CreatedAt:  createdAt,
	}
}

func taskMessageVisibility(msgType string) string {
	return taskMessageVisibilityForMessage(msgType, "", nil)
}

func taskMessageRequestVisibility(msg TaskMessageRequest) string {
	return taskMessageVisibilityForMessage(msg.Type, msg.Tool, msg.Input)
}

func taskMessageIsPhaseStatus(messageType, content string) bool {
	return messageType == "thinking" && strings.TrimSpace(content) == ""
}

func taskMessageVisibleToUser(messageType, content, visibility string) bool {
	_ = messageType
	_ = content
	return strings.TrimSpace(visibility) == "user_facing"
}

func taskMessageVisibilityForMessage(msgType, tool string, input map[string]any) string {
	if msgType == "compaction_started" || msgType == "compaction_finished" {
		return "diagnostic_only"
	}
	// Provider thinking and tool_result / log are diagnostic-only. Tool-use
	// activities retain their established mapped visibility below.
	if msgType == "thinking" || msgType == "tool_result" || msgType == "log" {
		return "diagnostic_only"
	}
	if !taskMessageToolIsMapped(msgType, tool, input) {
		return "diagnostic_only"
	}
	return "user_facing"
}

// ListTaskMessages returns the persisted messages for a task (for catch-up after reconnect).
func (h *Handler) ListTaskMessages(w http.ResponseWriter, r *http.Request) {
	taskID := chi.URLParam(r, "taskId")

	// Verify the caller owns this task's workspace.
	task, ok := h.requireDaemonTaskAccess(w, r, taskID)
	if !ok {
		return
	}

	var (
		messages []db.TaskMessage
		err      error
	)
	if sinceStr := r.URL.Query().Get("since"); sinceStr != "" {
		sinceSeq, parseErr := strconv.Atoi(sinceStr)
		if parseErr != nil {
			writeError(w, http.StatusBadRequest, "invalid since parameter")
			return
		}
		messages, err = h.Queries.ListTaskMessagesSince(r.Context(), db.ListTaskMessagesSinceParams{
			TaskID: parseUUID(taskID),
			Seq:    int32(sinceSeq),
		})
	} else {
		messages, err = h.Queries.ListTaskMessages(r.Context(), parseUUID(taskID))
	}
	if err != nil {
		writeError(w, http.StatusInternalServerError, "failed to list task messages")
		return
	}

	issueID := uuidToString(task.IssueID)

	resp := make([]protocol.TaskMessagePayload, len(messages))
	for i, m := range messages {
		resp[i] = taskMessageToPayload(m, taskID, issueID)
	}

	writeJSON(w, http.StatusOK, resp)
}

// GetActiveTaskForIssue returns all currently active tasks for an issue.
// Returns { tasks: [...] } array (may be empty).
func (h *Handler) GetActiveTaskForIssue(w http.ResponseWriter, r *http.Request) {
	issueID := chi.URLParam(r, "id")
	issue, ok := h.loadIssueForUser(w, r, issueID)
	if !ok {
		return
	}

	tasks, err := h.Queries.ListActiveTasksByIssue(r.Context(), issue.ID)
	if err != nil {
		tasks = nil
	}

	workspaceID := uuidToString(issue.WorkspaceID)
	resp := make([]AgentTaskResponse, len(tasks))
	for i, t := range tasks {
		resp[i] = taskToResponse(t, workspaceID)
	}

	writeJSON(w, http.StatusOK, map[string]any{"tasks": resp})
}

// CancelTask cancels a running or queued task by ID.
// Verifies both that the URL-parameter issue belongs to the caller's workspace
// and that the task belongs to that same issue — a task UUID from a different
// issue (in any workspace) must not be cancellable through this route.
func (h *Handler) CancelTask(w http.ResponseWriter, r *http.Request) {
	issueID := chi.URLParam(r, "id")
	issue, ok := h.loadIssueForUser(w, r, issueID)
	if !ok {
		return
	}

	taskID := chi.URLParam(r, "taskId")
	existing, err := h.Queries.GetAgentTask(r.Context(), parseUUID(taskID))
	if err != nil || uuidToString(existing.IssueID) != uuidToString(issue.ID) {
		writeError(w, http.StatusNotFound, "task not found")
		return
	}

	task, err := h.TaskService.CancelTask(r.Context(), existing.ID)
	if err != nil {
		slog.Warn("cancel task failed", "task_id", taskID, "error", err)
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}

	slog.Info("task cancelled by user", "task_id", taskID, "issue_id", uuidToString(task.IssueID))

	writeJSON(w, http.StatusOK, taskToResponse(*task, uuidToString(issue.WorkspaceID)))
}

// ListTasksByIssue returns all tasks (any status) for an issue — used for execution history.
func (h *Handler) ListTasksByIssue(w http.ResponseWriter, r *http.Request) {
	issueID := chi.URLParam(r, "id")
	issue, ok := h.loadIssueForUser(w, r, issueID)
	if !ok {
		return
	}

	tasks, err := h.Queries.ListTasksByIssue(r.Context(), issue.ID)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "failed to list tasks")
		return
	}

	workspaceID := uuidToString(issue.WorkspaceID)
	resp := make([]AgentTaskResponse, len(tasks))
	for i, t := range tasks {
		resp[i] = taskToResponse(t, workspaceID)
	}

	writeJSON(w, http.StatusOK, resp)
}

// ListAgentTaskMessages — GET /api/agent/tasks/{taskId}/messages.
// Dedicated agent read of the existing workspace-scoped task message model.
func (h *Handler) ListAgentTaskMessages(w http.ResponseWriter, r *http.Request) {
	if _, ok := h.requireAgentPrincipal(w, r); !ok {
		return
	}
	h.ListTaskMessagesByUser(w, r)
}

// ListTaskMessagesByUser returns task messages for a task.
// Used by the frontend under regular user auth and by the dedicated agent
// wrapper above. Verifies the task belongs to the caller's workspace.
func (h *Handler) ListTaskMessagesByUser(w http.ResponseWriter, r *http.Request) {
	taskID := chi.URLParam(r, "taskId")
	taskUUID, ok := parseUUIDOrBadRequest(w, taskID, "task_id")
	if !ok {
		return
	}

	task, err := h.Queries.GetAgentTask(r.Context(), taskUUID)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			resp, found, inboxErr := h.listInboxEventTaskMessagesByUser(r.Context(), taskUUID, taskID, middleware.WorkspaceIDFromContext(r.Context()), r.URL.Query().Get("since"))
			if inboxErr != nil {
				if errors.Is(inboxErr, errInvalidTaskMessageSince) {
					writeError(w, http.StatusBadRequest, "invalid since parameter")
					return
				}
				writeError(w, http.StatusInternalServerError, "failed to list task messages")
				return
			}
			if found {
				writeJSON(w, http.StatusOK, resp)
				return
			}
		}
		writeError(w, http.StatusNotFound, "task not found")
		return
	}

	// Verify the task belongs to the caller's workspace.
	wsID := h.TaskService.ResolveTaskWorkspaceID(r.Context(), task)
	if wsID == "" || wsID != middleware.WorkspaceIDFromContext(r.Context()) {
		writeError(w, http.StatusNotFound, "task not found")
		return
	}
	if projected, projectErr := h.projectInboxEventTaskMessages(
		r.Context(),
		taskUUID,
		taskID,
		parseUUID(wsID),
		r.URL.Query().Get("since"),
	); projectErr != nil {
		if errors.Is(projectErr, errInvalidTaskMessageSince) {
			writeError(w, http.StatusBadRequest, "invalid since parameter")
			return
		}
		writeError(w, http.StatusInternalServerError, "failed to list task messages")
		return
	} else if len(projected) > 0 {
		writeJSON(w, http.StatusOK, projected)
		return
	}

	var (
		messages []db.TaskMessage
		queryErr error
	)
	if sinceStr := r.URL.Query().Get("since"); sinceStr != "" {
		sinceSeq, parseErr := strconv.Atoi(sinceStr)
		if parseErr != nil {
			writeError(w, http.StatusBadRequest, "invalid since parameter")
			return
		}
		messages, queryErr = h.Queries.ListTaskMessagesSince(r.Context(), db.ListTaskMessagesSinceParams{
			TaskID: taskUUID,
			Seq:    int32(sinceSeq),
		})
	} else {
		messages, queryErr = h.Queries.ListTaskMessages(r.Context(), taskUUID)
	}
	if queryErr != nil {
		writeError(w, http.StatusInternalServerError, "failed to list task messages")
		return
	}

	issueID := uuidToString(task.IssueID)

	resp := make([]protocol.TaskMessagePayload, 0, len(messages))
	for _, m := range messages {
		if !taskMessageVisibleToUser(m.Type, m.Content.String, m.Visibility) {
			continue
		}
		resp = append(resp, taskMessageToPayload(m, taskID, issueID))
	}

	writeJSON(w, http.StatusOK, resp)
}

func (h *Handler) listInboxEventTaskMessagesByUser(ctx context.Context, eventID pgtype.UUID, taskID, workspaceID, sinceStr string) ([]protocol.TaskMessagePayload, bool, error) {
	if h == nil || h.DB == nil {
		return nil, false, nil
	}
	if strings.TrimSpace(workspaceID) == "" {
		return nil, false, nil
	}
	workspaceUUID := parseUUID(workspaceID)
	var exists bool
	if err := h.DB.QueryRow(ctx, `
		SELECT EXISTS (
			SELECT 1
			FROM agent_inbox_event
			WHERE id = $1
			  AND workspace_id = $2
		)
	`, eventID, workspaceUUID).Scan(&exists); err != nil {
		return nil, false, err
	}
	if !exists {
		return nil, false, nil
	}

	payloads, err := h.projectInboxEventTaskMessages(ctx, eventID, taskID, workspaceUUID, sinceStr)
	return payloads, true, err
}

func (h *Handler) projectInboxEventTaskMessages(ctx context.Context, eventID pgtype.UUID, taskID string, workspaceUUID pgtype.UUID, sinceStr string) ([]protocol.TaskMessagePayload, error) {
	if sinceStr != "" {
		if _, err := strconv.Atoi(sinceStr); err != nil {
			return nil, errInvalidTaskMessageSince
		}
	}
	// The legacy table-backed projection is intentionally gone. Runner
	// snapshots and entries are the Activity history; task transcripts retain
	// only their dedicated task-message storage.
	return []protocol.TaskMessagePayload{}, nil
}

// GetIssueUsage returns aggregated token usage for all tasks belonging to an issue.
func (h *Handler) GetIssueUsage(w http.ResponseWriter, r *http.Request) {
	issueID := chi.URLParam(r, "id")
	issue, ok := h.loadIssueForUser(w, r, issueID)
	if !ok {
		return
	}

	row, err := h.Queries.GetIssueUsageSummary(r.Context(), issue.ID)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "failed to get issue usage")
		return
	}

	writeJSON(w, http.StatusOK, map[string]any{
		"total_input_tokens":       row.TotalInputTokens,
		"total_output_tokens":      row.TotalOutputTokens,
		"total_cache_read_tokens":  row.TotalCacheReadTokens,
		"total_cache_write_tokens": row.TotalCacheWriteTokens,
		"task_count":               row.TaskCount,
	})
}
