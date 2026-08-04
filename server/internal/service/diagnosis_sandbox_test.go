// SPDX-License-Identifier: Apache-2.0

package service

import (
	"context"
	"errors"
	"os"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// ── Fakes ──

type fakeDiagnosisRunState struct {
	run         DiagnosisRunCheckpoint
	transitions []DiagnosisRunStatus
	setSandbox  []string // instanceID, tokenHash, executionMode
	failErr     error
}

func (f *fakeDiagnosisRunState) GetRun(context.Context, string) (DiagnosisRunCheckpoint, error) {
	return f.run, nil
}

func (f *fakeDiagnosisRunState) SetRunSandbox(_ context.Context, _ string, instanceID, tokenHash, mode string) error {
	f.setSandbox = []string{instanceID, tokenHash, mode}
	f.run.SandboxInstanceID = instanceID
	f.run.CapabilityTokenHash = tokenHash
	f.run.ExecutionMode = mode
	return nil
}

func (f *fakeDiagnosisRunState) MarkRunProvisioning(context.Context, string) error {
	f.run.Status = DiagnosisRunProvisioning
	f.transitions = append(f.transitions, DiagnosisRunProvisioning)
	return nil
}

func (f *fakeDiagnosisRunState) ActivateRun(context.Context, string) error {
	f.run.Status = DiagnosisRunRunning
	f.transitions = append(f.transitions, DiagnosisRunRunning)
	return nil
}

func (f *fakeDiagnosisRunState) FailRun(_ context.Context, _ string, cause error) error {
	f.run.Status = DiagnosisRunFailed
	if cause != nil {
		f.run.LastError = cause.Error()
	}
	return f.failErr
}

type fakeDiagnosisSandboxCreator struct {
	ref        SandboxInstanceRef
	createErr  error
	creates    []CreateSandboxInstanceInput
	lookupRef  SandboxInstanceRef
	lookupErr  error
	lookupArgs []string
}

func (f *fakeDiagnosisSandboxCreator) Create(_ context.Context, in CreateSandboxInstanceInput, _ string) (SandboxInstanceRef, error) {
	f.creates = append(f.creates, in)
	if f.createErr != nil {
		return SandboxInstanceRef{}, f.createErr
	}
	return f.ref, nil
}

func (f *fakeDiagnosisSandboxCreator) GetSandboxInstanceRef(_ context.Context, _, instanceID string) (SandboxInstanceRef, error) {
	f.lookupArgs = append(f.lookupArgs, instanceID)
	if f.lookupErr != nil {
		return SandboxInstanceRef{}, f.lookupErr
	}
	return f.lookupRef, nil
}

type runtimeWaitCall struct{ daemonID, sandboxInstanceID string }

type fakeDiagnosisRuntimeResolver struct {
	rt    RuntimeRef
	err   error
	calls []runtimeWaitCall
}

func (f *fakeDiagnosisRuntimeResolver) WaitOnline(_ context.Context, _, daemonID, sandboxInstanceID string) (RuntimeRef, error) {
	f.calls = append(f.calls, runtimeWaitCall{daemonID: daemonID, sandboxInstanceID: sandboxInstanceID})
	if f.err != nil {
		return RuntimeRef{}, f.err
	}
	return f.rt, nil
}

type fakeDiagnosisExtensionPusher struct {
	err    error
	called bool
	order  *[]string
}

func (f *fakeDiagnosisExtensionPusher) PushExtension(context.Context, string, string, string, string) error {
	f.called = true
	if f.order != nil {
		*f.order = append(*f.order, "push")
	}
	return f.err
}

type fakeDiagnosisWorkEnqueuer struct {
	err   error
	work  []DiagnosisWorkItem
	order *[]string
}

func (f *fakeDiagnosisWorkEnqueuer) EnqueueDiagnosisWork(_ context.Context, work DiagnosisWorkItem) error {
	if f.order != nil {
		*f.order = append(*f.order, "enqueue")
	}
	f.work = append(f.work, work)
	return f.err
}

type fakeDiagnosisSandboxReclaimer struct {
	err   error
	calls []string
}

func (f *fakeDiagnosisSandboxReclaimer) ReclaimDiagnosisSandbox(_ context.Context, _, sandboxInstanceID string) error {
	f.calls = append(f.calls, sandboxInstanceID)
	return f.err
}

// ── Harness ──

type diagnosisSandboxHarness struct {
	state     *fakeDiagnosisRunState
	creator   *fakeDiagnosisSandboxCreator
	resolver  *fakeDiagnosisRuntimeResolver
	pusher    *fakeDiagnosisExtensionPusher
	enqueuer  *fakeDiagnosisWorkEnqueuer
	reclaimer *fakeDiagnosisSandboxReclaimer
	orch      *DiagnosisSandboxOrchestrator
}

func newDiagnosisSandboxHarness(t *testing.T, run DiagnosisRunCheckpoint) *diagnosisSandboxHarness {
	t.Helper()
	h := &diagnosisSandboxHarness{
		state: &fakeDiagnosisRunState{run: run},
		creator: &fakeDiagnosisSandboxCreator{
			ref: SandboxInstanceRef{InstanceID: "inst-new", WorkspaceID: "ws-1", DaemonID: "daemon-1"},
		},
		resolver: &fakeDiagnosisRuntimeResolver{
			rt: RuntimeRef{ID: "rt-1", WorkspaceID: "ws-1", Provider: "pi", DaemonID: "daemon-1", SandboxInstanceID: "inst-new", Status: "online"},
		},
		pusher:    &fakeDiagnosisExtensionPusher{},
		enqueuer:  &fakeDiagnosisWorkEnqueuer{},
		reclaimer: &fakeDiagnosisSandboxReclaimer{},
	}
	orch, err := NewDiagnosisSandboxOrchestrator(DiagnosisSandboxOrchestratorConfig{
		State:     h.state,
		Sandboxes: h.creator,
		Resolver:  h.resolver,
		Pusher:    h.pusher,
		Enqueuer:  h.enqueuer,
		Reclaimer: h.reclaimer,
		ResolvePublicURL: func() (string, error) {
			return "https://public.example/", nil
		},
	})
	require.NoError(t, err)
	h.orch = orch
	return h
}

func diagnosisSandboxRequest() DiagnosisProvisionRequest {
	return DiagnosisProvisionRequest{
		RunID:           "run-1",
		WorkspaceID:     "ws-1",
		ProjectID:       "proj-1",
		ActorUserID:     "user-1",
		BootstrapPrompt: "topology",
		SystemPrompt:    "system",
		Model:           "m",
	}
}

// assertNoDiagnosisProcessEnv proves the sandbox path never mutates the
// process-wide environment the server-mode path used (os.Setenv at
// diagnosis_agent.go). Env reaches the sandbox exclusively via the enqueue
// payload.
func assertNoDiagnosisProcessEnv(t *testing.T) {
	t.Helper()
	for _, key := range []string{"MULTICA_DIAGNOSIS_API_URL", "MULTICA_DIAGNOSIS_CAPABILITY_TOKEN"} {
		_, set := os.LookupEnv(key)
		assert.False(t, set, "process env %s must stay unset on the sandbox path", key)
	}
}

// ── T011/T012/T013: provision success ──

func TestDiagnosisSandboxProvisionSuccess(t *testing.T) {
	assertNoDiagnosisProcessEnv(t)
	order := []string{}
	h := newDiagnosisSandboxHarness(t, DiagnosisRunCheckpoint{
		RunID: "run-1", ProjectID: "proj-1", TaskID: "task-1", Status: DiagnosisRunRunning,
	})
	h.pusher.order = &order
	h.enqueuer.order = &order

	err := h.orch.ProvisionRun(context.Background(), diagnosisSandboxRequest())
	require.NoError(t, err)

	// T011: dedicated daemon-enabled sandbox created, binding persisted,
	// provisioning → running transition.
	require.Len(t, h.creator.creates, 1)
	assert.Equal(t, "ws-1", h.creator.creates[0].WorkspaceID)
	assert.True(t, h.creator.creates[0].DaemonEnabled)
	require.Len(t, h.state.setSandbox, 3)
	assert.Equal(t, "inst-new", h.state.setSandbox[0])
	assert.NotEmpty(t, h.state.setSandbox[1], "capability token hash persisted")
	assert.Equal(t, DiagnosisExecutionModeSandbox, h.state.setSandbox[2])
	assert.Equal(t, []DiagnosisRunStatus{DiagnosisRunProvisioning, DiagnosisRunRunning}, h.state.transitions)
	assert.Equal(t, DiagnosisRunRunning, h.state.run.Status)
	require.Len(t, h.resolver.calls, 1)
	assert.Equal(t, "daemon-1", h.resolver.calls[0].daemonID)

	// T013: extension push happens before enqueue.
	assert.True(t, h.pusher.called)
	assert.Equal(t, []string{"push", "enqueue"}, order)

	// T012: per-run env injected at task level with the raw token whose hash
	// was persisted; URL derived from the public URL chain.
	require.Len(t, h.enqueuer.work, 1)
	work := h.enqueuer.work[0]
	assert.Equal(t, "rt-1", work.RuntimeID)
	assert.Equal(t, "inst-new", work.SandboxInstanceID)
	require.NotNil(t, work.Env)
	assert.Equal(t, "https://public.example/api/v1/diagnosis-runs/run-1", work.Env["MULTICA_DIAGNOSIS_API_URL"])
	token := work.Env["MULTICA_DIAGNOSIS_CAPABILITY_TOKEN"]
	require.NotEmpty(t, token)
	assert.True(t, VerifyDiagnosisCapabilityToken(token, h.state.setSandbox[1]))

	// No reclaim on success; no process-wide env mutation.
	assert.Empty(t, h.reclaimer.calls)
	assertNoDiagnosisProcessEnv(t)
}

func TestDiagnosisSandboxProvisionFailsClosedWithoutPublicURL(t *testing.T) {
	h := newDiagnosisSandboxHarness(t, DiagnosisRunCheckpoint{
		RunID: "run-1", ProjectID: "proj-1", TaskID: "task-1", Status: DiagnosisRunRunning,
	})
	orch, err := NewDiagnosisSandboxOrchestrator(DiagnosisSandboxOrchestratorConfig{
		State: h.state, Sandboxes: h.creator, Resolver: h.resolver,
		Pusher: h.pusher, Enqueuer: h.enqueuer, Reclaimer: h.reclaimer,
		ResolvePublicURL: func() (string, error) { return "", errors.New("no public URL") },
	})
	require.NoError(t, err)

	err = orch.ProvisionRun(context.Background(), diagnosisSandboxRequest())
	require.Error(t, err)
	assert.True(t, strings.HasPrefix(err.Error(), "provisioning:"))
	assert.Empty(t, h.creator.creates, "no sandbox may be created without a reachable API URL")
	assert.Equal(t, DiagnosisRunFailed, h.state.run.Status)
	assert.True(t, strings.HasPrefix(h.state.run.LastError, "provisioning:"))
	assert.Empty(t, h.reclaimer.calls)
}

// ── T011/T015: failure classification + reclaim ──

func TestDiagnosisSandboxProvisionCreateFailureClassified(t *testing.T) {
	h := newDiagnosisSandboxHarness(t, DiagnosisRunCheckpoint{
		RunID: "run-1", ProjectID: "proj-1", TaskID: "task-1", Status: DiagnosisRunRunning,
	})
	h.creator.createErr = errors.New("no node available")

	err := h.orch.ProvisionRun(context.Background(), diagnosisSandboxRequest())
	require.Error(t, err)
	assert.True(t, strings.HasPrefix(err.Error(), "provisioning: create sandbox:"))
	assert.Equal(t, DiagnosisRunFailed, h.state.run.Status)
	assert.True(t, strings.HasPrefix(h.state.run.LastError, "provisioning: create sandbox:"))
	// Create compensates its own partial instance; nothing extra to reclaim.
	assert.Empty(t, h.reclaimer.calls)
	assert.Empty(t, h.enqueuer.work)
}

func TestDiagnosisSandboxProvisionRuntimeWaitFailureReclaims(t *testing.T) {
	h := newDiagnosisSandboxHarness(t, DiagnosisRunCheckpoint{
		RunID: "run-1", ProjectID: "proj-1", TaskID: "task-1", Status: DiagnosisRunRunning,
	})
	h.resolver.err = errors.New("runtime readiness timeout")

	err := h.orch.ProvisionRun(context.Background(), diagnosisSandboxRequest())
	require.Error(t, err)
	assert.True(t, strings.HasPrefix(err.Error(), "provisioning: wait for sandbox runtime:"))
	assert.Equal(t, DiagnosisRunFailed, h.state.run.Status)
	// T015: provisioning failure with a created sandbox still reclaims it.
	assert.Equal(t, []string{"inst-new"}, h.reclaimer.calls)
	assert.Empty(t, h.enqueuer.work)
}

func TestDiagnosisSandboxProvisionExtensionFailureClassified(t *testing.T) {
	h := newDiagnosisSandboxHarness(t, DiagnosisRunCheckpoint{
		RunID: "run-1", ProjectID: "proj-1", TaskID: "task-1", Status: DiagnosisRunRunning,
	})
	h.pusher.err = errors.New("daemon unreachable")

	err := h.orch.ProvisionRun(context.Background(), diagnosisSandboxRequest())
	require.Error(t, err)
	assert.True(t, strings.HasPrefix(err.Error(), "extension:"))
	assert.Equal(t, DiagnosisRunFailed, h.state.run.Status)
	assert.Equal(t, []string{"inst-new"}, h.reclaimer.calls)
	assert.Empty(t, h.enqueuer.work)
}

func TestDiagnosisSandboxProvisionExtensionMissingUsesImageFallback(t *testing.T) {
	h := newDiagnosisSandboxHarness(t, DiagnosisRunCheckpoint{
		RunID: "run-1", ProjectID: "proj-1", TaskID: "task-1", Status: DiagnosisRunRunning,
	})
	h.pusher.err = ErrDiagnosisExtensionMissing

	err := h.orch.ProvisionRun(context.Background(), diagnosisSandboxRequest())
	require.NoError(t, err)
	assert.Equal(t, DiagnosisRunRunning, h.state.run.Status)
	require.Len(t, h.enqueuer.work, 1, "image-baked fallback continues to enqueue")
	assert.Empty(t, h.reclaimer.calls)
}

func TestDiagnosisSandboxProvisionEnqueueFailureClassified(t *testing.T) {
	h := newDiagnosisSandboxHarness(t, DiagnosisRunCheckpoint{
		RunID: "run-1", ProjectID: "proj-1", TaskID: "task-1", Status: DiagnosisRunRunning,
	})
	h.enqueuer.err = errors.New("runtime offline")

	err := h.orch.ProvisionRun(context.Background(), diagnosisSandboxRequest())
	require.Error(t, err)
	assert.True(t, strings.HasPrefix(err.Error(), "enqueue:"))
	assert.Equal(t, DiagnosisRunFailed, h.state.run.Status)
	assert.Equal(t, []string{"inst-new"}, h.reclaimer.calls)
}

// ── T016: resume reattach vs re-provision ──

func TestDiagnosisSandboxResumeReattachesToLiveSandbox(t *testing.T) {
	h := newDiagnosisSandboxHarness(t, DiagnosisRunCheckpoint{
		RunID: "run-1", ProjectID: "proj-1", TaskID: "task-1", Status: DiagnosisRunRunning,
		SandboxInstanceID: "inst-old", CapabilityTokenHash: "oldhash", ExecutionMode: DiagnosisExecutionModeSandbox,
	})
	h.creator.lookupRef = SandboxInstanceRef{InstanceID: "inst-old", WorkspaceID: "ws-1"}
	h.resolver.rt = RuntimeRef{ID: "rt-old", WorkspaceID: "ws-1", Provider: "pi", SandboxInstanceID: "inst-old", Status: "online"}

	err := h.orch.ProvisionRun(context.Background(), diagnosisSandboxRequest())
	require.NoError(t, err)

	assert.Empty(t, h.creator.creates, "reattach must not create a replacement sandbox")
	// The daemon nonce is not persisted; reattach resolves by sandbox ID only.
	require.Len(t, h.resolver.calls, 1)
	assert.Equal(t, "", h.resolver.calls[0].daemonID)
	assert.Equal(t, "inst-old", h.resolver.calls[0].sandboxInstanceID)
	// Token untouched: in-flight HMAC cursors stay valid across reattach.
	assert.Empty(t, h.state.setSandbox)
	assert.Equal(t, "oldhash", h.state.run.CapabilityTokenHash)
	assert.Equal(t, DiagnosisRunRunning, h.state.run.Status)
	// Enqueue without env: the per-run agent keeps its original credentials.
	require.Len(t, h.enqueuer.work, 1)
	assert.Nil(t, h.enqueuer.work[0].Env)
	assert.Equal(t, "rt-old", h.enqueuer.work[0].RuntimeID)
	assert.Empty(t, h.reclaimer.calls)
}

func TestDiagnosisSandboxResumeReprovisionsWhenSandboxGone(t *testing.T) {
	h := newDiagnosisSandboxHarness(t, DiagnosisRunCheckpoint{
		RunID: "run-1", ProjectID: "proj-1", TaskID: "task-1", Status: DiagnosisRunRunning,
		SandboxInstanceID: "inst-dead", CapabilityTokenHash: "oldhash", ExecutionMode: DiagnosisExecutionModeSandbox,
	})
	h.creator.lookupErr = errors.New("sandbox_instance_not_found")

	err := h.orch.ProvisionRun(context.Background(), diagnosisSandboxRequest())
	require.NoError(t, err)

	require.Len(t, h.creator.creates, 1, "dead sandbox must be re-provisioned")
	require.Len(t, h.state.setSandbox, 3)
	assert.Equal(t, "inst-new", h.state.setSandbox[0])
	assert.NotEqual(t, "oldhash", h.state.setSandbox[1], "re-provision re-mints the capability token")
	require.Len(t, h.enqueuer.work, 1)
	require.NotNil(t, h.enqueuer.work[0].Env)
	assert.True(t, VerifyDiagnosisCapabilityToken(h.enqueuer.work[0].Env["MULTICA_DIAGNOSIS_CAPABILITY_TOKEN"], h.state.setSandbox[1]))
	// The recorded sandbox is gone; nothing to reclaim.
	assert.Empty(t, h.reclaimer.calls)
	assert.Equal(t, DiagnosisRunRunning, h.state.run.Status)
}

func TestDiagnosisSandboxResumeReclaimsStaleSandboxWhenRuntimeOffline(t *testing.T) {
	h := newDiagnosisSandboxHarness(t, DiagnosisRunCheckpoint{
		RunID: "run-1", ProjectID: "proj-1", TaskID: "task-1", Status: DiagnosisRunRunning,
		SandboxInstanceID: "inst-stale", CapabilityTokenHash: "oldhash", ExecutionMode: DiagnosisExecutionModeSandbox,
	})
	h.creator.lookupRef = SandboxInstanceRef{InstanceID: "inst-stale", WorkspaceID: "ws-1"}
	// First WaitOnline (reattach, empty daemon ID) fails; second (fresh) succeeds.
	sequenced := &sequencedDiagnosisRuntimeResolver{
		results: []struct {
			rt  RuntimeRef
			err error
		}{
			{err: errors.New("runtime readiness timeout")},
			{rt: RuntimeRef{ID: "rt-1", WorkspaceID: "ws-1", Provider: "pi", DaemonID: "daemon-1", SandboxInstanceID: "inst-new", Status: "online"}},
		},
	}
	orch, err := NewDiagnosisSandboxOrchestrator(DiagnosisSandboxOrchestratorConfig{
		State: h.state, Sandboxes: h.creator, Resolver: sequenced,
		Pusher: h.pusher, Enqueuer: h.enqueuer, Reclaimer: h.reclaimer,
		ResolvePublicURL: func() (string, error) { return "https://public.example", nil },
	})
	require.NoError(t, err)

	err = orch.ProvisionRun(context.Background(), diagnosisSandboxRequest())
	require.NoError(t, err)
	assert.Equal(t, []string{"inst-stale"}, h.reclaimer.calls, "live-but-offline sandbox is reclaimed before re-provisioning")
	require.Len(t, h.creator.creates, 1)
	require.Len(t, h.enqueuer.work, 1)
	assert.Equal(t, DiagnosisRunRunning, h.state.run.Status)
}

type sequencedDiagnosisRuntimeResolver struct {
	results []struct {
		rt  RuntimeRef
		err error
	}
	idx int
}

func (s *sequencedDiagnosisRuntimeResolver) WaitOnline(context.Context, string, string, string) (RuntimeRef, error) {
	r := s.results[s.idx]
	if s.idx < len(s.results)-1 {
		s.idx++
	}
	return r.rt, r.err
}

// ── Misc guards ──

func TestDiagnosisSandboxProvisionTerminalRunIsNoop(t *testing.T) {
	for _, status := range []DiagnosisRunStatus{DiagnosisRunCompleted, DiagnosisRunFailed} {
		h := newDiagnosisSandboxHarness(t, DiagnosisRunCheckpoint{
			RunID: "run-1", ProjectID: "proj-1", TaskID: "task-1", Status: status,
			SandboxInstanceID: "inst-old",
		})
		err := h.orch.ProvisionRun(context.Background(), diagnosisSandboxRequest())
		require.NoError(t, err)
		assert.Empty(t, h.creator.creates)
		assert.Empty(t, h.resolver.calls)
		assert.Empty(t, h.enqueuer.work)
	}
}

func TestDiagnosisSandboxProvisionRequiresIdentityFields(t *testing.T) {
	h := newDiagnosisSandboxHarness(t, DiagnosisRunCheckpoint{RunID: "run-1", Status: DiagnosisRunRunning})
	err := h.orch.ProvisionRun(context.Background(), DiagnosisProvisionRequest{RunID: "run-1"})
	require.Error(t, err)
	assert.Empty(t, h.creator.creates)
}

func TestNewDiagnosisSandboxOrchestratorRequiresAllDeps(t *testing.T) {
	_, err := NewDiagnosisSandboxOrchestrator(DiagnosisSandboxOrchestratorConfig{})
	require.Error(t, err)
}

func TestResolveDiagnosisPublicURLChain(t *testing.T) {
	t.Setenv("MULTICA_PUBLIC_URL", "")
	t.Setenv("MULTICA_APP_URL", "")
	t.Setenv("MULTICA_SERVER_URL", "")
	_, err := ResolveDiagnosisPublicURL()
	require.Error(t, err, "fail closed when no public URL is configured")

	t.Setenv("MULTICA_SERVER_URL", "http://internal:8080/")
	url, err := ResolveDiagnosisPublicURL()
	require.NoError(t, err)
	assert.Equal(t, "http://internal:8080", url)

	t.Setenv("MULTICA_PUBLIC_URL", "https://public.example/")
	url, err = ResolveDiagnosisPublicURL()
	require.NoError(t, err)
	assert.Equal(t, "https://public.example", url, "MULTICA_PUBLIC_URL wins the chain")
	assert.Equal(t, "https://public.example/api/v1/diagnosis-runs/run-9", DiagnosisRunAPIBaseURL(url, "run-9"))
}

// ── T022: run identification at task termination ──

func TestDiagnosisRunIDFromTaskContext(t *testing.T) {
	// The enqueuer composes the stamp with the execution-config snapshot; the
	// extractor must see through both shapes.
	stamped, err := WithTaskExecutionConfig([]byte(`{"diagnosis_run_id":"run-7"}`), "model-x", "")
	require.NoError(t, err)
	assert.Equal(t, "run-7", DiagnosisRunIDFromTaskContext(stamped))

	assert.Equal(t, "run-7", DiagnosisRunIDFromTaskContext([]byte(`{"diagnosis_run_id":"run-7"}`)))
	assert.Empty(t, DiagnosisRunIDFromTaskContext(nil))
	assert.Empty(t, DiagnosisRunIDFromTaskContext([]byte(`not-json`)))
	assert.Empty(t, DiagnosisRunIDFromTaskContext([]byte(`{"execution_config":{}}`)))
	assert.Empty(t, DiagnosisRunIDFromTaskContext([]byte(`{"diagnosis_run_id":42}`)))
	assert.Empty(t, DiagnosisRunIDFromTaskContext([]byte(`{"diagnosis_run_id":"  "}`)))
}

func TestDiagnosisRunIDFromAgentName(t *testing.T) {
	assert.Equal(t, "run-9", DiagnosisRunIDFromAgentName("diagnosis-run-9"))
	assert.Empty(t, DiagnosisRunIDFromAgentName("rollout-agent-1"))
	assert.Empty(t, DiagnosisRunIDFromAgentName("diagnosis-"))
}

// ── T024: failure classification (spec SC-005) ──

func TestClassifyDiagnosisTaskFailure(t *testing.T) {
	cases := []struct {
		name          string
		errText       string
		failureReason string
		reasonCode    string
		wantPrefix    string
	}{
		{
			name:       "timeout from error text",
			errText:    "diagnosis agent exceeded deadline: context deadline exceeded",
			wantPrefix: "timeout: ",
		},
		{
			name:          "timeout from failure reason",
			errText:       "provider run aborted",
			failureReason: "agent_error.agent_timeout",
			wantPrefix:    "timeout: ",
		},
		{
			name:       "timeout from reason code",
			reasonCode: "timeout",
			wantPrefix: "timeout: ",
		},
		{
			name:       "connectivity to the diagnosis API URL",
			errText:    `multica_get_segment_messages: Post "https://multica.example/api/v1/diagnosis-runs/run-1/get-segment-messages": dial tcp 10.0.0.1:443: connect: connection refused`,
			wantPrefix: "connectivity: ",
		},
		{
			name:       "connectivity from env contract signal",
			errText:    "MULTICA_DIAGNOSIS_API_URL unreachable: no such host",
			wantPrefix: "connectivity: ",
		},
		{
			name:       "agent error otherwise",
			errText:    "pi exited: model not found or unavailable",
			wantPrefix: "agent: ",
		},
		{
			name:       "empty payload still classified",
			wantPrefix: "agent: ",
		},
	}
	prefixes := map[string]bool{}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			cause := ClassifyDiagnosisTaskFailure(tc.errText, tc.failureReason, tc.reasonCode)
			require.Error(t, cause)
			assert.True(t, strings.HasPrefix(cause.Error(), tc.wantPrefix),
				"cause %q must carry class prefix %q", cause.Error(), tc.wantPrefix)
			prefixes[tc.wantPrefix] = true
			// Timeout and connectivity are distinct, diagnosable classes.
			if tc.wantPrefix == "timeout: " {
				assert.NotContains(t, cause.Error(), "connectivity:")
			}
		})
	}
	assert.Equal(t, []bool{true, true, true},
		[]bool{prefixes["timeout: "], prefixes["connectivity: "], prefixes["agent: "]},
		"one case per non-provisioning cause class")
}

