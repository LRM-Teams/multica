package handler

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/multica-ai/multica/server/internal/util"
	db "github.com/multica-ai/multica/server/pkg/db/generated"
	"github.com/multica-ai/multica/server/pkg/protocol"
)

const daemonUpdateErrorMessageLimit = 240

var errDaemonUpdateObservationConflict = errors.New("daemon update observation revision conflicts with stored payload")

type daemonUpdateObservationValidationError struct {
	err error
}

func (e *daemonUpdateObservationValidationError) Error() string {
	return e.err.Error()
}

func (e *daemonUpdateObservationValidationError) Unwrap() error {
	return e.err
}

type daemonUpdateStatusParams struct {
	sessionID                  pgtype.UUID
	revision                   int64
	observedAt                 pgtype.Timestamptz
	autoUpdateEffectiveEnabled bool
	configSource               string
	ineligibleReason           pgtype.Text
	checkIntervalSeconds       int64
	phase                      string
	attemptSource              pgtype.Text
	lastAttemptAt              pgtype.Timestamptz
	lastOutcome                string
	targetVersion              pgtype.Text
	errorCode                  pgtype.Text
	errorMessage               pgtype.Text
	stagedVersion              pgtype.Text
	activationGeneration       pgtype.Int8
	payloadHash                string
}

