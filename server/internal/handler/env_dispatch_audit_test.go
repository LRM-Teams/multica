package handler

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/multica-ai/multica/server/internal/middleware"
	"github.com/multica-ai/multica/server/internal/service"
	db "github.com/multica-ai/multica/server/pkg/db/generated"
)

const (
	auditContractAuditID     = "11111111-1111-1111-1111-111111111111"
	auditContractWorkspaceID = "22222222-2222-2222-2222-222222222222"
	auditContractInitiatorID = "33333333-3333-3333-3333-333333333333"
	auditContractClientID    = "44444444-4444-4444-4444-444444444444"
)

type fakeEnvDispatchAuditReportLoader struct {
	report service.EnvDispatchAuditReport
	err    error

	t               *testing.T
	wantAuditID     string
	wantWorkspaceID string
	wantInitiatorID string
}

func (f *fakeEnvDispatchAuditReportLoader) LoadAuditReport(_ context.Context, auditID, workspaceID, initiatorID string, _ time.Time) (service.EnvDispatchAuditReport, error) {
	f.t.Helper()
	if auditID != f.wantAuditID {
		f.t.Errorf("loader audit_id = %q, want %q", auditID, f.wantAuditID)
	}
	if workspaceID != f.wantWorkspaceID {
		f.t.Errorf("loader workspace_id = %q, want %q", workspaceID, f.wantWorkspaceID)
	}
	if initiatorID != f.wantInitiatorID {
		f.t.Errorf("loader initiator_id = %q, want %q", initiatorID, f.wantInitiatorID)
	}
	return f.report, f.err
}

func newAuditReportLoader(t *testing.T, report service.EnvDispatchAuditReport, err error) *fakeEnvDispatchAuditReportLoader {
	t.Helper()
	return &fakeEnvDispatchAuditReportLoader{
		report:          report,
		err:             err,
		t:               t,
		wantAuditID:     auditContractAuditID,
		wantWorkspaceID: auditContractWorkspaceID,
		wantInitiatorID: auditContractInitiatorID,
	}
}

func auditContractRequest() *http.Request {
	r := httptest.NewRequest(http.MethodGet, "/api/v1/env-dispatch/audits/"+auditContractAuditID, nil)
	r.Header.Set("X-User-ID", auditContractInitiatorID)
	return r.WithContext(middleware.SetMemberContext(r.Context(), auditContractWorkspaceID, db.Member{}))
}

