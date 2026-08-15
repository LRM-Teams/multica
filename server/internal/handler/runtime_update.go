package handler

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"net/http"
	"strings"
	"sync"
	"time"

	"github.com/go-chi/chi/v5"
	db "github.com/multica-ai/multica/server/pkg/db/generated"
	"github.com/multica-ai/multica/server/pkg/protocol"
)

// ---------------------------------------------------------------------------
// CLI update request store
// ---------------------------------------------------------------------------

type UpdateStatus string

const (
	UpdatePending   UpdateStatus = "pending"
	UpdateRunning   UpdateStatus = "running"
	UpdateCompleted UpdateStatus = "completed"
	UpdateReady     UpdateStatus = "ready_to_apply"
	UpdateFailed    UpdateStatus = "failed"
	UpdateTimeout   UpdateStatus = "timeout"
	// UpdateQueued is a compatibility projection for a Machine Upgrade that
	// has not yet been accepted by its daemon. It is never persisted in the
	// retired runtime-owned update store.
	UpdateQueued UpdateStatus = "queued"
)

// UpdateRequest represents a pending or completed CLI update request.
type UpdateRequest struct {
	ID            string       `json:"id"`
	RuntimeID     string       `json:"runtime_id"`
	Status        UpdateStatus `json:"status"`
	TargetVersion string       `json:"target_version"`
	Output        string       `json:"output,omitempty"`
	Error         string       `json:"error,omitempty"`
	CreatedAt     time.Time    `json:"created_at"`
	UpdatedAt     time.Time    `json:"updated_at"`
	RunStartedAt  *time.Time   `json:"-"`
}

const (
	updatePendingTimeout = 120 * time.Second
	updateRunningTimeout = 150 * time.Second
	// updateReadyTimeout is the control-plane safety net for stuck
	// ready_to_apply rows (D7 / #815 B0). Clock starts when status becomes
	// ready_to_apply (UpdatedAt at that transition). 20m leaves 5m grace after
	// daemon T_hard=15m. Must match expireStale / applyUpdateTimeout.
	updateReadyTimeout   = 20 * time.Minute
	updateConfirmTimeout = 120 * time.Second
	updateStoreRetention = 5 * time.Minute
	// Keep the last terminal update around long enough for a backend deploy /
	// restart to preserve the user's "I just clicked Update" result and reason.
	updateTerminalRetention = 6 * time.Hour

	updateReadyTimeoutError = "activation did not complete within 20 minutes"
)

type UpdateStore interface {
	Create(ctx context.Context, runtimeID, targetVersion string) (*UpdateRequest, error)
	Get(ctx context.Context, id string) (*UpdateRequest, error)
	LatestForRuntime(ctx context.Context, runtimeID string) (*UpdateRequest, error)
	HasPending(ctx context.Context, runtimeID string) (bool, error)
	PopPending(ctx context.Context, runtimeID string) (*UpdateRequest, error)
	Complete(ctx context.Context, id string, output string) error
	ReadyToApply(ctx context.Context, id string, output string) error
	Fail(ctx context.Context, id string, errMsg string) error
}

func updateRequestTerminal(status UpdateStatus) bool {
	return status == UpdateCompleted || status == UpdateFailed || status == UpdateTimeout
}

func updateRequestBlocksNewRequest(status UpdateStatus) bool {
	return status == UpdatePending || status == UpdateRunning || status == UpdateReady
}

func applyUpdateTimeout(req *UpdateRequest, now time.Time) bool {
	switch req.Status {
	case UpdatePending:
		if now.Sub(req.CreatedAt) > updatePendingTimeout {
			req.Status = UpdateTimeout
			req.Error = "daemon did not respond within 120 seconds"
			req.UpdatedAt = now
			return true
		}
	case UpdateRunning:
		if req.RunStartedAt != nil && now.Sub(*req.RunStartedAt) > updateRunningTimeout {
			req.Status = UpdateTimeout
			req.Error = "update did not complete within 150 seconds"
			req.UpdatedAt = now
			return true
		}
	case UpdateReady:
		// Clock = UpdatedAt when entered ready_to_apply (set by ReadyToApply).
		if now.Sub(req.UpdatedAt) > updateReadyTimeout {
			req.Status = UpdateTimeout
			req.Error = updateReadyTimeoutError
			req.UpdatedAt = now
			return true
		}
	}
	return false
}

