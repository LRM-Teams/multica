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

type bindingControlTestCurrentSet struct {
	mu                sync.RWMutex
	pids              map[string]int
	daemonInstanceIDs map[string]string
	delayReady        []string
}

type bindingControlTestHost struct {
	host  *computer.Host
	state *bindingControlTestCurrentSet
}

type bindingControlTestChild struct {
	pid        int
	state      *bindingControlTestCurrentSet
	workspace  string
	wait       chan computer.RunnerExitClass
	stopOnce   sync.Once
	readyDelay bool
}

func (child *bindingControlTestChild) PID() int {
	if child.state == nil {
		return child.pid
	}
	child.state.mu.RLock()
	defer child.state.mu.RUnlock()
	return child.state.pids[child.workspace]
}
func (child *bindingControlTestChild) Wait() computer.RunnerExitClass {
	return <-child.wait
}
func (child *bindingControlTestChild) Stop() error {
	child.stopOnce.Do(func() { child.wait <- computer.RunnerExitGraceful })
	return nil
}

func (child *bindingControlTestChild) AwaitReady(ctx context.Context) (computer.BindingChildReady, error) {
	if child.readyDelay {
		<-ctx.Done()
		return computer.BindingChildReady{}, ctx.Err()
	}
	identity := fmt.Sprintf("child-%s-%d", child.workspace, child.PID())
	if child.state != nil {
		child.state.mu.Lock()
		child.state.daemonInstanceIDs[child.workspace] = identity
		child.state.mu.Unlock()
	}
	return computer.BindingChildReady{
		ProtocolVersion:  computer.BindingChildProtocolVersion,
		WorkspaceID:      child.workspace,
		DaemonInstanceID: identity,
		PID:              child.PID(),
		RunnerEndpoint:   "unix:///tmp/multica-test-runner.sock",
	}, nil
}

type capturingRunnerDiagnosticSink struct {
	workspaceID string
	event       diagnosticlog.Event
	recorded    chan struct{}
}

func (sink *capturingRunnerDiagnosticSink) record(workspaceID string, event diagnosticlog.Event) error {
	sink.workspaceID = workspaceID
	sink.event = event
	select {
	case sink.recorded <- struct{}{}:
	default:
	}
	return nil
}

