// SPDX-License-Identifier: Apache-2.0

package service

import (
	"context"
	"errors"
	"testing"

	"github.com/jackc/pgx/v5"

	"github.com/multica-ai/multica/server/internal/util"
	db "github.com/multica-ai/multica/server/pkg/db/generated"
)

const (
	testBranchWorkspace  = "11111111-1111-1111-1111-111111111111"
	testBranchChannel    = "22222222-2222-2222-2222-222222222222"
	testTriggerInstance  = "33333333-3333-3333-3333-333333333333"
	testPeerInstance     = "44444444-4444-4444-4444-444444444444"
	testBranchCheckpoint = "55555555-5555-5555-5555-555555555555"
)

type fakeBranchCheckpointStore struct {
	createCalls []EnvCheckpointCreateInput
	created     EnvCheckpoint
	createErr   error
	existing    []EnvCheckpoint
	listErr     error
}

func (f *fakeBranchCheckpointStore) Create(_ context.Context, in EnvCheckpointCreateInput) (EnvCheckpoint, error) {
	f.createCalls = append(f.createCalls, in)
	if f.createErr != nil {
		return EnvCheckpoint{}, f.createErr
	}
	cp := f.created
	if cp.ID == "" {
		cp = EnvCheckpoint{ID: testBranchCheckpoint, SaveStatus: EnvCheckpointSaveComplete}
	}
	return cp, nil
}

func (f *fakeBranchCheckpointStore) List(_ context.Context, _, _ string) ([]EnvCheckpoint, error) {
	if f.listErr != nil {
		return nil, f.listErr
	}
	return f.existing, nil
}

type fakeBranchSavepointReader struct {
	byCheckpoint map[string][]Savepoint
	listErr      error
	failedCalls  []string
}

func (f *fakeBranchSavepointReader) ListSavepoints(_ context.Context, checkpointID, _ string) ([]Savepoint, error) {
	if f.listErr != nil {
		return nil, f.listErr
	}
	return f.byCheckpoint[checkpointID], nil
}

func (f *fakeBranchSavepointReader) MarkSavepointFailed(_ context.Context, snapshotID, _, _ string) error {
	f.failedCalls = append(f.failedCalls, snapshotID)
	return nil
}

type fakeBranchRefResolver struct {
	refs    map[string]SandboxInstanceRef
	errs    map[string]error
	queried []string
}

func (f *fakeBranchRefResolver) GetSandboxInstanceRef(_ context.Context, workspaceID, instanceID string) (SandboxInstanceRef, error) {
	f.queried = append(f.queried, instanceID)
	if err := f.errs[instanceID]; err != nil {
		return SandboxInstanceRef{}, err
	}
	ref, ok := f.refs[instanceID]
	if !ok {
		return SandboxInstanceRef{}, errors.New("no such instance")
	}
	ref.WorkspaceID = workspaceID
	ref.InstanceID = instanceID
	return ref, nil
}

type fakeBranchSourceQueries struct {
	peers        []string
	peersErr     error
	peersArg     db.ListReadyEnvDispatchChannelInstancesParams
	savepointRow db.SandboxSnapshot
	savepointErr error
	savepointArg db.GetReadySavepointForInstanceParams
}

func (f *fakeBranchSourceQueries) ListReadyEnvDispatchChannelInstances(_ context.Context, arg db.ListReadyEnvDispatchChannelInstancesParams) ([]string, error) {
	f.peersArg = arg
	return f.peers, f.peersErr
}

func (f *fakeBranchSourceQueries) GetReadySavepointForInstance(_ context.Context, arg db.GetReadySavepointForInstanceParams) (db.SandboxSnapshot, error) {
	f.savepointArg = arg
	return f.savepointRow, f.savepointErr
}

type branchProviderFixture struct {
	checkpoints *fakeBranchCheckpointStore
	savepoints  *fakeBranchSavepointReader
	lanes       *fakeLaneRepo
	refs        *fakeBranchRefResolver
	queries     *fakeBranchSourceQueries
	provider    *branchSavepointProvider
}

func newBranchProviderFixture() *branchProviderFixture {
	f := &branchProviderFixture{
		checkpoints: &fakeBranchCheckpointStore{},
		savepoints: &fakeBranchSavepointReader{byCheckpoint: map[string][]Savepoint{
			testBranchCheckpoint: {
				{SnapshotID: "snap-trigger", CubeSnapshotID: "cube-trigger", InstanceID: testTriggerInstance, Status: savepointStatusReady},
				{SnapshotID: "snap-peer", CubeSnapshotID: "cube-peer", InstanceID: testPeerInstance, Status: savepointStatusReady},
			},
		}},
		lanes: newFakeLaneRepo(),
		refs: &fakeBranchRefResolver{refs: map[string]SandboxInstanceRef{
			testTriggerInstance: {NodeID: "node-1", LocalRef: "local-trigger"},
			testPeerInstance:    {NodeID: "node-1", LocalRef: "local-peer"},
		}},
		queries: &fakeBranchSourceQueries{},
	}
	f.provider = NewBranchSavepointProvider(f.checkpoints, f.savepoints, f.lanes, f.refs, f.queries)
	return f
}

