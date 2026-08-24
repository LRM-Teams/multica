package researchrun

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"
	"time"
)

type outboxV6StoreStub struct {
	intents           []V6OutboxIntent
	completed         []string
	rescheduled       []string
	failed            []string
	dispatchCompleted []string
	completeErrByID   map[string]error
	results           map[string]json.RawMessage
}

func (s *outboxV6StoreStub) ClaimV6Outbox(context.Context, string, time.Duration, int) ([]V6OutboxIntent, error) {
	return s.intents, nil
}

func (s *outboxV6StoreStub) CompleteV6Outbox(_ context.Context, id, _ string, result json.RawMessage) error {
	if err := s.completeErrByID[id]; err != nil {
		return err
	}
	s.completed = append(s.completed, id)
	if s.results == nil {
		s.results = map[string]json.RawMessage{}
	}
	s.results[id] = result
	return nil
}

func (s *outboxV6StoreStub) RescheduleV6Outbox(_ context.Context, id, _, message string, _ time.Time) error {
	s.rescheduled = append(s.rescheduled, id+":"+message)
	return nil
}

func (s *outboxV6StoreStub) FailV6Outbox(_ context.Context, id, _, message string) error {
	s.failed = append(s.failed, id+":"+message)
	return nil
}

func (s *outboxV6StoreStub) CompleteV6DispatchOutbox(_ context.Context, id, _, _ string) error {
	s.dispatchCompleted = append(s.dispatchCompleted, id)
	return nil
}

type runtimeTeamStoreStub struct {
	existing    map[string]V6TeamMember // agent id -> active membership
	addCalls    int
	archiveErr  error
	archiveDone []string
}

func (s *runtimeTeamStoreStub) AddV6TeamMember(_ context.Context, in AddV6TeamMemberInput) (V6TeamMember, error) {
	s.addCalls++
	return V6TeamMember{ID: "membership-new", AgentID: in.AgentID, State: V6TeamIdle}, nil
}

func (s *runtimeTeamStoreStub) ArchiveV6TeamMember(_ context.Context, in ArchiveV6TeamMemberInput) (V6TeamMember, error) {
	if s.archiveErr != nil {
		return V6TeamMember{}, s.archiveErr
	}
	s.archiveDone = append(s.archiveDone, in.MembershipID)
	return V6TeamMember{ID: in.MembershipID, State: V6TeamArchived}, nil
}

func (s *runtimeTeamStoreStub) FindActiveV6TeamMemberByAgent(_ context.Context, _, _, agentID string) (V6TeamMember, bool, error) {
	member, ok := s.existing[agentID]
	return member, ok, nil
}

type agentLifecycleStub struct {
	createCalls int
}

func (s *agentLifecycleStub) CreateAgent(context.Context, string, string, string, V6AgentSpec) (string, error) {
	s.createCalls++
	return "agent-1", nil
}

func (s *agentLifecycleStub) ArchiveAgent(context.Context, string, string, string) error { return nil }

type inboxDispatchStub struct {
	err error
}

func (s *inboxDispatchStub) DispatchV6Work(context.Context, V6AttemptAccess, V6WorkManifest, string) (string, error) {
	if s.err != nil {
		return "", s.err
	}
	return "inbox-task-1", nil
}

type outboxFixedClock struct{ now time.Time }

func (c outboxFixedClock) Now() time.Time { return c.now }

func createAgentIntent(t *testing.T, id string) V6OutboxIntent {
	t.Helper()
	payload, err := json.Marshal(map[string]any{
		"spec":       V6AgentSpec{Name: "Researcher", Capability: "search", MissionPrompt: "Investigate."},
		"membership": AddV6TeamMemberInput{WorkspaceID: "ws", RunID: "run", MissionPrompt: "Investigate."},
	})
	if err != nil {
		t.Fatal(err)
	}
	return V6OutboxIntent{ID: id, WorkspaceID: "ws", RunID: "run", Kind: "create_agent", IdempotencyKey: "key-" + id, Payload: payload}
}

func dispatchWorkIntent(t *testing.T, id string) V6OutboxIntent {
	t.Helper()
	payload, err := json.Marshal(V6DispatchIntentPayload{
		Access:   V6AttemptAccess{WorkspaceID: "ws", RunID: "run", WorkItemID: "work", AttemptID: "attempt", AgentID: "agent"},
		Manifest: json.RawMessage(`{}`),
	})
	if err != nil {
		t.Fatal(err)
	}
	return V6OutboxIntent{ID: id, WorkspaceID: "ws", RunID: "run", Kind: "dispatch_work_item", IdempotencyKey: "key-" + id, Payload: payload}
}

func newRuntimeModule(store *outboxV6StoreStub, team *runtimeTeamStoreStub, inbox InboxDispatchAdapter) v6RuntimeModule {
	return v6RuntimeModule{store: store, team: team, agents: &agentLifecycleStub{}, inbox: inbox, clock: outboxFixedClock{now: time.Unix(1755600000, 0)}}
}

func TestV6DeliverCreateAgentRedeliveryReusesActiveMembership(t *testing.T) {
	store := &outboxV6StoreStub{intents: []V6OutboxIntent{createAgentIntent(t, "outbox-1")}}
	team := &runtimeTeamStoreStub{existing: map[string]V6TeamMember{"agent-1": {ID: "membership-existing", AgentID: "agent-1", State: V6TeamIdle}}}
	module := newRuntimeModule(store, team, &inboxDispatchStub{})
	delivered, err := module.Deliver(context.Background(), 10)
	if err != nil || delivered != 1 {
		t.Fatalf("delivered=%d err=%v", delivered, err)
	}
	if team.addCalls != 0 {
		t.Fatalf("redelivery minted a duplicate membership: addCalls=%d", team.addCalls)
	}
	if !strings.Contains(string(store.results["outbox-1"]), "membership-existing") {
		t.Fatalf("result should reference the existing membership, got %s", store.results["outbox-1"])
	}
}