// InMemoryUpdateStore is a deterministic unit-test implementation. Production
// handlers always use PostgresUpdateStore so Web POST, daemon heartbeat,
// daemon report, UI polling, and API process replacement share one lifecycle.
type InMemoryUpdateStore struct {
	mu       sync.Mutex
	requests map[string]*UpdateRequest // keyed by update ID
}

func NewInMemoryUpdateStore() *InMemoryUpdateStore {
	return &InMemoryUpdateStore{
		requests: make(map[string]*UpdateRequest),
	}
}

func (s *InMemoryUpdateStore) Create(_ context.Context, runtimeID, targetVersion string) (*UpdateRequest, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	id := randomID()

	// Clean up old requests.
	now := time.Now()
	for id, req := range s.requests {
		applyUpdateTimeout(req, now)
		if updateRequestTerminal(req.Status) && now.Sub(req.UpdatedAt) > updateTerminalRetention {
			delete(s.requests, id)
		}
	}

	if existing, ok := s.requests[id]; ok {
		if existing.RuntimeID == runtimeID && existing.TargetVersion == targetVersion {
			return existing, nil
		}
		return nil, &updateError{msg: "update ID is already bound to another runtime update"}
	}

	// Reject if there is already an active (pending/running/ready) update.
	// applyUpdateTimeout above may have just freed a stuck ready_to_apply.
	for _, req := range s.requests {
		if req.RuntimeID == runtimeID && updateRequestBlocksNewRequest(req.Status) {
			return nil, errUpdateInProgress
		}
	}

	req := &UpdateRequest{
		ID:            id,
		RuntimeID:     runtimeID,
		Status:        UpdatePending,
		TargetVersion: targetVersion,
		CreatedAt:     now,
		UpdatedAt:     now,
	}
	s.requests[req.ID] = req
	return req, nil
}

var errUpdateInProgress = &updateError{msg: "an update is already in progress for this runtime"}

// runtimePinnedVersion centralizes the "pin wins" rule (task #81): a runtime
// pinned via MULTICA_PINNED_VERSION must never receive an automatic or
// queued update, on any delivery path (historical intent materialization or
// heartbeat delivery) — checked here
// once so a future rule change (e.g. an override) only needs one edit.
func runtimePinnedVersion(rt db.AgentRuntime) (version string, pinned bool) {
	if !rt.PinnedVersion.Valid {
		return "", false
	}
	v := strings.TrimSpace(rt.PinnedVersion.String)
	return v, v != ""
}

type updateError struct{ msg string }

func (e *updateError) Error() string { return e.msg }

func (s *InMemoryUpdateStore) Get(_ context.Context, id string) (*UpdateRequest, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	req, ok := s.requests[id]
	if !ok {
		return nil, nil
	}
	applyUpdateTimeout(req, time.Now())
	return req, nil
}

func (s *InMemoryUpdateStore) LatestForRuntime(_ context.Context, runtimeID string) (*UpdateRequest, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	now := time.Now()
	var latest *UpdateRequest
	for _, req := range s.requests {
		if req.RuntimeID != runtimeID {
			continue
		}
		applyUpdateTimeout(req, now)
		if latest == nil || req.UpdatedAt.After(latest.UpdatedAt) {
			latest = req
		}
	}
	return latest, nil
}

// LatestForRuntimes returns the newest request per requested runtime in one
// locked scan. ListAgentRuntimes uses it through its optional batch-store
// contract; single-runtime lifecycle callers keep using LatestForRuntime.
func (s *InMemoryUpdateStore) LatestForRuntimes(_ context.Context, runtimeIDs []string) (map[string]*UpdateRequest, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	requested := make(map[string]struct{}, len(runtimeIDs))
	for _, runtimeID := range runtimeIDs {
		if runtimeID != "" {
			requested[runtimeID] = struct{}{}
		}
	}
	latest := make(map[string]*UpdateRequest, len(requested))
	now := time.Now()
	for _, req := range s.requests {
		if _, ok := requested[req.RuntimeID]; !ok {
			continue
		}
		applyUpdateTimeout(req, now)
		if current := latest[req.RuntimeID]; current == nil || req.UpdatedAt.After(current.UpdatedAt) {
			latest[req.RuntimeID] = req
		}
	}
	return latest, nil
}

