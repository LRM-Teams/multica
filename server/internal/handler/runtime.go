package handler

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"net/http"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/go-chi/chi/v5"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/multica-ai/multica/server/internal/cli"
	"github.com/multica-ai/multica/server/internal/util"
	"github.com/multica-ai/multica/server/pkg/agent"
	db "github.com/multica-ai/multica/server/pkg/db/generated"
	"github.com/multica-ai/multica/server/pkg/protocol"
)

type AgentRuntimeResponse struct {
	ID          string  `json:"id"`
	WorkspaceID string  `json:"workspace_id"`
	DaemonID    *string `json:"daemon_id"`
	Name        string  `json:"name"`
	// DisplayName is the user-editable machine label. Empty means unset —
	// clients should fall back to Name (daemon hostname / reported label).
	// Daemon register/upsert never overwrites a non-empty value.
	DisplayName  string `json:"display_name"`
	RuntimeMode  string `json:"runtime_mode"`
	Provider     string `json:"provider"`
	LaunchHeader string `json:"launch_header"`
	// ProviderCapabilities is the FE-facing projection of
	// agent.ProviderCapabilities for this runtime's provider (task #62).
	// Distinct from Capabilities ([]string), which is the daemon protocol
	// advertise list. Older servers omit the object; treat missing as all-false.
	ProviderCapabilities ProviderCapabilitiesWire `json:"provider_capabilities"`
	Status               string                   `json:"status"`
	// DeviceInfo is the legacy composite string daemons still register
	// (e.g. "ubuntu · codex-cli 0.146.0").
	DeviceInfo string `json:"device_info"`
	// DeviceName is the machine label from registration (metadata.device_name).
	// Daemon already sends device_name separately; we persist it so clients
	// never re-parse device_info. Empty until the daemon re-registers after
	// this persist landed. Older servers omit the field.
	DeviceName string `json:"device_name"`
	// OS is the daemon-reported GOOS value from registration metadata. Older
	// daemons omit it, in which case clients must render an unknown value.
	OS             string   `json:"os"`
	Metadata       any      `json:"metadata"`
	Capabilities   []string `json:"capabilities"`
	CurrentVersion *string  `json:"current_version"`
	// DaemonTargetVersion is the one release target for the physical daemon.
	// Runtime TargetVersion below is retained only as a legacy lifecycle
	// projection; Computer clients must use this daemon-scoped field.
	DaemonTargetVersion *string `json:"daemon_target_version,omitempty"`
	TargetVersion       *string `json:"target_version,omitempty"`
	UpdateState         string  `json:"update_state"`
	RuntimeHealth       string  `json:"runtime_health"`
	UpdateError         *string `json:"update_error,omitempty"`
	// MachineUpgrade is the canonical daemon-scoped lifecycle. Older clients
	// ignore it; compatibility fields above continue to project legacy rows.
	MachineUpgrade *MachineUpgrade             `json:"machine_upgrade,omitempty"`
	AutoUpdate     *DaemonUpdateStatusResponse `json:"auto_update"`
	OwnerID        *string                     `json:"owner_id"`
	// Visibility is "private" (default — only the owner / workspace admins
	// can bind agents) or "public" (any workspace member can). See migration
	// 083 and canUseRuntimeForAgent.
	Visibility string  `json:"visibility"`
	LastSeenAt *string `json:"last_seen_at"`
	CreatedAt  string  `json:"created_at"`
	UpdatedAt  string  `json:"updated_at"`
	// ComputerConnected and DaemonLastSeenAt (task #58) reflect the physical
	// machine's own heartbeat, independent of this runtime's LastSeenAt —
	// see computerConnected's doc comment for why the two must not be
	// conflated. DaemonLastSeenAt is nil when the daemon has never sent a
	// heartbeat (e.g. a pre-#58 daemon binary, or a runtime with no
	// daemon_id).
	ComputerConnected bool    `json:"computer_connected"`
	DaemonLastSeenAt  *string `json:"daemon_last_seen_at"`
	// OfflineReason is the last recorded leave reason when Status is offline
	// (e.g. daemon_deregistered). Empty when online or never set. Distinguishes
	// "daemon not connected — start on the machine" from "no update available".
	OfflineReason *string `json:"offline_reason,omitempty"`
	// PinnedVersion (task #81) is non-nil when the daemon's
	// MULTICA_PINNED_VERSION reported this machine as pinned. This only
	// reflects the daemon's local intent — the server does not yet enforce
	// it against a server-initiated update, so UI copy must say "recorded
	// intent," not "guaranteed not to be upgraded."
	PinnedVersion *string `json:"pinned_version,omitempty"`
}

type DaemonUpdateStatusResponse struct {
	SessionID                  string  `json:"session_id"`
	Revision                   int64   `json:"revision"`
	ObservedAt                 string  `json:"observed_at"`
	AutoUpdateEffectiveEnabled bool    `json:"auto_update_effective_enabled"`
	ConfigSource               string  `json:"config_source"`
	IneligibleReason           *string `json:"ineligible_reason"`
	CheckIntervalSeconds       int64   `json:"check_interval_seconds"`
	Phase                      string  `json:"phase"`
	AttemptSource              *string `json:"attempt_source"`
	LastAttemptAt              *string `json:"last_attempt_at"`
	LastOutcome                string  `json:"last_outcome"`
	TargetVersion              *string `json:"target_version"`
	ErrorCode                  *string `json:"error_code"`
	ErrorMessage               *string `json:"error_message"`
	StagedVersion              *string `json:"staged_version"`
	ActivationGeneration       *int64  `json:"activation_generation"`
	ReceivedAt                 string  `json:"received_at"`
	UpdatedAt                  string  `json:"updated_at"`
}

func runtimeToResponse(rt db.AgentRuntime) AgentRuntimeResponse {
	return runtimeToResponseWithUpdate(rt, nil)
}

func (h *Handler) runtimeToResponse(ctx context.Context, rt db.AgentRuntime) AgentRuntimeResponse {
	update := h.latestRuntimeUpdate(ctx, rt)
	return h.runtimeToResponseWithResolvedUpdate(ctx, rt, update, h.latestMachineUpgrade(ctx, rt))
}

func (h *Handler) runtimeToResponseWithResolvedUpdate(ctx context.Context, rt db.AgentRuntime, update *UpdateRequest, machineUpgrade *MachineUpgrade) AgentRuntimeResponse {
	release := h.runtimeReleaseForResponse(ctx, rt, update)
	daemonHeartbeat := h.daemonHeartbeatForRuntime(ctx, rt)
	resp := runtimeToResponseWithUpdateReleaseAndObservation(rt, update, release, h.daemonUpdateStatusForRuntime(ctx, rt))
	resp.ComputerConnected = h.computerConnectedByRunner(runtimeDaemonKey(rt), uuidToString(rt.WorkspaceID), daemonHeartbeat, time.Now())
	if daemonHeartbeat != nil {
		resp.DaemonLastSeenAt = timestampToPtr(daemonHeartbeat.LastSeenAt)
	}
	resp.MachineUpgrade = machineUpgrade
	return resp
}

// attachDaemonTargetVersions gives every sibling runtime the same daemon
// release target. Prefer a currently available release over an old failed
// runtime operation; only use the latter when no available release exists so
// failure/retry UI can still name its target.
func attachDaemonTargetVersions(responses []AgentRuntimeResponse) {
	available := make(map[string]string)
	fallback := make(map[string]string)
	for _, resp := range responses {
		if resp.DaemonID == nil || strings.TrimSpace(*resp.DaemonID) == "" || resp.TargetVersion == nil {
			continue
		}
		daemonID := strings.TrimSpace(*resp.DaemonID)
		target := strings.TrimSpace(*resp.TargetVersion)
		if target == "" {
			continue
		}
		if resp.RuntimeHealth == "update_available" {
			if current, ok := available[daemonID]; !ok || cli.IsNewerVersion(target, current) {
				available[daemonID] = target
			}
			continue
		}
		if resp.RuntimeHealth == "failed" {
			if current, ok := fallback[daemonID]; !ok || cli.IsNewerVersion(target, current) {
				fallback[daemonID] = target
			}
		}
	}

	for i := range responses {
		if responses[i].DaemonID == nil {
			continue
		}
		daemonID := strings.TrimSpace(*responses[i].DaemonID)
		target := available[daemonID]
		if target == "" {
			target = fallback[daemonID]
		}
		if target == "" {
			responses[i].DaemonTargetVersion = nil
			continue
		}
		responses[i].DaemonTargetVersion = &target
	}
}