func TestBindingChildDiagnosticsAreAggregatedByHost(t *testing.T) {
	const controlToken = "host-control-token"
	sink := &capturingRunnerDiagnosticSink{recorded: make(chan struct{}, 1)}
	host := newBindingControlTestHost(t, controlToken, computer.HostControlCallbacks{
		Diagnostic: func(_ context.Context, _ computer.BindingChildIdentity, workspaceID string, event diagnosticlog.Event) error {
			return sink.record(workspaceID, event)
		},
	})
	installLiveBindingChild(t, host, "workspace-a", 101)
	serverURL := localHostControlRPC(t, host.host)

	client := newBindingHostControlClient(serverURL, controlToken, liveBindingIdentity(t, host, "workspace-a", 101))
	event := diagnosticlog.Event{Name: diagnosticlog.EventDeliveryStateChanged, Component: "message_coordinator"}
	if err := client.recordDiagnostic(context.Background(), "workspace-a", event); err != nil {
		t.Fatalf("record child diagnostic: %v", err)
	}
	select {
	case <-sink.recorded:
	case <-time.After(time.Second):
		t.Fatal("Host did not aggregate Binding child diagnostic")
	}
	if sink.workspaceID != "workspace-a" || sink.event.Name != event.Name || sink.event.Component != event.Component {
		t.Fatalf("Host diagnostic = workspace %q event %+v", sink.workspaceID, sink.event)
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

func TestBindingChildHarvestsWorkDigestFromHostNotUpgradePayload(t *testing.T) {
	const controlToken = "host-control-token"
	host := newBindingControlTestHost(t, controlToken, computer.HostControlCallbacks{})
	installLiveBindingChild(t, host, "workspace-a", 101)
	serverURL := localHostControlRPC(t, host.host)

	child := New(Config{}, slog.New(slog.NewTextHandler(io.Discard, nil)))
	child.bindingHostControl = newBindingHostControlClient(serverURL, controlToken, liveBindingIdentity(t, host, "workspace-a", 101))
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

func TestBindingChildForwardsConnectSocketUpgradeToService(t *testing.T) {
	executed := make(chan protocol.ComputerUpgradePayload, 1)
	host := newBindingControlTestHost(t, "host-control-token", computer.HostControlCallbacks{
		ComputerUpgrade: func(_ context.Context, _ computer.BindingChildIdentity, raw json.RawMessage) error {
			var command protocol.ComputerUpgradePayload
			if err := json.Unmarshal(raw, &command); err != nil {
				return err
			}
			executed <- command
			return nil
		},
	})
	installLiveBindingChild(t, host, "workspace-a", 101)
	serverURL := localHostControlRPC(t, host.host)

	child := New(Config{}, slog.New(slog.NewTextHandler(io.Discard, nil)))
	child.bindingHostControl = newBindingHostControlClient(serverURL, "host-control-token", liveBindingIdentity(t, host, "workspace-a", 101))
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

func TestBindingChildForwardsRestartToHost(t *testing.T) {
	const controlToken = "host-control-token"
	forwarded := make(chan HeartbeatResponse, 1)
	host := newBindingControlTestHost(t, controlToken, computer.HostControlCallbacks{
		MachineActions: func(_ context.Context, identity computer.BindingChildIdentity, raw json.RawMessage) error {
			if identity.WorkspaceID != "workspace-a" {
				t.Errorf("Host machine action workspace = %q", identity.WorkspaceID)
			}
			var ack HeartbeatResponse
			if err := json.Unmarshal(raw, &ack); err != nil {
				return err
			}
			forwarded <- ack
			return nil
		},
	})
	installLiveBindingChild(t, host, "workspace-a", 101)
	serverURL := localHostControlRPC(t, host.host)
	control := newBindingHostControlClient(serverURL, controlToken, liveBindingIdentity(t, host, "workspace-a", 101))
	if err := control.reportRuntimeSet(context.Background(), []Runtime{
		{ID: "runtime-a", WorkspaceID: "workspace-a", Provider: "pi"},
	}, "child-daemon-token", time.Now().Add(time.Hour).UTC().Format(time.RFC3339Nano)); err != nil {
		t.Fatalf("report runtime set: %v", err)
	}

	child := New(Config{}, slog.New(slog.NewTextHandler(io.Discard, nil)))
	child.bindingHostControl = control
	child.handleWorkspaceDaemonControlAck(context.Background(), &HeartbeatResponse{
		RuntimeID:      "runtime-a",
		PendingRestart: &PendingRestart{ID: "restart-a"},
	})

	select {
	case ack := <-forwarded:
		if ack.PendingRestart == nil || ack.PendingRestart.ID != "restart-a" {
			t.Fatalf("Host machine action = %+v", ack)
		}
	case <-time.After(time.Second):
		t.Fatal("Binding child did not forward restart to Host")
	}
}

func TestBindingChildReportsItsRuntimeSetToHost(t *testing.T) {
	const controlToken = "host-control-token"
	reported := make(chan struct{}, 1)
	host := newBindingControlTestHost(t, controlToken, computer.HostControlCallbacks{
		RuntimeSet: func(_ context.Context, identity computer.BindingChildIdentity, raw json.RawMessage, token string, _ time.Time) error {
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
	installLiveBindingChild(t, host, "workspace-a", 101)
	serverURL := localHostControlRPC(t, host.host)

	client := newBindingHostControlClient(serverURL, controlToken, liveBindingIdentity(t, host, "workspace-a", 101))
	expiresAt := time.Now().Add(time.Hour).UTC().Format(time.RFC3339Nano)
	if err := client.reportRuntimeSet(context.Background(), []Runtime{
		{ID: "runtime-a", WorkspaceID: "workspace-a", Provider: "pi"},
	}, "child-daemon-token", expiresAt); err != nil {
		t.Fatalf("report Binding child Runtime set: %v", err)
	}
	select {
	case <-reported:
	case <-time.After(time.Second):
		t.Fatal("Host did not aggregate the Binding child Runtime identity")
	}

	staleIdentity := liveBindingIdentity(t, host, "workspace-a", 101)
	staleIdentity.DaemonInstanceID = "stale-start"
	stale := newBindingHostControlClient(serverURL, controlToken, staleIdentity)
	if err := stale.reportRuntimeSet(context.Background(), []Runtime{
		{ID: "runtime-stale", WorkspaceID: "workspace-a", Provider: "pi"},
	}, "stale-token", expiresAt); err == nil {
		t.Fatal("Host accepted a Runtime set from a stale Binding child generation")
	}
}

func installLiveBindingChild(t *testing.T, current *bindingControlTestHost, workspaceID string, pid int) {
	installBindingChild(t, current, workspaceID, pid, false)
}

func installStartingBindingChild(t *testing.T, current *bindingControlTestHost, workspaceID string, pid int) {
	installBindingChild(t, current, workspaceID, pid, true)
}

func installBindingChild(t *testing.T, current *bindingControlTestHost, workspaceID string, pid int, delayReady bool) {
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
	current.host.Reconcile(context.Background(), desired)
	deadline := time.Now().Add(time.Second)
	for time.Now().Before(deadline) {
		state.mu.RLock()
		daemonInstanceID := state.daemonInstanceIDs[workspaceID]
		state.mu.RUnlock()
		identity := bindingChildControlIdentity{WorkspaceID: workspaceID, DaemonInstanceID: daemonInstanceID, PID: pid}
		if delayReady {
			identity.DaemonInstanceID = "pending-child"
			if current.host.Current(identity) {
				return
			}
		} else if daemonInstanceID != "" && current.host.Current(identity) {
			return
		}
		time.Sleep(time.Millisecond)
	}
	t.Fatalf("Binding child did not become current: workspace=%s pid=%d", workspaceID, pid)
}

func liveBindingIdentity(t *testing.T, current *bindingControlTestHost, workspaceID string, pid int) bindingChildControlIdentity {
	t.Helper()
	current.state.mu.RLock()
	daemonInstanceID := current.state.daemonInstanceIDs[workspaceID]
	current.state.mu.RUnlock()
	if daemonInstanceID == "" {
		t.Fatalf("Binding child %s has no daemon instance", workspaceID)
	}
	return bindingChildControlIdentity{WorkspaceID: workspaceID, DaemonInstanceID: daemonInstanceID, PID: pid}
}

func localHostControlRPC(t *testing.T, host *computer.Host) string {
	t.Helper()
	endpoint, listener := localHostControlRPCListener(t, host)
	t.Cleanup(func() { _ = listener.Close() })
	return endpoint
}

func localHostControlRPCListener(t *testing.T, host *computer.Host) (string, net.Listener) {
	t.Helper()
	registry := host.LocalControlRegistry(nil)
	endpoint := computer.ServiceControlEndpoint(t.TempDir())
	listener, err := computer.ListenLocalControl(endpoint)
	if err != nil {
		t.Fatal(err)
	}
	go computer.ServeLocalControlRPC(context.Background(), listener, registry)
	return endpoint, listener
}

func newBindingControlTestHost(t *testing.T, controlToken string, callbacks computer.HostControlCallbacks) *bindingControlTestHost {
	t.Helper()
	state := &bindingControlTestCurrentSet{pids: make(map[string]int), daemonInstanceIDs: make(map[string]string)}
	host, err := computer.NewHost(computer.HostConfig{
		ControlToken: controlToken, ControlCallbacks: callbacks,
		Spawn: func(workspaceID string) (computer.BindingChild, error) {
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
			return &bindingControlTestChild{pid: pid, state: state, workspace: workspaceID, wait: make(chan computer.RunnerExitClass, 1), readyDelay: delayReady}, nil
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		host.Stop()
	})
	return &bindingControlTestHost{host: host, state: state}
}