func (s *InMemoryUpdateStore) HasPending(_ context.Context, runtimeID string) (bool, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	now := time.Now()
	for _, req := range s.requests {
		applyUpdateTimeout(req, now)
		if req.RuntimeID == runtimeID && req.Status == UpdatePending {
			return true, nil
		}
	}
	return false, nil
}

// PopPending returns and marks as running the pending update for a runtime.
func (s *InMemoryUpdateStore) PopPending(_ context.Context, runtimeID string) (*UpdateRequest, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	var oldest *UpdateRequest
	now := time.Now()
	for _, req := range s.requests {
		applyUpdateTimeout(req, now)
		if req.RuntimeID == runtimeID && req.Status == UpdatePending {
			if oldest == nil || req.CreatedAt.Before(oldest.CreatedAt) {
				oldest = req
			}
		}
	}
	if oldest != nil {
		oldest.Status = UpdateRunning
		startedAt := now
		oldest.RunStartedAt = &startedAt
		oldest.UpdatedAt = now
	}
	return oldest, nil
}

func (s *InMemoryUpdateStore) Complete(_ context.Context, id string, output string) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	if req, ok := s.requests[id]; ok {
		if req.Status == UpdateCompleted {
			return nil
		}
		if req.Status != UpdateRunning && req.Status != UpdateReady {
			return invalidUpdateTransition(req.Status, UpdateCompleted)
		}
		req.Status = UpdateCompleted
		req.Output = output
		req.Error = ""
		req.UpdatedAt = time.Now()
	}
	return nil
}

func (s *InMemoryUpdateStore) ReadyToApply(_ context.Context, id string, output string) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	if req, ok := s.requests[id]; ok {
		if req.Status == UpdateReady {
			return nil
		}
		if req.Status != UpdateRunning {
			return invalidUpdateTransition(req.Status, UpdateReady)
		}
		req.Status = UpdateReady
		req.Output = output
		req.Error = ""
		req.UpdatedAt = time.Now()
	}
	return nil
}

func (s *InMemoryUpdateStore) Fail(_ context.Context, id string, errMsg string) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	if req, ok := s.requests[id]; ok {
		if req.Status == UpdateFailed {
			return nil
		}
		// #815 B-cutover path A: ready_to_apply → failed (e.g. drain_timeout)
		// is a legal terminal abandon; completed/timeout stay non-overwritable here.
		if req.Status != UpdateRunning && req.Status != UpdateReady {
			return invalidUpdateTransition(req.Status, UpdateFailed)
		}
		req.Status = UpdateFailed
		req.Output = ""
		req.Error = errMsg
		req.UpdatedAt = time.Now()
	}
	return nil
}

type updateTransitionError struct {
	from UpdateStatus
	to   UpdateStatus
}

func (e *updateTransitionError) Error() string {
	return "invalid daemon runtime update transition " + string(e.from) + " -> " + string(e.to)
}

func invalidUpdateTransition(from, to UpdateStatus) error {
	return &updateTransitionError{from: from, to: to}
}

// InitiateUpdate is the temporary runtime-scoped compatibility adapter for
// installed clients. Its runtime only supplies authorization and the daemon
// identity; createMachineUpgrade remains the sole mutation owner.
func (h *Handler) InitiateUpdate(w http.ResponseWriter, r *http.Request) {
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
	if !canOwnRuntime(member, rt) {
		writeError(w, http.StatusForbidden, "only the computer owner can update this runtime")
		return
	}

	var req createMachineUpgradeRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil && !errors.Is(err, io.EOF) {
		writeError(w, http.StatusBadRequest, "invalid request body")
		return
	}
	op, _, err := h.createMachineUpgrade(r, rt, member, req, true)
	if err != nil {
		h.writeMachineUpgradeError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, runtimeUpdateFromMachineUpgrade(op, uuidToString(rt.ID)))
}

// GetUpdate projects a daemon-scoped Machine Upgrade through the legacy
// runtime response shape. It deliberately never reads the retired
// runtime-owned update state.
func (h *Handler) GetUpdate(w http.ResponseWriter, r *http.Request) {
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
	if !canOwnRuntime(member, rt) {
		writeError(w, http.StatusForbidden, "only the computer owner can inspect this update")
		return
	}
	if h.MachineUpgradeStore == nil {
		writeError(w, http.StatusServiceUnavailable, "machine upgrade store is not configured")
		return
	}
	op, err := h.MachineUpgradeStore.Get(r.Context(), runtimeDaemonKey(rt), chi.URLParam(r, "updateId"))
	if err != nil {
		writeError(w, http.StatusInternalServerError, "failed to load machine upgrade: "+err.Error())
		return
	}
	if op == nil {
		writeError(w, http.StatusNotFound, "update not found")
		return
	}
	writeJSON(w, http.StatusOK, runtimeUpdateFromMachineUpgrade(op, uuidToString(rt.ID)))
}

