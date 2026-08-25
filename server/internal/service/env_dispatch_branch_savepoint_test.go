// SPDX-License-Identifier: Apache-2.0

package service

import (
	"context"
	"errors"
	"sync"
	"testing"
)

type fakeBranchSavepointProvider struct {
	mu sync.Mutex

	ensureCalls []BranchSavepointInput
	claimCalls  []BranchLaneInput
	settleCalls []BranchLaneSettleInput

	savepoint BranchSavepoint
	ensureErr error
	claimErr  error
	settleErr error
}

func newFakeBranchSavepointProvider() *fakeBranchSavepointProvider {
	return &fakeBranchSavepointProvider{
		savepoint: BranchSavepoint{
			CheckpointID: "cp-branch-1", SnapshotID: "snap-branch-1", Template: "cube-branch-1",
		},
	}
}

func (f *fakeBranchSavepointProvider) EnsureBranchSavepoint(_ context.Context, in BranchSavepointInput) (BranchSavepoint, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.ensureCalls = append(f.ensureCalls, in)
	if f.ensureErr != nil {
		return BranchSavepoint{}, f.ensureErr
	}
	return f.savepoint, nil
}

func (f *fakeBranchSavepointProvider) ClaimBranchLane(_ context.Context, in BranchLaneInput) (BranchLane, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.claimCalls = append(f.claimCalls, in)
	if f.claimErr != nil {
		return BranchLane{}, f.claimErr
	}
	return BranchLane{LaneID: "lane-" + in.LaneKey, LaneKey: in.LaneKey}, nil
}

func (f *fakeBranchSavepointProvider) SettleBranchLane(_ context.Context, in BranchLaneSettleInput) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.settleCalls = append(f.settleCalls, in)
	return f.settleErr
}

// newBranchSavepointFixture builds the branch+message shape every case here
// starts from: a source env with one sandbox, a squad roster whose leader is the
// trigger, and a savepoint provider installed.
func newBranchSavepointFixture() (*fakeEnvDispatchDeps, *fakeBranchSavepointProvider, EnvDispatchInput) {
	f := newFakeEnvDispatchDeps()
	const stateEnv = "state-env-1"
	f.envs[stateEnv] = Env{ID: stateEnv, SandboxIDs: []string{"state-sbx"}, Mode: EnvModeBranch, Domain: EnvDomainSelfPlay}
	f.projects["source-proj-1"] = stateEnv
	f.chatSess["source-sess-1"] = "source-proj-1"
	f.messageRoster = MessageRoster{LeaderID: "leader", AgentIDs: []string{"leader"}}
	f.branchTrigger = EnvCollaborationTrigger{
		AgentID: "leader", Kind: "channel_message", ChannelID: "source-channel",
		ProjectID: "source-proj-1", ChatSessionID: "source-sess-1",
		SourceMessageID: "source-msg-1", TaskID: "source-task-1", RuntimeID: "source-runtime-1",
	}
	f.branchSourceSandbox = "source-sandbox-leader"

	return f, newFakeBranchSavepointProvider(), EnvDispatchInput{
		WorkspaceID: "ws", UserID: "u", Mode: EnvModeBranch, EnvID: stateEnv,
		SourceProjectID: "source-proj-1", IdempotencyKey: "dispatch-abc",
		Domain: EnvDomainSelfPlay, DispatchType: EnvDispatchMessage, GroupSize: 1,
		AgentID: "leader", Message: &MessageInput{Content: "continue"},
	}
}

// TestBranchRolloutBootsFromTheSavepointNotAClone is the point of the reroute: a
// branch rollout starts from captured source state, so provisioning is handed a
// savepoint template instead of being told to clone a running sandbox.
func TestBranchRolloutBootsFromTheSavepointNotAClone(t *testing.T) {
	f, provider, in := newBranchSavepointFixture()

	svc := NewEnvDispatchService(f, 1).WithBranchSavepoints(provider)
	if _, err := svc.Dispatch(context.Background(), in); err != nil {
		t.Fatalf("dispatch: %v", err)
	}
	if len(f.provisionCalls) != 1 {
		t.Fatalf("provision calls = %d, want 1", len(f.provisionCalls))
	}
	got := f.provisionCalls[0]
	if got.SavepointTemplate != "cube-branch-1" {
		t.Fatalf("provisioning must boot from the savepoint template, got %q", got.SavepointTemplate)
	}
	// The source instance still travels for the binding record; what changed is
	// that it is no longer the thing the new sandbox is copied from.
	if got.SourceSandboxInstanceID != "source-sandbox-leader" {
		t.Fatalf("source sandbox = %q, want it still recorded", got.SourceSandboxInstanceID)
	}
}

