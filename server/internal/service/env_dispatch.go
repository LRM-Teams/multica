package service

import (
	"context"
	"fmt"
	"sync"
)

// EnvMode enumerates the reset modes (spec §4.2).
type EnvMode string

const (
	EnvModeScratch EnvMode = "scratch"
	EnvModeBranch  EnvMode = "branch"
)

// EnvDomain enumerates the dispatch domains (spec §4.2). Required on dispatch;
// each domain pins a dispatch_type (swe_lego⇒issue, self_play⇒message).
type EnvDomain string

const (
	EnvDomainSweLego  EnvDomain = "swe_lego"
	EnvDomainSelfPlay EnvDomain = "self_play"
)

// EnvDispatchType enumerates the dispatch types.
type EnvDispatchType string

const (
	EnvDispatchIssue   EnvDispatchType = "issue"
	EnvDispatchMessage EnvDispatchType = "message"
)

// EnvDispatchInput is the service-layer input for the unified dispatch.
type EnvDispatchInput struct {
	WorkspaceID     string
	UserID          string // creator/actor
	Mode            EnvMode
	EnvID           string    // base env (scratch) or state env (branch)
	SourceProjectID string    // branch only: the single project on EnvID (1:1 invariant), resolved by the handler
	Domain          EnvDomain // required
	DispatchType    EnvDispatchType
	GroupSize       int
	AgentID         string
	IdempotencyKey  string // optional; dedupes retries (spec §7.7)

	// Issue dispatch (required for scratch+swe_lego; forbidden for
	// branch+swe_lego where the copied issue is reused).
	Issue *IssueInput

	// Message dispatch (required for self_play).
	Message *MessageInput
}

type IssueInput struct {
	Title              string
	Description        string
	AcceptanceCriteria []string
	FailToPass         []string
	PassToPass         []string
}

type MessageInput struct {
	Content string
}

// EnvRollout is one element of the response array (spec §6.3).
type EnvRollout struct {
	EnvID         string // always a new env_id (branch always forks, incl. N=1)
	ProjectID     string
	IssueID       string // empty iff dispatch_type=message
	ChatSessionID string // empty iff dispatch_type=issue
	AgentRunID    string // empty if dispatch failed (partial rollout)
	Error         string // empty if rollout succeeded
}

// EnvDispatchResult wraps the rollouts slice.
type EnvDispatchResult struct {
	Rollouts []EnvRollout
}

// EnvDispatchDeps is the seam between the service and the DB + cloud runtime.
// Production wires this to real queries + cloudRuntimeProxy; tests inject a fake.
type EnvDispatchDeps interface {
	// Environment operations
	GetEnv(ctx context.Context, envID, workspaceID string) (Env, error)
	CreateEnv(ctx context.Context, workspaceID, sandboxID, parentEnvID string, mode EnvMode, domain EnvDomain) (envID string, err error)
	DeleteEnv(ctx context.Context, envID, workspaceID string) error

	// Sandbox operations (proxy to cloud-runtime/Fleet)
	ForkSandbox(ctx context.Context, sourceSandboxID string, idx int) (sandboxID string, err error)
	DeleteSandbox(ctx context.Context, sandboxID string) error
	BootSandbox(ctx context.Context, imageRef string) (sandboxID string, err error) // for POST /api/v1/env

	// Project operations
	GetProjectByEnvID(ctx context.Context, envID, workspaceID string) (projectID string, err error) // branch: resolve source env → its single project (1:1 invariant)
	CreateProject(ctx context.Context, workspaceID, name, envID string) (projectID string, err error)
	// CopyProjectSubtree deep-copies issues + chat sessions + messages under a
	// new project bound to envID; returns source→copied ID maps so dispatch can
	// target the copied issue (branch+swe_lego) or copied session (branch+self_play).
	CopyProjectSubtree(ctx context.Context, sourceProjectID, workspaceID, envID string) (newProjectID string, issueIDMap, chatSessionIDMap map[string]string, err error)
	DeleteProject(ctx context.Context, projectID, workspaceID string) error

	// Issue operations
	ListIssuesByProject(ctx context.Context, projectID, workspaceID string) ([]IssueRow, error)
	CreateIssue(ctx context.Context, projectID, workspaceID, creatorID, title, description string, acceptanceCriteria, failToPass, passToPass []string) (issueID string, err error)

	// Chat operations
	CreateChatSession(ctx context.Context, projectID, workspaceID, agentID, creatorID string) (sessionID string, err error)
	CreateChatMessage(ctx context.Context, sessionID, role, content string) (messageID string, err error)

	// Agent run
	EnqueueAgentRun(ctx context.Context, workspaceID, agentID, issueID, chatSessionID, sandboxID string, idx int) (runID string, err error)

	// Idempotency ledger (spec §7.7). GetIdempotentResponse returns ok=false
	// when the key is unseen; SaveIdempotentResponse persists the response for
	// replay. Both are workspace-scoped.
	GetIdempotentResponse(ctx context.Context, workspaceID, key string) (EnvDispatchResult, bool, error)
	SaveIdempotentResponse(ctx context.Context, workspaceID, key string, res EnvDispatchResult) error
}