// CancelUpdateIntent is retained at its historical URL. It cancels only the
// current queued Machine Upgrade; accepted work keeps the canonical conflict
// boundary and is never silently withdrawn.
func (h *Handler) CancelUpdateIntent(w http.ResponseWriter, r *http.Request) {
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
	if !canOwnRuntime(member, rt) {
		writeError(w, http.StatusForbidden, "only the computer owner can cancel this update")
		return
	}
	if h.MachineUpgradeStore == nil {
		writeError(w, http.StatusServiceUnavailable, "machine upgrade store is not configured")
		return
	}
	op, err := h.MachineUpgradeStore.LatestForDaemon(r.Context(), runtimeDaemonKey(rt))
	if err != nil {
		writeError(w, http.StatusInternalServerError, "failed to load machine upgrade: "+err.Error())
		return
	}
	if op == nil || op.Phase.terminal() {
		writeJSON(w, http.StatusOK, map[string]string{"status": "ok"})
		return
	}
	if _, err := h.MachineUpgradeStore.Cancel(r.Context(), op.DaemonID, op.ID); err != nil {
		if errors.Is(err, errMachineUpgradeAlreadyAccepted) {
			writeCodedError(w, http.StatusConflict, "machine_upgrade_already_accepted", err.Error())
			return
		}
		writeError(w, http.StatusInternalServerError, "failed to cancel machine upgrade: "+err.Error())
		return
	}
	h.publishComputerUpgradeProjection(r, runtimeDaemonKey(rt))
	writeJSON(w, http.StatusOK, map[string]string{"status": "ok"})
}

