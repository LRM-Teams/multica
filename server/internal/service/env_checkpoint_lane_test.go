// SPDX-License-Identifier: Apache-2.0

package service

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"sync"
	"testing"
	"time"
)

// --- fakes ---

// fakeLaneRepo stands in for the unique index. ClaimLane loses on an existing
// key, which is the only behavior of the real index the service depends on.
type fakeLaneRepo struct {
	mu    sync.Mutex
	lanes map[string]EnvCheckpointLane
	seq   int
}

func newFakeLaneRepo() *fakeLaneRepo {
	return &fakeLaneRepo{lanes: map[string]EnvCheckpointLane{}}
}

func laneRepoKey(checkpointID, laneKey string) string { return checkpointID + "/" + laneKey }

func (f *fakeLaneRepo) seed(lane EnvCheckpointLane) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.lanes[laneRepoKey(lane.CheckpointID, lane.LaneKey)] = lane
}

func (f *fakeLaneRepo) ClaimLane(_ context.Context, checkpointID, workspaceID, laneKey string) (EnvCheckpointLane, bool, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	key := laneRepoKey(checkpointID, laneKey)
	if _, exists := f.lanes[key]; exists {
		return EnvCheckpointLane{}, false, nil
	}
	f.seq++
	lane := EnvCheckpointLane{
		ID: fmt.Sprintf("lane-%d", f.seq), CheckpointID: checkpointID,
		WorkspaceID: workspaceID, LaneKey: laneKey, Status: LaneStatusProvisioning,
	}
	f.lanes[key] = lane
	return lane, true, nil
}

func (f *fakeLaneRepo) GetLane(_ context.Context, checkpointID, _, laneKey string) (EnvCheckpointLane, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	lane, ok := f.lanes[laneRepoKey(checkpointID, laneKey)]
	if !ok {
		return EnvCheckpointLane{}, fmt.Errorf("not found: lane %q", laneKey)
	}
	return lane, nil
}

func (f *fakeLaneRepo) ListLanes(_ context.Context, checkpointID, _ string) ([]EnvCheckpointLane, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	var out []EnvCheckpointLane
	for _, lane := range f.lanes {
		if lane.CheckpointID == checkpointID {
			out = append(out, lane)
		}
	}
	return out, nil
}

// byID finds a lane by its surrogate id, mirroring the real UPDATE ... WHERE id.
func (f *fakeLaneRepo) byID(laneID string) (string, EnvCheckpointLane, bool) {
	for key, lane := range f.lanes {
		if lane.ID == laneID {
			return key, lane, true
		}
	}
	return "", EnvCheckpointLane{}, false
}

func (f *fakeLaneRepo) RecordLaneStep(_ context.Context, laneID, _ string, step LaneStep) (EnvCheckpointLane, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	key, lane, ok := f.byID(laneID)
	if !ok {
		return EnvCheckpointLane{}, fmt.Errorf("not found: lane id %q", laneID)
	}
	// COALESCE semantics: an empty field leaves the stored value alone.
	for _, f := range []struct {
		dst *string
		src string
	}{
		{&lane.InstanceID, step.InstanceID},
		{&lane.ProjectID, step.ProjectID},
		{&lane.RuntimeID, step.RuntimeID},
		{&lane.TaskID, step.TaskID},
		{&lane.EnvID, step.EnvID},
		{&lane.ChannelID, step.ChannelID},
		{&lane.ChatSessionID, step.ChatSessionID},
		{&lane.SourceMessageID, step.SourceMessageID},
	} {
		if f.src != "" {
			*f.dst = f.src
		}
	}
	f.lanes[key] = lane
	return lane, nil
}

func (f *fakeLaneRepo) MarkLaneReady(_ context.Context, laneID, _ string) (EnvCheckpointLane, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	key, lane, ok := f.byID(laneID)
	if !ok {
		return EnvCheckpointLane{}, fmt.Errorf("not found: lane id %q", laneID)
	}
	lane.Status = LaneStatusReady
	lane.Error = ""
	f.lanes[key] = lane
	return lane, nil
}

