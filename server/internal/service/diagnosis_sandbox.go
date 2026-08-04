// SPDX-License-Identifier: Apache-2.0

package service

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"strings"
)

// Execution-mode values persisted on the diagnosis run row (migration 278).
const (
	DiagnosisExecutionModeSandbox = "sandbox"
	DiagnosisExecutionModeServer  = "server"
)

// ErrDiagnosisExtensionMissing is returned by DiagnosisExtensionPusher when
// the sandbox image baked no extension placeholder at the push target, so the
// orchestrator falls back to image-baked delivery instead of failing the run
// (research D3: per-run push with image fallback).
var ErrDiagnosisExtensionMissing = errors.New("diagnosis extension placeholder missing in sandbox")

// ── Terminal task → run mapping (T022) ──

// DiagnosisRunIDContextKey is the agent-inbox task-context JSON key the work
// enqueuer stamps so the terminal mapping can attribute a daemon-reported task
// to its diagnosis run without a database lookup.
const DiagnosisRunIDContextKey = "diagnosis_run_id"

// DiagnosisAgentNamePrefix prefixes the per-run diagnosis agent name
// ("diagnosis-<runID>"); it is the fallback run identifier when the task
// context carries no stamped run ID (e.g. tasks enqueued before stamping).
const DiagnosisAgentNamePrefix = "diagnosis-"

// DiagnosisRunIDFromTaskContext extracts the stamped diagnosis run ID from an
// agent-inbox task context JSON blob. It returns "" for absent or malformed
// contexts — the caller then falls back to the per-run agent name.
func DiagnosisRunIDFromTaskContext(contextJSON []byte) string {
	var contextMap map[string]json.RawMessage
	if len(contextJSON) == 0 || json.Unmarshal(contextJSON, &contextMap) != nil {
		return ""
	}
	var runID string
	if raw, ok := contextMap[DiagnosisRunIDContextKey]; !ok || json.Unmarshal(raw, &runID) != nil {
		return ""
	}
	return strings.TrimSpace(runID)
}

// DiagnosisRunIDFromAgentName extracts the run ID from the per-run diagnosis
// agent name, or "" when the agent is not a diagnosis agent.
func DiagnosisRunIDFromAgentName(name string) string {
	if !strings.HasPrefix(name, DiagnosisAgentNamePrefix) {
		return ""
	}
	return strings.TrimSpace(strings.TrimPrefix(name, DiagnosisAgentNamePrefix))
}

// DiagnosisTaskFailureClass values prefix the classified last_error of a run
// failed by the daemon-reported task outcome (spec SC-005: distinct,
// diagnosable failure records).
const (
	DiagnosisFailureClassTimeout      = "timeout"
	DiagnosisFailureClassConnectivity = "connectivity"
	DiagnosisFailureClassAgent        = "agent"
)

// diagnosisTimeoutSignals mark the diagnosis agent exceeding its time budget
// (DiagnosisAgentTimeout surfaces through the daemon as a timeout-class task
// failure). Matched case-insensitively against reason code, failure reason,
// and error text.
var diagnosisTimeoutSignals = []string{"timeout", "timed out", "deadline exceeded"}

// diagnosisConnectivitySignals mark the sandboxed agent being unable to reach
// the multica diagnosis API — the per-run API URL host shows up in fetch/dial
// errors, or the extension names its env contract explicitly.
var diagnosisConnectivitySignals = []string{
	"multica_diagnosis",
	"diagnosis-runs",
	"connection refused",
	"no such host",
	"network is unreachable",
	"dial tcp",
	"econnrefused",
	"connect:",
}

func containsAnySignal(haystack string, signals []string) bool {
	for _, signal := range signals {
		if strings.Contains(haystack, signal) {
			return true
		}
	}
	return false
}

// ClassifyDiagnosisTaskFailure maps a daemon-reported agent-inbox task failure
// to a classified run failure cause: "timeout: ..." when the payload signals
// the diagnosis timeout, "connectivity: ..." when it signals the agent could
// not reach the multica API, and "agent: ..." otherwise. Classification is
// best-effort string matching over the failure payload — simple and
// deterministic. DiagnosisStateStore.FailRun caps the persisted last_error at
// 1 KiB per convention.
func ClassifyDiagnosisTaskFailure(errText, failureReason, reasonCode string) error {
	detail := strings.TrimSpace(errText)
	if detail == "" {
		detail = strings.TrimSpace(failureReason)
	}
	if detail == "" {
		detail = strings.TrimSpace(reasonCode)
	}
	if detail == "" {
		detail = "diagnosis task failed"
	}
	haystack := strings.ToLower(reasonCode + " " + failureReason + " " + errText)
	class := DiagnosisFailureClassAgent
	switch {
	case containsAnySignal(haystack, diagnosisTimeoutSignals):
		class = DiagnosisFailureClassTimeout
	case containsAnySignal(haystack, diagnosisConnectivitySignals):
		class = DiagnosisFailureClassConnectivity
	}
	return fmt.Errorf("%s: %s", class, detail)
}