// Env is a snapshot of an environment row.
type Env struct {
	ID          string
	WorkspaceID string
	SandboxID   string
	ParentEnvID string // empty for base
	Mode        EnvMode
	Domain      EnvDomain
}

// IssueRow is a snapshot of an issue row (subset needed by the service).
type IssueRow struct {
	ID          string
	ProjectID   string
	Title       string
	Description string
}

// EnvDispatchService orchestrates reset → dispatch (spec §7).
type EnvDispatchService struct {
	deps        EnvDispatchDeps
	concurrency int
}

func NewEnvDispatchService(deps EnvDispatchDeps, concurrency int) *EnvDispatchService {
	if concurrency < 1 {
		concurrency = 8
	}
	return &EnvDispatchService{deps: deps, concurrency: concurrency}
}

// ErrAllDispatchFailed signals reset succeeded but every rollout's dispatch
// failed (spec §8 → 500). The returned result still carries rollouts[] so the
// caller can see the created envs/projects to clean up.
var ErrAllDispatchFailed = fmt.Errorf("dispatch_failed: all rollouts failed")

// Dispatch runs the unified dispatch flow.
func (s *EnvDispatchService) Dispatch(ctx context.Context, in EnvDispatchInput) (EnvDispatchResult, error) {
	if err := s.validate(in); err != nil {
		return EnvDispatchResult{}, err
	}

	// Idempotency replay (spec §7.7): a repeat key returns the stored response.
	if in.IdempotencyKey != "" {
		if prev, ok, err := s.deps.GetIdempotentResponse(ctx, in.WorkspaceID, in.IdempotencyKey); err != nil {
			return EnvDispatchResult{}, fmt.Errorf("idempotency lookup: %w", err)
		} else if ok {
			return prev, nil
		}
	}

	env, err := s.deps.GetEnv(ctx, in.EnvID, in.WorkspaceID)
	if err != nil {
		return EnvDispatchResult{}, fmt.Errorf("get env: %w", err)
	}

	// Branch: resolve the single source project on this env (spec §7.2 step 0).
	// The 1:1 unique index guarantees exactly one; a base env has none → error.
	if in.Mode == EnvModeBranch && in.SourceProjectID == "" {
		pid, err := s.deps.GetProjectByEnvID(ctx, in.EnvID, in.WorkspaceID)
		if err != nil {
			return EnvDispatchResult{}, fmt.Errorf("validation_failed: resolve source project: %w", err)
		}
		in.SourceProjectID = pid
	}

	rollouts := make([]EnvRollout, in.GroupSize)
	sem := make(chan struct{}, s.concurrency)
	var wg sync.WaitGroup
	var resetErrs []error
	var resetErrMu sync.Mutex

	for i := 0; i < in.GroupSize; i++ {
		wg.Add(1)
		go func(idx int) {
			defer wg.Done()
			sem <- struct{}{}
			defer func() { <-sem }()

			r, err := s.resetOne(ctx, in, env, idx)
			if err != nil {
				resetErrMu.Lock()
				resetErrs = append(resetErrs, fmt.Errorf("rollout %d reset: %w", idx, err))
				resetErrMu.Unlock()
				return
			}
			rollouts[idx] = r
		}(i)
	}
	wg.Wait()

	if len(resetErrs) > 0 {
		// Reset failed for ≥1 rollout → roll back every rollout and return a
		// reset_failed error (handler → 503). Reset is all-or-nothing.
		for i, r := range rollouts {
			if r.ProjectID != "" || r.EnvID != "" {
				s.rollbackRollout(ctx, in.WorkspaceID, r)
			}
			rollouts[i] = EnvRollout{}
		}
		return EnvDispatchResult{}, fmt.Errorf("reset_failed: %v", resetErrs[0])
	}

	// Dispatch phase: best-effort, per-rollout errors recorded in rollouts[i].Error.
	var dispatchWG sync.WaitGroup
	for i := 0; i < in.GroupSize; i++ {
		dispatchWG.Add(1)
		go func(idx int) {
			defer dispatchWG.Done()
			sem <- struct{}{}
			defer func() { <-sem }()
			s.dispatchOne(ctx, in, &rollouts[idx], idx)
		}(i)
	}
	dispatchWG.Wait()

	result := EnvDispatchResult{Rollouts: rollouts}

	// Persist the idempotency response so a retry replays it (spec §7.7). Best-effort.
	if in.IdempotencyKey != "" {
		_ = s.deps.SaveIdempotentResponse(ctx, in.WorkspaceID, in.IdempotencyKey, result)
	}

	// Status rule (spec §6.3/§8): ≥1 dispatched → nil (201); all failed →
	// ErrAllDispatchFailed (handler → 500, body still carries rollouts[]).
	succeeded := 0
	for _, r := range rollouts {
		if r.AgentRunID != "" {
			succeeded++
		}
	}
	if succeeded == 0 {
		return result, ErrAllDispatchFailed
	}
	return result, nil
}

