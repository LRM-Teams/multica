package service

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"sync"
	"testing"

	"github.com/multica-ai/multica/server/internal/util/stackerr"
)

type fakeSandboxInstanceCreator struct {
	calls      []createSandboxCall
	ref        SandboxInstanceRef
	err        error
	refs       map[string]SandboxInstanceRef // instanceID -> ref for GetSandboxInstanceRef
	getErr     error
	deleteCalls []string                     // instanceIDs passed to DeleteSandboxInstance
	deleteErr  error
}

func (c *fakeSandboxInstanceCreator) GetSandboxInstanceRef(_ context.Context, _, instanceID string) (SandboxInstanceRef, error) {
	if c.getErr != nil {
		return SandboxInstanceRef{}, c.getErr
	}
	if c.refs != nil {
		if ref, ok := c.refs[instanceID]; ok {
			return ref, nil
		}
	}
	return SandboxInstanceRef{}, fmt.Errorf("sandbox_instance not found: %s", instanceID)
}

type createSandboxCall struct {
	WorkspaceID   string
	ActorUserID   string
	Template      string
	BaseEnvID     string
	DaemonEnabled bool
	RuntimeEnv    map[string]string
}

func (c *fakeSandboxInstanceCreator) CreateSandboxInstance(_ context.Context, in CreateSandboxInstanceInput, actorUserID string) (SandboxInstanceRef, error) {
	c.calls = append(c.calls, createSandboxCall{WorkspaceID: in.WorkspaceID, ActorUserID: actorUserID, Template: in.Template, BaseEnvID: "", DaemonEnabled: in.DaemonEnabled, RuntimeEnv: in.RuntimeEnv})
	if c.err != nil {
		return SandboxInstanceRef{}, c.err
	}
	var ref SandboxInstanceRef
	if c.ref.InstanceID != "" {
		ref = c.ref
	} else {
		ref = SandboxInstanceRef{
			InstanceID:  fmt.Sprintf("inst-%d", len(c.calls)),
			WorkspaceID: in.WorkspaceID,
			Template:    in.Template,
		}
	}
	// Record the ref so GetSandboxInstanceRef (the instance-backed probe + the
	// scratch template derivation) can resolve the instance we just created.
	if c.refs == nil {
		c.refs = map[string]SandboxInstanceRef{}
	}
	c.refs[ref.InstanceID] = ref
	return ref, nil
}

func (c *fakeSandboxInstanceCreator) DeleteSandboxInstance(_ context.Context, ref SandboxInstanceRef, _ string) error {
	c.deleteCalls = append(c.deleteCalls, ref.InstanceID)
	return c.deleteErr
}

var _ SandboxInstanceCreator = (*fakeSandboxInstanceCreator)(nil)

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

	defaultSelfPlayEnvSetCalls int // number of SetDefaultSelfPlayEnv invocations (race assertions)

	// simulateRaceWinner, when non-empty, is forced as defaultSelfPlayEnv inside
	// SetDefaultSelfPlayEnv so the service's re-read sees a different winner than
	// the env it just created - driving the loser-cleanup path deterministically.
	simulateRaceWinner string

	trainingSaves []trainingSaveCall // every SaveTrainingDispatch call, in order

	forkErr        error
	createEnvErr   error
	copyProjectErr error
	createIssueErr error
	enqueueErr     error

	// Phase 2: PrecreateAgentRuntime recording + canned return, and the
	// runtime_id passed to each EnqueueAgentRun call (the pre-created R').
	precreateRuntimeCalls   []precreateRuntimeCall
	precreateRuntimeID      string // canned runtime id; "" -> auto "rt-<n>"
	precreateRuntimeCounter int
	precreateRuntimeErr     error
	enqueueRuntimeIDs       []string
	deleteRuntimeCalls      []string
	deleteRuntimeErr        error

	// Per-agent env spec DB-backed validation seam (§5).
	validateAgentErrs   map[string]error // agentID -> error; missing key = OK
	resolveEnvSpecErrs  map[string]error // template or base_env_id -> error; missing key = OK
	validateAgentCalls  []string
	resolveEnvSpecCalls []PerAgentEnvSpec
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
type precreateRuntimeCall struct {
	WorkspaceID string
	OwnerUserID string
	AgentID     string
}

func (f *fakeEnvDispatchDeps) EnqueueAgentRun(_ context.Context, _, _, _, _, _, _, _, runtimeID string, idx int) (string, error) {
	if f.enqueueErr != nil {
		return "", f.enqueueErr
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	f.runCounter++
	id := fmt.Sprintf("run-%d", f.runCounter)
	f.agentRuns = append(f.agentRuns, id)
	f.enqueueRuntimeIDs = append(f.enqueueRuntimeIDs, runtimeID)
	return id, nil
}

func (f *fakeEnvDispatchDeps) PrecreateAgentRuntime(_ context.Context, workspaceID, ownerUserID, agentID string) (string, string, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.precreateRuntimeCalls = append(f.precreateRuntimeCalls, precreateRuntimeCall{WorkspaceID: workspaceID, OwnerUserID: ownerUserID, AgentID: agentID})
	if f.precreateRuntimeErr != nil {
		return "", "", f.precreateRuntimeErr
	}
	f.precreateRuntimeCounter++
	rid := fmt.Sprintf("rt-%d", f.precreateRuntimeCounter)
	if f.precreateRuntimeID != "" {
		rid = f.precreateRuntimeID
	}
	return rid, "daemon-" + rid, nil
}

func (f *fakeEnvDispatchDeps) DeleteAgentRuntime(_ context.Context, _ string, runtimeID string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.deleteRuntimeCalls = append(f.deleteRuntimeCalls, runtimeID)
	if f.deleteRuntimeErr != nil {
		return f.deleteRuntimeErr
	}
	return nil
}
func (f *fakeEnvDispatchDeps) GetDefaultSelfPlayEnv(_ context.Context, _ string) (string, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	// Mirrors the real adapter: ("", nil) when unconfigured so the service can
	// distinguish "not configured" (auto-create) from a real query error.
	return f.defaultSelfPlayEnv, nil
}
func (f *fakeEnvDispatchDeps) SetDefaultSelfPlayEnv(_ context.Context, _, envID string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.defaultSelfPlayEnvSetCalls++
	// Conditional, only-if-empty: first concurrent writer wins.
	if f.defaultSelfPlayEnv == "" {
		f.defaultSelfPlayEnv = envID
	}
	// Optionally simulate a concurrent writer that already set a different
	// default, so the service's re-read picks up a winner != envID.
	if f.simulateRaceWinner != "" {
		f.defaultSelfPlayEnv = f.simulateRaceWinner
	}
	return nil
}

// trainingSaveCall records the arguments of one SaveTrainingDispatch call.
type trainingSaveCall struct {
	projectID     string
	workspaceID   string
	trainAgentID  string
	criticAgentID string
	defaultReward float64
}

func (f *fakeEnvDispatchDeps) SaveTrainingDispatch(_ context.Context, projectID, workspaceID, trainAgentID, criticAgentID string, defaultReward float64) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.trainingSaves = append(f.trainingSaves, trainingSaveCall{
		projectID: projectID, workspaceID: workspaceID,
		trainAgentID: trainAgentID, criticAgentID: criticAgentID,
		defaultReward: defaultReward,
	})
	return nil
}

