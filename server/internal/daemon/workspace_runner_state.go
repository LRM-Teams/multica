package daemon

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"strings"
	"sync"
	"time"

	"github.com/multica-ai/multica/server/pkg/protocol"
)

// WorkspaceRunnerConfig fixes the identities that must survive socket
// reconnects. Runtime membership is deliberately absent because it is mutable
// input, not Runner identity.
type WorkspaceRunnerConfig struct {
	DaemonID          string
	DaemonInstanceID  string
	WorkspaceID       string
	MaxAgentProcesses int
}

func (config WorkspaceRunnerConfig) validate() (WorkspaceRunnerConfig, error) {
	config.DaemonID = strings.TrimSpace(config.DaemonID)
	config.DaemonInstanceID = strings.TrimSpace(config.DaemonInstanceID)
	config.WorkspaceID = strings.TrimSpace(config.WorkspaceID)
	if config.DaemonID == "" || config.DaemonInstanceID == "" || config.WorkspaceID == "" {
		return WorkspaceRunnerConfig{}, errors.New("Workspace Runner Daemon, daemon instance, and Workspace identities are required")
	}
	return config, nil
}

// workspaceRunnerDependencies are machine-wide owners. WorkspaceRunner keeps
// their references but never copies their state or changes their lifetime.
type workspaceRunnerDependencies struct {
	daemon       *Daemon
	attachments  AgentAttachmentRegistry
	runtimes     *canonicalAgentRuntimePool
	credentials  *CredentialProxy
	diagnostics  *runnerDiagnosticRegistry
	now          func() time.Time
	onTransition func(agentLifecycleTransition)
}

// workspaceRunnerInboxSlot reserves the fixed Workspace ownership point for
// the InboxRegistry extraction. It intentionally owns no coordinator map yet;
// Delivery and recovery remain on Daemon until the dedicated migration.
type workspaceRunnerInboxSlot struct {
	workspaceID string
}

// WorkspaceRunner is one long-lived orchestration boundary for an
// authenticated Computer-Workspace Binding. It owns socket lifecycle while
// frame business handlers continue delegating to machine-wide Daemon services.
type WorkspaceRunner struct {
	config WorkspaceRunnerConfig
	daemon *Daemon

	processes *agentProcessManager
	activity  *agentActivityProducer
	inboxes   *workspaceRunnerInboxSlot

	attachments AgentAttachmentRegistry
	runtimes    *canonicalAgentRuntimePool
	credentials *CredentialProxy
	diagnostics *runnerDiagnosticRegistry

	connectionMu sync.Mutex
	connection   *workspaceRunnerConnection
}

func newWorkspaceRunner(config WorkspaceRunnerConfig, dependencies workspaceRunnerDependencies) (*WorkspaceRunner, error) {
	config, err := config.validate()
	if err != nil {
		return nil, err
	}
	if dependencies.daemon == nil || dependencies.attachments == nil || dependencies.runtimes == nil || dependencies.credentials == nil {
		return nil, errors.New("Workspace Runner Daemon, Attachment registry, Runtime pool, and Credential Proxy are required")
	}
	now := dependencies.now
	if now == nil {
		now = time.Now
	}
	return &WorkspaceRunner{
		config: config,
		daemon: dependencies.daemon,
		processes: newAgentProcessManager(
			config.MaxAgentProcesses,
			now,
			dependencies.onTransition,
		),
		activity:    newAgentActivityProducer(config.DaemonInstanceID, now, nil),
		inboxes:     &workspaceRunnerInboxSlot{workspaceID: config.WorkspaceID},
		attachments: dependencies.attachments,
		runtimes:    dependencies.runtimes,
		credentials: dependencies.credentials,
		diagnostics: dependencies.diagnostics,
	}, nil
}

func (runner *WorkspaceRunner) Run(ctx context.Context) {
	if runner == nil || runner.daemon == nil {
		return
	}
	runner.daemon.attachWorkspaceRunner(runner)
	defer runner.daemon.detachWorkspaceRunner(runner)
	backoff := time.Second
	for ctx.Err() == nil {
		if err := runner.runConnection(ctx); err != nil && ctx.Err() == nil && runner.daemon.logger != nil {
			runner.daemon.logger.Debug("workspace runner disconnected", "workspace_id", runner.config.WorkspaceID, "error", err)
		}
		timer := time.NewTimer(backoff)
		select {
		case <-ctx.Done():
			timer.Stop()
			return
		case <-timer.C:
		}
		if backoff < 15*time.Second {
			backoff *= 2
		}
	}
}

type workspaceRunnerConnection struct {
	workspaceID string
	ctx         context.Context
	cancel      context.CancelFunc

	writeMu sync.Mutex
	write   func(string, any) error
	close   func()
	once    sync.Once

	deliveries *workspaceRunnerDeliveryDispatcher
}