func branchSavepointInput() BranchSavepointInput {
	return BranchSavepointInput{
		WorkspaceID:      testBranchWorkspace,
		ActorUserID:      "user-1",
		SourceEnvID:      "env-src-1",
		SourceProjectID:  "proj-src-1",
		SourceChannelID:  testBranchChannel,
		SourceInstanceID: testTriggerInstance,
	}
}

// The capture must cover every sandbox the branch can inherit, not just the
// trigger's. A peer whose binding was copied is provisioned later on the mention
// path, which has five seconds -- far too little to snapshot anything -- so a peer
// missed here can never boot from source state at all.
func TestBranchCaptureCoversThePeersTheChannelCopyCarriesOver(t *testing.T) {
	f := newBranchProviderFixture()
	f.queries.peers = []string{testPeerInstance}

	got, err := f.provider.EnsureBranchSavepoint(context.Background(), branchSavepointInput())
	if err != nil {
		t.Fatalf("ensure: %v", err)
	}
	if len(f.checkpoints.createCalls) != 1 {
		t.Fatalf("checkpoint creates = %d, want 1", len(f.checkpoints.createCalls))
	}
	created := f.checkpoints.createCalls[0]
	if len(created.SandboxRefs) != 2 {
		t.Fatalf("captured refs = %+v, want the trigger and the peer", created.SandboxRefs)
	}
	if created.SaveMode != SaveModeSnapshot {
		t.Fatalf("save mode = %q, want snapshot: pausing the source would stop the env being branched",
			created.SaveMode)
	}
	// The trigger's own savepoint is what the dispatch boots from.
	if got.Template != "cube-trigger" || got.CheckpointID != testBranchCheckpoint {
		t.Fatalf("savepoint = %+v, want the trigger's", got)
	}
}

// Capture is keyed on the source env so re-expanding the same state reuses the
// snapshot instead of paying for another one (design D2).
func TestBranchCaptureReusesACompletedCaptureOfTheSameEnv(t *testing.T) {
	f := newBranchProviderFixture()
	f.checkpoints.existing = []EnvCheckpoint{{
		ID:         testBranchCheckpoint,
		EventRef:   branchSavepointEventRef("env-src-1"),
		SaveMode:   SaveModeSnapshot,
		SaveStatus: EnvCheckpointSaveComplete,
	}}

	got, err := f.provider.EnsureBranchSavepoint(context.Background(), branchSavepointInput())
	if err != nil {
		t.Fatalf("ensure: %v", err)
	}
	if len(f.checkpoints.createCalls) != 0 {
		t.Fatalf("a reusable capture must not snapshot again, got %+v", f.checkpoints.createCalls)
	}
	if got.Template != "cube-trigger" {
		t.Fatalf("template = %q, want the existing capture's", got.Template)
	}
}

// A capture of a different env, or one that never completed, is not reusable.
func TestBranchCaptureIgnoresCheckpointsItCannotUse(t *testing.T) {
	for _, tc := range []struct {
		name string
		cp   EnvCheckpoint
	}{
		{"another env", EnvCheckpoint{
			ID: testBranchCheckpoint, EventRef: branchSavepointEventRef("env-other"),
			SaveMode: SaveModeSnapshot, SaveStatus: EnvCheckpointSaveComplete,
		}},
		{"never completed", EnvCheckpoint{
			ID: testBranchCheckpoint, EventRef: branchSavepointEventRef("env-src-1"),
			SaveMode: SaveModeSnapshot, SaveStatus: EnvCheckpointSaveFailed,
		}},
		{"paused in place, so nothing was captured", EnvCheckpoint{
			ID: testBranchCheckpoint, EventRef: branchSavepointEventRef("env-src-1"),
			SaveMode: SaveModePauseInPlace, SaveStatus: EnvCheckpointSaveComplete,
		}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			f := newBranchProviderFixture()
			f.checkpoints.existing = []EnvCheckpoint{tc.cp}

			if _, err := f.provider.EnsureBranchSavepoint(context.Background(), branchSavepointInput()); err != nil {
				t.Fatalf("ensure: %v", err)
			}
			if len(f.checkpoints.createCalls) != 1 {
				t.Fatalf("want a fresh capture, got %d creates", len(f.checkpoints.createCalls))
			}
		})
	}
}

