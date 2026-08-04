package service

import (
	"context"
	"fmt"
	"sync"
	"testing"
	"time"
)

// recordingEnvDispatchAuditStorage is the service-test ledger. It deliberately
// records the values passed across the service boundary instead of deriving
// them from EnvDispatchResult: lazy bindings and rollback-only resources are
// not necessarily present in the response.
type recordingEnvDispatchAuditStorage struct {
	mu          sync.Mutex
	nextID      int
	runs        []EnvDispatchAuditReport
	resources   []EnvDispatchAuditResource
	events      []EnvDispatchAuditEvent
	obligations []EnvDispatchAuditObligation
}

func (s *recordingEnvDispatchAuditStorage) CreateAuditRun(_ context.Context, report EnvDispatchAuditReport) (EnvDispatchAuditReport, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.nextID++
	if report.AuditID == "" {
		report.AuditID = fmt.Sprintf("audit-%d", s.nextID)
	}
	s.runs = append(s.runs, report)
	return report, nil
}

func (s *recordingEnvDispatchAuditStorage) LoadAuditReport(_ context.Context, auditID, _, _ string, _ time.Time) (EnvDispatchAuditReport, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	for _, report := range s.runs {
		if report.AuditID == auditID {
			report.Resources = append([]EnvDispatchAuditResource(nil), s.resources...)
			report.Events = append([]EnvDispatchAuditEvent(nil), s.events...)
			report.Obligations = append([]EnvDispatchAuditObligation(nil), s.obligations...)
			return report, nil
		}
	}
	return EnvDispatchAuditReport{}, fmt.Errorf("audit run %q not found", auditID)
}

func (s *recordingEnvDispatchAuditStorage) UpsertAuditResource(_ context.Context, resource EnvDispatchAuditResource) (EnvDispatchAuditResource, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if resource.ID == "" {
		s.nextID++
		resource.ID = fmt.Sprintf("resource-%d", s.nextID)
	}
	for i, existing := range s.resources {
		if existing.AuditID == resource.AuditID && existing.Kind == resource.Kind && existing.ResourceID == resource.ResourceID {
			s.resources[i] = resource
			return resource, nil
		}
	}
	s.resources = append(s.resources, resource)
	return resource, nil
}

func (s *recordingEnvDispatchAuditStorage) UpdateAuditResourceClassification(_ context.Context, auditID, auditResourceID string, ownerState EnvDispatchAuditOwnerState, classification EnvDispatchAuditClassification, observedAt time.Time) (EnvDispatchAuditResource, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	for i := range s.resources {
		if s.resources[i].AuditID == auditID && s.resources[i].ID == auditResourceID {
			s.resources[i].OwnerState = ownerState
			s.resources[i].Classification = classification
			s.resources[i].LastObservedAt = &observedAt
			return s.resources[i], nil
		}
	}
	return EnvDispatchAuditResource{}, fmt.Errorf("audit resource %q not found", auditResourceID)
}

func (s *recordingEnvDispatchAuditStorage) AppendAuditEvent(_ context.Context, event EnvDispatchAuditEvent) (EnvDispatchAuditEvent, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.events = append(s.events, event)
	return event, nil
}

func (s *recordingEnvDispatchAuditStorage) EnsureReclamationObligation(_ context.Context, obligation EnvDispatchAuditObligation) (EnvDispatchAuditObligation, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.obligations = append(s.obligations, obligation)
	return obligation, nil
}

func (s *recordingEnvDispatchAuditStorage) UpdateAuditOutcome(_ context.Context, auditID string, outcome EnvDispatchAuditOutcome, completedAt *time.Time) (EnvDispatchAuditReport, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	for i := range s.runs {
		if s.runs[i].AuditID == auditID {
			s.runs[i].Outcome = outcome
			s.runs[i].CompletedAt = completedAt
			return s.runs[i], nil
		}
	}
	return EnvDispatchAuditReport{}, fmt.Errorf("audit run %q not found", auditID)
}