func (f *fakeLaneRepo) MarkLaneFailed(_ context.Context, laneID, _, reason string) (EnvCheckpointLane, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	key, lane, ok := f.byID(laneID)
	if !ok {
		return EnvCheckpointLane{}, fmt.Errorf("not found: lane id %q", laneID)
	}
	lane.Status = LaneStatusFailed
	lane.Error = reason
	f.lanes[key] = lane
	return lane, nil
}

func (f *fakeLaneRepo) CountProvisioningLanes(_ context.Context, checkpointID, _ string) (int, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	n := 0
	for _, lane := range f.lanes {
		if lane.CheckpointID == checkpointID && lane.Status == LaneStatusProvisioning {
			n++
		}
	}
	return n, nil
}

type fakeLaneMaterializer struct {
	instanceCalls []LaneInstanceInput
	projectCalls  []LaneProjectInput
	runtimeCalls  []LaneRuntimeInput

	instanceErr error
	projectErr  error
	runtimeErr  error
}

func (f *fakeLaneMaterializer) CreateLaneInstance(_ context.Context, in LaneInstanceInput) (SandboxInstanceRef, error) {
	f.instanceCalls = append(f.instanceCalls, in)
	if f.instanceErr != nil {
		return SandboxInstanceRef{}, f.instanceErr
	}
	return SandboxInstanceRef{
		InstanceID:  fmt.Sprintf("inst-%d", len(f.instanceCalls)),
		WorkspaceID: in.WorkspaceID,
	}, nil
}

func (f *fakeLaneMaterializer) CopyLaneProjectSubtree(_ context.Context, in LaneProjectInput) (string, string, error) {
	f.projectCalls = append(f.projectCalls, in)
	if f.projectErr != nil {
		return "", "", f.projectErr
	}
	n := len(f.projectCalls)
	return fmt.Sprintf("proj-%d", n), fmt.Sprintf("env-%d", n), nil
}

func (f *fakeLaneMaterializer) ProvisionLaneAgent(_ context.Context, in LaneRuntimeInput) (LaneBinding, error) {
	f.runtimeCalls = append(f.runtimeCalls, in)
	if f.runtimeErr != nil {
		return LaneBinding{}, f.runtimeErr
	}
	n := len(f.runtimeCalls)
	return LaneBinding{
		RuntimeID:       fmt.Sprintf("rt-%d", n),
		DaemonID:        fmt.Sprintf("daemon-%d", n),
		AgentID:         in.AgentID,
		ChannelID:       fmt.Sprintf("chan-%d", n),
		ChatSessionID:   fmt.Sprintf("cs-%d", n),
		SourceMessageID: fmt.Sprintf("msg-%d", n),
	}, nil
}

type fakeSavepointReader struct {
	savepoints []Savepoint
	listCalls  int
	failed     []string
	listErr    error
}

func (f *fakeSavepointReader) ListSavepoints(_ context.Context, _, _ string) ([]Savepoint, error) {
	f.listCalls++
	if f.listErr != nil {
		return nil, f.listErr
	}
	return f.savepoints, nil
}

func (f *fakeSavepointReader) MarkSavepointFailed(_ context.Context, snapshotID, _, _ string) error {
	f.failed = append(f.failed, snapshotID)
	return nil
}

// --- fixture ---

type fanoutDeps struct {
	repo       *fakeCheckpointRepo
	creator    *fakeSavepointCreator
	savepoints *fakeSavepointReader
	lanes      *fakeLaneRepo
	mat        *fakeLaneMaterializer
	forked     *fakeContinuationStrategy
}

