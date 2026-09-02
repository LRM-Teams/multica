package daemon

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net"
	"sync"
	"testing"
	"time"

	"github.com/multica-ai/multica/server/internal/computer"
	"github.com/multica-ai/multica/server/internal/diagnosticlog"
	"github.com/multica-ai/multica/server/pkg/protocol"
)

type workspaceDaemonTestCurrentSet struct {
	mu                sync.RWMutex
	pids              map[string]int
	daemonInstanceIDs map[string]string
	delayReady        []string
}

type computerControlTestHarness struct {
	computerCore *computer.ComputerCore
	state        *workspaceDaemonTestCurrentSet
}

type workspaceDaemonTestProcess struct {
	pid        int
	state      *workspaceDaemonTestCurrentSet
	workspace  string
	wait       chan computer.WorkspaceDaemonExitClass
	stopOnce   sync.Once
	readyDelay bool
}

func (child *workspaceDaemonTestProcess) PID() int {
	if child.state == nil {
		return child.pid
	}
	child.state.mu.RLock()
	defer child.state.mu.RUnlock()
	return child.state.pids[child.workspace]
}
func (child *workspaceDaemonTestProcess) Wait() computer.WorkspaceDaemonExitClass {
	return <-child.wait
}
func (child *workspaceDaemonTestProcess) Stop() error {
	child.stopOnce.Do(func() { child.wait <- computer.WorkspaceDaemonExitGraceful })
	return nil
}

func (child *workspaceDaemonTestProcess) AwaitReady(ctx context.Context) (computer.WorkspaceDaemonReady, error) {
	if child.readyDelay {
		<-ctx.Done()
		return computer.WorkspaceDaemonReady{}, ctx.Err()
	}
	identity := fmt.Sprintf("child-%s-%d", child.workspace, child.PID())
	if child.state != nil {
		child.state.mu.Lock()
		child.state.daemonInstanceIDs[child.workspace] = identity
		child.state.mu.Unlock()
	}
	return computer.WorkspaceDaemonReady{
		ProtocolVersion:  computer.WorkspaceDaemonProtocolVersion,
		WorkspaceID:      child.workspace,
		DaemonInstanceID: identity,
		PID:              child.PID(),
		RunnerEndpoint:   "unix:///tmp/multica-test-runner.sock",
	}, nil
}

type capturingWorkspaceDaemonDiagnosticSink struct {
	workspaceID string
	event       diagnosticlog.Event
	recorded    chan struct{}
}

func (sink *capturingWorkspaceDaemonDiagnosticSink) record(workspaceID string, event diagnosticlog.Event) error {
	sink.workspaceID = workspaceID
	sink.event = event
	select {
	case sink.recorded <- struct{}{}:
	default:
	}
	return nil
}

func TestWorkspaceDaemonDiagnosticsAreAggregatedByComputer(t *testing.T) {
	const controlToken = "computer-control-token"
	sink := &capturingWorkspaceDaemonDiagnosticSink{recorded: make(chan struct{}, 1)}
	computerCore := newComputerControlTestHarness(t, controlToken, computer.ComputerControlCallbacks{
		Diagnostic: func(_ context.Context, _ computer.WorkspaceDaemonIdentity, workspaceID string, event diagnosticlog.Event) error {
			return sink.record(workspaceID, event)
		},
	})
	installLiveWorkspaceDaemon(t, computerCore, "workspace-a", 101)
	serverURL := localComputerControlRPC(t, computerCore.computerCore)

	client := newWorkspaceDaemonComputerControl(serverURL, controlToken, liveWorkspaceDaemonIdentity(t, computerCore, "workspace-a", 101))
	event := diagnosticlog.Event{Name: diagnosticlog.EventDeliveryStateChanged, Component: "message_coordinator"}
	if err := client.recordDiagnostic(context.Background(), "workspace-a", event); err != nil {
		t.Fatalf("record child diagnostic: %v", err)
	}
	select {
	case <-sink.recorded:
	case <-time.After(time.Second):
		t.Fatal("Computer did not aggregate WorkspaceDaemon diagnostic")
	}
	if sink.workspaceID != "workspace-a" || sink.event.Name != event.Name || sink.event.Component != event.Component {
		t.Fatalf("Computer diagnostic = workspace %q event %+v", sink.workspaceID, sink.event)
	}
}