func (runner *WorkspaceRunner) sendOnConnection(connection *workspaceRunnerConnection, eventType string, payload any) error {
	runner.connectionMu.Lock()
	defer runner.connectionMu.Unlock()
	if runner.connection != connection {
		return errors.New("Workspace Runner connection is stale")
	}
	return connection.Write(eventType, payload)
}

func (runner *WorkspaceRunner) sendOnCurrentConnection(eventType string, payload any) error {
	runner.connectionMu.Lock()
	defer runner.connectionMu.Unlock()
	if runner.connection == nil {
		return errors.New("Workspace Runner connection is unavailable")
	}
	return runner.connection.Write(eventType, payload)
}

func (connection *workspaceRunnerConnection) Write(eventType string, payload any) error {
	connection.writeMu.Lock()
	defer connection.writeMu.Unlock()
	return connection.write(eventType, payload)
}

func (connection *workspaceRunnerConnection) Close() {
	if connection == nil {
		return
	}
	connection.once.Do(func() {
		connection.cancel()
		connection.close()
	})
}

func (runner *WorkspaceRunner) replaceConnection(next *workspaceRunnerConnection) {
	runner.connectionMu.Lock()
	previous := runner.connection
	runner.connection = next
	runner.connectionMu.Unlock()
	if previous != nil && previous != next {
		previous.Close()
	}
}

func (runner *WorkspaceRunner) releaseConnection(current *workspaceRunnerConnection) {
	runner.connectionMu.Lock()
	if runner.connection == current {
		runner.connection = nil
	}
	runner.connectionMu.Unlock()
	current.Close()
}

func (runner *WorkspaceRunner) runConnection(ctx context.Context) error {
	d := runner.daemon
	if d.client == nil {
		return fmt.Errorf("daemon client unavailable")
	}
	workspaceID := runner.config.WorkspaceID
	token := d.client.WorkspaceDaemonToken(workspaceID, time.Now())
	if token == "" {
		return fmt.Errorf("workspace daemon token unavailable")
	}
	wsURL, err := workspaceRunnerURL(d.cfg.ServerBaseURL, workspaceID)
	if err != nil {
		return err
	}
	headers := http.Header{"Authorization": []string{"Bearer " + token}}
	if d.client.platform != "" {
		headers.Set("X-Client-Platform", d.client.platform)
	}
	if d.client.version != "" {
		headers.Set("X-Client-Version", d.client.version)
	}
	if d.client.os != "" {
		headers.Set("X-Client-OS", d.client.os)
	}
	dialer := taskWakeupDialer()
	conn, _, err := dialer.DialContext(ctx, wsURL, headers)
	if err != nil {
		return err
	}
	connectionCtx, cancel := context.WithCancel(ctx)
	connection := &workspaceRunnerConnection{
		workspaceID: workspaceID, ctx: connectionCtx, cancel: cancel,
		write: func(eventType string, payload any) error { return writeWorkspaceRunnerFrame(conn, eventType, payload) },
		close: func() { _ = conn.Close() },
	}
	runner.replaceConnection(connection)
	defer runner.releaseConnection(connection)
	if err := connection.Write(protocol.EventWorkspaceRunnerReady, protocol.WorkspaceRunnerReadyPayload{
		WorkspaceID: workspaceID, DaemonInstanceID: runner.config.DaemonInstanceID,
	}); err != nil {
		return err
	}
	return runner.serveConnection(connection, conn)
}

func (d *Daemon) newWorkspaceRunner(workspaceID string) (*WorkspaceRunner, error) {
	if d == nil {
		return nil, errors.New("Workspace Runner Daemon is required")
	}
	onTransition := func(transition agentLifecycleTransition) {
		if d.lifecycleDiagnostics == nil {
			return
		}
		if err := d.lifecycleDiagnostics.Record(transition); err != nil && d.logger != nil {
			// Local diagnostics are intentionally non-blocking for lifecycle.
			d.logger.Debug("agent lifecycle diagnostic write failed", "error", err)
		}
	}
	return newWorkspaceRunner(WorkspaceRunnerConfig{
		DaemonID:          d.cfg.DaemonID,
		DaemonInstanceID:  d.runnerInstanceID,
		WorkspaceID:       workspaceID,
		MaxAgentProcesses: d.cfg.MaxAgentProcesses,
	}, workspaceRunnerDependencies{
		daemon:       d,
		attachments:  d.attachmentRegistry(),
		runtimes:     d.canonicalRuntimes,
		credentials:  d.CredentialProxy(),
		diagnostics:  d.runnerDiagnostics,
		now:          time.Now,
		onTransition: onTransition,
	})
}
