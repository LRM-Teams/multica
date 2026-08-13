package service

import (
	"context"
	"errors"
	"fmt"
	"reflect"
	"strings"
	"testing"
	"time"
)

type mixedBranchPreflightDeps struct {
	*fakeEnvDispatchDeps
	failStage              string
	events                 []string
	acceptedMessages       int
	agentTurns            int
	startAttempts          int
	preparedAgents         []string
	revokedAgents          []string
	localTargetCalls       int
	provisionAttempts      int
	openedAReALSessions    []string
	reclaimedAReALSessions []string
	reclaimFailures        map[string]error
	reclaimAttempts        []string
}

func newMixedBranchPreflightDeps() *mixedBranchPreflightDeps {
	base := newFakeEnvDispatchDeps()
	const stateEnv = "mixed-branch-state-env"
	base.envs[stateEnv] = Env{ID: stateEnv, SandboxIDs: []string{"state-sandbox"}, Mode: EnvModeBranch, Domain: EnvDomainSelfPlay}
	base.projects["mixed-branch-source-project"] = stateEnv
	base.chatSess["mixed-branch-source-session"] = "mixed-branch-source-project"
	base.messageRoster = MessageRoster{LeaderID: "agent-online", AgentIDs: []string{"agent-online", "agent-offline"}}
	base.mixedRoster = []MixedDispatchRosterAgent{
		{SourceAgentID: "agent-online", Provider: "pi", TargetPolicy: "target-a", Tokenizer: "tokenizer-a", OnlineReady: true},
		{SourceAgentID: "agent-offline", Provider: "pi", TargetPolicy: "target-a", Tokenizer: "tokenizer-a", OfflineReady: true},
	}
	base.branchTrigger = EnvCollaborationTrigger{
		AgentID: "agent-online", Kind: "channel_message", ChannelID: "source-channel",
		ProjectID: "mixed-branch-source-project", ChatSessionID: "mixed-branch-source-session",
		SourceMessageID: "source-message", TaskID: "source-task", RuntimeID: "source-runtime",
	}
	base.branchSourceSandbox = "source-sandbox-online"
	return &mixedBranchPreflightDeps{fakeEnvDispatchDeps: base}
}

func (f *mixedBranchPreflightDeps) ProvisionEnvDispatchAgent(ctx context.Context, in EnvDispatchAgentProvisionInput) (EnvDispatchAgentProvisionResult, error) {
	f.provisionAttempts++
	f.events = append(f.events, "provision:"+in.AgentID)
	if f.failStage == "provision" && in.AgentID == "agent-offline" {
		return EnvDispatchAgentProvisionResult{}, errors.New("synthetic later provisioning failure")
	}
	if f.failStage == "sibling-provision" && f.provisionAttempts == 4 {
		return EnvDispatchAgentProvisionResult{}, errors.New("synthetic sibling provisioning failure")
	}
	runtimeID, daemonID, err := f.fakeEnvDispatchDeps.PrecreateAgentRuntime(ctx, in.WorkspaceID, in.UserID, in.AgentID)
	if err != nil {
		return EnvDispatchAgentProvisionResult{}, err
	}
	arealSessionID := ""
	if in.TrainingMode == "online_rl" {
		arealSessionID = fmt.Sprintf("areal-session-%s-%s", in.EnvID, in.AgentID)
		f.openedAReALSessions = append(f.openedAReALSessions, arealSessionID)
	}
	return EnvDispatchAgentProvisionResult{
		AgentID:           "derived-" + in.AgentID,
		SandboxInstanceID: "sandbox-" + in.AgentID,
		RuntimeID:         runtimeID,
		DaemonID:          daemonID,
		ChatSessionID:     "pi-session-" + in.AgentID,
		AReALSessionID:    arealSessionID,
	}, nil
}

func (f *mixedBranchPreflightDeps) PrepareMixedDispatchRunAgent(_ context.Context, runID string, runAgent MixedDispatchRunAgent) (MixedDispatchRunAgent, error) {
	f.events = append(f.events, "prepare:"+runAgent.SourceAgentID)
	if f.failStage == "prepare" && runAgent.SourceAgentID == "agent-offline" {
		return MixedDispatchRunAgent{}, errors.New("synthetic native Pi start failure")
	}
	runAgent.RunAgentID = "run-agent-" + runID + "-" + runAgent.SourceAgentID
	runAgent.PiSessionID = "native-pi-session-" + runID + "-" + runAgent.SourceAgentID
	runAgent.CaptureBoundary = runAgent.PiSessionID + ":1"
	f.preparedAgents = append(f.preparedAgents, runAgent.SourceAgentID)
	return runAgent, nil
}