func (s *recordingEnvDispatchAuditStorage) UpdateAuditVerdict(_ context.Context, auditID string, verdict EnvDispatchAuditVerdict, completedAt *time.Time) (EnvDispatchAuditReport, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	for i := range s.runs {
		if s.runs[i].AuditID == auditID {
			s.runs[i].Verdict = verdict
			s.runs[i].CompletedAt = completedAt
			return s.runs[i], nil
		}
	}
	return EnvDispatchAuditReport{}, fmt.Errorf("audit run %q not found", auditID)
}

func (s *recordingEnvDispatchAuditStorage) ReconcileEligibleReclamationObligations(_ context.Context, _, _ time.Time, _ int32) ([]EnvDispatchAuditReclamationClaim, error) {
	return nil, nil
}

func (s *recordingEnvDispatchAuditStorage) MarkReclamationObligationSucceeded(_ context.Context, obligationID string, _ *time.Time) (EnvDispatchAuditObligation, error) {
	return s.updateObligation(obligationID, EnvDispatchAuditObligationSucceeded)
}

func (s *recordingEnvDispatchAuditStorage) MarkReclamationObligationNotRequired(_ context.Context, obligationID string, _ *time.Time) (EnvDispatchAuditObligation, error) {
	return s.updateObligation(obligationID, EnvDispatchAuditObligationNotRequired)
}

func (s *recordingEnvDispatchAuditStorage) RescheduleReclamationObligation(_ context.Context, obligationID string, _ time.Time, reasonCode *string, nextAttemptAt time.Time) (EnvDispatchAuditObligation, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	for i := range s.obligations {
		if s.obligations[i].ID == obligationID {
			s.obligations[i].State = EnvDispatchAuditObligationPending
			s.obligations[i].LastErrorCode = reasonCode
			s.obligations[i].NextAttemptAt = &nextAttemptAt
			return s.obligations[i], nil
		}
	}
	return EnvDispatchAuditObligation{}, fmt.Errorf("obligation %q not found", obligationID)
}

func (s *recordingEnvDispatchAuditStorage) ExhaustReclamationObligation(_ context.Context, obligationID string, _ *time.Time, reasonCode *string) (EnvDispatchAuditObligation, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	for i := range s.obligations {
		if s.obligations[i].ID == obligationID {
			s.obligations[i].State = EnvDispatchAuditObligationExhausted
			s.obligations[i].LastErrorCode = reasonCode
			return s.obligations[i], nil
		}
	}
	return EnvDispatchAuditObligation{}, fmt.Errorf("obligation %q not found", obligationID)
}

func (s *recordingEnvDispatchAuditStorage) updateObligation(obligationID string, state EnvDispatchAuditObligationState) (EnvDispatchAuditObligation, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	for i := range s.obligations {
		if s.obligations[i].ID == obligationID {
			s.obligations[i].State = state
			return s.obligations[i], nil
		}
	}
	return EnvDispatchAuditObligation{}, fmt.Errorf("obligation %q not found", obligationID)
}

var _ EnvDispatchAuditStorage = (*recordingEnvDispatchAuditStorage)(nil)

// unavailableSandboxDeleteDeps turns a rollback's post-delete observation into
// an unavailable observation without changing the shared service-test fake.
type unavailableSandboxDeleteDeps struct {
	*fakeEnvDispatchDeps
}

func (d unavailableSandboxDeleteDeps) DeleteSandbox(_ context.Context, sandboxID string) error {
	d.mu.Lock()
	d.deleteSandboxCalls = append(d.deleteSandboxCalls, sandboxID)
	d.mu.Unlock()
	return fmt.Errorf("sandbox observation unavailable")
}

func auditedMessageInput(baseEnv string, groupSize int) EnvDispatchInput {
	return EnvDispatchInput{
		WorkspaceID:  "workspace-audit",
		UserID:       "initiator-audit",
		Mode:         EnvModeScratch,
		EnvID:        baseEnv,
		Domain:       EnvDomainSelfPlay,
		DispatchType: EnvDispatchMessage,
		GroupSize:    groupSize,
		AgentID:      "leader",
		Message:      &MessageInput{Content: "audit-safe message"},
		Audit:        enabledEnvDispatchAuditRequest(),
	}
}

