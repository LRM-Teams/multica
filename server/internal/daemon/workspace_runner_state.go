package daemon

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"strings"
	"sync"
	"time"

	"github.com/multica-ai/multica/server/pkg/agent"
	"github.com/multica-ai/multica/server/pkg/protocol"
)

// WorkspaceRunnerConfig fixes the identities that must survive socket
// reconnects. Runtime membership is deliberately absent because it is mutable
// input, not Runner identity.
type WorkspaceRunnerConfig struct {
	DaemonID         string
	DaemonInstanceID string
	WorkspaceID      string
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
	client                      *Client
	serverBaseURL               string
	workspacesRoot              string
	logger                      *slog.Logger
	attachments                 *localAgentAttachmentRegistry
	runtimes                    *canonicalAgentRuntimePool
	processAdmission            agentProcessAdmission
	diagnostics                 runnerDiagnosticSink
	openInbox                   inboxCoordinatorFactory
	runtimeSet                  func() AgentAttachmentRuntimeSet
	ensureResidentRuntime       func(context.Context, string, string, *agent.PiRunIdentity) error
	configureProviderSession    func(string, string, *string) error
	currentProviderSession      func(string, string) (string, error)
	recordProviderSession       func(string, string, string)
	mixedRunActivityAck         func(protocol.MixedRunActivityTransitionAckPayload) error
	mixedRunActivityReplay      func(send func(string, any) error)
	requestReminderSnapshot     func(string)
	handleReminderInput         func(context.Context, protocol.ReminderOwnerInputPayload)
	removeDetachedReminderAgent func(string) error
	controlHeartbeatInterval    time.Duration
	controlHeartbeatPayload     func(string) protocol.DaemonHeartbeatRequestPayload
	controlHeartbeatAck         func(context.Context, *HeartbeatResponse)
	controlHeartbeatChanges     func() (<-chan struct{}, func())
	now                         func() time.Time
	onTransition                func(agentLifecycleTransition)
}

// WorkspaceRunner is one long-lived orchestration boundary for an
// authenticated Computer-Workspace Binding. Callers interact through Runner
// methods; its Inbox, process, Activity, Attachment, and socket state never
// escape to the machine-wide Daemon lifecycle owner.
type WorkspaceRunner struct {
	config WorkspaceRunnerConfig
	client *Client
	logger *slog.Logger

	serverBaseURL  string
	workspacesRoot string

	processes *agentProcessManager
	activity  *agentActivityProducer
	inboxes   *InboxRegistry

	attachments *localAgentAttachmentRegistry
	runtimes    *canonicalAgentRuntimePool
	diagnostics runnerDiagnosticSink

	runtimeSet                  func() AgentAttachmentRuntimeSet
	ensureResidentRuntime       func(context.Context, string, string, *agent.PiRunIdentity) error
	configureProviderSession    func(string, string, *string) error
	currentProviderSession      func(string, string) (string, error)
	recordProviderSession       func(string, string, string)
	mixedRunActivityAck         func(protocol.MixedRunActivityTransitionAckPayload) error
	mixedRunActivityReplay      func(send func(string, any) error)
	requestReminderSnapshot     func(string)
	handleReminderInput         func(context.Context, protocol.ReminderOwnerInputPayload)
	removeDetachedReminderAgent func(string) error
	controlHeartbeatInterval    time.Duration
	controlHeartbeatPayload     func(string) protocol.DaemonHeartbeatRequestPayload
	controlHeartbeatAck         func(context.Context, *HeartbeatResponse)
	controlHeartbeatChanges     func() (<-chan struct{}, func())

	residency *agentResidencyStore
	life      context.Context
	lifeStop  context.CancelFunc

	connectionMu sync.Mutex
	connection   *workspaceRunnerConnection
	onReady      func()
}

func (runner *WorkspaceRunner) WorkspaceID() string {
	if runner == nil {
		return ""
	}
	return runner.config.WorkspaceID
}

