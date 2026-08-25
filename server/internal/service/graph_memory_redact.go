package service

import "github.com/multica-ai/multica/server/internal/diagnosticlog"

// RedactGraphMemoryObservability removes credentials and unsafe diagnostics
// before graph-memory errors are persisted or returned through observability
// APIs. It intentionally shares diagnosticlog's existing patterns.
func RedactGraphMemoryObservability(value string) string {
	return diagnosticlog.SanitizeText(value)
}
