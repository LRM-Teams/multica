// SPDX-License-Identifier: Apache-2.0

package service

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"sync"
	"testing"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	db "github.com/multica-ai/multica/server/pkg/db/generated"
)

// fakeInteractionDAGStore is an in-memory InteractionDAGStore for unit tests.
type fakeInteractionDAGStore struct {
	mu               sync.Mutex
	sessionRuns      map[string]db.InteractionDAGSessionRun
	segmentSnapshots []db.InsertInteractionDAGSegmentWithSnapshotParams
	edges            []db.InsertInteractionDAGEdgeParams

	upsertErr                error
	getSessionRunErr         error
	insertSegmentSnapshotErr error
	insertEdgeErr            error
}

func newFakeInteractionDAGStore() *fakeInteractionDAGStore {
	return &fakeInteractionDAGStore{sessionRuns: map[string]db.InteractionDAGSessionRun{}}
}

func (f *fakeInteractionDAGStore) UpsertInteractionDAGSessionRun(_ context.Context, arg db.UpsertInteractionDAGSessionRunParams) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.upsertErr != nil {
		return f.upsertErr
	}
	f.sessionRuns[arg.SessionID] = db.InteractionDAGSessionRun{
		SessionID:  arg.SessionID,
		ProjectID:  arg.ProjectID,
		AgentRunID: arg.AgentRunID,
		IssueID:    arg.IssueID,
	}
	return nil
}

func (f *fakeInteractionDAGStore) GetInteractionDAGSessionRun(_ context.Context, sessionID string) (db.InteractionDAGSessionRun, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.getSessionRunErr != nil {
		return db.InteractionDAGSessionRun{}, f.getSessionRunErr
	}
	row, ok := f.sessionRuns[sessionID]
	if !ok {
		return db.InteractionDAGSessionRun{}, pgx.ErrNoRows
	}
	return row, nil
}

func (f *fakeInteractionDAGStore) InsertInteractionDAGSegmentWithSnapshot(_ context.Context, arg db.InsertInteractionDAGSegmentWithSnapshotParams) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.insertSegmentSnapshotErr != nil {
		return f.insertSegmentSnapshotErr
	}
	f.segmentSnapshots = append(f.segmentSnapshots, arg)
	return nil
}

func (f *fakeInteractionDAGStore) InsertInteractionDAGEdge(_ context.Context, arg db.InsertInteractionDAGEdgeParams) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.insertEdgeErr != nil {
		return f.insertEdgeErr
	}
	f.edges = append(f.edges, arg)
	return nil
}

var _ InteractionDAGStore = (*fakeInteractionDAGStore)(nil)

// fakeArealSegmentClient is an in-memory ArealSegmentClient for unit tests.
type fakeArealSegmentClient struct {
	mu                sync.Mutex
	closeSegmentID    int
	closeSegmentErr   error
	exportPayload     json.RawMessage
	exportErr         error
	closeCalls        []string
	exportCalls       []exportCall
	exportCallForTraj map[int]json.RawMessage // trajectoryID -> payload (per-call override)
}

type exportCall struct {
	sessionID    string
	trajectoryID int
}

func (c *fakeArealSegmentClient) CloseSegment(_ context.Context, proxyKey string) (int, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.closeCalls = append(c.closeCalls, proxyKey)
	if c.closeSegmentErr != nil {
		return 0, c.closeSegmentErr
	}
	return c.closeSegmentID, nil
}

func (c *fakeArealSegmentClient) ExportTrajectory(_ context.Context, sessionID string, trajectoryID int) (json.RawMessage, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.exportCalls = append(c.exportCalls, exportCall{sessionID: sessionID, trajectoryID: trajectoryID})
	if c.exportErr != nil {
		return nil, c.exportErr
	}
	if c.exportCallForTraj != nil {
		if p, ok := c.exportCallForTraj[trajectoryID]; ok {
			return p, nil
		}
	}
	return c.exportPayload, nil
}

var _ ArealSegmentClient = (*fakeArealSegmentClient)(nil)

func ptrText(s string) pgtype.Text { return pgtype.Text{String: s, Valid: s != ""} }