func TestV6DeliverCreateAgentAddsMembershipWhenNoneActive(t *testing.T) {
	store := &outboxV6StoreStub{intents: []V6OutboxIntent{createAgentIntent(t, "outbox-1")}}
	team := &runtimeTeamStoreStub{}
	module := newRuntimeModule(store, team, &inboxDispatchStub{})
	delivered, err := module.Deliver(context.Background(), 10)
	if err != nil || delivered != 1 || team.addCalls != 1 {
		t.Fatalf("delivered=%d addCalls=%d err=%v", delivered, team.addCalls, err)
	}
}

func TestV6DeliverArchiveAgentAlreadyArchivedConverges(t *testing.T) {
	payload, err := json.Marshal(map[string]string{"agent_id": "agent-1", "membership_id": "membership-1", "reason": "done"})
	if err != nil {
		t.Fatal(err)
	}
	store := &outboxV6StoreStub{intents: []V6OutboxIntent{{ID: "outbox-1", WorkspaceID: "ws", RunID: "run", Kind: "archive_agent", IdempotencyKey: "key", Payload: payload}}}
	team := &runtimeTeamStoreStub{archiveErr: ErrInvalidTransition}
	module := newRuntimeModule(store, team, &inboxDispatchStub{})
	delivered, err := module.Deliver(context.Background(), 10)
	if err != nil || delivered != 1 {
		t.Fatalf("delivered=%d err=%v", delivered, err)
	}
	if len(store.rescheduled) != 0 || len(store.failed) != 0 {
		t.Fatalf("already-archived member must converge, rescheduled=%v failed=%v", store.rescheduled, store.failed)
	}
}

func TestV6DeliverFailsFastOnNonRetryableError(t *testing.T) {
	store := &outboxV6StoreStub{intents: []V6OutboxIntent{dispatchWorkIntent(t, "outbox-1")}}
	module := newRuntimeModule(store, &runtimeTeamStoreStub{}, &inboxDispatchStub{err: NonRetryableDispatchError(errors.New("agent archived"))})
	if _, err := module.Deliver(context.Background(), 10); err != nil {
		t.Fatal(err)
	}
	if len(store.failed) != 1 {
		t.Fatalf("non-retryable error must fail the outbox row immediately, failed=%v rescheduled=%v", store.failed, store.rescheduled)
	}
	if len(store.rescheduled) != 0 {
		t.Fatalf("non-retryable error must not be rescheduled: %v", store.rescheduled)
	}
}

func TestV6DeliverInvalidPayloadFailsFast(t *testing.T) {
	store := &outboxV6StoreStub{intents: []V6OutboxIntent{{ID: "outbox-1", WorkspaceID: "ws", RunID: "run", Kind: "dispatch_work_item", IdempotencyKey: "key", Payload: json.RawMessage(`{broken`)}}}
	module := newRuntimeModule(store, &runtimeTeamStoreStub{}, &inboxDispatchStub{})
	if _, err := module.Deliver(context.Background(), 10); err != nil {
		t.Fatal(err)
	}
	if len(store.failed) != 1 || len(store.rescheduled) != 0 {
		t.Fatalf("broken payload can never deliver and must fail fast, failed=%v rescheduled=%v", store.failed, store.rescheduled)
	}
}

func TestV6DeliverReschedulesRetryableError(t *testing.T) {
	store := &outboxV6StoreStub{intents: []V6OutboxIntent{dispatchWorkIntent(t, "outbox-1")}}
	module := newRuntimeModule(store, &runtimeTeamStoreStub{}, &inboxDispatchStub{err: errors.New("runtime briefly offline")})
	if _, err := module.Deliver(context.Background(), 10); err != nil {
		t.Fatal(err)
	}
	if len(store.rescheduled) != 1 || len(store.failed) != 0 {
		t.Fatalf("retryable error must reschedule, rescheduled=%v failed=%v", store.rescheduled, store.failed)
	}
}

func TestV6DeliverContinuesBatchAfterCompleteFailure(t *testing.T) {
	store := &outboxV6StoreStub{
		intents:         []V6OutboxIntent{createAgentIntent(t, "outbox-1"), createAgentIntent(t, "outbox-2")},
		completeErrByID: map[string]error{"outbox-1": errors.New("lease lost")},
	}
	team := &runtimeTeamStoreStub{}
	module := newRuntimeModule(store, team, &inboxDispatchStub{})
	delivered, err := module.Deliver(context.Background(), 10)
	if err == nil {
		t.Fatal("expected the first intent's completion error to surface")
	}
	if delivered != 1 || len(store.completed) != 1 || store.completed[0] != "outbox-2" {
		t.Fatalf("second intent must still deliver: delivered=%d completed=%v", delivered, store.completed)
	}
}

func TestV6OutboxSQLGuardsLeaseAndEmitsFailureEvent(t *testing.T) {
	requireSourceFragments(t, "postgres_outbox_v6.go",
		"lease_expires_at <= now()", "v6_outbox_delivery_failed",
		"txOpV6OutboxReschedule", "txOpV6OutboxFail", "commitResearchTx")
	requireSourceFragments(t, "postgres_work_item_recovery.go",
		"o.lease_expires_at IS NULL OR o.lease_expires_at <= now()")
}