func (h *Handler) latestMachineUpgrade(ctx context.Context, rt db.AgentRuntime) *MachineUpgrade {
	if h == nil || h.MachineUpgradeStore == nil {
		return nil
	}
	op, err := h.MachineUpgradeStore.LatestForDaemon(ctx, runtimeDaemonKey(rt))
	if err != nil {
		slog.Warn("failed to load machine upgrade state", "error", err, "runtime_id", uuidToString(rt.ID))
		return nil
	}
	return op
}

func (h *Handler) machineUpgradesForList(ctx context.Context, runtimes []db.AgentRuntime) map[string]*MachineUpgrade {
	result := make(map[string]*MachineUpgrade)
	if h == nil || h.MachineUpgradeStore == nil || len(runtimes) == 0 {
		return result
	}
	daemons := make([]string, 0, len(runtimes))
	seen := make(map[string]struct{})
	for _, rt := range runtimes {
		if daemonID := runtimeDaemonKey(rt); daemonID != "" {
			if _, ok := seen[daemonID]; !ok {
				seen[daemonID] = struct{}{}
				daemons = append(daemons, daemonID)
			}
		}
	}
	upgrades, err := h.MachineUpgradeStore.LatestForDaemons(ctx, daemons)
	if err != nil {
		slog.Warn("failed to load machine upgrade states", "error", err)
		return result
	}
	return upgrades
}

// daemonHeartbeatForRuntime is the single-runtime counterpart to
// daemonHeartbeatsForList, for call sites that build one response at a time
// rather than a batch.
func (h *Handler) daemonHeartbeatForRuntime(ctx context.Context, rt db.AgentRuntime) *db.DaemonHeartbeat {
	daemonID := runtimeDaemonKey(rt)
	if h == nil || h.Queries == nil || daemonID == "" {
		return nil
	}
	hb, err := h.Queries.GetDaemonHeartbeat(ctx, db.GetDaemonHeartbeatParams{
		WorkspaceID: rt.WorkspaceID,
		DaemonID:    daemonID,
	})
	if err != nil {
		return nil
	}
	return &hb
}

func (h *Handler) latestRuntimeUpdate(ctx context.Context, rt db.AgentRuntime) *UpdateRequest {
	if h != nil && h.UpdateStore != nil {
		runtimeID := uuidToString(rt.ID)
		update, err := h.UpdateStore.LatestForRuntime(ctx, runtimeID)
		if err != nil {
			slog.Warn("failed to load runtime update state", "error", err, "runtime_id", runtimeID)
			return nil
		}
		return update
	}
	return nil
}

// runtimeUpdateBatchStore is intentionally optional so custom/test stores that
// only implement UpdateStore retain the single-runtime fallback.
type runtimeUpdateBatchStore interface {
	LatestForRuntimes(ctx context.Context, runtimeIDs []string) (map[string]*UpdateRequest, error)
}

func (h *Handler) runtimeUpdatesForList(ctx context.Context, runtimes []db.AgentRuntime) map[string]*UpdateRequest {
	updates := make(map[string]*UpdateRequest, len(runtimes))
	if h == nil || h.UpdateStore == nil || len(runtimes) == 0 {
		return updates
	}

	runtimeIDs := make([]string, 0, len(runtimes))
	for _, rt := range runtimes {
		runtimeIDs = append(runtimeIDs, uuidToString(rt.ID))
	}
	if batchStore, ok := h.UpdateStore.(runtimeUpdateBatchStore); ok {
		batch, err := batchStore.LatestForRuntimes(ctx, runtimeIDs)
		if err != nil {
			slog.Warn("failed to load runtime update states", "error", err)
			return coalesceRuntimeUpdatesByDaemon(runtimes, updates)
		}
		return coalesceRuntimeUpdatesByDaemon(runtimes, batch)
	}

	for _, runtimeID := range runtimeIDs {
		update, err := h.UpdateStore.LatestForRuntime(ctx, runtimeID)
		if err != nil {
			slog.Warn("failed to load runtime update state", "error", err, "runtime_id", runtimeID)
			continue
		}
		if update != nil {
			updates[runtimeID] = update
		}
	}
	return coalesceRuntimeUpdatesByDaemon(runtimes, updates)
}

func coalesceRuntimeUpdatesByDaemon(runtimes []db.AgentRuntime, updates map[string]*UpdateRequest) map[string]*UpdateRequest {
	resolved := make(map[string]*UpdateRequest, len(runtimes))
	for runtimeID, update := range updates {
		resolved[runtimeID] = update
	}

	latestByDaemon := map[string]*UpdateRequest{}
	for _, rt := range runtimes {
		daemonID := runtimeDaemonKey(rt)
		if daemonID == "" {
			continue
		}
		update := updates[uuidToString(rt.ID)]
		if newerRuntimeUpdate(update, latestByDaemon[daemonID]) {
			latestByDaemon[daemonID] = update
		}
	}

	for _, rt := range runtimes {
		daemonID := runtimeDaemonKey(rt)
		if daemonID == "" {
			continue
		}
		if update := latestByDaemon[daemonID]; update != nil {
			resolved[uuidToString(rt.ID)] = update
		}
	}
	return resolved
}

func runtimeDaemonKey(rt db.AgentRuntime) string {
	if !rt.DaemonID.Valid {
		return ""
	}
	return strings.TrimSpace(rt.DaemonID.String)
}

func newerRuntimeUpdate(candidate, current *UpdateRequest) bool {
	if candidate == nil {
		return false
	}
	if current == nil {
		return true
	}
	if candidate.UpdatedAt.After(current.UpdatedAt) {
		return true
	}
	if candidate.UpdatedAt.Equal(current.UpdatedAt) && candidate.CreatedAt.After(current.CreatedAt) {
		return true
	}
	return false
}

func runtimeToResponseWithUpdate(rt db.AgentRuntime, update *UpdateRequest) AgentRuntimeResponse {
	return runtimeToResponseWithUpdateAndRelease(rt, update, nil)
}

func runtimeToResponseWithUpdateAndRelease(rt db.AgentRuntime, update *UpdateRequest, release *RuntimeRelease) AgentRuntimeResponse {
	return runtimeToResponseWithUpdateReleaseAndObservation(rt, update, release, nil)
}

func runtimeToResponseWithUpdateReleaseAndObservation(
	rt db.AgentRuntime,
	update *UpdateRequest,
	release *RuntimeRelease,
	autoUpdate *DaemonUpdateStatusResponse,
) AgentRuntimeResponse {
	metadata := runtimeMetadata(rt)
	currentVersion := runtimeCurrentVersion(metadata)
	targetVersion, updateState := runtimeUpdateState(update, currentVersion)
	targetVersion, updateState, stagedWaiting := applyAutoUpdateRestartWindow(autoUpdate, currentVersion, targetVersion, updateState)
	now := time.Now()
	availableUpdateTarget := runtimeAvailableUpdateTarget(rt, metadata, currentVersion, targetVersion, updateState, release, now)
	runtimeHealth := deriveRuntimeHealth(rt, currentVersion, targetVersion, updateState, availableUpdateTarget, now)
	if runtimeHealth == "update_available" && availableUpdateTarget != nil {
		targetVersion = availableUpdateTarget
	}
	// Hard gate (P0 / task #120): never project an "upgrade" target that is not
	// strictly newer than the running CLI. Stale completed/failed rows, equal
	// versions, and lagging latest-cache hits must not light the upgrade button.
	targetVersion, runtimeHealth = clampUpgradeProjection(currentVersion, targetVersion, updateState, runtimeHealth)
	updateError := runtimeUpdateError(update, currentVersion, updateState)
	if stagedWaiting && updateError == nil {
		// Stable code for FE i18n: staged binary waiting for daemon reconnect/switch.
		msg := "update_staged_waiting_restart"
		updateError = &msg
	}
	return AgentRuntimeResponse{
		ID:                   uuidToString(rt.ID),
		WorkspaceID:          uuidToString(rt.WorkspaceID),
		DaemonID:             textToPtr(rt.DaemonID),
		Name:                 rt.Name,
		DisplayName:          rt.DisplayName,
		RuntimeMode:          rt.RuntimeMode,
		Provider:             rt.Provider,
		LaunchHeader:         agent.LaunchHeader(rt.Provider),
		ProviderCapabilities: providerCapabilitiesWire(rt.Provider),
		Status:               rt.Status,
		DeviceInfo:           rt.DeviceInfo,
		DeviceName:           deviceNameFromRuntime(rt.DeviceInfo, metadata),
		OS:                   operatingSystemFromRuntime(metadata),
		Metadata:             metadata,
		Capabilities:         runtimeCapabilities(metadata),
		CurrentVersion:       currentVersion,
		TargetVersion:        targetVersion,
		UpdateState:          updateState,
		RuntimeHealth:        runtimeHealth,
		UpdateError:          updateError,
		AutoUpdate:           autoUpdate,
		OwnerID:              uuidToPtr(rt.OwnerID),
		Visibility:           rt.Visibility,
		LastSeenAt:           timestampToPtr(rt.LastSeenAt),
		CreatedAt:            timestampToString(rt.CreatedAt),
		UpdatedAt:            timestampToString(rt.UpdatedAt),
		PinnedVersion:        nullableTextPtr(rt.PinnedVersion),
		OfflineReason:        nullableTextPtr(rt.OfflineReason),
	}
}