// TestRecordSessionAgentRun_UpsertsMapping verifies RecordSessionAgentRun
// stores the {session_id -> agent_run_id, issue_id} mapping and that a second
// call upserts (re-binds) rather than erroring on the PK.
func TestInteractionDAG_RecordSessionAgentRun_UpsertsMapping(t *testing.T) {
	store := newFakeInteractionDAGStore()
	svc := NewInteractionDAGService(store, &fakeArealSegmentClient{}, true)

	if err := svc.RecordSessionAgentRun(context.Background(), "proj-1", "sess-1", "run-1", "issue-1"); err != nil {
		t.Fatalf("first record: %v", err)
	}
	row, ok := store.sessionRuns["sess-1"]
	if !ok {
		t.Fatal("session_run row not stored")
	}
	assert.Equal(t, "run-1", row.AgentRunID)
	assert.Equal(t, ptrText("issue-1"), row.IssueID)
	assert.Equal(t, "proj-1", row.ProjectID)

	// Upsert: re-bind the same session to a new run (retry attempt, D8).
	if err := svc.RecordSessionAgentRun(context.Background(), "proj-1", "sess-1", "run-2", "issue-1"); err != nil {
		t.Fatalf("upsert record: %v", err)
	}
	row = store.sessionRuns["sess-1"]
	assert.Equal(t, "run-2", row.AgentRunID, "upsert must re-bind agent_run_id")
}

// TestRecordSessionAgentRun_RejectsMissingIDs verifies required-id validation.
func TestInteractionDAG_RecordSessionAgentRun_RejectsMissingIDs(t *testing.T) {
	svc := NewInteractionDAGService(newFakeInteractionDAGStore(), &fakeArealSegmentClient{}, true)
	for _, tc := range []struct{ project, session, run string }{
		{"", "s", "r"}, {"p", "", "r"}, {"p", "s", ""},
	} {
		if err := svc.RecordSessionAgentRun(context.Background(), tc.project, tc.session, tc.run, "i"); err == nil {
			t.Fatalf("expected error for %+v", tc)
		}
	}
}

// TestCloseSegmentForEvent_RecordsSegment verifies the full close+export+
// record path: looks up agent_run_id via session, closes the segment, exports
// the trajectory, decodes tensor_ref, and stores a segment + env snapshot.
func TestInteractionDAG_CloseSegmentForEvent_RecordsSegment(t *testing.T) {
	store := newFakeInteractionDAGStore()
	client := &fakeArealSegmentClient{
		closeSegmentID: 7,
		exportPayload:  json.RawMessage(`{"tensor_ref":{"shard_id":"shard-1"}}`),
	}
	svc := NewInteractionDAGService(store, client, true)

	require.NoError(t, svc.RecordSessionAgentRun(context.Background(), "proj-1", "sess-1", "run-1", "issue-1"))

	segID, err := svc.CloseSegmentForEvent(context.Background(), "proj-1", "sess-1", "proxy-key", "delegation",
		map[string]any{"sandbox_ids": []string{"sbx-1"}, "issue_snapshot_id": "snap-1", "env_state": map[string]any{"k": "v"}})
	require.NoError(t, err)

	// segment_id is stable: <sessionID>-<trajectoryID>.
	assert.Equal(t, "sess-1-7", segID)

	// CloseSegment called once with the per-call proxy key.
	assert.Equal(t, []string{"proxy-key"}, client.closeCalls)
	// ExportTrajectory called once with the session + the returned trajectory id.
	require.Len(t, client.exportCalls, 1)
	assert.Equal(t, "sess-1", client.exportCalls[0].sessionID)
	assert.Equal(t, 7, client.exportCalls[0].trajectoryID)

	// The combined insert records both segment fields and env-snapshot fields in
	// a single atomic call (CTE), carrying the looked-up agent_run_id + issue_id.
	require.Len(t, store.segmentSnapshots, 1)
	row := store.segmentSnapshots[0]
	assert.Equal(t, "sess-1-7", row.SegmentID)
	assert.Equal(t, "proj-1", row.ProjectID)
	assert.Equal(t, "run-1", row.AgentRunID, "agent_run_id must come from the session lookup, not the caller")
	assert.Equal(t, ptrText("issue-1"), row.IssueID, "issue_id must come from the session lookup")
	assert.EqualValues(t, 7, row.TrajectoryID)
	assert.JSONEq(t, `{"shard_id":"shard-1"}`, string(row.TensorRef))
	assert.Equal(t, ptrText("delegation"), row.ClosingEvent)
	// Env-snapshot fields are 1:1 with the segment (atomic insert).
	assert.JSONEq(t, `["sbx-1"]`, string(row.SandboxIDs))
	assert.Equal(t, ptrText("snap-1"), row.IssueSnapshotID)
	assert.JSONEq(t, `{"k":"v"}`, string(row.EnvState))
}