func auditContractReport() service.EnvDispatchAuditReport {
	now := time.Date(2026, time.August, 3, 12, 0, 0, 0, time.UTC)
	return service.EnvDispatchAuditReport{
		AuditID:             auditContractAuditID,
		WorkspaceID:         auditContractWorkspaceID,
		InitiatorID:         auditContractInitiatorID,
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

type auditedCreateContract struct {
	request        EnvDispatchRequest
	auditRecord    service.EnvDispatchAuditReport
	clientAuditID  string
	createResponse EnvDispatchResponse
}

func newAuditedCreateContract(t *testing.T, partialFailure bool) auditedCreateContract {
	t.Helper()
	const requestBody = `{
		"mode":"scratch",
		"env_id":"44444444-4444-4444-4444-444444444444",
		"domain":"swe_lego",
		"dispatch_type":"issue",
		"group_size":1,
		"agent_id":"44444444-4444-4444-4444-444444444444",
		"training_mode":false,
		"issue":{"title":"audit contract only"},
		"audit":{
			"enabled":true,
			"reclamation_window_seconds":600,
			"audit_id":"44444444-4444-4444-4444-444444444444"
		}
	}`
	var request EnvDispatchRequest
	if err := json.Unmarshal([]byte(requestBody), &request); err != nil {
		t.Fatalf("decode audited request: %v", err)
	}

	response := EnvDispatchResponse{
		ProjectID: "project-success",
		Rollouts:  []EnvRolloutResponse{{ProjectID: "project-success"}},
	}
	if partialFailure {
		response = EnvDispatchResponse{
			ProjectID: "project-partial",
			Message:   "one rollout failed",
			Rollouts:  []EnvRolloutResponse{{ProjectID: "project-partial", Error: "provisioning_failed"}},
		}
	}

	return auditedCreateContract{
		request:        request,
		auditRecord:    auditContractReport(),
		clientAuditID:  auditContractClientID,
		createResponse: response,
	}
}

// auditHTTPStatusError intentionally exposes only a stable structural method.
// T013 can classify it with errors.As against an interface containing
// AuditHTTPStatus() int without importing this test-only type.
type auditHTTPStatusError int

func (e auditHTTPStatusError) Error() string { return "audit report status contract" }

func (e auditHTTPStatusError) AuditHTTPStatus() int { return int(e) }

// T011 must keep audit opt-in: audited requests preserve their correlation
// request, while every audited result (including a partial failure) carries a
// report locator. envDispatchAuditResponseFromReport is deliberately a future
// pure handler mapper: it must derive the public locator from the
// server-generated record, not client input or a test-built JSON map.
func TestEnvDispatchAudit_CreateWireContract(t *testing.T) {
	t.Run("request preserves opt-in audit configuration", func(t *testing.T) {
		contract := newAuditedCreateContract(t, false)

		body, err := json.Marshal(contract.request)
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
		if got := audit["audit_id"]; got != nil {
			t.Fatalf("client must not choose audit_id, got %v", got)
		}
	})

	t.Run("success and partial failure project audit report locator", func(t *testing.T) {
		for _, partialFailure := range []bool{false, true} {
			contract := newAuditedCreateContract(t, partialFailure)
			response := contract.createResponse
			response.Audit = envDispatchAuditResponseFromReport(contract.auditRecord)
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
			if got := audit["audit_id"]; got != contract.auditRecord.AuditID {
				t.Fatalf("audit.audit_id = %v, want server-generated %q", got, contract.auditRecord.AuditID)
			}
			if got := audit["audit_id"]; got == contract.clientAuditID {
				t.Fatalf("audit.audit_id = %v, must not reuse client-supplied id", got)
			}
			wantReportURL := "/api/v1/env-dispatch/audits/" + contract.auditRecord.AuditID
			if got := audit["report_url"]; got != wantReportURL {
				t.Fatalf("audit.report_url = %v, want %q", got, wantReportURL)
			}
			wantDeadline := contract.auditRecord.ReclamationDeadline.Format(time.RFC3339)
			if got := audit["reclamation_deadline"]; got != wantDeadline {
				t.Fatalf("audit.reclamation_deadline = %v, want %q", got, wantDeadline)
			}
		}
	})
}

func TestMapEnvDispatchAuditRequest_MapsOnlyEnabledRequests(t *testing.T) {
	t.Run("enabled", func(t *testing.T) {
		got := mapEnvDispatchAuditRequest(&EnvDispatchAuditRequest{
			Enabled:                  true,
			ReclamationWindowSeconds: 600,
		})
		if got == nil || !got.Enabled || got.ReclamationWindow != 10*time.Minute {
			t.Fatalf("mapEnvDispatchAuditRequest() = %+v, want enabled 10-minute request", got)
		}
	})

	for _, input := range []*EnvDispatchAuditRequest{nil, {Enabled: false, ReclamationWindowSeconds: 600}} {
		if got := mapEnvDispatchAuditRequest(input); got != nil {
			t.Fatalf("mapEnvDispatchAuditRequest(%+v) = %+v, want nil", input, got)
		}
	}
}

func TestMapAuditEvent_OmitsUnsanitizedReasonCode(t *testing.T) {
	event := mapAuditEvent(db.EnvDispatchAuditEvent{
		ID:         parseUUID(auditContractAuditID),
		AuditID:    parseUUID(auditContractAuditID),
		EventType:  "creation_failed",
		ReasonCode: pgtype.Text{String: "postgres connection refused", Valid: true},
		OccurredAt: pgtype.Timestamptz{Time: time.Now().UTC(), Valid: true},
	})
	if event.ReasonCode != nil {
		t.Fatalf("unsafe reason code escaped storage mapping: %q", *event.ReasonCode)
	}
}

func TestEnvDispatchAuditReport_LoadsOnlyInitiatorScopedEvidence(t *testing.T) {
	loader := newAuditReportLoader(t, auditContractReport(), nil)
	w := httptest.NewRecorder()
	h := newTestHandler(Config{})

	report, ok := h.loadVisibleEnvDispatchAuditReport(w, auditContractRequest(), loader, auditContractAuditID)
	if !ok {
		t.Fatalf("loadVisibleEnvDispatchAuditReport returned false: status=%d body=%s", w.Code, w.Body.String())
	}
	if report.AuditID != auditContractAuditID {
		t.Fatalf("audit_id = %q, want %q", report.AuditID, auditContractAuditID)
	}
}

func TestEnvDispatchAuditReport_VisibilityDeniedReturns403(t *testing.T) {
	loader := newAuditReportLoader(t, service.EnvDispatchAuditReport{}, auditHTTPStatusError(http.StatusForbidden))
	w := httptest.NewRecorder()
	h := newTestHandler(Config{})

	_, ok := h.loadVisibleEnvDispatchAuditReport(w, auditContractRequest(), loader, auditContractAuditID)
	if ok {
		t.Fatal("visibility-denied audit report must not be returned")
	}
	if w.Code != http.StatusForbidden {
		t.Fatalf("status = %d, want 403; body=%s", w.Code, w.Body.String())
	}
}

func TestEnvDispatchAuditReport_MissingVisibleReportReturns404(t *testing.T) {
	loader := newAuditReportLoader(t, service.EnvDispatchAuditReport{}, pgx.ErrNoRows)
	w := httptest.NewRecorder()
	h := newTestHandler(Config{})

	_, ok := h.loadVisibleEnvDispatchAuditReport(w, auditContractRequest(), loader, auditContractAuditID)
	if ok {
		t.Fatal("missing audit report must not be returned")
	}
	if w.Code != http.StatusNotFound {
		t.Fatalf("status = %d, want 404; body=%s", w.Code, w.Body.String())
	}
}

func TestEnvDispatchAuditReport_ObservationUnavailableReturns503(t *testing.T) {
	loader := newAuditReportLoader(t, service.EnvDispatchAuditReport{}, auditHTTPStatusError(http.StatusServiceUnavailable))
	w := httptest.NewRecorder()
	h := newTestHandler(Config{})

	_, ok := h.loadVisibleEnvDispatchAuditReport(w, auditContractRequest(), loader, auditContractAuditID)
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
		AuditResourceID: ptrAuditContractString("resource-1"),
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

func TestEnvDispatchAuditReport_OmitsResourceIDForRunLevelEvent(t *testing.T) {
	report := auditContractReport()
	report.Events = []service.EnvDispatchAuditEvent{{
		ID:         "event-1",
		Sequence:   1,
		Type:       service.EnvDispatchAuditEventCreationFailed,
		OccurredAt: report.AsOf,
	}}

	body, err := json.Marshal(mapEnvDispatchAuditReportResponse(report))
	if err != nil {
		t.Fatalf("encode public audit report: %v", err)
	}
	var response struct {
		Events []map[string]any `json:"events"`
	}
	if err := json.Unmarshal(body, &response); err != nil {
		t.Fatalf("decode public audit report: %v", err)
	}
	if len(response.Events) != 1 {
		t.Fatalf("event count = %d, want 1", len(response.Events))
	}
	if _, ok := response.Events[0]["audit_resource_id"]; ok {
		t.Fatalf("run-level event must omit audit_resource_id: %s", body)
	}
}

func ptrAuditContractString(value string) *string {
	return &value
}