// newFanoutFixture builds a snapshot-mode checkpoint with exactly one ready
// savepoint over one source instance — the shape every fan-out case starts from,
// so each test states only its own deviation.
func newFanoutFixture(t *testing.T) (*EnvCheckpointService, *fanoutDeps) {
	t.Helper()
	d := &fanoutDeps{
		repo:    newFakeCheckpointRepo(),
		creator: &fakeSavepointCreator{},
		savepoints: &fakeSavepointReader{savepoints: []Savepoint{
			{SnapshotID: "snap-1", CubeSnapshotID: "cube-1", InstanceID: "src-1", Status: "ready"},
		}},
		lanes:  newFakeLaneRepo(),
		mat:    &fakeLaneMaterializer{},
		forked: &fakeContinuationStrategy{mode: SaveModeSnapshot},
	}
	d.repo.checkpoints["cp-1"] = EnvCheckpoint{
		ID: "cp-1", WorkspaceID: "ws", ProjectID: "proj",
		SaveMode: SaveModeSnapshot, SaveStatus: EnvCheckpointSaveComplete,
		SandboxRefs:     []SandboxInstanceRef{{InstanceID: "src-1", WorkspaceID: "ws"}},
		ResumeTrigger:   json.RawMessage(`{"agent_id":"a-1","project_id":"proj","kind":"chat"}`),
		SourceChannelID: "chan-src",
	}
	svc := NewEnvCheckpointService(d.repo, &fakeCheckpointSaver{}, &fakeCheckpointResumer{},
		&fakeProjectSnapshotReader{}, &fakeInFlightResolver{},
		ContinuationRegistry{Forked: d.forked}).
		WithSavepointCreator(d.creator).
		WithLanes(d.lanes, d.mat, d.savepoints)
	return svc, d
}

func fanOut(svc *EnvCheckpointService, laneCount int, anchor string) (ResumeFromCheckpointResult, error) {
	return svc.ResumeFromCheckpoint(context.Background(), ResumeFromCheckpointInput{
		WorkspaceID: "ws", CheckpointID: "cp-1", ActorUserID: "u",
		LaneCount: laneCount, LaneKeyAnchor: anchor,
	})
}

// --- tests ---

// The invariant the whole change exists for: N lanes cost one snapshot, taken
// once at capture time, not one snapshot per lane.
func TestThreeLanesTriggerOneSnapshotPerSourceInstance(t *testing.T) {
	svc, deps := newFanoutFixture(t)

	res, err := fanOut(svc, 3, "dispatch-abc")
	if err != nil {
		t.Fatalf("resume: %v", err)
	}
	if len(deps.creator.calls) != 0 {
		t.Fatalf("resume must take no new snapshot, got %d", len(deps.creator.calls))
	}
	if deps.savepoints.listCalls != 1 {
		t.Fatalf("resume must read the savepoints once, got %d", deps.savepoints.listCalls)
	}
	if len(res.Lanes) != 3 {
		t.Fatalf("lanes = %d, want 3", len(res.Lanes))
	}
	if len(deps.mat.instanceCalls) != 3 {
		t.Fatalf("want 3 lane instances, got %d", len(deps.mat.instanceCalls))
	}
	for _, c := range deps.mat.instanceCalls {
		if c.Savepoint.CubeSnapshotID != "cube-1" {
			t.Fatalf("lane instance must come from the single savepoint, got %q", c.Savepoint.CubeSnapshotID)
		}
	}
	if len(deps.mat.projectCalls) != 3 || len(deps.mat.runtimeCalls) != 3 {
		t.Fatalf("each lane needs its own subtree and runtime: subtrees=%d runtimes=%d",
			len(deps.mat.projectCalls), len(deps.mat.runtimeCalls))
	}
	// Sharing a runtime or a conversation would make the lanes observe each
	// other, which is the opposite of independent continuations.
	for _, field := range []struct {
		name string
		get  func(ContinuationRequest) string
	}{
		{"runtime", func(r ContinuationRequest) string { return r.Lane.RuntimeID }},
		{"env", func(r ContinuationRequest) string { return r.Lane.LaneEnvID }},
		{"channel", func(r ContinuationRequest) string { return r.Lane.ChannelID }},
		{"chat session", func(r ContinuationRequest) string { return r.Lane.ChatSessionID }},
	} {
		seen := map[string]bool{}
		for _, c := range deps.forked.calls {
			v := field.get(c)
			if v == "" {
				t.Fatalf("lane %q has no %s", c.Lane.LaneKey, field.name)
			}
			if seen[v] {
				t.Fatalf("lanes must not share a %s: %q", field.name, v)
			}
			seen[v] = true
		}
	}
	if len(deps.forked.calls) != 3 {
		t.Fatalf("want 3 continuations, got %d", len(deps.forked.calls))
	}
	if res.Status != ResumeCompleted {
		t.Fatalf("status = %q, want completed", res.Status)
	}
}