func newWorkspaceRunner(config WorkspaceRunnerConfig, dependencies workspaceRunnerDependencies) (*WorkspaceRunner, error) {
	config, err := config.validate()
	if err != nil {
		return nil, err
	}
	if dependencies.client == nil || dependencies.attachments == nil || dependencies.runtimes == nil || dependencies.processAdmission == nil || dependencies.openInbox == nil || dependencies.runtimeSet == nil || dependencies.ensureResidentRuntime == nil {
		return nil, errors.New("Workspace Runner client, Attachment registry, Runtime pool, process admission, Inbox factory, Runtime scope, and resident Runtime are required")
	}
	now := dependencies.now
	if now == nil {
		now = time.Now
	}
	inboxes, err := newInboxRegistry(config.WorkspaceID, inboxRegistryDependencies{
		attachments: dependencies.attachments,
		ownsRuntime: func(runtimeID string) bool {
			for _, current := range dependencies.runtimeSet().RuntimeIDs {
				if current == runtimeID {
					return true
				}
			}
			return false
		},
		open:   dependencies.openInbox,
		logger: dependencies.logger,
	})
	if err != nil {
		return nil, err
	}
	life, lifeStop := context.WithCancel(context.Background())
	return &WorkspaceRunner{
		config:         config,
		client:         dependencies.client,
		logger:         dependencies.logger,
		serverBaseURL:  dependencies.serverBaseURL,
		workspacesRoot: dependencies.workspacesRoot,
		processes: newAgentProcessManager(
			config.WorkspaceID,
			dependencies.processAdmission,
			now,
			dependencies.onTransition,
		),
		activity:                    newAgentActivityProducer(config.DaemonInstanceID, now, nil),
		inboxes:                     inboxes,
		attachments:                 dependencies.attachments,
		runtimes:                    dependencies.runtimes,
		diagnostics:                 dependencies.diagnostics,
		runtimeSet:                  dependencies.runtimeSet,
		ensureResidentRuntime:       dependencies.ensureResidentRuntime,
		configureProviderSession:    dependencies.configureProviderSession,
		currentProviderSession:      dependencies.currentProviderSession,
		recordProviderSession:       dependencies.recordProviderSession,
		mixedRunActivityAck:         dependencies.mixedRunActivityAck,
		mixedRunActivityReplay:      dependencies.mixedRunActivityReplay,
		requestReminderSnapshot:     dependencies.requestReminderSnapshot,
		handleReminderInput:         dependencies.handleReminderInput,
		removeDetachedReminderAgent: dependencies.removeDetachedReminderAgent,
		controlHeartbeatInterval:    dependencies.controlHeartbeatInterval,
		controlHeartbeatPayload:     dependencies.controlHeartbeatPayload,
		controlHeartbeatAck:         dependencies.controlHeartbeatAck,
		controlHeartbeatChanges:     dependencies.controlHeartbeatChanges,
		residency:                   newAgentResidencyStore(now),
		life:                        life,
		lifeStop:                    lifeStop,
	}, nil
}

func (runner *WorkspaceRunner) Close() {
	if runner == nil {
		return
	}
	if runner.lifeStop != nil {
		runner.lifeStop()
	}
	if runner.residency != nil {
		runner.residency.close()
	}
}

