package service

import (
	"context"
	"fmt"
	"strings"
	"testing"
)

type fakeSweTemplateDeps struct {
	cache           SweLegoTemplateCacheRecord
	hit, claimed    bool
	calls           []string
	snapshotErr     error
	failed, deleted bool
}

func (f *fakeSweTemplateDeps) GetCache(context.Context, string, string) (SweLegoTemplateCacheRecord, bool, error) {
	return f.cache, f.hit, nil
}
func (f *fakeSweTemplateDeps) ClaimBuild(context.Context, string, string, string) (bool, error) {
	return f.claimed, nil
}
func (f *fakeSweTemplateDeps) CreateBuilder(context.Context, SweLegoTemplateRequest) (string, string, error) {
	f.calls = append(f.calls, "create:tpl-parent")
	return "builder", "cube-builder", nil
}
func (f *fakeSweTemplateDeps) ExecBuilder(context.Context, string, string) error {
	f.calls = append(f.calls, "exec")
	return nil
}
func (f *fakeSweTemplateDeps) SnapshotBuilder(context.Context, string) (string, error) {
	f.calls = append(f.calls, "snapshot")
	if f.snapshotErr != nil {
		return "", f.snapshotErr
	}
	return "tpl-task", nil
}
func (f *fakeSweTemplateDeps) DeleteBuilder(context.Context, string) error {
	f.calls = append(f.calls, "delete")
	f.deleted = true
	return nil
}
func (f *fakeSweTemplateDeps) CompleteBuild(context.Context, string, string, string) error {
	f.calls = append(f.calls, "complete")
	return nil
}
func (f *fakeSweTemplateDeps) FailBuild(context.Context, string, string, string) error {
	f.failed = true
	return nil
}
func templateRequest() SweLegoTemplateRequest {
	return SweLegoTemplateRequest{WorkspaceID: "ws", UserID: "u", NodeID: "node", ParentTemplateID: "tpl-parent", SourceTaskID: "source", RepoURL: "https://example.test/repo.git", BaseCommit: "abc", IssueDate: "2025-01-01T00:00:00Z"}
}
func TestMaterializeCacheHitReturnsExistingTaskTemplate(t *testing.T) {
	f := &fakeSweTemplateDeps{hit: true, cache: SweLegoTemplateCacheRecord{Status: "ready", TaskTemplateID: "tpl-task"}}
	got, err := NewSweLegoTemplateMaterializer(f).Materialize(context.Background(), templateRequest())
	if err != nil || got != "tpl-task" || len(f.calls) != 0 {
		t.Fatalf("got=%q err=%v calls=%v", got, err, f.calls)
	}
}
func TestMaterializeBuildsFromParentThenSnapshots(t *testing.T) {
	f := &fakeSweTemplateDeps{claimed: true}
	got, err := NewSweLegoTemplateMaterializer(f).Materialize(context.Background(), templateRequest())
	if err != nil || got != "tpl-task" {
		t.Fatalf("got=%q err=%v", got, err)
	}
	if strings.Join(f.calls, ",") != "create:tpl-parent,exec,snapshot,complete,delete" {
		t.Fatalf("calls=%v", f.calls)
	}
}
func TestMaterializeFailureCleansBuilderAndMarksCacheFailed(t *testing.T) {
	f := &fakeSweTemplateDeps{claimed: true, snapshotErr: fmt.Errorf("snapshot unavailable")}
	_, err := NewSweLegoTemplateMaterializer(f).Materialize(context.Background(), templateRequest())
	if err == nil || !strings.Contains(err.Error(), "snapshot") || !f.failed || !f.deleted {
		t.Fatalf("err=%v failed=%v deleted=%v", err, f.failed, f.deleted)
	}
}
func TestSweLegoBuilderScriptNeverUsesLegacyDockerBase(t *testing.T) {
	script, err := SweLegoBuilderScript(templateRequest())
	if err != nil {
		t.Fatal(err)
	}
	for _, bad := range []string{"swe-lego/python", "COPY multica-daemon", "docker build"} {
		if strings.Contains(script, bad) {
			t.Fatalf("legacy docker path: %s", bad)
		}
	}
	for _, want := range []string{"~/.multica/daemon.id", "~/.git-credentials", "git filter-repo"} {
		if !strings.Contains(script, want) {
			t.Fatalf("missing %s", want)
		}
	}
}