// assertDiagnosisFailedRecord checks the persisted terminal failure record for
// one cause class: failed status, classified last_error prefix, bounded error.
func assertDiagnosisFailedRecord(t *testing.T, store *DiagnosisStateStore, runID, wantPrefix string) {
	t.Helper()
	run, err := store.GetRun(context.Background(), runID)
	require.NoError(t, err)
	assert.Equal(t, DiagnosisRunFailed, run.Status)
	assert.True(t, strings.HasPrefix(run.LastError, wantPrefix),
		"last_error %q must carry class prefix %q", run.LastError, wantPrefix)
	assert.LessOrEqual(t, len(run.LastError), 1024, "last_error capped at 1 KiB")
}

func TestDiagnosisFailureRecordProvisioning(t *testing.T) {
	h := newDiagnosisSandboxHarness(t, DiagnosisRunCheckpoint{
		RunID: "run-1", ProjectID: "proj-1", TaskID: "task-1", Status: DiagnosisRunRunning,
	})
	h.creator.createErr = errors.New("no node available")

	err := h.orch.ProvisionRun(context.Background(), diagnosisSandboxRequest())
	require.Error(t, err)
	assert.Equal(t, DiagnosisRunFailed, h.state.run.Status)
	assert.True(t, strings.HasPrefix(h.state.run.LastError, "provisioning: "))
	assert.NotContains(t, h.state.run.LastError, "connectivity:")
	assert.NotContains(t, h.state.run.LastError, "agent:")
}

