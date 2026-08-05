package middleware

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/go-chi/chi/v5"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/multica-ai/multica/server/internal/auth"
)

// fakeDiagnosisRunLoader is an in-memory DiagnosisRunLoader for middleware tests.
type fakeDiagnosisRunLoader struct {
	runs map[string]DiagnosisRun
	err  error
}

func (f *fakeDiagnosisRunLoader) GetRun(_ context.Context, runID string) (DiagnosisRun, error) {
	if f.err != nil {
		return DiagnosisRun{}, f.err
	}
	run, ok := f.runs[runID]
	if !ok {
		return DiagnosisRun{}, ErrDiagnosisRunNotFound
	}
	return run, nil
}

const diagnosisTestToken = "0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef"

func newDiagnosisAuthTestRouter(loader DiagnosisRunLoader) (chi.Router, *DiagnosisRun) {
	var injected DiagnosisRun
	r := chi.NewRouter()
	r.Route("/api/v1/diagnosis-runs/{runID}", func(r chi.Router) {
		r.Use(DiagnosisRunAuth(loader))
		r.Get("/diagnosis-progress", func(w http.ResponseWriter, r *http.Request) {
			run, ok := DiagnosisRunFromContext(r.Context())
			if !ok {
				w.WriteHeader(http.StatusInternalServerError)
				return
			}
			injected = run
			w.WriteHeader(http.StatusOK)
		})
	})
	return r, &injected
}

func newDiagnosisRun(status string) DiagnosisRun {
	return DiagnosisRun{
		RunID:               "run-1",
		ProjectID:           "project-1",
		TaskID:              "task-1",
		TopologyHash:        "topo-1",
		OrderedSegmentIDs:   []string{"seg-1"},
		Status:              status,
		CapabilityTokenHash: auth.HashToken(diagnosisTestToken),
		ExecutionMode:       "sandbox",
	}
}

func doDiagnosisAuthRequest(t *testing.T, router http.Handler, runID, authHeader string) *httptest.ResponseRecorder {
	t.Helper()
	req := httptest.NewRequest(http.MethodGet, "/api/v1/diagnosis-runs/"+runID+"/diagnosis-progress", nil)
	if authHeader != "" {
		req.Header.Set("Authorization", authHeader)
	}
	w := httptest.NewRecorder()
	router.ServeHTTP(w, req)
	return w
}

func TestDiagnosisRunAuth_MissingToken(t *testing.T) {
	loader := &fakeDiagnosisRunLoader{runs: map[string]DiagnosisRun{"run-1": newDiagnosisRun("running")}}
	router, _ := newDiagnosisAuthTestRouter(loader)

	w := doDiagnosisAuthRequest(t, router, "run-1", "")
	assert.Equal(t, http.StatusUnauthorized, w.Code)
}

func TestDiagnosisRunAuth_RejectsTokenMismatch(t *testing.T) {
	loader := &fakeDiagnosisRunLoader{runs: map[string]DiagnosisRun{"run-1": newDiagnosisRun("running")}}
	router, _ := newDiagnosisAuthTestRouter(loader)

	w := doDiagnosisAuthRequest(t, router, "run-1", "Bearer wrong-token")
	assert.Equal(t, http.StatusForbidden, w.Code)
}

func TestDiagnosisRunAuth_RejectsUnknownRun(t *testing.T) {
	loader := &fakeDiagnosisRunLoader{runs: map[string]DiagnosisRun{"run-1": newDiagnosisRun("running")}}
	router, _ := newDiagnosisAuthTestRouter(loader)

	w := doDiagnosisAuthRequest(t, router, "run-unknown", "Bearer "+diagnosisTestToken)
	assert.Equal(t, http.StatusNotFound, w.Code)
	assert.Contains(t, w.Body.String(), "run_not_found")
}

func TestDiagnosisRunAuth_RejectsTerminalRun(t *testing.T) {
	for _, status := range []string{DiagnosisRunStatusCompleted, DiagnosisRunStatusFailed} {
		loader := &fakeDiagnosisRunLoader{runs: map[string]DiagnosisRun{"run-1": newDiagnosisRun(status)}}
		router, _ := newDiagnosisAuthTestRouter(loader)

		w := doDiagnosisAuthRequest(t, router, "run-1", "Bearer "+diagnosisTestToken)
		assert.Equal(t, http.StatusForbidden, w.Code, "status %s", status)
		assert.JSONEq(t, `{"error":"run_terminal"}`, w.Body.String())
	}
}

func TestDiagnosisRunAuth_RejectsRunWithoutTokenHash(t *testing.T) {
	run := newDiagnosisRun("running")
	run.CapabilityTokenHash = ""
	loader := &fakeDiagnosisRunLoader{runs: map[string]DiagnosisRun{"run-1": run}}
	router, _ := newDiagnosisAuthTestRouter(loader)

	w := doDiagnosisAuthRequest(t, router, "run-1", "Bearer "+diagnosisTestToken)
	assert.Equal(t, http.StatusForbidden, w.Code)
}

func TestDiagnosisRunAuth_LookupFailureIs503(t *testing.T) {
	loader := &fakeDiagnosisRunLoader{err: errors.New("db down")}
	router, _ := newDiagnosisAuthTestRouter(loader)

	w := doDiagnosisAuthRequest(t, router, "run-1", "Bearer "+diagnosisTestToken)
	assert.Equal(t, http.StatusServiceUnavailable, w.Code)
}

func TestDiagnosisRunAuth_InjectsRunRecord(t *testing.T) {
	loader := &fakeDiagnosisRunLoader{runs: map[string]DiagnosisRun{"run-1": newDiagnosisRun("provisioning")}}
	router, injected := newDiagnosisAuthTestRouter(loader)

	w := doDiagnosisAuthRequest(t, router, "run-1", "Bearer "+diagnosisTestToken)
	require.Equal(t, http.StatusOK, w.Code)
	assert.Equal(t, "run-1", injected.RunID)
	assert.Equal(t, "project-1", injected.ProjectID)
	assert.Equal(t, "provisioning", injected.Status)
	assert.Equal(t, []string{"seg-1"}, injected.OrderedSegmentIDs)
}
