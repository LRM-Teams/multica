// SPDX-License-Identifier: Apache-2.0

package service

import (
	"context"
	"errors"
	"testing"
)

// --- fakes ---

type fakeLaneMaterializerDeps struct {
	creates []CreateSandboxInstanceInput
	envs    []laneEnvCall
	copies  []laneCopyCall
	agents  []LaneAgentProvisionInput

	createErr    error
	envErr       error
	copyErr      error
	provisionErr error
}

type laneEnvCall struct {
	WorkspaceID string
	SandboxIDs  []string
	ParentEnvID string
}

type laneCopyCall struct {
	SourceProjectID string
	WorkspaceID     string
	EnvID           string
}

func (f *fakeLaneMaterializerDeps) CreateSandboxInstance(_ context.Context, in CreateSandboxInstanceInput, _ string) (SandboxInstanceRef, error) {
	f.creates = append(f.creates, in)
	if f.createErr != nil {
		return SandboxInstanceRef{}, f.createErr
	}
	return SandboxInstanceRef{
		InstanceID:  "lane-instance",
		WorkspaceID: in.WorkspaceID,
		Template:    in.Template,
		DaemonID:    "lane-daemon",
	}, nil
}

func (f *fakeLaneMaterializerDeps) CreateEnv(_ context.Context, workspaceID string, sandboxIDs []string, parentEnvID string, _ EnvMode, _ EnvDomain) (string, error) {
	f.envs = append(f.envs, laneEnvCall{workspaceID, sandboxIDs, parentEnvID})
	if f.envErr != nil {
		return "", f.envErr
	}
	return "lane-env", nil
}

func (f *fakeLaneMaterializerDeps) CopyProjectSubtree(_ context.Context, sourceProjectID, workspaceID, envID string) (string, map[string]string, map[string]string, error) {
	f.copies = append(f.copies, laneCopyCall{sourceProjectID, workspaceID, envID})
	if f.copyErr != nil {
		return "", nil, nil, f.copyErr
	}
	return "lane-project", map[string]string{}, map[string]string{}, nil
}

func (f *fakeLaneMaterializerDeps) ProvisionLaneAgentRuntime(_ context.Context, in LaneAgentProvisionInput) (LaneBinding, error) {
	f.agents = append(f.agents, in)
	if f.provisionErr != nil {
		return LaneBinding{}, f.provisionErr
	}
	return LaneBinding{
		RuntimeID:       "lane-runtime",
		DaemonID:        "lane-daemon",
		AgentID:         in.AgentID,
		ChannelID:       in.ChannelID,
		ChatSessionID:   "lane-session",
		SourceMessageID: "lane-message",
	}, nil
}

func readySavepoint() Savepoint {
	return Savepoint{
		SnapshotID:     testSnapshotUUID,
		CubeSnapshotID: "cube-tmpl-1",
		InstanceID:     testInstanceUUID,
		Status:         savepointStatusReady,
	}
}

// --- CreateLaneInstance ---

// TestLaneInstanceIsCreatedFromTheSavepointTemplate is the point of the whole
// change on this path: the lane boots from the captured state, not from the
// source's original template and not from a live clone.
func TestLaneInstanceIsCreatedFromTheSavepointTemplate(t *testing.T) {
	deps := &fakeLaneMaterializerDeps{}
	mat := NewLaneMaterializer(deps)

	ref, err := mat.CreateLaneInstance(context.Background(), LaneInstanceInput{
		WorkspaceID: testWorkspaceUUID,
		ActorUserID: testUserUUID,
		LaneKey:     "anchor#0",
		Savepoint:   readySavepoint(),
	})
	if err != nil {
		t.Fatalf("create lane instance: %v", err)
	}
	if ref.InstanceID != "lane-instance" {
		t.Fatalf("ref = %+v", ref)
	}
	if len(deps.creates) != 1 {
		t.Fatalf("creates = %d, want 1", len(deps.creates))
	}
	got := deps.creates[0]
	if got.Template != "cube-tmpl-1" {
		t.Fatalf("template = %q, want the savepoint's Cube template", got.Template)
	}
	// The lane's agent is re-engaged through its in-sandbox daemon, so a lane
	// created without one has no runtime to route the continuation to.
	if !got.DaemonEnabled {
		t.Fatal("a lane sandbox must boot its daemon")
	}
}

