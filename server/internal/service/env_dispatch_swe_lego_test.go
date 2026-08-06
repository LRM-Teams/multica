package service

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"testing"
)

// fakeSweLegoTemplateResolver records every LookupReadyTemplate call so tests
// can assert when the dispatch integration point fires and what request it
// resolves. It is read-only by construction: there is no build to assert.
type fakeSweLegoTemplateResolver struct {
	calls      []SweLegoTemplateRequest
	templateID string
	status     string
	err        error
}

func (r *fakeSweLegoTemplateResolver) LookupReadyTemplate(_ context.Context, req SweLegoTemplateRequest) (string, string, error) {
	r.calls = append(r.calls, req)
	if r.err != nil {
		return "", "", r.err
	}
	return r.templateID, r.status, nil
}

const sweLegoIssueSourcePayload = `{"title":"source title","description":"source description",` +
	`"repo_url":"https://example.test/repo.git","base_commit":"abc123","issue_date":"2025-01-02T03:04:05Z"}`

func seedSweLegoIssueSource(f *fakeEnvDispatchDeps, id string) {
	f.sourceTasks[id] = SourceTask{
		ID: id, WorkspaceID: "ws", Type: SourceTaskIssue,
		Payload: json.RawMessage(sweLegoIssueSourcePayload),
	}
}

// The integration point: a ready cache hit sets TaskTemplateID and every
// rollout sandbox boots from the pre-materialized task template.
func TestDispatch_ScratchSweLegoIssueSource_UsesReadyTaskTemplate(t *testing.T) {
	f := newFakeEnvDispatchDeps()
	baseEnv := f.seedBaseEnv()
	seedSweLegoIssueSource(f, "source-issue")
	creator := &fakeSandboxInstanceCreator{}
	resolver := &fakeSweLegoTemplateResolver{templateID: "tpl-task", status: "ready"}
	svc := NewEnvDispatchService(f, 1).
		WithSandboxLifecycle(creator).
		WithSweLegoTemplateResolver(resolver)

	res, err := svc.Dispatch(context.Background(), EnvDispatchInput{
		WorkspaceID: "ws", UserID: "u", Mode: EnvModeScratch, EnvID: baseEnv,
		Domain: EnvDomainSweLego, DispatchType: EnvDispatchIssue, GroupSize: 2,
		AgentID: "ag", SourceTaskID: "source-issue",
	})
	if err != nil {
		t.Fatalf("dispatch: %v", err)
	}
	if len(resolver.calls) != 1 {
		t.Fatalf("resolver calls = %d, want 1", len(resolver.calls))
	}
	req := resolver.calls[0]
	if req.SourceTaskID != "source-issue" || req.WorkspaceID != "ws" || req.UserID != "u" {
		t.Fatalf("resolver request identity = %+v", req)
	}
	if req.RepoURL != "https://example.test/repo.git" || req.BaseCommit != "abc123" || req.IssueDate != "2025-01-02T03:04:05Z" {
		t.Fatalf("resolver request payload = %+v", req)
	}
	if len(creator.calls) != 2 {
		t.Fatalf("sandbox creates = %d, want 2 (one per rollout)", len(creator.calls))
	}
	for i, call := range creator.calls {
		if call.Template != "tpl-task" {
			t.Fatalf("rollout %d sandbox template = %q, want tpl-task", i, call.Template)
		}
	}
	if len(res.Rollouts) != 2 || res.Rollouts[0].AgentRunID == "" {
		t.Fatalf("rollouts = %+v", res.Rollouts)
	}
}

// A cache that is not ready (missing / building / failed) fails the dispatch
// with an actionable validation error before any fan-out — the resolver is
// read-only, it never builds.
func TestDispatch_ScratchSweLegoIssueSource_NotReadyCacheFailsValidation(t *testing.T) {
	cases := []struct {
		name      string
		status    string
		wantState string
	}{
		{name: "missing", status: "", wantState: "not built"},
		{name: "building", status: "building", wantState: "building"},
		{name: "failed", status: "failed", wantState: "failed"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			f := newFakeEnvDispatchDeps()
			baseEnv := f.seedBaseEnv()
			seedSweLegoIssueSource(f, "source-issue")
			resolver := &fakeSweLegoTemplateResolver{status: tc.status}
			svc := NewEnvDispatchService(f, 1).WithSweLegoTemplateResolver(resolver)

			_, err := svc.Dispatch(context.Background(), EnvDispatchInput{
				WorkspaceID: "ws", UserID: "u", Mode: EnvModeScratch, EnvID: baseEnv,
				Domain: EnvDomainSweLego, DispatchType: EnvDispatchIssue, GroupSize: 1,
				AgentID: "ag", SourceTaskID: "source-issue",
			})
			if err == nil {
				t.Fatalf("dispatch must fail when the cache is %s", tc.wantState)
			}
			for _, want := range []string{"validation_failed", "not materialized", "source-issue", tc.wantState, "materialize first"} {
				if !strings.Contains(err.Error(), want) {
					t.Fatalf("error = %v, want it to contain %q", err, want)
				}
			}
			if len(f.createdIssues) != 0 || len(f.projects) != 0 {
				t.Fatalf("not-ready cache must abort before fan-out: issues=%v projects=%v", f.createdIssues, f.projects)
			}
		})
	}
}