func (h *Handler) runtimeReleaseForResponse(ctx context.Context, rt db.AgentRuntime, update *UpdateRequest) *RuntimeRelease {
	if h == nil || h.RuntimeReleaseSource == nil {
		return nil
	}
	metadata := runtimeMetadata(rt)
	currentVersion := runtimeCurrentVersion(metadata)
	targetVersion, updateState := runtimeUpdateState(update, currentVersion)
	if !runtimeShouldFetchLatestRelease(rt, metadata, currentVersion, targetVersion, updateState, time.Now()) {
		return nil
	}
	release, err := h.RuntimeReleaseSource.Latest(ctx)
	if err != nil {
		slog.Warn("failed to load latest runtime release", "error", err, "runtime_id", uuidToString(rt.ID))
		return nil
	}
	return release
}

func runtimeMetadata(rt db.AgentRuntime) any {
	var metadata any
	if rt.Metadata != nil {
		json.Unmarshal(rt.Metadata, &metadata)
	}
	if metadata == nil {
		metadata = map[string]any{}
	}
	return metadata
}

func runtimeCurrentVersion(metadata any) *string {
	metadataMap, ok := metadata.(map[string]any)
	if !ok {
		return nil
	}
	value, ok := metadataMap["cli_version"].(string)
	if !ok || strings.TrimSpace(value) == "" {
		return nil
	}
	return &value
}

func runtimeUpdateState(update *UpdateRequest, currentVersion *string) (*string, string) {
	if update == nil {
		return nil, "idle"
	}
	targetVersion := update.TargetVersion
	switch update.Status {
	case UpdatePending:
		return &targetVersion, "pending"
	case UpdateRunning:
		return &targetVersion, "running"
	case UpdateCompleted:
		if completedUpdateConfirmationTimedOut(update, currentVersion, time.Now()) {
			return &targetVersion, "timed_out"
		}
		return &targetVersion, "completed"
	case UpdateReady:
		if runtimeVersionAtLeastTarget(currentVersion, &targetVersion) {
			return &targetVersion, "completed"
		}
		return &targetVersion, "ready_to_apply"
	case UpdateFailed:
		if runtimeVersionAtLeastTarget(currentVersion, &targetVersion) {
			return &targetVersion, "completed"
		}
		return &targetVersion, "failed"
	case UpdateTimeout:
		if runtimeVersionAtLeastTarget(currentVersion, &targetVersion) {
			return &targetVersion, "completed"
		}
		return &targetVersion, "timed_out"
	default:
		return &targetVersion, "idle"
	}
}

func completedUpdateConfirmationTimedOut(update *UpdateRequest, currentVersion *string, now time.Time) bool {
	if update == nil || update.Status != UpdateCompleted || update.UpdatedAt.IsZero() {
		return false
	}
	targetVersion := update.TargetVersion
	if runtimeVersionAtLeastTarget(currentVersion, &targetVersion) {
		return false
	}
	return now.Sub(update.UpdatedAt) > updateConfirmTimeout
}

func runtimeUpdateError(update *UpdateRequest, currentVersion *string, updateState string) *string {
	if update == nil {
		return nil
	}
	if updateState == "timed_out" && update.Status == UpdateCompleted {
		targetVersion := update.TargetVersion
		if !runtimeVersionAtLeastTarget(currentVersion, &targetVersion) {
			reason := "old_version_reported_after_update"
			return &reason
		}
	}
	// Surface the last terminal failure/timeout reason on the runtime
	// projection so a page refresh still shows human-readable cause
	// (Frank 2026-08-03). Key off derived updateState only — when a
	// failed/timeout attempt already matches target, runtimeUpdateState
	// remaps to "completed" and we must not keep a stale error that
	// masks the healthy register (see TestRuntimeToResponseRetained…).
	if updateState == "timed_out" || updateState == "failed" {
		reason := strings.TrimSpace(update.Error)
		if reason == "" {
			if updateState == "timed_out" {
				reason = "runtime_update_timed_out"
			} else {
				reason = "runtime_update_failed"
			}
		}
		return &reason
	}
	return nil
}

// applyAutoUpdateRestartWindow folds daemon observation (phase restart_pending /
// last_outcome update_succeeded with a staged target) into the per-runtime
// update projection so the Computer page does not show a bare old version while
// a staged binary waits for reconnect/switch (Frank upgrade-UX #1, 2026-08-03).
// Returns stagedWaiting=true when callers should attach a durable waiting code.
func applyAutoUpdateRestartWindow(
	autoUpdate *DaemonUpdateStatusResponse,
	currentVersion, targetVersion *string,
	updateState string,
) (*string, string, bool) {
	if autoUpdate == nil {
		return targetVersion, updateState, false
	}
	obsTarget := autoUpdate.TargetVersion
	if obsTarget == nil || strings.TrimSpace(*obsTarget) == "" {
		obsTarget = autoUpdate.StagedVersion
	}
	if obsTarget == nil || strings.TrimSpace(*obsTarget) == "" {
		return targetVersion, updateState, false
	}
	if runtimeVersionAtLeastTarget(currentVersion, obsTarget) {
		return targetVersion, updateState, false
	}
	phase := strings.TrimSpace(autoUpdate.Phase)
	outcome := strings.TrimSpace(autoUpdate.LastOutcome)
	waiting := phase == "restart_pending" || outcome == "update_succeeded"
	if !waiting {
		return targetVersion, updateState, false
	}
	// Prefer observation target when the per-runtime row is idle/missing.
	switch updateState {
	case "idle", "completed":
		updateState = "ready_to_apply"
		targetVersion = obsTarget
	case "ready_to_apply", "pending", "running", "failed", "timed_out":
		if targetVersion == nil || strings.TrimSpace(*targetVersion) == "" {
			targetVersion = obsTarget
		}
	}
	return targetVersion, updateState, waiting && (updateState == "ready_to_apply" || phase == "restart_pending")
}

func runtimeShouldFetchLatestRelease(rt db.AgentRuntime, metadata any, currentVersion, targetVersion *string, updateState string, now time.Time) bool {
	// Task #53: keyed off runtimeConnectivity (heartbeat freshness), not the
	// raw status column — that column can still read "online" for up to
	// ~180s after the runtime actually went silent (sweeper lag). See
	// deriveRuntimeHealth below for the same reasoning.
	if runtimeConnectivity(rt, now) != runtimeConnectivityOnline || rt.RuntimeMode != "local" || currentVersion == nil {
		return false
	}
	if launchedBy(metadata) == "desktop" {
		return false
	}
	if !cli.IsReleaseVersion(*currentVersion) {
		return false
	}
	switch updateState {
	case "idle":
		return true
	case "completed":
		return runtimeVersionAtLeastTarget(currentVersion, targetVersion)
	default:
		return false
	}
}

func runtimeAvailableUpdateTarget(rt db.AgentRuntime, metadata any, currentVersion, targetVersion *string, updateState string, release *RuntimeRelease, now time.Time) *string {
	if release == nil || release.TagName == "" {
		return nil
	}
	if !runtimeShouldFetchLatestRelease(rt, metadata, currentVersion, targetVersion, updateState, now) {
		return nil
	}
	if currentVersion == nil || !cli.IsNewerVersion(release.TagName, *currentVersion) {
		return nil
	}
	target := release.TagName
	return &target
}

// clampUpgradeProjection enforces IsNewer(target, current) on the API surface.
// In-flight updates (pending/running) keep their target so the UI can show
// progress; every other state drops a non-newer target and demotes
// update_available → ok.
func clampUpgradeProjection(currentVersion, targetVersion *string, updateState, runtimeHealth string) (*string, string) {
	switch updateState {
	case "pending", "running":
		return targetVersion, runtimeHealth
	}
	if targetVersion == nil {
		if runtimeHealth == "update_available" {
			return nil, "ok"
		}
		return nil, runtimeHealth
	}
	if currentVersion != nil && cli.IsNewerVersion(*targetVersion, *currentVersion) {
		return targetVersion, runtimeHealth
	}
	// target == current, target older, or unparsable → no upgrade affordance
	if runtimeHealth == "update_available" {
		runtimeHealth = "ok"
	}
	return nil, runtimeHealth
}