func TestDiagnosisFailureRecordConnectivity(t *testing.T) {
	store, _ := newTestDiagnosisStore(t)
	createTestDiagnosisRun(t, store, "run-conn", "seg-1")
	cause := ClassifyDiagnosisTaskFailure(
		`Post "https://multica.example/api/v1/diagnosis-runs/run-conn/get-segment-messages": dial tcp: connect: connection refused`, "", "")
	require.NoError(t, store.FailRun(context.Background(), "run-conn", cause))
	assertDiagnosisFailedRecord(t, store, "run-conn", "connectivity: ")
}

func TestDiagnosisFailureRecordAgent(t *testing.T) {
	store, _ := newTestDiagnosisStore(t)
	createTestDiagnosisRun(t, store, "run-agent", "seg-1")
	cause := ClassifyDiagnosisTaskFailure("pi exited: model not found or unavailable", "", "")
	require.NoError(t, store.FailRun(context.Background(), "run-agent", cause))
	assertDiagnosisFailedRecord(t, store, "run-agent", "agent: ")
}

func TestDiagnosisFailureRecordTimeout(t *testing.T) {
	store, _ := newTestDiagnosisStore(t)
	createTestDiagnosisRun(t, store, "run-timeout", "seg-1")
	cause := ClassifyDiagnosisTaskFailure("", "agent_error.agent_timeout", "")
	require.NoError(t, store.FailRun(context.Background(), "run-timeout", cause))
	assertDiagnosisFailedRecord(t, store, "run-timeout", "timeout: ")
}

func TestDiagnosisFailureRecordErrorCappedAt1KiB(t *testing.T) {
	store, _ := newTestDiagnosisStore(t)
	createTestDiagnosisRun(t, store, "run-huge", "seg-1")
	huge := strings.Repeat("x", 8*1024)
	require.NoError(t, store.FailRun(context.Background(), "run-huge",
		ClassifyDiagnosisTaskFailure(huge, "", "")))
	run, err := store.GetRun(context.Background(), "run-huge")
	require.NoError(t, err)
	assert.LessOrEqual(t, len(run.LastError), 1024)
	assert.True(t, strings.HasPrefix(run.LastError, "agent: "))
}