// TestBranchDispatchCapturesTheSourceOnceForTheWholeGroup guards the cost that
// would otherwise scale with group size: rollouts are reset and dispatched
// concurrently, so capturing per rollout would snapshot the same source N times
// and race on creating the checkpoint that owns them.
func TestBranchDispatchCapturesTheSourceOnceForTheWholeGroup(t *testing.T) {
	f, provider, in := newBranchSavepointFixture()
	in.GroupSize = 4

	svc := NewEnvDispatchService(f, 8).WithBranchSavepoints(provider)
	res, err := svc.Dispatch(context.Background(), in)
	if err != nil {
		t.Fatalf("dispatch: %v", err)
	}
	if len(res.Rollouts) != 4 {
		t.Fatalf("rollouts = %d, want 4", len(res.Rollouts))
	}
	if len(provider.ensureCalls) != 1 {
		t.Fatalf("savepoint captures = %d, want exactly 1 for the group", len(provider.ensureCalls))
	}
	if provider.ensureCalls[0].SourceEnvID != in.EnvID ||
		provider.ensureCalls[0].SourceInstanceID != "source-sandbox-leader" {
		t.Fatalf("capture input = %+v", provider.ensureCalls[0])
	}
	// The source channel travels so the provider can also capture the peers whose
	// bindings the channel copy carried over. Their provisioning runs later on the
	// mention path, which has no time to snapshot anything.
	if provider.ensureCalls[0].SourceChannelID != "source-channel" {
		t.Fatalf("capture must name the source channel, got %q", provider.ensureCalls[0].SourceChannelID)
	}
	// Every rollout boots from that one savepoint.
	for i, call := range f.provisionCalls {
		if call.SavepointTemplate != "cube-branch-1" {
			t.Fatalf("rollout %d template = %q", i, call.SavepointTemplate)
		}
	}
}

// TestBranchLaneIsPreSeededWithTheRolloutsOwnRows is design D6: the reset phase
// owns the env, project and copied channel, so the lane records them rather than
// creating a second set. A lane that minted its own would leave the copied
// conversation -- the entire point of branch+message -- attached to an abandoned
// project.
func TestBranchLaneIsPreSeededWithTheRolloutsOwnRows(t *testing.T) {
	f, provider, in := newBranchSavepointFixture()

	svc := NewEnvDispatchService(f, 1).WithBranchSavepoints(provider)
	res, err := svc.Dispatch(context.Background(), in)
	if err != nil {
		t.Fatalf("dispatch: %v", err)
	}
	r := res.Rollouts[0]
	if len(provider.claimCalls) != 1 {
		t.Fatalf("lane claims = %d, want 1", len(provider.claimCalls))
	}
	claim := provider.claimCalls[0]
	if claim.CheckpointID != "cp-branch-1" {
		t.Fatalf("lane must be claimed on the capturing checkpoint, got %q", claim.CheckpointID)
	}
	if claim.EnvID != r.EnvID || claim.ProjectID != r.ProjectID || claim.ChannelID != r.ChannelID {
		t.Fatalf("lane claim = %+v, want the rollout's own env/project/channel (%s/%s/%s)",
			claim, r.EnvID, r.ProjectID, r.ChannelID)
	}
	if claim.LaneKey == "" || claim.LaneKey == claim.EnvID {
		t.Fatalf("lane key = %q, want a key derived from the dispatch anchor", claim.LaneKey)
	}
}

// A lane's recorded ids are what savepoint reclamation reads to know the
// savepoint is still in use, so provisioning's results have to land on the row.
func TestBranchLaneIsSettledWithWhatProvisioningProduced(t *testing.T) {
	f, provider, in := newBranchSavepointFixture()

	svc := NewEnvDispatchService(f, 1).WithBranchSavepoints(provider)
	res, err := svc.Dispatch(context.Background(), in)
	if err != nil {
		t.Fatalf("dispatch: %v", err)
	}
	r := res.Rollouts[0]
	if len(provider.settleCalls) != 1 {
		t.Fatalf("lane settles = %d, want 1", len(provider.settleCalls))
	}
	settle := provider.settleCalls[0]
	if settle.LaneID != "lane-"+provider.claimCalls[0].LaneKey {
		t.Fatalf("settle must target the claimed lane, got %q", settle.LaneID)
	}
	if settle.Status != LaneStatusReady {
		t.Fatalf("settle status = %q, want ready", settle.Status)
	}
	if settle.InstanceID == "" || settle.RuntimeID == "" || settle.ChatSessionID != r.ChatSessionID {
		t.Fatalf("settle = %+v, want the provisioned ids", settle)
	}
}