func normalizeDaemonUpdateObservation(observation protocol.DaemonUpdateObservation) (daemonUpdateStatusParams, error) {
	observation.SessionID = strings.TrimSpace(observation.SessionID)
	observation.ObservedAt = strings.TrimSpace(observation.ObservedAt)
	observation.ConfigSource = strings.TrimSpace(observation.ConfigSource)
	observation.IneligibleReason = strings.TrimSpace(observation.IneligibleReason)
	observation.Phase = strings.TrimSpace(observation.Phase)
	observation.AttemptSource = strings.TrimSpace(observation.AttemptSource)
	observation.LastAttemptAt = strings.TrimSpace(observation.LastAttemptAt)
	observation.LastOutcome = strings.TrimSpace(observation.LastOutcome)
	observation.TargetVersion = strings.TrimSpace(observation.TargetVersion)
	observation.ErrorCode = strings.TrimSpace(observation.ErrorCode)
	observation.ErrorMessage = strings.Join(strings.Fields(observation.ErrorMessage), " ")
	observation.StagedVersion = strings.TrimSpace(observation.StagedVersion)

	sessionID, err := util.ParseUUID(observation.SessionID)
	if err != nil {
		return daemonUpdateStatusParams{}, errors.New("auto_update.session_id must be a UUID")
	}
	if observation.Revision <= 0 {
		return daemonUpdateStatusParams{}, errors.New("auto_update.revision must be positive")
	}
	observedAt, err := time.Parse(time.RFC3339Nano, observation.ObservedAt)
	if err != nil {
		return daemonUpdateStatusParams{}, errors.New("auto_update.observed_at must be RFC3339")
	}
	if !daemonUpdateValueAllowed(observation.ConfigSource, "official_host_default", "self_host_default", "env_enabled", "env_disabled", "cli_disabled", "deprecated_noop", "auto_detect") {
		return daemonUpdateStatusParams{}, fmt.Errorf("invalid auto_update.config_source %q", observation.ConfigSource)
	}
	if observation.IneligibleReason != "" && !daemonUpdateValueAllowed(observation.IneligibleReason, "desktop_managed", "non_release_build", "explicit_only") {
		return daemonUpdateStatusParams{}, fmt.Errorf("invalid auto_update.ineligible_reason %q", observation.IneligibleReason)
	}
	if observation.CheckIntervalSeconds <= 0 {
		return daemonUpdateStatusParams{}, errors.New("auto_update.check_interval_seconds must be positive")
	}
	if !daemonUpdateValueAllowed(observation.Phase, "disabled", "waiting", "checking", "updating", "restart_pending") {
		return daemonUpdateStatusParams{}, fmt.Errorf("invalid auto_update.phase %q", observation.Phase)
	}
	if observation.AttemptSource != "" && !daemonUpdateValueAllowed(observation.AttemptSource, "auto", "server") {
		return daemonUpdateStatusParams{}, fmt.Errorf("invalid auto_update.attempt_source %q", observation.AttemptSource)
	}
	var lastAttemptAt pgtype.Timestamptz
	if observation.LastAttemptAt != "" {
		parsed, err := time.Parse(time.RFC3339Nano, observation.LastAttemptAt)
		if err != nil {
			return daemonUpdateStatusParams{}, errors.New("auto_update.last_attempt_at must be RFC3339")
		}
		lastAttemptAt = pgtype.Timestamptz{Time: parsed, Valid: true}
	}
	if !daemonUpdateValueAllowed(observation.LastOutcome, "never_checked", "up_to_date", "update_available", "busy", "pinned", "fetch_failed", "verification_failed", "update_failed", "update_succeeded", "interrupted", "explicit_only") {
		return daemonUpdateStatusParams{}, fmt.Errorf("invalid auto_update.last_outcome %q", observation.LastOutcome)
	}
	if len(observation.ErrorCode) > 80 {
		return daemonUpdateStatusParams{}, errors.New("auto_update.error_code is too long")
	}
	if observation.ErrorCode != "" && !daemonUpdateValueAllowed(
		observation.ErrorCode,
		"daemon_restarted_during_update",
		"release_fetch_failed",
		"download_update_failed",
		"updated_binary_verification_failed",
		"desktop_managed",
	) {
		return daemonUpdateStatusParams{}, fmt.Errorf("invalid auto_update.error_code %q", observation.ErrorCode)
	}
	if len(observation.ErrorMessage) > daemonUpdateErrorMessageLimit {
		return daemonUpdateStatusParams{}, errors.New("auto_update.error_message is too long")
	}
	var activationGeneration pgtype.Int8
	if observation.ActivationGeneration != nil {
		if *observation.ActivationGeneration > uint64(^uint64(0)>>1) {
			return daemonUpdateStatusParams{}, errors.New("auto_update.activation_generation is too large")
		}
		activationGeneration = pgtype.Int8{Int64: int64(*observation.ActivationGeneration), Valid: true}
	}
	payload, err := json.Marshal(observation)
	if err != nil {
		return daemonUpdateStatusParams{}, fmt.Errorf("marshal auto_update observation: %w", err)
	}
	digest := sha256.Sum256(payload)
	return daemonUpdateStatusParams{
		sessionID:                  sessionID,
		revision:                   observation.Revision,
		observedAt:                 pgtype.Timestamptz{Time: observedAt, Valid: true},
		autoUpdateEffectiveEnabled: observation.AutoUpdateEffectiveEnabled,
		configSource:               observation.ConfigSource,
		ineligibleReason:           optionalDaemonUpdateText(observation.IneligibleReason),
		checkIntervalSeconds:       observation.CheckIntervalSeconds,
		phase:                      observation.Phase,
		attemptSource:              optionalDaemonUpdateText(observation.AttemptSource),
		lastAttemptAt:              lastAttemptAt,
		lastOutcome:                observation.LastOutcome,
		targetVersion:              optionalDaemonUpdateText(observation.TargetVersion),
		errorCode:                  optionalDaemonUpdateText(observation.ErrorCode),
		errorMessage:               optionalDaemonUpdateText(observation.ErrorMessage),
		stagedVersion:              optionalDaemonUpdateText(observation.StagedVersion),
		activationGeneration:       activationGeneration,
		payloadHash:                hex.EncodeToString(digest[:]),
	}, nil
}

