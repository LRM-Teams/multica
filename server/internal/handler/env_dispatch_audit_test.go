package handler

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/multica-ai/multica/server/internal/middleware"
	"github.com/multica-ai/multica/server/internal/service"
	db "github.com/multica-ai/multica/server/pkg/db/generated"
)

const auditContractUUID = "11111111-1111-1111-1111-111111111111"

type fakeEnvDispatchAuditReportLoader struct {
	report service.EnvDispatchAuditReport
	err    error

	gotAuditID     string
	gotWorkspaceID string
	gotInitiatorID string
}

func (f *fakeEnvDispatchAuditReportLoader) LoadAuditReport(_ context.Context, auditID, workspaceID, initiatorID string, _ time.Time) (service.EnvDispatchAuditReport, error) {
	f.gotAuditID = auditID
	f.gotWorkspaceID = workspaceID
	f.gotInitiatorID = initiatorID
	return f.report, f.err
}

func auditContractRequest() *http.Request {
	r := httptest.NewRequest(http.MethodGet, "/api/v1/env-dispatch/audits/"+auditContractUUID, nil)
	r.Header.Set("X-User-ID", auditContractUUID)
	return r.WithContext(middleware.SetMemberContext(r.Context(), auditContractUUID, db.Member{}))
}

func auditContractReport() service.EnvDispatchAuditReport {
	now := time.Date(2026, time.August, 3, 12, 0, 0, 0, time.UTC)
	return service.EnvDispatchAuditReport{
		AuditID:             auditContractUUID,
		WorkspaceID:         auditContractUUID,
		InitiatorID:         auditContractUUID,
		DispatchType:        service.EnvDispatchAuditDispatchIssue,
		PrimaryScopeID:      "project-1",
		Outcome:             service.EnvDispatchAuditOutcomeSucceeded,
		Verdict:             service.EnvDispatchAuditVerdictPending,
		ReclamationDeadline: now.Add(10 * time.Minute),
		StartedAt:           now,
		CreatedAt:           now,
		UpdatedAt:           now,
		AsOf:                now,
	}
}

// T011 must keep audit opt-in: audited requests preserve their correlation
// request, while every audited result (including a partial failure) carries a
// report locator. The assertions use JSON rather than future T011 types so
// this contract compiles before those types exist.
func TestEnvDispatchAudit_CreateWireContract(t *testing.T) {
	t.Run("request preserves opt-in audit configuration", func(t *testing.T) {
		var request EnvDispatchRequest
		if err := json.Unmarshal([]byte(`{
			"mode":"scratch",
			"env_id":"`+auditContractUUID+`",
			"domain":"swe_lego",
			"dispatch_type":"issue",
			"group_size":1,
			"agent_id":"`+auditContractUUID+`",
			"training_mode":false,
			"issue":{"title":"audit contract only"},
			"audit":{"enabled":true,"reclamation_window_seconds":600}
		}`), &request); err != nil {
			t.Fatalf("decode audited request: %v", err)
		}

		body, err := json.Marshal(request)
		if err != nil {
			t.Fatalf("encode audited request: %v", err)
		}
		var wire map[string]any
		if err := json.Unmarshal(body, &wire); err != nil {
			t.Fatalf("decode audited request wire form: %v", err)
		}
		audit, ok := wire["audit"].(map[string]any)
		if !ok {
			t.Fatalf("audited request lost audit configuration: %s", body)
		}
		if enabled, ok := audit["enabled"].(bool); !ok || !enabled {
			t.Fatalf("audit.enabled = %v, want true", audit["enabled"])
		}
		if got := audit["reclamation_window_seconds"]; got != float64(600) {
			t.Fatalf("audit.reclamation_window_seconds = %v, want 600", got)
		}
	})

	t.Run("success and partial failure project audit report locator", func(t *testing.T) {
		for _, response := range []EnvDispatchResponse{
			{ProjectID: "project-success", Rollouts: []EnvRolloutResponse{{ProjectID: "project-success"}}},
			{ProjectID: "project-partial", Message: "one rollout failed", Rollouts: []EnvRolloutResponse{{ProjectID: "project-partial", Error: "provisioning_failed"}}},
		} {
			body, err := json.Marshal(response)
			if err != nil {
				t.Fatalf("encode audited response: %v", err)
			}
			var wire map[string]any
			if err := json.Unmarshal(body, &wire); err != nil {
				t.Fatalf("decode audited response wire form: %v", err)
			}
			audit, ok := wire["audit"].(map[string]any)
			if !ok {
				t.Fatalf("audited response missing audit report locator: %s", body)
			}
			if got := audit["audit_id"]; got == "" || got == nil {
				t.Fatalf("audit.audit_id = %v, want generated correlation", got)
			}
			if got := audit["report_url"]; got == "" || got == nil {
				t.Fatalf("audit.report_url = %v, want audit report path", got)
			}
			if got := audit["reclamation_deadline"]; got == "" || got == nil {
				t.Fatalf("audit.reclamation_deadline = %v, want RFC3339 deadline", got)
			}
		}
	})
}