// maybeMaterializeUpdateIntent turns a durable UpdateIntent into a concrete
// daemon_runtime_update attempt the moment a heartbeat proves the runtime is
// reachable. Called unconditionally on every heartbeat (handler/daemon.go,
// right before the existing HasPending/PopPending block) — cheap no-op when
// there's no live intent, which is the common case.
func (h *Handler) maybeMaterializeUpdateIntent(ctx context.Context, rt db.AgentRuntime) {
	if h.UpdateIntentStore == nil || h.UpdateStore == nil || h.RuntimeReleaseSource == nil {
		return
	}
	runtimeID := uuidToString(rt.ID)
	// Pin wins (task #81): an intent always resolves to whatever's newest at
	// delivery time — fundamentally
	// incompatible with "stay on this version". Leave the intent live and
	// un-materialized rather than consume it into an attempt that would
	// never be delivered (see the heartbeat-delivery gate in daemon.go for
	// why materializing anyway would strand a permanently-undeliverable
	// attempt). Lifting the pin requires a daemon restart (task #81), which
	// reports the new state on its next register — the following heartbeat
	// picks this back up normally.
	if _, pinned := runtimePinnedVersion(rt); pinned {
		return
	}
	intent, err := h.UpdateIntentStore.Get(ctx, runtimeID)
	if err != nil {
		slog.Warn("get update intent failed", "error", err, "runtime_id", runtimeID)
		return
	}
	if !intent.Live() {
		return
	}

	if time.Now().After(intent.ExpiresAt) {
		if err := h.UpdateIntentStore.MarkExpired(ctx, runtimeID); err != nil {
			slog.Warn("mark update intent expired failed", "error", err, "runtime_id", runtimeID)
			return
		}
		// Never a silent disappearance (Parker's rule, 2026-08-02): the row
		// stays readable via GetUpdate with status "timeout" until an admin
		// re-requests or explicitly cancels — this log line plus that
		// visible terminal state are the trail.
		slog.Warn("update intent expired without being delivered", "runtime_id", runtimeID, "created_at", intent.CreatedAt)
		return
	}

	latest, err := h.UpdateStore.LatestForRuntime(ctx, runtimeID)
	if err != nil {
		slog.Warn("check latest update for intent materialization failed", "error", err, "runtime_id", runtimeID)
		return
	}
	if latest != nil {
		if updateRequestBlocksNewRequest(latest.Status) {
			// Already in flight — this heartbeat's own HasPending/PopPending
			// will deliver or progress it; nothing to materialize.
			return
		}
		if latest.Status == UpdateCompleted && !latest.UpdatedAt.Before(intent.CreatedAt) {
			// Fulfilled by an attempt created at/after this intent — done.
			if err := h.UpdateIntentStore.Delete(ctx, runtimeID); err != nil {
				slog.Warn("delete fulfilled update intent failed", "error", err, "runtime_id", runtimeID)
			}
			return
		}
		if (latest.Status == UpdateFailed || latest.Status == UpdateTimeout) &&
			latest.ID != intent.LastFailedAttemptID &&
			!latest.UpdatedAt.Before(intent.CreatedAt) {
			// A real attempt, belonging to THIS intent (created at/after
			// intent.CreatedAt — a re-request via Create() resets
			// LastFailedAttemptID, so without this temporal guard a stale
			// failed attempt from a prior, already-given-up cycle would be
			// misread as a fresh failure and immediately re-trigger backoff
			// on a request that should retry right away), was materialized
			// and failed — this, and only this, is what counts toward the
			// retry budget (Parker's non-negotiable, 2026-08-02): a runtime
			// with no heartbeat never reaches this line at all, since this
			// whole function only runs when a heartbeat just arrived. Fold
			// it in and wait out the backoff instead of retrying on this
			// same heartbeat.
			if err := h.UpdateIntentStore.RecordFailure(ctx, runtimeID, latest.ID); err != nil {
				slog.Warn("record update intent failure failed", "error", err, "runtime_id", runtimeID)
			}
			return
		}
		// Otherwise latest is a terminal failed/timeout attempt already
		// folded into the backoff above on a prior heartbeat, or predates
		// this intent entirely (a fresh re-request past a prior give-up) —
		// fall through to the due-for-retry check below.
	}

	// IsDueForRetry, not intent.DueForRetry(time.Now()) — task #80: NextRetryAt
	// is written using the database's clock (RecordFailure/Create both use
	// now()), so comparing it against this application server's time.Now()
	// crossed two clocks and could misjudge a just-eligible intent as not due
	// yet. The SQL-side comparison below never leaves the database's clock.
	due, err := h.UpdateIntentStore.IsDueForRetry(ctx, runtimeID)
	if err != nil {
		slog.Warn("check update intent retry due failed", "error", err, "runtime_id", runtimeID)
		return
	}
	if !due {
		return // backing off after a recent failure — not due yet
	}

	release, err := h.RuntimeReleaseSource.Latest(ctx)
	if err != nil || release == nil {
		slog.Warn("resolve latest release for intent materialization failed", "error", err, "runtime_id", runtimeID)
		return // try again on the next heartbeat
	}
	if _, err := h.UpdateStore.Create(ctx, runtimeID, release.TagName); err != nil && !errors.Is(err, errUpdateInProgress) {
		slog.Warn("materialize update intent into attempt failed", "error", err, "runtime_id", runtimeID)
	}
}

