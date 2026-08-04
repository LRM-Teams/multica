package handler

import (
	"context"
	"errors"
	"net/http"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/multica-ai/multica/server/internal/service"
)

// EnvDispatchAuditReportResponse is the public, correlation-scoped audit
// evidence view. It intentionally excludes initiator and workspace identities:
// the enclosing request scope is the authority for both. Its nested values are
// restricted to identifiers, states, timestamps, and sanitized reason codes.
type EnvDispatchAuditReportResponse struct {
	AuditID             string                               `json:"audit_id"`
	DispatchType        service.EnvDispatchAuditDispatchType `json:"dispatch_type"`
	PrimaryScopeID      string                               `json:"primary_scope_id"`
	Outcome             service.EnvDispatchAuditOutcome      `json:"outcome"`
	Verdict             service.EnvDispatchAuditVerdict      `json:"verdict"`
	ReclamationDeadline time.Time                            `json:"reclamation_deadline"`
	StartedAt           time.Time                            `json:"started_at"`
	CompletedAt         *time.Time                           `json:"completed_at,omitempty"`
	CreatedAt           time.Time                            `json:"created_at"`
	UpdatedAt           time.Time                            `json:"updated_at"`
	AsOf                time.Time                            `json:"as_of"`
	Resources           []EnvDispatchAuditResourceResponse   `json:"resources"`
	Events              []EnvDispatchAuditEventResponse      `json:"events"`
	Obligations         []EnvDispatchAuditObligationResponse `json:"obligations"`
}

// EnvDispatchAuditResourceResponse exposes only identifier and lifecycle
// evidence. ResourceID is an opaque resource identifier, never task or message
// content.
type EnvDispatchAuditResourceResponse struct {
	ID              string                                 `json:"id"`
	Kind            service.EnvDispatchAuditResourceKind   `json:"kind"`
	ResourceID      string                                 `json:"resource_id"`
	DaemonID        *string                                `json:"daemon_id,omitempty"`
	EnvironmentID   *string                                `json:"environment_id,omitempty"`
	ProjectID       *string                                `json:"project_id,omitempty"`
	ChannelID       *string                                `json:"channel_id,omitempty"`
	OwnershipMode   service.EnvDispatchAuditOwnershipMode  `json:"ownership_mode"`
	OwnerState      service.EnvDispatchAuditOwnerState     `json:"owner_state"`
	Classification  service.EnvDispatchAuditClassification `json:"classification"`
	FirstObservedAt time.Time                              `json:"first_observed_at"`
	LastObservedAt  *time.Time                             `json:"last_observed_at,omitempty"`
	ReclaimedAt     *time.Time                             `json:"reclaimed_at,omitempty"`
	CreatedAt       time.Time                              `json:"created_at"`
	UpdatedAt       time.Time                              `json:"updated_at"`
}

// EnvDispatchAuditEventResponse holds an append-only audit observation. The
// mapper deliberately emits only a sanitized reason code, never raw transport
// errors or any event payload.
type EnvDispatchAuditEventResponse struct {
	ID              string                            `json:"id"`
	AuditResourceID *string                           `json:"audit_resource_id,omitempty"`
	Sequence        int64                             `json:"sequence"`
	Type            service.EnvDispatchAuditEventType `json:"type"`
	ReasonCode      *string                           `json:"reason_code,omitempty"`
	OccurredAt      time.Time                         `json:"occurred_at"`
}

// EnvDispatchAuditObligationResponse is the safe public state of one
// reclamation obligation. It omits the internal lease token and maps an error
// only when it is a sanitized code.
type EnvDispatchAuditObligationResponse struct {
	ID              string                                    `json:"id"`
	AuditResourceID string                                    `json:"audit_resource_id"`
	Trigger         service.EnvDispatchAuditObligationTrigger `json:"trigger"`
	State           service.EnvDispatchAuditObligationState   `json:"state"`
	AttemptCount    int32                                     `json:"attempt_count"`
	LastErrorCode   *string                                   `json:"last_error_code,omitempty"`
	NextAttemptAt   *time.Time                                `json:"next_attempt_at,omitempty"`
	CreatedAt       time.Time                                 `json:"created_at"`
	UpdatedAt       time.Time                                 `json:"updated_at"`
}

// envDispatchAuditReportLoader is the read-only seam used by the eventual
// route and handler contract tests. It intentionally exposes only the T004
// report-load method, rather than the full mutation-capable storage interface.
type envDispatchAuditReportLoader interface {
	LoadAuditReport(ctx context.Context, auditID, workspaceID, initiatorID string, asOf time.Time) (service.EnvDispatchAuditReport, error)
}

// loadVisibleEnvDispatchAuditReport verifies the current human's workspace
// scope before loading a report. The loader receives the authenticated user as
// initiator, so a report cannot be observed across workspaces or by another
// workspace member. Route wiring belongs to T013.
func (h *Handler) loadVisibleEnvDispatchAuditReport(w http.ResponseWriter, r *http.Request, loader envDispatchAuditReportLoader, auditID string) (EnvDispatchAuditReportResponse, bool) {
	userID, ok := requireUserID(w, r)
	if !ok {
		return EnvDispatchAuditReportResponse{}, false
	}
	workspaceID := ctxWorkspaceID(r.Context())
	if workspaceID == "" {
		writeError(w, http.StatusBadRequest, "workspace ID required")
		return EnvDispatchAuditReportResponse{}, false
	}
	if _, ok := parseUUIDOrBadRequest(w, auditID, "audit_id"); !ok {
		return EnvDispatchAuditReportResponse{}, false
	}
	if _, ok := h.workspaceMember(w, r, workspaceID); !ok {
		return EnvDispatchAuditReportResponse{}, false
	}

	report, err := loader.LoadAuditReport(r.Context(), auditID, workspaceID, userID, time.Now().UTC())
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			writeError(w, http.StatusNotFound, "env dispatch audit not found")
		} else if status, ok := envDispatchAuditHTTPStatus(err); ok {
			// Visibility / observation contracts surface stable HTTP codes
			// (403/503) instead of collapsing to a generic 500.
			writeError(w, status, "failed to load env dispatch audit")
		} else {
			writeError(w, http.StatusInternalServerError, "failed to load env dispatch audit")
		}
		return EnvDispatchAuditReportResponse{}, false
	}
	return mapEnvDispatchAuditReportResponse(report), true
}