// validate implements the §6.3 validation table (the subset that's
// service-level; UUID-shape validation lives in the handler).
func (s *EnvDispatchService) validate(in EnvDispatchInput) error {
	if in.Mode != EnvModeScratch && in.Mode != EnvModeBranch {
		return fmt.Errorf("validation_failed: mode must be scratch or branch")
	}
	if in.DispatchType != EnvDispatchIssue && in.DispatchType != EnvDispatchMessage {
		return fmt.Errorf("validation_failed: dispatch_type must be issue or message")
	}
	if in.GroupSize < 1 || in.GroupSize > 64 {
		return fmt.Errorf("validation_failed: group_size must be in [1, 64]")
	}
	if in.Domain != EnvDomainSweLego && in.Domain != EnvDomainSelfPlay {
		return fmt.Errorf("validation_failed: domain is required (swe_lego or self_play)")
	}
	if in.Domain == EnvDomainSweLego && in.DispatchType == EnvDispatchMessage {
		return fmt.Errorf("validation_failed: swe_lego domain is issue-only")
	}
	if in.Domain == EnvDomainSelfPlay && in.DispatchType == EnvDispatchIssue {
		return fmt.Errorf("not_implemented: self_play + issue dispatch")
	}
	if in.Mode == EnvModeBranch && in.Domain == EnvDomainSweLego && in.Issue != nil {
		return fmt.Errorf("validation_failed: issue must not be supplied for branch+swe_lego (copied issue is reused)")
	}
	if in.Mode == EnvModeScratch && in.Domain == EnvDomainSweLego && in.Issue == nil {
		return fmt.Errorf("validation_failed: issue required for scratch+swe_lego")
	}
	if in.DispatchType == EnvDispatchMessage && (in.Message == nil || in.Message.Content == "") {
		return fmt.Errorf("validation_failed: message.content required")
	}
	return nil
}

