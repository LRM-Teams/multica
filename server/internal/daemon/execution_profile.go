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
	executionProfileAttentionProbe = "attention_probe"
	executionProfileProtocolTurn   = "protocol_turn"

	restrictedAgentInstructionsBytes   = 4 * 1024
	restrictedMemoryBytes              = 4 * 1024
	restrictedMessageBytes             = 4 * 1024
	restrictedContextBytes             = 8 * 1024
	restrictedContextMessages          = 8
	restrictedExecutionMaxOutputTokens = 96
)

var attentionProbeOutputKeys = []string{
	"decision",
	"confidence",
	"value_type",
	"summary",
	"evidence_refs",
	"model_version",
	"seen_up_to_seq",
}

type attentionProbeOutput struct {
	Decision     string   `json:"decision"`
	Confidence   float64  `json:"confidence"`
	ValueType    string   `json:"value_type"`
	Summary      string   `json:"summary"`
	EvidenceRefs []string `json:"evidence_refs"`
	ModelVersion string   `json:"model_version"`
	SeenUpToSeq  int64    `json:"seen_up_to_seq"`
}

func taskExecutionProfile(task Task) (string, error) {
	profile := ""
	if task.ExecutionConfig != nil {
		profile = strings.TrimSpace(task.ExecutionConfig.ExecutionProfile)
	}
	switch profile {
	case "", executionProfileFull:
		return executionProfileFull, nil
	case executionProfileAttentionProbe, executionProfileProtocolTurn:
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
	return profile == executionProfileAttentionProbe || profile == executionProfileProtocolTurn
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
	task.Repos = nil
	task.ProjectResources = nil
	task.ChatMessageAttachments = nil
	task.PriorSessionID = ""
	task.PriorWorkDir = ""
	task.WorkspaceContext = ""
	task.ProvisionManagedWorkdir = false
	task.ManagedWorkdirRelPath = ""
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

func restrictedMemorySummary(memories []MemoryData, budget int) string {
	if budget <= 0 || len(memories) == 0 {
		return ""
	}
	var b strings.Builder
	for _, memory := range memories {
		content := strings.TrimSpace(memory.Content)
		if content == "" || b.Len() >= budget {
			continue
		}
		prefix := "- "
		if name := strings.TrimSpace(memory.Name); name != "" {
			prefix += name + ": "
		}
		remaining := budget - b.Len()
		if len(prefix) >= remaining {
			break
		}
		if b.Len() > 0 {
			b.WriteByte('\n')
			remaining--
		}
		b.WriteString(prefix)
		remaining -= len(prefix)
		b.WriteString(truncateUTF8Bytes(content, remaining))
	}
	return truncateUTF8Bytes(b.String(), budget)
}

func restrictedExecutionSystemPrompt(profile string) string {
	switch profile {
	case executionProfileAttentionProbe:
		return "You are running an internal attention probe for your agent identity. Tools, shell, files, network access, and public messaging are unavailable. Judge only from the supplied bounded context. Return exactly one JSON object and no prose."
	case executionProfileProtocolTurn:
		return "You are running one bounded protocol turn. Tools, shell, files, network access, and public messaging are unavailable. Follow the supplied protocol state and return exactly the requested structured result with no extra prose."
	default:
		return ""
	}
}

func parseRestrictedExecutionOutput(profile, output string) (json.RawMessage, error) {
	switch profile {
	case executionProfileAttentionProbe:
		return parseAttentionProbeOutput(output)
	case executionProfileProtocolTurn:
		return parseProtocolTurnOutput(output)
	default:
		return nil, fmt.Errorf("execution profile %q has no restricted output contract", profile)
	}
}

func parseAttentionProbeOutput(output string) (json.RawMessage, error) {
	var parsed attentionProbeOutput
	rawObject, err := decodeStrictJSONObject(output, &parsed)
	if err != nil {
		return nil, fmt.Errorf("attention probe output: %w", err)
	}
	for _, key := range attentionProbeOutputKeys {
		rawValue, ok := rawObject[key]
		if !ok {
			return nil, fmt.Errorf("attention probe output: missing required field %q", key)
		}
		if strings.TrimSpace(string(rawValue)) == "null" {
			return nil, fmt.Errorf("attention probe output: field %q must not be null", key)
		}
	}
	if !stringInSet(parsed.Decision, "SILENT", "ANSWER", "CONTRIBUTE", "COORDINATE") {
		return nil, fmt.Errorf("attention probe output: invalid decision %q", parsed.Decision)
	}
	if parsed.Confidence < 0 || parsed.Confidence > 1 {
		return nil, fmt.Errorf("attention probe output: confidence %v is outside [0,1]", parsed.Confidence)
	}
	if !stringInSet(parsed.ValueType, "none", "direct_answer", "unique_evidence", "correction", "task_claim", "needs_protocol") {
		return nil, fmt.Errorf("attention probe output: invalid value_type %q", parsed.ValueType)
	}
	if parsed.EvidenceRefs == nil {
		return nil, fmt.Errorf("attention probe output: evidence_refs must be an array")
	}
	var untypedEvidence []any
	if err := json.Unmarshal(rawObject["evidence_refs"], &untypedEvidence); err != nil {
		return nil, fmt.Errorf("attention probe output: evidence_refs must be an array of strings")
	}
	for _, ref := range untypedEvidence {
		if _, ok := ref.(string); !ok {
			return nil, fmt.Errorf("attention probe output: evidence_refs must contain only strings")
		}
	}
	if strings.TrimSpace(parsed.ModelVersion) == "" {
		return nil, fmt.Errorf("attention probe output: model_version must not be empty")
	}
	if parsed.SeenUpToSeq < 0 {
		return nil, fmt.Errorf("attention probe output: seen_up_to_seq must be non-negative")
	}
	canonical, err := json.Marshal(parsed)
	if err != nil {
		return nil, fmt.Errorf("attention probe output: marshal canonical result: %w", err)
	}
	return canonical, nil
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

func bindRestrictedOutputMetadata(profile string, output json.RawMessage, configuredModel string, usage []TaskUsageEntry, task Task) (json.RawMessage, error) {
	if profile != executionProfileAttentionProbe {
		return output, nil
	}
	var parsed attentionProbeOutput
	if err := json.Unmarshal(output, &parsed); err != nil {
		return nil, fmt.Errorf("bind attention probe metadata: %w", err)
	}
	for _, entry := range usage {
		if model := strings.TrimSpace(entry.Model); model != "" {
			parsed.ModelVersion = model
			break
		}
	}
	if model := strings.TrimSpace(configuredModel); parsed.ModelVersion == "" && model != "" {
		parsed.ModelVersion = model
	}
	if task.InboxEvent != nil {
		parsed.SeenUpToSeq = task.InboxEvent.SeqTo
	}
	canonical, err := json.Marshal(parsed)
	if err != nil {
		return nil, fmt.Errorf("bind attention probe metadata: %w", err)
	}
	return canonical, nil
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

func stringInSet(value string, allowed ...string) bool {
	for _, candidate := range allowed {
		if value == candidate {
			return true
		}
	}
	return false
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
// A later attention-round layer persists the structured probe output through
// its own internal result contract before this terminal callback is sent.
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
	result.Options = nil
	result.Type = protocol.ChatOutputKindNoReply
	result.Parts = nil
	result.Reaction = nil
	return result
}