func launchedBy(metadata any) string {
	metadataMap, ok := metadata.(map[string]any)
	if !ok {
		return ""
	}
	value, _ := metadataMap["launched_by"].(string)
	return strings.TrimSpace(value)
}

func deriveRuntimeHealth(rt db.AgentRuntime, currentVersion, targetVersion *string, updateState string, availableUpdateTarget *string, now time.Time) string {
	// Task #53: was `rt.Status != "online"`, trusting the raw column
	// directly. That column can read "online" for up to ~180s after the
	// runtime actually went silent (sweeper lag), which produced a real,
	// user-visible bug: this health badge and the agent list's
	// runtime_display_status field (#1664, which already made this same
	// fix) could disagree about whether the same runtime was reachable.
	// runtimeConnectivity derives freshness from last_seen_at at read time
	// instead of trusting the lagging persisted value.
	if runtimeConnectivity(rt, now) != runtimeConnectivityOnline {
		return "offline"
	}
	switch updateState {
	case "pending", "running":
		return "updating"
	case "completed":
		if !runtimeVersionAtLeastTarget(currentVersion, targetVersion) {
			return "updating"
		}
		if availableUpdateTarget != nil {
			return "update_available"
		}
		return "ok"
	case "ready_to_apply":
		return "update_available"
	case "failed", "timed_out":
		return "failed"
	default:
		if availableUpdateTarget != nil {
			return "update_available"
		}
		return "ok"
	}
}

func versionsMatch(left, right *string) bool {
	if left == nil || right == nil {
		return false
	}
	normalize := func(value string) string {
		return strings.TrimPrefix(strings.TrimSpace(value), "v")
	}
	return normalize(*left) != "" && normalize(*left) == normalize(*right)
}

func runtimeVersionAtLeastTarget(current, target *string) bool {
	if versionsMatch(current, target) {
		return true
	}
	if current == nil || target == nil {
		return false
	}
	return cli.IsNewerVersion(*current, *target)
}

func runtimeCapabilities(metadata any) []string {
	metadataMap, ok := metadata.(map[string]any)
	if !ok {
		return []string{}
	}
	raw, ok := metadataMap["capabilities"]
	if !ok {
		return []string{}
	}
	switch capabilities := raw.(type) {
	case []string:
		return normalizeDaemonCapabilities(capabilities)
	case []any:
		values := make([]string, 0, len(capabilities))
		for _, capability := range capabilities {
			if value, ok := capability.(string); ok {
				values = append(values, value)
			}
		}
		return normalizeDaemonCapabilities(values)
	default:
		return []string{}
	}
}

// ---------------------------------------------------------------------------
// Runtime Usage
// ---------------------------------------------------------------------------

type RuntimeUsageResponse struct {
	RuntimeID        string `json:"runtime_id"`
	Date             string `json:"date"`
	Provider         string `json:"provider"`
	Model            string `json:"model"`
	InputTokens      int64  `json:"input_tokens"`
	OutputTokens     int64  `json:"output_tokens"`
	CacheReadTokens  int64  `json:"cache_read_tokens"`
	CacheWriteTokens int64  `json:"cache_write_tokens"`
}

// GetRuntimeUsage returns daily token usage for a runtime, aggregated from
// per-task usage records captured by the daemon. This is scoped to
// Daemon-executed tasks only (i.e. excludes users' local CLI usage of the
// same tool).
func (h *Handler) GetRuntimeUsage(w http.ResponseWriter, r *http.Request) {
	runtimeID := chi.URLParam(r, "runtimeId")
	runtimeUUID, ok := parseUUIDOrBadRequest(w, runtimeID, "runtime_id")
	if !ok {
		return
	}

	rt, err := h.Queries.GetAgentRuntime(r.Context(), runtimeUUID)
	if err != nil {
		writeError(w, http.StatusNotFound, "runtime not found")
		return
	}

	if _, ok := h.requireWorkspaceMember(w, r, uuidToString(rt.WorkspaceID), "runtime not found"); !ok {
		return
	}

	// All runtime reports render in the viewer's tz.
	viewTZ := h.resolveViewingTZ(r)
	since := parseSinceParamInTZ(r, 90, viewTZ)

	resp, err := h.listRuntimeUsage(r.Context(), rt.ID, viewTZ, since)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "failed to list usage")
		return
	}

	writeJSON(w, http.StatusOK, resp)
}

// listRuntimeUsage reads the daily-bucketed trend from agent_usage_hourly,
// applying the viewer's tz to project bucket_hour into local days.
func (h *Handler) listRuntimeUsage(ctx context.Context, runtimeID pgtype.UUID, tz string, since pgtype.Timestamptz) ([]RuntimeUsageResponse, error) {
	resolvedRuntimeID := uuidToString(runtimeID)
	rows, err := h.Queries.ListRuntimeUsage(ctx, db.ListRuntimeUsageParams{
		RuntimeID: runtimeID,
		Since:     since,
		Tz:        tz,
	})
	if err != nil {
		return nil, err
	}
	resp := make([]RuntimeUsageResponse, len(rows))
	for i, row := range rows {
		resp[i] = RuntimeUsageResponse{
			RuntimeID:        resolvedRuntimeID,
			Date:             row.Date.Time.Format("2006-01-02"),
			Provider:         row.Provider,
			Model:            row.Model,
			InputTokens:      row.InputTokens,
			OutputTokens:     row.OutputTokens,
			CacheReadTokens:  row.CacheReadTokens,
			CacheWriteTokens: row.CacheWriteTokens,
		}
	}
	return resp, nil
}

// GetRuntimeTaskActivity returns hourly task activity distribution for a runtime.
func (h *Handler) GetRuntimeTaskActivity(w http.ResponseWriter, r *http.Request) {
	runtimeID := chi.URLParam(r, "runtimeId")
	runtimeUUID, ok := parseUUIDOrBadRequest(w, runtimeID, "runtime_id")
	if !ok {
		return
	}

	rt, err := h.Queries.GetAgentRuntime(r.Context(), runtimeUUID)
	if err != nil {
		writeError(w, http.StatusNotFound, "runtime not found")
		return
	}

	if _, ok := h.requireWorkspaceMember(w, r, uuidToString(rt.WorkspaceID), "runtime not found"); !ok {
		return
	}

	viewTZ := h.resolveViewingTZ(r)
	rows, err := h.Queries.GetRuntimeTaskHourlyActivity(r.Context(), db.GetRuntimeTaskHourlyActivityParams{
		RuntimeID: rt.ID,
		Tz:        viewTZ,
	})
	if err != nil {
		writeError(w, http.StatusInternalServerError, "failed to get task activity")
		return
	}

	type HourlyActivity struct {
		Hour  int `json:"hour"`
		Count int `json:"count"`
	}

	resp := make([]HourlyActivity, len(rows))
	for i, row := range rows {
		resp[i] = HourlyActivity{Hour: int(row.Hour), Count: int(row.Count)}
	}

	writeJSON(w, http.StatusOK, resp)
}

// RuntimeUsageByAgentResponse is one (agent, model) row of "Cost by agent".
// Model stays on the wire because cost is computed client-side from a model
// pricing table, intentionally not stored server-side so pricing changes
// don't require a back-fill. The client groups by agent_id and sums.
type RuntimeUsageByAgentResponse struct {
	AgentID          string `json:"agent_id"`
	Model            string `json:"model"`
	InputTokens      int64  `json:"input_tokens"`
	OutputTokens     int64  `json:"output_tokens"`
	CacheReadTokens  int64  `json:"cache_read_tokens"`
	CacheWriteTokens int64  `json:"cache_write_tokens"`
	TaskCount        int32  `json:"task_count"`
}

