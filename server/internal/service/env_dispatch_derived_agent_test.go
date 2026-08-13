package service

import (
	"context"
	"errors"
	"strings"
	"testing"
)

type fakeCloneDeps struct {
	source     SourceAgent
	loadErr    error
	createErr  error
	copyErr    error
	replaceErr error
	setErr     error

	created    CreateDerivedAgentInput
	copied     bool
	replaced   bool
	setBinding string
}

func (f *fakeCloneDeps) LoadSourceAgent(_ context.Context, _, _ string) (SourceAgent, error) {
	if f.loadErr != nil {
		return SourceAgent{}, f.loadErr
	}
	return f.source, nil
}
func (f *fakeCloneDeps) CreateDerivedAgent(_ context.Context, in CreateDerivedAgentInput) (string, error) {
	if f.createErr != nil {
		return "", f.createErr
	}
	f.created = in
	return "derived-1", nil
}
func (f *fakeCloneDeps) CopyApprovedSkills(_ context.Context, _, _, _ string) error {
	if f.copyErr != nil {
		return f.copyErr
	}
	f.copied = true
	return nil
}
func (f *fakeCloneDeps) ReplaceDispatchChannelMember(_ context.Context, _, _, _ string) error {
	if f.replaceErr != nil {
		return f.replaceErr
	}
	f.replaced = true
	return nil
}
func (f *fakeCloneDeps) SetBindingDerivedAgent(_ context.Context, _, derivedID string) error {
	if f.setErr != nil {
		return f.setErr
	}
	f.setBinding = derivedID
	return nil
}

func cloneInput() CloneEnvDispatchAgentInput {
	return CloneEnvDispatchAgentInput{
		WorkspaceID: "ws-1", SourceAgentID: "src-1", RuntimeID: "rt-1",
		EnvID: "env-1", ChannelID: "ch-1", BindingID: "bind-1",
		ExecutionModel: "env-leader-1/glm-5.2",
	}
}

// TestCloneEnvDispatchAgentCreatesDerivedWithLineageAndRuntime verifies the
// happy path: the derived agent is created with source_agent_id lineage and the
// discovered runtime binding, approved config is copied, the dispatch channel
// member is replaced, and the binding is updated. The source is never mutated -
// CloneDeps exposes no source-mutating method, so source immutability is
// structural (the real-DB assertion that source.RuntimeID is unchanged lands
// with the adapter wiring / CI).
func TestCloneEnvDispatchAgentCreatesDerivedWithLineageAndRuntime(t *testing.T) {
	deps := &fakeCloneDeps{source: SourceAgent{
		WorkspaceID: "ws-1", ID: "src-1", Name: "agent-a",
		Instructions: "do thing", ApprovedConfig: []byte(`{"pi":"visible"}`),
	}}
	derivedID, err := CloneEnvDispatchAgent(context.Background(), deps, cloneInput())
	if err != nil {
		t.Fatalf("want nil error, got %v", err)
	}
	if derivedID != "derived-1" {
		t.Fatalf("derivedID = %q, want derived-1", derivedID)
	}
	if deps.created.SourceAgentID != "src-1" {
		t.Fatalf("derived SourceAgentID = %q, want src-1", deps.created.SourceAgentID)
	}
	if deps.created.ExecutionModel != "env-leader-1/glm-5.2" {
		t.Fatalf("derived ExecutionModel = %q, want env-leader-1/glm-5.2", deps.created.ExecutionModel)
	}
	if deps.created.RuntimeID != "rt-1" {
		t.Fatalf("derived RuntimeID = %q, want rt-1", deps.created.RuntimeID)
	}
	if deps.created.Name != "env-bind-1" || deps.created.Instructions != "do thing" {
		t.Fatalf("derived did not copy approved config: %+v", deps.created)
	}
	if string(deps.created.ApprovedConfig) != `{"pi":"visible"}` {
		t.Fatalf("derived did not copy approved config blob: %s", deps.created.ApprovedConfig)
	}
	if !deps.copied || !deps.replaced || deps.setBinding != "derived-1" {
		t.Fatalf("clone did not complete all steps: copied=%v replaced=%v setBinding=%q", deps.copied, deps.replaced, deps.setBinding)
	}
}

func TestCloneEnvDispatchAgentRejectsSourceWorkspaceMismatch(t *testing.T) {
	deps := &fakeCloneDeps{source: SourceAgent{WorkspaceID: "ws-other", ID: "src-1"}}
	_, err := CloneEnvDispatchAgent(context.Background(), deps, cloneInput())
	if err == nil || !strings.Contains(err.Error(), "workspace mismatch") {
		t.Fatalf("want workspace mismatch error, got %v", err)
	}
	if deps.created.SourceAgentID != "" {
		t.Fatalf("derived agent must not be created on workspace mismatch")
	}
	if deps.copied || deps.replaced {
		t.Fatalf("no downstream steps may run on workspace mismatch")
	}
}

func TestCloneEnvDispatchAgentRejectsMissingInput(t *testing.T) {
	deps := &fakeCloneDeps{source: SourceAgent{WorkspaceID: "ws-1"}}
	_, err := CloneEnvDispatchAgent(context.Background(), deps, CloneEnvDispatchAgentInput{WorkspaceID: "ws-1"})
	if err == nil || !strings.Contains(err.Error(), "validation_failed") {
		t.Fatalf("want validation_failed error, got %v", err)
	}
}