// TestBranchLaneIsFailedWhenProvisioningFails keeps a dead lane from pinning its
// savepoint forever: reclamation refuses to release a savepoint whose lanes are
// still provisioning, so a lane whose rollout failed has to be recorded failed.
func TestBranchLaneIsFailedWhenProvisioningFails(t *testing.T) {
	f, provider, in := newBranchSavepointFixture()
	f.provisionErr = errors.New("sandbox create refused")

	svc := NewEnvDispatchService(f, 1).WithBranchSavepoints(provider)
	res, err := svc.Dispatch(context.Background(), in)
	// The group is size 1, so its single failure is every rollout failing.
	if !errors.Is(err, ErrAllDispatchFailed) {
		t.Fatalf("err = %v, want ErrAllDispatchFailed", err)
	}
	if res.Rollouts[0].Error == "" {
		t.Fatal("rollout must carry the provisioning error")
	}
	if len(provider.settleCalls) != 1 || provider.settleCalls[0].Status != LaneStatusFailed {
		t.Fatalf("lane must be settled failed, got %+v", provider.settleCalls)
	}
}

// TestBranchCaptureFailureStopsBeforeAnyRolloutIsBuilt is fail-fast: capture is
// the one step that can fail for the whole group at once, so it runs before the
// reset fan-out. Failing after would leave N envs, projects and copied channels
// to roll back for a dispatch that could never have proceeded.
func TestBranchCaptureFailureStopsBeforeAnyRolloutIsBuilt(t *testing.T) {
	f, provider, in := newBranchSavepointFixture()
	in.GroupSize = 3
	provider.ensureErr = errors.New("snapshot failed")

	envsBefore := len(f.envs)

	svc := NewEnvDispatchService(f, 8).WithBranchSavepoints(provider)
	_, err := svc.Dispatch(context.Background(), in)
	if err == nil {
		t.Fatal("dispatch must fail when the source cannot be captured")
	}
	if len(f.envs) != envsBefore {
		t.Fatalf("no env may be created for an uncapturable source, envs went %d -> %d",
			envsBefore, len(f.envs))
	}
	if len(f.provisionCalls) != 0 {
		t.Fatalf("nothing may be provisioned, got %+v", f.provisionCalls)
	}
}

// TestBranchDispatchRefusedWithoutASavepointProvider is the fail-closed guard for
// the rollout window. The clone job it replaces is being removed, so a server
// without the provider installed must refuse rather than silently fall back to a
// path that no longer exists.
func TestBranchDispatchRefusedWithoutASavepointProvider(t *testing.T) {
	f, _, in := newBranchSavepointFixture()

	svc := NewEnvDispatchService(f, 1)
	if _, err := svc.Dispatch(context.Background(), in); err == nil {
		t.Fatal("branch dispatch without a savepoint provider must be refused")
	}
	if len(f.provisionCalls) != 0 {
		t.Fatalf("nothing may be provisioned, got %+v", f.provisionCalls)
	}
}

// A branch whose trigger has no source sandbox instance has no filesystem to
// capture -- it provisions from scratch, exactly as before -- so capture must not
// run and no template may be passed.
func TestBranchWithNoSourceInstanceCapturesNothing(t *testing.T) {
	f, provider, in := newBranchSavepointFixture()
	f.branchSourceSandbox = ""

	svc := NewEnvDispatchService(f, 1).WithBranchSavepoints(provider)
	if _, err := svc.Dispatch(context.Background(), in); err != nil {
		t.Fatalf("dispatch: %v", err)
	}
	if len(provider.ensureCalls) != 0 {
		t.Fatalf("nothing to capture, yet capture ran: %+v", provider.ensureCalls)
	}
	if len(provider.claimCalls) != 0 {
		t.Fatalf("no savepoint means no lane, got %+v", provider.claimCalls)
	}
	if len(f.provisionCalls) != 1 || f.provisionCalls[0].SavepointTemplate != "" {
		t.Fatalf("provisioning must get no template, got %+v", f.provisionCalls)
	}
}