func (f *mixedBranchPreflightDeps) RevokeMixedDispatchRunAgent(_ context.Context, _ string, runAgent MixedDispatchRunAgent) error {
	f.revokedAgents = append(f.revokedAgents, runAgent.SourceAgentID)
	return nil
}

func (f *mixedBranchPreflightDeps) BindMixedDispatchRunAgent(_ context.Context, _ string, agent MixedDispatchRunAgent) error {
	f.events = append(f.events, "bind:"+agent.SourceAgentID)
	if agent.RunAgentID == "" || agent.PiSessionID == "" || agent.CaptureBoundary == "" || strings.HasPrefix(agent.PiSessionID, "pi-session-") {
		return errors.New("synthetic incomplete or provisional Pi binding")
	}
	if f.failStage == "bind" && agent.SourceAgentID == "agent-offline" {
		return errors.New("synthetic bind failure")
	}
	f.mixedRunAgents = append(f.mixedRunAgents, agent)
	return nil
}

func (f *mixedBranchPreflightDeps) CreateChannelMessage(_ context.Context, _, _, _, _ string) (string, error) {
	f.events = append(f.events, "send")
	if f.failStage == "send" {
		return "", errors.New("synthetic canonical send failure")
	}
	f.acceptedMessages++
	return fmt.Sprintf("accepted-message-%d", f.acceptedMessages), nil
}

func (f *mixedBranchPreflightDeps) PersistMixedDispatchInitialMessage(_ context.Context, _, _, _, _ string) (PreparedMixedDispatchMessage, error) {
	f.events = append(f.events, "persist")
	if f.failStage == "send" {
		return PreparedMixedDispatchMessage{}, errors.New("synthetic canonical send failure")
	}
	return NewPreparedMixedDispatchMessage("persisted-message", time.Now().UTC(), func(context.Context) {
		f.events = append(f.events, "deliver")
		f.acceptedMessages++
		f.agentTurns++
	}), nil
}

func (f *mixedBranchPreflightDeps) StartMixedDispatchRun(_ context.Context, runID string, _ time.Time) error {
	f.events = append(f.events, "start")
	f.startAttempts++
	if f.failStage == "start" || (f.failStage == "sibling-start" && f.startAttempts == 2) {
		return errors.New("synthetic timeout-start failure")
	}
	f.mixedStartedRuns = append(f.mixedStartedRuns, runID)
	return nil
}

func (f *mixedBranchPreflightDeps) SetEnvDispatchRunLocalTargets(ctx context.Context, projectID, workspaceID, localIssueID, localChannelID string) error {
	f.localTargetCalls++
	if f.failStage == "post-dispatch-local-target" && f.localTargetCalls == 2 {
		return errors.New("synthetic post-dispatch local-target failure")
	}
	return f.fakeEnvDispatchDeps.SetEnvDispatchRunLocalTargets(ctx, projectID, workspaceID, localIssueID, localChannelID)
}

func (f *mixedBranchPreflightDeps) ReclaimMixedDispatchProvision(ctx context.Context, workspaceID, _, _, _ string, provisioned EnvDispatchAgentProvisionResult) error {
	f.reclaimAttempts = append(f.reclaimAttempts, provisioned.RuntimeID)
	var cleanupErrs []error
	if provisioned.AReALSessionID != "" {
		f.reclaimedAReALSessions = append(f.reclaimedAReALSessions, provisioned.AReALSessionID)
	}
	if err := f.reclaimFailures[provisioned.RuntimeID]; err != nil {
		cleanupErrs = append(cleanupErrs, err)
	}
	if provisioned.RuntimeID != "" {
		if err := f.fakeEnvDispatchDeps.DeleteAgentRuntime(ctx, workspaceID, provisioned.RuntimeID); err != nil {
			cleanupErrs = append(cleanupErrs, err)
		}
	}
	return errors.Join(cleanupErrs...)
}

func mixedBranchDispatchInput() EnvDispatchInput {
	return EnvDispatchInput{
		WorkspaceID: "ws", UserID: "user", Mode: EnvModeBranch, EnvID: "mixed-branch-state-env",
		SourceProjectID: "mixed-branch-source-project", Domain: EnvDomainSelfPlay,
		DispatchType: EnvDispatchMessage, GroupSize: 1, AgentID: "agent-online",
		OnlineTrainableAgents: []string{"agent-online"}, OfflineTrainableAgents: []string{"agent-offline"},
		QuietWindowMS: 2000, TotalTimeoutSeconds: 3300,
		Message: &MessageInput{Content: "continue mixed branch"},
	}
}