// A resolver lookup failure aborts the dispatch before any fan-out with a
// server-side (non-validation) error carrying context.
func TestDispatch_ScratchSweLegoIssueSource_ResolverFailureFailsDispatch(t *testing.T) {
	f := newFakeEnvDispatchDeps()
	baseEnv := f.seedBaseEnv()
	seedSweLegoIssueSource(f, "source-issue")
	resolver := &fakeSweLegoTemplateResolver{err: fmt.Errorf("cache lookup unavailable")}
	svc := NewEnvDispatchService(f, 1).WithSweLegoTemplateResolver(resolver)

	_, err := svc.Dispatch(context.Background(), EnvDispatchInput{
		WorkspaceID: "ws", UserID: "u", Mode: EnvModeScratch, EnvID: baseEnv,
		Domain: EnvDomainSweLego, DispatchType: EnvDispatchIssue, GroupSize: 1,
		AgentID: "ag", SourceTaskID: "source-issue",
	})
	if err == nil || !strings.Contains(err.Error(), "resolve swe_lego task template") {
		t.Fatalf("error = %v, want resolve context", err)
	}
	if strings.Contains(err.Error(), "validation_failed") {
		t.Fatalf("resolver failure must not be a validation error: %v", err)
	}
	if len(f.createdIssues) != 0 || len(f.projects) != 0 {
		t.Fatalf("resolver failure must abort before fan-out: issues=%v projects=%v", f.createdIssues, f.projects)
	}
}

// The parent template prefers the one recorded on the instance-backed base
// env's sandbox instance.
func TestDispatch_ScratchSweLegoIssueSource_ParentTemplateFromBaseEnvInstance(t *testing.T) {
	f := newFakeEnvDispatchDeps()
	baseEnv := f.seedBaseEnv()
	seedSweLegoIssueSource(f, "source-issue")
	creator := &fakeSandboxInstanceCreator{
		refs: map[string]SandboxInstanceRef{
			"base-sbx": {InstanceID: "base-sbx", WorkspaceID: "ws", Template: "tpl-parent"},
		},
	}
	resolver := &fakeSweLegoTemplateResolver{templateID: "tpl-task", status: "ready"}
	svc := NewEnvDispatchService(f, 1).
		WithSandboxLifecycle(creator).
		WithSweLegoTemplateResolver(resolver)

	_, err := svc.Dispatch(context.Background(), EnvDispatchInput{
		WorkspaceID: "ws", UserID: "u", Mode: EnvModeScratch, EnvID: baseEnv,
		Domain: EnvDomainSweLego, DispatchType: EnvDispatchIssue, GroupSize: 1,
		AgentID: "ag", SourceTaskID: "source-issue",
	})
	if err != nil {
		t.Fatalf("dispatch: %v", err)
	}
	if len(resolver.calls) != 1 || resolver.calls[0].ParentTemplateID != "tpl-parent" {
		t.Fatalf("resolver parent template = %+v", resolver.calls)
	}
}

// Message source tasks never touch the template cache, even with a resolver
// installed.
func TestDispatch_ScratchSelfPlayMessageSource_SkipsTemplateLookup(t *testing.T) {
	f := newFakeEnvDispatchDeps()
	baseEnv := f.seedBaseEnv()
	f.sourceTasks["source-message"] = SourceTask{
		ID: "source-message", WorkspaceID: "ws", Type: SourceTaskMessage,
		Payload: json.RawMessage(`{"content":"hello"}`),
	}
	resolver := &fakeSweLegoTemplateResolver{templateID: "tpl-task", status: "ready"}
	svc := NewEnvDispatchService(f, 1).WithSweLegoTemplateResolver(resolver)

	_, err := svc.Dispatch(context.Background(), EnvDispatchInput{
		WorkspaceID: "ws", UserID: "u", Mode: EnvModeScratch, EnvID: baseEnv,
		Domain: EnvDomainSelfPlay, DispatchType: EnvDispatchMessage, GroupSize: 1,
		AgentID: "ag", SourceTaskID: "source-message",
	})
	if err != nil {
		t.Fatalf("dispatch: %v", err)
	}
	if len(resolver.calls) != 0 {
		t.Fatalf("message source must not resolve templates, calls = %+v", resolver.calls)
	}
}