// A capture whose save did not complete has no usable template, so returning it
// would hand the dispatch an empty string and provision a blank sandbox.
func TestBranchCaptureRefusesAnIncompleteSave(t *testing.T) {
	f := newBranchProviderFixture()
	f.checkpoints.created = EnvCheckpoint{ID: testBranchCheckpoint, SaveStatus: EnvCheckpointSaveTimedOut}

	if _, err := f.provider.EnsureBranchSavepoint(context.Background(), branchSavepointInput()); err == nil {
		t.Fatal("a timed-out capture must not be reported as a usable savepoint")
	}
}

// A peer's sandbox may be deleted between reading the bindings and resolving refs;
// that peer just boots fresh. The trigger's cannot be skipped -- the dispatch it
// serves has nothing to continue without it.
func TestBranchCaptureSkipsAVanishedPeerButNotTheTrigger(t *testing.T) {
	f := newBranchProviderFixture()
	f.queries.peers = []string{testPeerInstance}
	f.refs.errs = map[string]error{testPeerInstance: errors.New("instance gone")}

	if _, err := f.provider.EnsureBranchSavepoint(context.Background(), branchSavepointInput()); err != nil {
		t.Fatalf("a vanished peer must not fail the dispatch: %v", err)
	}
	if refs := f.checkpoints.createCalls[0].SandboxRefs; len(refs) != 1 || refs[0].InstanceID != testTriggerInstance {
		t.Fatalf("captured refs = %+v, want the trigger only", refs)
	}

	f2 := newBranchProviderFixture()
	f2.refs.errs = map[string]error{testTriggerInstance: errors.New("instance gone")}
	if _, err := f2.provider.EnsureBranchSavepoint(context.Background(), branchSavepointInput()); err == nil {
		t.Fatal("a missing trigger sandbox must fail the dispatch")
	}
}

// The peer lookup is workspace-scoped and returns the template, which is what the
// mention path creates the peer's sandbox from.
func TestPeerLookupReturnsTheTemplateForItsSourceInstance(t *testing.T) {
	f := newBranchProviderFixture()
	f.queries.savepointRow = db.SandboxSnapshot{CubeSnapshotID: "cube-peer"}

	got, err := f.provider.LookupSavepointTemplate(context.Background(), testBranchWorkspace, testPeerInstance)
	if err != nil {
		t.Fatalf("lookup: %v", err)
	}
	if got != "cube-peer" {
		t.Fatalf("template = %q, want cube-peer", got)
	}
	wantWorkspace, _ := util.ParseUUID(testBranchWorkspace)
	wantInstance, _ := util.ParseUUID(testPeerInstance)
	if f.queries.savepointArg.WorkspaceID != wantWorkspace || f.queries.savepointArg.InstanceID != wantInstance {
		t.Fatalf("lookup arg = %+v, want it scoped to the workspace and instance", f.queries.savepointArg)
	}
}

// A source instance with no capture is a typed refusal, not an empty template: the
// caller must fail rather than silently provision a sandbox with none of the state
// its binding says it inherits.
func TestPeerLookupRefusesWhenNothingWasCaptured(t *testing.T) {
	for _, tc := range []struct {
		name string
		set  func(*fakeBranchSourceQueries)
	}{
		{"no row", func(q *fakeBranchSourceQueries) { q.savepointErr = pgx.ErrNoRows }},
		{"row with no template", func(q *fakeBranchSourceQueries) {
			q.savepointRow = db.SandboxSnapshot{CubeSnapshotID: ""}
		}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			f := newBranchProviderFixture()
			tc.set(f.queries)

			_, err := f.provider.LookupSavepointTemplate(context.Background(), testBranchWorkspace, testPeerInstance)
			if !errors.Is(err, ErrNoSavepointForInstance) {
				t.Fatalf("err = %v, want ErrNoSavepointForInstance", err)
			}
		})
	}
}

func TestPeerLookupRejectsMalformedIDs(t *testing.T) {
	f := newBranchProviderFixture()
	if _, err := f.provider.LookupSavepointTemplate(context.Background(), "not-a-uuid", testPeerInstance); err == nil {
		t.Fatal("malformed workspace id must be rejected before querying")
	}
	if _, err := f.provider.LookupSavepointTemplate(context.Background(), testBranchWorkspace, "not-a-uuid"); err == nil {
		t.Fatal("malformed instance id must be rejected before querying")
	}
}