func TestDispatchMixedBranchCompletesPreflightBeforeCanonicalSend(t *testing.T) {
	deps := newMixedBranchPreflightDeps()
	result, err := NewEnvDispatchService(deps, 1).Dispatch(context.Background(), mixedBranchDispatchInput())
	if err != nil {
		t.Fatalf("mixed branch dispatch: %v", err)
	}
	wantEvents := []string{
		"provision:agent-online", "provision:agent-offline",
		"prepare:agent-online", "bind:agent-online", "prepare:agent-offline", "bind:agent-offline",
		"persist", "start", "deliver",
	}
	if !reflect.DeepEqual(deps.events, wantEvents) {
		t.Fatalf("mixed branch event order = %v, want %v", deps.events, wantEvents)
	}
	if len(deps.channelRuns) != 0 || len(deps.triggers) != 0 {
		t.Fatalf("mixed branch used legacy trigger activity: runs=%v triggers=%v", deps.channelRuns, deps.triggers)
	}
	if deps.acceptedMessages != 1 || len(result.RunAgents) != 2 || result.InitialMessageSubmittedAt.IsZero() {
		t.Fatalf("mixed branch acceptance metadata: accepted=%d agents=%+v submitted=%v", deps.acceptedMessages, result.RunAgents, result.InitialMessageSubmittedAt)
	}
}

func TestDispatchMixedBranchFailureCompensatesFreshAReALSessionAtEveryRollbackEdge(t *testing.T) {
	for _, stage := range []string{"provision", "prepare", "bind", "send", "start"} {
		t.Run(stage, func(t *testing.T) {
			deps := newMixedBranchPreflightDeps()
			deps.failStage = stage
			_, err := NewEnvDispatchService(deps, 1).Dispatch(context.Background(), mixedBranchDispatchInput())
			if err == nil || !strings.Contains(err.Error(), "synthetic") {
				t.Fatalf("dispatch error = %v, want synthetic %s failure", err, stage)
			}
			if !reflect.DeepEqual(deps.reclaimedAReALSessions, deps.openedAReALSessions) {
				t.Fatalf("reclaimed AReAL sessions = %v, want every opened session %v at %s rollback", deps.reclaimedAReALSessions, deps.openedAReALSessions, stage)
			}
			if stage == "provision" || stage == "prepare" || stage == "bind" {
				for _, event := range deps.events {
					if event == "persist" || event == "start" || event == "deliver" {
						t.Fatalf("preflight failure reached conversation activity: %v", deps.events)
					}
				}
				if deps.acceptedMessages != 0 || deps.agentTurns != 0 {
					t.Fatalf("preflight failure accepted=%d turns=%d", deps.acceptedMessages, deps.agentTurns)
				}
			}
			if stage == "send" && (deps.acceptedMessages != 0 || deps.agentTurns != 0) {
				t.Fatalf("failed canonical persistence reached delivery: accepted=%d turns=%d", deps.acceptedMessages, deps.agentTurns)
			}
			if stage == "start" && (deps.acceptedMessages != 0 || deps.agentTurns != 0) {
				t.Fatalf("timeout-start failure reached an agent turn: accepted=%d turns=%d events=%v", deps.acceptedMessages, deps.agentTurns, deps.events)
			}
			if !reflect.DeepEqual(stringCountSet(deps.revokedAgents), stringCountSet(deps.preparedAgents)) {
				t.Fatalf("revoked Pi agents = %v, want every prepared agent %v at %s rollback", deps.revokedAgents, deps.preparedAgents, stage)
			}
		})
	}
}

