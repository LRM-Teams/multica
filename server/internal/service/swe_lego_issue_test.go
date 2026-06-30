package service

import (
	"context"
	"errors"
	"testing"
)

// fakeSweLegoDeps isolates the service from the DB + cloud runtime. Each
// field records calls so the test can assert ordering and rollback.
type fakeSweLegoDeps struct {
	createdProjectID string
	createdIssueID   string

	buildImageRef string
	buildNodeID   string
	buildErr      error

	bootedSandboxID string
	bootedRuntimeID string
	bootErr         error

	forkedSandboxIDs []string
	forkErrOn        int // 1-based index that fails; 0 = none
	deletedSandboxes []string

	enqueuedRunIDs []string
	enqueueErrOn   int // 1-based index that fails; 0 = none

	deletedProject bool
}

func (f *fakeSweLegoDeps) CreateProject(ctx context.Context, name string) (string, error) {
	f.createdProjectID = "proj-1"
	return f.createdProjectID, nil
}
func (f *fakeSweLegoDeps) CreateIssue(ctx context.Context, projectID, title, body, criteria string, f2p, p2p []string) (string, error) {
	f.createdIssueID = "issue-1"
	return f.createdIssueID, nil
}
func (f *fakeSweLegoDeps) BuildImage(ctx context.Context, repoURL, baseCommit, issueDate, baseImage string) (imageRef, nodeID string, err error) {
	if f.buildErr != nil {
		return "", "", f.buildErr
	}
	f.buildImageRef = "swe-lego:deadbeef"
	f.buildNodeID = "node-1"
	return f.buildImageRef, f.buildNodeID, nil
}
func (f *fakeSweLegoDeps) BootBaseSandbox(ctx context.Context, imageRef, nodeID string) (sandboxID, runtimeID string, err error) {
	if f.bootErr != nil {
		return "", "", f.bootErr
	}
	f.bootedSandboxID = "sbx-base"
	f.bootedRuntimeID = "rt-base"
	return f.bootedSandboxID, f.bootedRuntimeID, nil
}
func (f *fakeSweLegoDeps) ForkSandbox(ctx context.Context, sourceSandboxID string, idx int) (string, error) {
	if f.forkErrOn != 0 && idx == f.forkErrOn {
		return "", errors.New("fork failed")
	}
	id := "sbx-fork-" + itoa(idx)
	f.forkedSandboxIDs = append(f.forkedSandboxIDs, id)
	return id, nil
}
func (f *fakeSweLegoDeps) EnqueueAgentRun(ctx context.Context, issueID, sandboxID string, idx int) (string, error) {
	if f.enqueueErrOn != 0 && idx == f.enqueueErrOn {
		return "", errors.New("enqueue failed")
	}
	id := "run-" + itoa(idx)
	f.enqueuedRunIDs = append(f.enqueuedRunIDs, id)
	return id, nil
}
func (f *fakeSweLegoDeps) DeleteSandbox(ctx context.Context, sandboxID string) error {
	f.deletedSandboxes = append(f.deletedSandboxes, sandboxID)
	return nil
}
func (f *fakeSweLegoDeps) DeleteProject(ctx context.Context, projectID string) error {
	f.deletedProject = true
	return nil
}

func itoa(i int) string {
	// avoid pulling in strconv for the test fake
	return string(rune('0' + i)) // single-digit only; tests use group_size <= 3
}

func TestSweLegoIssueService_HappyPath(t *testing.T) {
	ctx := context.Background()
	deps := &fakeSweLegoDeps{}
	svc := NewSweLegoIssueService(deps)
	res, err := svc.Create(ctx, SweLegoIssueInput{
		RepoURL: "r", BaseCommit: "c", IssueDate: "d", IssueTitle: "t", IssueText: "x",
		AcceptanceCriteria: "a", FailToPass: []string{"f"}, PassToPass: []string{"p"},
		GroupSize: 2, AgentConfigID: "ag", BaseImage: "swe-lego/python:3.11",
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if res.ProjectID != "proj-1" || res.IssueID != "issue-1" {
		t.Fatalf("unexpected ids: %+v", res)
	}
	if len(res.AgentRunIDs) != 2 {
		t.Fatalf("expected 2 agent runs, got %d", len(res.AgentRunIDs))
	}
	if res.BuildNodeID != "node-1" || res.BaseSandboxID != "sbx-base" {
		t.Fatalf("expected node-1/sbx-base, got %s/%s", res.BuildNodeID, res.BaseSandboxID)
	}
	// No rollback on success.
	if deps.deletedProject || len(deps.deletedSandboxes) != 0 {
		t.Fatalf("unexpected rollback on success: project=%v sandboxes=%v", deps.deletedProject, deps.deletedSandboxes)
	}
}

func TestSweLegoIssueService_BuildFailureRollsBackProject(t *testing.T) {
	ctx := context.Background()
	deps := &fakeSweLegoDeps{buildErr: errors.New("filter-repo failed")}
	svc := NewSweLegoIssueService(deps)
	_, err := svc.Create(ctx, SweLegoIssueInput{
		RepoURL: "r", BaseCommit: "c", IssueDate: "d", IssueTitle: "t", IssueText: "x",
		AcceptanceCriteria: "a", FailToPass: []string{"f"}, PassToPass: []string{"p"},
		GroupSize: 2, AgentConfigID: "ag", BaseImage: "b",
	})
	if err == nil {
		t.Fatal("expected error on build failure")
	}
	// Project was created before the build; it must be rolled back.
	if !deps.deletedProject {
		t.Fatal("expected project rollback on build failure")
	}
	// No sandbox should have been booted or forked.
	if deps.bootedSandboxID != "" || len(deps.forkedSandboxIDs) != 0 {
		t.Fatal("expected no sandbox boot/fork on build failure")
	}
}

func TestSweLegoIssueService_ForkFailureRollsBackSandboxesAndProject(t *testing.T) {
	ctx := context.Background()
	deps := &fakeSweLegoDeps{forkErrOn: 2} // the second fork fails
	svc := NewSweLegoIssueService(deps)
	_, err := svc.Create(ctx, SweLegoIssueInput{
		RepoURL: "r", BaseCommit: "c", IssueDate: "d", IssueTitle: "t", IssueText: "x",
		AcceptanceCriteria: "a", FailToPass: []string{"f"}, PassToPass: []string{"p"},
		GroupSize: 2, AgentConfigID: "ag", BaseImage: "b",
	})
	if err == nil {
		t.Fatal("expected error on fork failure")
	}
	// The first fork succeeded, then the second failed → rollback the first
	// fork + the base sandbox + the project.
	if len(deps.deletedSandboxes) < 2 {
		t.Fatalf("expected >= 2 sandbox deletes (fork + base), got %v", deps.deletedSandboxes)
	}
	if !deps.deletedProject {
		t.Fatal("expected project rollback on fork failure")
	}
}