func TestRepeatedLaneKeyReturnsExistingLane(t *testing.T) {
	svc, deps := newFanoutFixture(t)

	first, err := fanOut(svc, 1, "same")
	if err != nil {
		t.Fatalf("first resume: %v", err)
	}
	second, err := fanOut(svc, 1, "same")
	if err != nil {
		t.Fatalf("second resume: %v", err)
	}
	if len(deps.mat.instanceCalls) != 1 {
		t.Fatalf("retry must not materialize a second sandbox, instances = %d", len(deps.mat.instanceCalls))
	}
	if first.Lanes[0].LaneKey != second.Lanes[0].LaneKey {
		t.Fatalf("lane key not stable: %q vs %q", first.Lanes[0].LaneKey, second.Lanes[0].LaneKey)
	}
	if first.Lanes[0].InstanceID != second.Lanes[0].InstanceID {
		t.Fatalf("retry must return the existing instance: %q vs %q",
			first.Lanes[0].InstanceID, second.Lanes[0].InstanceID)
	}
	if len(deps.forked.calls) != 1 {
		t.Fatalf("retry must not re-enqueue the continuation, calls = %d", len(deps.forked.calls))
	}
}

func TestNewLaneKeyReExpandsWithoutASecondCheckpoint(t *testing.T) {
	svc, deps := newFanoutFixture(t)

	for _, anchor := range []string{"anchor-a", "anchor-b"} {
		if _, err := fanOut(svc, 1, anchor); err != nil {
			t.Fatalf("resume %s: %v", anchor, err)
		}
	}
	all, err := deps.lanes.ListLanes(context.Background(), "cp-1", "ws")
	if err != nil {
		t.Fatalf("list lanes: %v", err)
	}
	if len(all) != 2 {
		t.Fatalf("a new anchor must add a lane, got %d", len(all))
	}
	if len(deps.repo.createCalls) != 0 {
		t.Fatalf("re-expansion must not create a second checkpoint, got %d", len(deps.repo.createCalls))
	}
	if len(deps.creator.calls) != 0 {
		t.Fatalf("re-expansion must reuse the savepoint, snapshots taken = %d", len(deps.creator.calls))
	}
}

func TestInterruptedLaneContinuesFromFirstIncompleteStep(t *testing.T) {
	svc, deps := newFanoutFixture(t)
	// Crashed after the sandbox came up, before the subtree copy.
	deps.lanes.seed(EnvCheckpointLane{
		ID: "l-0", CheckpointID: "cp-1", WorkspaceID: "ws",
		LaneKey: laneKeyForOrdinal("dispatch-abc", 0), Status: LaneStatusProvisioning,
		InstanceID: "inst-recovered",
	})

	res, err := fanOut(svc, 1, "dispatch-abc")
	if err != nil {
		t.Fatalf("resume: %v", err)
	}
	if len(deps.mat.instanceCalls) != 0 {
		t.Fatalf("a completed step must not re-run, instance calls = %d", len(deps.mat.instanceCalls))
	}
	if len(deps.mat.projectCalls) != 1 || len(deps.mat.runtimeCalls) != 1 {
		t.Fatalf("remaining steps must run once: subtrees=%d runtimes=%d",
			len(deps.mat.projectCalls), len(deps.mat.runtimeCalls))
	}
	if res.Lanes[0].InstanceID != "inst-recovered" {
		t.Fatalf("a recovered lane must keep its sandbox, got %q", res.Lanes[0].InstanceID)
	}
	if res.Lanes[0].Status != LaneStatusReady {
		t.Fatalf("recovered lane status = %q, want ready", res.Lanes[0].Status)
	}
}