// TestCloseSegmentForEvent_LeafSegmentClosingEventEmpty verifies a leaf
// (root-completion) segment records closing_event as NULL (invalid pgtype.Text).
func TestInteractionDAG_CloseSegmentForEvent_LeafSegmentClosingEventEmpty(t *testing.T) {
	store := newFakeInteractionDAGStore()
	client := &fakeArealSegmentClient{
		closeSegmentID: 3,
		exportPayload:  json.RawMessage(`{"shard_id":"shard-leaf"}`),
	}
	svc := NewInteractionDAGService(store, client, true)
	require.NoError(t, svc.RecordSessionAgentRun(context.Background(), "proj-1", "sess-2", "run-2", ""))

	_, err := svc.CloseSegmentForEvent(context.Background(), "proj-1", "sess-2", "pk", "",
		map[string]any{"sandbox_ids": []string{"sbx-1"}})
	require.NoError(t, err)

	require.Len(t, store.segmentSnapshots, 1)
	assert.False(t, store.segmentSnapshots[0].ClosingEvent.Valid, "leaf segment closing_event must be NULL")
	// tensor_ref decoded from a payload that is itself the ref object.
	assert.JSONEq(t, `{"shard_id":"shard-leaf"}`, string(store.segmentSnapshots[0].TensorRef))
}

// TestCloseSegmentForEvent_MissingSessionLookupErrors verifies that when no
// RecordSessionAgentRun was called, CloseSegmentForEvent errors and does NOT
// call CloseSegment (no dangling trajectory close on the bridge).
func TestInteractionDAG_CloseSegmentForEvent_MissingSessionLookupErrors(t *testing.T) {
	store := newFakeInteractionDAGStore()
	client := &fakeArealSegmentClient{closeSegmentID: 1, exportPayload: json.RawMessage(`{"tensor_ref":{"shard_id":"s"}}`)}
	svc := NewInteractionDAGService(store, client, true)

	_, err := svc.CloseSegmentForEvent(context.Background(), "proj-1", "unknown-sess", "pk", "delegation", nil)
	require.Error(t, err)
	assert.Empty(t, client.closeCalls, "must not close a segment when the session lookup fails")
	assert.Empty(t, client.exportCalls, "must not export when the session lookup fails")
}

// TestCloseSegmentForEvent_CloseSegmentErrorPropagates verifies a bridge
// CloseSegment error is returned and ExportTrajectory is not called.
func TestInteractionDAG_CloseSegmentForEvent_CloseSegmentErrorPropagates(t *testing.T) {
	store := newFakeInteractionDAGStore()
	client := &fakeArealSegmentClient{
		closeSegmentErr: errors.New("bridge down"),
	}
	svc := NewInteractionDAGService(store, client, true)
	require.NoError(t, svc.RecordSessionAgentRun(context.Background(), "proj-1", "sess-1", "run-1", "issue-1"))

	_, err := svc.CloseSegmentForEvent(context.Background(), "proj-1", "sess-1", "pk", "delegation", nil)
	require.Error(t, err)
	assert.Empty(t, client.exportCalls, "must not export after a close failure")
	assert.Empty(t, store.segmentSnapshots, "must not record a segment after a close failure")
}

// TestCloseSegmentForEvent_ExportErrorPropagates verifies an export error is
// returned and no segment row is recorded.
func TestInteractionDAG_CloseSegmentForEvent_ExportErrorPropagates(t *testing.T) {
	store := newFakeInteractionDAGStore()
	client := &fakeArealSegmentClient{
		closeSegmentID: 9,
		exportErr:      errors.New("export 500"),
	}
	svc := NewInteractionDAGService(store, client, true)
	require.NoError(t, svc.RecordSessionAgentRun(context.Background(), "proj-1", "sess-1", "run-1", "issue-1"))

	_, err := svc.CloseSegmentForEvent(context.Background(), "proj-1", "sess-1", "pk", "delegation", nil)
	require.Error(t, err)
	assert.Empty(t, store.segmentSnapshots, "must not record a segment after an export failure")
}

