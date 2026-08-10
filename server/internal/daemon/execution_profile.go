package daemon

import (
	"encoding/json"
	"fmt"
	"io"
	"strings"
	"unicode/utf8"

	"github.com/multica-ai/multica/server/pkg/protocol"
)

const (
	executionProfileFull           = "full"
	executionProfileProtocolTurn   = "protocol_turn"
	executionProfileAttentionProbe = "attention_probe"

	restrictedAgentInstructionsBytes   = 4 * 1024
	restrictedMemoryBytes              = 4 * 1024
	restrictedMessageBytes             = 4 * 1024
	restrictedContextBytes             = 8 * 1024
	restrictedContextMessages          = 8
	restrictedExecutionMaxOutputTokens = 96
)

func taskExecutionProfile(task Task) (string, error) {
	profile := ""
	if task.ExecutionConfig != nil {
		profile = strings.TrimSpace(task.ExecutionConfig.ExecutionProfile)
	}
	switch profile {
	case "", executionProfileFull:
		return executionProfileFull, nil
	case executionProfileProtocolTurn, executionProfileAttentionProbe:
		if err := validateRestrictedExecutionConfig(task.ExecutionConfig); err != nil {
			return "", fmt.Errorf("execution profile %q: %w", profile, err)
		}
		return profile, nil
	default:
		return "", fmt.Errorf("unsupported execution profile %q", profile)
	}
}

func validateRestrictedExecutionConfig(config *TaskExecutionConfig) error {
	if config == nil {
		return nil
	}
	if config.ToolsEnabled {
		return fmt.Errorf("tools_enabled must be false")
	}
	if config.ContextMessages < 0 || config.ContextMessages > restrictedContextMessages {
		return fmt.Errorf("context_messages must be between 0 and %d", restrictedContextMessages)
	}
	if config.MemoryBudgetBytes < 0 || config.MemoryBudgetBytes > restrictedMemoryBytes {
		return fmt.Errorf("memory_budget_bytes must be between 0 and %d", restrictedMemoryBytes)
	}
	if config.MaxOutputTokens < 0 || config.MaxOutputTokens > restrictedExecutionMaxOutputTokens {
		return fmt.Errorf("max_output_tokens must be between 0 and %d", restrictedExecutionMaxOutputTokens)
	}
	return nil
}

func isRestrictedExecutionProfile(profile string) bool {
	return profile == executionProfileProtocolTurn || profile == executionProfileAttentionProbe
}

func restrictedOutputTokenLimit(profile string) int {
	if isRestrictedExecutionProfile(profile) {
		return restrictedExecutionMaxOutputTokens
	}
	return 0
}

func restrictedOutputTokenLimitForTask(task Task, profile string) int {
	limit := restrictedOutputTokenLimit(profile)
	if limit == 0 || task.ExecutionConfig == nil || task.ExecutionConfig.MaxOutputTokens == 0 {
		return limit
	}
	return task.ExecutionConfig.MaxOutputTokens
}

func restrictedContextLimits(task Task, profile string) (int, int) {
	if !isRestrictedExecutionProfile(profile) {
		return 0, 0
	}
	maxMessages := restrictedContextMessages
	memoryBytes := restrictedMemoryBytes
	if task.ExecutionConfig == nil {
		return maxMessages, memoryBytes
	}
	if task.ExecutionConfig.ContextMessages > 0 {
		maxMessages = task.ExecutionConfig.ContextMessages
	}
	if task.ExecutionConfig.MemoryBudgetBytes > 0 {
		memoryBytes = task.ExecutionConfig.MemoryBudgetBytes
	}
	return maxMessages, memoryBytes
}

func totalTaskOutputTokens(usage []TaskUsageEntry) int64 {
	var total int64
	for _, entry := range usage {
		total += entry.OutputTokens
	}
	return total
}

func validateExecutionProfileProvider(profile, provider string) error {
	if !isRestrictedExecutionProfile(profile) {
		return nil
	}
	if provider != "pi" {
		return fmt.Errorf("execution profile %q requires a provider with enforced tool isolation; provider %q is not supported", profile, provider)
	}
	return nil
}

// restrictTaskForExecutionProfile removes every task surface that would make a
// lightweight judgment behave like a normal coding run. The original Task is
// passed by value; AgentData is copied before mutation so callers can retain the
// claimed payload for audit logging.
func restrictTaskForExecutionProfile(task Task, profile string) Task {
	if !isRestrictedExecutionProfile(profile) {
		return task
	}
	contextMessages, memoryBudgetBytes := restrictedContextLimits(task, profile)
	task.ChatMessageAttachments = nil
	task.PriorSessionID = ""
	task.PriorWorkDir = ""
	task.WorkspaceContext = ""
	task.ArealProxy = nil
	task.ChatContextSummary = boundedRestrictedContext(task.ChatContextSummary, contextMessages, restrictedContextBytes)
	task.ChatMessage = truncateUTF8Bytes(task.ChatMessage, restrictedMessageBytes)
	if task.Agent == nil {
		return task
	}
	agentCopy := *task.Agent
	agentCopy.Instructions = truncateUTF8Bytes(agentCopy.Instructions, restrictedAgentInstructionsBytes)
	agentCopy.Skills = nil
	agentCopy.CustomArgs = nil
	agentCopy.McpConfig = nil
	agentCopy.Memories = compactRestrictedMemories(agentCopy.Memories, memoryBudgetBytes)
	task.Agent = &agentCopy
	return task
}