// TestFanOutRefusedForACheckpointWithNoSourceConversation holds design D8's
// release boundary. Lanes must each continue their own copy of the source
// conversation; a checkpoint that never recorded which conversation that was
// could only be served from the source's own channel, which would put every lane
// in one thread. Refusing is typed as not-resumable because the request is fine
// and the checkpoint is what cannot serve it.
func TestFanOutRefusedForACheckpointWithNoSourceConversation(t *testing.T) {
	svc, deps := newFanoutFixture(t)
	cp := deps.repo.checkpoints["cp-1"]
	cp.SourceChannelID = ""
	deps.repo.checkpoints["cp-1"] = cp

	_, err := fanOut(svc, 2, "dispatch-abc")
	if !errors.Is(err, ErrCheckpointNotResumable) {
		t.Fatalf("err = %v, want ErrCheckpointNotResumable", err)
	}
	if len(deps.lanes.lanes) != 0 {
		t.Fatalf("no lane may be claimed for a refused fan-out, got %+v", deps.lanes.lanes)
	}

	// One lane is not fan-out: the caller supplies that lane's conversation, so
	// it stays available and this refusal must not reach it.
	if _, err := fanOut(svc, 1, "dispatch-abc"); err != nil {
		t.Fatalf("single-lane resume must stay available: %v", err)
	}
}

// TestRuntimeStepActsOnTheLanesRecordedIdentity pins what the runtime step is
// told. Branch dispatch pre-seeds the lane with the env, project and channel its
// reset phase created (design D6), so the step has to act on those; a step that
// only learns the sandbox would mint a second conversation and leave the copied
// one it exists to continue unused.
func TestRuntimeStepActsOnTheLanesRecordedIdentity(t *testing.T) {
	svc, deps := newFanoutFixture(t)
	deps.lanes.seed(EnvCheckpointLane{
		ID: "l-0", CheckpointID: "cp-1", WorkspaceID: "ws",
		LaneKey: laneKeyForOrdinal("dispatch-abc", 0), Status: LaneStatusProvisioning,
		InstanceID: "inst-seeded", ProjectID: "proj-seeded", EnvID: "env-seeded",
		ChannelID: "chan-seeded",
	})

	if _, err := fanOut(svc, 1, "dispatch-abc"); err != nil {
		t.Fatalf("resume: %v", err)
	}
	if len(deps.mat.runtimeCalls) != 1 {
		t.Fatalf("runtime calls = %d, want 1", len(deps.mat.runtimeCalls))
	}
	got := deps.mat.runtimeCalls[0]
	if got.InstanceID != "inst-seeded" || got.ProjectID != "proj-seeded" ||
		got.EnvID != "env-seeded" || got.ChannelID != "chan-seeded" {
		t.Fatalf("runtime step did not receive the lane's recorded identity: %+v", got)
	}
}