// DiagnosisExtensionFileName is the file name of the generated trusted pi
// extension inside the push target directory.
const DiagnosisExtensionFileName = "multica-diagnosis-tools.ts"

// DiagnosisExtensionRelDir is the daemon-WorkspacesRoot-relative directory the
// orchestrator pushes the generated extension into. The diagnosis sandbox
// image must bake a placeholder file at
// <WorkspacesRoot>/<workspaceID>/.multica/diagnosis/multica-diagnosis-tools.ts
// for the per-run refresh to land (the daemon write-file RPC is edit-only and
// cannot create files); when the placeholder is absent the image-baked copy of
// the extension is used as-is.
func DiagnosisExtensionRelDir(workspaceID string) string {
	return workspaceID + "/.multica/diagnosis"
}

// ResolveDiagnosisPublicURL resolves the server base URL reachable from inside
// sandboxes via the established MULTICA_PUBLIC_URL → MULTICA_APP_URL →
// MULTICA_SERVER_URL chain (research D7, same chain as MintSandboxRuntimeEnv).
// It fails closed when no public URL is configured: the sandboxed agent would
// otherwise receive an unusable API address.
func ResolveDiagnosisPublicURL() (string, error) {
	for _, env := range []string{"MULTICA_PUBLIC_URL", "MULTICA_APP_URL", "MULTICA_SERVER_URL"} {
		if v := strings.TrimSpace(os.Getenv(env)); v != "" {
			return strings.TrimRight(v, "/"), nil
		}
	}
	return "", fmt.Errorf("server public URL not configured (set MULTICA_PUBLIC_URL/MULTICA_APP_URL/MULTICA_SERVER_URL)")
}

// DiagnosisRunAPIBaseURL builds the run-scoped diagnosis API base the
// sandboxed agent calls (MULTICA_DIAGNOSIS_API_URL).
func DiagnosisRunAPIBaseURL(publicURL, runID string) string {
	return strings.TrimRight(publicURL, "/") + "/api/v1/diagnosis-runs/" + runID
}

// DiagnosisRunState is the narrow state-store surface the orchestrator needs.
// *DiagnosisStateStore satisfies it in production; tests substitute a fake.
type DiagnosisRunState interface {
	GetRun(ctx context.Context, runID string) (DiagnosisRunCheckpoint, error)
	SetRunSandbox(ctx context.Context, runID, sandboxInstanceID, capabilityTokenHash, executionMode string) error
	MarkRunProvisioning(ctx context.Context, runID string) error
	ActivateRun(ctx context.Context, runID string) error
	FailRun(ctx context.Context, runID string, cause error) error
}

// DiagnosisSandboxCreator creates and looks up the dedicated per-run sandbox.
// *EnvSandboxLifecycleService satisfies it in production.
type DiagnosisSandboxCreator interface {
	Create(ctx context.Context, in CreateSandboxInstanceInput, actorUserID string) (SandboxInstanceRef, error)
	GetSandboxInstanceRef(ctx context.Context, workspaceID, instanceID string) (SandboxInstanceRef, error)
}

// DiagnosisSandboxRuntimeResolver waits for the sandbox's daemon-registered
// online pi runtime. daemonID is the correlation nonce returned by Create; an
// empty daemonID means the caller reattaches to an existing sandbox whose
// nonce was never persisted, so the resolver matches by sandbox_instance_id
// alone.
type DiagnosisSandboxRuntimeResolver interface {
	WaitOnline(ctx context.Context, workspaceID, daemonID, sandboxInstanceID string) (RuntimeRef, error)
}

// DiagnosisExtensionPusher delivers the generated extension source into the
// sandbox workdir via the daemonws file-ops channel. It returns
// ErrDiagnosisExtensionMissing when the image baked no placeholder at the
// target (image-baked delivery fallback).
type DiagnosisExtensionPusher interface {
	PushExtension(ctx context.Context, runtimeID, relPath, filePath, content string) error
}