func TestStandaloneDaemonIgnoresConnectSocketUpgrade(t *testing.T) {
	child := New(Config{}, slog.New(slog.NewTextHandler(io.Discard, nil)))
	err := child.handleComputerControlCommand(context.Background(), protocol.EventComputerUpgrade, protocol.ComputerUpgradePayload{
		RequestID: "upgrade-a", TargetVersion: "v9.9.9",
	})
	if err != nil {
		t.Fatalf("standalone DaemonCore upgrade error = %v, want ignore", err)
	}
}

func TestWorkspaceDaemonCollectRootsCommandReadsComputerFile(t *testing.T) {
	const controlToken = "computer-control-token"
	computerCore := newComputerControlTestHarness(t, controlToken, computer.ComputerControlCallbacks{})
	installLiveWorkspaceDaemon(t, computerCore, "workspace-a", 101)
	serverURL := localComputerControlRPC(t, computerCore.computerCore)

	child := New(Config{}, slog.New(slog.NewTextHandler(io.Discard, nil)))
	child.computerControl = newWorkspaceDaemonComputerControl(serverURL, controlToken, liveWorkspaceDaemonIdentity(t, computerCore, "workspace-a", 101))
	roots, err := child.handleComputerCollectRootsCommand(context.Background(), protocol.ComputerCollectRootsPayload{
		RequestID: "roots-1",
	})
	if err != nil {
		t.Fatalf("collect roots get: %v", err)
	}
	if len(roots) != 0 {
		t.Fatalf("unset collect roots = %#v", roots)
	}
}

func TestWorkspaceDaemonHarvestsWorkDigestFromComputerNotUpgradePayload(t *testing.T) {
	const controlToken = "computer-control-token"
	computerCore := newComputerControlTestHarness(t, controlToken, computer.ComputerControlCallbacks{})
	installLiveWorkspaceDaemon(t, computerCore, "workspace-a", 101)
	serverURL := localComputerControlRPC(t, computerCore.computerCore)

	child := New(Config{}, slog.New(slog.NewTextHandler(io.Discard, nil)))
	child.computerControl = newWorkspaceDaemonComputerControl(serverURL, controlToken, liveWorkspaceDaemonIdentity(t, computerCore, "workspace-a", 101))
	start := time.Date(2026, time.August, 10, 0, 0, 0, 0, time.UTC)
	digest, err := child.handleComputerWorkDigestCommand(context.Background(), protocol.ComputerWorkDigestPayload{
		RequestID: "digest-1", Start: start, End: start.Add(24 * time.Hour),
	})
	if err != nil {
		t.Fatalf("work digest command: %v", err)
	}
	if !digest.Disabled || len(digest.Repos) != 0 {
		t.Fatalf("default journal digest %+v", digest)
	}
}

func TestWorkspaceDaemonForwardsConnectSocketUpgradeToComputer(t *testing.T) {
	executed := make(chan protocol.ComputerUpgradePayload, 1)
	computerCore := newComputerControlTestHarness(t, "computer-control-token", computer.ComputerControlCallbacks{
		ComputerUpgrade: func(_ context.Context, _ computer.WorkspaceDaemonIdentity, raw json.RawMessage) error {
			var command protocol.ComputerUpgradePayload
			if err := json.Unmarshal(raw, &command); err != nil {
				return err
			}
			executed <- command
			return nil
		},
	})
	installLiveWorkspaceDaemon(t, computerCore, "workspace-a", 101)
	serverURL := localComputerControlRPC(t, computerCore.computerCore)

	child := New(Config{}, slog.New(slog.NewTextHandler(io.Discard, nil)))
	child.computerControl = newWorkspaceDaemonComputerControl(serverURL, "computer-control-token", liveWorkspaceDaemonIdentity(t, computerCore, "workspace-a", 101))
	if err := child.handleComputerControlCommand(context.Background(), protocol.EventComputerUpgrade, protocol.ComputerUpgradePayload{
		RequestID: "upgrade-a", TargetVersion: "v9.9.9",
	}); err != nil {
		t.Fatalf("handleComputerControlCommand: %v", err)
	}

	select {
	case command := <-executed:
		if command.RequestID != "upgrade-a" || command.TargetVersion != "v9.9.9" {
			t.Fatalf("child executor command = %+v", command)
		}
	case <-time.After(time.Second):
		t.Fatal("DaemonCore connect-socket upgrade was not forwarded to the Computer service")
	}
}

