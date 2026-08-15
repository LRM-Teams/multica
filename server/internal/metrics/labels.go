package metrics

import (
	"regexp"
	"strings"

	"github.com/multica-ai/multica/server/pkg/taskfailure"
)

const (
	labelSource         = "source"
	labelRuntimeMode    = "runtime_mode"
	labelProvider       = "provider"
	labelTerminalStatus = "terminal_status"
	labelFailureReason  = "failure_reason"
	labelTokenType      = "token_type"
	labelModel          = "model"
	labelModelAlias     = "model_alias"

	// PR3 labels (funnel / community / commercial).
	labelSignupSource = "signup_source"
	labelPlatform     = "platform"
	labelPath         = "path"
	labelCadence      = "cadence"
	labelTriggerKind  = "trigger_kind"
	labelReason       = "reason"
	labelRecoverable  = "recoverable"
	labelKind         = "kind"
	labelStatus       = "status"
	labelEventKind    = "event_kind"
	labelAction       = "action"
	labelResult       = "result"
	labelOp           = "op"
	labelOutcome      = "outcome"
	labelDecision     = "decision"
)

var businessMetricLabels = map[string][]string{
	"multica_agent_task_enqueued_total":                  {labelSource, labelRuntimeMode},
	"multica_agent_task_dispatched_total":                {labelSource, labelRuntimeMode},
	"multica_agent_task_started_total":                   {labelSource, labelRuntimeMode, labelProvider},
	"multica_agent_task_terminal_total":                  {labelSource, labelRuntimeMode, labelTerminalStatus},
	"multica_agent_task_failed_total":                    {labelSource, labelRuntimeMode, labelFailureReason},
	"multica_agent_task_queue_wait_seconds":              {labelSource, labelRuntimeMode},
	"multica_agent_task_run_seconds":                     {labelSource, labelRuntimeMode, labelTerminalStatus},
	"multica_agent_task_total_seconds":                   {labelSource, labelRuntimeMode, labelTerminalStatus},
	"multica_agent_task_in_progress":                     {labelSource, labelRuntimeMode},
	"multica_agent_task_iteration_count":                 {labelSource, labelTerminalStatus},
	"multica_llm_tokens_total":                           {labelProvider, labelModel, labelTokenType, labelRuntimeMode, labelSource},
	"multica_llm_cost_usd_total":                         {labelProvider, labelModel, labelTokenType, labelRuntimeMode, labelSource},
	"multica_llm_unpriced_tokens_total":                  {labelProvider, labelModelAlias, labelTokenType},
	"multica_llm_request_total":                          {labelProvider, labelModel, labelRuntimeMode},
	"multica_task_queued_expired_total":                  {labelSource, labelRuntimeMode},
	"multica_task_lease_expired_total":                   {labelSource},
	"multica_channel_ambient_gate_decisions_total":       {labelAction, labelReason},
	"multica_channel_output_suppressed_total":            {labelReason},
	"multica_channel_full_execution_wakes_total":         {labelReason},
	"multica_channel_full_execution_amplification_ratio": {},
	"multica_channel_trigger_depth":                      {},
	"multica_freshness_hold_resolution_seconds":          {labelOutcome},
	"multica_agent_delete_duration_seconds":              {labelResult},
	"multica_graph_memory_recall_total":                  {labelResult},
	"multica_graph_memory_explore_rounds":                {},
	"multica_graph_memory_judge_total":                   {labelResult},
	"multica_graph_memory_ingest_total":                  {labelResult},
	"multica_graph_memory_version_switch_total":          {},
	"multica_graph_memory_backtest_bypass_ratio":         {},

	// PR3 funnel / community / commercial.
	"multica_signup_total":                             {labelSignupSource},
	"multica_workspace_created_total":                  {labelSource},
	"multica_team_invite_sent_total":                   {},
	"multica_team_invite_accepted_total":               {},
	"multica_onboarding_started_total":                 {labelPlatform},
	"multica_onboarding_questionnaire_submitted_total": {},
	"multica_onboarding_completed_total":               {labelPath},
	"multica_cloud_waitlist_joined_total":              {},
	"multica_issue_created_total":                      {labelSource, labelPlatform},
	"multica_chat_message_sent_total":                  {labelPlatform},
	"multica_agent_created_total":                      {labelRuntimeMode, labelSource},
	"multica_autopilot_created_total":                  {labelCadence},
	"multica_issue_executed_total":                     {labelSource},
	"multica_runtime_registered_total":                 {labelRuntimeMode, labelProvider},
	"multica_runtime_ready_total":                      {labelRuntimeMode, labelProvider},
	"multica_runtime_ready_seconds":                    {labelRuntimeMode, labelProvider},
	"multica_runtime_failed_total":                     {labelRuntimeMode, labelProvider, labelFailureReason, labelRecoverable},
	"multica_runtime_offline_total":                    {labelRuntimeMode, labelProvider},
	"multica_daemon_ws_message_received_total":         {labelKind},
	"multica_autopilot_run_started_total":              {labelCadence, labelTriggerKind},
	"multica_autopilot_run_terminal_total":             {labelCadence, labelTriggerKind, labelTerminalStatus},
	"multica_autopilot_run_skipped_total":              {labelCadence, labelReason},
	"multica_webhook_delivery_total":                   {labelProvider, labelStatus},
	"multica_github_event_received_total":              {labelEventKind, labelAction},
	"multica_github_pr_review_total":                   {labelResult},
	"multica_cloudruntime_request_total":               {labelOp, labelStatus},
	"multica_cloudruntime_request_duration_seconds":    {labelOp},
	"multica_feedback_submitted_total":                 {labelKind, labelPlatform},
	"multica_contact_sales_submitted_total":            {labelSource},
}

