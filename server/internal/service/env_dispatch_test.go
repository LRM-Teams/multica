package service

import (
	"context"
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
func (f *fakeEnvDispatchDeps) CreateEnv(_ context.Context, _, sandboxID, parentEnvID string, mode EnvMode, domain EnvDomain) (string, error) {
	if f.createEnvErr != nil {
		return "", f.createEnvErr
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	id := fmt.Sprintf("env-%d", len(f.envs))
	f.envs[id] = Env{ID: id, SandboxID: sandboxID, ParentEnvID: parentEnvID, Mode: mode, Domain: domain}
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
func (f *fakeEnvDispatchDeps) EnqueueAgentRun(_ context.Context, _, _, _, _, _ string, idx int) (string, error) {
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

// Helper: seed a base env in the fake.
func (f *fakeEnvDispatchDeps) seedBaseEnv() string {
	f.mu.Lock()
	defer f.mu.Unlock()
	id := "base-env-1"
	f.envs[id] = Env{ID: id, SandboxID: "base-sbx", Mode: EnvModeBase, Domain: ""}
	return id
}

const EnvModeBase EnvMode = "base"

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
	f.envs[stateEnv] = Env{ID: stateEnv, SandboxID: "state-sbx", Mode: EnvModeBranch, Domain: EnvDomainSweLego}
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
	f.envs[stateEnv] = Env{ID: stateEnv, SandboxID: "state-sbx", Mode: EnvModeBranch, Domain: EnvDomainSelfPlay}
	f.projects["source-proj-1"] = stateEnv

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
	// No new "sess-*" session should be created for branch (append only).
	if len(f.chatSess) != 0 {
		t.Fatalf("branch must not create new sessions, got %d", len(f.chatSess))
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