// GetRuntimeUsageByAgent returns per-agent token aggregates for a runtime
// since the cutoff window. Drives the runtime-detail "Cost by agent" tab.
func (h *Handler) GetRuntimeUsageByAgent(w http.ResponseWriter, r *http.Request) {
	runtimeID := chi.URLParam(r, "runtimeId")
	runtimeUUID, ok := parseUUIDOrBadRequest(w, runtimeID, "runtime_id")
	if !ok {
		return
	}

	rt, err := h.Queries.GetAgentRuntime(r.Context(), runtimeUUID)
	if err != nil {
		writeError(w, http.StatusNotFound, "runtime not found")
		return
	}

	if _, ok := h.requireWorkspaceMember(w, r, uuidToString(rt.WorkspaceID), "runtime not found"); !ok {
		return
	}

	// No date bucketing — tz only sets the cutoff boundary so "last 30
	// days" means 30 of the viewer's days.
	viewTZ := h.resolveViewingTZ(r)
	since := parseSinceParamInTZ(r, 30, viewTZ)

	rows, err := h.Queries.ListRuntimeUsageByAgent(r.Context(), db.ListRuntimeUsageByAgentParams{
		RuntimeID: rt.ID,
		Since:     since,
	})
	if err != nil {
		writeError(w, http.StatusInternalServerError, "failed to list usage by agent")
		return
	}

	resp := make([]RuntimeUsageByAgentResponse, len(rows))
	for i, row := range rows {
		resp[i] = RuntimeUsageByAgentResponse{
			AgentID:          uuidToString(row.AgentID),
			Model:            row.Model,
			InputTokens:      row.InputTokens,
			OutputTokens:     row.OutputTokens,
			CacheReadTokens:  row.CacheReadTokens,
			CacheWriteTokens: row.CacheWriteTokens,
			TaskCount:        row.TaskCount,
		}
	}

	writeJSON(w, http.StatusOK, resp)
}

// RuntimeUsageByHourResponse is one (hour, model) row. Hours with zero
// activity are omitted by the SQL — clients fill the gap to render a
// continuous 0..23 axis. Model is preserved for client-side cost math.
type RuntimeUsageByHourResponse struct {
	Hour             int    `json:"hour"`
	Model            string `json:"model"`
	InputTokens      int64  `json:"input_tokens"`
	OutputTokens     int64  `json:"output_tokens"`
	CacheReadTokens  int64  `json:"cache_read_tokens"`
	CacheWriteTokens int64  `json:"cache_write_tokens"`
	TaskCount        int32  `json:"task_count"`
}

// GetRuntimeUsageByHour returns hourly (0..23) token aggregates for a
// runtime since the cutoff window. Drives the "By hour" tab.
//
// The hour-of-day axis is bucketed in the viewer's tz like every other
// report — the same timezone resolved by resolveViewingTZ from the request's
// `?tz=` param or the authenticated user's stored user.timezone.
func (h *Handler) GetRuntimeUsageByHour(w http.ResponseWriter, r *http.Request) {
	runtimeID := chi.URLParam(r, "runtimeId")
	runtimeUUID, ok := parseUUIDOrBadRequest(w, runtimeID, "runtime_id")
	if !ok {
		return
	}

	rt, err := h.Queries.GetAgentRuntime(r.Context(), runtimeUUID)
	if err != nil {
		writeError(w, http.StatusNotFound, "runtime not found")
		return
	}

	if _, ok := h.requireWorkspaceMember(w, r, uuidToString(rt.WorkspaceID), "runtime not found"); !ok {
		return
	}

	viewTZ := h.resolveViewingTZ(r)
	since := parseSinceParamInTZ(r, 30, viewTZ)

	rows, err := h.Queries.GetRuntimeUsageByHour(r.Context(), db.GetRuntimeUsageByHourParams{
		RuntimeID: rt.ID,
		Since:     since,
		Tz:        viewTZ,
	})
	if err != nil {
		writeError(w, http.StatusInternalServerError, "failed to get usage by hour")
		return
	}

	resp := make([]RuntimeUsageByHourResponse, len(rows))
	for i, row := range rows {
		resp[i] = RuntimeUsageByHourResponse{
			Hour:             int(row.Hour),
			Model:            row.Model,
			InputTokens:      row.InputTokens,
			OutputTokens:     row.OutputTokens,
			CacheReadTokens:  row.CacheReadTokens,
			CacheWriteTokens: row.CacheWriteTokens,
			TaskCount:        row.TaskCount,
		}
	}

	writeJSON(w, http.StatusOK, resp)
}

// sinceFromDays is the pure, now-injectable core of parseSinceParamInTZ.
// Given the current instant, a day count and an IANA location, it returns
// the instant of local midnight `days` days before `now`'s local calendar
// day. `now` is a parameter so the DST boundary maths can be tested at
// pinned dates (see TestSinceFromDays).
//
// The cutoff yields N+1 calendar buckets (today-days … today inclusive).
// The extra day versus a naive "-(days-1)" is deliberate headroom, not an
// off-by-one:
//   - Runtime detail's sliceWindow filters `date >= today-days` (closed) and
//     its prior-window delta reaches back to today-2*days, so the today-days
//     bucket MUST exist or the oldest bar / KPI delta silently loses data.
//   - The workspace dashboard re-filters client-side with -(days-1); the one
//     extra day the backend returns is trimmed there — harmless.
//
// Do not "tighten" this to -(days-1): it would break the runtime detail page.
func sinceFromDays(now time.Time, days int, loc *time.Location) time.Time {
	local := now.In(loc)
	startOfToday := time.Date(local.Year(), local.Month(), local.Day(), 0, 0, 0, 0, loc)
	return startOfToday.AddDate(0, 0, -days)
}

// parseSinceParamInTZ parses the "days" query parameter into a cutoff
// timestamptz. Anchors the cutoff to start-of-day-(N) in the supplied IANA zone so that
// `days=N` returns full N+1 calendar buckets in that zone (today's partial
// bucket + N prior full days). If tzName is empty or unparseable, falls back
// to UTC — never returns an error so handlers stay simple.
func parseSinceParamInTZ(r *http.Request, defaultDays int, tzName string) pgtype.Timestamptz {
	days := defaultDays
	if d := r.URL.Query().Get("days"); d != "" {
		if parsed, err := strconv.Atoi(d); err == nil && parsed > 0 && parsed <= 365 {
			days = parsed
		}
	}
	loc, err := time.LoadLocation(tzName)
	if err != nil || loc == nil {
		loc = time.UTC
	}
	return pgtype.Timestamptz{Time: sinceFromDays(time.Now(), days, loc), Valid: true}
}

// resolveViewingTZ resolves the IANA tz to render the response in:
// `?tz=` query param, else the authenticated user's stored
// user.timezone, else "UTC". Invalid values fall through rather than
// erroring — tz is a display concern.
//
// The browser app always sends `?tz=` (resolved client-side by
// useViewingTimezone), so the `GetUser` lookup below is a COLD fallback
// hit only by API clients / older builds that omit the param — it is not
// a hot path. Do not replicate this DB-read pattern into a handler that
// runs without a `?tz=`-supplying client in front of it.
func (h *Handler) resolveViewingTZ(r *http.Request) string {
	if tz := strings.TrimSpace(r.URL.Query().Get("tz")); tz != "" {
		if loc, err := time.LoadLocation(tz); err == nil && loc != nil {
			return tz
		}
	}
	if userID := requestUserID(r); userID != "" {
		uid, err := util.ParseUUID(userID)
		if err != nil {
			slog.Warn("resolveViewingTZ: malformed X-User-ID, falling back to UTC",
				"path", r.URL.Path, "user_id", userID)
		}
		if err == nil {
			slog.Debug("resolveViewingTZ cold path: ?tz= missing, reading user.timezone",
				"path", r.URL.Path, "user_id", userID)
			if user, err := h.Queries.GetUser(r.Context(), uid); err == nil && user.Timezone.Valid {
				stored := strings.TrimSpace(user.Timezone.String)
				if stored != "" {
					if loc, err := time.LoadLocation(stored); err == nil && loc != nil {
						return stored
					}
				}
			}
		}
	}
	return "UTC"
}

const maxRuntimeDisplayNameLength = 128

// UpdateAgentRuntimeRequest is the JSON body accepted by PATCH /api/runtimes/:id.
// Only fields users may legitimately edit are listed; other runtime metadata
// (provider, daemon_id, status…) flows in from the daemon and is read-only here.
type UpdateAgentRuntimeRequest struct {
	// Visibility flips a runtime between "private" (default — only the owner
	// or workspace admins can bind agents) and "public" (any workspace
	// member can). Owner / workspace admin only, gated by canEditRuntime.
	Visibility *string `json:"visibility,omitempty"`
	// DisplayName sets the user-editable machine label. Empty string clears
	// the override so clients fall back to daemon-reported name. Whitespace
	// is trimmed. Owner / workspace admin only, gated by canEditRuntime.
	DisplayName *string `json:"display_name,omitempty"`
}

