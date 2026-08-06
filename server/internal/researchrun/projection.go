package researchrun

import (
	"context"
	"errors"
	"time"
)

const projectionBatchLimit = 500

// projectionEventStore is the persistence surface owned by Projection Module.
// It deliberately excludes Research Run mutation and scheduling operations.
type projectionEventStore interface {
	ListUnprojectedEvents(context.Context, string, int) ([]RunEvent, error)
	AssertRunLease(context.Context, string) error
	MarkEventProjected(context.Context, string) error
	MarkEventProjectionFailed(context.Context, string, string, time.Time) error
}

// projectionModule drains committed Research Run events into the configured
// projection output. Canonical state has already committed before this module
// runs; output failures only update durable projection retry state.
type projectionModule struct {
	store  projectionEventStore
	output Projector
	clock  Clock
}

func (module projectionModule) ProjectPending(ctx context.Context, sessionID string) error {
	if module.output == nil {
		return nil
	}
	for range projectionBatchLimit {
		events, err := module.store.ListUnprojectedEvents(ctx, sessionID, 1)
		if err != nil {
			return err
		}
		if len(events) == 0 {
			return nil
		}
		event := events[0]
		if err = module.store.AssertRunLease(ctx, sessionID); err != nil {
			return err
		}
		if err = module.output.Project(ctx, event); err != nil {
			delay := projectionRetryDelay(event.ProjectionAttempts)
			if markErr := module.store.MarkEventProjectionFailed(ctx, event.ID, err.Error(), module.clock.Now().Add(delay)); markErr != nil {
				return errors.Join(err, markErr)
			}
			return err
		}
		if err = module.store.MarkEventProjected(ctx, event.ID); err != nil {
			return err
		}
	}
	return errors.New("research event projection batch limit reached")
}

func projectionRetryDelay(attempts int) time.Duration {
	if attempts < 0 {
		attempts = 0
	}
	return time.Duration(1<<min(attempts, 8)) * time.Second
}

// projectPending is the Engine adapter into Projection Module. Keeping this
// method preserves the existing Engine call sites while projection invariants
// live outside orchestration.
func (e *Engine) projectPending(ctx context.Context, sessionID string) error {
	return (projectionModule{store: e.store, output: e.projector, clock: e.clock}).ProjectPending(ctx, sessionID)
}