var forbiddenMetricLabels = map[string]struct{}{
	"workspace_id": {},
	"user_id":      {},
	"agent_id":     {},
	"task_id":      {},
	"issue_id":     {},
	"runtime_id":   {},
	"session_id":   {},
	"ip":           {},
}

var (
	knownSources = map[string]string{
		"issue":           "issue",
		"chat":            "chat",
		"autopilot":       "autopilot",
		"autopilot_issue": "autopilot_issue",
		"quick_create":    "quick_create",
		"manual":          "manual",
		"api":             "api",
		"other":           "other",
	}
	knownRuntimeModes = map[string]string{
		"local":   "local",
		"cloud":   "cloud",
		"unknown": "unknown",
	}
	knownRuntimeProviders = map[string]string{
		"antigravity":   "antigravity",
		"claude":        "claude",
		"codebuddy":     "codebuddy",
		"codex":         "codex",
		"copilot":       "copilot",
		"cursor":        "cursor",
		"gemini":        "gemini",
		"grok":          "grok",
		"hermes":        "hermes",
		"kiro":          "kiro",
		"kimi":          "kimi",
		"multica_agent": "multica_agent",
		"openclaw":      "openclaw",
		"opencode":      "opencode",
		"pi":            "pi",
		"other":         "other",
	}
	knownTerminalStatuses = map[string]string{
		"completed": "completed",
		"failed":    "failed",
		"cancelled": "cancelled",
		"blocked":   "blocked",
		"other":     "other",
	}
	knownTokenTypes = map[string]string{
		"input":       "input",
		"output":      "output",
		"cache_read":  "cache_read",
		"cache_write": "cache_write",
	}
	knownAmbientGateActions = map[string]string{
		"enqueued":          "enqueued",
		"coalesced":         "coalesced",
		"dropped":           "dropped",
		"fused":             "fused",
		"relevance_skipped": "relevance_skipped",
		"other":             "other",
	}
	knownAmbientGateReasons = map[string]string{
		"accepted":             "accepted",
		"gate_off":             "gate_off",
		"gate_error":           "gate_error",
		"non_text_noise":       "non_text_noise",
		"non_action":           "non_action",
		"agent_active_ambient": "agent_active_ambient",
		"agent_window_cap":     "agent_window_cap",
		"channel_window_cap":   "channel_window_cap",
		"runtime_window_cap":   "runtime_window_cap",
		"other":                "other",
	}
	knownChannelOutputSuppressedReasons = map[string]string{
		"daemon_outdated":        "daemon_outdated",
		"legacy_protocol_output": "legacy_protocol_output",
		"tool_transport_output":  "tool_transport_output",
		"invalid_output":         "invalid_output",
		"invalid_action":         "invalid_action",
		"invalid_type":           "invalid_type",
		"empty_message":          "empty_message",
		"invalid_reaction":       "invalid_reaction",
		"message_missing_action": "message_missing_action",
		"invalid_target":         "invalid_target",
		"unsent_final_output":    "unsent_final_output",
		"other":                  "other",
	}
	knownFullExecutionWakeReasons = map[string]string{
		"explicit_mention": "explicit_mention",
		"group_command":    "group_command",
		"thread_reply":     "thread_reply",
		"dm":               "dm",
		"legacy_full":      "legacy_full",
		"other":            "other",
	}
	knownFailureReasons = map[string]string{}
	modelAliasUnsafeRe  = regexp.MustCompile(`[^a-z0-9._:/+-]+`)
)