func (f *fakeEnvDispatchDeps) ValidateAgentInWorkspaceOrSquad(_ context.Context, _, _, agentID string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.validateAgentCalls = append(f.validateAgentCalls, agentID)
	if f.validateAgentErrs != nil {
		if err, ok := f.validateAgentErrs[agentID]; ok {
			return err
		}
	}
	return nil
}

func (f *fakeEnvDispatchDeps) ResolvePerAgentEnvSpec(_ context.Context, _ string, spec PerAgentEnvSpec) (SandboxInstanceRef, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.resolveEnvSpecCalls = append(f.resolveEnvSpecCalls, spec)
	key := spec.Template
	if key == "" {
		key = spec.BaseEnvID
	}
	if f.resolveEnvSpecErrs != nil {
		if err, ok := f.resolveEnvSpecErrs[key]; ok {
			return SandboxInstanceRef{}, err
		}
	}
	template := spec.Template
	if template == "" {
		template = "default"
	}
	return SandboxInstanceRef{Template: template, WorkspaceID: spec.AgentID}, nil
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

// fakeAdapterErr mirrors the production adapter: it wraps a raw error with a
// stackerr stack captured at the call site. This helper stands in for an
// adapter method so Dispatch's stack propagation can be exercised end-to-end
// without a real DB / cloud-runtime.
func fakeAdapterErr(label, msg string) error {
	return stackerr.Wrap(errors.New(msg), label)
}

// TestDispatch_ResetFailedPreservesAdapterStack guards the reset_failed %w seam
// (service/env_dispatch.go): the adapter-origin StackError must survive Unwrap
// through "rollout N reset" and "reset_failed" so the handler can render its
// traceback. With the prior %v the stack was flattened away and StackOf -> nil.
func TestDispatch_ResetFailedPreservesAdapterStack(t *testing.T) {
	f := newFakeEnvDispatchDeps()
	baseEnv := f.seedBaseEnv()
	f.forkErr = fakeAdapterErr("fork sandbox", "fork crashed")
	svc := NewEnvDispatchService(f, 8)
	_, err := svc.Dispatch(context.Background(), EnvDispatchInput{
		WorkspaceID: "ws", Mode: EnvModeScratch, EnvID: baseEnv,
		Domain: EnvDomainSweLego, DispatchType: EnvDispatchIssue, GroupSize: 2,
		AgentID: "ag", Issue: &IssueInput{Title: "t"},
	})
	if err == nil {
		t.Fatal("want reset_failed error")
	}
	if !strings.Contains(err.Error(), "reset_failed") {
		t.Fatalf("want reset_failed in error, got %q", err.Error())
	}
	st := stackerr.StackOf(err)
	if st == nil {
		t.Fatalf("StackOf returned nil; reset_failed seam lost the adapter stack: %v", err)
	}
	if !strings.Contains(string(st), "fakeAdapterErr") {
		t.Fatalf("captured stack missing the adapter-origin frame:\n%s", st)
	}
}

// TestDispatch_AllDispatchFail_RolloutsCarryStack verifies the 500 path: each
// failed rollout captures the adapter-origin stack (dispatchOne -> StackOf) so
// the handler can surface a per-rollout traceback.
func TestDispatch_AllDispatchFail_RolloutsCarryStack(t *testing.T) {
	f := newFakeEnvDispatchDeps()
	baseEnv := f.seedBaseEnv()
	f.enqueueErr = fakeAdapterErr("create agent task", "enqueue crashed")
	svc := NewEnvDispatchService(f, 8)
	res, err := svc.Dispatch(context.Background(), EnvDispatchInput{
		WorkspaceID: "ws", Mode: EnvModeScratch, EnvID: baseEnv,
		Domain: EnvDomainSweLego, DispatchType: EnvDispatchIssue, GroupSize: 2,
		AgentID: "ag", Issue: &IssueInput{Title: "t"},
	})
	if !errors.Is(err, ErrAllDispatchFailed) {
		t.Fatalf("want ErrAllDispatchFailed, got %v", err)
	}
	if len(res.Rollouts) != 2 {
		t.Fatalf("want 2 rollouts, got %d", len(res.Rollouts))
	}
	for i, r := range res.Rollouts {
		if r.Stack == nil {
			t.Fatalf("rollout %d: expected origin stack, got nil", i)
		}
		if !strings.Contains(string(r.Stack), "fakeAdapterErr") {
			t.Fatalf("rollout %d: stack missing adapter-origin frame", i)
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
// configured AND no sandbox lifecycle is injected (so auto-create cannot run).
// With a lifecycle, the self_play case auto-creates a default instead - see
// TestDispatch_AutoCreatesDefaultSelfPlayEnvAndReuses.
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
		t.Error("self_play with empty env_id, no default, and no lifecycle must be rejected")
	}
}

// TestDispatch_AutoCreatesDefaultSelfPlayEnvAndReuses verifies that a
// scratch+self_play dispatch with an empty env_id and no configured default
// auto-creates a base env (sandbox_instance-backed), persists it as the
// workspace default, forks it via the sandbox_instance backend, and that a
// second dispatch reuses the now-configured default (no second auto-create).
func TestDispatch_AutoCreatesDefaultSelfPlayEnvAndReuses(t *testing.T) {
	f := newFakeEnvDispatchDeps()
	lc := &fakeSandboxInstanceCreator{}
	svc := NewEnvDispatchService(f, 1).WithSandboxLifecycle(lc)

	in := EnvDispatchInput{
		WorkspaceID: "ws", UserID: "u", Mode: EnvModeScratch, EnvID: "",
		Domain: EnvDomainSelfPlay, DispatchType: EnvDispatchMessage,
		GroupSize: 1, AgentID: "ag", Message: &MessageInput{Content: "hi"},
		DefaultBaseTemplate: "py312",
	}

	// Dispatch 1: auto-creates the default.
	res1, err := svc.Dispatch(context.Background(), in)
	if err != nil {
		t.Fatalf("dispatch 1 (auto-create): %v", err)
	}
	if res1.Rollouts[0].EnvID == "" {
		t.Fatal("dispatch 1: expected a forked rollout env_id")
	}
	if f.defaultSelfPlayEnv == "" {
		t.Fatal("dispatch 1: expected the default self_play env to be persisted")
	}
	if f.defaultSelfPlayEnvSetCalls != 1 {
		t.Fatalf("dispatch 1: want 1 SetDefaultSelfPlayEnv call, got %d", f.defaultSelfPlayEnvSetCalls)
	}
	// The auto-created base env's sandbox is a sandbox_instance (inst-1 from the
	// first CreateSandboxInstance call), and the rollout fork creates a second
	// instance (inst-2) reusing the base's template.
	if len(lc.calls) != 2 {
		t.Fatalf("want 2 sandbox_instance creates (base + rollout fork), got %d", len(lc.calls))
	}
	if lc.calls[0].Template != "py312" {
		t.Fatalf("auto-created base template = %q, want %q", lc.calls[0].Template, "py312")
	}
	if lc.calls[1].Template != "py312" {
		t.Fatalf("rollout fork template = %q, want %q (should reuse base template)", lc.calls[1].Template, "py312")
	}
	// The auto-created base env is a forkable template holder - it must NOT boot
	// a daemon (no DaemonEnabled). The rollout fork is the per-agent execution
	// sandbox - it MUST be daemon-enabled so the in-sandbox daemon can reach
	// multica. This is the Phase 1 daemon-runtime-env wiring contract.
	if lc.calls[0].DaemonEnabled {
		t.Fatalf("auto-created base env sandbox must not be daemon-enabled, got %+v", lc.calls[0])
	}
	if !lc.calls[1].DaemonEnabled {
		t.Fatalf("rollout fork sandbox must be daemon-enabled, got %+v", lc.calls[1])
	}

	// Dispatch 2: reuses the configured default; no second auto-create.
	setCallsBefore := f.defaultSelfPlayEnvSetCalls
	createsBefore := len(lc.calls)
	res2, err := svc.Dispatch(context.Background(), in)
	if err != nil {
		t.Fatalf("dispatch 2 (reuse): %v", err)
	}
	if res2.Rollouts[0].EnvID == "" {
		t.Fatal("dispatch 2: expected a forked rollout env_id")
	}
	if f.defaultSelfPlayEnvSetCalls != setCallsBefore {
		t.Fatalf("dispatch 2: default already configured; want 0 additional SetDefaultSelfPlayEnv calls, got %d", f.defaultSelfPlayEnvSetCalls-setCallsBefore)
	}
	// Only the rollout fork creates a new instance (no base auto-create).
	if len(lc.calls) != createsBefore+1 {
		t.Fatalf("dispatch 2: want 1 additional sandbox_instance create (rollout fork), got %d", len(lc.calls)-createsBefore)
	}
}

// TestDispatch_AutoCreateDefaultEnvRaceLoserCleanup verifies the loser path of
// the auto-create race: when the re-read of the default returns a different
// winner (a concurrent writer already set one), the dispatch cleans up its own
// auto-created sandbox_instance and proceeds to fork the winner.
func TestDispatch_AutoCreateDefaultEnvRaceLoserCleanup(t *testing.T) {
	f := newFakeEnvDispatchDeps()
	lc := &fakeSandboxInstanceCreator{}
	// Pre-seed the race winner: a base env whose sandbox is a sandbox_instance
	// the lifecycle can resolve (so the instance-backend probe succeeds).
	const winnerEnv = "winner-base"
	const winnerInst = "winner-inst"
	f.envs[winnerEnv] = Env{ID: winnerEnv, Mode: EnvModeBase, SandboxIDs: []string{winnerInst}}
	lc.refs = map[string]SandboxInstanceRef{winnerInst: {InstanceID: winnerInst, Template: "default"}}
	// Force the re-read after SetDefaultSelfPlayEnv to return the winner, so the
	// env this dispatch auto-creates is treated as the loser.
	f.simulateRaceWinner = winnerEnv

	svc := NewEnvDispatchService(f, 1).WithSandboxLifecycle(lc)
	res, err := svc.Dispatch(context.Background(), EnvDispatchInput{
		WorkspaceID: "ws", UserID: "u", Mode: EnvModeScratch, EnvID: "",
		Domain: EnvDomainSelfPlay, DispatchType: EnvDispatchMessage,
		GroupSize: 1, AgentID: "ag", Message: &MessageInput{Content: "hi"},
	})
	if err != nil {
		t.Fatalf("loser-cleanup dispatch: %v", err)
	}
	if res.Rollouts[0].EnvID == "" {
		t.Fatal("expected the dispatch to fork the winner base env")
	}
	// The loser's auto-created sandbox_instance must have been reclaimed.
	if len(lc.deleteCalls) != 1 {
		t.Fatalf("want 1 DeleteSandboxInstance (loser cleanup), got %d: %v", len(lc.deleteCalls), lc.deleteCalls)
	}
	if lc.deleteCalls[0] == winnerInst {
		t.Fatalf("loser cleanup deleted the winner instance %q; should delete its own auto-created instance", winnerInst)
	}
	// The persisted default is the winner, and the winner base env still exists.
	if f.defaultSelfPlayEnv != winnerEnv {
		t.Fatalf("default = %q, want %q", f.defaultSelfPlayEnv, winnerEnv)
	}
	if _, ok := f.envs[winnerEnv]; !ok {
		t.Fatal("winner base env must not be deleted")
	}
}

// TestDispatch_PersistsTrainingDispatchWhenTrainAgentSet verifies that when a
// dispatch carries a train_agent_id, exactly one training_dispatch row is
// persisted per successful rollout project, keyed by the rollout's project_id
// with the workspace_id, train_agent_id, and the default reward (spec §4.1).
func TestDispatch_PersistsTrainingDispatchWhenTrainAgentSet(t *testing.T) {
	f := newFakeEnvDispatchDeps()
	baseEnv := f.seedBaseEnv()
	svc := NewEnvDispatchService(f, 8)
	res, err := svc.Dispatch(context.Background(), EnvDispatchInput{
		WorkspaceID: "ws", UserID: "u", Mode: EnvModeScratch, EnvID: baseEnv,
		Domain: EnvDomainSweLego, DispatchType: EnvDispatchIssue, GroupSize: 3,
		AgentID: "ag", TrainAgentID: "ag", Issue: &IssueInput{Title: "t"},
	})
	if err != nil {
		t.Fatalf("dispatch: %v", err)
	}

	if len(f.trainingSaves) != 3 {
		t.Fatalf("want 3 training_dispatch saves (one per rollout project), got %d", len(f.trainingSaves))
	}

	// Each save must correspond 1:1 to a rollout project with the correct
	// workspace_id, train_agent_id, and default reward.
	savedByProject := map[string]trainingSaveCall{}
	for _, s := range f.trainingSaves {
		if s.workspaceID != "ws" {
			t.Fatalf("save workspace_id = %q, want %q", s.workspaceID, "ws")
		}
		if s.trainAgentID != "ag" {
			t.Fatalf("save train_agent_id = %q, want %q", s.trainAgentID, "ag")
		}
		if s.defaultReward != DefaultTrainingReward {
			t.Fatalf("save default_reward = %v, want %v", s.defaultReward, DefaultTrainingReward)
		}
		savedByProject[s.projectID] = s
	}
	if len(savedByProject) != 3 {
		t.Fatalf("want 3 distinct project_ids saved, got %d", len(savedByProject))
	}
	for i, r := range res.Rollouts {
		if _, ok := savedByProject[r.ProjectID]; !ok {
			t.Fatalf("rollout %d project %q has no training_dispatch save", i, r.ProjectID)
		}
	}
}

// TestDispatch_NoTrainingDispatchWhenTrainAgentEmpty verifies that with no
// train_agent_id, zero training_dispatch rows are persisted (today's behavior
// exactly).
func TestDispatch_NoTrainingDispatchWhenTrainAgentEmpty(t *testing.T) {
	f := newFakeEnvDispatchDeps()
	baseEnv := f.seedBaseEnv()
	svc := NewEnvDispatchService(f, 8)
	_, err := svc.Dispatch(context.Background(), EnvDispatchInput{
		WorkspaceID: "ws", UserID: "u", Mode: EnvModeScratch, EnvID: baseEnv,
		Domain: EnvDomainSweLego, DispatchType: EnvDispatchIssue, GroupSize: 3,
		AgentID: "ag", Issue: &IssueInput{Title: "t"},
	})
	if err != nil {
		t.Fatalf("dispatch: %v", err)
	}
	if len(f.trainingSaves) != 0 {
		t.Fatalf("want 0 training_dispatch saves when train_agent_id empty, got %d", len(f.trainingSaves))
	}
}

// TestEnvDispatchInput_Validate_CriticAgentID exercises the critic_agent_id validation rules:
// critic_agent_id requires train_agent_id; critic_agent_id must differ from train_agent_id;
// critic_agent_id must differ from agent_id; empty critic_agent_id is unchanged behavior.
func TestEnvDispatchInput_Validate_CriticAgentID(t *testing.T) {
	tests := []struct {
		name    string
		in      EnvDispatchInput
		wantErr string
	}{
		{"empty critic ok (squad)", EnvDispatchInput{WorkspaceID: "ws", SquadID: "sq", TrainAgentID: "train", Mode: EnvModeScratch, EnvID: "base", Domain: EnvDomainSelfPlay, DispatchType: EnvDispatchMessage, GroupSize: 1, Message: &MessageInput{Content: "hi"}}, ""},
		{"critic with squad+train ok", EnvDispatchInput{WorkspaceID: "ws", SquadID: "sq", TrainAgentID: "train", CriticAgentID: "crit", Mode: EnvModeScratch, EnvID: "base", Domain: EnvDomainSelfPlay, DispatchType: EnvDispatchMessage, GroupSize: 1, Message: &MessageInput{Content: "hi"}}, ""},
		{"critic without train rejected", EnvDispatchInput{WorkspaceID: "ws", SquadID: "sq", CriticAgentID: "crit", Mode: EnvModeScratch, EnvID: "base", Domain: EnvDomainSelfPlay, DispatchType: EnvDispatchMessage, GroupSize: 1, Message: &MessageInput{Content: "hi"}}, "validation_failed: critic_agent_id requires train_agent_id"},
		{"critic == train rejected", EnvDispatchInput{WorkspaceID: "ws", SquadID: "sq", TrainAgentID: "same", CriticAgentID: "same", Mode: EnvModeScratch, EnvID: "base", Domain: EnvDomainSelfPlay, DispatchType: EnvDispatchMessage, GroupSize: 1, Message: &MessageInput{Content: "hi"}}, "validation_failed: critic_agent_id must differ from train_agent_id"},
		{"critic == agent rejected (single agent)", EnvDispatchInput{WorkspaceID: "ws", AgentID: "ag", TrainAgentID: "ag", CriticAgentID: "ag", Mode: EnvModeScratch, EnvID: "base", Domain: EnvDomainSelfPlay, DispatchType: EnvDispatchMessage, GroupSize: 1, Message: &MessageInput{Content: "hi"}}, "validation_failed: critic_agent_id must differ from train_agent_id"},
		{"critic ok with squad (no agent id)", EnvDispatchInput{WorkspaceID: "ws", SquadID: "sq", TrainAgentID: "train", CriticAgentID: "crit", Mode: EnvModeScratch, EnvID: "base", Domain: EnvDomainSelfPlay, DispatchType: EnvDispatchMessage, GroupSize: 1, Message: &MessageInput{Content: "hi"}}, ""},
		{"empty critic ok (single agent)", EnvDispatchInput{WorkspaceID: "ws", AgentID: "ag", TrainAgentID: "ag", Mode: EnvModeScratch, EnvID: "base", Domain: EnvDomainSelfPlay, DispatchType: EnvDispatchMessage, GroupSize: 1, Message: &MessageInput{Content: "hi"}}, ""},
		{"critic with single agent ok", EnvDispatchInput{WorkspaceID: "ws", AgentID: "ag", TrainAgentID: "ag", CriticAgentID: "crit", Mode: EnvModeScratch, EnvID: "base", Domain: EnvDomainSelfPlay, DispatchType: EnvDispatchMessage, GroupSize: 1, Message: &MessageInput{Content: "hi"}}, ""},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			f := newFakeEnvDispatchDeps()
			svc := NewEnvDispatchService(f, 1)
			err := svc.validate(tc.in)
			if tc.wantErr == "" {
				if err != nil {
					t.Fatalf("expected no error, got %v", err)
				}
			} else {
				if err == nil {
					t.Fatalf("expected error containing %q, got nil", tc.wantErr)
				}
				if err.Error() != tc.wantErr {
					t.Fatalf("expected error %q, got %q", tc.wantErr, err.Error())
				}
			}
		})
	}
}