// DiagnosisWorkEnqueuer delivers the diagnosis bootstrap prompt to the sandbox
// runtime through the daemon task protocol (agent-inbox). The production
// implementation is a direct agent-inbox enqueue against the discovered
// runtime — a per-run diagnosis agent row carries the per-run env in its
// custom_env (the established secret channel the daemon injects into the pi
// process at launch), one chat session + one prompt message + one chat task.
// The env-dispatch channel-run machinery was rejected: it is coupled to
// channel membership, roster framing, and derived-agent cloning that a
// channel-less diagnosis run does not have.
type DiagnosisWorkEnqueuer interface {
	EnqueueDiagnosisWork(ctx context.Context, work DiagnosisWorkItem) error
}

// DiagnosisSandboxReclaimer deletes a run's sandbox on terminal transitions,
// reusing the env-sandbox lifecycle delete job (with the force-delete
// fallback when no sandboxd node is available).
type DiagnosisSandboxReclaimer interface {
	ReclaimDiagnosisSandbox(ctx context.Context, workspaceID, sandboxInstanceID string) error
}

// DiagnosisWorkItem is the enqueue payload for one sandboxed diagnosis run.
type DiagnosisWorkItem struct {
	RunID             string
	WorkspaceID       string
	ProjectID         string
	ActorUserID       string
	RuntimeID         string
	SandboxInstanceID string
	BootstrapPrompt   string
	SystemPrompt      string
	Model             string
	// Env carries the per-run diagnosis API credentials
	// (MULTICA_DIAGNOSIS_API_URL / MULTICA_DIAGNOSIS_CAPABILITY_TOKEN)
	// injected into the sandboxed agent's environment at task level. Nil on
	// reattach: the per-run agent already carries the env minted by the
	// original provision, and the raw token is deliberately not recoverable
	// server-side (only its hash is persisted).
	Env map[string]string
}

// DiagnosisProvisionRequest drives one ProvisionRun call.
type DiagnosisProvisionRequest struct {
	RunID           string
	WorkspaceID     string
	ProjectID       string
	ActorUserID     string
	BootstrapPrompt string
	SystemPrompt    string
	Model           string
}

// DiagnosisSandboxOrchestratorConfig wires the orchestrator's dependencies.
// ResolvePublicURL and ExtensionSource default to the env-chain resolver and
// the generated extension source when nil.
type DiagnosisSandboxOrchestratorConfig struct {
	State            DiagnosisRunState
	Sandboxes        DiagnosisSandboxCreator
	Resolver         DiagnosisSandboxRuntimeResolver
	Pusher           DiagnosisExtensionPusher
	Enqueuer         DiagnosisWorkEnqueuer
	Reclaimer        DiagnosisSandboxReclaimer
	ResolvePublicURL func() (string, error)
	ExtensionSource  func() string
}

// DiagnosisSandboxOrchestrator provisions a dedicated sandbox per diagnosis
// run and drives the run through provisioning → running (spec 005, US1). All
// dependencies are narrow interfaces so the lifecycle can be unit-tested with
// fakes — no real sandboxes, no DB.
type DiagnosisSandboxOrchestrator struct {
	state            DiagnosisRunState
	sandboxes        DiagnosisSandboxCreator
	resolver         DiagnosisSandboxRuntimeResolver
	pusher           DiagnosisExtensionPusher
	enqueuer         DiagnosisWorkEnqueuer
	reclaimer        DiagnosisSandboxReclaimer
	resolvePublicURL func() (string, error)
	extensionSource  func() string
}

// NewDiagnosisSandboxOrchestrator validates the wiring and returns the
// orchestrator. Every lifecycle dependency is required — a partially wired
// orchestrator would fail runs with confusing causes.
func NewDiagnosisSandboxOrchestrator(cfg DiagnosisSandboxOrchestratorConfig) (*DiagnosisSandboxOrchestrator, error) {
	if cfg.State == nil || cfg.Sandboxes == nil || cfg.Resolver == nil ||
		cfg.Pusher == nil || cfg.Enqueuer == nil || cfg.Reclaimer == nil {
		return nil, fmt.Errorf("diagnosis sandbox orchestrator: all dependencies are required")
	}
	resolvePublicURL := cfg.ResolvePublicURL
	if resolvePublicURL == nil {
		resolvePublicURL = ResolveDiagnosisPublicURL
	}
	extensionSource := cfg.ExtensionSource
	if extensionSource == nil {
		extensionSource = DiagnosisPiExtensionSource
	}
	return &DiagnosisSandboxOrchestrator{
		state:            cfg.State,
		sandboxes:        cfg.Sandboxes,
		resolver:         cfg.Resolver,
		pusher:           cfg.Pusher,
		enqueuer:         cfg.Enqueuer,
		reclaimer:        cfg.Reclaimer,
		resolvePublicURL: resolvePublicURL,
		extensionSource:  extensionSource,
	}, nil
}