// UpdateAgentRuntime handles PATCH /api/runtimes/:id. Visibility and
// display_name are editable; the request shape is open-ended so future
// fields can be added without a route change.
// Workspace-membership-checked; write access is gated by canEditRuntime.
func (h *Handler) UpdateAgentRuntime(w http.ResponseWriter, r *http.Request) {
	runtimeID := chi.URLParam(r, "runtimeId")
	runtimeUUID, ok := parseUUIDOrBadRequest(w, runtimeID, "runtime_id")
	if !ok {
		return
	}

	rt, err := h.Queries.GetAgentRuntime(r.Context(), runtimeUUID)
	if err != nil {
		writeError(w, http.StatusNotFound, "runtime not found")
		return
	}

	member, ok := h.requireWorkspaceMember(w, r, uuidToString(rt.WorkspaceID), "runtime not found")
	if !ok {
		return
	}
	if !canEditRuntime(member, rt) {
		writeError(w, http.StatusForbidden, "you can only edit your own runtimes")
		return
	}

	var req UpdateAgentRuntimeRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid JSON body")
		return
	}

	var (
		newVisibility   string
		needVisibility  bool
		newDisplayName  string
		needDisplayName bool
		changed         bool
	)
	if req.Visibility != nil {
		v := *req.Visibility
		if v != "private" && v != "public" {
			writeError(w, http.StatusBadRequest, "visibility must be 'private' or 'public'")
			return
		}
		if v != rt.Visibility {
			newVisibility = v
			needVisibility = true
		}
	}
	if req.DisplayName != nil {
		trimmed := strings.TrimSpace(*req.DisplayName)
		if utf8.RuneCountInString(trimmed) > maxRuntimeDisplayNameLength {
			writeError(w, http.StatusBadRequest, fmt.Sprintf("display_name must be %d characters or fewer", maxRuntimeDisplayNameLength))
			return
		}
		if trimmed != rt.DisplayName {
			newDisplayName = trimmed
			needDisplayName = true
		}
	}

	if needVisibility {
		updated, err := h.Queries.UpdateAgentRuntimeVisibility(r.Context(), db.UpdateAgentRuntimeVisibilityParams{
			ID:         runtimeUUID,
			Visibility: newVisibility,
		})
		if err != nil {
			slog.Error("UpdateAgentRuntimeVisibility failed", "error", err, "runtime_id", runtimeID)
			writeError(w, http.StatusInternalServerError, "failed to update runtime")
			return
		}
		rt = updated
		changed = true
	}
	if needDisplayName {
		updated, err := h.Queries.UpdateAgentRuntimeDisplayName(r.Context(), db.UpdateAgentRuntimeDisplayNameParams{
			ID:          runtimeUUID,
			DisplayName: newDisplayName,
		})
		if err != nil {
			slog.Error("UpdateAgentRuntimeDisplayName failed", "error", err, "runtime_id", runtimeID)
			writeError(w, http.StatusInternalServerError, "failed to update runtime")
			return
		}
		rt = updated
		changed = true
	}

	if changed {
		// Notify connected clients that runtime metadata changed so the
		// list/detail pages refresh — matches the pattern used by
		// DeleteAgentRuntime.
		h.publish(protocol.EventDaemonRegister, uuidToString(rt.WorkspaceID), "member", uuidToString(member.UserID), map[string]any{
			"action": "update",
		})
	}

	writeJSON(w, http.StatusOK, h.runtimeToResponse(r.Context(), rt))
}

func canEditRuntime(member db.Member, rt db.AgentRuntime) bool {
	if roleAllowed(member.Role, "owner", "admin") {
		return true
	}
	return rt.OwnerID.Valid && uuidToString(rt.OwnerID) == uuidToString(member.UserID)
}

// canDeleteRuntime intentionally has no workspace owner/admin override.
// Runtime deletion is reserved for the member who owns the runtime.
func canDeleteRuntime(member db.Member, rt db.AgentRuntime) bool {
	return rt.OwnerID.Valid && uuidToString(rt.OwnerID) == uuidToString(member.UserID)
}

// canOwnRuntime is Computer-owner-only for restart mutations.
func canOwnRuntime(member db.Member, rt db.AgentRuntime) bool {
	return rt.OwnerID.Valid && uuidToString(rt.OwnerID) == uuidToString(member.UserID)
}

// canManageMachineUpgrade is Computer-owner-only. A Workspace role grants
// authority over that Workspace, not over another person's machine-wide
// Computer lifecycle.
func canManageMachineUpgrade(member db.Member, rt db.AgentRuntime) bool {
	return canOwnRuntime(member, rt)
}

// canUseRuntimeForAgent reports whether a workspace member is allowed to
// bind a new agent to — or move an existing agent onto — the given runtime.
// Mirrors canEditRuntime but layers on the runtime's visibility flag so a
// `public` runtime is usable by anyone in the workspace while a `private`
// runtime stays bound to its owner. Workspace owners/admins keep an
// administrative override for both. See migration 083 for the visibility
// column.
func canUseRuntimeForAgent(member db.Member, rt db.AgentRuntime) bool {
	if roleAllowed(member.Role, "owner", "admin") {
		return true
	}
	if rt.Visibility == "public" {
		return true
	}
	return rt.OwnerID.Valid && uuidToString(rt.OwnerID) == uuidToString(member.UserID)
}

// runtimesShareMachine reports whether two runtimes represent the same
// physical machine. Same-machine moves do not require the cross-device memory
// sync capability. It is deliberately narrower than the frontend's cosmetic
// runtimeMachineKey (packages/views/runtimes/components/
// runtime-machines.ts), which also falls back to parsing a hostname out of
// `name`/`device_info` for display grouping — free text is not a safe signal
// for an authorization boundary. Here, only daemon_id (the persistent
// per-installation UUID written once to <profile-dir>/daemon.id — see
// server/internal/daemon/config.go's EnsureDaemonID) counts as machine
// identity. A runtime with no daemon_id (a pure API-key/non-daemon-backed
// runtime) never shares a machine with anything, including another such
// runtime — each is its own isolated machine by construction, same as the
// frontend's ID-keyed fallback.
func runtimesShareMachine(a, b db.AgentRuntime) bool {
	if a.RuntimeMode != b.RuntimeMode {
		return false
	}
	if !a.DaemonID.Valid || a.DaemonID.String == "" {
		return false
	}
	if !b.DaemonID.Valid || b.DaemonID.String == "" {
		return false
	}
	return a.DaemonID.String == b.DaemonID.String
}

func (h *Handler) ListAgentRuntimes(w http.ResponseWriter, r *http.Request) {
	workspaceID := h.resolveWorkspaceID(r)
	userID, ok := requireUserID(w, r)
	if !ok {
		return
	}

	var runtimes []db.AgentRuntime
	var err error

	if ownerFilter := r.URL.Query().Get("owner"); ownerFilter == "me" {
		runtimes, err = h.Queries.ListAgentRuntimesByOwner(r.Context(), db.ListAgentRuntimesByOwnerParams{
			WorkspaceID: parseUUID(workspaceID),
			OwnerID:     parseUUID(userID),
		})
	} else {
		// Privacy: a member sees their own runtimes plus everyone's public
		// ones — never another member's private runtime. No owner/admin
		// override: visibility is per-user even for workspace admins.
		runtimes, err = h.Queries.ListVisibleAgentRuntimes(r.Context(), db.ListVisibleAgentRuntimesParams{
			WorkspaceID: parseUUID(workspaceID),
			OwnerID:     parseUUID(userID),
		})
	}

	if err != nil {
		writeError(w, http.StatusInternalServerError, "failed to list runtimes")
		return
	}

	writeJSON(w, http.StatusOK, h.agentRuntimeResponsesForList(r.Context(), runtimes))
}

func (h *Handler) agentRuntimeResponsesForList(ctx context.Context, runtimes []db.AgentRuntime) []AgentRuntimeResponse {
	resp := make([]AgentRuntimeResponse, len(runtimes))
	updates := h.runtimeUpdatesForList(ctx, runtimes)
	machineUpgrades := h.machineUpgradesForList(ctx, runtimes)
	autoUpdates := h.daemonUpdateStatusesForList(ctx, runtimes)
	daemonHeartbeats := h.daemonHeartbeatsForList(ctx, runtimes)
	now := time.Now()
	for i, rt := range runtimes {
		update := updates[uuidToString(rt.ID)]
		release := h.runtimeReleaseForResponse(ctx, rt, update)
		resp[i] = runtimeToResponseWithUpdateReleaseAndObservation(rt, update, release, autoUpdates[runtimeDaemonKey(rt)])
		resp[i].MachineUpgrade = machineUpgrades[runtimeDaemonKey(rt)]
		hb := daemonHeartbeats[runtimeDaemonKey(rt)]
		resp[i].ComputerConnected = h.computerConnectedByRunner(runtimeDaemonKey(rt), uuidToString(rt.WorkspaceID), hb, now)
		if hb != nil {
			resp[i].DaemonLastSeenAt = timestampToPtr(hb.LastSeenAt)
		}
	}
	attachDaemonTargetVersions(resp)
	return resp
}

