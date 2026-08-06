package researchrun

import (
	"context"
	"errors"
	"fmt"
	"testing"
	"time"
)

func TestProjectionModuleProjectsCommittedEventsInOrder(t *testing.T) {
	store := &projectionTestStore{events: []RunEvent{{ID: "event-1"}, {ID: "event-2"}}}
	output := &projectionTestOutput{}
	module := projectionModule{store: store, output: output, clock: fixedProjectionClock{now: time.Unix(100, 0).UTC()}}

	if err := module.ProjectPending(context.Background(), "session-1"); err != nil {
		t.Fatal(err)
	}
	if got := fmt.Sprint(output.projected); got != "[event-1 event-2]" {
		t.Fatalf("projected=%s", got)
	}
	if got := fmt.Sprint(store.markedProjected); got != "[event-1 event-2]" {
		t.Fatalf("marked projected=%s", got)
	}
	if len(store.events) != 0 {
		t.Fatalf("events remaining=%d", len(store.events))
	}
}

func TestProjectionModulePersistsRetryAfterOutputFailure(t *testing.T) {
	now := time.Unix(200, 0).UTC()
	store := &projectionTestStore{events: []RunEvent{{ID: "event-1", ProjectionAttempts: 2}}}
	outputErr := errors.New("projection unavailable")
	module := projectionModule{
		store: store, output: &projectionTestOutput{err: outputErr}, clock: fixedProjectionClock{now: now},
	}

	err := module.ProjectPending(context.Background(), "session-1")
	if !errors.Is(err, outputErr) {
		t.Fatalf("error=%v", err)
	}
	if len(store.failures) != 1 {
		t.Fatalf("failures=%+v", store.failures)
	}
	failure := store.failures[0]
	if failure.eventID != "event-1" || failure.message != outputErr.Error() || !failure.nextAttempt.Equal(now.Add(4*time.Second)) {
		t.Fatalf("failure=%+v", failure)
	}
	if len(store.markedProjected) != 0 || len(store.events) != 1 {
		t.Fatalf("failed event was acknowledged: projected=%v remaining=%d", store.markedProjected, len(store.events))
	}
}

func TestProjectionModuleStopsAtBoundedBatch(t *testing.T) {
	events := make([]RunEvent, projectionBatchLimit+1)
	for index := range events {
		events[index].ID = fmt.Sprintf("event-%03d", index)
	}
	store := &projectionTestStore{events: events}
	module := projectionModule{store: store, output: &projectionTestOutput{}, clock: fixedProjectionClock{}}

	err := module.ProjectPending(context.Background(), "session-1")
	if err == nil || err.Error() != "research event projection batch limit reached" {
		t.Fatalf("error=%v", err)
	}
	if len(store.markedProjected) != projectionBatchLimit || len(store.events) != 1 {
		t.Fatalf("projected=%d remaining=%d", len(store.markedProjected), len(store.events))
	}
}

func TestProjectionModuleDoesNotCallOutputAfterLeaseLoss(t *testing.T) {
	store := &projectionTestStore{
		events:    []RunEvent{{ID: "event-1", SessionID: "session-1"}},
		assertErr: ErrRunLeaseLost,
	}
	output := &projectionTestOutput{}
	err := (projectionModule{store: store, output: output, clock: fixedProjectionClock{}}).ProjectPending(context.Background(), "session-1")
	if !errors.Is(err, ErrRunLeaseLost) {
		t.Fatalf("error=%v", err)
	}
	if len(output.projected) != 0 || len(store.markedProjected) != 0 || len(store.failures) != 0 {
		t.Fatalf("projected=%v marked=%v failures=%v", output.projected, store.markedProjected, store.failures)
	}
}

func TestProjectionModuleWithoutOutputDoesNotReadOutbox(t *testing.T) {
	store := &projectionTestStore{events: []RunEvent{{ID: "event-1"}}}
	module := projectionModule{store: store, clock: fixedProjectionClock{}}

	if err := module.ProjectPending(context.Background(), "session-1"); err != nil {
		t.Fatal(err)
	}
	if store.listCalls != 0 {
		t.Fatalf("list calls=%d", store.listCalls)
	}
}

type projectionTestFailure struct {
	eventID     string
	message     string
	nextAttempt time.Time
}

type projectionTestStore struct {
	events          []RunEvent
	listCalls       int
	markedProjected []string
	failures        []projectionTestFailure
	assertErr       error
}

func (store *projectionTestStore) ListUnprojectedEvents(_ context.Context, _ string, limit int) ([]RunEvent, error) {
	store.listCalls++
	if limit <= 0 || len(store.events) == 0 {
		return nil, nil
	}
	if limit > len(store.events) {
		limit = len(store.events)
	}
	return append([]RunEvent(nil), store.events[:limit]...), nil
}

func (store *projectionTestStore) AssertRunLease(_ context.Context, _ string) error {
	return store.assertErr
}

func (store *projectionTestStore) MarkEventProjected(_ context.Context, eventID string) error {
	if len(store.events) == 0 || store.events[0].ID != eventID {
		return fmt.Errorf("unexpected projected event %q", eventID)
	}
	store.markedProjected = append(store.markedProjected, eventID)
	store.events = store.events[1:]
	return nil
}

func (store *projectionTestStore) MarkEventProjectionFailed(_ context.Context, eventID, message string, nextAttempt time.Time) error {
	store.failures = append(store.failures, projectionTestFailure{eventID: eventID, message: message, nextAttempt: nextAttempt})
	return nil
}

type projectionTestOutput struct {
	projected []string
	err       error
}

func (output *projectionTestOutput) Project(_ context.Context, event RunEvent) error {
	output.projected = append(output.projected, event.ID)
	return output.err
}

type fixedProjectionClock struct{ now time.Time }

func (clock fixedProjectionClock) Now() time.Time { return clock.now }