// ProvisionRun creates-or-reattaches the dedicated sandbox for a run and
// enqueues the diagnosis work. It is safe to call for both fresh runs and
// resume (T016): a run with a recorded sandbox reattaches when the sandbox is
// still alive, otherwise a replacement is provisioned with a re-minted token
// (re-minting invalidates in-flight HMAC cursors keyed by the old token hash —
// the intended safe behavior). Every failure marks the run failed with a
// classified cause ("provisioning: ...", "extension: ...", "enqueue: ...") and
// reclaims any sandbox the attempt created; reclaim is best-effort.
func (o *DiagnosisSandboxOrchestrator) ProvisionRun(ctx context.Context, req DiagnosisProvisionRequest) error {
	if strings.TrimSpace(req.RunID) == "" || strings.TrimSpace(req.WorkspaceID) == "" || strings.TrimSpace(req.ActorUserID) == "" {
		return fmt.Errorf("diagnosis sandbox: run_id, workspace_id and actor_user_id are required")
	}
	run, err := o.state.GetRun(ctx, req.RunID)
	if err != nil {
		return fmt.Errorf("diagnosis sandbox: load run %s: %w", req.RunID, err)
	}
	if run.Status == DiagnosisRunCompleted || run.Status == DiagnosisRunFailed {
		// Terminal run: nothing to provision (idempotent trigger replay).
		return nil
	}
	publicURL, err := o.resolvePublicURL()
	if err != nil {
		return o.failRun(ctx, req, "", fmt.Errorf("provisioning: %w", err))
	}
	if run.SandboxInstanceID != "" {
		if rt, ok := o.tryReattach(ctx, req, run.SandboxInstanceID); ok {
			return o.activateAndEnqueue(ctx, req, run, rt, nil)
		}
		slog.Info("diagnosis sandbox: recorded sandbox unusable, re-provisioning",
			"run_id", req.RunID, "sandbox_instance_id", run.SandboxInstanceID)
	}
	return o.provisionFresh(ctx, req, run, publicURL)
}

// tryReattach checks whether the run's recorded sandbox is still usable: the
// instance must still exist and its daemon-registered pi runtime must come
// online. A dead instance is simply re-provisioned; a live instance whose
// runtime never registers is reclaimed before re-provisioning.
func (o *DiagnosisSandboxOrchestrator) tryReattach(ctx context.Context, req DiagnosisProvisionRequest, sandboxInstanceID string) (RuntimeRef, bool) {
	ref, err := o.sandboxes.GetSandboxInstanceRef(ctx, req.WorkspaceID, sandboxInstanceID)
	if err != nil {
		return RuntimeRef{}, false
	}
	rt, err := o.resolver.WaitOnline(ctx, req.WorkspaceID, "", ref.InstanceID)
	if err != nil {
		slog.Warn("diagnosis sandbox: reattach runtime wait failed, reclaiming stale sandbox",
			"run_id", req.RunID, "sandbox_instance_id", ref.InstanceID, "error", err)
		o.reclaimBestEffort(ctx, req.RunID, req.WorkspaceID, ref.InstanceID)
		return RuntimeRef{}, false
	}
	return rt, true
}

// provisionFresh mints a new capability token, creates the dedicated sandbox,
// persists the binding, and waits for the runtime before activating the run.
func (o *DiagnosisSandboxOrchestrator) provisionFresh(ctx context.Context, req DiagnosisProvisionRequest, run DiagnosisRunCheckpoint, publicURL string) error {
	token, err := MintDiagnosisCapabilityToken()
	if err != nil {
		return o.failRun(ctx, req, "", fmt.Errorf("provisioning: mint capability token: %w", err))
	}
	ref, err := o.sandboxes.Create(ctx, CreateSandboxInstanceInput{
		WorkspaceID:   req.WorkspaceID,
		Template:      "default",
		Name:          "diagnosis-" + req.RunID,
		DaemonEnabled: true,
	}, req.ActorUserID)
	if err != nil {
		// Create compensates its own partially created instance, so there is
		// nothing to reclaim here.
		return o.failRun(ctx, req, "", fmt.Errorf("provisioning: create sandbox: %w", err))
	}
	fail := func(cause error) error {
		return o.failRun(ctx, req, ref.InstanceID, cause)
	}
	if err := o.state.SetRunSandbox(ctx, req.RunID, ref.InstanceID, HashDiagnosisCapabilityToken(token), DiagnosisExecutionModeSandbox); err != nil {
		return fail(fmt.Errorf("provisioning: persist sandbox binding: %w", err))
	}
	if err := o.state.MarkRunProvisioning(ctx, req.RunID); err != nil {
		return fail(fmt.Errorf("provisioning: mark run provisioning: %w", err))
	}
	rt, err := o.resolver.WaitOnline(ctx, req.WorkspaceID, ref.DaemonID, ref.InstanceID)
	if err != nil {
		return fail(fmt.Errorf("provisioning: wait for sandbox runtime: %w", err))
	}
	env := map[string]string{
		"MULTICA_DIAGNOSIS_API_URL":          DiagnosisRunAPIBaseURL(publicURL, req.RunID),
		"MULTICA_DIAGNOSIS_CAPABILITY_TOKEN": token,
	}
	return o.activateAndEnqueue(ctx, req, run, rt, env)
}