// DeleteAgentRuntime deletes a runtime after permission and dependency checks.
//
// The strict variant: refuses with 409 + structured `runtime_has_active_agents`
// when any non-archived agent is still bound to the runtime, and returns the
// blocking agent list in the response body so the front-end can pivot to the
// cascade dialog without an extra round-trip. The cascade itself lives at
// POST /api/runtimes/:id/archive-agents-and-delete (ArchiveAgentsAndDeleteRuntime
// below) and runs the multi-write teardown inside a single transaction.
func (h *Handler) DeleteAgentRuntime(w http.ResponseWriter, r *http.Request) {
	runtimeID := chi.URLParam(r, "runtimeId")
	runtimeUUID, ok := parseUUIDOrBadRequest(w, runtimeID, "runtime_id")
	if !ok {
		return
	}

	rt, err := h.Queries.GetAgentRuntime(r.Context(), runtimeUUID)
	if err != nil {
		writeError(w, http.StatusNotFound, "runtime not found")
		return
	}

	wsID := uuidToString(rt.WorkspaceID)
	member, ok := h.requireWorkspaceMember(w, r, wsID, "runtime not found")
	if !ok {
		return
	}

	if !canDeleteRuntime(member, rt) {
		writeError(w, http.StatusForbidden, "you can only delete your own runtimes")
		return
	}
	userID := uuidToString(member.UserID)

	// Check if any active (non-archived) agents are bound to this runtime.
	// Surface them on the 409 so the dialog can render the cascade plan
	// directly from this response — saves a second round-trip when the
	// user clicked Delete from a stale list page.
	activeAgents, err := h.Queries.ListActiveAgentsByRuntime(r.Context(), rt.ID)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "failed to check runtime dependencies")
		return
	}
	if len(activeAgents) > 0 {
		writeJSON(w, http.StatusConflict, runtimeHasActiveAgentsResponse(activeAgents))
		return
	}

	activeInboxEventCount, err := countActiveInboxEventsByRuntimeIDs(r.Context(), h.DB, []pgtype.UUID{rt.ID})
	if err != nil {
		writeError(w, http.StatusInternalServerError, "failed to check runtime inbox dependencies")
		return
	}
	if activeInboxEventCount > 0 {
		writeJSON(w, http.StatusConflict, map[string]any{
			"error":                    "cannot delete runtime: it has active inbox events. Wait for them to finish or cancel them first.",
			"code":                     "runtime_has_active_inbox_events",
			"active_inbox_event_count": activeInboxEventCount,
		})
		return
	}

	tx, err := h.TxStarter.Begin(r.Context())
	if err != nil {
		writeError(w, http.StatusInternalServerError, "failed to delete runtime")
		return
	}
	defer tx.Rollback(r.Context())
	if daemonID := runtimeDaemonKey(rt); daemonID != "" {
		if err := lockDaemonRegistration(r.Context(), tx, wsID, daemonID); err != nil {
			writeError(w, http.StatusInternalServerError, "failed to lock computer registration")
			return
		}
	}
	qtx := h.Queries.WithTx(tx)

	// Shared teardown with Computer deletion (DeleteComputer):
	// pause autopilots → drop archived agents → fail memory curation → delete
	// the runtime row.
	if err := teardownRuntimeWithoutActiveAgents(r.Context(), qtx, tx, rt.ID); err != nil {
		writeError(w, http.StatusInternalServerError, "failed to delete runtime")
		return
	}
	if err := deleteOrphanDaemonUpdateStatusForRuntime(r.Context(), qtx, rt); err != nil {
		writeError(w, http.StatusInternalServerError, "failed to delete runtime update status")
		return
	}
	if err := tx.Commit(r.Context()); err != nil {
		writeError(w, http.StatusInternalServerError, "failed to delete runtime")
		return
	}

	slog.Info("runtime deleted", "runtime_id", uuidToString(rt.ID), "deleted_by", userID)

	// Notify frontend to refresh runtime list.
	h.publish(protocol.EventDaemonRegister, wsID, "member", userID, map[string]any{
		"action": "delete",
	})

	writeJSON(w, http.StatusOK, map[string]string{"status": "ok"})
}

// runtimeHasActiveAgentsResponse builds the structured 409 body shared by
// DeleteAgentRuntime (light-mode block) and ArchiveAgentsAndDeleteRuntime
// (cascade-plan-changed). The shape is:
//
//	{
//	  "error": "...",
//	  "code":  "runtime_has_active_agents" | "runtime_delete_plan_changed",
//	  "active_agents": [AgentResponse, ...]
//	}
//
// Front-end branches on `code`. The caller picks which code to send; this
// helper just normalises the agent serialisation and the error string.
func runtimeHasActiveAgentsResponse(agents []db.Agent) map[string]any {
	resp := make([]AgentResponse, len(agents))
	for i, a := range agents {
		resp[i] = agentToResponse(a)
	}
	return map[string]any{
		"error":         "cannot delete runtime: it has active agents bound to it. Archive or reassign the agents first.",
		"code":          "runtime_has_active_agents",
		"active_agents": resp,
	}
}

// archiveAgentsAndDeleteRuntimeRequest is the wire shape for the cascade
// endpoint. expected_active_agent_ids is the snapshot the user just confirmed
// in the dialog — the server compares it to the live set inside the
// transaction and refuses with runtime_delete_plan_changed if anything moved
// between dialog open and confirm. That guarantees the user is approving the
// exact agent set that will be archived, even if a teammate adds or archives
// an agent in the same window.
type archiveAgentsAndDeleteRuntimeRequest struct {
	ExpectedActiveAgentIDs []string `json:"expected_active_agent_ids"`
}