// TestEnvDispatchTrainedRolloutCreatesSandboxInstanceRefs verifies that when
// a sandbox lifecycle creator is injected and the dispatch is save/resume-
// capable (train_agent_id set), reset creates a sandbox_instance via the
// creator instead of forking Fleet sandboxes, and populates SandboxRefs.
func TestEnvDispatchTrainedRolloutCreatesSandboxInstanceRefs(t *testing.T) {
	f := newFakeEnvDispatchDeps()
	baseEnv := f.seedBaseEnv()
	creator := &fakeSandboxInstanceCreator{ref: SandboxInstanceRef{
		InstanceID: "inst-1", WorkspaceID: "ws", NodeID: "node-1",
		Template: "default", Status: "pending",
		RuntimeMetadata: json.RawMessage(`{}`),
	}}
	svc := NewEnvDispatchService(f, 8).WithSandboxLifecycle(creator)

	res, err := svc.Dispatch(context.Background(), EnvDispatchInput{
		WorkspaceID: "ws", UserID: "u", Mode: EnvModeScratch, EnvID: baseEnv,
		Domain: EnvDomainSweLego, DispatchType: EnvDispatchIssue, GroupSize: 1,
		AgentID: "ag", TrainAgentID: "ag", Issue: &IssueInput{Title: "t"},
	})
	if err != nil {
		t.Fatalf("dispatch: %v", err)
	}
	if len(creator.calls) != 1 {
		t.Fatalf("want 1 sandbox_instance create, got %d", len(creator.calls))
	}
	if len(f.sandboxes) != 0 {
		t.Fatalf("trained rollout must not fork Fleet sandboxes, got %d", len(f.sandboxes))
	}
	if len(res.Rollouts) != 1 || len(res.Rollouts[0].SandboxRefs) != 1 {
		t.Fatalf("want 1 rollout with 1 sandbox ref, got %+v", res.Rollouts)
	}
	if res.Rollouts[0].SandboxRefs[0].InstanceID != "inst-1" {
		t.Fatalf("unexpected sandbox ref: %+v", res.Rollouts[0].SandboxRefs[0])
	}
	env := f.envs[res.Rollouts[0].EnvID]
	if len(env.SandboxIDs) != 1 || env.SandboxIDs[0] != "inst-1" {
		t.Fatalf("env sandbox_ids should carry the sandbox_instance id, got %+v", env.SandboxIDs)
	}
}