func (h *Handler) registerDaemonUpdateObservation(
	ctx context.Context,
	workspaceID pgtype.UUID,
	daemonID string,
	observation *protocol.DaemonUpdateObservation,
) error {
	daemonID = strings.TrimSpace(daemonID)
	if observation == nil {
		return h.Queries.DeleteDaemonUpdateStatus(ctx, db.DeleteDaemonUpdateStatusParams{
			WorkspaceID: workspaceID,
			DaemonID:    daemonID,
		})
	}
	params, err := normalizeDaemonUpdateObservation(*observation)
	if err != nil {
		return &daemonUpdateObservationValidationError{err: err}
	}
	affected, err := h.Queries.RegisterDaemonUpdateStatus(ctx, db.RegisterDaemonUpdateStatusParams{
		WorkspaceID:                workspaceID,
		DaemonID:                   daemonID,
		SessionID:                  params.sessionID,
		Revision:                   params.revision,
		ObservedAt:                 params.observedAt,
		AutoUpdateEffectiveEnabled: params.autoUpdateEffectiveEnabled,
		ConfigSource:               params.configSource,
		IneligibleReason:           params.ineligibleReason,
		CheckIntervalSeconds:       params.checkIntervalSeconds,
		Phase:                      params.phase,
		AttemptSource:              params.attemptSource,
		LastAttemptAt:              params.lastAttemptAt,
		LastOutcome:                params.lastOutcome,
		TargetVersion:              params.targetVersion,
		ErrorCode:                  params.errorCode,
		ErrorMessage:               params.errorMessage,
		StagedVersion:              params.stagedVersion,
		ActivationGeneration:       params.activationGeneration,
		PayloadHash:                params.payloadHash,
	})
	if err != nil || affected > 0 {
		return err
	}
	current, err := h.Queries.GetDaemonUpdateStatus(ctx, db.GetDaemonUpdateStatusParams{
		WorkspaceID: workspaceID,
		DaemonID:    daemonID,
	})
	if err != nil {
		return err
	}
	if current.SessionID == params.sessionID &&
		current.Revision == params.revision &&
		current.PayloadHash == params.payloadHash {
		return nil
	}
	return errDaemonUpdateObservationConflict
}

func (h *Handler) advanceDaemonUpdateObservation(
	ctx context.Context,
	runtime db.AgentRuntime,
	observation *protocol.DaemonUpdateObservation,
) {
	if observation == nil || !runtime.DaemonID.Valid || strings.TrimSpace(runtime.DaemonID.String) == "" {
		return
	}
	params, err := normalizeDaemonUpdateObservation(*observation)
	if err != nil {
		slog.Warn("daemon heartbeat carried invalid auto-update observation", "runtime_id", uuidToString(runtime.ID), "error", err)
		return
	}
	_, err = h.Queries.AdvanceDaemonUpdateStatus(ctx, db.AdvanceDaemonUpdateStatusParams{
		Revision:                   params.revision,
		ObservedAt:                 params.observedAt,
		AutoUpdateEffectiveEnabled: params.autoUpdateEffectiveEnabled,
		ConfigSource:               params.configSource,
		IneligibleReason:           params.ineligibleReason,
		CheckIntervalSeconds:       params.checkIntervalSeconds,
		Phase:                      params.phase,
		AttemptSource:              params.attemptSource,
		LastAttemptAt:              params.lastAttemptAt,
		LastOutcome:                params.lastOutcome,
		TargetVersion:              params.targetVersion,
		ErrorCode:                  params.errorCode,
		ErrorMessage:               params.errorMessage,
		StagedVersion:              params.stagedVersion,
		ActivationGeneration:       params.activationGeneration,
		PayloadHash:                params.payloadHash,
		WorkspaceID:                runtime.WorkspaceID,
		DaemonID:                   strings.TrimSpace(runtime.DaemonID.String),
		SessionID:                  params.sessionID,
	})
	if err != nil {
		slog.Warn("failed to advance daemon auto-update observation", "runtime_id", uuidToString(runtime.ID), "error", err)
	}
	// affected == 0 is the normal hot path: an identical current revision,
	// an out-of-order transport, or a stale process. The monotonic UPDATE is
	// itself the fail-closed boundary; do not add a diagnostic SELECT to every
	// ordinary heartbeat.
}

func daemonUpdateValueAllowed(value string, allowed ...string) bool {
	for _, candidate := range allowed {
		if value == candidate {
			return true
		}
	}
	return false
}

func optionalDaemonUpdateText(value string) pgtype.Text {
	if value == "" {
		return pgtype.Text{}
	}
	return pgtype.Text{String: value, Valid: true}
}

