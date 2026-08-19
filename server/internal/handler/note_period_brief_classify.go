package handler

import (
	"strings"

	"github.com/multica-ai/multica/server/pkg/taskfailure"
)

// Max retries the synthesizer may trigger per collector within one Brief run
// (initial dispatch does not count as a retry).
const notePeriodBriefCollectorMaxRetries = 3

// periodBriefCollectorDisposition is the platform verdict exposed on the
// status board so the synthesizer can abandon vs retry without guessing.
type periodBriefCollectorDisposition struct {
	Status      string // ready | empty | failed | cancelled | stalled | running
	Retryable   bool
	AbandonWhy  string // non-empty when Retryable is false and not ready
	Detail      string // failure / stall reason for the board
	FailureKind string // taskfailure reason or "config" / "transient"
}

// classifyPeriodBriefCollectorOutcome maps job projection + pack harvest into
// a synthesizer-facing status. Permanent config/auth/model failures are never
// retryable — re-running cannot fix a missing API key.
func classifyPeriodBriefCollectorOutcome(
	jobStatus string,
	failureReason string,
	errorText string,
	packReady bool,
	timedOutWhileRunning bool,
) periodBriefCollectorDisposition {
	if packReady {
		return periodBriefCollectorDisposition{
			Status:    "ready",
			Retryable: false,
		}
	}

	combined := strings.TrimSpace(strings.TrimSpace(failureReason) + " " + strings.TrimSpace(errorText))
	permanent, kind, why := periodBriefFailureIsPermanent(combined, failureReason)

	switch {
	case timedOutWhileRunning && (jobStatus == "" || jobStatus == "pending" || jobStatus == "dispatched" || jobStatus == "running"):
		return periodBriefCollectorDisposition{
			Status:      "stalled",
			Retryable:   !permanent,
			AbandonWhy:  why,
			Detail:      firstNonEmpty(combined, "collector still running past safety ceiling"),
			FailureKind: firstNonEmpty(kind, "transient"),
		}
	case jobStatus == "failed":
		if permanent {
			return periodBriefCollectorDisposition{
				Status:      "failed",
				Retryable:   false,
				AbandonWhy:  why,
				Detail:      combined,
				FailureKind: kind,
			}
		}
		return periodBriefCollectorDisposition{
			Status:      "failed",
			Retryable:   true,
			Detail:      combined,
			FailureKind: firstNonEmpty(kind, "transient"),
		}
	case jobStatus == "cancelled":
		return periodBriefCollectorDisposition{
			Status:      "cancelled",
			Retryable:   false,
			AbandonWhy:  "collector job cancelled",
			Detail:      combined,
			FailureKind: "cancelled",
		}
	case jobStatus == "completed":
		// Finished without a pack — often a glitchy turn; retry may help.
		return periodBriefCollectorDisposition{
			Status:      "empty",
			Retryable:   true,
			Detail:      firstNonEmpty(combined, "collector finished without a pack --note-write"),
			FailureKind: "empty_pack",
		}
	case jobStatus == "pending" || jobStatus == "dispatched" || jobStatus == "running":
		return periodBriefCollectorDisposition{
			Status:    "running",
			Retryable: false,
			Detail:    "collector still running",
		}
	default:
		if permanent {
			return periodBriefCollectorDisposition{
				Status:      "failed",
				Retryable:   false,
				AbandonWhy:  why,
				Detail:      combined,
				FailureKind: kind,
			}
		}
		return periodBriefCollectorDisposition{
			Status:      "empty",
			Retryable:   true,
			Detail:      firstNonEmpty(combined, "pack still stub"),
			FailureKind: "empty_pack",
		}
	}
}

// periodBriefFailureIsPermanent reports config/auth/model/quota problems that
// will fail again on re-dispatch until a human fixes the runtime/agent.
func periodBriefFailureIsPermanent(combined, failureReason string) (permanent bool, kind, why string) {
	lower := strings.ToLower(strings.TrimSpace(combined))
	fr := strings.ToLower(strings.TrimSpace(failureReason))

	// Explicit pi / daemon copy seen in the wild ("No API key").
	if strings.Contains(lower, "no api key") ||
		strings.Contains(lower, "missing api key") ||
		(strings.Contains(lower, "api key") && strings.Contains(lower, "not set")) ||
		(strings.Contains(lower, "api_key") && strings.Contains(lower, "missing")) {
		return true, "config", "collector runtime/agent missing model API key — fix config, do not retry"
	}

	switch taskfailure.Reason(fr) {
	case taskfailure.ReasonAgentMissingConfig:
		return true, fr, "missing agent/model config — fix config, do not retry"
	case taskfailure.ReasonAgentProviderAuthOrAccess:
		return true, fr, "provider auth/access failure — fix credentials, do not retry"
	case taskfailure.ReasonAgentModelNotFoundOrUnavailable:
		return true, fr, "model not found/unavailable — fix agent model, do not retry"
	case taskfailure.ReasonAgentProviderQuotaLimit:
		return true, fr, "provider quota/billing lock — fix billing, do not retry"
	case taskfailure.ReasonAgentBlocked:
		return true, fr, "agent blocked — do not retry until unblocked"
	case taskfailure.ReasonAgentRuntimeMissingExecutable,
		taskfailure.ReasonAgentRuntimeVersionUnsupported:
		return true, fr, "collector runner not usable — fix runtime install/version, do not retry"
	case taskfailure.ReasonAPIInvalidRequest:
		return true, fr, "poisoned API request — fix payload/session, do not blind-retry"
	case taskfailure.ReasonAgentContextOverflow:
		return true, fr, "context overflow — not fixed by re-dispatch alone"
	}

	classified := taskfailure.Classify(combined)
	switch classified {
	case taskfailure.ReasonAgentMissingConfig:
		return true, string(classified), "missing agent/model config — fix config, do not retry"
	case taskfailure.ReasonAgentProviderAuthOrAccess:
		return true, string(classified), "provider auth/access failure — fix credentials, do not retry"
	case taskfailure.ReasonAgentModelNotFoundOrUnavailable:
		return true, string(classified), "model not found/unavailable — fix agent model, do not retry"
	case taskfailure.ReasonAgentProviderQuotaLimit:
		return true, string(classified), "provider quota/billing lock — fix billing, do not retry"
	case taskfailure.ReasonAgentRuntimeMissingExecutable,
		taskfailure.ReasonAgentRuntimeVersionUnsupported:
		return true, string(classified), "collector runner not usable — fix runtime install/version, do not retry"
	case taskfailure.ReasonAgentContextOverflow:
		return true, string(classified), "context overflow — not fixed by re-dispatch alone"
	}

	return false, string(classified), ""
}