// TestEnvDispatchNonTrainedRolloutPreservesFleetPath verifies that without a
// train_agent_id (or without an injected lifecycle creator), the existing
// Fleet fork path is preserved and SandboxRefs stays empty.
func TestEnvDispatchNonTrainedRolloutPreservesFleetPath(t *testing.T) {
	f := newFakeEnvDispatchDeps()
	baseEnv := f.seedBaseEnv()
	creator := &fakeSandboxInstanceCreator{ref: SandboxInstanceRef{InstanceID: "inst-1", WorkspaceID: "ws"}}
	svc := NewEnvDispatchService(f, 8).WithSandboxLifecycle(creator)

	res, err := svc.Dispatch(context.Background(), EnvDispatchInput{
		WorkspaceID: "ws", UserID: "u", Mode: EnvModeScratch, EnvID: baseEnv,
		Domain: EnvDomainSweLego, DispatchType: EnvDispatchIssue, GroupSize: 1,
		AgentID: "ag", Issue: &IssueInput{Title: "t"},
	})
	if err != nil {
		t.Fatalf("dispatch: %v", err)
	}
	if len(creator.calls) != 0 {
		t.Fatalf("non-trained rollout must not create sandbox_instances, got %d", len(creator.calls))
	}
	if len(f.sandboxes) != 1 {
		t.Fatalf("non-trained rollout should fork 1 Fleet sandbox, got %d", len(f.sandboxes))
	}
	if len(res.Rollouts) != 1 || len(res.Rollouts[0].SandboxRefs) != 0 {
		t.Fatalf("non-trained rollout must have no sandbox refs, got %+v", res.Rollouts)
	}
}