// activateAndEnqueue is the shared tail of reattach and fresh provisioning:
// activate the run, deliver the extension, then enqueue the work. env is nil
// on reattach (the per-run agent keeps its original credentials).
func (o *DiagnosisSandboxOrchestrator) activateAndEnqueue(ctx context.Context, req DiagnosisProvisionRequest, run DiagnosisRunCheckpoint, rt RuntimeRef, env map[string]string) error {
	sandboxInstanceID := rt.SandboxInstanceID
	if sandboxInstanceID == "" {
		sandboxInstanceID = run.SandboxInstanceID
	}
	fail := func(cause error) error {
		return o.failRun(ctx, req, sandboxInstanceID, cause)
	}
	if err := o.state.ActivateRun(ctx, req.RunID); err != nil {
		return fail(fmt.Errorf("provisioning: activate run: %w", err))
	}
	if err := o.pusher.PushExtension(ctx, rt.ID, DiagnosisExtensionRelDir(req.WorkspaceID), DiagnosisExtensionFileName, o.extensionSource()); err != nil {
		if errors.Is(err, ErrDiagnosisExtensionMissing) {
			// Documented fallback (research D3): the diagnosis sandbox image
			// carries the extension baked at pi's discovery path, so a missing
			// push placeholder is not fatal.
			slog.Warn("diagnosis sandbox: extension placeholder missing, relying on image-baked extension",
				"run_id", req.RunID, "sandbox_instance_id", sandboxInstanceID)
		} else {
			return fail(fmt.Errorf("extension: deliver trusted extension: %w", err))
		}
	}
	if err := o.enqueuer.EnqueueDiagnosisWork(ctx, DiagnosisWorkItem{
		RunID:             req.RunID,
		WorkspaceID:       req.WorkspaceID,
		ProjectID:         req.ProjectID,
		ActorUserID:       req.ActorUserID,
		RuntimeID:         rt.ID,
		SandboxInstanceID: sandboxInstanceID,
		BootstrapPrompt:   req.BootstrapPrompt,
		SystemPrompt:      req.SystemPrompt,
		Model:             req.Model,
		Env:               env,
	}); err != nil {
		return fail(fmt.Errorf("enqueue: deliver diagnosis work: %w", err))
	}
	slog.Info("diagnosis sandbox: run provisioned",
		"run_id", req.RunID, "sandbox_instance_id", sandboxInstanceID, "runtime_id", rt.ID)
	return nil
}

// failRun marks the run failed with a bounded classified cause and reclaims
// the sandbox when one is bound to the attempt. Reclaim and FailRun failures
// are logged, never silently dropped.
func (o *DiagnosisSandboxOrchestrator) failRun(ctx context.Context, req DiagnosisProvisionRequest, sandboxInstanceID string, cause error) error {
	if sandboxInstanceID != "" {
		o.reclaimBestEffort(ctx, req.RunID, req.WorkspaceID, sandboxInstanceID)
	}
	if err := o.state.FailRun(ctx, req.RunID, cause); err != nil {
		slog.Warn("diagnosis sandbox: fail run transition failed", "run_id", req.RunID, "error", err)
	}
	return cause
}

// reclaimBestEffort deletes a sandbox without masking the primary failure.
func (o *DiagnosisSandboxOrchestrator) reclaimBestEffort(ctx context.Context, runID, workspaceID, sandboxInstanceID string) {
	if sandboxInstanceID == "" {
		return
	}
	if err := o.reclaimer.ReclaimDiagnosisSandbox(context.WithoutCancel(ctx), workspaceID, sandboxInstanceID); err != nil {
		slog.Warn("diagnosis sandbox: reclaim failed",
			"run_id", runID, "sandbox_instance_id", sandboxInstanceID, "error", err)
	}
}