func (runner *WorkspaceRunner) Run(ctx context.Context) {
	if runner == nil || runner.client == nil {
		return
	}
	defer func() {
		runner.Close()
		runner.inboxes.Close()
		runner.processes.Close()
		runner.activity.Close()
	}()
	backoff := time.Second
	for ctx.Err() == nil {
		if err := runner.runConnection(ctx); err != nil && ctx.Err() == nil && runner.logger != nil {
			runner.logger.Debug("workspace runner disconnected", "workspace_id", runner.config.WorkspaceID, "error", err)
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
	if runner.client == nil {
		return fmt.Errorf("daemon client unavailable")
	}
	workspaceID := runner.config.WorkspaceID
	token := runner.client.WorkspaceDaemonToken(workspaceID, time.Now())
	if token == "" {
		return fmt.Errorf("workspace daemon token unavailable")
	}
	wsURL, err := workspaceRunnerURL(runner.serverBaseURL, workspaceID)
	if err != nil {
		return err
	}
	headers := http.Header{"Authorization": []string{"Bearer " + token}}
	if runner.client.platform != "" {
		headers.Set("X-Client-Platform", runner.client.platform)
	}
	if runner.client.version != "" {
		headers.Set("X-Client-Version", runner.client.version)
	}
	if runner.client.os != "" {
		headers.Set("X-Client-OS", runner.client.os)
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
	stopWatch := make(chan struct{})
	defer close(stopWatch)
	go func() {
		select {
		case <-ctx.Done():
			connection.Close()
		case <-stopWatch:
		}
	}()
	if err := connection.Write(protocol.EventWorkspaceRunnerReady, protocol.WorkspaceRunnerReadyPayload{
		WorkspaceID: workspaceID, DaemonInstanceID: runner.config.DaemonInstanceID,
		RunningAgents:      runner.processes.RunningAgentIDs(),
		ActiveCapabilities: runner.activeCapabilities(),
	}); err != nil {
		return err
	}
	if runner.onReady != nil {
		runner.onReady()
	}
	return runner.serveConnection(connection, conn)
}

func (runner *WorkspaceRunner) activeCapabilities() []string {
	capabilities := []string{
		protocol.DaemonCapabilityWorkspaceRunnerAttachment,
		protocol.DaemonCapabilityWorkspaceRunnerAgentReset,
		protocol.DaemonCapabilityReminderTransientInput,
	}
	if runner != nil && runner.controlHeartbeatPayload != nil && runner.controlHeartbeatAck != nil {
		capabilities = append(capabilities, protocol.DaemonCapabilityWorkspaceRunnerControlPlane)
	}
	return capabilities
}

func (d *Daemon) newWorkspaceRunner(workspaceID string) (*WorkspaceRunner, error) {
	if d == nil {
		return nil, errors.New("Workspace Runner Daemon is required")
	}
	onTransition := func(transition agentLifecycleTransition) {
		d.recordAgentLifecycleTransition(transition)
	}
	return newWorkspaceRunner(WorkspaceRunnerConfig{
		DaemonID:         d.cfg.DaemonID,
		DaemonInstanceID: d.runnerInstanceID,
		WorkspaceID:      workspaceID,
	}, workspaceRunnerDependencies{
		client:           d.client,
		serverBaseURL:    d.cfg.ServerBaseURL,
		workspacesRoot:   d.cfg.WorkspacesRoot,
		logger:           d.logger,
		attachments:      d.attachmentRegistry(),
		runtimes:         d.canonicalRuntimes,
		processAdmission: d.processAdmission,
		diagnostics:      d.runnerDiagnostics,
		openInbox:        d.openMessageCoordinator,
		runtimeSet: func() AgentAttachmentRuntimeSet {
			return d.attachmentRuntimeSet(workspaceID)
		},
		ensureResidentRuntime: d.ensureResidentMessageRuntime,
		configureProviderSession: func(agentID, runtimeID string, providerSessionID *string) error {
			if providerSessionID != nil {
				if d.agentRuntimeSessions == nil {
					return errors.New("Agent provider session store is unavailable")
				}
				if err := d.agentRuntimeSessions.Put(agentID, runtimeID, *providerSessionID); err != nil {
					return fmt.Errorf("apply Agent provider session: %w", err)
				}
			}
			return nil
		},
		currentProviderSession: func(agentID, runtimeID string) (string, error) {
			if d.agentRuntimeSessions == nil {
				return "", errors.New("Agent provider session store is unavailable")
			}
			return d.agentRuntimeSessions.Get(agentID, runtimeID)
		},
		recordProviderSession: d.recordProviderSession,
		mixedRunActivityAck:   d.ackMixedRunActivity,
		mixedRunActivityReplay: func(send func(string, any) error) {
			d.replayMixedRunActivity(workspaceID, send)
		},
		requestReminderSnapshot: func(agentID string) {
			d.requestReminderSnapshot(workspaceID, agentID)
		},
		handleReminderInput: func(ctx context.Context, payload protocol.ReminderOwnerInputPayload) {
			d.handleReminderOwnerInput(ctx, payload)
		},
		removeDetachedReminderAgent: d.removeDetachedReminderAgent,
		controlHeartbeatInterval:    d.cfg.HeartbeatInterval,
		controlHeartbeatPayload:     d.controlPlaneHeartbeatPayload,
		controlHeartbeatAck:         d.handleWorkspaceRunnerControlAck,
		controlHeartbeatChanges: func() (<-chan struct{}, func()) {
			if d.updateObservation == nil {
				return nil, func() {}
			}
			return d.updateObservation.Subscribe()
		},
		now:          time.Now,
		onTransition: onTransition,
	})
}