func enabledEnvDispatchAuditRequest() *EnvDispatchAuditRequest {
	return &EnvDispatchAuditRequest{Enabled: true, ReclamationWindow: 10 * time.Minute}
}

// T011 has not yet added Audit.Enabled to EnvDispatchInput. Until that wire
// contract exists, WithAuditStorage is the smallest existing T006 construction
// seam that lets these service tests demand T012's lifecycle behavior. T012
// must retain the assertions below while switching activation to Audit.Enabled;
// storage injection is capability wiring, never the production opt-in signal.
func newAuditedDispatchService(deps EnvDispatchDeps) (*EnvDispatchService, *recordingEnvDispatchAuditStorage) {
	storage := &recordingEnvDispatchAuditStorage{}
	return NewEnvDispatchService(deps, 1).WithAuditStorage(storage), storage
}

func TestDispatchAudit_StorageInjectionAloneDoesNotOptIn(t *testing.T) {
	deps := newFakeEnvDispatchDeps()
	svc, storage := newAuditedDispatchService(deps)
	input := auditedMessageInput(deps.seedBaseEnv(), 1)
	input.Audit = nil

	_, err := svc.Dispatch(context.Background(), input)
	if err != nil {
		t.Fatalf("Dispatch() error = %v", err)
	}
	if len(storage.runs) != 0 || len(storage.resources) != 0 || len(storage.events) != 0 || len(storage.obligations) != 0 {
		t.Fatalf("storage injection without Audit.Enabled must not persist audit evidence: runs=%+v resources=%+v events=%+v obligations=%+v", storage.runs, storage.resources, storage.events, storage.obligations)
	}
}

func TestDispatchAudit_CorrelatesAllRolloutsAndLazyBindings(t *testing.T) {
	deps := newFakeEnvDispatchDeps()
	deps.messageRoster = MessageRoster{LeaderID: "leader", AgentIDs: []string{"leader", "lazy-peer"}}
	svc, storage := newAuditedDispatchService(deps)

	result, err := svc.Dispatch(context.Background(), auditedMessageInput(deps.seedBaseEnv(), 2))
	if err != nil {
		t.Fatalf("Dispatch() error = %v", err)
	}
	if len(result.Rollouts) != 2 {
		t.Fatalf("Dispatch() rollouts = %d, want 2", len(result.Rollouts))
	}
	if len(storage.runs) != 1 {
		t.Fatalf("audited dispatch must create one correlation run, got %d", len(storage.runs))
	}

	run := storage.runs[0]
	if run.AuditID == "" {
		t.Fatal("audit run must have a server-generated correlation ID")
	}
	if result.Audit == nil || result.Audit.AuditID != run.AuditID {
		t.Fatalf("Dispatch() audit = %+v, want server-created report %q", result.Audit, run.AuditID)
	}
	if run.WorkspaceID != "workspace-audit" || run.InitiatorID != "initiator-audit" {
		t.Fatalf("audit scope = workspace %q initiator %q, want request scope", run.WorkspaceID, run.InitiatorID)
	}
	if run.DispatchType != EnvDispatchAuditDispatchMessage {
		t.Fatalf("audit dispatch type = %q, want message", run.DispatchType)
	}

	bindingIDsByScope := make(map[string]map[string]struct{}, len(result.Rollouts))
	for _, resource := range storage.resources {
		if resource.AuditID != run.AuditID {
			t.Fatalf("resource %q escaped audit correlation %q", resource.ResourceID, run.AuditID)
		}
		if resource.Kind == EnvDispatchAuditResourceBinding {
			if resource.ChannelID == nil || resource.ProjectID == nil {
				t.Fatalf("binding %q must retain channel and project scope: %+v", resource.ResourceID, resource)
			}
			if resource.ResourceID == "" {
				t.Fatalf("binding evidence for scope %q:%q is missing its logical identity", *resource.ChannelID, *resource.ProjectID)
			}
			scope := *resource.ChannelID + ":" + *resource.ProjectID
			if bindingIDsByScope[scope] == nil {
				bindingIDsByScope[scope] = map[string]struct{}{}
			}
			bindingIDsByScope[scope][resource.ResourceID] = struct{}{}
		}
	}
	for _, rollout := range result.Rollouts {
		if rollout.AgentSandboxes["leader"].Status != "ready" {
			t.Fatalf("leader must be ready in rollout scope %q:%q, got %+v", rollout.ChannelID, rollout.ProjectID, rollout.AgentSandboxes["leader"])
		}
		if rollout.AgentSandboxes["lazy-peer"].Status != "pending" {
			t.Fatalf("lazy peer must remain pending before its first mention, got %+v", rollout.AgentSandboxes["lazy-peer"])
		}
		scope := rollout.ChannelID + ":" + rollout.ProjectID
		if len(bindingIDsByScope[scope]) != 2 {
			t.Fatalf("rollout scope %q must retain distinct leader and lazy-peer binding identities, got %d bindings: %+v", scope, len(bindingIDsByScope[scope]), storage.resources)
		}
	}
	if len(bindingIDsByScope) != len(result.Rollouts) {
		t.Fatalf("binding scopes = %d, want every rollout scope (%d): %+v", len(bindingIDsByScope), len(result.Rollouts), storage.resources)
	}
}