// TestCloseSegmentForEvent_BadTensorRefErrors verifies that an export payload
// with no decodable tensor_ref is reported as an error (absence stays
// distinguishable at the boundary), and no segment is recorded.
func TestInteractionDAG_CloseSegmentForEvent_BadTensorRefErrors(t *testing.T) {
	store := newFakeInteractionDAGStore()
	client := &fakeArealSegmentClient{
		closeSegmentID: 4,
		exportPayload:  json.RawMessage(`"not-an-object"`),
	}
	svc := NewInteractionDAGService(store, client, true)
	require.NoError(t, svc.RecordSessionAgentRun(context.Background(), "proj-1", "sess-1", "run-1", "issue-1"))

	_, err := svc.CloseSegmentForEvent(context.Background(), "proj-1", "sess-1", "pk", "delegation", nil)
	require.Error(t, err)
	assert.Empty(t, store.segmentSnapshots, "must not record a segment when tensor_ref is undecodable")
}

// TestCloseSegmentForEvent_StoreInsertErrorPropagates verifies that when the
// atomic segment+snapshot store insert fails, the error is returned and nothing
// is persisted to the fake store. (With the real DB the CTE guarantees neither
// row survives; the unit test asserts the call was attempted but rejected.)
func TestInteractionDAG_CloseSegmentForEvent_StoreInsertErrorPropagates(t *testing.T) {
	store := newFakeInteractionDAGStore()
	store.insertSegmentSnapshotErr = errors.New("db write failed")
	client := &fakeArealSegmentClient{
		closeSegmentID: 5,
		exportPayload:  json.RawMessage(`{"tensor_ref":{"shard_id":"s"}}`),
	}
	svc := NewInteractionDAGService(store, client, true)
	require.NoError(t, svc.RecordSessionAgentRun(context.Background(), "proj-1", "sess-1", "run-1", "issue-1"))

	_, err := svc.CloseSegmentForEvent(context.Background(), "proj-1", "sess-1", "pk", "delegation", nil)
	require.Error(t, err)
	assert.Empty(t, store.segmentSnapshots, "must not persist on insert error")
}

// TestAddEdge_StoresTypedEdge verifies each allowed edge type is stored.
func TestInteractionDAG_AddEdge_StoresTypedEdge(t *testing.T) {
	for _, tc := range []struct {
		name     string
		edgeType string
	}{
		{"delegation", EdgeTypeDelegation},
		{"mention", EdgeTypeMention},
		{"completion", EdgeTypeCompletion},
	} {
		t.Run(tc.name, func(t *testing.T) {
			store := newFakeInteractionDAGStore()
			svc := NewInteractionDAGService(store, &fakeArealSegmentClient{}, true)
			err := svc.AddEdge(context.Background(), "proj-1", "seg-a", "seg-b", tc.edgeType)
			require.NoError(t, err)
			require.Len(t, store.edges, 1)
			e := store.edges[0]
			assert.Equal(t, "proj-1", e.ProjectID)
			assert.Equal(t, "seg-a", e.SrcSegmentID)
			assert.Equal(t, "seg-b", e.DstSegmentID)
			assert.Equal(t, tc.edgeType, e.Type)
		})
	}
}

// TestAddEdge_RejectsBadType verifies an invalid edge type returns
// ErrInvalidEdgeType and stores nothing.
func TestInteractionDAG_AddEdge_RejectsBadType(t *testing.T) {
	store := newFakeInteractionDAGStore()
	svc := NewInteractionDAGService(store, &fakeArealSegmentClient{}, true)
	err := svc.AddEdge(context.Background(), "proj-1", "seg-a", "seg-b", "handoff")
	require.Error(t, err)
	assert.ErrorIs(t, err, ErrInvalidEdgeType)
	assert.Empty(t, store.edges)
}

// TestAddEdge_RejectsMissingIDs verifies required-id validation.
func TestInteractionDAG_AddEdge_RejectsMissingIDs(t *testing.T) {
	svc := NewInteractionDAGService(newFakeInteractionDAGStore(), &fakeArealSegmentClient{}, true)
	for _, tc := range []struct{ project, src, dst string }{
		{"", "s", "d"}, {"p", "", "d"}, {"p", "s", ""},
	} {
		if err := svc.AddEdge(context.Background(), tc.project, tc.src, tc.dst, EdgeTypeDelegation); err == nil {
			t.Fatalf("expected error for %+v", tc)
		}
	}
}

