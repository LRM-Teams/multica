package daemon

import (
	"context"
	"errors"
	"strings"
	"time"
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

type workspaceRunnerHost interface {
	runWorkspaceRunner(context.Context, string)
}

// workspaceRunnerDependencies are machine-wide owners. WorkspaceRunner keeps
// their references but never copies their state or changes their lifetime.
type workspaceRunnerDependencies struct {
	host         workspaceRunnerHost
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
// authenticated Computer-Workspace Binding. Socket behavior remains delegated
// to the existing Daemon loop until its dedicated migration.
type WorkspaceRunner struct {
	config WorkspaceRunnerConfig
	host   workspaceRunnerHost

	processes *agentProcessManager
	activity  *agentActivityProducer
	inboxes   *workspaceRunnerInboxSlot

	attachments AgentAttachmentRegistry
	runtimes    *canonicalAgentRuntimePool
	credentials *CredentialProxy
	diagnostics *runnerDiagnosticRegistry
}

func newWorkspaceRunner(config WorkspaceRunnerConfig, dependencies workspaceRunnerDependencies) (*WorkspaceRunner, error) {
	config, err := config.validate()
	if err != nil {
		return nil, err
	}
	if dependencies.host == nil || dependencies.attachments == nil || dependencies.runtimes == nil || dependencies.credentials == nil {
		return nil, errors.New("Workspace Runner host, Attachment registry, Runtime pool, and Credential Proxy are required")
	}
	now := dependencies.now
	if now == nil {
		now = time.Now
	}
	return &WorkspaceRunner{
		config: config,
		host:   dependencies.host,
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
	if runner == nil || runner.host == nil {
		return
	}
	runner.host.runWorkspaceRunner(ctx, runner.config.WorkspaceID)
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
		host:         d,
		attachments:  d.attachmentRegistry(),
		runtimes:     d.canonicalRuntimes,
		credentials:  d.CredentialProxy(),
		diagnostics:  d.runnerDiagnostics,
		now:          time.Now,
		onTransition: onTransition,
	})
}
