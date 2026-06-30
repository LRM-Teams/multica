package service

import (
	"context"
	"fmt"
)

// SweLegoIssueInput is the service-layer input for the atomic create.
type SweLegoIssueInput struct {
	RepoURL             string
	BaseCommit          string
	IssueDate           string
	IssueTitle          string
	IssueText           string
	AcceptanceCriteria  string
	FailToPass          []string
	PassToPass          []string
	GroupSize           int
	AgentConfigID       string
	BaseImage           string
}

// SweLegoIssueResult is the service-layer output (mirrors the 201 response).
type SweLegoIssueResult struct {
	ProjectID            string
	IssueID              string
	ImageID              string
	BuildNodeID          string
	BaseSandboxID        string
	BaseSandboxRuntimeID string
	AgentRunIDs          []string
}

// SweLegoDeps is the seam between the service and the DB + cloud runtime.
// Each method corresponds to one step of the orchestration (spec §4.2).
// Production wires this to real queries + cloudRuntimeProxy; tests inject
// a fake.
type SweLegoDeps interface {
	CreateProject(ctx context.Context, name string) (string, error)
	CreateIssue(ctx context.Context, projectID, title, body, criteria string, f2p, p2p []string) (string, error)
	BuildImage(ctx context.Context, repoURL, baseCommit, issueDate, baseImage string) (imageRef, nodeID string, err error)
	BootBaseSandbox(ctx context.Context, imageRef, nodeID string) (sandboxID, runtimeID string, err error)
	ForkSandbox(ctx context.Context, sourceSandboxID string, idx int) (string, error)
	EnqueueAgentRun(ctx context.Context, issueID, sandboxID string, idx int) (string, error)
	DeleteSandbox(ctx context.Context, sandboxID string) error
	DeleteProject(ctx context.Context, projectID string) error
}

// SweLegoIssueService orchestrates the atomic per-issue setup (spec §4.2).
type SweLegoIssueService struct {
	deps SweLegoDeps
}

func NewSweLegoIssueService(deps SweLegoDeps) *SweLegoIssueService {
	return &SweLegoIssueService{deps: deps}
}

// Create runs the six-step sequence. On any failure after the project is
// created, it rolls back: deletes forked sandboxes, the base sandbox, and
// the project. The built image stays cached on the build node (spec §4.1
// atomicity contract — image build is expensive).
func (s *SweLegoIssueService) Create(ctx context.Context, in SweLegoIssueInput) (SweLegoIssueResult, error) {
	projectID, err := s.deps.CreateProject(ctx, fmt.Sprintf("swe-lego-%s-%s", sanitizeRepoName(in.RepoURL), shortSHA(in.BaseCommit)))
	if err != nil {
		return SweLegoIssueResult{}, fmt.Errorf("create project: %w", err)
	}
	issueID, imageRef, nodeID, baseSandboxID, baseRuntimeID, runIDs, forkedIDs, err := s.createAfterProject(ctx, projectID, in)
	if err != nil {
		s.rollback(ctx, projectID, baseSandboxID, forkedIDs)
		return SweLegoIssueResult{}, err
	}
	return SweLegoIssueResult{
		ProjectID: projectID, IssueID: issueID, ImageID: imageRef,
		BuildNodeID: nodeID, BaseSandboxID: baseSandboxID,
		BaseSandboxRuntimeID: baseRuntimeID, AgentRunIDs: runIDs,
	}, nil
}

func (s *SweLegoIssueService) createAfterProject(ctx context.Context, projectID string, in SweLegoIssueInput) (issueID, imageRef, nodeID, baseSandboxID, baseRuntimeID string, runIDs, forkedIDs []string, err error) {
	// 2. CreateIssue
	issueID, err = s.deps.CreateIssue(ctx, projectID, in.IssueTitle, in.IssueText, in.AcceptanceCriteria, in.FailToPass, in.PassToPass)
	if err != nil {
		return "", "", "", "", "", nil, nil, fmt.Errorf("create issue: %w", err)
	}
	// 3. BuildImage
	imageRef, nodeID, err = s.deps.BuildImage(ctx, in.RepoURL, in.BaseCommit, in.IssueDate, in.BaseImage)
	if err != nil {
		return issueID, "", "", "", "", nil, nil, fmt.Errorf("build image: %w", err)
	}
	// 4. BootBaseSandbox (on the same node that built the image)
	baseSandboxID, baseRuntimeID, err = s.deps.BootBaseSandbox(ctx, imageRef, nodeID)
	if err != nil {
		return issueID, imageRef, nodeID, "", "", nil, nil, fmt.Errorf("boot base sandbox: %w", err)
	}
	// 5. Fork × group_size + enqueue
	runIDs = make([]string, 0, in.GroupSize)
	forkedIDs = make([]string, 0, in.GroupSize)
	for i := 1; i <= in.GroupSize; i++ {
		forked, ferr := s.deps.ForkSandbox(ctx, baseSandboxID, i)
		if ferr != nil {
			return issueID, imageRef, nodeID, baseSandboxID, baseRuntimeID, runIDs, forkedIDs, fmt.Errorf("fork %d: %w", i, ferr)
		}
		runID, eerr := s.deps.EnqueueAgentRun(ctx, issueID, forked, i)
		if eerr != nil {
			// enqueue failed → clean up this forked sandbox too (not yet in forkedIDs).
			_ = s.deps.DeleteSandbox(ctx, forked)
			return issueID, imageRef, nodeID, baseSandboxID, baseRuntimeID, runIDs, forkedIDs, fmt.Errorf("enqueue %d: %w", i, eerr)
		}
		forkedIDs = append(forkedIDs, forked)
		runIDs = append(runIDs, runID)
	}
	return issueID, imageRef, nodeID, baseSandboxID, baseRuntimeID, runIDs, forkedIDs, nil
}

func (s *SweLegoIssueService) rollback(ctx context.Context, projectID, baseSandboxID string, forkedIDs []string) {
	// Best-effort. Logs are the caller's responsibility; this never raises.
	for _, id := range forkedIDs {
		_ = s.deps.DeleteSandbox(ctx, id)
	}
	if baseSandboxID != "" {
		_ = s.deps.DeleteSandbox(ctx, baseSandboxID)
	}
	_ = s.deps.DeleteProject(ctx, projectID)
}

// shortSHA returns the first 8 hex chars of a commit-ish, for the project
// name. Falls back to the full string if shorter.
func shortSHA(s string) string {
	if len(s) > 8 {
		return s[:8]
	}
	return s
}

// sanitizeRepoName strips URL scaffolding from a repo URL for use in a
// project name. "https://github.com/foo/bar" → "foo-bar".
func sanitizeRepoName(repoURL string) string {
	// Strip scheme.
	rest := repoURL
	for _, prefix := range []string{"https://", "http://", "git://", "ssh://"} {
		if len(rest) > len(prefix) && rest[:len(prefix)] == prefix {
			rest = rest[len(prefix):]
			break
		}
	}
	// Strip trailing .git.
	if len(rest) > 4 && rest[len(rest)-4:] == ".git" {
		rest = rest[:len(rest)-4]
	}
	// Replace path separators with dashes.
	out := make([]byte, 0, len(rest))
	for i := 0; i < len(rest); i++ {
		c := rest[i]
		if c == '/' || c == ':' || c == '@' {
			out = append(out, '-')
		} else {
			out = append(out, c)
		}
	}
	return string(out)
}
