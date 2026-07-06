package service

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"testing"
)

type fakeEnvDispatchDeps struct {
	mu sync.Mutex

	envs       map[string]Env        // by envID
	sandboxes  map[string]string     // sandboxID -> sourceSandboxID (for fork provenance)
	projects   map[string]string     // projectID -> envID
	issues     map[string][]IssueRow // projectID -> issues
	chatSess   map[string]string     // sessionID -> projectID
	agentRuns  []string              // every enqueued runID
	runCounter int
	idem       map[string]EnvDispatchResult // idempotency ledger

	defaultSelfPlayEnv string // per-workspace default self_play base env ("" = unconfigured)

	forkErr        error
	createEnvErr   error
	copyProjectErr error
	createIssueErr error
	enqueueErr     error
}

func newFakeEnvDispatchDeps() *fakeEnvDispatchDeps {
	return &fakeEnvDispatchDeps{
		envs: map[string]Env{}, sandboxes: map[string]string{}, projects: map[string]string{},
		issues: map[string][]IssueRow{}, chatSess: map[string]string{},
	}
}

func (f *fakeEnvDispatchDeps) GetEnv(_ context.Context, envID, _ string) (Env, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	e, ok := f.envs[envID]
	if !ok {
		return Env{}, fmt.Errorf("not found")
	}
	return e, nil
}
func (f *fakeEnvDispatchDeps) CreateEnv(_ context.Context, _ string, sandboxIDs []string, parentEnvID string, mode EnvMode, domain EnvDomain) (string, error) {
	if f.createEnvErr != nil {
		return "", f.createEnvErr
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	id := fmt.Sprintf("env-%d", len(f.envs))
	f.envs[id] = Env{ID: id, SandboxIDs: sandboxIDs, ParentEnvID: parentEnvID, Mode: mode, Domain: domain}
	return id, nil
}
func (f *fakeEnvDispatchDeps) DeleteEnv(_ context.Context, envID, _ string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	delete(f.envs, envID)
	return nil
}
func (f *fakeEnvDispatchDeps) ForkSandbox(_ context.Context, _ string, idx int) (string, error) {
	if f.forkErr != nil {
		return "", f.forkErr
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	id := fmt.Sprintf("sbx-fork-%d-%d", idx, len(f.sandboxes))
	f.sandboxes[id] = "forked"
	return id, nil
}
func (f *fakeEnvDispatchDeps) DeleteSandbox(_ context.Context, _ string) error { return nil }
func (f *fakeEnvDispatchDeps) BootSandbox(_ context.Context, _ string) (string, error) {
	return "sbx-booted", nil
}
func (f *fakeEnvDispatchDeps) CreateProject(_ context.Context, _, _, envID string) (string, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	id := fmt.Sprintf("proj-%d", len(f.projects))
	f.projects[id] = envID
	return id, nil
}
func (f *fakeEnvDispatchDeps) CopyProjectSubtree(_ context.Context, _, _, envID string) (string, map[string]string, map[string]string, error) {
	if f.copyProjectErr != nil {
		return "", nil, nil, f.copyProjectErr
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	pid := fmt.Sprintf("proj-copy-%d", len(f.projects))
	f.projects[pid] = envID
	imap := map[string]string{"source-issue-1": "copied-issue-1"}
	smap := map[string]string{"source-sess-1": "copied-sess-1"}
	f.issues[pid] = []IssueRow{{ID: "copied-issue-1", ProjectID: pid}}
	return pid, imap, smap, nil
}
func (f *fakeEnvDispatchDeps) GetProjectByEnvID(_ context.Context, envID, _ string) (string, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	for pid, eid := range f.projects {
		if eid == envID {
			return pid, nil
		}
	}
	return "", fmt.Errorf("no project for env %s", envID)
}
func (f *fakeEnvDispatchDeps) GetIdempotentResponse(_ context.Context, _, key string) (EnvDispatchResult, bool, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	r, ok := f.idem[key]
	return r, ok, nil
}
func (f *fakeEnvDispatchDeps) SaveIdempotentResponse(_ context.Context, _, key string, res EnvDispatchResult) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.idem == nil {
		f.idem = map[string]EnvDispatchResult{}
	}
	f.idem[key] = res
	return nil
}
func (f *fakeEnvDispatchDeps) DeleteProject(_ context.Context, pid, _ string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	delete(f.projects, pid)
	delete(f.issues, pid)
	return nil
}
func (f *fakeEnvDispatchDeps) ListIssuesByProject(_ context.Context, pid, _ string) ([]IssueRow, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.issues[pid], nil
}
func (f *fakeEnvDispatchDeps) ListChatSessionsByProject(_ context.Context, pid, _ string) ([]string, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	var out []string
	for sid, p := range f.chatSess {
		if p == pid {
			out = append(out, sid)
		}
	}
	return out, nil
}
func (f *fakeEnvDispatchDeps) CreateIssue(_ context.Context, pid, _, _, title, _ string, _, _, _ []string) (string, error) {
	if f.createIssueErr != nil {
		return "", f.createIssueErr
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	id := fmt.Sprintf("issue-%d", len(f.issues))
	f.issues[pid] = append(f.issues[pid], IssueRow{ID: id, ProjectID: pid, Title: title})
	return id, nil
}
func (f *fakeEnvDispatchDeps) CreateChatSession(_ context.Context, pid, _, _, _ string) (string, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	id := fmt.Sprintf("sess-%d", len(f.chatSess))
	f.chatSess[id] = pid
	return id, nil
}
func (f *fakeEnvDispatchDeps) CreateChatMessage(_ context.Context, _, _, _ string) (string, error) {
	return "msg-1", nil
}
func (f *fakeEnvDispatchDeps) EnqueueAgentRun(_ context.Context, _, _, _, _, _, _ string, idx int) (string, error) {
	if f.enqueueErr != nil {
		return "", f.enqueueErr
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	f.runCounter++
	id := fmt.Sprintf("run-%d", f.runCounter)
	f.agentRuns = append(f.agentRuns, id)
	return id, nil
}
func (f *fakeEnvDispatchDeps) GetDefaultSelfPlayEnv(_ context.Context, _ string) (string, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.defaultSelfPlayEnv == "" {
		return "", fmt.Errorf("not configured")
	}
	return f.defaultSelfPlayEnv, nil
}

// Helper: seed a base env in the fake.
func (f *fakeEnvDispatchDeps) seedBaseEnv() string {
	f.mu.Lock()
	defer f.mu.Unlock()
	id := "base-env-1"
	f.envs[id] = Env{ID: id, SandboxIDs: []string{"base-sbx"}, Mode: EnvModeBase, Domain: ""}
	return id
}

// TestValidate_TrainAgentID exercises the train_agent_id validation rule
// (spec §4.1): a non-empty train_agent_id is allowed when a squad_id is set
// (a team member) OR when it equals agent_id (single-agent training);
// otherwise it is rejected. An empty train_agent_id is today's behavior
// exactly (no new error).
func TestValidate_TrainAgentID(t *testing.T) {
	f := newFakeEnvDispatchDeps()
	svc := NewEnvDispatchService(f, 1)
	base := EnvDispatchInput{
		WorkspaceID: "ws", Mode: EnvModeScratch, EnvID: "base",
		Domain: EnvDomainSelfPlay, DispatchType: EnvDispatchMessage,
		GroupSize: 1, Message: &MessageInput{Content: "hi"},
	}

	// Empty train_agent_id + single agent → unchanged behavior (accepted).
	empty := base
	empty.AgentID = "ag"
	if err := svc.validate(empty); err != nil {
		t.Fatalf("empty train_agent_id must be accepted, got %v", err)
	}

	// train_agent_id == agent_id (single-agent training) → accepted.
	single := base
	single.AgentID = "ag"
	single.TrainAgentID = "ag"
	if err := svc.validate(single); err != nil {
		t.Fatalf("train_agent_id == agent_id must be accepted, got %v", err)
	}

	// train_agent_id with squad_id (team member) → accepted.
	team := base
	team.SquadID = "sq"
	team.TrainAgentID = "member"
	if err := svc.validate(team); err != nil {
		t.Fatalf("train_agent_id with squad_id must be accepted, got %v", err)
	}

	// train_agent_id set, single agent, but != agent_id and no squad → rejected.
	bad := base
	bad.AgentID = "ag"
	bad.TrainAgentID = "other"
	if err := svc.validate(bad); err == nil {
		t.Fatal("train_agent_id != agent_id without squad_id must be rejected")
	}
}

// TestDispatch_ScratchSweLegoIssue_N3 exercises the happy path.
func TestDispatch_ScratchSweLegoIssue_N3(t *testing.T) {
	f := newFakeEnvDispatchDeps()
	baseEnv := f.seedBaseEnv()
	svc := NewEnvDispatchService(f, 8)
	res, err := svc.Dispatch(context.Background(), EnvDispatchInput{
		WorkspaceID: "ws", UserID: "u", Mode: EnvModeScratch, EnvID: baseEnv,
		Domain: EnvDomainSweLego, DispatchType: EnvDispatchIssue, GroupSize: 3,
		AgentID: "ag", Issue: &IssueInput{Title: "t"},
	})
	if err != nil {
		t.Fatalf("dispatch: %v", err)
	}
	if len(res.Rollouts) != 3 {
		t.Fatalf("want 3 rollouts, got %d", len(res.Rollouts))
	}
	for i, r := range res.Rollouts {
		if r.AgentRunID == "" {
			t.Fatalf("rollout %d: no agent_run_id", i)
		}
		if r.IssueID == "" {
			t.Fatalf("rollout %d: no issue_id", i)
		}
		if r.EnvID == baseEnv {
			t.Fatalf("rollout %d: env_id reused (should be fork)", i)
		}
	}
}

func TestDispatch_ScratchSelfPlayMessage_N3(t *testing.T) {
	f := newFakeEnvDispatchDeps()
	baseEnv := f.seedBaseEnv()
	svc := NewEnvDispatchService(f, 8)
	res, err := svc.Dispatch(context.Background(), EnvDispatchInput{
		WorkspaceID: "ws", UserID: "u", Mode: EnvModeScratch, EnvID: baseEnv,
		Domain: EnvDomainSelfPlay, DispatchType: EnvDispatchMessage, GroupSize: 3,
		AgentID: "ag", Message: &MessageInput{Content: "q"},
	})
	if err != nil {
		t.Fatalf("dispatch: %v", err)
	}
	if len(res.Rollouts) != 3 {
		t.Fatalf("want 3, got %d", len(res.Rollouts))
	}
	for i, r := range res.Rollouts {
		if r.ChatSessionID == "" {
			t.Fatalf("rollout %d: no session", i)
		}
	}
}

func TestDispatch_BranchSweLegoIssue_N1_Forks(t *testing.T) {
	f := newFakeEnvDispatchDeps()
	// seed a state env (mode != base) with a project + single swe_lego issue
	stateEnv := "state-env-1"
	f.envs[stateEnv] = Env{ID: stateEnv, SandboxIDs: []string{"state-sbx"}, Mode: EnvModeBranch, Domain: EnvDomainSweLego}
	f.projects["source-proj-1"] = stateEnv
	f.issues["source-proj-1"] = []IssueRow{{ID: "source-issue-1", ProjectID: "source-proj-1"}}

	svc := NewEnvDispatchService(f, 8)
	res, err := svc.Dispatch(context.Background(), EnvDispatchInput{
		WorkspaceID: "ws", UserID: "u", Mode: EnvModeBranch, EnvID: stateEnv,
		SourceProjectID: "source-proj-1",
		Domain:          EnvDomainSweLego, DispatchType: EnvDispatchIssue, GroupSize: 1,
		AgentID: "ag",
	})
	if err != nil {
		t.Fatalf("dispatch: %v", err)
	}
	if len(res.Rollouts) != 1 {
		t.Fatalf("want 1, got %d", len(res.Rollouts))
	}
	r := res.Rollouts[0]
	if r.EnvID == stateEnv {
		t.Fatalf("env_id should be a fresh fork, not the source %s", r.EnvID)
	}
	if r.IssueID != "copied-issue-1" {
		t.Fatalf("issue should be copied, got %s", r.IssueID)
	}
	if _, ok := f.envs[stateEnv]; !ok {
		t.Fatal("source env must remain intact (re-branchable)")
	}
}

func TestDispatch_BranchSelfPlayMessage_N2(t *testing.T) {
	f := newFakeEnvDispatchDeps()
	stateEnv := "state-env-1"
	f.envs[stateEnv] = Env{ID: stateEnv, SandboxIDs: []string{"state-sbx"}, Mode: EnvModeBranch, Domain: EnvDomainSelfPlay}
	f.projects["source-proj-1"] = stateEnv
	// §7.4: branch+self_play requires the source project to have exactly one
	// chat session. Seed it so validateBranchSource passes.
	f.chatSess["source-sess-1"] = "source-proj-1"
	sessionsBefore := len(f.chatSess)

	svc := NewEnvDispatchService(f, 8)
	res, err := svc.Dispatch(context.Background(), EnvDispatchInput{
		WorkspaceID: "ws", UserID: "u", Mode: EnvModeBranch, EnvID: stateEnv,
		SourceProjectID: "source-proj-1",
		Domain:          EnvDomainSelfPlay, DispatchType: EnvDispatchMessage, GroupSize: 2,
		AgentID: "ag", Message: &MessageInput{Content: "q"},
	})
	if err != nil {
		t.Fatalf("dispatch: %v", err)
	}
	if len(res.Rollouts) != 2 {
		t.Fatalf("want 2, got %d", len(res.Rollouts))
	}
	for i, r := range res.Rollouts {
		if r.EnvID == stateEnv {
			t.Fatalf("rollout %d: env_id should be forked, not reused", i)
		}
		// branch appends to the COPIED session, not a freshly created one (spec §7.3).
		if r.ChatSessionID != "copied-sess-1" {
			t.Fatalf("rollout %d: want copied session, got %s", i, r.ChatSessionID)
		}
	}
	// No new "sess-*" session should be created for branch (append only): the
	// only session present is the seeded source session.
	if len(f.chatSess) != sessionsBefore {
		t.Fatalf("branch must not create new sessions, got %d (want %d)", len(f.chatSess), sessionsBefore)
	}
}

func TestDispatch_RejectsSweLegoMessage(t *testing.T) {
	f := newFakeEnvDispatchDeps()
	svc := NewEnvDispatchService(f, 8)
	_, err := svc.Dispatch(context.Background(), EnvDispatchInput{
		Mode: EnvModeScratch, EnvID: "base", Domain: EnvDomainSweLego,
		DispatchType: EnvDispatchMessage, GroupSize: 1, Message: &MessageInput{Content: "q"},
	})
	if err == nil {
		t.Fatal("want error")
	}
}

func TestDispatch_RejectsSelfPlayIssue_501(t *testing.T) {
	f := newFakeEnvDispatchDeps()
	svc := NewEnvDispatchService(f, 8)
	_, err := svc.Dispatch(context.Background(), EnvDispatchInput{
		Mode: EnvModeScratch, EnvID: "base", Domain: EnvDomainSelfPlay,
		DispatchType: EnvDispatchIssue, GroupSize: 1, Issue: &IssueInput{Title: "t"},
	})
	if err == nil {
		t.Fatal("want error")
	}
}

func TestDispatch_RejectsMissingDomain(t *testing.T) {
	f := newFakeEnvDispatchDeps()
	svc := NewEnvDispatchService(f, 8)
	_, err := svc.Dispatch(context.Background(), EnvDispatchInput{
		Mode: EnvModeScratch, EnvID: "base", DispatchType: EnvDispatchIssue,
		GroupSize: 1, Issue: &IssueInput{Title: "t"},
	})
	if err == nil {
		t.Fatal("want error (domain required)")
	}
}

func TestDispatch_RejectsBranchSweLegoWithIssue(t *testing.T) {
	f := newFakeEnvDispatchDeps()
	svc := NewEnvDispatchService(f, 8)
	_, err := svc.Dispatch(context.Background(), EnvDispatchInput{
		Mode: EnvModeBranch, EnvID: "state", Domain: EnvDomainSweLego,
		DispatchType: EnvDispatchIssue, GroupSize: 1, Issue: &IssueInput{Title: "t"},
	})
	if err == nil {
		t.Fatal("want error")
	}
}

func TestDispatch_RollbackOnForkFailure(t *testing.T) {
	f := newFakeEnvDispatchDeps()
	baseEnv := f.seedBaseEnv()
	f.forkErr = fmt.Errorf("fork crashed")
	svc := NewEnvDispatchService(f, 8)
	_, err := svc.Dispatch(context.Background(), EnvDispatchInput{
		WorkspaceID: "ws", Mode: EnvModeScratch, EnvID: baseEnv,
		Domain: EnvDomainSweLego, DispatchType: EnvDispatchIssue, GroupSize: 2,
		AgentID: "ag", Issue: &IssueInput{Title: "t"},
	})
	if err == nil {
		t.Fatal("want reset_failed error")
	}
	// No projects should remain.
	if len(f.projects) != 0 {
		t.Fatalf("want 0 projects after rollback, got %d", len(f.projects))
	}
}

func TestDispatch_AllDispatchFail_KeepsEnvReturnsSentinel(t *testing.T) {
	f := newFakeEnvDispatchDeps()
	baseEnv := f.seedBaseEnv()
	f.enqueueErr = fmt.Errorf("enqueue crashed") // every EnqueueAgentRun fails
	svc := NewEnvDispatchService(f, 8)
	res, err := svc.Dispatch(context.Background(), EnvDispatchInput{
		WorkspaceID: "ws", Mode: EnvModeScratch, EnvID: baseEnv,
		Domain: EnvDomainSweLego, DispatchType: EnvDispatchIssue, GroupSize: 2,
		AgentID: "ag", Issue: &IssueInput{Title: "t"},
	})
	// All dispatches failed → ErrAllDispatchFailed (handler → 500), env kept.
	if !errors.Is(err, ErrAllDispatchFailed) {
		t.Fatalf("want ErrAllDispatchFailed, got %v", err)
	}
	if len(res.Rollouts) != 2 {
		t.Fatalf("want 2, got %d", len(res.Rollouts))
	}
	for i, r := range res.Rollouts {
		if r.AgentRunID != "" {
			t.Fatalf("rollout %d: agent_run_id should be empty", i)
		}
		if r.Error == "" {
			t.Fatalf("rollout %d: error should be set", i)
		}
		if r.ProjectID == "" || r.EnvID == "" {
			t.Fatalf("rollout %d: env+project should be kept", i)
		}
	}
}

func TestDispatch_IdempotencyReplay(t *testing.T) {
	f := newFakeEnvDispatchDeps()
	baseEnv := f.seedBaseEnv()
	svc := NewEnvDispatchService(f, 8)
	in := EnvDispatchInput{
		WorkspaceID: "ws", Mode: EnvModeScratch, EnvID: baseEnv,
		Domain: EnvDomainSweLego, DispatchType: EnvDispatchIssue, GroupSize: 2,
		AgentID: "ag", Issue: &IssueInput{Title: "t"}, IdempotencyKey: "key-1",
	}
	first, err := svc.Dispatch(context.Background(), in)
	if err != nil {
		t.Fatalf("first dispatch: %v", err)
	}
	projectsAfterFirst := len(f.projects)
	runsAfterFirst := len(f.agentRuns)

	second, err := svc.Dispatch(context.Background(), in)
	if err != nil {
		t.Fatalf("replay dispatch: %v", err)
	}
	// Replay returns the stored response and does NO new work.
	if len(f.projects) != projectsAfterFirst {
		t.Fatalf("replay created new projects: %d → %d", projectsAfterFirst, len(f.projects))
	}
	if len(f.agentRuns) != runsAfterFirst {
		t.Fatalf("replay enqueued new runs")
	}
	if second.Rollouts[0].ProjectID != first.Rollouts[0].ProjectID {
		t.Fatal("replay returned different rollouts")
	}
}

func TestDispatch_RejectsMissingAgentID(t *testing.T) {
	f := newFakeEnvDispatchDeps()
	svc := NewEnvDispatchService(f, 8)
	_, err := svc.Dispatch(context.Background(), EnvDispatchInput{
		Mode: EnvModeScratch, EnvID: "base", Domain: EnvDomainSweLego,
		DispatchType: EnvDispatchIssue, GroupSize: 1, Issue: &IssueInput{Title: "t"},
	})
	if err == nil {
		t.Fatal("want error (agent_id required)")
	}
}

func TestDispatch_RejectsScratchNonBaseEnv(t *testing.T) {
	f := newFakeEnvDispatchDeps()
	stateEnv := "state-env-1"
	f.envs[stateEnv] = Env{ID: stateEnv, SandboxIDs: []string{"sbx"}, Mode: EnvModeBranch}
	svc := NewEnvDispatchService(f, 8)
	_, err := svc.Dispatch(context.Background(), EnvDispatchInput{
		WorkspaceID: "ws", Mode: EnvModeScratch, EnvID: stateEnv,
		Domain: EnvDomainSweLego, DispatchType: EnvDispatchIssue, GroupSize: 1,
		AgentID: "ag", Issue: &IssueInput{Title: "t"},
	})
	if err == nil {
		t.Fatal("want error (scratch requires a base env)")
	}
}

func TestDispatch_RejectsBranchBaseEnv(t *testing.T) {
	f := newFakeEnvDispatchDeps()
	baseEnv := f.seedBaseEnv()
	svc := NewEnvDispatchService(f, 8)
	_, err := svc.Dispatch(context.Background(), EnvDispatchInput{
		WorkspaceID: "ws", Mode: EnvModeBranch, EnvID: baseEnv,
		Domain: EnvDomainSweLego, DispatchType: EnvDispatchIssue, GroupSize: 1,
		AgentID: "ag",
	})
	if err == nil {
		t.Fatal("want error (branch requires a state env)")
	}
}

func TestDispatch_RejectsBranchSweLegoZeroIssues(t *testing.T) {
	f := newFakeEnvDispatchDeps()
	stateEnv := "state-env-1"
	f.envs[stateEnv] = Env{ID: stateEnv, SandboxIDs: []string{"sbx"}, Mode: EnvModeBranch, Domain: EnvDomainSweLego}
	f.projects["source-proj-1"] = stateEnv // no issues seeded → zero

	svc := NewEnvDispatchService(f, 8)
	_, err := svc.Dispatch(context.Background(), EnvDispatchInput{
		WorkspaceID: "ws", Mode: EnvModeBranch, EnvID: stateEnv,
		SourceProjectID: "source-proj-1",
		Domain:          EnvDomainSweLego, DispatchType: EnvDispatchIssue, GroupSize: 1,
		AgentID: "ag",
	})
	if err == nil {
		t.Fatal("want error (branch source must have exactly one issue)")
	}
	// No fork/create work should have happened (validated upfront).
	if len(f.sandboxes) != 0 {
		t.Fatalf("want 0 forked sandboxes, got %d", len(f.sandboxes))
	}
}

func TestDispatch_RejectsBranchSweLegoMultipleIssues(t *testing.T) {
	f := newFakeEnvDispatchDeps()
	stateEnv := "state-env-1"
	f.envs[stateEnv] = Env{ID: stateEnv, SandboxIDs: []string{"sbx"}, Mode: EnvModeBranch, Domain: EnvDomainSweLego}
	f.projects["source-proj-1"] = stateEnv
	f.issues["source-proj-1"] = []IssueRow{{ID: "a"}, {ID: "b"}}

	svc := NewEnvDispatchService(f, 8)
	_, err := svc.Dispatch(context.Background(), EnvDispatchInput{
		WorkspaceID: "ws", Mode: EnvModeBranch, EnvID: stateEnv,
		SourceProjectID: "source-proj-1",
		Domain:          EnvDomainSweLego, DispatchType: EnvDispatchIssue, GroupSize: 1,
		AgentID: "ag",
	})
	if err == nil {
		t.Fatal("want error (branch source must have exactly one issue)")
	}
}

// TestDispatch_ForksEverySandbox verifies the sandbox-list model: an env that
// hosts several agents (several sandboxes) is branched by forking every one of
// them into the new env.
func TestDispatch_ForksEverySandbox(t *testing.T) {
	f := newFakeEnvDispatchDeps()
	stateEnv := "state-env-1"
	f.envs[stateEnv] = Env{
		ID: stateEnv, SandboxIDs: []string{"sbx-a", "sbx-b", "sbx-c"},
		Mode: EnvModeBranch, Domain: EnvDomainSweLego,
	}
	f.projects["source-proj-1"] = stateEnv
	f.issues["source-proj-1"] = []IssueRow{{ID: "source-issue-1", ProjectID: "source-proj-1"}}

	svc := NewEnvDispatchService(f, 8)
	res, err := svc.Dispatch(context.Background(), EnvDispatchInput{
		WorkspaceID: "ws", Mode: EnvModeBranch, EnvID: stateEnv,
		SourceProjectID: "source-proj-1",
		Domain:          EnvDomainSweLego, DispatchType: EnvDispatchIssue, GroupSize: 1,
		AgentID: "ag",
	})
	if err != nil {
		t.Fatalf("dispatch: %v", err)
	}
	newEnv, ok := f.envs[res.Rollouts[0].EnvID]
	if !ok {
		t.Fatalf("new env %s not found", res.Rollouts[0].EnvID)
	}
	if len(newEnv.SandboxIDs) != 3 {
		t.Fatalf("want 3 forked sandboxes in the new env, got %d", len(newEnv.SandboxIDs))
	}
}

// TestDispatch_ResumeNormalizesToBranch verifies mode="resume" is treated as
// branch: it forks a state env and dispatches the copied issue (spec D1).
func TestDispatch_ResumeNormalizesToBranch(t *testing.T) {
	f := newFakeEnvDispatchDeps()
	// a state (branch-able) env + its 1:1 project with one issue
	f.envs["src-env"] = Env{ID: "src-env", Mode: EnvModeBranch, Domain: EnvDomainSweLego, SandboxIDs: []string{"s1"}}
	f.projects["src-proj"] = "src-env"
	f.issues["src-proj"] = []IssueRow{{ID: "iss-1", ProjectID: "src-proj"}}
	svc := NewEnvDispatchService(f, 4)
	res, err := svc.Dispatch(context.Background(), EnvDispatchInput{
		WorkspaceID: "ws", Mode: "resume", EnvID: "src-env",
		Domain: EnvDomainSweLego, DispatchType: EnvDispatchIssue,
		GroupSize: 1, AgentID: "ag",
	})
	if err != nil {
		t.Fatalf("resume dispatch: %v", err)
	}
	if len(res.Rollouts) != 1 || res.Rollouts[0].AgentRunID == "" {
		t.Fatalf("resume should behave as branch and dispatch, got %+v", res.Rollouts)
	}
}

// TestValidate_ExactlyOneAgentOrSquad verifies that neither/both agent_id and
// squad_id are rejected (spec D4).
func TestValidate_ExactlyOneAgentOrSquad(t *testing.T) {
	f := newFakeEnvDispatchDeps()
	svc := NewEnvDispatchService(f, 1)
	base := EnvDispatchInput{
		WorkspaceID: "ws", Mode: EnvModeScratch, EnvID: "base",
		Domain: EnvDomainSelfPlay, DispatchType: EnvDispatchMessage,
		GroupSize: 1, Message: &MessageInput{Content: "hi"},
	}
	// neither
	if _, err := svc.Dispatch(context.Background(), base); err == nil {
		t.Error("expected error when neither agent_id nor squad_id set")
	}
	// both
	b2 := base
	b2.AgentID, b2.SquadID = "ag", "sq"
	if _, err := svc.Dispatch(context.Background(), b2); err == nil {
		t.Error("expected error when both agent_id and squad_id set")
	}
}

// TestDispatch_EmptyEnvIDResolvesDefaultForSelfPlay verifies scratch+self_play
// with an empty env_id resolves the configured per-workspace default base env
// and forks it (spec D2/D3).
func TestDispatch_EmptyEnvIDResolvesDefaultForSelfPlay(t *testing.T) {
	f := newFakeEnvDispatchDeps()
	f.envs["ws-default-base"] = Env{ID: "ws-default-base", Mode: EnvModeBase, SandboxIDs: []string{"s1"}}
	f.defaultSelfPlayEnv = "ws-default-base"
	svc := NewEnvDispatchService(f, 1)
	res, err := svc.Dispatch(context.Background(), EnvDispatchInput{
		WorkspaceID: "ws", Mode: EnvModeScratch, EnvID: "",
		Domain: EnvDomainSelfPlay, DispatchType: EnvDispatchMessage,
		GroupSize: 1, AgentID: "ag", Message: &MessageInput{Content: "hi"},
	})
	if err != nil {
		t.Fatalf("default-env self_play dispatch: %v", err)
	}
	if res.Rollouts[0].EnvID == "" {
		t.Fatal("expected a forked env_id from the default base")
	}
}

// TestValidate_EmptyEnvIDRejectedForSweLegoAndUnconfigured verifies empty
// env_id is rejected for swe_lego, and for self_play when no default is
// configured (spec D2/D3).
func TestValidate_EmptyEnvIDRejectedForSweLegoAndUnconfigured(t *testing.T) {
	f := newFakeEnvDispatchDeps() // defaultSelfPlayEnv == "" (unconfigured)
	svc := NewEnvDispatchService(f, 1)
	// swe_lego + empty env_id → 400
	if _, err := svc.Dispatch(context.Background(), EnvDispatchInput{
		WorkspaceID: "ws", Mode: EnvModeScratch, EnvID: "",
		Domain: EnvDomainSweLego, DispatchType: EnvDispatchIssue,
		GroupSize: 1, AgentID: "ag", Issue: &IssueInput{Title: "t"},
	}); err == nil {
		t.Error("swe_lego with empty env_id must be rejected")
	}
	// self_play + empty env_id + no default configured → 400
	if _, err := svc.Dispatch(context.Background(), EnvDispatchInput{
		WorkspaceID: "ws", Mode: EnvModeScratch, EnvID: "",
		Domain: EnvDomainSelfPlay, DispatchType: EnvDispatchMessage,
		GroupSize: 1, AgentID: "ag", Message: &MessageInput{Content: "hi"},
	}); err == nil {
		t.Error("self_play with empty env_id and no default must be rejected")
	}
}