// ArchiveAgentsAndDeleteRuntime is the cascade entry point: archive every
// agent currently bound to the runtime, cancel their queued/running tasks,
// pause autopilots that target them, hard-delete the now-detached archived
// rows so the agent.runtime_id FK no longer pins the runtime, and finally
// delete the runtime row itself — all inside a single transaction so a
// partial failure never leaves a runtime half-torn-down.
//
// Transaction order follows the reference revoke flow in
// revokeAndRemoveMember (workspace_revoke.go) so the two cascade paths share
// the same race-safety properties: the dispatcher can't claim a task whose
// runtime is about to vanish, autopilots can't fire onto a dead assignee,
// and post-commit publish events emit the same task:cancelled →
// agent:archived → daemon:register fan-out.
//
// The expected_active_agent_ids check is the load-bearing piece for the UX:
// the front-end snapshots the agent list when the dialog opens and presents
// the user a checkbox confirmation; if a teammate adds or archives an agent
// while that dialog is open, this endpoint refuses with
// runtime_delete_plan_changed and the latest list, so the user never confirms
// a stale plan.
func (h *Handler) ArchiveAgentsAndDeleteRuntime(w http.ResponseWriter, r *http.Request) {
	runtimeID := chi.URLParam(r, "runtimeId")
	runtimeUUID, ok := parseUUIDOrBadRequest(w, runtimeID, "runtime_id")
	if !ok {
		return
	}

	var req archiveAgentsAndDeleteRuntimeRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid request body")
		return
	}
	expected, ok := parseExpectedActiveAgentIDs(req.ExpectedActiveAgentIDs)
	if !ok {
		writeError(w, http.StatusBadRequest, "expected_active_agent_ids must be a list of valid UUIDs")
		return
	}

	rt, err := h.Queries.GetAgentRuntime(r.Context(), runtimeUUID)
	if err != nil {
		writeError(w, http.StatusNotFound, "runtime not found")
		return
	}

	wsID := uuidToString(rt.WorkspaceID)
	member, ok := h.requireWorkspaceMember(w, r, wsID, "runtime not found")
	if !ok {
		return
	}
	if !canDeleteRuntime(member, rt) {
		writeError(w, http.StatusForbidden, "you can only delete your own runtimes")
		return
	}
	userID := uuidToString(member.UserID)

	tx, err := h.TxStarter.Begin(r.Context())
	if err != nil {
		writeError(w, http.StatusInternalServerError, "failed to start transaction")
		return
	}
	defer tx.Rollback(r.Context())
	if daemonID := runtimeDaemonKey(rt); daemonID != "" {
		if err := lockDaemonRegistration(r.Context(), tx, wsID, daemonID); err != nil {
			writeError(w, http.StatusInternalServerError, "failed to lock computer registration")
			return
		}
	}
	qtx := h.Queries.WithTx(tx)

	// Lock the runtime row first. PostgreSQL's FK validation on
	// agent.runtime_id requires FOR KEY SHARE on the parent runtime row,
	// which conflicts with FOR UPDATE — so any concurrent INSERT or
	// UPDATE that would point a new/moved agent at this runtime now
	// blocks until our tx finishes. This is the "兜底" lock that keeps
	// new actives from appearing between our snapshot and our archive.
	if _, err := qtx.LockAgentRuntime(r.Context(), rt.ID); err != nil {
		writeError(w, http.StatusInternalServerError, "failed to lock runtime")
		return
	}

	// Re-list active agents inside the transaction, with FOR UPDATE on
	// each row so a concurrent archive/move of one of those existing
	// agents also blocks until we commit. Comparing against the expected
	// set here closes the dialog-open / user-confirm race: even if a
	// teammate creates or archives an agent on this runtime while the
	// dialog was open, the user is approving exactly the set the server
	// is about to archive.
	currentActive, err := qtx.ListActiveAgentsByRuntimeForUpdate(r.Context(), rt.ID)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "failed to enumerate active agents")
		return
	}
	if !activeAgentSetMatches(currentActive, expected) {
		// Refuse with the latest snapshot so the front-end can re-render
		// the dialog and force a fresh user confirmation. Reuses the
		// shared response helper but overrides the code to a planning
		// signal so the dialog can distinguish "you opened from a stale
		// page" from "the plan you confirmed just changed under you".
		body := runtimeHasActiveAgentsResponse(currentActive)
		body["code"] = "runtime_delete_plan_changed"
		body["error"] = "the active agent set changed; please review and confirm again."
		writeJSON(w, http.StatusConflict, body)
		return
	}

	// Build the agent ID list once — it's the explicit allowlist for the
	// archive UPDATE below and the runtime-or-agent task cancel further
	// down. By keying the archive off this list (not off runtime_id) we
	// guarantee that agents not in the user's confirmed set can never
	// be silently archived, even if the row-level locks above somehow
	// missed something. Defense in depth.
	currentActiveIDs := make([]pgtype.UUID, len(currentActive))
	for i, a := range currentActive {
		currentActiveIDs[i] = a.ID
	}

	// 1. Archive every active agent on this runtime, narrowed to the
	//    user-confirmed expected_active_agent_ids set (which equals
	//    currentActive at this point). Returns the affected rows so the
	//    post-commit publish loop can fan out agent:archived per agent.
	archivedAgents, err := qtx.ArchiveAgentsByIDs(r.Context(), db.ArchiveAgentsByIDsParams{
		ArchivedBy: member.UserID,
		AgentIds:   currentActiveIDs,
	})
	if err != nil {
		writeError(w, http.StatusInternalServerError, "failed to archive agents")
		return
	}

	// 2. Cancel queued/dispatched/running tasks. Match by runtime_id AND
	//    by archived agent ids: agent.runtime_id can be reassigned without
	//    rewriting historical agent_inbox_event rows, so an agent we just
	//    archived may still own tasks pinned to a different runtime — and
	//    Inbox admission does not gate on agent.archived_at.
	archivedIDs := make([]pgtype.UUID, len(archivedAgents))
	for i, a := range archivedAgents {
		archivedIDs[i] = a.ID
	}
	cancelledTasks, err := qtx.CancelAgentTasksByRuntimeOrAgent(r.Context(), db.CancelAgentTasksByRuntimeOrAgentParams{
		RuntimeIds: []pgtype.UUID{rt.ID},
		AgentIds:   archivedIDs,
	})
	if err != nil {
		writeError(w, http.StatusInternalServerError, "failed to cancel tasks")
		return
	}

	// 3. Pause autopilots whose assignee is one of the archived agents.
	//    Snapshots the full archived set on this runtime — including any
	//    that were already archived before this call — because the
	//    DeleteArchivedAgentsByRuntime below will hard-delete the lot, and
	//    a paused autopilot is much louder in the UI than a silently-
	//    dangling assignee_id (see migration 096 for why the FK is gone).
	allArchivedIDs, err := qtx.ListArchivedAgentIDsByRuntime(r.Context(), rt.ID)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "failed to enumerate archived agents")
		return
	}
	if len(allArchivedIDs) > 0 {
		if err := teardownArchivedAgentDependents(r.Context(), qtx, allArchivedIDs); err != nil {
			writeError(w, http.StatusInternalServerError, "failed to clean up archived agent dependencies")
			return
		}
	}

	// 4. Hard-delete the archived agents so the agent.runtime_id FK
	//    (ON DELETE RESTRICT) no longer keeps the runtime alive.
	if err := qtx.DeleteArchivedAgentsByRuntime(r.Context(), rt.ID); err != nil {
		writeError(w, http.StatusInternalServerError, "failed to clean up archived agents")
		return
	}

	// Fail incomplete memory curation runs before deleting the runtime row.
	// The runtime_id FK is ON DELETE SET NULL, so without this cleanup any
	// queued/waiting_runtime/running run would have its runtime_id nulled and
	// linger forever — no daemon can claim a run whose runtime_id is NULL.
	if _, err := tx.Exec(r.Context(), `
		UPDATE memory_curation_run
		   SET status = 'failed', error = 'runtime deleted', finished_at = now()
		 WHERE runtime_id = $1 AND status IN ('queued', 'waiting_runtime', 'running')
	`, rt.ID); err != nil {
		writeError(w, http.StatusInternalServerError, "failed to clean up memory curation runs")
		return
	}

	// 5. Finally delete the runtime row itself.
	if err := qtx.DeleteAgentRuntime(r.Context(), rt.ID); err != nil {
		writeError(w, http.StatusInternalServerError, "failed to delete runtime")
		return
	}
	if err := deleteOrphanDaemonUpdateStatusForRuntime(r.Context(), qtx, rt); err != nil {
		writeError(w, http.StatusInternalServerError, "failed to delete runtime update status")
		return
	}

	if err := tx.Commit(r.Context()); err != nil {
		writeError(w, http.StatusInternalServerError, "failed to commit transaction")
		return
	}

	// Post-commit fan-out — same ordering as publishRevocation so subscribers
	// observe task:cancelled before agent:archived before the runtime list
	// refresh, matching the order other revocation paths use.
	if h.TaskService != nil && len(cancelledTasks) > 0 {
		h.TaskService.BroadcastCancelledTasks(r.Context(), cancelledTasks)
	}
	for _, a := range archivedAgents {
		h.publish(protocol.EventAgentArchived, wsID, "member", userID, map[string]any{
			"agent": agentToResponse(a),
		})
	}
	h.publish(protocol.EventDaemonRegister, wsID, "member", userID, map[string]any{
		"action": "delete",
	})

	slog.Info("runtime deleted via cascade",
		"runtime_id", uuidToString(rt.ID),
		"deleted_by", userID,
		"agents_archived", len(archivedAgents),
		"tasks_cancelled", len(cancelledTasks),
	)

	writeJSON(w, http.StatusOK, map[string]any{
		"status":          "ok",
		"agents_archived": len(archivedAgents),
		"tasks_cancelled": len(cancelledTasks),
	})
}

// parseExpectedActiveAgentIDs validates the cascade endpoint's
// expected_active_agent_ids list. nil / empty is allowed (an empty set is a
// valid plan: "I confirmed there are no active agents" — the cascade then
// just deletes the runtime without archiving anything). Returns ok=false on
// any malformed UUID so the handler responds 400 instead of silently
// matching a different set.
func parseExpectedActiveAgentIDs(raw []string) (map[string]struct{}, bool) {
	out := make(map[string]struct{}, len(raw))
	for _, s := range raw {
		u, err := util.ParseUUID(s)
		if err != nil || !u.Valid {
			return nil, false
		}
		out[uuidToString(u)] = struct{}{}
	}
	return out, true
}

// activeAgentSetMatches reports whether the live set of active agents on the
// runtime matches the snapshot the front-end confirmed. Order-insensitive
// because the front-end may render in any order; size + membership is what
// matters for "did the plan change?".
func activeAgentSetMatches(current []db.Agent, expected map[string]struct{}) bool {
	if len(current) != len(expected) {
		return false
	}
	for _, a := range current {
		if _, ok := expected[uuidToString(a.ID)]; !ok {
			return false
		}
	}
	return true
}
