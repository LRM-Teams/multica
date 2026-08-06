package service

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"strings"
	"time"
)

const sweLegoTemplateBuildTimeout = 20 * time.Minute

type SweLegoTemplateRequest struct {
	WorkspaceID, UserID, NodeID    string
	ParentTemplateID               string
	SourceTaskID                   string
	RepoURL, BaseCommit, IssueDate string
}

type SweLegoTemplateCacheRecord struct {
	Status, ParentTemplateID, TaskTemplateID, Error string
}

// SweLegoTemplateMaterializer derives a task-specific Cube template from the
// selected ready parent template. It never chooses a Docker image or starts a
// daemon in the builder sandbox. Building happens ONLY via the manual warm-up
// endpoint (POST /api/v1/source-tasks/{id}/materialize); the dispatch path
// never builds — it reads the ready cache through SweLegoTemplateResolver.
type SweLegoTemplateMaterializer interface {
	Materialize(context.Context, SweLegoTemplateRequest) (string, error)
}

// SweLegoTemplateResolver is the read-only counterpart of the materializer
// used on the dispatch path: it resolves node placement + the parent
// template, then reads the node-local cache. LookupReadyTemplate returns the
// ready task template id, or "" plus the cache status ("building", "failed",
// or "" when no row exists) for diagnostics. It performs exactly one cache
// read and never triggers a builder/exec/snapshot.
type SweLegoTemplateResolver interface {
	LookupReadyTemplate(ctx context.Context, req SweLegoTemplateRequest) (templateID, status string, err error)
}

type SweLegoTemplateMaterializerDeps interface {
	GetCache(context.Context, string, string) (SweLegoTemplateCacheRecord, bool, error)
	ClaimBuild(context.Context, string, string, string) (claimed bool, err error)
	CreateBuilder(context.Context, SweLegoTemplateRequest) (builderID, localRef string, err error)
	ExecBuilder(context.Context, string, string) error
	SnapshotBuilder(context.Context, string) (string, error)
	DeleteBuilder(context.Context, string) error
	CompleteBuild(context.Context, string, string, string) error
	FailBuild(context.Context, string, string, string) error
}

type sweLegoTemplateMaterializer struct {
	deps SweLegoTemplateMaterializerDeps
}

func NewSweLegoTemplateMaterializer(deps SweLegoTemplateMaterializerDeps) SweLegoTemplateMaterializer {
	return &sweLegoTemplateMaterializer{deps: deps}
}

func SweLegoTemplateCacheKey(repoURL, baseCommit, issueDate, parentTemplate string) string {
	sum := sha256.Sum256([]byte(repoURL + "|" + baseCommit + "|" + issueDate + "|" + parentTemplate))
	return hex.EncodeToString(sum[:])
}

func (m *sweLegoTemplateMaterializer) Materialize(ctx context.Context, req SweLegoTemplateRequest) (string, error) {
	if err := validateSweLegoTemplateRequest(req); err != nil {
		return "", err
	}
	key := SweLegoTemplateCacheKey(req.RepoURL, req.BaseCommit, req.IssueDate, req.ParentTemplateID)
	if cached, ok, err := m.deps.GetCache(ctx, req.NodeID, key); err != nil {
		return "", fmt.Errorf("get template cache: %w", err)
	} else if ok && cached.Status == "ready" && cached.TaskTemplateID != "" {
		return cached.TaskTemplateID, nil
	}
	claimed, err := m.deps.ClaimBuild(ctx, req.NodeID, key, req.ParentTemplateID)
	if err != nil {
		return "", fmt.Errorf("claim template build: %w", err)
	}
	if !claimed {
		return "", fmt.Errorf("template build already in progress for node-local cache key %s", key)
	}
	builderID := ""
	fail := func(cause error) (string, error) {
		_ = m.deps.FailBuild(context.WithoutCancel(ctx), req.NodeID, key, safeTemplateError(cause))
		if builderID != "" {
			_ = m.deps.DeleteBuilder(context.WithoutCancel(ctx), builderID)
		}
		return "", cause
	}
	var localRef string
	builderID, localRef, err = m.deps.CreateBuilder(ctx, req)
	if err != nil {
		return fail(fmt.Errorf("create builder: %w", err))
	}
	script, err := SweLegoBuilderScript(req)
	if err != nil {
		return fail(err)
	}
	buildCtx, cancel := context.WithTimeout(ctx, sweLegoTemplateBuildTimeout)
	defer cancel()
	if err := m.deps.ExecBuilder(buildCtx, localRef, script); err != nil {
		return fail(fmt.Errorf("execute builder: %w", err))
	}
	templateID, err := m.deps.SnapshotBuilder(buildCtx, builderID)
	if err != nil {
		return fail(fmt.Errorf("snapshot builder: %w", err))
	}
	if err := m.deps.CompleteBuild(ctx, req.NodeID, key, templateID); err != nil {
		return fail(fmt.Errorf("complete template cache: %w", err))
	}
	_ = m.deps.DeleteBuilder(context.WithoutCancel(ctx), builderID)
	return templateID, nil
}

func validateSweLegoTemplateRequest(req SweLegoTemplateRequest) error {
	if req.WorkspaceID == "" || req.UserID == "" || req.NodeID == "" || req.ParentTemplateID == "" || req.SourceTaskID == "" || req.RepoURL == "" || req.BaseCommit == "" {
		return fmt.Errorf("workspace, user, node, parent template, source task, repo URL, and base commit are required")
	}
	if _, err := time.Parse(time.RFC3339, req.IssueDate); err != nil {
		return fmt.Errorf("parse issue_date: %w", err)
	}
	return nil
}

// SweLegoBuilderScript runs inside a non-daemon builder derived from the active
// Cube parent. It deliberately removes credentials, daemon identity, and build
// leftovers before snapshotting while retaining Pi and Multica installed by the parent.
func SweLegoBuilderScript(req SweLegoTemplateRequest) (string, error) {
	if err := validateSweLegoTemplateRequest(req); err != nil {
		return "", err
	}
	cutoff, _ := time.Parse(time.RFC3339, req.IssueDate)
	return fmt.Sprintf("set -euo pipefail\nrm -rf /tmp/swe-lego-build /workspace/repo\ngit clone --filter=blob:none %s /workspace/repo\ncd /workspace/repo\ngit fetch origin %s\ngit checkout %s\ngit filter-repo --force --commit-callback %s\npython -m pip install -e . 2>/dev/null || true\nrm -f ~/.multica/daemon.id ~/.git-credentials\nrm -rf /tmp/swe-lego-build\n", shellQuote(req.RepoURL), shellQuote(req.BaseCommit), shellQuote(req.BaseCommit), shellQuote(fmt.Sprintf("if int(commit.committer_date.split()[0]) > %d: commit.skip()", cutoff.Unix()))), nil
}

func safeTemplateError(err error) string { return strings.TrimSpace(err.Error()) }