// A lane that already provisioned its conversation must not copy a second one on
// recovery, which is why the conversation ids are persisted rather than derived.
func TestInterruptedLaneAfterProvisioningDoesNotReprovision(t *testing.T) {
	svc, deps := newFanoutFixture(t)
	deps.lanes.seed(EnvCheckpointLane{
		ID: "l-0", CheckpointID: "cp-1", WorkspaceID: "ws",
		LaneKey: laneKeyForOrdinal("dispatch-abc", 0), Status: LaneStatusProvisioning,
		InstanceID: "inst-recovered", ProjectID: "proj-recovered", EnvID: "env-recovered",
		RuntimeID: "rt-recovered", ChannelID: "chan-recovered",
		ChatSessionID: "cs-recovered", SourceMessageID: "msg-recovered",
	})

	res, err := fanOut(svc, 1, "dispatch-abc")
	if err != nil {
		t.Fatalf("resume: %v", err)
	}
	if len(deps.mat.runtimeCalls) != 0 {
		t.Fatalf("a provisioned lane must not be provisioned again, calls = %d", len(deps.mat.runtimeCalls))
	}
	if len(deps.forked.calls) != 1 {
		t.Fatalf("the remaining step is the agent run, calls = %d", len(deps.forked.calls))
	}
	lane := deps.forked.calls[0].Lane
	if lane.ChannelID != "chan-recovered" || lane.SourceMessageID != "msg-recovered" {
		t.Fatalf("the recovered conversation must be reused: %+v", lane)
	}
	if res.Lanes[0].Status != LaneStatusReady {
		t.Fatalf("lane status = %q, want ready", res.Lanes[0].Status)
	}
}

func TestLaneWithMissingSavepointFailsTypedAndMarksSavepointFailed(t *testing.T) {
	svc, deps := newFanoutFixture(t)
	deps.mat.instanceErr = ErrSavepointGone

	res, err := fanOut(svc, 1, "dispatch-abc")
	if !errors.Is(err, ErrSavepointGone) {
		t.Fatalf("expected typed ErrSavepointGone, got %v", err)
	}
	if res.Lanes[0].Status != LaneStatusFailed {
		t.Fatalf("lane status = %q, want failed", res.Lanes[0].Status)
	}
	// Marking the savepoint failed is what makes later resumes fail fast instead
	// of each one rediscovering the same missing snapshot.
	if len(deps.savepoints.failed) != 1 || deps.savepoints.failed[0] != "snap-1" {
		t.Fatalf("savepoint must be marked failed: %v", deps.savepoints.failed)
	}
}

func TestAllLanesFailedIsReportedAsFailure(t *testing.T) {
	svc, deps := newFanoutFixture(t)
	deps.mat.instanceErr = fmt.Errorf("cube unavailable")

	res, err := fanOut(svc, 3, "dispatch-abc")
	if err == nil {
		t.Fatal("all lanes failing must surface as an error, not a partial success")
	}
	if len(res.Lanes) != 3 {
		t.Fatalf("failure must still report every lane, got %d", len(res.Lanes))
	}
	for i, lane := range res.Lanes {
		if lane.Status != LaneStatusFailed {
			t.Fatalf("lane %d status = %q, want failed", i, lane.Status)
		}
		if lane.Error == "" {
			t.Fatalf("lane %d must carry a diagnosable error", i)
		}
	}
}

// One lane's agent run failing leaves that lane's sandbox intact, so it is a
// partial result the caller can act on rather than a whole-resume failure.
func TestLaneTaskEnqueueFailureIsPerLanePartial(t *testing.T) {
	svc, deps := newFanoutFixture(t)
	deps.forked.errOnCall = map[int]error{1: fmt.Errorf("enqueue rejected")}

	res, err := fanOut(svc, 3, "dispatch-abc")
	if err != nil {
		t.Fatalf("a single trigger failure is partial, not fatal: %v", err)
	}
	want := []TriggerStatus{TriggerExecuted, TriggerFailed, TriggerExecuted}
	for i, lane := range res.Lanes {
		if lane.TriggerStatus != want[i] {
			t.Fatalf("lane %d trigger = %q, want %q", i, lane.TriggerStatus, want[i])
		}
		if lane.InstanceID == "" {
			t.Fatalf("lane %d lost its sandbox on trigger failure", i)
		}
	}
	if res.Status != ResumePartial {
		t.Fatalf("result status = %q, want partial", res.Status)
	}
}