// Branch dispatches inherit the source task but never touch the template
// cache.
func TestDispatch_BranchSweLegoIssueSource_SkipsTemplateLookup(t *testing.T) {
	f := newFakeEnvDispatchDeps()
	const stateEnv = "state-env"
	f.envs[stateEnv] = Env{ID: stateEnv, SandboxIDs: []string{"state-sandbox"}, Mode: EnvModeBranch, Domain: EnvDomainSweLego}
	f.projects["source-project"] = stateEnv
	f.issues["source-project"] = []IssueRow{{ID: "source-issue", ProjectID: "source-project"}}
	seedSweLegoIssueSource(f, "source-task")
	f.dispatchRuns["source-project"] = fakeEnvDispatchRun{WorkspaceID: "ws", SourceTaskID: "source-task", RunID: "source-run"}
	resolver := &fakeSweLegoTemplateResolver{templateID: "tpl-task", status: "ready"}
	svc := NewEnvDispatchService(f, 1).WithSweLegoTemplateResolver(resolver)

	_, err := svc.Dispatch(context.Background(), EnvDispatchInput{
		WorkspaceID: "ws", UserID: "u", Mode: EnvModeBranch, EnvID: stateEnv,
		Domain: EnvDomainSweLego, DispatchType: EnvDispatchIssue, GroupSize: 1, AgentID: "ag",
	})
	if err != nil {
		t.Fatalf("dispatch: %v", err)
	}
	if len(resolver.calls) != 0 {
		t.Fatalf("branch must not resolve templates, calls = %+v", resolver.calls)
	}
}

// An issue source without repo_url/base_commit/issue_date is rejected as a
// validation error before the resolver is invoked.
func TestDispatch_ScratchSweLegoIssueSource_MissingRepoFieldsRejected(t *testing.T) {
	f := newFakeEnvDispatchDeps()
	baseEnv := f.seedBaseEnv()
	f.sourceTasks["source-issue"] = SourceTask{
		ID: "source-issue", WorkspaceID: "ws", Type: SourceTaskIssue,
		Payload: json.RawMessage(`{"title":"t","description":"d"}`),
	}
	resolver := &fakeSweLegoTemplateResolver{templateID: "tpl-task", status: "ready"}
	svc := NewEnvDispatchService(f, 1).WithSweLegoTemplateResolver(resolver)

	_, err := svc.Dispatch(context.Background(), EnvDispatchInput{
		WorkspaceID: "ws", UserID: "u", Mode: EnvModeScratch, EnvID: baseEnv,
		Domain: EnvDomainSweLego, DispatchType: EnvDispatchIssue, GroupSize: 1,
		AgentID: "ag", SourceTaskID: "source-issue",
	})
	if err == nil || !strings.Contains(err.Error(), "validation_failed") || !strings.Contains(err.Error(), "repo_url") {
		t.Fatalf("error = %v, want validation_failed naming repo_url", err)
	}
	if len(resolver.calls) != 0 {
		t.Fatalf("resolver must not run on invalid payload, calls = %+v", resolver.calls)
	}
}

// Without an injected resolver the dispatch keeps today's behavior: no
// TaskTemplateID, plain Fleet-backed sandboxes.
func TestDispatch_ScratchSweLegoIssueSource_NilResolverPreservesBehavior(t *testing.T) {
	f := newFakeEnvDispatchDeps()
	baseEnv := f.seedBaseEnv()
	seedSweLegoIssueSource(f, "source-issue")
	svc := NewEnvDispatchService(f, 1)

	res, err := svc.Dispatch(context.Background(), EnvDispatchInput{
		WorkspaceID: "ws", UserID: "u", Mode: EnvModeScratch, EnvID: baseEnv,
		Domain: EnvDomainSweLego, DispatchType: EnvDispatchIssue, GroupSize: 1,
		AgentID: "ag", SourceTaskID: "source-issue",
	})
	if err != nil {
		t.Fatalf("dispatch: %v", err)
	}
	if len(res.Rollouts) != 1 || len(res.Rollouts[0].SandboxRefs) != 0 {
		t.Fatalf("nil resolver must keep the Fleet path, rollouts = %+v", res.Rollouts)
	}
	if len(f.sandboxes) != 1 {
		t.Fatalf("nil resolver should fork 1 Fleet sandbox, got %d", len(f.sandboxes))
	}
}