// TestInteractionDAGService_DisabledIsNoOp verifies that a disabled service
// (INTERACTION_DAG_ENABLED=false) touches neither the store nor the bridge.
func TestInteractionDAGService_DisabledIsNoOp(t *testing.T) {
	store := newFakeInteractionDAGStore()
	client := &fakeArealSegmentClient{closeSegmentID: 1, exportPayload: json.RawMessage(`{"tensor_ref":{"shard_id":"s"}}`)}
	svc := NewInteractionDAGService(store, client, false)

	assert.NoError(t, svc.RecordSessionAgentRun(context.Background(), "p", "s", "r", "i"))
	_, err := svc.CloseSegmentForEvent(context.Background(), "p", "s", "pk", "delegation", nil)
	assert.NoError(t, err)
	assert.NoError(t, svc.AddEdge(context.Background(), "p", "a", "b", EdgeTypeDelegation))

	assert.Empty(t, store.sessionRuns)
	assert.Empty(t, store.segmentSnapshots)
	assert.Empty(t, store.edges)
	assert.Empty(t, client.closeCalls)
}

// TestCloseSegmentForEvent_FanOutDeterministic verifies that closing several
// segments for one session produces distinct, deterministic segment ids and
// acyclic edge ordering (delegation fan-out: src -> many dst).
func TestInteractionDAG_CloseSegmentForEvent_FanOutDeterministic(t *testing.T) {
	store := newFakeInteractionDAGStore()
	client := &fakeArealSegmentClient{
		closeSegmentID: 100,
		exportCallForTraj: map[int]json.RawMessage{
			100: json.RawMessage(`{"tensor_ref":{"shard_id":"sh-0"}}`),
			101: json.RawMessage(`{"tensor_ref":{"shard_id":"sh-1"}}`),
			102: json.RawMessage(`{"tensor_ref":{"shard_id":"sh-2"}}`),
		},
	}
	svc := NewInteractionDAGService(store, client, true)
	require.NoError(t, svc.RecordSessionAgentRun(context.Background(), "proj-1", "sess-1", "run-1", "issue-1"))

	// First close: the source segment.
	rootID, err := svc.CloseSegmentForEvent(context.Background(), "proj-1", "sess-1", "pk", "completion", nil)
	require.NoError(t, err)
	assert.Equal(t, "sess-1-100", rootID)

	// Two further closes (children). closeSegmentID is fixed at 100, so bump
	// it per call to get distinct trajectory ids.
	client.closeSegmentID = 101
	child1, err := svc.CloseSegmentForEvent(context.Background(), "proj-1", "sess-1", "pk", "delegation", nil)
	require.NoError(t, err)
	client.closeSegmentID = 102
	child2, err := svc.CloseSegmentForEvent(context.Background(), "proj-1", "sess-1", "pk", "delegation", nil)
	require.NoError(t, err)

	ids := map[string]bool{rootID: true, child1: true, child2: true}
	assert.Len(t, ids, 3, "segment ids must be distinct")
	assert.Equal(t, "sess-1-101", child1)
	assert.Equal(t, "sess-1-102", child2)
	// Three atomic segment+snapshot inserts recorded.
	require.Len(t, store.segmentSnapshots, 3)

	// Fan-out edges root -> child1, root -> child2 (acyclic, deterministic).
	require.NoError(t, svc.AddEdge(context.Background(), "proj-1", rootID, child1, EdgeTypeDelegation))
	require.NoError(t, svc.AddEdge(context.Background(), "proj-1", rootID, child2, EdgeTypeDelegation))
	require.Len(t, store.edges, 2)
	assert.Equal(t, rootID, store.edges[0].SrcSegmentID)
	assert.Equal(t, child1, store.edges[0].DstSegmentID)
}

// TestEncodeEnvSnapshot_NilOrEmpty verifies that a nil or empty env-snapshot
// map yields "{}" for env_state (not JSON "null"), matching the column's
// DEFAULT '{}'::jsonb intent. Minor #2: json.Marshal(nil map) returns "null".
func TestInteractionDAG_EncodeEnvSnapshot_NilOrEmpty(t *testing.T) {
	for _, tc := range []struct {
		name string
		snap map[string]any
	}{
		{"nil", nil},
		{"empty", map[string]any{}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			sandboxIDs, issueSnapshotID, envState := encodeEnvSnapshot(tc.snap)
			assert.JSONEq(t, `[]`, string(sandboxIDs))
			assert.False(t, issueSnapshotID.Valid)
			assert.JSONEq(t, `{}`, string(envState), "nil/empty snapshot must yield {} not null")
		})
	}
}

