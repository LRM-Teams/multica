package daemon

import (
	"context"
	"testing"
)

func attachTestWorkspaceRunner(t *testing.T, d *Daemon, workspaceID string, send func(string, any) error) (*WorkspaceRunner, *workspaceRunnerConnection) {
	t.Helper()
	ctx, cancel := context.WithCancel(context.Background())
	connection := &workspaceRunnerConnection{
		workspaceID: workspaceID,
		ctx:         ctx,
		cancel:      cancel,
		write:       send,
		close:       func() {},
	}
	runner := &WorkspaceRunner{
		config: WorkspaceRunnerConfig{WorkspaceID: workspaceID},
		daemon: d,
	}
	runner.replaceConnection(connection)
	d.attachWorkspaceRunner(runner)
	t.Cleanup(func() {
		d.detachWorkspaceRunner(runner)
		runner.releaseConnection(connection)
	})
	return runner, connection
}