func TestDispatchAudit_PartialResetFailureRecordsRollbackEvidence(t *testing.T) {
	deps := newFakeEnvDispatchDeps()
	deps.createEnvFailAfter = 1 // one rollout is created, then the peer fails and all state rolls back
	svc, storage := newAuditedDispatchService(deps)

	_, err := svc.Dispatch(context.Background(), EnvDispatchInput{
		WorkspaceID: "workspace-audit", UserID: "initiator-audit", Mode: EnvModeScratch,
		EnvID: deps.seedBaseEnv(), Domain: EnvDomainSweLego, DispatchType: EnvDispatchIssue,
		GroupSize: 2, AgentID: "agent", Issue: &IssueInput{Title: "audit-safe title"},
		Audit: enabledEnvDispatchAuditRequest(),
	})
	if err == nil {
		t.Fatal("Dispatch() error = nil, want reset failure")
	}
	if len(storage.runs) != 1 {
		t.Fatalf("partial reset must retain one audit correlation, got %d", len(storage.runs))
	}
	if !hasAuditEvent(storage.events, EnvDispatchAuditEventCreationFailed) {
		t.Fatalf("partial reset must record creation_failed, got events %+v", storage.events)
	}
	if !hasAuditEvent(storage.events, EnvDispatchAuditEventRollbackStarted) {
		t.Fatalf("partial reset must record rollback_started, got events %+v", storage.events)
	}
	if len(storage.resources) == 0 {
		t.Fatal("partial reset must preserve evidence for resources created before rollback")
	}
}

func TestDispatchAudit_FirstCreationFailureRecordsRunLevelEventsWithoutSyntheticResource(t *testing.T) {
	deps := newFakeEnvDispatchDeps()
	deps.createEnvErr = fmt.Errorf("create env failed before a rollout exists")
	svc, storage := newAuditedDispatchService(deps)

	_, err := svc.Dispatch(context.Background(), EnvDispatchInput{
		WorkspaceID: "workspace-audit", UserID: "initiator-audit", Mode: EnvModeScratch,
		EnvID: deps.seedBaseEnv(), Domain: EnvDomainSweLego, DispatchType: EnvDispatchIssue,
		GroupSize: 1, AgentID: "agent", Issue: &IssueInput{Title: "audit-safe title"},
		Audit: enabledEnvDispatchAuditRequest(),
	})
	if err == nil {
		t.Fatal("Dispatch() error = nil, want reset failure")
	}

	for _, eventType := range []EnvDispatchAuditEventType{
		EnvDispatchAuditEventCreationFailed,
		EnvDispatchAuditEventRollbackStarted,
		EnvDispatchAuditEventDispatchOutcome,
	} {
		if !hasRunLevelAuditEvent(storage.events, eventType) {
			t.Fatalf("first creation failure must persist run-level %q, got events %+v", eventType, storage.events)
		}
	}
	for _, resource := range storage.resources {
		if resource.Kind == EnvDispatchAuditResourceKind("derived_agent") {
			t.Fatalf("audit resources must not contain synthetic derived_agent evidence: %+v", storage.resources)
		}
	}
}