// --- Integration test against a real Postgres (skipped without DATABASE_URL) ---
// Validates the hand-written sqlc queries + migration 158 actually work,
// since sqlc generate is broken in this repo.

func interactionDAGTestPool(t *testing.T) *pgxpool.Pool {
	t.Helper()
	dbURL := os.Getenv("DATABASE_URL")
	if dbURL == "" {
		t.Skip("integration test requires Postgres at DATABASE_URL")
	}
	pool, err := pgxpool.New(context.Background(), dbURL)
	if err != nil {
		t.Fatalf("connect: %v", err)
	}
	return pool
}

// TestInteractionDAGQueries_Integration exercises the hand-written *db.Queries
// methods (Upsert/Get session_run, Insert segment+env_snapshot via the atomic
// CTE, Insert edge) inside a transaction that rolls back, so it is hermetic.
// Catches SQL/typing errors the fake-based unit tests cannot.
func TestInteractionDAGQueries_Integration(t *testing.T) {
	pool := interactionDAGTestPool(t)
	defer pool.Close()

	ctx := context.Background()
	tx, err := pool.Begin(ctx)
	require.NoError(t, err)
	defer tx.Rollback(ctx)
	q := db.New(tx)

	// Upsert + Get session_run round-trip (issue_id nullable -> NULL when empty).
	require.NoError(t, q.UpsertInteractionDAGSessionRun(ctx, db.UpsertInteractionDAGSessionRunParams{
		SessionID: "int-sess-1", ProjectID: "int-proj", AgentRunID: "int-run", IssueID: ptrText("int-issue"),
	}))
	got, err := q.GetInteractionDAGSessionRun(ctx, "int-sess-1")
	require.NoError(t, err)
	assert.Equal(t, "int-run", got.AgentRunID)
	assert.True(t, got.IssueID.Valid)
	assert.Equal(t, "int-issue", got.IssueID.String)

	// Empty issue_id -> NULL round-trips.
	require.NoError(t, q.UpsertInteractionDAGSessionRun(ctx, db.UpsertInteractionDAGSessionRunParams{
		SessionID: "int-sess-2", ProjectID: "int-proj", AgentRunID: "int-run-2", IssueID: pgtype.Text{},
	}))
	got2, err := q.GetInteractionDAGSessionRun(ctx, "int-sess-2")
	require.NoError(t, err)
	assert.False(t, got2.IssueID.Valid)

	// Insert segment + env_snapshot atomically via the CTE (FK satisfied within
	// the single statement; both rows commit or roll back together).
	segID := "int-sess-1-42"
	require.NoError(t, q.InsertInteractionDAGSegmentWithSnapshot(ctx, db.InsertInteractionDAGSegmentWithSnapshotParams{
		SegmentID: segID, ProjectID: "int-proj", AgentRunID: "int-run",
		IssueID: ptrText("int-issue"), TrajectoryID: 42,
		TensorRef: []byte(`{"shard_id":"int-shard"}`), ClosingEvent: ptrText("delegation"),
		SandboxIDs: []byte(`["sbx"]`), EnvState: []byte(`{}`),
	}))

	// Insert typed edges (CHECK constraint accepts the 3 types).
	for _, et := range []string{EdgeTypeDelegation, EdgeTypeMention, EdgeTypeCompletion} {
		require.NoError(t, q.InsertInteractionDAGEdge(ctx, db.InsertInteractionDAGEdgeParams{
			ProjectID: "int-proj", SrcSegmentID: segID, DstSegmentID: "int-other", Type: et,
		}), "edge type %s must be accepted", et)
	}

	// CHECK constraint rejects a bad type.
	err = q.InsertInteractionDAGEdge(ctx, db.InsertInteractionDAGEdgeParams{
		ProjectID: "int-proj", SrcSegmentID: segID, DstSegmentID: "int-other", Type: "handoff",
	})
	require.Error(t, err, "bad edge type must violate the CHECK constraint")
}