func TestEnvDispatchAuditReport_LoadsOnlyInitiatorScopedEvidence(t *testing.T) {
	loader := &fakeEnvDispatchAuditReportLoader{report: auditContractReport()}
	w := httptest.NewRecorder()
	h := newTestHandler(Config{})

	report, ok := h.loadVisibleEnvDispatchAuditReport(w, auditContractRequest(), loader, auditContractUUID)
	if !ok {
		t.Fatalf("loadVisibleEnvDispatchAuditReport returned false: status=%d body=%s", w.Code, w.Body.String())
	}
	if report.AuditID != auditContractUUID {
		t.Fatalf("audit_id = %q, want %q", report.AuditID, auditContractUUID)
	}
	if loader.gotAuditID != auditContractUUID || loader.gotWorkspaceID != auditContractUUID || loader.gotInitiatorID != auditContractUUID {
		t.Fatalf("loader scope = audit:%q workspace:%q initiator:%q, want current request scope", loader.gotAuditID, loader.gotWorkspaceID, loader.gotInitiatorID)
	}
}

func TestEnvDispatchAuditReport_VisibilityDeniedReturns403(t *testing.T) {
	loader := &fakeEnvDispatchAuditReportLoader{err: errors.New("audit visibility denied")}
	w := httptest.NewRecorder()
	h := newTestHandler(Config{})

	_, ok := h.loadVisibleEnvDispatchAuditReport(w, auditContractRequest(), loader, auditContractUUID)
	if ok {
		t.Fatal("visibility-denied audit report must not be returned")
	}
	if w.Code != http.StatusForbidden {
		t.Fatalf("status = %d, want 403; body=%s", w.Code, w.Body.String())
	}
}

func TestEnvDispatchAuditReport_MissingVisibleReportReturns404(t *testing.T) {
	loader := &fakeEnvDispatchAuditReportLoader{err: pgx.ErrNoRows}
	w := httptest.NewRecorder()
	h := newTestHandler(Config{})

	_, ok := h.loadVisibleEnvDispatchAuditReport(w, auditContractRequest(), loader, auditContractUUID)
	if ok {
		t.Fatal("missing audit report must not be returned")
	}
	if w.Code != http.StatusNotFound {
		t.Fatalf("status = %d, want 404; body=%s", w.Code, w.Body.String())
	}
}

func TestEnvDispatchAuditReport_ObservationUnavailableReturns503(t *testing.T) {
	loader := &fakeEnvDispatchAuditReportLoader{err: errors.New("audit observation unavailable")}
	w := httptest.NewRecorder()
	h := newTestHandler(Config{})

	_, ok := h.loadVisibleEnvDispatchAuditReport(w, auditContractRequest(), loader, auditContractUUID)
	if ok {
		t.Fatal("unavailable audit observation must not be returned as current evidence")
	}
	if w.Code != http.StatusServiceUnavailable {
		t.Fatalf("status = %d, want 503; body=%s", w.Code, w.Body.String())
	}
}

func TestEnvDispatchAuditReport_RedactsContentCredentialsAndLeaseTokens(t *testing.T) {
	secret := "Bearer audit-secret-must-not-escape"
	content := "customer issue body must-not-escape"
	report := auditContractReport()
	report.Events = []service.EnvDispatchAuditEvent{{
		ID:              "event-1",
		AuditResourceID: "resource-1",
		Sequence:        1,
		Type:            service.EnvDispatchAuditEventCleanupFailed,
		ReasonCode:      ptrAuditContractString("raw error: " + secret + " " + content),
		OccurredAt:      report.AsOf,
	}}
	report.Obligations = []service.EnvDispatchAuditObligation{{
		ID:              "obligation-1",
		AuditResourceID: "resource-1",
		Trigger:         service.EnvDispatchAuditObligationRollback,
		State:           service.EnvDispatchAuditObligationInProgress,
		LastErrorCode:   ptrAuditContractString("raw error: " + secret),
		LeaseAcquiredAt: &report.AsOf,
		CreatedAt:       report.AsOf,
		UpdatedAt:       report.AsOf,
	}}

	body, err := json.Marshal(mapEnvDispatchAuditReportResponse(report))
	if err != nil {
		t.Fatalf("encode public audit report: %v", err)
	}
	for _, forbidden := range []string{secret, content, "lease_acquired_at"} {
		if strings.Contains(string(body), forbidden) {
			t.Fatalf("public audit report leaked %q: %s", forbidden, body)
		}
	}
}

func ptrAuditContractString(value string) *string {
	return &value
}
