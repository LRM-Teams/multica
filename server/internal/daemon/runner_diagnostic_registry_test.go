package daemon

import (
	"errors"
	"strings"
	"sync"

	"github.com/multica-ai/multica/server/internal/diagnosticlog"
)

// runnerDiagnosticRegistry is a test sink for diagnostic event assertions.
// Production WorkspaceDaemonCore processes forward events to ComputerCore,
// whose diagnosticLoggers map is the sole owner of durable log writers.
type runnerDiagnosticRegistry struct {
	store             *diagnosticlog.Store
	environment       diagnosticlog.Environment
	daemonInstanceID  string
	computerID        string
	serviceGeneration string

	mu      sync.Mutex
	loggers map[string]*diagnosticlog.Logger
	failed  map[string]struct{}
}

func (r *runnerDiagnosticRegistry) record(workspaceID string, event diagnosticlog.Event) error {
	workspaceID = strings.TrimSpace(workspaceID)
	if r == nil || r.store == nil {
		return errors.New("diagnostic store is unavailable")
	}
	r.mu.Lock()
	logger := r.loggers[workspaceID]
	_, failed := r.failed[workspaceID]
	if logger == nil && !failed {
		var err error
		logger, err = r.store.Runner(diagnosticlog.RunnerOptions{
			Environment:       r.environment,
			WorkspaceID:       workspaceID,
			DaemonInstanceID:  r.daemonInstanceID,
			ComputerID:        r.computerID,
			ServiceGeneration: r.serviceGeneration,
		})
		if err != nil {
			r.failed[workspaceID] = struct{}{}
			r.mu.Unlock()
			return err
		}
		r.loggers[workspaceID] = logger
	}
	r.mu.Unlock()
	if logger == nil {
		return errors.New("diagnostic logger is unavailable")
	}
	return logger.Record(event)
}