func TestCloneEnvDispatchAgentSurfacesCreateError(t *testing.T) {
	deps := &fakeCloneDeps{source: SourceAgent{WorkspaceID: "ws-1"}, createErr: errors.New("db down")}
	_, err := CloneEnvDispatchAgent(context.Background(), deps, cloneInput())
	if err == nil || !strings.Contains(err.Error(), "create derived agent") || !strings.Contains(err.Error(), "db down") {
		t.Fatalf("want wrapped create error, got %v", err)
	}
	if deps.copied || deps.replaced {
		t.Fatalf("downstream steps must not run after create failure")
	}
}

func TestCloneEnvDispatchAgentSurfacesCopyError(t *testing.T) {
	deps := &fakeCloneDeps{source: SourceAgent{WorkspaceID: "ws-1"}, copyErr: errors.New("skills copy failed")}
	_, err := CloneEnvDispatchAgent(context.Background(), deps, cloneInput())
	if err == nil || !strings.Contains(err.Error(), "copy approved skills") {
		t.Fatalf("want wrapped copy error, got %v", err)
	}
	if deps.replaced {
		t.Fatalf("channel member must not be replaced after skills copy failure")
	}
}

func validMixedProvisionPlan() MixedDispatchPlan {
	return MixedDispatchPlan{
		OnlineTrainableAgents:  []string{"source-online"},
		OfflineTrainableAgents: []string{"source-offline"},
		TargetPolicy:           "target-a",
		Tokenizer:              "tokenizer-a",
		RunAgents: []MixedDispatchRunAgent{
			{SourceAgentID: "source-online", TrainingMode: "online_rl"},
			{SourceAgentID: "source-offline", TrainingMode: "offline_rl"},
			{SourceAgentID: "source-none", TrainingMode: "none"},
		},
	}
}

func validMixedProvisionedAgents() []MixedDispatchRunAgent {
	return []MixedDispatchRunAgent{
		{SourceAgentID: "source-online", ExecutionAgentID: "derived-online", RuntimeID: "runtime-online", PiSessionID: "pi-online", AReALSessionID: "areal-online", TrainingMode: "online_rl"},
		{SourceAgentID: "source-offline", ExecutionAgentID: "derived-offline", RuntimeID: "runtime-offline", PiSessionID: "pi-offline", TrainingMode: "offline_rl"},
		{SourceAgentID: "source-none", ExecutionAgentID: "derived-none", RuntimeID: "runtime-none", PiSessionID: "pi-none", TrainingMode: "none"},
	}
}

func TestValidateMixedDispatchProvisionedAgentsAcceptsReadyFreshDerivedBindings(t *testing.T) {
	if err := ValidateMixedDispatchProvisionedAgents(validMixedProvisionPlan(), validMixedProvisionedAgents()); err != nil {
		t.Fatalf("valid provisioned run agents rejected: %v", err)
	}
}

func TestValidateMixedDispatchProvisionedAgentsRejectsIncompletePreSendState(t *testing.T) {
	cases := map[string]func([]MixedDispatchRunAgent){
		"missing derived binding":             func(agents []MixedDispatchRunAgent) { agents[0].ExecutionAgentID = "" },
		"source reused as execution identity": func(agents []MixedDispatchRunAgent) { agents[0].ExecutionAgentID = agents[0].SourceAgentID },
		"runtime not ready":                   func(agents []MixedDispatchRunAgent) { agents[0].RuntimeID = "" },
		"missing Pi session":                  func(agents []MixedDispatchRunAgent) { agents[0].PiSessionID = "" },
		"Pi session reused":                   func(agents []MixedDispatchRunAgent) { agents[1].PiSessionID = agents[0].PiSessionID },
		"online provider session missing":     func(agents []MixedDispatchRunAgent) { agents[0].AReALSessionID = "" },
		"unexpected none provider session":    func(agents []MixedDispatchRunAgent) { agents[2].AReALSessionID = "unexpected" },
		"classification drift":                func(agents []MixedDispatchRunAgent) { agents[1].TrainingMode = "none" },
		"source binding drift":                func(agents []MixedDispatchRunAgent) { agents[1].SourceAgentID = "other-source" },
	}
	for name, mutate := range cases {
		t.Run(name, func(t *testing.T) {
			agents := validMixedProvisionedAgents()
			mutate(agents)
			if err := ValidateMixedDispatchProvisionedAgents(validMixedProvisionPlan(), agents); err == nil {
				t.Fatal("invalid pre-send state unexpectedly accepted")
			}
		})
	}
}

func TestValidateMixedDispatchProvisionedAgentsRejectsUnsupportedTrainingMode(t *testing.T) {
	plan := validMixedProvisionPlan()
	plan.RunAgents[2].TrainingMode = "legacy"
	agents := validMixedProvisionedAgents()
	agents[2].TrainingMode = "legacy"
	if err := ValidateMixedDispatchProvisionedAgents(plan, agents); err == nil || !strings.Contains(err.Error(), "training mode") {
		t.Fatalf("unsupported training mode error = %v, want field-specific rejection", err)
	}
}
