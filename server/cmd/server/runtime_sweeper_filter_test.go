package main

import (
	"context"
	"reflect"
	"sort"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgtype"
	"github.com/multica-ai/multica/server/internal/handler"
	"github.com/multica-ai/multica/server/internal/util"
	db "github.com/multica-ai/multica/server/pkg/db/generated"
)

// fakeLiveness drives every Available / IsAliveBatch branch in
// filterStaleRuntimesByLiveness without standing up Redis. The sweeper-side
// behavior is the most subtle part of the design, so it gets a dedicated
// suite here even though the handler package has analogous coverage.
type fakeLiveness struct {
	available   bool
	aliveResult map[string]bool
	aliveOK     bool
}

func (f *fakeLiveness) Available() bool { return f.available }
func (f *fakeLiveness) Touch(_ context.Context, _ string, _ time.Duration) error {
	return nil
}
func (f *fakeLiveness) IsAliveBatch(_ context.Context, ids []string) (map[string]bool, bool) {
	if !f.aliveOK {
		return nil, false
	}
	out := make(map[string]bool, len(ids))
	for _, id := range ids {
		out[id] = f.aliveResult[id]
	}
	return out, true
}
func (f *fakeLiveness) Forget(_ context.Context, _ string) {}

func makeUUIDForFilter(t *testing.T, s string) pgtype.UUID {
	t.Helper()
	u, err := util.ParseUUID(s)
	if err != nil {
		t.Fatalf("ParseUUID(%q): %v", s, err)
	}
	return u
}

func candidateRow(t *testing.T, id string) db.SelectStaleOnlineRuntimesRow {
	return db.SelectStaleOnlineRuntimesRow{ID: makeUUIDForFilter(t, id)}
}

func sortedIDStrings(ids []pgtype.UUID) []string {
	out := make([]string, len(ids))
	for i, id := range ids {
		out[i] = util.UUIDToString(id)
	}
	sort.Strings(out)
	return out
}

func sortedRowIDs(rows []db.SelectStaleOnlineRuntimesRow) []string {
	ids := make([]pgtype.UUID, 0, len(rows))
	for _, r := range rows {
		ids = append(ids, r.ID)
	}
	return sortedIDStrings(ids)
}

func candidateRowWithRunner(t *testing.T, id, daemonID, workspaceID string) db.SelectStaleOnlineRuntimesRow {
	return db.SelectStaleOnlineRuntimesRow{
		ID:          makeUUIDForFilter(t, id),
		DaemonID:    pgtype.Text{String: daemonID, Valid: true},
		WorkspaceID: makeUUIDForFilter(t, workspaceID),
	}
}

// TestFilterStaleRuntimesByPresence_SkipsConnectedRunner confirms that a stale
// DB candidate whose daemon currently has a live WorkspaceRunner socket is NOT
// marked offline (LRM-1571: WS-connected daemons stop heartbeating; socket
// presence is the online signal).
func TestFilterStaleRuntimesByPresence_SkipsConnectedRunner(t *testing.T) {
	connectedID := "11111111-1111-1111-1111-111111111111"
	deadID := "22222222-2222-2222-2222-222222222222"
	connectedWorkspace := "33333333-3333-3333-3333-333333333333"
	connectedDaemon := "agent-main"
	deadWorkspace := "44444444-4444-4444-4444-444444444444"
	deadDaemon := "agent-worker"
	candidates := []db.SelectStaleOnlineRuntimesRow{
		candidateRowWithRunner(t, connectedID, connectedDaemon, connectedWorkspace),
		candidateRowWithRunner(t, deadID, deadDaemon, deadWorkspace),
	}
	presence := &fakePresence{hasRunner: map[string]bool{
		runnerKey(connectedDaemon, connectedWorkspace): true,
	}}
	got := filterStaleRuntimesByPresence(candidates, presence)
	want := []string{deadID}
	if !reflect.DeepEqual(sortedRowIDs(got), want) {
		t.Fatalf("connected runner should be kept (stale but alive via WS); disconnected stale should stay offline: got=%v want=%v",
			sortedRowIDs(got), want)
	}
}

