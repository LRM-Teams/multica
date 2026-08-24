package daemon

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"runtime"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/multica-ai/multica/server/pkg/agent"
	"github.com/multica-ai/multica/server/pkg/protocol"
)

// WorkspaceDaemonConfig fixes the identities that must survive socket
// reconnects. Runtime membership is deliberately absent because it is mutable
// input, not WorkspaceDaemon identity.
type WorkspaceDaemonConfig struct {
	DaemonID         string
	DaemonInstanceID string
	WorkspaceID      string
	DeviceName       string
	OS               string
	CLIVersion       string
	// MachineID mirrors Config.MachineID: the OS-level machine fingerprint,
	// reported in the Runner ready payload so the server can persist it on the
	// Computer identity and use it for same-machine convergence (LRM-1570).
	MachineID string
}

func (config WorkspaceDaemonConfig) validate() (WorkspaceDaemonConfig, error) {
	config.DaemonID = strings.TrimSpace(config.DaemonID)
	config.DaemonInstanceID = strings.TrimSpace(config.DaemonInstanceID)
	config.WorkspaceID = strings.TrimSpace(config.WorkspaceID)
	if config.DaemonID == "" || config.DaemonInstanceID == "" || config.WorkspaceID == "" {
		return WorkspaceDaemonConfig{}, errors.New("WorkspaceDaemon Daemon, daemon instance, and Workspace identities are required")
	}
	return config, nil
}

// workspaceDaemonDependencies are machine-wide owners. WorkspaceDaemon keeps
// their references but never copies their state or changes their lifetime.
type workspaceDaemonDependencies struct {
	client                    *Client
	serverBaseURL             string
	workspacesRoot            string
	logger                    *slog.Logger
	runtimes                  *agentRuntimePool
	diagnostics               runnerDiagnosticSink
	openInbox                 inboxCoordinatorFactory
	runtimeIDs                func() []string
	ensureResidentRuntime     func(context.Context, string, string, *agent.PiRunIdentity) error
	configureProviderSession  func(string, string, string) error
	currentProviderSession    func(string, string) (string, error)
	recordProviderSession     func(string, string, string)
	notifyAppInbox            func(context.Context, string, string) error
	retryAppInboxAcks         func(context.Context, string)
	mixedRunActivityAck       func(protocol.MixedRunActivityTransitionAckPayload) error
	mixedRunActivityReplay    func(send func(string, any) error)
	controlHeartbeatInterval  time.Duration
	controlHeartbeatPayload   func(string) protocol.DaemonHeartbeatRequestPayload
	controlHeartbeatAck       func(context.Context, *HeartbeatResponse)
	controlHeartbeatChanges   func() (<-chan struct{}, func())
	handleComputerControl     func(context.Context, string, protocol.ComputerUpgradePayload) error
	handleComputerWorkDigest  func(context.Context, protocol.ComputerWorkDigestPayload) (protocol.WorkDigest, error)
	handleComputerWorkJournal func(context.Context, protocol.ComputerWorkJournalPayload) (bool, error)
	setComputerUpgradeEmit    func(func(string, any) error)
	now                       func() time.Time
	onTransition              func(agentLifecycleTransition)
	// rememberGraphProfile caches the server-delivered effective graph
	// memory profile for this runner's workspace (spec §10). Nil disables.
	rememberGraphProfile func(memoryType string, exploreAgents, exploreMaxRounds int)
}