func TestSnapshotFanOutRefusedWhenLaneSeamsMissing(t *testing.T) {
	repo := newFakeCheckpointRepo()
	putResumableCheckpoint(repo, SaveModeSnapshot, EnvCheckpointSaveComplete)
	resumer := &fakeCheckpointResumer{}
	svc := newResumeService(repo, resumer)

	if _, err := resumeOneLane(svc, "cp-1"); err == nil {
		t.Fatal("snapshot resume without the lane seams must be refused")
	}
	// Refused rather than degraded: falling back to the pause-in-place path
	// would resume instances that were never stopped.
	if len(resumer.calls) != 0 {
		t.Fatalf("a refused fan-out must not touch the sources, got %d", len(resumer.calls))
	}
}

func TestSnapshotFanOutRefusedWhenCheckpointOwnsNoSavepoint(t *testing.T) {
	svc, d := newFanoutFixture(t)
	d.savepoints.savepoints = nil

	_, err := fanOut(svc, 1, "dispatch-abc")
	if !errors.Is(err, ErrCheckpointNotResumable) {
		t.Fatalf("a savepoint-less snapshot checkpoint is permanently unresumable, got %v", err)
	}
	if len(d.mat.instanceCalls) != 0 {
		t.Fatalf("nothing may be materialized without a savepoint, got %d", len(d.mat.instanceCalls))
	}
}

// TestRetriedRequestDerivesTheSameLaneKeys pins the property the unique index
// relies on: the same anchor and ordinal always name the same lane, so a retry
// claims the lane it already owns instead of minting a second one. Distinct
// anchors must not collide, or two unrelated requests would fight over one lane.
func TestRetriedRequestDerivesTheSameLaneKeys(t *testing.T) {
	const anchor = "11111111-1111-1111-1111-111111111111"
	var first, second []string
	for i := 0; i < 3; i++ {
		first = append(first, laneKeyForOrdinal(anchor, i))
		second = append(second, laneKeyForOrdinal(anchor, i))
	}
	for i := range first {
		if first[i] != second[i] {
			t.Fatalf("lane key %d not retry-stable: %q vs %q", i, first[i], second[i])
		}
	}
	seen := map[string]bool{}
	for _, k := range first {
		if seen[k] {
			t.Fatalf("ordinals must not collide within one anchor: %q repeated", k)
		}
		seen[k] = true
	}
	if laneKeyForOrdinal("22222222-2222-2222-2222-222222222222", 0) == first[0] {
		t.Fatal("a different anchor must produce a different lane key")
	}
}

// TestBranchDispatchStillAcceptsAKeylessRequest records the deferral decided in
// plan Task 9: the anchor is documented, but branch dispatch does not yet demand
// it, because today's clients send none. When plan Task 13 makes branch dispatch
// go through checkpoint resume, this test is the one that must be inverted — and
// only after the client sends the key.
func TestBranchDispatchStillAcceptsAKeylessRequest(t *testing.T) {
	svc := NewEnvDispatchService(newFakeEnvDispatchDeps(), 1)
	in := EnvDispatchInput{
		WorkspaceID: "ws", Mode: EnvModeBranch, EnvID: "src-env",
		SourceProjectID: "src-proj", AgentID: "ag",
		Domain: EnvDomainSelfPlay, DispatchType: EnvDispatchMessage,
		GroupSize: 3, Message: &MessageInput{Content: "hi"},
		IdempotencyKey: "",
	}
	if err := svc.validate(in); err != nil {
		t.Fatalf("keyless branch dispatch must still validate until Task 13: %v", err)
	}
}

