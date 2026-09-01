package scheduler

import (
	"context"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/multica-ai/multica/server/internal/service"
)

type stubInteractionDAGPublisher struct {
	claims  int
	health  service.InteractionDAGPublishHealth
	healthE error
	calls   int
}

func (s *stubInteractionDAGPublisher) PublishClaim(_ context.Context, _ int) (int, error) {
	s.calls++
	return s.claims, nil
}

func (s *stubInteractionDAGPublisher) PublishHealth(_ context.Context) (service.InteractionDAGPublishHealth, error) {
	return s.health, s.healthE
}

func TestInteractionDAGPublishJob_RegistersDurableSpec(t *testing.T) {
	spec := InteractionDAGPublishJob(nil, nil)
	assert.Equal(t, JobNameInteractionDAGPublish, spec.Name)
	assert.Equal(t, time.Minute, spec.Cadence)
	scopes, err := spec.Scopes(context.Background(), time.Now().UTC())
	require.NoError(t, err)
	assert.Equal(t, []Scope{ScopeGlobal}, scopes)
	assert.True(t, spec.AllowStaleReentry, "publish ticks must survive a stale manager")
	assert.GreaterOrEqual(t, spec.MaxAttempts, 2)
	assert.Positive(t, spec.RunTimeout)
	assert.Positive(t, spec.StaleTimeout)
	assert.Positive(t, spec.HeartbeatInterval)
	require.NotNil(t, spec.Handler)
}

func TestInteractionDAGPublishJob_HandlerIsNilPublisherSafe(t *testing.T) {
	spec := InteractionDAGPublishJob(nil, nil)
	result, err := spec.Handler(context.Background(), HandlerInput{})
	require.NoError(t, err)
	assert.Zero(t, result.RowsAffected)
}

func TestInteractionDAGPublishJob_HandlerDrainsAndReportsHealth(t *testing.T) {
	publisher := &stubInteractionDAGPublisher{
		claims: 3,
		health: service.InteractionDAGPublishHealth{
			Pending: 5, Retry: 2, DeadLetter: 1, RedactionFailed: 1, Backlog: 7, Published: 42,
		},
	}
	spec := InteractionDAGPublishJob(nil, publisher)

	heartbeats := 0
	result, err := spec.Handler(context.Background(), HandlerInput{
		Heartbeat: func(context.Context) error { heartbeats++; return nil },
	})
	require.NoError(t, err)
	assert.Equal(t, int64(3), result.RowsAffected)
	assert.Equal(t, 1, heartbeats)
	assert.Equal(t, 1, publisher.calls, "a partial batch means the outbox is idle for this tick")

	snapshot, ok := result.Result["publish_health"].(service.InteractionDAGPublishHealth)
	require.True(t, ok, "health counters ride along on every tick")
	assert.Equal(t, int64(7), snapshot.Backlog)
	assert.Equal(t, int64(1), snapshot.DeadLetter)
	assert.Equal(t, int64(42), snapshot.Published)
}

func TestInteractionDAGPublishJob_HandlerPropagatesFailure(t *testing.T) {
	spec := InteractionDAGPublishJob(nil, &failingInteractionDAGPublisher{})
	_, err := spec.Handler(context.Background(), HandlerInput{})
	require.Error(t, err)
}

type failingInteractionDAGPublisher struct {
	stubInteractionDAGPublisher
}

func (f *failingInteractionDAGPublisher) PublishClaim(context.Context, int) (int, error) {
	return 0, context.DeadlineExceeded
}

type stubGraphMemoryProjector struct {
	claims int
	err    error
	calls  int
}

func (s *stubGraphMemoryProjector) ProjectClaim(_ context.Context, _ int) (int, error) {
	s.calls++
	if s.err != nil {
		return s.calls, s.err
	}
	return s.claims, nil
}

func TestGraphMemoryProjectionJob_RegistersDurableSpec(t *testing.T) {
	spec := GraphMemoryProjectionJob(nil, nil)
	assert.Equal(t, JobNameGraphMemoryProjection, spec.Name)
	assert.Equal(t, time.Minute, spec.Cadence)
	scopes, err := spec.Scopes(context.Background(), time.Now().UTC())
	require.NoError(t, err)
	assert.Equal(t, []Scope{ScopeGlobal}, scopes)
	assert.True(t, spec.AllowStaleReentry)
	assert.GreaterOrEqual(t, spec.MaxAttempts, 2)
	assert.Positive(t, spec.RunTimeout)
	require.NotNil(t, spec.Handler)
}

func TestGraphMemoryProjectionJob_HandlerIsNilProjectorSafe(t *testing.T) {
	spec := GraphMemoryProjectionJob(nil, nil)
	result, err := spec.Handler(context.Background(), HandlerInput{})
	require.NoError(t, err)
	assert.Zero(t, result.RowsAffected)
}

func TestGraphMemoryProjectionJob_HandlerDrains(t *testing.T) {
	projector := &stubGraphMemoryProjector{claims: 3}
	spec := GraphMemoryProjectionJob(nil, projector)
	result, err := spec.Handler(context.Background(), HandlerInput{})
	require.NoError(t, err)
	assert.Equal(t, int64(3), result.RowsAffected, "one drain round below the batch size")
	assert.Equal(t, 1, projector.calls)
}

func TestGraphMemoryProjectionJob_HandlerPropagatesFailure(t *testing.T) {
	projector := &stubGraphMemoryProjector{err: assert.AnError}
	spec := GraphMemoryProjectionJob(nil, projector)
	_, err := spec.Handler(context.Background(), HandlerInput{})
	require.Error(t, err)
}