// The lane records the rows the reset phase created rather than minting its own
// (design D6).
func TestLaneClaimRecordsTheRolloutsRows(t *testing.T) {
	f := newBranchProviderFixture()

	lane, err := f.provider.ClaimBranchLane(context.Background(), BranchLaneInput{
		WorkspaceID: testBranchWorkspace, CheckpointID: testBranchCheckpoint,
		LaneKey: "anchor#0", EnvID: "env-copy-1", ProjectID: "proj-copy-1", ChannelID: "chan-copy-1",
	})
	if err != nil {
		t.Fatalf("claim: %v", err)
	}
	if lane.LaneID == "" || lane.LaneKey != "anchor#0" {
		t.Fatalf("lane = %+v", lane)
	}
	stored, err := f.lanes.GetLane(context.Background(), testBranchCheckpoint, testBranchWorkspace, "anchor#0")
	if err != nil {
		t.Fatalf("load lane: %v", err)
	}
	if stored.EnvID != "env-copy-1" || stored.ProjectID != "proj-copy-1" || stored.ChannelID != "chan-copy-1" {
		t.Fatalf("stored lane = %+v, want the rollout's own rows", stored)
	}
	if stored.Status != LaneStatusProvisioning {
		t.Fatalf("status = %q, want provisioning until the sandbox exists", stored.Status)
	}
}

// A retried dispatch claims the same lane key. Losing that race is the retry
// finding its own earlier lane, so it adopts it instead of failing.
func TestLaneClaimAdoptsAnExistingLaneOnRetry(t *testing.T) {
	f := newBranchProviderFixture()
	f.lanes.seed(EnvCheckpointLane{
		ID: "lane-existing", CheckpointID: testBranchCheckpoint,
		WorkspaceID: testBranchWorkspace, LaneKey: "anchor#0", Status: LaneStatusProvisioning,
	})

	lane, err := f.provider.ClaimBranchLane(context.Background(), BranchLaneInput{
		WorkspaceID: testBranchWorkspace, CheckpointID: testBranchCheckpoint, LaneKey: "anchor#0",
		EnvID: "env-copy-1",
	})
	if err != nil {
		t.Fatalf("claim on retry: %v", err)
	}
	if lane.LaneID != "lane-existing" {
		t.Fatalf("lane = %+v, want the lane the first attempt claimed", lane)
	}
}

func TestLaneSettleRecordsProvisionedIDsThenMarksReady(t *testing.T) {
	f := newBranchProviderFixture()
	claimed, err := f.provider.ClaimBranchLane(context.Background(), BranchLaneInput{
		WorkspaceID: testBranchWorkspace, CheckpointID: testBranchCheckpoint, LaneKey: "anchor#0",
	})
	if err != nil {
		t.Fatalf("claim: %v", err)
	}

	if err := f.provider.SettleBranchLane(context.Background(), BranchLaneSettleInput{
		WorkspaceID: testBranchWorkspace, LaneID: claimed.LaneID, Status: LaneStatusReady,
		InstanceID: "inst-1", RuntimeID: "rt-1", ChatSessionID: "sess-1",
	}); err != nil {
		t.Fatalf("settle: %v", err)
	}
	stored, err := f.lanes.GetLane(context.Background(), testBranchCheckpoint, testBranchWorkspace, "anchor#0")
	if err != nil {
		t.Fatalf("load lane: %v", err)
	}
	if stored.InstanceID != "inst-1" || stored.RuntimeID != "rt-1" || stored.ChatSessionID != "sess-1" {
		t.Fatalf("stored lane = %+v, want the provisioned ids", stored)
	}
	if stored.Status != LaneStatusReady {
		t.Fatalf("status = %q, want ready", stored.Status)
	}
}

// A failed lane has to reach a terminal state, or reclamation will refuse to
// release the savepoint it holds forever.
func TestLaneSettleMarksFailedWithTheReason(t *testing.T) {
	f := newBranchProviderFixture()
	claimed, err := f.provider.ClaimBranchLane(context.Background(), BranchLaneInput{
		WorkspaceID: testBranchWorkspace, CheckpointID: testBranchCheckpoint, LaneKey: "anchor#0",
	})
	if err != nil {
		t.Fatalf("claim: %v", err)
	}

	if err := f.provider.SettleBranchLane(context.Background(), BranchLaneSettleInput{
		WorkspaceID: testBranchWorkspace, LaneID: claimed.LaneID,
		Status: LaneStatusFailed, Error: "sandbox create refused",
	}); err != nil {
		t.Fatalf("settle failed: %v", err)
	}
	stored, err := f.lanes.GetLane(context.Background(), testBranchCheckpoint, testBranchWorkspace, "anchor#0")
	if err != nil {
		t.Fatalf("load lane: %v", err)
	}
	if stored.Status != LaneStatusFailed || stored.Error != "sandbox create refused" {
		t.Fatalf("stored lane = %+v, want failed with the reason", stored)
	}
}