func mapEnvDispatchAuditReportResponse(report service.EnvDispatchAuditReport) EnvDispatchAuditReportResponse {
	resources := make([]EnvDispatchAuditResourceResponse, 0, len(report.Resources))
	for _, resource := range report.Resources {
		resources = append(resources, EnvDispatchAuditResourceResponse{
			ID:              resource.ID,
			Kind:            resource.Kind,
			ResourceID:      resource.ResourceID,
			DaemonID:        resource.DaemonID,
			EnvironmentID:   resource.EnvironmentID,
			ProjectID:       resource.ProjectID,
			ChannelID:       resource.ChannelID,
			OwnershipMode:   resource.OwnershipMode,
			OwnerState:      resource.OwnerState,
			Classification:  resource.Classification,
			FirstObservedAt: resource.FirstObservedAt,
			LastObservedAt:  resource.LastObservedAt,
			ReclaimedAt:     resource.ReclaimedAt,
			CreatedAt:       resource.CreatedAt,
			UpdatedAt:       resource.UpdatedAt,
		})
	}

	events := make([]EnvDispatchAuditEventResponse, 0, len(report.Events))
	for _, event := range report.Events {
		events = append(events, EnvDispatchAuditEventResponse{
			ID:              event.ID,
			AuditResourceID: event.AuditResourceID,
			Sequence:        event.Sequence,
			Type:            event.Type,
			ReasonCode:      sanitizedEnvDispatchAuditCode(event.ReasonCode),
			OccurredAt:      event.OccurredAt,
		})
	}

	obligations := make([]EnvDispatchAuditObligationResponse, 0, len(report.Obligations))
	for _, obligation := range report.Obligations {
		obligations = append(obligations, EnvDispatchAuditObligationResponse{
			ID:              obligation.ID,
			AuditResourceID: obligation.AuditResourceID,
			Trigger:         obligation.Trigger,
			State:           obligation.State,
			AttemptCount:    obligation.AttemptCount,
			LastErrorCode:   sanitizedEnvDispatchAuditCode(obligation.LastErrorCode),
			NextAttemptAt:   obligation.NextAttemptAt,
			CreatedAt:       obligation.CreatedAt,
			UpdatedAt:       obligation.UpdatedAt,
		})
	}

	return EnvDispatchAuditReportResponse{
		AuditID:             report.AuditID,
		DispatchType:        report.DispatchType,
		PrimaryScopeID:      report.PrimaryScopeID,
		Outcome:             report.Outcome,
		Verdict:             report.Verdict,
		ReclamationDeadline: report.ReclamationDeadline,
		StartedAt:           report.StartedAt,
		CompletedAt:         report.CompletedAt,
		CreatedAt:           report.CreatedAt,
		UpdatedAt:           report.UpdatedAt,
		AsOf:                report.AsOf,
		Resources:           resources,
		Events:              events,
		Obligations:         obligations,
	}
}

// sanitizedEnvDispatchAuditCode is a defensive response-boundary check for
// storage adapters. Unknown values are omitted rather than treating a
// database-safe string as safe public error detail.
func sanitizedEnvDispatchAuditCode(code *string) *string {
	if code == nil {
		return nil
	}

	// This is the closed public vocabulary for audit reason and error codes.
	// Storage constraints only reject malformed strings; they do not establish
	// that a well-formed value is safe to disclose. Additions must correspond to
	// a documented audit transition or reclamation outcome.
	switch *code {
	case "already_reclaimed",
		"binding_observed",
		"classification_updated",
		"cleanup_attempted",
		"cleanup_exhausted",
		"cleanup_failed",
		"cleanup_requested",
		"cleanup_retry_scheduled",
		"creation_failed",
		"dispatch_cancelled",
		"dispatch_deleted",
		"dispatch_failed",
		"dispatch_outcome",
		"dispatch_rejected",
		"dispatch_timed_out",
		"observation_unavailable",
		"owner_active",
		"owner_deleted",
		"owner_terminal",
		"owner_unknown",
		"ownership_deferred",
		"provisioned",
		"reclaimed",
		"reclamation_deadline_exceeded",
		"reclamation_lease_expired",
		"reclamation_retry_exhausted",
		"resource_not_found",
		"rollback_started",
		"runtime_offlined",
		"sandbox_deletion_failed",
		"sandbox_deletion_requested",
		"sandbox_deletion_unavailable":
		return code
	default:
		return nil
	}
}

// envDispatchAuditHTTPStatus extracts a stable HTTP status from loader errors
// that implement AuditHTTPStatus() int (handler tests + service contracts).
func envDispatchAuditHTTPStatus(err error) (int, bool) {
	type statusCarrier interface {
		AuditHTTPStatus() int
	}
	var carrier statusCarrier
	if errors.As(err, &carrier) {
		status := carrier.AuditHTTPStatus()
		if status >= 400 && status < 600 {
			return status, true
		}
	}
	return 0, false
}