// TestEnvDispatchSandboxInstanceBranchCreatesFreshFromTemplate verifies that a
// save/resume-capable branch rollout creates a fresh sandbox_instance from the
// source env's template (resolved via GetSandboxInstanceRef) rather than a live
// Fleet fork, and carries the new ref on the rollout.
func TestEnvDispatchSandboxInstanceBranchCreatesFreshFromTemplate(t *testing.T) {
	f := newFakeEnvDispatchDeps()
	// Seed a source STATE env (branch source) with one sandbox_instance.
	sourceEnvID := "src-env-1"
	f.envs[sourceEnvID] = Env{
		ID: sourceEnvID, WorkspaceID: "ws",
		SandboxIDs: []string{"src-inst-1"},
		Mode:       EnvModeBranch, Domain: EnvDomainSweLego,
	}
	f.projects["src-proj-1"] = sourceEnvID
	f.issues["src-proj-1"] = []IssueRow{{ID: "src-issue-1", ProjectID: "src-proj-1"}}

	creator := &fakeSandboxInstanceCreator{
		refs: map[string]SandboxInstanceRef{
			"src-inst-1": {InstanceID: "src-inst-1", WorkspaceID: "ws", Template: "python-3.12"},
		},
	}
	svc := NewEnvDispatchService(f, 8).WithSandboxLifecycle(creator)

	res, err := svc.Dispatch(context.Background(), EnvDispatchInput{
		WorkspaceID: "ws", UserID: "u", Mode: EnvModeBranch, EnvID: sourceEnvID,
		Domain: EnvDomainSweLego, DispatchType: EnvDispatchIssue, GroupSize: 1,
		AgentID: "ag", TrainAgentID: "ag",
	})
	if err != nil {
		t.Fatalf("dispatch: %v", err)
	}
	if len(creator.calls) != 1 {
		t.Fatalf("want 1 sandbox_instance create, got %d", len(creator.calls))
	}
	// The created instance must use the source env's template, not "default".
	if creator.calls[0].Template != "python-3.12" {
		t.Fatalf("want template python-3.12 (from source), got %q", creator.calls[0].Template)
	}
	if len(f.sandboxes) != 0 {
		t.Fatalf("branch rollout must not fork Fleet sandboxes, got %d", len(f.sandboxes))
	}
	if len(res.Rollouts) != 1 || len(res.Rollouts[0].SandboxRefs) != 1 {
		t.Fatalf("want 1 rollout with 1 sandbox ref, got %+v", res.Rollouts)
	}
}

// TestEnvDispatchPerAgentEnvSpecs_ShapeValidation exercises the synchronous
// shape rules for per-agent env specs: empty specs preserve current behavior,
// every spec needs an agent_id with exactly one of template/base_env_id, and
// duplicate agents are rejected.
func TestEnvDispatchPerAgentEnvSpecs_ShapeValidation(t *testing.T) {
	tests := []struct {
		name    string
		specs   []PerAgentEnvSpec
		wantErr string
	}{
		{"empty ok", nil, ""},
		{"valid template ok", []PerAgentEnvSpec{{AgentID: "a", Template: "python"}}, ""},
		{"valid base_env ok", []PerAgentEnvSpec{{AgentID: "a", BaseEnvID: "base"}}, ""},
		{"empty agent_id rejected", []PerAgentEnvSpec{{Template: "python"}}, "validation_failed: per_agent_env agent_id is required"},
		{"missing template and base_env rejected", []PerAgentEnvSpec{{AgentID: "a"}}, "validation_failed: per_agent_env spec for agent a needs a template or base_env_id"},
		{"both template and base_env rejected", []PerAgentEnvSpec{{AgentID: "a", Template: "python", BaseEnvID: "base"}}, "validation_failed: per_agent_env spec for agent a must set template or base_env_id, not both"},
		{"duplicate agent rejected", []PerAgentEnvSpec{{AgentID: "a", Template: "x"}, {AgentID: "a", Template: "y"}}, "validation_failed: per_agent_env agent_id a is duplicated"},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			f := newFakeEnvDispatchDeps()
			svc := NewEnvDispatchService(f, 1)
			in := EnvDispatchInput{
				WorkspaceID: "ws", SquadID: "sq", Mode: EnvModeScratch, EnvID: "base",
				Domain: EnvDomainSelfPlay, DispatchType: EnvDispatchMessage, GroupSize: 1,
				Message: &MessageInput{Content: "hi"}, PerAgentEnvSpecs: tc.specs,
			}
			err := svc.validate(in)
			if tc.wantErr == "" {
				if err != nil {
					t.Fatalf("expected no error, got %v", err)
				}
			} else {
				if err == nil {
					t.Fatalf("expected error %q, got nil", tc.wantErr)
				}
				if err.Error() != tc.wantErr {
					t.Fatalf("expected error %q, got %q", tc.wantErr, err.Error())
				}
			}
		})
	}
}

// TestEnvDispatchPerAgentEnvSpecsRejectUnknownAgent verifies that DB-backed
// agent membership validation rejects per-agent env specs whose agent_id is not
// a member of the workspace/squad, before any rollout state is created.
func TestEnvDispatchPerAgentEnvSpecsRejectUnknownAgent(t *testing.T) {
	f := newFakeEnvDispatchDeps()
	baseEnv := f.seedBaseEnv()
	f.validateAgentErrs = map[string]error{"ghost": fmt.Errorf("agent not in workspace")}
	svc := NewEnvDispatchService(f, 1)

	_, err := svc.Dispatch(context.Background(), EnvDispatchInput{
		WorkspaceID: "ws", UserID: "u", SquadID: "sq", Mode: EnvModeScratch, EnvID: baseEnv,
		Domain: EnvDomainSelfPlay, DispatchType: EnvDispatchMessage, GroupSize: 1,
		Message:          &MessageInput{Content: "hi"},
		PerAgentEnvSpecs: []PerAgentEnvSpec{{AgentID: "ghost", Template: "python"}},
	})
	if err == nil {
		t.Fatalf("expected validation error for unknown agent, got nil")
	}
	if !strings.Contains(err.Error(), "validation_failed") {
		t.Fatalf("expected validation_failed error, got %v", err)
	}
	if !strings.Contains(err.Error(), "ghost") {
		t.Fatalf("expected error to reference agent ghost, got %v", err)
	}
	if len(f.envs) != 1 {
		t.Fatalf("reject must happen before env creation; envs=%d", len(f.envs))
	}
}

