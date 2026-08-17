package daemon

import (
	"context"
	"io/fs"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/multica-ai/multica/server/pkg/agent"
	"github.com/multica-ai/multica/server/pkg/protocol"
)

var _ interface{ Run(context.Context) } = (*WorkspaceRunner)(nil)

func TestWorkspaceRunnerConstructionRequiresFixedIdentity(t *testing.T) {
	runtimes := newCanonicalAgentRuntimePool()
	base := WorkspaceRunnerConfig{
		DaemonID: "daemon-1", DaemonInstanceID: "daemon-instance-1", WorkspaceID: "workspace-1",
	}
	dependencies := workspaceRunnerDependencies{
		client: NewClient(""), runtimes: runtimes,
		processAdmission:      runtimes.managedProcessAdmission(),
		openInbox:             func(InboxKey, string) (*MessageCoordinator, error) { return nil, nil },
		runtimeIDs:            func() []string { return []string{"runtime-1"} },
		ensureResidentRuntime: func(context.Context, string, string, *agent.PiRunIdentity) error { return nil },
	}
	for name, mutate := range map[string]func(*WorkspaceRunnerConfig){
		"Daemon":          func(config *WorkspaceRunnerConfig) { config.DaemonID = "" },
		"Daemon instance": func(config *WorkspaceRunnerConfig) { config.DaemonInstanceID = "" },
		"Workspace":       func(config *WorkspaceRunnerConfig) { config.WorkspaceID = "" },
	} {
		t.Run(name, func(t *testing.T) {
			config := base
			mutate(&config)
			if _, err := newWorkspaceRunner(config, dependencies); err == nil {
				t.Fatal("constructor accepted missing fixed identity")
			}
		})
	}
	for name, mutate := range map[string]func(*workspaceRunnerDependencies){
		"client":            func(dependencies *workspaceRunnerDependencies) { dependencies.client = nil },
		"runtimes":          func(dependencies *workspaceRunnerDependencies) { dependencies.runtimes = nil },
		"process admission": func(dependencies *workspaceRunnerDependencies) { dependencies.processAdmission = nil },
		"Inbox factory":     func(dependencies *workspaceRunnerDependencies) { dependencies.openInbox = nil },
		"Runtime scope":     func(dependencies *workspaceRunnerDependencies) { dependencies.runtimeIDs = nil },
		"resident Runtime":  func(dependencies *workspaceRunnerDependencies) { dependencies.ensureResidentRuntime = nil },
	} {
		t.Run(name, func(t *testing.T) {
			candidate := dependencies
			mutate(&candidate)
			if _, err := newWorkspaceRunner(base, candidate); err == nil {
				t.Fatal("constructor accepted missing dependency")
			}
		})
	}
}

func TestDaemonConnectionURLIsWorkspaceScoped(t *testing.T) {
	got, err := daemonConnectionURL("https://api.example.com/multica", "workspace-1")
	if err != nil {
		t.Fatal(err)
	}
	const want = "wss://api.example.com/multica/api/daemon/connect?workspace_id=workspace-1"
	if got != want {
		t.Fatalf("daemonConnectionURL() = %q, want %q", got, want)
	}
}

func TestDaemonConnectionConnectedTracksSocketLifetime(t *testing.T) {
	connection := newDaemonConnection("workspace-1", context.Background(), func(string, any) error { return nil }, func() {})
	if !connection.Connected() {
		t.Fatal("new DaemonConnection must start connected")
	}
	connection.Close()
	if connection.Connected() {
		t.Fatal("closed DaemonConnection must not stay connected")
	}
}