func TestWorkspaceDaemonForwardsRestartToComputer(t *testing.T) {
	const controlToken = "computer-control-token"
	forwarded := make(chan HeartbeatResponse, 1)
	computerCore := newComputerControlTestHarness(t, controlToken, computer.ComputerControlCallbacks{
		MachineActions: func(_ context.Context, identity computer.WorkspaceDaemonIdentity, raw json.RawMessage) error {
			if identity.WorkspaceID != "workspace-a" {
				t.Errorf("Computer machine action workspace = %q", identity.WorkspaceID)
			}
			var ack HeartbeatResponse
			if err := json.Unmarshal(raw, &ack); err != nil {
				return err
			}
			forwarded <- ack
			return nil
		},
	})
	installLiveWorkspaceDaemon(t, computerCore, "workspace-a", 101)
	serverURL := localComputerControlRPC(t, computerCore.computerCore)
	control := newWorkspaceDaemonComputerControl(serverURL, controlToken, liveWorkspaceDaemonIdentity(t, computerCore, "workspace-a", 101))
	if err := control.reportRuntimeSet(context.Background(), []Runtime{
		{ID: "runtime-a", WorkspaceID: "workspace-a", Provider: "pi"},
	}, "child-daemon-token", time.Now().Add(time.Hour).UTC().Format(time.RFC3339Nano)); err != nil {
		t.Fatalf("report runtime set: %v", err)
	}

	child := New(Config{}, slog.New(slog.NewTextHandler(io.Discard, nil)))
	child.computerControl = control
	child.handleWorkspaceDaemonControlAck(context.Background(), &HeartbeatResponse{
		RuntimeID:      "runtime-a",
		PendingRestart: &PendingRestart{ID: "restart-a"},
	})

	select {
	case ack := <-forwarded:
		if ack.PendingRestart == nil || ack.PendingRestart.ID != "restart-a" {
			t.Fatalf("Computer machine action = %+v", ack)
		}
	case <-time.After(time.Second):
		t.Fatal("WorkspaceDaemon did not forward restart to Computer")
	}
}

func TestWorkspaceDaemonReportsItsRuntimeSetToComputer(t *testing.T) {
	const controlToken = "computer-control-token"
	reported := make(chan struct{}, 1)
	computerCore := newComputerControlTestHarness(t, controlToken, computer.ComputerControlCallbacks{
		RuntimeSet: func(_ context.Context, identity computer.WorkspaceDaemonIdentity, raw json.RawMessage, token string, _ time.Time) error {
			if identity.WorkspaceID != "workspace-a" || token != "child-daemon-token" {
				t.Errorf("Runtime report identity=%+v token=%q", identity, token)
			}
			var runtimes []Runtime
			if err := json.Unmarshal(raw, &runtimes); err != nil {
				return err
			}
			if len(runtimes) != 1 || runtimes[0].ID != "runtime-a" {
				t.Errorf("Runtime report = %+v", runtimes)
			}
			reported <- struct{}{}
			return nil
		},
	})
	installLiveWorkspaceDaemon(t, computerCore, "workspace-a", 101)
	serverURL := localComputerControlRPC(t, computerCore.computerCore)

	client := newWorkspaceDaemonComputerControl(serverURL, controlToken, liveWorkspaceDaemonIdentity(t, computerCore, "workspace-a", 101))
	expiresAt := time.Now().Add(time.Hour).UTC().Format(time.RFC3339Nano)
	if err := client.reportRuntimeSet(context.Background(), []Runtime{
		{ID: "runtime-a", WorkspaceID: "workspace-a", Provider: "pi"},
	}, "child-daemon-token", expiresAt); err != nil {
		t.Fatalf("report WorkspaceDaemon Runtime set: %v", err)
	}
	select {
	case <-reported:
	case <-time.After(time.Second):
		t.Fatal("Computer did not aggregate the WorkspaceDaemon Runtime identity")
	}

	staleIdentity := liveWorkspaceDaemonIdentity(t, computerCore, "workspace-a", 101)
	staleIdentity.DaemonInstanceID = "stale-start"
	stale := newWorkspaceDaemonComputerControl(serverURL, controlToken, staleIdentity)
	if err := stale.reportRuntimeSet(context.Background(), []Runtime{
		{ID: "runtime-stale", WorkspaceID: "workspace-a", Provider: "pi"},
	}, "stale-token", expiresAt); err == nil {
		t.Fatal("Computer accepted a Runtime set from a stale WorkspaceDaemon generation")
	}
}