// TestEnvDispatchPerAgentEnvSpecsRejectUnknownEnvSpec verifies that DB-backed
// env spec resolution rejects per-agent env specs whose template/base_env_id is
// unknown or unauthorized, before any rollout state is created.
func TestEnvDispatchPerAgentEnvSpecsRejectUnknownEnvSpec(t *testing.T) {
	f := newFakeEnvDispatchDeps()
	baseEnv := f.seedBaseEnv()
	f.resolveEnvSpecErrs = map[string]error{"bad-template": fmt.Errorf("template not found: bad-template")}
	svc := NewEnvDispatchService(f, 1)

	_, err := svc.Dispatch(context.Background(), EnvDispatchInput{
		WorkspaceID: "ws", UserID: "u", SquadID: "sq", Mode: EnvModeScratch, EnvID: baseEnv,
		Domain: EnvDomainSelfPlay, DispatchType: EnvDispatchMessage, GroupSize: 1,
		Message:          &MessageInput{Content: "hi"},
		PerAgentEnvSpecs: []PerAgentEnvSpec{{AgentID: "ag", Template: "bad-template"}},
	})
	if err == nil {
		t.Fatalf("expected validation error for unknown env spec, got nil")
	}
	if !strings.Contains(err.Error(), "validation_failed") {
		t.Fatalf("expected validation_failed error, got %v", err)
	}
	if !strings.Contains(err.Error(), "bad-template") {
		t.Fatalf("expected error to reference bad-template, got %v", err)
	}
	if len(f.envs) != 1 {
		t.Fatalf("reject must happen before env creation; envs=%d", len(f.envs))
	}
}

// TestEnvDispatchPerAgentEnvSpecsEmptyPreservesCurrentBehavior verifies that
// empty per-agent env specs trigger no DB-backed validation calls and produce
// the same rollout shape as today's default/shared sandbox behavior.
func TestEnvDispatchPerAgentEnvSpecsEmptyPreservesCurrentBehavior(t *testing.T) {
	f := newFakeEnvDispatchDeps()
	baseEnv := f.seedBaseEnv()
	svc := NewEnvDispatchService(f, 1)

	res, err := svc.Dispatch(context.Background(), EnvDispatchInput{
		WorkspaceID: "ws", UserID: "u", Mode: EnvModeScratch, EnvID: baseEnv,
		Domain: EnvDomainSweLego, DispatchType: EnvDispatchIssue, GroupSize: 1,
		AgentID: "ag", Issue: &IssueInput{Title: "t"},
	})
	if err != nil {
		t.Fatalf("dispatch: %v", err)
	}
	if len(f.validateAgentCalls) != 0 {
		t.Fatalf("empty specs must not trigger agent validation, got %d calls", len(f.validateAgentCalls))
	}
	if len(f.resolveEnvSpecCalls) != 0 {
		t.Fatalf("empty specs must not trigger env spec resolution, got %d calls", len(f.resolveEnvSpecCalls))
	}
	if len(res.Rollouts) != 1 || len(res.Rollouts[0].AgentSandboxRefs) != 0 {
		t.Fatalf("empty specs must not populate AgentSandboxRefs, got %+v", res.Rollouts)
	}
}

// TestEnvDispatchPerAgentEnvSpecsAssignDistinctSandboxRefs verifies that
// per-agent env specs produce one sandbox_instance per spec, with each ref
// keyed by agent_id in AgentSandboxRefs and all refs distinct.
func TestEnvDispatchPerAgentEnvSpecsAssignDistinctSandboxRefs(t *testing.T) {
	f := newFakeEnvDispatchDeps()
	baseEnv := f.seedBaseEnv()
	creator := &fakeSandboxInstanceCreator{} // distinct refs auto-generated
	svc := NewEnvDispatchService(f, 1).WithSandboxLifecycle(creator)

	res, err := svc.Dispatch(context.Background(), EnvDispatchInput{
		WorkspaceID: "ws", UserID: "u", SquadID: "sq", Mode: EnvModeScratch, EnvID: baseEnv,
		Domain: EnvDomainSelfPlay, DispatchType: EnvDispatchMessage, GroupSize: 1,
		TrainAgentID: "train",
		Message:      &MessageInput{Content: "hi"},
		PerAgentEnvSpecs: []PerAgentEnvSpec{
			{AgentID: "a1", Template: "python"},
			{AgentID: "a2", Template: "node"},
		},
	})
	if err != nil {
		t.Fatalf("dispatch: %v", err)
	}
	if len(creator.calls) != 2 {
		t.Fatalf("want 2 sandbox_instance creates, got %d", len(creator.calls))
	}
	// Each per-agent execution sandbox must be daemon-enabled so the in-sandbox
	// daemon can reach multica (Phase 1 daemon-runtime-env wiring contract).
	for i, c := range creator.calls {
		if !c.DaemonEnabled {
			t.Fatalf("per-agent sandbox call %d must be daemon-enabled, got %+v", i, c)
		}
	}
	if len(res.Rollouts) != 1 {
		t.Fatalf("want 1 rollout, got %d", len(res.Rollouts))
	}
	refs := res.Rollouts[0].AgentSandboxRefs
	if len(refs) != 2 {
		t.Fatalf("want 2 agent sandbox refs, got %d", len(refs))
	}
	r1, ok1 := refs["a1"]
	r2, ok2 := refs["a2"]
	if !ok1 || !ok2 {
		t.Fatalf("missing agent refs: a1=%v a2=%v", ok1, ok2)
	}
	if r1.InstanceID == r2.InstanceID {
		t.Fatalf("agent refs must be distinct: both %s", r1.InstanceID)
	}
}

// TestDispatch_PrecreatesRuntimeAndRoutesTaskToSandboxRuntime verifies the
// Phase 2 contract for a single-agent, instance-backed self_play rollout: the
// service pre-creates an agent_runtime row R' (keyed by a daemon_id), injects
// that daemon_id as MULTICA_DAEMON_ID into the sandbox runtime_env (so the
// in-sandbox daemon adopts R' on register), and routes the task to R' instead
// of the session's runtime. runtime_id is deterministic at dispatch time -
// no NULL, no deferred binding.
func TestDispatch_PrecreatesRuntimeAndRoutesTaskToSandboxRuntime(t *testing.T) {
	f := newFakeEnvDispatchDeps()
	baseEnv := f.seedBaseEnv() // base-env-1, SandboxIDs=["base-sbx"], mode=base
	// Seed the base env's sandbox as a resolvable sandbox_instance so
	// InstanceBackedBase is probed true (-> sandbox_instance backend).
	creator := &fakeSandboxInstanceCreator{
		refs: map[string]SandboxInstanceRef{
			"base-sbx": {InstanceID: "base-sbx", WorkspaceID: "ws", Template: "default"},
		},
	}
	svc := NewEnvDispatchService(f, 1).WithSandboxLifecycle(creator)

	res, err := svc.Dispatch(context.Background(), EnvDispatchInput{
		WorkspaceID: "ws", UserID: "u", Mode: EnvModeScratch, EnvID: baseEnv,
		Domain: EnvDomainSelfPlay, DispatchType: EnvDispatchMessage,
		GroupSize: 1, AgentID: "ag",
		Message: &MessageInput{Content: "hi"},
	})
	if err != nil {
		t.Fatalf("dispatch: %v", err)
	}
	if len(res.Rollouts) != 1 {
		t.Fatalf("want 1 rollout, got %d", len(res.Rollouts))
	}

	// PrecreateAgentRuntime was called once for the single agent.
	if len(f.precreateRuntimeCalls) != 1 {
		t.Fatalf("want 1 PrecreateAgentRuntime call, got %d", len(f.precreateRuntimeCalls))
	}
	pc := f.precreateRuntimeCalls[0]
	if pc.WorkspaceID != "ws" || pc.OwnerUserID != "u" || pc.AgentID != "ag" {
		t.Fatalf("unexpected precreate call: %+v", pc)
	}

	// The rollout sandbox is daemon-enabled AND carries the pre-assigned
	// MULTICA_DAEMON_ID (the in-sandbox daemon adopts R' on register). The base
	// env's sandbox was pre-seeded, so only the rollout fork creates an instance.
	if len(creator.calls) != 1 {
		t.Fatalf("want 1 sandbox_instance create (rollout fork), got %d", len(creator.calls))
	}
	sb := creator.calls[0]
	if !sb.DaemonEnabled {
		t.Fatalf("rollout sandbox must be daemon-enabled, got %+v", sb)
	}
	if sb.RuntimeEnv["MULTICA_DAEMON_ID"] != "daemon-rt-1" {
		t.Fatalf("MULTICA_DAEMON_ID = %q, want daemon-rt-1", sb.RuntimeEnv["MULTICA_DAEMON_ID"])
	}

	// The rollout's sandbox ref carries R' (runtime_id + daemon_id).
	if len(res.Rollouts[0].SandboxRefs) != 1 {
		t.Fatalf("want 1 sandbox ref, got %d", len(res.Rollouts[0].SandboxRefs))
	}
	ref := res.Rollouts[0].SandboxRefs[0]
	if ref.RuntimeID != "rt-1" || ref.DaemonID != "daemon-rt-1" {
		t.Fatalf("sandbox ref runtime = %q/%q, want rt-1/daemon-rt-1", ref.RuntimeID, ref.DaemonID)
	}

	// The task was routed to R' (not the session runtime).
	if len(f.enqueueRuntimeIDs) != 1 || f.enqueueRuntimeIDs[0] != "rt-1" {
		t.Fatalf("enqueue runtime_id = %v, want [rt-1]", f.enqueueRuntimeIDs)
	}
}