// TestSnapshotCheckpointRoundTripRunningToLanes walks the whole state machine the
// change exists to enable: a running project is captured in snapshot mode, then
// materialized into three independent lanes. The other fan-out tests start from a
// pre-seeded checkpoint, so this is the only one that proves capture and resume
// agree — resume is fed exactly the savepoints create produced, and the trip as a
// whole is asserted to cost one snapshot, not one per lane.
func TestSnapshotCheckpointRoundTripRunningToLanes(t *testing.T) {
	repo := newFakeCheckpointRepo()
	creator := &fakeSavepointCreator{}
	savepoints := &fakeSavepointReader{}
	lanes := newFakeLaneRepo()
	mat := &fakeLaneMaterializer{}
	forked := &fakeContinuationStrategy{
		mode:    SaveModeSnapshot,
		outcome: ContinuationOutcome{Status: TriggerExecuted, TaskID: "run-1"},
	}
	saver := &fakeCheckpointSaver{}
	svc := NewEnvCheckpointService(repo, saver, &fakeCheckpointResumer{},
		&fakeProjectSnapshotReader{snapshot: json.RawMessage(`{}`)},
		&fakeInFlightResolver{triggers: []ResumeTrigger{
			{TaskID: "t-1", RuntimeID: "r-1", AgentID: "a-1", ProjectID: "proj", Kind: "chat"},
		}},
		ContinuationRegistry{Forked: forked}).
		WithSavepointCreator(creator).
		WithLanes(lanes, mat, savepoints)

	cp, err := svc.Create(context.Background(), EnvCheckpointCreateInput{
		WorkspaceID: "ws", ProjectID: "proj", SaveMode: SaveModeSnapshot,
		SandboxRefs: []SandboxInstanceRef{{InstanceID: "src-1", WorkspaceID: "ws"}},
		ActorUserID: "u", SaveTimeout: 5 * time.Second,
	})
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	if cp.SaveStatus != EnvCheckpointSaveComplete {
		t.Fatalf("save status = %s, want complete", cp.SaveStatus)
	}
	// Capture must not disturb the source: the whole point of snapshot mode is
	// that the project it was taken from keeps running.
	if len(saver.calls) != 0 {
		t.Fatalf("snapshot capture must not stop the source, got %d stops", len(saver.calls))
	}

	// Resume sees the savepoints capture actually produced, not literals.
	savepoints.savepoints = creator.produced
	// Stand in for the source conversation design D8's migration records at
	// capture time. Without it fan-out is refused, which is the shipping state
	// until that migration lands; this test is about the state machine behind it.
	stored := repo.checkpoints[cp.ID]
	stored.SourceChannelID = "chan-src"
	repo.checkpoints[cp.ID] = stored

	res, err := svc.ResumeFromCheckpoint(context.Background(), ResumeFromCheckpointInput{
		WorkspaceID: "ws", CheckpointID: cp.ID, ActorUserID: "u",
		LaneCount: 3, LaneKeyAnchor: "dispatch-abc",
	})
	if err != nil {
		t.Fatalf("resume: %v", err)
	}
	if res.Status != ResumeCompleted {
		t.Fatalf("status = %s, want completed", res.Status)
	}
	if len(res.Lanes) != 3 {
		t.Fatalf("lanes = %d, want 3", len(res.Lanes))
	}
	for _, lane := range res.Lanes {
		if lane.Status != LaneStatusReady {
			t.Fatalf("lane %s status = %s (%s), want ready", lane.LaneKey, lane.Status, lane.Error)
		}
		if lane.TriggerStatus != TriggerExecuted {
			t.Fatalf("lane %s continuation = %s, want executed", lane.LaneKey, lane.TriggerStatus)
		}
	}
	if len(creator.calls) != 1 {
		t.Fatalf("the whole round trip must take exactly one snapshot, got %d", len(creator.calls))
	}
	// Every lane must have been built from that one savepoint.
	for _, c := range mat.instanceCalls {
		if c.Savepoint.CubeSnapshotID != creator.produced[0].CubeSnapshotID {
			t.Fatalf("lane built from %q, want the captured %q",
				c.Savepoint.CubeSnapshotID, creator.produced[0].CubeSnapshotID)
		}
	}
}