func TestDispatchMixedBranchCleanupFailuresAreAggregatedAndRetried(t *testing.T) {
	deps := newMixedBranchPreflightDeps()
	deps.failStage = "bind"
	// Runtime IDs are allocated deterministically by the fake in provision order.
	deps.reclaimFailures = map[string]error{
		"rt-1": errors.New("injected online cleanup failure"),
		"rt-2": errors.New("injected offline cleanup failure"),
	}

	_, err := NewEnvDispatchService(deps, 1).Dispatch(context.Background(), mixedBranchDispatchInput())
	if err == nil {
		t.Fatal("dispatch unexpectedly succeeded")
	}
	text := err.Error()
	for _, want := range []string{"synthetic bind failure", "injected online cleanup failure", "injected offline cleanup failure"} {
		if !strings.Contains(text, want) {
			t.Fatalf("dispatch error %q does not surface %q", text, want)
		}
	}
	if !strings.HasPrefix(text, "failed_preflight:") || strings.Index(text, "synthetic bind failure") > strings.Index(text, "injected online cleanup failure") {
		t.Fatalf("initiating error is not primary: %q", text)
	}
	attempts := map[string]int{}
	for _, runtimeID := range deps.reclaimAttempts {
		attempts[runtimeID]++
	}
	for _, runtimeID := range []string{"rt-1", "rt-2"} {
		if attempts[runtimeID] < 2 {
			t.Fatalf("cleanup attempts for %s = %d, want initial attempt plus idempotent retry; all=%v", runtimeID, attempts[runtimeID], deps.reclaimAttempts)
		}
	}
}

func TestDispatchMixedBranchSiblingPreflightFailureSendsNoInitialMessage(t *testing.T) {
	deps := newMixedBranchPreflightDeps()
	deps.failStage = "sibling-provision"
	input := mixedBranchDispatchInput()
	input.GroupSize = 2

	_, err := NewEnvDispatchService(deps, 1).Dispatch(context.Background(), input)
	if err == nil || !strings.Contains(err.Error(), "synthetic sibling provisioning failure") {
		t.Fatalf("dispatch error = %v, want sibling provisioning failure", err)
	}
	if deps.acceptedMessages != 0 {
		t.Fatalf("failed sibling preflight accepted %d initial messages, want zero", deps.acceptedMessages)
	}
	if deps.startAttempts != 0 {
		t.Fatalf("failed sibling preflight started %d rollout timeouts, want zero", deps.startAttempts)
	}
	assertSameAReALSessionSet(t, deps.reclaimedAReALSessions, deps.openedAReALSessions)
}

func TestDispatchMixedBranchSiblingFailureCompensatesAlreadyStartedRollout(t *testing.T) {
	deps := newMixedBranchPreflightDeps()
	deps.failStage = "sibling-start"
	input := mixedBranchDispatchInput()
	input.GroupSize = 2

	_, err := NewEnvDispatchService(deps, 1).Dispatch(context.Background(), input)
	if err == nil || !strings.Contains(err.Error(), "synthetic timeout-start failure") {
		t.Fatalf("dispatch error = %v, want sibling timeout-start failure", err)
	}
	if deps.startAttempts != 2 || len(deps.mixedStartedRuns) != 1 {
		t.Fatalf("timeout starts: attempts=%d successful=%v, want one accepted sibling before failure", deps.startAttempts, deps.mixedStartedRuns)
	}
	assertSameAReALSessionSet(t, deps.reclaimedAReALSessions, deps.openedAReALSessions)
}

func TestDispatchMixedBranchPostDispatchPersistenceFailureCompensatesStartedRollout(t *testing.T) {
	deps := newMixedBranchPreflightDeps()
	deps.failStage = "post-dispatch-local-target"

	_, err := NewEnvDispatchService(deps, 1).Dispatch(context.Background(), mixedBranchDispatchInput())
	if err == nil || !strings.Contains(err.Error(), "synthetic post-dispatch local-target failure") {
		t.Fatalf("dispatch error = %v, want post-dispatch local-target failure", err)
	}
	if deps.acceptedMessages != 1 || deps.startAttempts != 1 || len(deps.mixedStartedRuns) != 1 || deps.localTargetCalls != 2 {
		t.Fatalf("post-dispatch edge: accepted=%d startAttempts=%d successfulStarts=%v localTargetCalls=%d", deps.acceptedMessages, deps.startAttempts, deps.mixedStartedRuns, deps.localTargetCalls)
	}
	assertSameAReALSessionSet(t, deps.reclaimedAReALSessions, deps.openedAReALSessions)
}

func assertSameAReALSessionSet(t *testing.T, got, want []string) {
	t.Helper()
	gotSet := make(map[string]int, len(got))
	wantSet := make(map[string]int, len(want))
	for _, sessionID := range got {
		gotSet[sessionID]++
	}
	for _, sessionID := range want {
		wantSet[sessionID]++
	}
	if !reflect.DeepEqual(gotSet, wantSet) {
		t.Fatalf("reclaimed AReAL sessions = %v, want every opened session %v", got, want)
	}
}

func stringCountSet(values []string) map[string]int {
	set := make(map[string]int, len(values))
	for _, value := range values {
		set[value]++
	}
	return set
}
