package daemon

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"
	"time"

	"github.com/multica-ai/multica/server/internal/computer"
	"github.com/multica-ai/multica/server/internal/diagnosticlog"
	"github.com/multica-ai/multica/server/pkg/protocol"
)

type bindingControlTestCurrentSet struct {
	mu   sync.RWMutex
	pids map[string]int
}

type bindingControlTestHost struct {
	host  *computer.Host
	state *bindingControlTestCurrentSet
}

type bindingControlTestChild struct {
	pid      int
	wait     chan computer.RunnerExitClass
	stopOnce sync.Once
}

func (child *bindingControlTestChild) PID() int { return child.pid }
func (child *bindingControlTestChild) Wait() computer.RunnerExitClass {
	return <-child.wait
}
func (child *bindingControlTestChild) Stop() error {
	child.stopOnce.Do(func() { child.wait <- computer.RunnerExitGraceful })
	return nil
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

func TestBindingChildrenShareHostProcessCapacity(t *testing.T) {
	const controlToken = "host-control-token"
	host := newBindingControlTestHost(t, controlToken, 1, computer.HostControlCallbacks{})
	installLiveBindingChild(t, host, "workspace-a", 101)
	installLiveBindingChild(t, host, "workspace-b", 202)

	mux := http.NewServeMux()
	host.host.RegisterRoutes(mux)
	server := httptest.NewServer(mux)
	t.Cleanup(server.Close)

	firstClient := newBindingHostControlClient(server.URL, controlToken, bindingChildControlIdentity{
		WorkspaceID: "workspace-a", RunnerGeneration: 1, PID: 101,
	})
	secondClient := newBindingHostControlClient(server.URL, controlToken, bindingChildControlIdentity{
		WorkspaceID: "workspace-b", RunnerGeneration: 1, PID: 202,
	})
	first := newRemoteAgentProcessAdmission(firstClient)
	second := newRemoteAgentProcessAdmission(secondClient)
	t.Cleanup(first.Close)
	t.Cleanup(second.Close)

	firstGrant, admitted := first.Acquire(agentProcessCapacityRequest{
		WorkspaceID: "workspace-a", AgentID: "agent-a", RuntimeID: "runtime-a", LaunchID: "launch-a",
	})
	if !admitted {
		t.Fatal("first Binding child did not receive Host capacity")
	}
	secondGranted := make(chan agentProcessCapacityGrant, 1)
	secondGrant, admitted := second.Acquire(agentProcessCapacityRequest{
		WorkspaceID: "workspace-b", AgentID: "agent-b", RuntimeID: "runtime-b", LaunchID: "launch-b",
		Waiter: func(grant agentProcessCapacityGrant) { secondGranted <- grant },
	})
	if admitted {
		t.Fatal("second Binding child bypassed the machine-wide capacity cap")
	}

	first.Release(firstGrant)
	select {
	case grant := <-secondGranted:
		if grant != secondGrant || !second.Active(grant) {
			t.Fatalf("promoted Host grant = %+v active=%v, want %+v active", grant, second.Active(grant), secondGrant)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("Host capacity release did not promote the queued sibling Binding")
	}
	second.Release(secondGrant)
}

func TestProviderRuntimeCreationUsesHostProcessCapacity(t *testing.T) {
	const controlToken = "host-control-token"
	host := newBindingControlTestHost(t, controlToken, 1, computer.HostControlCallbacks{})
	installLiveBindingChild(t, host, "workspace-a", 101)
	installLiveBindingChild(t, host, "workspace-b", 202)
	mux := http.NewServeMux()
	host.host.RegisterRoutes(mux)
	server := httptest.NewServer(mux)
	t.Cleanup(server.Close)

	firstAdmission := newRemoteAgentProcessAdmission(newBindingHostControlClient(server.URL, controlToken, bindingChildControlIdentity{WorkspaceID: "workspace-a", RunnerGeneration: 1, PID: 101}))
	secondAdmission := newRemoteAgentProcessAdmission(newBindingHostControlClient(server.URL, controlToken, bindingChildControlIdentity{WorkspaceID: "workspace-b", RunnerGeneration: 1, PID: 202}))
	t.Cleanup(firstAdmission.Close)
	t.Cleanup(secondAdmission.Close)
	firstPool := newCanonicalAgentRuntimePool()
	firstPool.setMachineProcessAdmission("workspace-a", firstAdmission)
	secondPool := newCanonicalAgentRuntimePool()
	secondPool.setMachineProcessAdmission("workspace-b", secondAdmission)

	firstGrant, err := firstPool.reserveMachineProcessCapacity(context.Background(), "agent-a", "runtime-a")
	if err != nil {
		t.Fatalf("reserve first provider capacity: %v", err)
	}
	secondResult := make(chan agentProcessCapacityGrant, 1)
	secondErr := make(chan error, 1)
	go func() {
		grant, err := secondPool.reserveMachineProcessCapacity(context.Background(), "agent-b", "runtime-b")
		if err != nil {
			secondErr <- err
			return
		}
		secondResult <- grant
	}()
	select {
	case grant := <-secondResult:
		t.Fatalf("sibling provider bypassed Host capacity with grant %+v", grant)
	case err := <-secondErr:
		t.Fatalf("sibling provider capacity failed instead of queueing: %v", err)
	case <-time.After(100 * time.Millisecond):
	}
	firstAdmission.Release(firstGrant)
	select {
	case grant := <-secondResult:
		secondAdmission.Release(grant)
	case err := <-secondErr:
		t.Fatalf("queued sibling provider capacity: %v", err)
	case <-time.After(2 * time.Second):
		t.Fatal("Host did not promote queued sibling provider")
	}
}

func TestBindingHostControlRejectsStaleRunnerGeneration(t *testing.T) {
	const controlToken = "host-control-token"
	host := newBindingControlTestHost(t, controlToken, 0, computer.HostControlCallbacks{})
	installLiveBindingChild(t, host, "workspace-a", 101)
	mux := http.NewServeMux()
	host.host.RegisterRoutes(mux)
	server := httptest.NewServer(mux)
	t.Cleanup(server.Close)

	stale := newBindingHostControlClient(server.URL, controlToken, bindingChildControlIdentity{
		WorkspaceID: "workspace-a", RunnerGeneration: 2, PID: 101,
	})
	if err := stale.Attest(context.Background()); err == nil {
		t.Fatal("Host accepted a stale Binding child generation")
	}
}

func TestBindingChildCrashReleasesItsHostCapacity(t *testing.T) {
	const controlToken = "host-control-token"
	host := newBindingControlTestHost(t, controlToken, 1, computer.HostControlCallbacks{})
	installLiveBindingChild(t, host, "workspace-a", 101)
	installLiveBindingChild(t, host, "workspace-b", 202)
	mux := http.NewServeMux()
	host.host.RegisterRoutes(mux)
	server := httptest.NewServer(mux)
	t.Cleanup(server.Close)

	first := newRemoteAgentProcessAdmission(newBindingHostControlClient(server.URL, controlToken, bindingChildControlIdentity{WorkspaceID: "workspace-a", RunnerGeneration: 1, PID: 101}))
	second := newRemoteAgentProcessAdmission(newBindingHostControlClient(server.URL, controlToken, bindingChildControlIdentity{WorkspaceID: "workspace-b", RunnerGeneration: 1, PID: 202}))
	t.Cleanup(first.Close)
	t.Cleanup(second.Close)
	if _, admitted := first.Acquire(agentProcessCapacityRequest{WorkspaceID: "workspace-a", AgentID: "agent-a", RuntimeID: "runtime-a", LaunchID: "launch-a"}); !admitted {
		t.Fatal("first Binding child did not receive capacity")
	}
	granted := make(chan agentProcessCapacityGrant, 1)
	if _, admitted := second.Acquire(agentProcessCapacityRequest{WorkspaceID: "workspace-b", AgentID: "agent-b", RuntimeID: "runtime-b", LaunchID: "launch-b", Waiter: func(grant agentProcessCapacityGrant) { granted <- grant }}); admitted {
		t.Fatal("sibling Binding bypassed Host capacity")
	}

	host.host.Release(bindingChildControlIdentity{WorkspaceID: "workspace-a", RunnerGeneration: 1, PID: 101})
	select {
	case <-granted:
	case <-time.After(2 * time.Second):
		t.Fatal("crashed Binding child leaked Host capacity")
	}
}

func TestBindingChildDiagnosticsAreAggregatedByHost(t *testing.T) {
	const controlToken = "host-control-token"
	sink := &capturingRunnerDiagnosticSink{recorded: make(chan struct{}, 1)}
	host := newBindingControlTestHost(t, controlToken, 0, computer.HostControlCallbacks{
		Diagnostic: func(_ context.Context, _ computer.BindingChildIdentity, workspaceID string, event diagnosticlog.Event) error {
			return sink.record(workspaceID, event)
		},
	})
	installLiveBindingChild(t, host, "workspace-a", 101)
	mux := http.NewServeMux()
	host.host.RegisterRoutes(mux)
	server := httptest.NewServer(mux)
	t.Cleanup(server.Close)

	client := newBindingHostControlClient(server.URL, controlToken, bindingChildControlIdentity{WorkspaceID: "workspace-a", RunnerGeneration: 1, PID: 101})
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

func TestBindingChildExecutesConnectSocketUpgradeLocally(t *testing.T) {
	executed := make(chan protocol.ComputerUpgradePayload, 1)
	hostHits := make(chan struct{}, 1)
	host := newBindingControlTestHost(t, "host-control-token", 0, computer.HostControlCallbacks{
		MachineActions: func(context.Context, computer.BindingChildIdentity, json.RawMessage) error {
			hostHits <- struct{}{}
			return errors.New("Host must not execute a connect-socket Machine Upgrade")
		},
	})
	installLiveBindingChild(t, host, "workspace-a", 101)
	mux := http.NewServeMux()
	host.host.RegisterRoutes(mux)
	server := httptest.NewServer(mux)
	t.Cleanup(server.Close)

	child := New(Config{}, slog.New(slog.NewTextHandler(io.Discard, nil)))
	child.bindingHostControl = newBindingHostControlClient(server.URL, "host-control-token", bindingChildControlIdentity{WorkspaceID: "workspace-a", RunnerGeneration: 1, PID: 101})
	child.bindingMachineUpgrade = func(_ context.Context, command protocol.ComputerUpgradePayload) error {
		executed <- command
		return nil
	}
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
		t.Fatal("DaemonCore connect-socket upgrade was not executed in the Binding child")
	}
	select {
	case <-hostHits:
		t.Fatal("connect-socket Machine Upgrade was forwarded to Host")
	case <-time.After(50 * time.Millisecond):
	}
}

func TestBindingChildForwardsRestartToHost(t *testing.T) {
	const controlToken = "host-control-token"
	forwarded := make(chan HeartbeatResponse, 1)
	host := newBindingControlTestHost(t, controlToken, 0, computer.HostControlCallbacks{
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
	mux := http.NewServeMux()
	host.host.RegisterRoutes(mux)
	server := httptest.NewServer(mux)
	t.Cleanup(server.Close)
	control := newBindingHostControlClient(server.URL, controlToken, bindingChildControlIdentity{WorkspaceID: "workspace-a", RunnerGeneration: 1, PID: 101})
	if err := control.reportRuntimeSet(context.Background(), []Runtime{
		{ID: "runtime-a", WorkspaceID: "workspace-a", Provider: "pi"},
	}, "child-daemon-token", time.Now().Add(time.Hour).UTC().Format(time.RFC3339Nano)); err != nil {
		t.Fatalf("report runtime set: %v", err)
	}

	child := New(Config{}, slog.New(slog.NewTextHandler(io.Discard, nil)))
	child.bindingHostControl = control
	child.handleWorkspaceRunnerControlAck(context.Background(), &HeartbeatResponse{
		RuntimeID:      "runtime-a",
		PendingRestart: &PendingRestart{ID: "restart-a"},
	})

	select {
	case ack := <-forwarded:
		if ack.PendingRestart == nil || ack.PendingRestart.ID != "restart-a" || ack.PendingMachineUpgrade != nil {
			t.Fatalf("Host machine action = %+v", ack)
		}
	case <-time.After(time.Second):
		t.Fatal("Binding child did not forward restart to Host")
	}
}

func TestBindingChildReportsItsRuntimeSetToHost(t *testing.T) {
	const controlToken = "host-control-token"
	reported := make(chan struct{}, 1)
	host := newBindingControlTestHost(t, controlToken, 0, computer.HostControlCallbacks{
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
	mux := http.NewServeMux()
	host.host.RegisterRoutes(mux)
	server := httptest.NewServer(mux)
	t.Cleanup(server.Close)

	client := newBindingHostControlClient(server.URL, controlToken, bindingChildControlIdentity{
		WorkspaceID: "workspace-a", RunnerGeneration: 1, PID: 101,
	})
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

	stale := newBindingHostControlClient(server.URL, controlToken, bindingChildControlIdentity{
		WorkspaceID: "workspace-a", RunnerGeneration: 2, PID: 101,
	})
	if err := stale.reportRuntimeSet(context.Background(), []Runtime{
		{ID: "runtime-stale", WorkspaceID: "workspace-a", Provider: "pi"},
	}, "stale-token", expiresAt); err == nil {
		t.Fatal("Host accepted a Runtime set from a stale Binding child generation")
	}
}

func installLiveBindingChild(t *testing.T, current *bindingControlTestHost, workspaceID string, pid int) {
	t.Helper()
	state := current.state
	state.mu.Lock()
	state.pids[workspaceID] = pid
	desired := make([]string, 0, len(state.pids))
	for current := range state.pids {
		desired = append(desired, current)
	}
	state.mu.Unlock()
	current.host.Reconcile(context.Background(), desired)
	identity := bindingChildControlIdentity{WorkspaceID: workspaceID, RunnerGeneration: 1, PID: pid}
	deadline := time.Now().Add(time.Second)
	for time.Now().Before(deadline) {
		if current.host.Current(identity) {
			return
		}
		time.Sleep(time.Millisecond)
	}
	t.Fatalf("Binding child did not become current: %+v", identity)
}

func newBindingControlTestHost(t *testing.T, controlToken string, maxProcesses int, callbacks computer.HostControlCallbacks) *bindingControlTestHost {
	t.Helper()
	state := &bindingControlTestCurrentSet{pids: make(map[string]int)}
	host, err := computer.NewHost(computer.HostConfig{
		ControlToken: controlToken, MaxAgentProcesses: maxProcesses, ControlCallbacks: callbacks,
		Spawn: func(workspaceID string, _ int64) (computer.BindingChild, error) {
			state.mu.RLock()
			pid := state.pids[workspaceID]
			state.mu.RUnlock()
			return &bindingControlTestChild{pid: pid, wait: make(chan computer.RunnerExitClass, 1)}, nil
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
