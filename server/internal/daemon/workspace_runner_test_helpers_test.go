package daemon

import (
	"context"
	"testing"
)

func attachTestWorkspaceRunner(t *testing.T, d *Daemon, workspaceID string, send func(string, any) error) (*WorkspaceRunner, *workspaceRunnerConnection) {
	t.Helper()
	if send == nil {
		send = func(string, any) error { return nil }
	}
	attachments := d.attachmentRegistry()
	if attachments == nil {
		attachments = newLocalAgentAttachmentRegistry("", nil)
	}
	inboxes, err := newInboxRegistry(workspaceID, inboxRegistryDependencies{
		attachments: attachments,
		ownsRuntime: func(runtimeID string) bool {
			return d.ownsWorkspaceRunnerRuntime(workspaceID, runtimeID)
		},
		open:   d.openMessageCoordinator,
		logger: d.logger,
	})
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	connection := &workspaceRunnerConnection{
		workspaceID: workspaceID,
		ctx:         ctx,
		cancel:      cancel,
		write:       send,
		close:       func() {},
	}
	runner := &WorkspaceRunner{
		config:    WorkspaceRunnerConfig{WorkspaceID: workspaceID},
		daemon:    d,
		processes: newAgentProcessManager(d.cfg.MaxAgentProcesses, nil, nil),
		activity:  newAgentActivityProducer(d.runnerInstanceID, nil, nil),
		inboxes:   inboxes,
	}
	runner.replaceConnection(connection)
	d.attachWorkspaceRunner(runner)
	t.Cleanup(func() {
		d.detachWorkspaceRunner(runner)
		runner.releaseConnection(connection)
		runner.inboxes.Close()
	})
	return runner, connection
}

func registerTestInbox(t *testing.T, d *Daemon, key InboxKey, runtimeID string, coordinator *MessageCoordinator) *WorkspaceRunner {
	t.Helper()
	runner := d.currentWorkspaceRunner(key.WorkspaceID)
	if runner == nil {
		runner, _ = attachTestWorkspaceRunner(t, d, key.WorkspaceID, nil)
	}
	registerTestRunnerInbox(t, runner, key, runtimeID, coordinator)
	return runner
}

func installTestRunnerActivity(t *testing.T, d *Daemon, workspaceID string, producer *agentActivityProducer) *WorkspaceRunner {
	t.Helper()
	runner := d.currentWorkspaceRunner(workspaceID)
	if runner == nil {
		runner, _ = attachTestWorkspaceRunner(t, d, workspaceID, nil)
	}
	runner.activity = producer
	return runner
}

func registerTestRunnerInbox(t *testing.T, runner *WorkspaceRunner, key InboxKey, runtimeID string, coordinator *MessageCoordinator) {
	t.Helper()
	if runner == nil || runner.inboxes == nil {
		t.Fatal("test Workspace Runner has no Inbox registry")
	}
	if runner.config.WorkspaceID != key.WorkspaceID {
		t.Fatalf("Runner Workspace %q cannot register Inbox %+v", runner.config.WorkspaceID, key)
	}
	runner.inboxes.mu.Lock()
	if previous := runner.inboxes.inboxes[key.AgentID]; previous.coordinator != nil && previous.coordinator != coordinator {
		previous.coordinator.Close()
	}
	runner.inboxes.inboxes[key.AgentID] = inboxRegistryEntry{runtimeID: runtimeID, coordinator: coordinator}
	runner.inboxes.mu.Unlock()
}

func resolveTestInbox(t *testing.T, d *Daemon, key InboxKey) (*MessageCoordinator, string) {
	t.Helper()
	runner := d.currentWorkspaceRunner(key.WorkspaceID)
	if runner == nil || runner.inboxes == nil {
		t.Fatalf("Workspace Runner %q is unavailable", key.WorkspaceID)
	}
	coordinator, runtimeID, ok := runner.inboxes.Resolve(key.AgentID)
	if !ok {
		t.Fatalf("Inbox %+v is unavailable", key)
	}
	return coordinator, runtimeID
}

func prepareHeadlessWorkspaceRunnerTestDaemon(d *Daemon, workspacesRoot string) {
	if d.cfg.DaemonID == "" {
		d.cfg.DaemonID = "daemon-test"
	}
	if d.cfg.WorkspacesRoot == "" {
		d.cfg.WorkspacesRoot = workspacesRoot
	}
	if d.runnerInstanceID == "" {
		d.runnerInstanceID = "runner-test"
	}
	if d.canonicalRuntimes == nil {
		d.canonicalRuntimes = newCanonicalAgentRuntimePool()
	}
	if d.agentAttachments == nil {
		d.agentAttachments = newLocalAgentAttachmentRegistry(workspacesRoot, d.logger)
	}
	if d.workspaceRunners == nil {
		d.workspaceRunners = make(map[string]*WorkspaceRunner)
	}
}