func TestWorkspaceRunnerConnectionSerializesConcurrentWrites(t *testing.T) {
	var active atomic.Int64
	var maximum atomic.Int64
	connection := newDaemonConnection("workspace-1", context.Background(), func(string, any) error {
		current := active.Add(1)
		for current > maximum.Load() && !maximum.CompareAndSwap(maximum.Load(), current) {
		}
		time.Sleep(time.Millisecond)
		active.Add(-1)
		return nil
	}, func() {})
	var writes sync.WaitGroup
	for index := 0; index < 20; index++ {
		writes.Add(1)
		go func() {
			defer writes.Done()
			if err := connection.Write("test", nil); err != nil {
				t.Error(err)
			}
		}()
	}
	writes.Wait()
	if got := maximum.Load(); got != 1 {
		t.Fatalf("maximum concurrent socket writes = %d, want 1", got)
	}
}

func TestCurrentComputerDoesNotStartLegacyHTTPHeartbeatControlCarrier(t *testing.T) {
	raw, err := os.ReadFile("daemon.go")
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(raw), "go d.heartbeatLoop(ctx)") {
		t.Fatal("Computer.Run still starts the legacy HTTP heartbeat control carrier")
	}
}

func TestWorkspaceRunnerAdvertisesControlPlaneOnlyWithBothDirections(t *testing.T) {
	hasControl := func(runner *WorkspaceRunner) bool {
		for _, capability := range runner.activeCapabilities() {
			if capability == protocol.DaemonCapabilityWorkspaceRunnerControlPlane {
				return true
			}
		}
		return false
	}
	tests := []struct {
		name    string
		runner  *WorkspaceRunner
		control bool
	}{
		{name: "neither", runner: &WorkspaceRunner{}},
		{name: "send only", runner: &WorkspaceRunner{controlHeartbeatPayload: func(string) protocol.DaemonHeartbeatRequestPayload { return protocol.DaemonHeartbeatRequestPayload{} }}},
		{name: "ack only", runner: &WorkspaceRunner{controlHeartbeatAck: func(context.Context, *HeartbeatResponse) {}}},
		{name: "both", runner: &WorkspaceRunner{
			controlHeartbeatPayload: func(string) protocol.DaemonHeartbeatRequestPayload { return protocol.DaemonHeartbeatRequestPayload{} },
			controlHeartbeatAck:     func(context.Context, *HeartbeatResponse) {},
		}, control: true},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			if got := hasControl(test.runner); got != test.control {
				t.Fatalf("control capability = %v, want %v", got, test.control)
			}
		})
	}
}

func TestWorkspaceRunnerControlHeartbeatUsesExactWorkspaceRuntimeSet(t *testing.T) {
	d := New(Config{DaemonID: "computer-1", HeartbeatInterval: time.Hour}, nil)
	d.mu.Lock()
	d.runtimeIndex["runtime-1"] = Runtime{ID: "runtime-1", WorkspaceID: "workspace-1"}
	d.runtimeIndex["runtime-2"] = Runtime{ID: "runtime-2", WorkspaceID: "workspace-1"}
	d.runtimeIndex["runtime-other"] = Runtime{ID: "runtime-other", WorkspaceID: "workspace-other"}
	d.mu.Unlock()
	runner, err := d.newWorkspaceRunner("workspace-1")
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	type controlFrame struct {
		eventType string
		payload   any
	}
	frames := make(chan controlFrame, 3)
	connection := newDaemonConnection("workspace-1", ctx, func(eventType string, payload any) error {
		frames <- controlFrame{eventType: eventType, payload: payload}
		return nil
	}, func() {})
	runner.replaceConnection(connection)
	done := make(chan struct{})
	go func() {
		defer close(done)
		runner.runControlPlaneHeartbeats(ctx, connection)
	}()
	got := make(map[string]bool)
	for len(got) < 2 {
		select {
		case frame := <-frames:
			if frame.eventType != protocol.EventDaemonHeartbeat {
				t.Fatalf("control event type = %q", frame.eventType)
			}
			heartbeat, ok := frame.payload.(protocol.DaemonHeartbeatRequestPayload)
			if !ok {
				t.Fatalf("control payload type = %T", frame.payload)
			}
			got[heartbeat.RuntimeID] = true
		case <-time.After(time.Second):
			t.Fatalf("control heartbeats = %v, want both workspace-1 Runtimes", got)
		}
	}
	if got["runtime-other"] || !got["runtime-1"] || !got["runtime-2"] {
		t.Fatalf("control heartbeat Runtime scope = %v", got)
	}
	cancel()
	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("control heartbeat loop did not stop")
	}
}