func compactRestrictedMemories(memories []MemoryData, budget int) []MemoryData {
	if budget <= 0 || len(memories) == 0 {
		return nil
	}
	remaining := budget
	compacted := make([]MemoryData, 0, len(memories))
	for _, memory := range memories {
		if remaining <= 0 {
			break
		}
		copyMemory := memory
		copyMemory.Content = truncateUTF8Bytes(copyMemory.Content, remaining)
		used := len(copyMemory.Content)
		if used == 0 {
			continue
		}
		remaining -= used
		compacted = append(compacted, copyMemory)
	}
	return compacted
}

func truncateUTF8Tail(value string, limit int) string {
	if limit <= 0 {
		return ""
	}
	if len(value) <= limit {
		return value
	}
	value = value[len(value)-limit:]
	for len(value) > 0 && !utf8.ValidString(value) {
		value = value[1:]
	}
	return value
}

func boundedRestrictedContext(value string, maxMessages, maxBytes int) string {
	value = strings.ReplaceAll(value, "\r\n", "\n")
	value = strings.ReplaceAll(value, "\r", "\n")
	lines := strings.Split(value, "\n")
	bounded := make([]string, 0, len(lines))
	for _, line := range lines {
		line = strings.TrimSpace(line)
		if line != "" {
			bounded = append(bounded, line)
		}
	}
	if maxMessages <= 0 || len(bounded) == 0 {
		return ""
	}
	if len(bounded) > maxMessages {
		bounded = bounded[len(bounded)-maxMessages:]
	}
	return truncateUTF8Tail(strings.Join(bounded, "\n"), maxBytes)
}

func restrictedExecutionSystemPrompt(profile string) string {
	switch profile {
	case executionProfileProtocolTurn:
		return "You are running one bounded protocol turn. Tools, shell, files, network access, and public messaging are unavailable. Follow the supplied protocol state and return exactly the requested structured result with no extra prose."
	case executionProfileAttentionProbe:
		return "You are running an internal attention probe for your agent identity. Tools, shell, files, network access, and public messaging are unavailable. Judge only from the supplied bounded context and return exactly one JSON object: {\"decision\":\"SILENT|ANSWER|CONTRIBUTE|COORDINATE\",\"confidence\":<0..1>,\"value_type\":\"...\",\"summary\":\"...\",\"evidence_refs\":[\"...\"],\"seen_up_to_seq\":<int>} with no extra prose."
	default:
		return ""
	}
}

func parseRestrictedExecutionOutput(profile, output string) (json.RawMessage, error) {
	switch profile {
	case executionProfileProtocolTurn:
		return parseProtocolTurnOutput(output)
	case executionProfileAttentionProbe:
		dec, ok, _ := ParseAttentionProbeOutput(output)
		if !ok {
			return nil, fmt.Errorf("attention probe output: %w", errAttentionProbeUnusable)
		}
		return dec.CanonicalJSON()
	default:
		return nil, fmt.Errorf("execution profile %q has no restricted output contract", profile)
	}
}

func parseProtocolTurnOutput(output string) (json.RawMessage, error) {
	var parsed map[string]json.RawMessage
	if _, err := decodeStrictJSONObject(output, &parsed); err != nil {
		return nil, fmt.Errorf("protocol turn output: %w", err)
	}
	if len(parsed) == 0 {
		return nil, fmt.Errorf("protocol turn output: JSON object must not be empty")
	}
	canonical, err := json.Marshal(parsed)
	if err != nil {
		return nil, fmt.Errorf("protocol turn output: marshal canonical result: %w", err)
	}
	return canonical, nil
}

// bindRestrictedOutputMetadata is a hook for restricted execution profiles
// that need to stamp server-known metadata onto their structured output
// before persistence. No current profile requires this; protocol_turn output
// passes through unchanged.
func bindRestrictedOutputMetadata(profile string, output json.RawMessage, configuredModel string, usage []TaskUsageEntry, task Task) (json.RawMessage, error) {
	return output, nil
}

func decodeStrictJSONObject(output string, destination any) (map[string]json.RawMessage, error) {
	trimmed := strings.TrimSpace(output)
	if trimmed == "" {
		return nil, fmt.Errorf("empty output")
	}
	decoder := json.NewDecoder(strings.NewReader(trimmed))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(destination); err != nil {
		return nil, fmt.Errorf("invalid JSON object: %w", err)
	}
	if err := decoder.Decode(&struct{}{}); err != io.EOF {
		if err == nil {
			return nil, fmt.Errorf("multiple JSON values are not allowed")
		}
		return nil, fmt.Errorf("trailing content is not allowed: %w", err)
	}
	var rawObject map[string]json.RawMessage
	if err := json.Unmarshal([]byte(trimmed), &rawObject); err != nil || rawObject == nil {
		return nil, fmt.Errorf("top-level value must be a JSON object")
	}
	return rawObject, nil
}

func suppressPublicOutputForTask(task Task) bool {
	if task.ExecutionConfig == nil {
		return false
	}
	profile := strings.TrimSpace(task.ExecutionConfig.ExecutionProfile)
	return profile != "" && profile != executionProfileFull
}

// restrictResultForExecutionProfile prevents a sidecar cognition run from
// mutating the primary chat session or becoming a user-visible completion.
// A later protocol layer persists the structured turn output through its own
// internal result contract before this terminal callback is sent.
func restrictResultForExecutionProfile(result TaskResult, profile string) TaskResult {
	if !isRestrictedExecutionProfile(profile) {
		return result
	}
	result.SessionID = ""
	result.WorkDir = ""
	result.OutputSuppressedReason = protocol.ChannelOutputSuppressedReasonRestrictedExecutionProfile
	if result.Status != "completed" {
		return result
	}
	result.Comment = ""
	result.BranchName = ""
	result.Action = protocol.ChatOutputActionNoReply
	result.Target = ""
	result.Type = protocol.ChatOutputKindNoReply
	result.Parts = nil
	result.Reaction = nil
	return result
}