// TestDispatch_SquadDispatchDoesNotPrecreateRuntime verifies that squad
// dispatch (no single agent) does NOT pre-create R' or route to it - the
// sandbox boots a daemon that registers its own runtime and the task stays on
// the leader's runtime. (Squad R' routing is deferred.)
func TestDispatch_SquadDispatchDoesNotPrecreateRuntime(t *testing.T) {
	f := newFakeEnvDispatchDeps()
	baseEnv := f.seedBaseEnv()
	creator := &fakeSandboxInstanceCreator{
		refs: map[string]SandboxInstanceRef{
			"base-sbx": {InstanceID: "base-sbx", WorkspaceID: "ws", Template: "default"},
		},
	}
	svc := NewEnvDispatchService(f, 1).WithSandboxLifecycle(creator)

	if _, err := svc.Dispatch(context.Background(), EnvDispatchInput{
		WorkspaceID: "ws", UserID: "u", SquadID: "sq", Mode: EnvModeScratch, EnvID: baseEnv,
		Domain: EnvDomainSelfPlay, DispatchType: EnvDispatchMessage, GroupSize: 1,
		Message: &MessageInput{Content: "hi"},
	}); err != nil {
		t.Fatalf("dispatch: %v", err)
	}
	if len(f.precreateRuntimeCalls) != 0 {
		t.Fatalf("squad dispatch must not precreate runtime, got %d calls", len(f.precreateRuntimeCalls))
	}
	if len(f.enqueueRuntimeIDs) != 1 || f.enqueueRuntimeIDs[0] != "" {
		t.Fatalf("squad dispatch task must keep empty runtime_id (leader runtime), got %v", f.enqueueRuntimeIDs)
	}
}

// TestDispatch_SandboxCreateFailureReclaimsPrecreatedRuntime verifies Phase 2b
// concern 1: when the rollout sandbox_instance fork fails AFTER R' was
// pre-created, the pre-created runtime row is reclaimed so an offline orphan
// does not linger. (Reclaim happens inside createSandboxInstanceRefs, before
// dispatchOne ever runs.)
func TestDispatch_SandboxCreateFailureReclaimsPrecreatedRuntime(t *testing.T) {
	f := newFakeEnvDispatchDeps()
	baseEnv := f.seedBaseEnv()
	creator := &fakeSandboxInstanceCreator{
		refs: map[string]SandboxInstanceRef{
			"base-sbx": {InstanceID: "base-sbx", WorkspaceID: "ws", Template: "default"},
		},
		err: fmt.Errorf("sandbox create exploded"), // fork CreateSandboxInstance fails
	}
	svc := NewEnvDispatchService(f, 1).WithSandboxLifecycle(creator)

	_, err := svc.Dispatch(context.Background(), EnvDispatchInput{
		WorkspaceID: "ws", UserID: "u", Mode: EnvModeScratch, EnvID: baseEnv,
		Domain: EnvDomainSelfPlay, DispatchType: EnvDispatchMessage,
		GroupSize: 1, AgentID: "ag",
		Message: &MessageInput{Content: "hi"},
	})
	if err == nil {
		t.Fatalf("dispatch must fail when sandbox_instance fork fails")
	}
	// R' was pre-created then reclaimed because the sandbox fork failed.
	if len(f.precreateRuntimeCalls) != 1 {
		t.Fatalf("want 1 precreate call, got %d", len(f.precreateRuntimeCalls))
	}
	if len(f.deleteRuntimeCalls) != 1 || f.deleteRuntimeCalls[0] != "rt-1" {
		t.Fatalf("orphan R' must be reclaimed, deleteRuntimeCalls=%v", f.deleteRuntimeCalls)
	}
}

// TestDispatch_EnqueueFailureReclaimsPrecreatedRuntime verifies Phase 2b
// concern 1: when the sandbox fork succeeds but the task enqueue fails (so the
// rollout's task is never created), dispatchOne's defer reclaims R'. Dispatch is
// best-effort here - it returns no top-level error but the rollout records one.
func TestDispatch_EnqueueFailureReclaimsPrecreatedRuntime(t *testing.T) {
	f := newFakeEnvDispatchDeps()
	f.enqueueErr = fmt.Errorf("enqueue crashed")
	baseEnv := f.seedBaseEnv()
	creator := &fakeSandboxInstanceCreator{
		refs: map[string]SandboxInstanceRef{
			"base-sbx": {InstanceID: "base-sbx", WorkspaceID: "ws", Template: "default"},
		},
	}
	svc := NewEnvDispatchService(f, 1).WithSandboxLifecycle(creator)

	res, err := svc.Dispatch(context.Background(), EnvDispatchInput{
		WorkspaceID: "ws", UserID: "u", Mode: EnvModeScratch, EnvID: baseEnv,
		Domain: EnvDomainSelfPlay, DispatchType: EnvDispatchMessage,
		GroupSize: 1, AgentID: "ag",
		Message: &MessageInput{Content: "hi"},
	})
	// Status rule (spec §6.3): 0 succeeded -> ErrAllDispatchFailed, but the
	// rollouts[] are still returned in the body. The reclaim is independent of
	// the top-level status - it fires from dispatchOne's defer because the
	// rollout's task was never created.
	if !errors.Is(err, ErrAllDispatchFailed) {
		t.Fatalf("dispatch with 0 succeeded must return ErrAllDispatchFailed, got %v", err)
	}
	if len(res.Rollouts) != 1 || res.Rollouts[0].Error == "" {
		t.Fatalf("rollout must record the enqueue error, got %+v", res.Rollouts)
	}
	if res.Rollouts[0].AgentRunID != "" {
		t.Fatalf("rollout must have no agent run on enqueue failure, got %q", res.Rollouts[0].AgentRunID)
	}
	if len(f.deleteRuntimeCalls) != 1 || f.deleteRuntimeCalls[0] != "rt-1" {
		t.Fatalf("orphan R' must be reclaimed on enqueue failure, deleteRuntimeCalls=%v", f.deleteRuntimeCalls)
	}
}