// WorkspaceDaemon is one long-lived orchestration boundary for an
// authenticated Computer-Workspace Binding. Callers interact through Runner
// methods; its Inbox, process, Activity, and socket state never
// escape to the machine-wide Daemon lifecycle owner.
type WorkspaceDaemon struct {
	config WorkspaceDaemonConfig
	client *Client
	logger *slog.Logger

	serverBaseURL  string
	workspacesRoot string

	processes *agentProcessManager
	activity  *agentActivityProducer
	inboxes   *InboxRegistry

	runtimes    *agentRuntimePool
	diagnostics runnerDiagnosticSink

	runtimeIDs                func() []string
	ensureResidentRuntime     func(context.Context, string, string, *agent.PiRunIdentity) error
	configureProviderSession  func(string, string, string) error
	currentProviderSession    func(string, string) (string, error)
	recordProviderSession     func(string, string, string)
	notifyAppInbox            func(context.Context, string, string) error
	retryAppInboxAcks         func(context.Context, string)
	mixedRunActivityAck       func(protocol.MixedRunActivityTransitionAckPayload) error
	mixedRunActivityReplay    func(send func(string, any) error)
	controlHeartbeatInterval  time.Duration
	controlHeartbeatPayload   func(string) protocol.DaemonHeartbeatRequestPayload
	controlHeartbeatAck       func(context.Context, *HeartbeatResponse)
	controlHeartbeatChanges   func() (<-chan struct{}, func())
	handleComputerControl     func(context.Context, string, protocol.ComputerUpgradePayload) error
	handleComputerWorkDigest  func(context.Context, protocol.ComputerWorkDigestPayload) (protocol.WorkDigest, error)
	handleComputerWorkJournal func(context.Context, protocol.ComputerWorkJournalPayload) (bool, error)
	setComputerUpgradeEmit    func(func(string, any) error)
	rememberGraphProfile      func(memoryType string, exploreAgents, exploreMaxRounds int)

	residency *agentResidencyStore
	life      context.Context
	lifeStop  context.CancelFunc

	connectionMu sync.Mutex
	connection   *DaemonConnection
	onReady      func()
}

func (runner *WorkspaceDaemon) WorkspaceID() string {
	if runner == nil {
		return ""
	}
	return runner.config.WorkspaceID
}

