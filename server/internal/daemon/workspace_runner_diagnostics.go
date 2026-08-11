package daemon

import "github.com/multica-ai/multica/server/internal/diagnosticlog"

func (runner *WorkspaceRunner) recordDiagnostic(event diagnosticlog.Event) {
	if runner == nil || runner.diagnostics == nil {
		return
	}
	if err := runner.diagnostics.record(runner.config.WorkspaceID, event); err != nil && runner.logger != nil {
		// Diagnostic persistence never changes product control flow.
		runner.logger.Warn("Workspace Runner diagnostic record dropped", "reason", "sink_unavailable")
	}
}