func TestDispatchAudit_DistinguishesAbsentResourceFromUnavailableObservation(t *testing.T) {
	for _, tc := range []struct {
		name               string
		deps               EnvDispatchDeps
		fixture            *fakeEnvDispatchDeps
		wantClassification EnvDispatchAuditClassification
		wantUnavailable    bool
	}{
		{
			name:               "deleted resource is reclaimed",
			fixture:            newFakeEnvDispatchDeps(),
			wantClassification: EnvDispatchAuditClassificationReclaimed,
		},
		{
			name:               "delete observation is unavailable",
			fixture:            newFakeEnvDispatchDeps(),
			wantClassification: EnvDispatchAuditClassificationInconclusive,
			wantUnavailable:    true,
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			deps := tc.deps
			if deps == nil {
				deps = tc.fixture
				if tc.wantUnavailable {
					deps = unavailableSandboxDeleteDeps{fakeEnvDispatchDeps: tc.fixture}
				}
			}
			tc.fixture.createEnvFailAfter = 1 // force cleanup after one created rollout
			svc, storage := newAuditedDispatchService(deps)

			_, err := svc.Dispatch(context.Background(), EnvDispatchInput{
				WorkspaceID: "workspace-audit", UserID: "initiator-audit", Mode: EnvModeScratch,
				EnvID: tc.fixture.seedBaseEnv(), Domain: EnvDomainSweLego, DispatchType: EnvDispatchIssue,
				GroupSize: 2, AgentID: "agent", Issue: &IssueInput{Title: "audit-safe title"},
				Audit: enabledEnvDispatchAuditRequest(),
			})
			if err == nil {
				t.Fatal("Dispatch() error = nil, want reset failure that triggers rollback")
			}

			if len(tc.fixture.deleteSandboxCalls) == 0 {
				t.Fatalf("rollback did not target any sandbox: resources=%+v events=%+v", storage.resources, storage.events)
			}
			for _, sandboxID := range tc.fixture.deleteSandboxCalls {
				resource, ok := findAuditResource(storage.resources, EnvDispatchAuditResourceSandbox, sandboxID)
				if !ok {
					t.Fatalf("rollback sandbox %q is missing audit resource evidence: %+v", sandboxID, storage.resources)
				}
				if resource.Classification != tc.wantClassification {
					t.Fatalf("rollback sandbox %q classification = %q, want %q", sandboxID, resource.Classification, tc.wantClassification)
				}
				if resource.Classification == EnvDispatchAuditClassificationReclaimed && tc.wantUnavailable {
					t.Fatalf("unavailable rollback sandbox %q must never be reclaimed", sandboxID)
				}
				if got := hasAuditEventForResource(storage.events, EnvDispatchAuditEventObservationUnavailable, resource.ID); got != tc.wantUnavailable {
					t.Fatalf("rollback sandbox %q observation_unavailable = %t, want %t; events=%+v", sandboxID, got, tc.wantUnavailable, storage.events)
				}
			}
		})
	}
}

func hasAuditEvent(events []EnvDispatchAuditEvent, want EnvDispatchAuditEventType) bool {
	for _, event := range events {
		if event.Type == want {
			return true
		}
	}
	return false
}

func hasRunLevelAuditEvent(events []EnvDispatchAuditEvent, want EnvDispatchAuditEventType) bool {
	for _, event := range events {
		if event.Type == want && event.AuditResourceID == nil {
			return true
		}
	}
	return false
}

func findAuditResource(resources []EnvDispatchAuditResource, kind EnvDispatchAuditResourceKind, resourceID string) (EnvDispatchAuditResource, bool) {
	for _, resource := range resources {
		if resource.Kind == kind && resource.ResourceID == resourceID {
			return resource, true
		}
	}
	return EnvDispatchAuditResource{}, false
}

func hasAuditEventForResource(events []EnvDispatchAuditEvent, want EnvDispatchAuditEventType, auditResourceID string) bool {
	for _, event := range events {
		if event.Type == want && event.AuditResourceID != nil && *event.AuditResourceID == auditResourceID {
			return true
		}
	}
	return false
}