func newWorkspaceDaemon(config WorkspaceDaemonConfig, dependencies workspaceDaemonDependencies) (*WorkspaceDaemon, error) {
	config, err := config.validate()
	if err != nil {
		return nil, err
	}
	if dependencies.client == nil || dependencies.runtimes == nil || dependencies.openInbox == nil || dependencies.runtimeIDs == nil || dependencies.ensureResidentRuntime == nil {
		return nil, errors.New("WorkspaceDaemon client, Runtime pool, Inbox factory, Runtime scope, and resident Runtime are required")
	}
	now := dependencies.now
	if now == nil {
		now = time.Now
	}
	inboxes, err := newInboxRegistry(config.WorkspaceID, inboxRegistryDependencies{
		ownsRuntime: func(runtimeID string) bool {
			for _, current := range dependencies.runtimeIDs() {
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
	return &WorkspaceDaemon{
		config:                    config,
		client:                    dependencies.client,
		logger:                    dependencies.logger,
		serverBaseURL:             dependencies.serverBaseURL,
		workspacesRoot:            dependencies.workspacesRoot,
		processes:                 newAgentProcessManager(now, dependencies.onTransition),
		activity:                  newAgentActivityProducer(config.DaemonInstanceID, now, nil),
		inboxes:                   inboxes,
		runtimes:                  dependencies.runtimes,
		diagnostics:               dependencies.diagnostics,
		runtimeIDs:                dependencies.runtimeIDs,
		ensureResidentRuntime:     dependencies.ensureResidentRuntime,
		configureProviderSession:  dependencies.configureProviderSession,
		currentProviderSession:    dependencies.currentProviderSession,
		recordProviderSession:     dependencies.recordProviderSession,
		notifyAppInbox:            dependencies.notifyAppInbox,
		retryAppInboxAcks:         dependencies.retryAppInboxAcks,
		mixedRunActivityAck:       dependencies.mixedRunActivityAck,
		mixedRunActivityReplay:    dependencies.mixedRunActivityReplay,
		rememberGraphProfile:      dependencies.rememberGraphProfile,
		controlHeartbeatInterval:  dependencies.controlHeartbeatInterval,
		controlHeartbeatPayload:   dependencies.controlHeartbeatPayload,
		controlHeartbeatAck:       dependencies.controlHeartbeatAck,
		controlHeartbeatChanges:   dependencies.controlHeartbeatChanges,
		handleComputerControl:     dependencies.handleComputerControl,
		handleComputerWorkDigest:  dependencies.handleComputerWorkDigest,
		handleComputerWorkJournal: dependencies.handleComputerWorkJournal,
		setComputerUpgradeEmit:    dependencies.setComputerUpgradeEmit,
		residency:                 newAgentResidencyStore(now),
		life:                      life,
		lifeStop:                  lifeStop,
	}, nil
}

func (runner *WorkspaceDaemon) Close() {
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

func (runner *WorkspaceDaemon) Run(ctx context.Context) {
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
			runner.logger.Debug("workspace daemon disconnected", "workspace_id", runner.config.WorkspaceID, "error", err)
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

func (runner *WorkspaceDaemon) sendOnConnection(connection *DaemonConnection, eventType string, payload any) error {
	runner.connectionMu.Lock()
	defer runner.connectionMu.Unlock()
	if runner.connection != connection {
		return errors.New("WorkspaceDaemon connection is stale")
	}
	return connection.Write(eventType, payload)
}

func (runner *WorkspaceDaemon) sendOnCurrentConnection(eventType string, payload any) error {
	runner.connectionMu.Lock()
	defer runner.connectionMu.Unlock()
	if runner.connection == nil || !runner.connection.Connected() {
		return errors.New("WorkspaceDaemon connection is unavailable")
	}
	return runner.connection.Write(eventType, payload)
}

func (runner *WorkspaceDaemon) replaceConnection(next *DaemonConnection) {
	runner.connectionMu.Lock()
	previous := runner.connection
	runner.connection = next
	runner.connectionMu.Unlock()
	if previous != nil && previous != next {
		previous.Close()
	}
}

func (runner *WorkspaceDaemon) releaseConnection(current *DaemonConnection) {
	runner.connectionMu.Lock()
	if runner.connection == current {
		runner.connection = nil
	}
	runner.connectionMu.Unlock()
	current.Close()
}

func (runner *WorkspaceDaemon) runConnection(ctx context.Context) error {
	if runner.client == nil {
		return fmt.Errorf("daemon client unavailable")
	}
	workspaceID := runner.config.WorkspaceID
	token := runner.client.WorkspaceDaemonToken(workspaceID, time.Now())
	if token == "" {
		return fmt.Errorf("workspace daemon token unavailable")
	}
	wsURL, err := daemonConnectionURL(runner.serverBaseURL, workspaceID)
	if err != nil {
		return err
	}
	headers := http.Header{"Authorization": []string{"Bearer " + token}}
	runner.client.addIdentityHeaders(headers)
	dialer := taskWakeupDialer()
	conn, _, err := dialer.DialContext(ctx, wsURL, headers)
	if err != nil {
		return err
	}
	connection := newDaemonConnection(workspaceID, ctx,
		func(eventType string, payload any) error { return writeDaemonConnectionFrame(conn, eventType, payload) },
		func() { _ = conn.Close() },
	)
	if runner.setComputerUpgradeEmit != nil {
		runner.setComputerUpgradeEmit(connection.Write)
		defer runner.setComputerUpgradeEmit(nil)
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
	if err := connection.Write(protocol.EventWorkspaceDaemonReady, protocol.WorkspaceReadyPayload{
		WorkspaceID: workspaceID, DaemonInstanceID: runner.config.DaemonInstanceID,
		DeviceName:         runner.config.DeviceName,
		OS:                 runner.config.OS,
		CLIVersion:         runner.config.CLIVersion,
		MachineID:          runner.config.MachineID,
		RunningAgents:      runner.processes.RunningAgentIDs(),
		ActiveCapabilities: runner.activeCapabilities(),
	}); err != nil {
		return err
	}
	if runner.onReady != nil {
		runner.onReady()
	}
	if runner.retryAppInboxAcks != nil {
		go runner.retryAppInboxAcks(ctx, workspaceID)
	}
	// Replay after ready so the Hub has claimed this socket as the current
	// Runner. agent:status active sent before that claim is dropped as stale.
	if runner.activity != nil {
		for _, frame := range runner.activity.ReconnectFrames() {
			if err := connection.Write(frame.EventType, frame.Payload); err != nil {
				return err
			}
		}
	}
	return runner.serveConnection(connection, conn)
}

func (runner *WorkspaceDaemon) activeCapabilities() []string {
	capabilities := []string{
		protocol.DaemonCapabilityWorkspaceDaemonAgentProcess,
		protocol.DaemonCapabilityWorkspaceDaemonAgentReset,
	}
	if runner != nil && runner.controlHeartbeatPayload != nil && runner.controlHeartbeatAck != nil {
		capabilities = append(capabilities, protocol.DaemonCapabilityWorkspaceDaemonControlPlane)
	}
	return capabilities
}

func (d *Daemon) newWorkspaceDaemon(workspaceID string) (*WorkspaceDaemon, error) {
	if d == nil {
		return nil, errors.New("WorkspaceDaemon Daemon is required")
	}
	onTransition := func(transition agentLifecycleTransition) {
		d.recordAgentLifecycleTransition(transition)
	}
	return newWorkspaceDaemon(WorkspaceDaemonConfig{
		DaemonID:         d.cfg.DaemonID,
		DaemonInstanceID: d.runnerInstanceID,
		WorkspaceID:      workspaceID,
		DeviceName:       d.cfg.DeviceName,
		OS:               normalizeGOOS(runtime.GOOS),
		CLIVersion:       d.cfg.CLIVersion,
		MachineID:        d.cfg.MachineID,
	}, workspaceDaemonDependencies{
		client:         d.client,
		serverBaseURL:  d.cfg.ServerBaseURL,
		workspacesRoot: d.cfg.WorkspacesRoot,
		logger:         d.logger,
		runtimes:       d.canonicalRuntimes,
		diagnostics:    d.runnerDiagnostics,
		openInbox:      d.openMessageCoordinator,
		runtimeIDs: func() []string {
			d.mu.Lock()
			defer d.mu.Unlock()
			ids := make([]string, 0)
			for runtimeID, runtime := range d.runtimeIndex {
				if runtime.WorkspaceID == workspaceID {
					ids = append(ids, runtimeID)
				}
			}
			sort.Strings(ids)
			return ids
		},
		ensureResidentRuntime: d.ensureResidentMessageRuntime,
		configureProviderSession: func(agentID, runtimeID, providerSessionID string) error {
			if d.agentRuntimeSessions == nil {
				return errors.New("Agent provider session store is unavailable")
			}
			if err := d.agentRuntimeSessions.Put(agentID, runtimeID, providerSessionID); err != nil {
				return fmt.Errorf("apply Agent provider session: %w", err)
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
		notifyAppInbox:        d.notifyAgentAppInbox,
		retryAppInboxAcks:     d.retryAgentAppInboxAckIntents,
		mixedRunActivityAck:   d.ackMixedRunActivity,
		mixedRunActivityReplay: func(send func(string, any) error) {
			d.replayMixedRunActivity(workspaceID, send)
		},
		rememberGraphProfile: func(memoryType string, exploreAgents, exploreMaxRounds int) {
			d.rememberGraphProfile(workspaceID, memoryType, exploreAgents, exploreMaxRounds)
		},
		controlHeartbeatInterval:  d.cfg.HeartbeatInterval,
		controlHeartbeatPayload:   d.controlPlaneHeartbeatPayload,
		controlHeartbeatAck:       d.handleWorkspaceDaemonControlAck,
		controlHeartbeatChanges:   func() (<-chan struct{}, func()) { return nil, func() {} },
		handleComputerControl:     d.handleComputerControlCommand,
		handleComputerWorkDigest:  d.handleComputerWorkDigestCommand,
		handleComputerWorkJournal: d.handleComputerWorkJournalCommand,
		setComputerUpgradeEmit:    d.setComputerUpgradeEmit,
		now:                       time.Now,
		onTransition:              onTransition,
	})
}