// TestLaneInstanceRefusesASavepointWithNoTemplate is the silent-wrong-result
// guard: creating with an empty template would succeed against the node default
// and hand back a blank sandbox that looks healthy but lost the captured state.
// It reports ErrSavepointGone so the caller marks the savepoint failed instead of
// retrying a savepoint that will never work.
func TestLaneInstanceRefusesASavepointWithNoTemplate(t *testing.T) {
	deps := &fakeLaneMaterializerDeps{}
	sp := readySavepoint()
	sp.CubeSnapshotID = ""

	_, err := NewLaneMaterializer(deps).CreateLaneInstance(context.Background(), LaneInstanceInput{
		WorkspaceID: testWorkspaceUUID, LaneKey: "anchor#0", Savepoint: sp,
	})
	if !errors.Is(err, ErrSavepointGone) {
		t.Fatalf("err = %v, want ErrSavepointGone", err)
	}
	if len(deps.creates) != 0 {
		t.Fatalf("nothing may be created from an empty template, got %+v", deps.creates)
	}
}

func TestLaneInstanceRefusesANonReadySavepoint(t *testing.T) {
	for _, status := range []string{savepointStatusCreating, savepointStatusFailed, savepointStatusDeleting} {
		deps := &fakeLaneMaterializerDeps{}
		sp := readySavepoint()
		sp.Status = status

		_, err := NewLaneMaterializer(deps).CreateLaneInstance(context.Background(), LaneInstanceInput{
			WorkspaceID: testWorkspaceUUID, LaneKey: "anchor#0", Savepoint: sp,
		})
		if !errors.Is(err, ErrSavepointGone) {
			t.Fatalf("status %q: err = %v, want ErrSavepointGone", status, err)
		}
		if len(deps.creates) != 0 {
			t.Fatalf("status %q: nothing may be created", status)
		}
	}
}

// --- CopyLaneProjectSubtree ---

// TestLaneSubtreeCopyCreatesTheEnvWithoutSandboxes mirrors what branch dispatch's
// reset phase does: the env is reserved empty and the lane's sandbox is attached
// after provisioning, so the env never points at the source's instance.
func TestLaneSubtreeCopyCreatesTheEnvWithoutSandboxes(t *testing.T) {
	deps := &fakeLaneMaterializerDeps{}

	projectID, envID, err := NewLaneMaterializer(deps).CopyLaneProjectSubtree(context.Background(), LaneProjectInput{
		WorkspaceID:     testWorkspaceUUID,
		ActorUserID:     testUserUUID,
		LaneKey:         "anchor#0",
		SourceProjectID: testProjectUUID,
	})
	if err != nil {
		t.Fatalf("copy subtree: %v", err)
	}
	if projectID != "lane-project" || envID != "lane-env" {
		t.Fatalf("project=%q env=%q", projectID, envID)
	}
	if len(deps.envs) != 1 || len(deps.envs[0].SandboxIDs) != 0 {
		t.Fatalf("lane env must be reserved with no sandboxes: %+v", deps.envs)
	}
	if len(deps.copies) != 1 || deps.copies[0].SourceProjectID != testProjectUUID || deps.copies[0].EnvID != "lane-env" {
		t.Fatalf("subtree copy = %+v, want the source project copied into the lane env", deps.copies)
	}
}

func TestLaneSubtreeCopyRequiresASourceProject(t *testing.T) {
	deps := &fakeLaneMaterializerDeps{}
	if _, _, err := NewLaneMaterializer(deps).CopyLaneProjectSubtree(context.Background(), LaneProjectInput{
		WorkspaceID: testWorkspaceUUID, LaneKey: "anchor#0",
	}); err == nil {
		t.Fatal("a subtree copy with no source project must be refused")
	}
	if len(deps.envs) != 0 {
		t.Fatalf("no env may be created for an unsatisfiable copy, got %+v", deps.envs)
	}
}

// --- ProvisionLaneAgent ---

