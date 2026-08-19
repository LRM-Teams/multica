package daemon

import (
	"strings"

	"github.com/multica-ai/multica/server/pkg/agent"
	"github.com/multica-ai/multica/server/pkg/taskfailure"
)

// FailureReason values for tasks whose session is "poisoned" — i.e.
// resuming the same conversation on a follow-up task would deterministically
// reproduce the same failure. Listed here so the server-side query
// GetLastTaskSession can filter them out and the next task starts from
// a fresh agent session instead of inheriting the bad state.
//
// Two flavors:
//   - Output-side: agent "completed" with output that is actually a known
//     fallback marker (gave up mid-thought, emitted a meta message). Detected
//     via classifyPoisonedOutput.
//   - Error-side: the LLM API itself rejected the request with a 400
//     invalid_request_error (oversized payload, malformed image, etc.).
//     The bad message is already baked into the conversation history, so
//     every resume hits the same 400. Detected via classifyPoisonedError.
//   - Timeout-side: provider adapters report semantic/no-progress inactivity
//     after the session got stuck without agent progress. Resuming that session
//     can replay the same stuck state, while a fresh manual rerun may succeed.
//     Detected via classifyResumeUnsafeTimeout.
//
// MUL-2946: ReasonIterationLimit and ReasonAPIInvalidRequest are aliased
// to the canonical taskfailure values so the daemon and the in-flight
// classifier (used by every other failure path) share a single source
// of truth. agent_fallback_message and codex_semantic_inactivity are
// pre-existing operational reasons not in the canonical 21 — kept as
// string literals here until a follow-up PR migrates them or extends
// the taxonomy.
const (
	FailureReasonIterationLimit          = string(taskfailure.ReasonIterationLimit)
	FailureReasonAgentFallbackMsg        = "agent_fallback_message"
	FailureReasonAPIInvalidRequest       = string(taskfailure.ReasonAPIInvalidRequest)
	FailureReasonCodexSemanticInactivity = "codex_semantic_inactivity"
	FailureReasonGrokFirstTurnNoProgress = "grok_first_turn_no_progress"
	FailureReasonGrokToolPermission      = "grok_tool_permission_failure"
)

// poisonedOutputMaxLen caps how long an output can be and still be
// classified as a poisoned fallback. Real fallback messages are short,
// one-sentence affairs; a long output that happens to mention a marker
// is almost certainly a real conclusion (e.g. a code-review reply
// quoting these strings, like the one currently quoting them in
// MUL-1630). The cap intentionally errs on the side of NOT classifying
// — a missed poisoned task gets retried by user action, but a
// false-positive turns a successful task into a failure and a system
// comment.
const poisonedOutputMaxLen = 320

// poisonedMarkers maps a substring fingerprint of a known agent fallback
// terminal message to its failure_reason classifier. Match is case-
// insensitive and substring-based; the cap above prevents long outputs
// that quote a marker from being misclassified.
var poisonedMarkers = []struct {
	Substring string
	Reason    string
}{
	{"i reached the iteration limit", FailureReasonIterationLimit},
	{"put your final update inside the content string", FailureReasonAgentFallbackMsg},
}

// classifyPoisonedOutput reports whether output matches a known agent
// fallback terminal message and, if so, returns the failure_reason that
// should be persisted on the task row. Long outputs are never
// classified: a real fallback is the agent's only utterance for the
// turn, so anything beyond ~one paragraph is treated as a real result
// even if it contains a marker substring.
func classifyPoisonedOutput(output string) (string, bool) {
	trimmed := strings.TrimSpace(output)
	if trimmed == "" || len(trimmed) > poisonedOutputMaxLen {
		return "", false
	}
	lowered := strings.ToLower(trimmed)
	for _, m := range poisonedMarkers {
		if strings.Contains(lowered, m.Substring) {
			return m.Reason, true
		}
	}
	return "", false
}

// classifyFailedOutput catches a provider wrapper failure that was emitted as
// the entire final answer while the wrapper process still exited successfully.
// The exact prefix and short length keep normal answers that quote logs out of
// this path. These failures are retryable according to the shared taskfailure
// taxonomy and do not poison the resumable agent session.
func classifyFailedOutput(output string) (string, bool) {
	trimmed := strings.TrimSpace(output)
	if trimmed == "" || len(trimmed) > poisonedOutputMaxLen {
		return "", false
	}
	if !strings.HasPrefix(strings.ToLower(trimmed), "error: retriableerror:") {
		return "", false
	}
	return taskfailure.Classify(trimmed).String(), true
}