// ReportUpdateResult receives the update result from the daemon.
func (h *Handler) ReportUpdateResult(w http.ResponseWriter, r *http.Request) {
	runtimeID := chi.URLParam(r, "runtimeId")

	// Verify the caller owns this runtime's workspace.
	rt, ok := h.requireDaemonRuntimeAccess(w, r, runtimeID)
	if !ok {
		return
	}

	updateID := chi.URLParam(r, "updateId")

	var req struct {
		Status string `json:"status"` // "running", "completed", "ready_to_apply", or "failed"
		Output string `json:"output"`
		Error  string `json:"error"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid request body")
		return
	}
	requestedStatus := UpdateStatus(req.Status)
	switch requestedStatus {
	case UpdateRunning, UpdateCompleted, UpdateReady, UpdateFailed:
	default:
		writeError(w, http.StatusBadRequest, "invalid status: "+req.Status)
		return
	}

	existing, err := h.UpdateStore.Get(r.Context(), updateID)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "failed to load update: "+err.Error())
		return
	}
	if existing == nil || existing.RuntimeID != runtimeID {
		writeError(w, http.StatusNotFound, "update not found")
		return
	}
	if existing.Status == requestedStatus {
		slog.Debug("ignoring idempotent update report", "runtime_id", runtimeID, "update_id", updateID, "status", existing.Status)
		writeJSON(w, http.StatusOK, map[string]string{"status": "ok"})
		return
	}
	// Narrow late report: server already timeout + daemon late failed+drain_timeout → 200 no-op.
	// completed + late failed stays 409 via transition check below.
	if lateFailedReportIsNoOp(existing, requestedStatus, req.Error) {
		slog.Debug("ignoring late drain_timeout after server timeout", "runtime_id", runtimeID, "update_id", updateID)
		writeJSON(w, http.StatusOK, map[string]string{"status": "ok"})
		return
	}
	if !updateReportTransitionAllowed(existing.Status, requestedStatus) {
		writeError(w, http.StatusConflict, invalidUpdateTransition(existing.Status, requestedStatus).Error())
		return
	}

	switch requestedStatus {
	case UpdateCompleted:
		if err := h.UpdateStore.Complete(r.Context(), updateID, req.Output); err != nil {
			var transitionErr *updateTransitionError
			if errors.As(err, &transitionErr) {
				writeError(w, http.StatusConflict, transitionErr.Error())
				return
			}
			slog.Error("UpdateStore Complete failed", "error", err, "update_id", updateID)
			writeError(w, http.StatusInternalServerError, "failed to persist completion")
			return
		}
		h.publish(protocol.EventDaemonRuntimeUpdated, uuidToString(rt.WorkspaceID), "system", "", map[string]any{
			"runtime": h.runtimeToResponse(r.Context(), rt),
		})
	case UpdateReady:
		if err := h.UpdateStore.ReadyToApply(r.Context(), updateID, req.Output); err != nil {
			var transitionErr *updateTransitionError
			if errors.As(err, &transitionErr) {
				writeError(w, http.StatusConflict, transitionErr.Error())
				return
			}
			slog.Error("UpdateStore ReadyToApply failed", "error", err, "update_id", updateID)
			writeError(w, http.StatusInternalServerError, "failed to persist ready-to-apply state")
			return
		}
		h.publish(protocol.EventDaemonRuntimeUpdated, uuidToString(rt.WorkspaceID), "system", "", map[string]any{
			"runtime": h.runtimeToResponse(r.Context(), rt),
		})
	case UpdateFailed:
		if err := h.UpdateStore.Fail(r.Context(), updateID, req.Error); err != nil {
			var transitionErr *updateTransitionError
			if errors.As(err, &transitionErr) {
				writeError(w, http.StatusConflict, transitionErr.Error())
				return
			}
			slog.Error("UpdateStore Fail failed", "error", err, "update_id", updateID)
			writeError(w, http.StatusInternalServerError, "failed to persist failure")
			return
		}
		// The loaded record predates this report; carry the daemon's exact
		// failure into the canonical bridge projection below.
		existing.Error = req.Error
		h.publish(protocol.EventDaemonRuntimeUpdated, uuidToString(rt.WorkspaceID), "system", "", map[string]any{
			"runtime": h.runtimeToResponse(r.Context(), rt),
		})
	case UpdateRunning:
		// No-op: status is already "running" from PopPending. This call is
		// just a progress signal from the daemon to confirm it received the
		// update command and is executing it.
	}

	writeJSON(w, http.StatusOK, map[string]string{"status": "ok"})
}

func updateReportTransitionAllowed(from, to UpdateStatus) bool {
	if from == to {
		return true
	}
	switch from {
	case UpdateRunning:
		return to == UpdateReady || to == UpdateCompleted || to == UpdateFailed
	case UpdateReady:
		// completed (success apply) or failed (path A abandon / drain_timeout).
		return to == UpdateCompleted || to == UpdateFailed
	default:
		return false
	}
}

// DrainTimeoutError is the stable machine string daemons report on D6 path A.
const DrainTimeoutError = "drain_timeout"

// lateFailedReportIsNoOp reports whether a late failed report against an already
// terminal server row should be accepted as 200 no-op (Barry cutover lock):
// only timeout + exact failed+drain_timeout; failed is already covered by
// status-equality idempotency. completed + late failed must stay 409.
func lateFailedReportIsNoOp(existing *UpdateRequest, requestedStatus UpdateStatus, errMsg string) bool {
	if existing == nil || requestedStatus != UpdateFailed {
		return false
	}
	if existing.Status == UpdateTimeout && errMsg == DrainTimeoutError {
		return true
	}
	return false
}