// TestLaneAgentProvisioningActsOnThePreSeededChannel is design D6 in the small:
// the lane continues the conversation it was given rather than opening a new one.
func TestLaneAgentProvisioningActsOnThePreSeededChannel(t *testing.T) {
	deps := &fakeLaneMaterializerDeps{}

	binding, err := NewLaneMaterializer(deps).ProvisionLaneAgent(context.Background(), LaneRuntimeInput{
		WorkspaceID: testWorkspaceUUID,
		ActorUserID: testUserUUID,
		LaneKey:     "anchor#0",
		AgentID:     "agent-1",
		InstanceID:  "lane-instance",
		ProjectID:   "lane-project",
		EnvID:       "lane-env",
		ChannelID:   "chan-copied",
	})
	if err != nil {
		t.Fatalf("provision lane agent: %v", err)
	}
	if binding.ChannelID != "chan-copied" {
		t.Fatalf("binding channel = %q, want the pre-seeded channel", binding.ChannelID)
	}
	if binding.RuntimeID != "lane-runtime" || binding.ChatSessionID != "lane-session" {
		t.Fatalf("binding = %+v", binding)
	}
	if len(deps.agents) != 1 {
		t.Fatalf("provision calls = %d, want 1", len(deps.agents))
	}
	got := deps.agents[0]
	if got.ChannelID != "chan-copied" || got.InstanceID != "lane-instance" ||
		got.ProjectID != "lane-project" || got.EnvID != "lane-env" || got.AgentID != "agent-1" {
		t.Fatalf("provision input = %+v", got)
	}
}

// TestLaneAgentProvisioningRefusesAChannellessLane holds design D8's boundary. A
// lane with no channel could only be served by reusing the source's, which would
// make lanes share one conversation -- the exact opposite of independent
// continuations. Minting the lane its own channel needs the source conversation
// the checkpoint does not yet record, so this is refused until it does.
func TestLaneAgentProvisioningRefusesAChannellessLane(t *testing.T) {
	deps := &fakeLaneMaterializerDeps{}

	_, err := NewLaneMaterializer(deps).ProvisionLaneAgent(context.Background(), LaneRuntimeInput{
		WorkspaceID: testWorkspaceUUID, LaneKey: "anchor#0", AgentID: "agent-1",
		InstanceID: "lane-instance", ProjectID: "lane-project", EnvID: "lane-env",
	})
	if !errors.Is(err, ErrLaneConversationUnavailable) {
		t.Fatalf("err = %v, want ErrLaneConversationUnavailable", err)
	}
	if len(deps.agents) != 0 {
		t.Fatalf("nothing may be provisioned without a conversation, got %+v", deps.agents)
	}
}

func TestLaneAgentProvisioningRequiresItsSandboxAndAgent(t *testing.T) {
	base := LaneRuntimeInput{
		WorkspaceID: testWorkspaceUUID, LaneKey: "anchor#0", AgentID: "agent-1",
		InstanceID: "lane-instance", ProjectID: "lane-project", EnvID: "lane-env",
		ChannelID: "chan-copied",
	}
	for name, mutate := range map[string]func(*LaneRuntimeInput){
		"no sandbox": func(in *LaneRuntimeInput) { in.InstanceID = "" },
		"no agent":   func(in *LaneRuntimeInput) { in.AgentID = "" },
		"no project": func(in *LaneRuntimeInput) { in.ProjectID = "" },
	} {
		in := base
		mutate(&in)
		deps := &fakeLaneMaterializerDeps{}
		if _, err := NewLaneMaterializer(deps).ProvisionLaneAgent(context.Background(), in); err == nil {
			t.Fatalf("%s: must be refused", name)
		}
		if len(deps.agents) != 0 {
			t.Fatalf("%s: nothing may be provisioned", name)
		}
	}
}

func TestLaneMaterializerPropagatesDepErrors(t *testing.T) {
	sentinel := errors.New("boom")
	mat := NewLaneMaterializer(&fakeLaneMaterializerDeps{
		createErr: sentinel, envErr: sentinel, provisionErr: sentinel,
	})
	ctx := context.Background()

	if _, err := mat.CreateLaneInstance(ctx, LaneInstanceInput{
		WorkspaceID: testWorkspaceUUID, Savepoint: readySavepoint(),
	}); !errors.Is(err, sentinel) {
		t.Fatalf("create: err = %v", err)
	}
	if _, _, err := mat.CopyLaneProjectSubtree(ctx, LaneProjectInput{
		WorkspaceID: testWorkspaceUUID, SourceProjectID: testProjectUUID,
	}); !errors.Is(err, sentinel) {
		t.Fatalf("copy: err = %v", err)
	}
	if _, err := mat.ProvisionLaneAgent(ctx, LaneRuntimeInput{
		WorkspaceID: testWorkspaceUUID, AgentID: "a", InstanceID: "i",
		ProjectID: "p", EnvID: "e", ChannelID: "c",
	}); !errors.Is(err, sentinel) {
		t.Fatalf("provision: err = %v", err)
	}
}
