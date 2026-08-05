package service

import (
	"context"
	"math"
	"sync"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgtype"
	"github.com/multica-ai/multica/server/internal/util"
	db "github.com/multica-ai/multica/server/pkg/db/generated"
)

func TestDeliveryPillar(t *testing.T) {
	t.Parallel()
	score := deliveryPillar(10, 0)
	if score < 80 {
		t.Fatalf("expected strong delivery score, got %v", score)
	}
	if deliveryPillar(0, 0) != 0 {
		t.Fatalf("expected zero for no tasks")
	}
}

func TestEvolutionPillar(t *testing.T) {
	t.Parallel()
	score := evolutionPillar(8, 10, 2)
	if score < 50 {
		t.Fatalf("expected meaningful evolution score, got %v", score)
	}
}

func TestClassID(t *testing.T) {
	t.Parallel()
	cases := []struct {
		score float64
		want  string
	}{
		{90, "dreadnought"},
		{75, "battleship"},
		{60, "cruiser"},
		{45, "frigate"},
		{30, "corvette"},
		{10, "reserve"},
	}
	for _, tc := range cases {
		got := fleetClassFromScore(tc.score, true)
		if got != tc.want {
			t.Fatalf("score %v => %q, want %q", tc.score, got, tc.want)
		}
	}
	if fleetClassFromScore(90, false) != "reserve" {
		t.Fatal("insufficient sample should force reserve")
	}
}

func TestFleetScoreWeightsSum(t *testing.T) {
	t.Parallel()
	sum := fleetWeightDelivery + fleetWeightEvolution + fleetWeightGrowth + fleetWeightEfficiency
	if math.Abs(sum-1.0) > 0.0001 {
		t.Fatalf("pillar weights must sum to 1, got %v", sum)
	}
}

func TestSnapshotToViewIncludesConfiguredMinimumSample(t *testing.T) {
	t.Parallel()

	view := snapshotToView(db.AgentFleetSnapshot{SampleTasks: 4}, 12)

	if view.MinSampleTasks != 12 {
		t.Fatalf("minimum sample tasks = %d, want 12", view.MinSampleTasks)
	}
	if view.SampleSufficient {
		t.Fatal("four samples must remain insufficient when the configured minimum is twelve")
	}
}

func TestRefreshWorkspaceAfterArchiveAsyncReturnsImmediatelyAndCoalesces(t *testing.T) {
	workspaceID := pgtype.UUID{Bytes: [16]byte{1}, Valid: true}
	started := make(chan struct{})
	release := make(chan struct{})

	var callsMu sync.Mutex
	calls := 0
	service := NewAgentFleetRankService(&db.Queries{})
	service.archiveRefresh = func(_ context.Context, gotWorkspaceID pgtype.UUID) error {
		if gotWorkspaceID != workspaceID {
			t.Errorf("workspace id = %v, want %v", gotWorkspaceID, workspaceID)
		}
		callsMu.Lock()
		calls++
		call := calls
		callsMu.Unlock()
		if call == 1 {
			close(started)
			<-release
		}
		return nil
	}

	returned := make(chan struct{})
	go func() {
		service.RefreshWorkspaceAfterArchiveAsync(workspaceID)
		close(returned)
	}()

	select {
	case <-returned:
	case <-time.After(time.Second):
		t.Fatal("archive refresh scheduling blocked on workspace refresh")
	}
	select {
	case <-started:
	case <-time.After(time.Second):
		t.Fatal("archive refresh did not start")
	}

	service.RefreshWorkspaceAfterArchiveAsync(workspaceID)
	close(release)

	deadline := time.After(time.Second)
	for {
		callsMu.Lock()
		gotCalls := calls
		callsMu.Unlock()
		service.archiveRefreshMu.Lock()
		queued := service.archiveRefreshQueued[util.UUIDToString(workspaceID)]
		service.archiveRefreshMu.Unlock()
		if gotCalls == 2 && !queued {
			return
		}
		select {
		case <-deadline:
			t.Fatalf("refresh calls=%d queued=%t, want two completed coalesced passes", gotCalls, queued)
		case <-time.After(time.Millisecond):
		}
	}
}