func TestWorkspaceRunnerReconnectReplacesConnectionContextAndWriter(t *testing.T) {
	runner := &WorkspaceRunner{}
	var firstClosed, secondClosed atomic.Int64
	first := newDaemonConnection("workspace-1", context.Background(), func(string, any) error { return nil }, func() { firstClosed.Add(1) })
	second := newDaemonConnection("workspace-1", context.Background(), func(string, any) error { return nil }, func() { secondClosed.Add(1) })
	runner.replaceConnection(first)
	runner.replaceConnection(second)
	select {
	case <-first.ctx.Done():
	default:
		t.Fatal("reconnect did not cancel the replaced connection context")
	}
	if first.Connected() {
		t.Fatal("replaced DaemonConnection must not stay connected")
	}
	if firstClosed.Load() != 1 || runner.connection != second {
		t.Fatalf("first close count=%d current=%p want second=%p", firstClosed.Load(), runner.connection, second)
	}
	runner.releaseConnection(second)
	select {
	case <-second.ctx.Done():
	default:
		t.Fatal("release did not cancel the current connection context")
	}
	if secondClosed.Load() != 1 || runner.connection != nil {
		t.Fatalf("second close count=%d current=%p", secondClosed.Load(), runner.connection)
	}
}

func TestComputerSupervisesProcessAndBindingChildOwnsWorkspaceRunner(t *testing.T) {
	daemonRaw, err := os.ReadFile("workspace_runner.go")
	if err != nil {
		t.Fatal(err)
	}
	childRaw, err := os.ReadFile("binding_child_runtime.go")
	if err != nil {
		t.Fatal(err)
	}
	supervisorRaw, err := os.ReadFile("../computer/binding_supervisor.go")
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(daemonRaw), "superviseWorkspaceRunner") || strings.Contains(string(daemonRaw), "workspaceRunnerChildren") {
		t.Fatal("daemon package still contains Computer Host process supervision")
	}
	if !strings.Contains(string(childRaw), "runner.Run(runCtx)") {
		t.Fatal("Binding child does not own WorkspaceRunner.Run")
	}
	if !strings.Contains(string(childRaw), "adoptWorkspaceRunner(runner)") {
		t.Fatal("Binding child does not publish its Workspace Runner for Credential Proxy lookup")
	}
	if !strings.Contains(string(supervisorRaw), "type BindingSupervisor struct") || !strings.Contains(string(supervisorRaw), "child.Wait()") {
		t.Fatal("computer package does not own Binding child supervision")
	}
}

func TestDaemonHasNoWorkspaceRunnerTransportOrGenerationMaps(t *testing.T) {
	for _, path := range []string{"daemon.go", "workspace_runner_delivery.go"} {
		raw, err := os.ReadFile(path)
		if err != nil {
			t.Fatal(err)
		}
		for _, forbidden := range []string{
			"runnerMessageTransports",
			"runnerMessageGeneration",
			"workspaceRunnerMessageTransport",
			"attachWorkspaceRunnerMessageTransport",
			"detachWorkspaceRunnerMessageTransport",
		} {
			if strings.Contains(string(raw), forbidden) {
				t.Fatalf("%s still contains obsolete transport ownership %q", path, forbidden)
			}
		}
	}
}

