package middleware

import (
	"context"
	"crypto/subtle"
	"errors"
	"log/slog"
	"net/http"

	"github.com/go-chi/chi/v5"
	"github.com/multica-ai/multica/server/internal/auth"
)

// Diagnosis-run capability-token auth (spec 005). The sandboxed diagnosis
// agent authenticates with a per-run bearer token minted at provisioning
// time; the server stores only the token's SHA-256 hash on the run row.
// This middleware resolves the {runID} route param, verifies the token
// against that run's stored hash (constant-time), rejects terminal runs,
// and injects the run record into the request context for the handlers.
//
// The run record is a middleware-local type because internal/service depends
// on this package (via internal/metrics); the concrete loader in
// cmd/server/router.go adapts service.DiagnosisStateStore.

// ErrDiagnosisRunNotFound is returned by DiagnosisRunLoader when the run ID
// does not exist; the middleware maps it to 404.
var ErrDiagnosisRunNotFound = errors.New("diagnosis run not found")

// DiagnosisRun is the run record the capability-token routes operate on.
type DiagnosisRun struct {
	RunID               string
	ProjectID           string
	TaskID              string
	TopologyHash        string
	OrderedSegmentIDs   []string
	Status              string
	CapabilityTokenHash string
	ExecutionMode       string
	SandboxInstanceID   string
}

// Terminal diagnosis run statuses; mirrors service.DiagnosisRunStatus.
const (
	DiagnosisRunStatusCompleted = "completed"
	DiagnosisRunStatusFailed    = "failed"
)

type diagnosisRunContextKey int

const ctxKeyDiagnosisRun diagnosisRunContextKey = iota

// DiagnosisRunLoader is the narrow read surface the middleware needs; the
// production implementation adapts *service.DiagnosisStateStore.
type DiagnosisRunLoader interface {
	GetRun(ctx context.Context, runID string) (DiagnosisRun, error)
}

// DiagnosisRunFromContext returns the run record injected by DiagnosisRunAuth.
func DiagnosisRunFromContext(ctx context.Context) (DiagnosisRun, bool) {
	run, ok := ctx.Value(ctxKeyDiagnosisRun).(DiagnosisRun)
	return run, ok
}

// WithDiagnosisRun returns a context carrying the run record. Used by tests
// to exercise handlers without the middleware.
func WithDiagnosisRun(ctx context.Context, run DiagnosisRun) context.Context {
	return context.WithValue(ctx, ctxKeyDiagnosisRun, run)
}

// DiagnosisRunAuth verifies the per-run capability token for
// /api/v1/diagnosis-runs/{runID} routes.
func DiagnosisRunAuth(loader DiagnosisRunLoader) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			runID := chi.URLParam(r, "runID")
			if runID == "" {
				writeError(w, http.StatusBadRequest, "run ID required")
				return
			}
			run, err := loader.GetRun(r.Context(), runID)
			if err != nil {
				if errors.Is(err, ErrDiagnosisRunNotFound) {
					writeError(w, http.StatusNotFound, "run_not_found")
					return
				}
				slog.Warn("diagnosis_auth: run lookup failed", "run_id", runID, "error", err)
				writeError(w, http.StatusServiceUnavailable, "run lookup unavailable")
				return
			}

			token, ok := bearerToken(w, r)
			if !ok {
				return
			}
			// Constant-time verify against the stored SHA-256 hash. An empty
			// stored hash never matches: server-mode runs have no capability
			// token and must not be reachable over this API. The hash scheme
			// is shared with service.HashDiagnosisCapabilityToken.
			if run.CapabilityTokenHash == "" ||
				subtle.ConstantTimeCompare([]byte(auth.HashToken(token)), []byte(run.CapabilityTokenHash)) != 1 {
				slog.Warn("diagnosis_auth: capability token mismatch", "run_id", runID)
				writeError(w, http.StatusForbidden, "capability token does not match run")
				return
			}
			if run.Status == DiagnosisRunStatusCompleted || run.Status == DiagnosisRunStatusFailed {
				writeError(w, http.StatusForbidden, "run_terminal")
				return
			}

			next.ServeHTTP(w, r.WithContext(WithDiagnosisRun(r.Context(), run)))
		})
	}
}