// TestFilterStaleRuntimesByPresence_NilPresencePassesThrough confirms that a
// nil presence source (unit-test wiring, no Hub) keeps every candidate,
// matching the pre-LRM-1571 behavior.
func TestFilterStaleRuntimesByPresence_NilPresencePassesThrough(t *testing.T) {
	a := "11111111-1111-1111-1111-111111111111"
	b := "22222222-2222-2222-2222-222222222222"
	candidates := []db.SelectStaleOnlineRuntimesRow{
		candidateRow(t, a),
		candidateRow(t, b),
	}
	got := filterStaleRuntimesByPresence(candidates, nil)
	want := []string{a, b}
	if !reflect.DeepEqual(sortedRowIDs(got), want) {
		t.Fatalf("nil presence should pass every candidate through: got=%v want=%v",
			sortedRowIDs(got), want)
	}
}

type fakePresence struct {
	hasRunner map[string]bool
}

func (f *fakePresence) HasWorkspaceRunner(daemonID, workspaceID string) bool {
	return f.hasRunner[runnerKey(daemonID, workspaceID)]
}

func runnerKey(daemonID, workspaceID string) string {
	return daemonID + "/" + workspaceID
}

// TestFilterStaleRuntimesByLiveness_NoopStorePassesThrough confirms that with
// no Redis the filter returns every candidate — the sweeper trusts the DB
// stale window and behaves like the legacy MarkStaleRuntimesOffline path.
func TestFilterStaleRuntimesByLiveness_NoopStorePassesThrough(t *testing.T) {
	a := "11111111-1111-1111-1111-111111111111"
	b := "22222222-2222-2222-2222-222222222222"
	candidates := []db.SelectStaleOnlineRuntimesRow{
		candidateRow(t, a),
		candidateRow(t, b),
	}

	got := filterStaleRuntimesByLiveness(context.Background(), candidates, handler.NewNoopLivenessStore())

	want := []string{a, b}
	if !reflect.DeepEqual(sortedIDStrings(got), want) {
		t.Fatalf("noop store should pass every candidate through: got=%v want=%v",
			sortedIDStrings(got), want)
	}
}

// TestFilterStaleRuntimesByLiveness_AliveCandidatesSkipped confirms the core
// optimization: candidates whose Redis liveness is fresh are NOT marked
// offline, even though their DB last_seen_at is stale.
func TestFilterStaleRuntimesByLiveness_AliveCandidatesSkipped(t *testing.T) {
	aliveID := "11111111-1111-1111-1111-111111111111"
	deadID := "22222222-2222-2222-2222-222222222222"
	candidates := []db.SelectStaleOnlineRuntimesRow{
		candidateRow(t, aliveID),
		candidateRow(t, deadID),
	}

	got := filterStaleRuntimesByLiveness(context.Background(), candidates, &fakeLiveness{
		available: true,
		aliveOK:   true,
		aliveResult: map[string]bool{
			aliveID: true,
			// deadID intentionally absent — defaults to false.
		},
	})

	want := []string{deadID}
	if !reflect.DeepEqual(sortedIDStrings(got), want) {
		t.Fatalf("alive candidates should be skipped: got=%v want=%v",
			sortedIDStrings(got), want)
	}
}

// TestFilterStaleRuntimesByLiveness_StoreErrorFallsBackToDB confirms the
// graceful-degradation contract: when IsAliveBatch returns ok=false, the
// sweeper trusts the DB stale window — same as if Redis were not configured.
func TestFilterStaleRuntimesByLiveness_StoreErrorFallsBackToDB(t *testing.T) {
	a := "11111111-1111-1111-1111-111111111111"
	b := "22222222-2222-2222-2222-222222222222"
	candidates := []db.SelectStaleOnlineRuntimesRow{
		candidateRow(t, a),
		candidateRow(t, b),
	}

	got := filterStaleRuntimesByLiveness(context.Background(), candidates, &fakeLiveness{
		available: true,
		aliveOK:   false, // simulates Redis MGET error
	})

	want := []string{a, b}
	if !reflect.DeepEqual(sortedIDStrings(got), want) {
		t.Fatalf("store error should fall back to passing every candidate: got=%v want=%v",
			sortedIDStrings(got), want)
	}
}