func init() {
	for _, reason := range taskfailure.AllReasons() {
		knownFailureReasons[reason.String()] = reason.String()
	}
}

func validateBusinessMetricLabels() {
	for metric, labels := range businessMetricLabels {
		for _, label := range labels {
			if _, forbidden := forbiddenMetricLabels[label]; forbidden {
				panic("forbidden high-cardinality label " + label + " on " + metric)
			}
		}
	}
}

func metricLabels(metric string) []string {
	labels, ok := businessMetricLabels[metric]
	if !ok {
		panic("missing business metric label definition for " + metric)
	}
	return labels
}

func NormalizeTaskSource(value string) string {
	value = strings.ToLower(strings.TrimSpace(value))
	if normalized, ok := knownSources[value]; ok {
		return normalized
	}
	return "other"
}

func NormalizeRuntimeMode(value string) string {
	value = strings.ToLower(strings.TrimSpace(value))
	if normalized, ok := knownRuntimeModes[value]; ok {
		return normalized
	}
	return "unknown"
}

func NormalizeRuntimeProvider(value string) string {
	value = strings.ToLower(strings.TrimSpace(value))
	if normalized, ok := knownRuntimeProviders[value]; ok {
		return normalized
	}
	return "other"
}

func NormalizeTerminalStatus(value string) string {
	value = strings.ToLower(strings.TrimSpace(value))
	if normalized, ok := knownTerminalStatuses[value]; ok {
		return normalized
	}
	return "other"
}

func NormalizeFailureReason(value string) string {
	value = strings.TrimSpace(value)
	if normalized, ok := knownFailureReasons[value]; ok {
		return normalized
	}
	return taskfailure.Classify(value).String()
}

func NormalizeTokenType(value string) string {
	value = strings.ToLower(strings.TrimSpace(value))
	if normalized, ok := knownTokenTypes[value]; ok {
		return normalized
	}
	return "input"
}

func NormalizeAmbientGateAction(value string) string {
	value = strings.ToLower(strings.TrimSpace(value))
	if normalized, ok := knownAmbientGateActions[value]; ok {
		return normalized
	}
	return "other"
}

func NormalizeAmbientGateReason(value string) string {
	value = strings.ToLower(strings.TrimSpace(value))
	if normalized, ok := knownAmbientGateReasons[value]; ok {
		return normalized
	}
	return "other"
}

func NormalizeChannelOutputSuppressedReason(value string) string {
	value = strings.ToLower(strings.TrimSpace(value))
	if normalized, ok := knownChannelOutputSuppressedReasons[value]; ok {
		return normalized
	}
	return "other"
}

func NormalizeFullExecutionWakeReason(value string) string {
	return normalizeKnownLabel(value, knownFullExecutionWakeReasons, "other")
}

func normalizeKnownLabel(value string, known map[string]string, fallback string) string {
	value = strings.ToLower(strings.TrimSpace(value))
	if normalized, ok := known[value]; ok {
		return normalized
	}
	return fallback
}

func NormalizeModelAlias(value string) string {
	value = strings.ToLower(strings.TrimSpace(value))
	if value == "" {
		return "unknown"
	}
	value = modelAliasUnsafeRe.ReplaceAllString(value, "_")
	if len(value) > 128 {
		return value[:128]
	}
	return value
}
