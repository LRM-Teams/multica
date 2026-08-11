package daemon

import (
	"context"
	"testing"
)

var _ interface{ Run(context.Context) } = (*WorkspaceRunner)(nil)

type workspaceRunnerHostFunc func(context.Context, string)

func (f workspaceRunnerHostFunc) runWorkspaceRunner(ctx context.Context, workspaceID string) {
	f(ctx, workspaceID)
}

func TestWorkspaceRunnerConstructionRequiresFixedIdentity(t *testing.T) {
	host := workspaceRunnerHostFunc(func(context.Context, string) {})
	registry := newLocalAgentAttachmentRegistry(t.TempDir(), nil)
	runtimes := newCanonicalAgentRuntimePool()
	credentials := &CredentialProxy{}
	base := WorkspaceRunnerConfig{
		DaemonID: "daemon-1", DaemonInstanceID: "daemon-instance-1", WorkspaceID: "workspace-1",
	}
	dependencies := workspaceRunnerDependencies{
		host: host, attachments: registry, runtimes: runtimes, credentials: credentials,
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
		"host":        func(dependencies *workspaceRunnerDependencies) { dependencies.host = nil },
		"attachments": func(dependencies *workspaceRunnerDependencies) { dependencies.attachments = nil },
		"runtimes":    func(dependencies *workspaceRunnerDependencies) { dependencies.runtimes = nil },
		"credentials": func(dependencies *workspaceRunnerDependencies) { dependencies.credentials = nil },
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

func TestWorkspaceRunnerOwnsLocalStateAndSharesMachineDependencies(t *testing.T) {
	host := workspaceRunnerHostFunc(func(context.Context, string) {})
	attachments := newLocalAgentAttachmentRegistry(t.TempDir(), nil)
	runtimes := newCanonicalAgentRuntimePool()
	credentials := &CredentialProxy{}
	diagnostics := &runnerDiagnosticRegistry{}
	dependencies := workspaceRunnerDependencies{
		host: host, attachments: attachments, runtimes: runtimes,
		credentials: credentials, diagnostics: diagnostics,
	}
	first, err := newWorkspaceRunner(WorkspaceRunnerConfig{
		DaemonID: "daemon-1", DaemonInstanceID: "instance-1", WorkspaceID: "workspace-1", MaxAgentProcesses: 2,
	}, dependencies)
	if err != nil {
		t.Fatal(err)
	}
	second, err := newWorkspaceRunner(WorkspaceRunnerConfig{
		DaemonID: "daemon-1", DaemonInstanceID: "instance-1", WorkspaceID: "workspace-2", MaxAgentProcesses: 2,
	}, dependencies)
	if err != nil {
		t.Fatal(err)
	}
	if first.attachments != attachments || first.runtimes != runtimes || first.credentials != credentials || first.diagnostics != diagnostics {
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

func TestWorkspaceRunnerRunUsesFixedWorkspaceIdentity(t *testing.T) {
	called := make(chan string, 1)
	host := workspaceRunnerHostFunc(func(_ context.Context, workspaceID string) { called <- workspaceID })
	runner, err := newWorkspaceRunner(WorkspaceRunnerConfig{
		DaemonID: "daemon-1", DaemonInstanceID: "instance-1", WorkspaceID: "workspace-1",
	}, workspaceRunnerDependencies{
		host:        host,
		attachments: newLocalAgentAttachmentRegistry(t.TempDir(), nil),
		runtimes:    newCanonicalAgentRuntimePool(),
		credentials: &CredentialProxy{},
	})
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	runner.Run(ctx)
	if got := <-called; got != "workspace-1" {
		t.Fatalf("Run Workspace = %q, want workspace-1", got)
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
	if runner.attachments != d.agentAttachments || runner.runtimes != d.canonicalRuntimes || runner.credentials.daemon != d || runner.diagnostics != d.runnerDiagnostics {
		t.Fatal("Daemon did not inject its machine-wide owners")
	}
	if runner.config.MaxAgentProcesses != 3 {
		t.Fatalf("Runner process cap = %d, want 3", runner.config.MaxAgentProcesses)
	}
}