// classifyPoisonedError reports whether an agent error message indicates
// the LLM API itself rejected the request body — i.e. the conversation
// history contains content the API will not accept (oversized image,
// malformed base64, prompt-too-long, etc.). The conversation cannot be
// resumed: every retry replays the same body and reproduces the same 400.
// The classifier returns FailureReasonAPIInvalidRequest so GetLastTaskSession
// excludes the task from the (agent_id, issue_id) resume lookup, and the
// next task on the issue starts a fresh session instead of permanently
// inheriting the bad state.
//
// Match shape: the Claude Code SDK and similar backends surface upstream
// API failures verbatim, e.g.
//
//	API Error: 400 {"type":"error","error":{"type":"invalid_request_error","message":"Could not process image"},"request_id":"..."}
//
// Matching on both "400" and "invalid_request_error" keeps the classifier
// narrow: 429 rate-limits, 5xx overloads, and tool-shaped errors are
// transient and SHOULD resume on retry.
func classifyPoisonedError(errMsg string) (string, bool) {
	if errMsg == "" {
		return "", false
	}
	lowered := strings.ToLower(errMsg)
	// Both markers must be present: "400" alone is too generic (a tool
	// could surface a 400 from anywhere) and "invalid_request_error"
	// alone could in theory appear in non-poisoning contexts. The
	// combination is the canonical Anthropic error shape and indicates
	// the request body — i.e. the conversation history — is the problem.
	if strings.Contains(lowered, "invalid_request_error") && strings.Contains(lowered, "400") {
		return FailureReasonAPIInvalidRequest, true
	}
	// Pi + OpenAI Responses: tool-history replay includes input[n].status,
	// which the API rejects. Resuming that session replays the same illegal
	// field. Match without requiring the Anthropic 400 JSON envelope.
	if strings.Contains(lowered, "unknown parameter") &&
		strings.Contains(lowered, "input[") &&
		strings.Contains(lowered, ".status") {
		return FailureReasonAPIInvalidRequest, true
	}
	return "", false
}

// classifyResumeUnsafeTimeout reports whether a timeout/no-progress failure
// means the recorded session should not be resumed. Keep this intentionally
// provider-specific: ordinary daemon/backend timeouts are infrastructure-shaped
// and should keep the resume pointer so retries can continue the in-flight
// conversation.
func classifyResumeUnsafeTimeout(provider, errMsg string) (string, bool) {
	if errMsg == "" {
		return "", false
	}
	provider = strings.ToLower(strings.TrimSpace(provider))
	lowered := strings.ToLower(errMsg)
	switch provider {
	case agent.ProviderCodex:
		if strings.Contains(lowered, strings.ToLower(agent.CodexSemanticInactivityMarker)) ||
			strings.Contains(lowered, strings.ToLower(agent.CodexFirstTurnNoProgressMarker)) {
			return FailureReasonCodexSemanticInactivity, true
		}
	case agent.ProviderGrok:
		if strings.Contains(lowered, strings.ToLower(agent.GrokFirstStreamEventTimeoutMarker)) ||
			strings.Contains(lowered, strings.ToLower(agent.GrokNoStreamingJSONEventsMarker)) {
			return FailureReasonGrokFirstTurnNoProgress, true
		}
	}
	return "", false
}

// classifyResumeUnsafeToolFailure recognizes the narrow Grok failure where a
// retained provider session still references an obsolete permission/tool
// schema. Replaying that session deterministically fails before the shell or
// Multica transport command starts, so both the in-process backend and the
// persisted resume pointer must be discarded.
func classifyResumeUnsafeToolFailure(provider, errMsg string) (string, bool) {
	if strings.ToLower(strings.TrimSpace(provider)) != agent.ProviderGrok {
		return "", false
	}
	lowered := strings.ToLower(strings.TrimSpace(errMsg))
	if strings.Contains(lowered, "failed to request permission from user") &&
		strings.Contains(lowered, "unknown permission option for tool") {
		return FailureReasonGrokToolPermission, true
	}
	return "", false
}