// TestDispatch_SuccessKeepsPrecreatedRuntime verifies Phase 2b concern 1: on a
// successful dispatch the pre-created R' is NOT reclaimed - the task is routed
// to it and the in-sandbox daemon adopts it on register.
func TestDispatch_SuccessKeepsPrecreatedRuntime(t *testing.T) {
	f := newFakeEnvDispatchDeps()
	baseEnv := f.seedBaseEnv()
	creator := &fakeSandboxInstanceCreator{
		refs: map[string]SandboxInstanceRef{
			"base-sbx": {InstanceID: "base-sbx", WorkspaceID: "ws", Template: "default"},
		},
	}
	svc := NewEnvDispatchService(f, 1).WithSandboxLifecycle(creator)

	if _, err := svc.Dispatch(context.Background(), EnvDispatchInput{
		WorkspaceID: "ws", UserID: "u", Mode: EnvModeScratch, EnvID: baseEnv,
		Domain: EnvDomainSelfPlay, DispatchType: EnvDispatchMessage,
		GroupSize: 1, AgentID: "ag",
		Message: &MessageInput{Content: "hi"},
	}); err != nil {
		t.Fatalf("dispatch: %v", err)
	}
	if len(f.precreateRuntimeCalls) != 1 {
		t.Fatalf("want 1 precreate call, got %d", len(f.precreateRuntimeCalls))
	}
	if len(f.deleteRuntimeCalls) != 0 {
		t.Fatalf("successful dispatch must NOT reclaim R', deleteRuntimeCalls=%v", f.deleteRuntimeCalls)
	}
}

// TestEnvDispatchPerAgentEnvSpecsPartialSquadUsesDefaults verifies that when
// only some squad members have per-agent env specs, specified members get their
// own sandbox_instance refs and unspecified members do not get entries in
// AgentSandboxRefs (they use the shared/default behavior).
func TestEnvDispatchPerAgentEnvSpecsPartialSquadUsesDefaults(t *testing.T) {
	f := newFakeEnvDispatchDeps()
	baseEnv := f.seedBaseEnv()
	creator := &fakeSandboxInstanceCreator{}
	svc := NewEnvDispatchService(f, 1).WithSandboxLifecycle(creator)

	res, err := svc.Dispatch(context.Background(), EnvDispatchInput{
		WorkspaceID: "ws", UserID: "u", SquadID: "sq", Mode: EnvModeScratch, EnvID: baseEnv,
		Domain: EnvDomainSelfPlay, DispatchType: EnvDispatchMessage, GroupSize: 1,
		TrainAgentID: "train",
		Message:      &MessageInput{Content: "hi"},
		PerAgentEnvSpecs: []PerAgentEnvSpec{
			{AgentID: "a1", Template: "python"},
		},
	})
	if err != nil {
		t.Fatalf("dispatch: %v", err)
	}
	refs := res.Rollouts[0].AgentSandboxRefs
	if len(refs) != 1 {
		t.Fatalf("want 1 agent sandbox ref (only specified), got %d", len(refs))
	}
	if _, ok := refs["a1"]; !ok {
		t.Fatalf("missing ref for specified agent a1")
	}
	if _, ok := refs["a2"]; ok {
		t.Fatalf("unspecified agent a2 should not have a ref")
	}
}

// TestEnvDispatch_PersistsCriticAgentID verifies that critic_agent_id is
// persisted to training_dispatch when provided.
func TestEnvDispatch_PersistsCriticAgentID(t *testing.T) {
	f := newFakeEnvDispatchDeps()
	fake := f
	fake.trainingSaves = []trainingSaveCall{} // track inserts

	baseEnv := fake.seedBaseEnv()
	svc := NewEnvDispatchService(fake, 8)
	in := EnvDispatchInput{
		WorkspaceID:   "ws",
		UserID:        "user",
		Mode:          EnvModeScratch,
		EnvID:         baseEnv,
		Domain:        EnvDomainSweLego,
		DispatchType:  EnvDispatchIssue,
		GroupSize:     1,
		AgentID:       "agent-1",
		TrainAgentID:  "agent-1",
		CriticAgentID: "critic-1",
		Issue: &IssueInput{
			Title: "Test Issue",
		},
	}
	result, err := svc.Dispatch(context.Background(), in)
	if err != nil {
		t.Fatalf("dispatch: %v", err)
	}

	if len(fake.trainingSaves) != 1 {
		t.Fatalf("want 1 training_dispatch save, got %d", len(fake.trainingSaves))
	}
	if fake.trainingSaves[0].criticAgentID != "critic-1" {
		t.Fatalf("save criticAgentID = %q, want %q", fake.trainingSaves[0].criticAgentID, "critic-1")
	}
	if fake.trainingSaves[0].trainAgentID != "agent-1" {
		t.Fatalf("save trainAgentID = %q, want %q", fake.trainingSaves[0].trainAgentID, "agent-1")
	}
	if fake.trainingSaves[0].projectID != result.Rollouts[0].ProjectID {
		t.Fatalf("save projectID mismatch: got %q, want %q", fake.trainingSaves[0].projectID, result.Rollouts[0].ProjectID)
	}
}

// TestEnvDispatch_NoCritic_PersistsNull verifies that when no critic_agent_id
// is provided, the persisted value is empty (NULL in DB terms).
func TestEnvDispatch_NoCritic_PersistsNull(t *testing.T) {
	f := newFakeEnvDispatchDeps()
	fake := f
	fake.trainingSaves = []trainingSaveCall{} // track inserts

	baseEnv := fake.seedBaseEnv()
	svc := NewEnvDispatchService(fake, 8)
	in := EnvDispatchInput{
		WorkspaceID:  "ws",
		UserID:       "user",
		Mode:         EnvModeScratch,
		EnvID:        baseEnv,
		Domain:       EnvDomainSweLego,
		DispatchType: EnvDispatchIssue,
		GroupSize:    1,
		AgentID:      "agent-1",
		TrainAgentID: "agent-1",
		Issue: &IssueInput{
			Title: "Test Issue",
		},
	}
	result, err := svc.Dispatch(context.Background(), in)
	if err != nil {
		t.Fatalf("dispatch: %v", err)
	}

	if len(fake.trainingSaves) != 1 {
		t.Fatalf("want 1 training_dispatch save, got %d", len(fake.trainingSaves))
	}
	if fake.trainingSaves[0].criticAgentID != "" {
		t.Fatalf("save criticAgentID should be empty, got %q", fake.trainingSaves[0].criticAgentID)
	}
	if fake.trainingSaves[0].trainAgentID != "agent-1" {
		t.Fatalf("save trainAgentID = %q, want %q", fake.trainingSaves[0].trainAgentID, "agent-1")
	}
	if fake.trainingSaves[0].projectID != result.Rollouts[0].ProjectID {
		t.Fatalf("save projectID mismatch: got %q, want %q", fake.trainingSaves[0].projectID, result.Rollouts[0].ProjectID)
	}
}