func installLiveWorkspaceDaemon(t *testing.T, current *computerControlTestHarness, workspaceID string, pid int) {
	installWorkspaceDaemon(t, current, workspaceID, pid, false)
}

func installStartingWorkspaceDaemon(t *testing.T, current *computerControlTestHarness, workspaceID string, pid int) {
	installWorkspaceDaemon(t, current, workspaceID, pid, true)
}

func installWorkspaceDaemon(t *testing.T, current *computerControlTestHarness, workspaceID string, pid int, delayReady bool) {
	t.Helper()
	state := current.state
	state.mu.Lock()
	state.pids[workspaceID] = pid
	if delayReady {
		state.delayReady = append(state.delayReady, workspaceID)
	}
	desired := make([]string, 0, len(state.pids))
	for current := range state.pids {
		desired = append(desired, current)
	}
	state.mu.Unlock()
	current.computerCore.Reconcile(context.Background(), desired)
	deadline := time.Now().Add(time.Second)
	for time.Now().Before(deadline) {
		state.mu.RLock()
		daemonInstanceID := state.daemonInstanceIDs[workspaceID]
		state.mu.RUnlock()
		identity := workspaceDaemonControlIdentity{WorkspaceID: workspaceID, DaemonInstanceID: daemonInstanceID, PID: pid}
		if delayReady {
			identity.DaemonInstanceID = "pending-child"
			if current.computerCore.Current(identity) {
				return
			}
		} else if daemonInstanceID != "" && current.computerCore.Current(identity) {
			return
		}
		time.Sleep(time.Millisecond)
	}
	t.Fatalf("WorkspaceDaemon did not become current: workspace=%s pid=%d", workspaceID, pid)
}

func liveWorkspaceDaemonIdentity(t *testing.T, current *computerControlTestHarness, workspaceID string, pid int) workspaceDaemonControlIdentity {
	t.Helper()
	current.state.mu.RLock()
	daemonInstanceID := current.state.daemonInstanceIDs[workspaceID]
	current.state.mu.RUnlock()
	if daemonInstanceID == "" {
		t.Fatalf("WorkspaceDaemon %s has no daemon instance", workspaceID)
	}
	return workspaceDaemonControlIdentity{WorkspaceID: workspaceID, DaemonInstanceID: daemonInstanceID, PID: pid}
}

func localComputerControlRPC(t *testing.T, computerCore *computer.ComputerCore) string {
	t.Helper()
	endpoint, listener := localComputerControlRPCListener(t, computerCore)
	t.Cleanup(func() { _ = listener.Close() })
	return endpoint
}

func localComputerControlRPCListener(t *testing.T, computerCore *computer.ComputerCore) (string, net.Listener) {
	t.Helper()
	registry := computerCore.LocalControlRegistry(nil)
	endpoint := computer.ServiceControlEndpoint(t.TempDir())
	listener, err := computer.ListenLocalControl(endpoint)
	if err != nil {
		t.Fatal(err)
	}
	go computer.ServeLocalControlRPC(context.Background(), listener, registry)
	return endpoint, listener
}

func newComputerControlTestHarness(t *testing.T, controlToken string, callbacks computer.ComputerControlCallbacks) *computerControlTestHarness {
	t.Helper()
	state := &workspaceDaemonTestCurrentSet{pids: make(map[string]int), daemonInstanceIDs: make(map[string]string)}
	computerCore, err := computer.NewComputerCore(computer.ComputerCoreConfig{
		ControlToken: controlToken, ControlCallbacks: callbacks,
		Spawn: func(workspaceID string) (computer.WorkspaceDaemonProcess, error) {
			state.mu.Lock()
			pid := state.pids[workspaceID]
			delayReady := false
			for i, delayed := range state.delayReady {
				if delayed == workspaceID {
					delayReady = true
					state.delayReady = append(state.delayReady[:i], state.delayReady[i+1:]...)
					break
				}
			}
			state.mu.Unlock()
			return &workspaceDaemonTestProcess{pid: pid, state: state, workspace: workspaceID, wait: make(chan computer.WorkspaceDaemonExitClass, 1), readyDelay: delayReady}, nil
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		computerCore.Stop()
	})
	return &computerControlTestHarness{computerCore: computerCore, state: state}
}