func deleteOrphanDaemonUpdateStatusForRuntime(ctx context.Context, queries *db.Queries, runtime db.AgentRuntime) error {
	daemonID := runtimeDaemonKey(runtime)
	if queries == nil || daemonID == "" {
		return nil
	}
	return queries.DeleteDaemonUpdateStatusIfOrphan(ctx, db.DeleteDaemonUpdateStatusIfOrphanParams{
		WorkspaceID: runtime.WorkspaceID,
		DaemonID:    daemonID,
	})
}

func (h *Handler) daemonUpdateStatusForRuntime(ctx context.Context, runtime db.AgentRuntime) *DaemonUpdateStatusResponse {
	daemonID := runtimeDaemonKey(runtime)
	if h == nil || h.Queries == nil || daemonID == "" {
		return nil
	}
	row, err := h.Queries.GetDaemonUpdateStatus(ctx, db.GetDaemonUpdateStatusParams{
		WorkspaceID: runtime.WorkspaceID,
		DaemonID:    daemonID,
	})
	if err != nil {
		if !errors.Is(err, pgx.ErrNoRows) {
			slog.Warn("failed to load daemon auto-update status", "runtime_id", uuidToString(runtime.ID), "error", err)
		}
		return nil
	}
	return daemonUpdateStatusToResponse(row)
}

func (h *Handler) daemonUpdateStatusesForList(ctx context.Context, runtimes []db.AgentRuntime) map[string]*DaemonUpdateStatusResponse {
	result := make(map[string]*DaemonUpdateStatusResponse)
	if h == nil || h.Queries == nil || len(runtimes) == 0 {
		return result
	}
	daemonSet := make(map[string]struct{})
	var workspaceID pgtype.UUID
	for _, runtime := range runtimes {
		if !workspaceID.Valid {
			workspaceID = runtime.WorkspaceID
		}
		if daemonID := runtimeDaemonKey(runtime); daemonID != "" {
			daemonSet[daemonID] = struct{}{}
		}
	}
	if len(daemonSet) == 0 {
		return result
	}
	daemonIDs := make([]string, 0, len(daemonSet))
	for daemonID := range daemonSet {
		daemonIDs = append(daemonIDs, daemonID)
	}
	rows, err := h.Queries.ListDaemonUpdateStatusesForWorkspace(ctx, db.ListDaemonUpdateStatusesForWorkspaceParams{
		WorkspaceID: workspaceID,
		DaemonIds:   daemonIDs,
	})
	if err != nil {
		slog.Warn("failed to list daemon auto-update statuses", "workspace_id", uuidToString(workspaceID), "error", err)
		return result
	}
	for _, row := range rows {
		result[row.DaemonID] = daemonUpdateStatusToResponse(row)
	}
	return result
}

func daemonUpdateStatusToResponse(row db.DaemonUpdateStatus) *DaemonUpdateStatusResponse {
	var activationGeneration *int64
	if row.ActivationGeneration.Valid {
		value := row.ActivationGeneration.Int64
		activationGeneration = &value
	}
	return &DaemonUpdateStatusResponse{
		SessionID:                  uuidToString(row.SessionID),
		Revision:                   row.Revision,
		ObservedAt:                 timestampToString(row.ObservedAt),
		AutoUpdateEffectiveEnabled: row.AutoUpdateEffectiveEnabled,
		ConfigSource:               row.ConfigSource,
		IneligibleReason:           textToPtr(row.IneligibleReason),
		CheckIntervalSeconds:       row.CheckIntervalSeconds,
		Phase:                      row.Phase,
		AttemptSource:              textToPtr(row.AttemptSource),
		LastAttemptAt:              timestampToPtr(row.LastAttemptAt),
		LastOutcome:                row.LastOutcome,
		TargetVersion:              textToPtr(row.TargetVersion),
		ErrorCode:                  textToPtr(row.ErrorCode),
		ErrorMessage:               textToPtr(row.ErrorMessage),
		StagedVersion:              textToPtr(row.StagedVersion),
		ActivationGeneration:       activationGeneration,
		ReceivedAt:                 timestampToString(row.ReceivedAt),
		UpdatedAt:                  timestampToString(row.UpdatedAt),
	}
}