func TestWorkspaceRunnerInternalsDoNotEscapeRunnerModule(t *testing.T) {
	daemonType := reflect.TypeOf((*Daemon)(nil))
	for _, owner := range []reflect.Type{reflect.TypeOf(WorkspaceRunner{}), reflect.TypeOf(workspaceRunnerDependencies{})} {
		for i := 0; i < owner.NumField(); i++ {
			field := owner.Field(i)
			if field.Type == daemonType {
				t.Fatalf("%s retains whole-Daemon dependency in field %s", owner.Name(), field.Name)
			}
		}
	}

	err := filepath.WalkDir(".", func(path string, entry fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if entry.IsDir() || !strings.HasSuffix(path, ".go") || strings.HasSuffix(path, "_test.go") {
			return nil
		}
		name := filepath.Base(path)
		if strings.HasPrefix(name, "workspace_runner_") || name == "workspace_runner.go" {
			return nil
		}
		raw, readErr := os.ReadFile(path)
		if readErr != nil {
			return readErr
		}
		for _, forbidden := range []string{
			"runner.inboxes", "runner.processes", "runner.activity", "runner.attachments", "runner.runtimes",
			"runner.messageCoordinator", "candidateRunner.inboxes", "candidateRunner.processes",
			"candidateRunner.activity", "candidateRunner.messageCoordinator",
		} {
			if strings.Contains(string(raw), forbidden) {
				t.Errorf("%s reaches through WorkspaceRunner ownership via %q", path, forbidden)
			}
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
}

func TestWorkspaceRunnerOwnsLocalStateAndSharesMachineDependencies(t *testing.T) {
	runtimes := newCanonicalAgentRuntimePool()
	diagnostics := &runnerDiagnosticRegistry{}
	dependencies := workspaceRunnerDependencies{
		client: NewClient(""), runtimes: runtimes,
		processAdmission:      runtimes.managedProcessAdmission(),
		diagnostics:           diagnostics,
		openInbox:             func(InboxKey, string) (*MessageCoordinator, error) { return nil, nil },
		runtimeIDs:            func() []string { return []string{"runtime-1"} },
		ensureResidentRuntime: func(context.Context, string, string, *agent.PiRunIdentity) error { return nil },
	}
	first, err := newWorkspaceRunner(WorkspaceRunnerConfig{
		DaemonID: "daemon-1", DaemonInstanceID: "instance-1", WorkspaceID: "workspace-1",
	}, dependencies)
	if err != nil {
		t.Fatal(err)
	}
	second, err := newWorkspaceRunner(WorkspaceRunnerConfig{
		DaemonID: "daemon-1", DaemonInstanceID: "instance-1", WorkspaceID: "workspace-2",
	}, dependencies)
	if err != nil {
		t.Fatal(err)
	}
	if first.runtimes != runtimes || first.diagnostics != diagnostics {
		t.Fatal("Runner copied or replaced a machine-wide dependency")
	}
	if first.processes == nil || first.activity == nil || first.inboxes == nil {
		t.Fatal("Runner local state was not constructed")
	}
	if first.processes == second.processes || first.activity == second.activity || first.inboxes == second.inboxes {
		t.Fatal("different Workspace Runners share Runner-owned state")
	}
	if first.inboxes.workspaceID != "workspace-1" || second.inboxes.workspaceID != "workspace-2" {
		t.Fatal("Inbox registry slots lost their fixed Workspace scope")
	}
	if first.activity.daemonInstanceID != "instance-1" {
		t.Fatalf("Activity producer daemon instance = %q", first.activity.daemonInstanceID)
	}
}

func TestDaemonBuildsWorkspaceRunnerFromSharedOwners(t *testing.T) {
	d := New(Config{DaemonID: "daemon-1", MaxAgentProcesses: 3}, nil)
	d.runnerInstanceID = "instance-1"
	d.runnerDiagnostics = &runnerDiagnosticRegistry{}
	runner, err := d.newWorkspaceRunner("workspace-1")
	if err != nil {
		t.Fatal(err)
	}
	if runner.runtimes != d.canonicalRuntimes || runner.diagnostics != d.runnerDiagnostics {
		t.Fatal("Daemon did not inject its machine-wide owners")
	}
	if runner.processes.admission == nil {
		t.Fatal("Daemon did not inject machine-wide process admission")
	}
}