// resetOne does the per-rollout reset (sandbox + env + project) per §7.2.
func (s *EnvDispatchService) resetOne(ctx context.Context, in EnvDispatchInput, sourceEnv Env, idx int) (EnvRollout, error) {
	// Branch always forks (spec §4.3): the source sandbox is never reused in
	// place, so the source state stays re-branchable (MCTS). Scratch forks the
	// base. Both paths create a fresh env row.
	forked, err := s.deps.ForkSandbox(ctx, sourceEnv.SandboxID, idx)
	if err != nil {
		return EnvRollout{}, fmt.Errorf("fork sandbox: %w", err)
	}
	sandboxID := forked
	mode := EnvModeScratch
	if in.Mode == EnvModeBranch {
		mode = EnvModeBranch
	}
	envID, err := s.deps.CreateEnv(ctx, in.WorkspaceID, sandboxID, sourceEnv.ID, mode, in.Domain)
	if err != nil {
		_ = s.deps.DeleteSandbox(ctx, sandboxID)
		return EnvRollout{}, fmt.Errorf("create env: %w", err)
	}

	// Project
	var projectID string
	var issueIDMap, chatSessionIDMap map[string]string
	if in.Mode == EnvModeScratch {
		name := fmt.Sprintf("env-dispatch-%s", envID) // unique, spec §7.2
		pid, err := s.deps.CreateProject(ctx, in.WorkspaceID, name, envID)
		if err != nil {
			s.rollbackRollout(ctx, in.WorkspaceID, EnvRollout{EnvID: envID})
			return EnvRollout{}, fmt.Errorf("create project: %w", err)
		}
		projectID = pid
	} else {
		// branch — copy source project subtree (issues + chat sessions).
		// in.SourceProjectID is resolved by the handler from the 1:1 env→project.
		pid, imap, smap, err := s.deps.CopyProjectSubtree(ctx, in.SourceProjectID, in.WorkspaceID, envID)
		if err != nil {
			s.rollbackRollout(ctx, in.WorkspaceID, EnvRollout{EnvID: envID})
			return EnvRollout{}, fmt.Errorf("copy project: %w", err)
		}
		projectID = pid
		issueIDMap = imap
		chatSessionIDMap = smap
	}

	r := EnvRollout{EnvID: envID, ProjectID: projectID}
	// Stash the single copied entity for dispatchOne (spec §7.4: exactly one).
	if in.Mode == EnvModeBranch {
		if in.Domain == EnvDomainSweLego {
			for _, newID := range issueIDMap {
				r.IssueID = newID
				break
			}
		} else if in.Domain == EnvDomainSelfPlay {
			for _, newID := range chatSessionIDMap {
				r.ChatSessionID = newID
				break
			}
		}
	}
	return r, nil
}

// dispatchOne runs the dispatch phase for one rollout (§7.3). Best-effort:
// failures recorded in r.Error, no rollback.
func (s *EnvDispatchService) dispatchOne(ctx context.Context, in EnvDispatchInput, r *EnvRollout, idx int) {
	if in.DispatchType == EnvDispatchIssue {
		issueID := r.IssueID // branch+swe_lego: copied issue id
		if issueID == "" {
			// scratch+swe_lego — create the new issue
			ii := in.Issue
			newID, err := s.deps.CreateIssue(ctx, r.ProjectID, in.WorkspaceID, in.UserID, ii.Title, ii.Description, ii.AcceptanceCriteria, ii.FailToPass, ii.PassToPass)
			if err != nil {
				r.Error = fmt.Sprintf("create issue: %v", err)
				return
			}
			issueID = newID
			r.IssueID = newID
		}
		runID, err := s.deps.EnqueueAgentRun(ctx, in.WorkspaceID, in.AgentID, issueID, "", "", idx)
		if err != nil {
			r.Error = fmt.Sprintf("enqueue agent run: %v", err)
			return
		}
		r.AgentRunID = runID
		return
	}
	// message (self_play)
	sessionID := r.ChatSessionID // branch: the copied session (spec §7.4); empty for scratch
	if sessionID == "" {
		// scratch+self_play — new session bound to the new project
		newID, err := s.deps.CreateChatSession(ctx, r.ProjectID, in.WorkspaceID, in.AgentID, in.UserID)
		if err != nil {
			r.Error = fmt.Sprintf("create chat session: %v", err)
			return
		}
		sessionID = newID
		r.ChatSessionID = newID
	}
	// branch continues the copied conversation by appending; scratch starts fresh (spec §7.3).
	if _, err := s.deps.CreateChatMessage(ctx, sessionID, "user", in.Message.Content); err != nil {
		r.Error = fmt.Sprintf("create chat message: %v", err)
		return
	}
	runID, err := s.deps.EnqueueAgentRun(ctx, in.WorkspaceID, in.AgentID, "", sessionID, "", idx)
	if err != nil {
		r.Error = fmt.Sprintf("enqueue agent run: %v", err)
		return
	}
	r.AgentRunID = runID
}

// rollbackRollout cleans up a partially-created rollout (reset phase only).
// Order matters under ON DELETE RESTRICT: delete the project first (it
// references env_id), then the env row, then its sandbox. Every rollout forks
// its own sandbox, so this never touches a shared/source sandbox.
func (s *EnvDispatchService) rollbackRollout(ctx context.Context, workspaceID string, r EnvRollout) {
	if r.ProjectID != "" {
		_ = s.deps.DeleteProject(ctx, r.ProjectID, workspaceID)
	}
	if r.EnvID != "" {
		env, err := s.deps.GetEnv(ctx, r.EnvID, workspaceID)
		_ = s.deps.DeleteEnv(ctx, r.EnvID, workspaceID)
		if err == nil {
			_ = s.deps.DeleteSandbox(ctx, env.SandboxID)
		}
	}
}
