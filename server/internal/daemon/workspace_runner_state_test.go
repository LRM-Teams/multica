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
)

var _ interface{ Run(context.Context) } = (*WorkspaceRunner)(nil)

func TestWorkspaceRunnerConstructionRequiresFixedIdentity(t *testing.T) {
	registry := newLocalAgentAttachmentRegistry(t.TempDir(), nil)
	runtimes := newCanonicalAgentRuntimePool()
	base := WorkspaceRunnerConfig{
		DaemonID: "daemon-1", DaemonInstanceID: "daemon-instance-1", WorkspaceID: "workspace-1",
	}
	dependencies := workspaceRunnerDependencies{
		client: NewClient(""), attachments: registry, runtimes: runtimes,
		openInbox: func(InboxKey, string) (*MessageCoordinator, error) { return nil, nil },
		runtimeSet: func() AgentAttachmentRuntimeSet {
			return AgentAttachmentRuntimeSet{WorkspaceID: "workspace-1"}
		},
		ensureResidentRuntime: func(context.Context, string, string) error { return nil },
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
		"client":           func(dependencies *workspaceRunnerDependencies) { dependencies.client = nil },
		"attachments":      func(dependencies *workspaceRunnerDependencies) { dependencies.attachments = nil },
		"runtimes":         func(dependencies *workspaceRunnerDependencies) { dependencies.runtimes = nil },
		"Inbox factory":    func(dependencies *workspaceRunnerDependencies) { dependencies.openInbox = nil },
		"Runtime scope":    func(dependencies *workspaceRunnerDependencies) { dependencies.runtimeSet = nil },
		"resident Runtime": func(dependencies *workspaceRunnerDependencies) { dependencies.ensureResidentRuntime = nil },
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

func TestWorkspaceRunnerConnectionSerializesConcurrentWrites(t *testing.T) {
	var active atomic.Int64
	var maximum atomic.Int64
	connection := &workspaceRunnerConnection{
		write: func(string, any) error {
			current := active.Add(1)
			for current > maximum.Load() && !maximum.CompareAndSwap(maximum.Load(), current) {
			}
			time.Sleep(time.Millisecond)
			active.Add(-1)
			return nil
		},
	}
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

func TestWorkspaceRunnerReconnectReplacesConnectionContextAndWriter(t *testing.T) {
	runner := &WorkspaceRunner{}
	firstCtx, firstCancel := context.WithCancel(context.Background())
	secondCtx, secondCancel := context.WithCancel(context.Background())
	var firstClosed, secondClosed atomic.Int64
	first := &workspaceRunnerConnection{ctx: firstCtx, cancel: firstCancel, write: func(string, any) error { return nil }, close: func() { firstClosed.Add(1) }}
	second := &workspaceRunnerConnection{ctx: secondCtx, cancel: secondCancel, write: func(string, any) error { return nil }, close: func() { secondClosed.Add(1) }}
	runner.replaceConnection(first)
	runner.replaceConnection(second)
	select {
	case <-firstCtx.Done():
	default:
		t.Fatal("reconnect did not cancel the replaced connection context")
	}
	if firstClosed.Load() != 1 || runner.connection != second {
		t.Fatalf("first close count=%d current=%p want second=%p", firstClosed.Load(), runner.connection, second)
	}
	runner.releaseConnection(second)
	select {
	case <-secondCtx.Done():
	default:
		t.Fatal("release did not cancel the current connection context")
	}
	if secondClosed.Load() != 1 || runner.connection != nil {
		t.Fatalf("second close count=%d current=%p", secondClosed.Load(), runner.connection)
	}
}

func TestDaemonStartsWorkspaceRunnerWithoutOwningSocketInternals(t *testing.T) {
	raw, err := os.ReadFile("workspace_runner.go")
	if err != nil {
		t.Fatal(err)
	}
	source := string(raw)
	if !strings.Contains(source, "go runner.Run(child)") {
		t.Fatal("Daemon does not start WorkspaceRunner through Run(ctx)")
	}
	for _, forbidden := range []string{
		"func (d *Daemon) runWorkspaceRunner(",
		"func (d *Daemon) runWorkspaceRunnerConnection(",
	} {
		if strings.Contains(source, forbidden) {
			t.Fatalf("Daemon still owns Workspace Runner socket lifecycle %q", forbidden)
		}
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
	attachments := newLocalAgentAttachmentRegistry(t.TempDir(), nil)
	runtimes := newCanonicalAgentRuntimePool()
	diagnostics := &runnerDiagnosticRegistry{}
	dependencies := workspaceRunnerDependencies{
		client: NewClient(""), attachments: attachments, runtimes: runtimes,
		diagnostics: diagnostics,
		openInbox:   func(InboxKey, string) (*MessageCoordinator, error) { return nil, nil },
		runtimeSet: func() AgentAttachmentRuntimeSet {
			return AgentAttachmentRuntimeSet{WorkspaceID: "workspace-1"}
		},
		ensureResidentRuntime: func(context.Context, string, string) error { return nil },
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
	if first.attachments != attachments || first.runtimes != runtimes || first.diagnostics != diagnostics {
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
	if runner.attachments != d.agentAttachments || runner.runtimes != d.canonicalRuntimes || runner.diagnostics != d.runnerDiagnostics {
		t.Fatal("Daemon did not inject its machine-wide owners")
	}
	if runner.processes.admission == nil {
		t.Fatal("Daemon did not inject machine-wide process admission")
	}
}
