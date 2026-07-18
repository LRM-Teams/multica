package daemon

import (
	"fmt"
	"strings"

	"github.com/multica-ai/multica/server/pkg/protocol"
)

const (
	executionProfileFull           = "full"
	executionProfileAttentionProbe = "attention_probe"
	executionProfileProtocolTurn   = "protocol_turn"

	restrictedAgentInstructionsRunes = 4096
	restrictedMemoryRunes            = 4096
)

func taskExecutionProfile(task Task) (string, error) {
	profile := ""
	if task.ExecutionConfig != nil {
		profile = strings.TrimSpace(task.ExecutionConfig.ExecutionProfile)
	}
	switch profile {
	case "", executionProfileFull:
		return executionProfileFull, nil
	case executionProfileAttentionProbe, executionProfileProtocolTurn:
		return profile, nil
	default:
		return "", fmt.Errorf("unsupported execution profile %q", profile)
	}
}

func isRestrictedExecutionProfile(profile string) bool {
	return profile == executionProfileAttentionProbe || profile == executionProfileProtocolTurn
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
	task.Repos = nil
	task.ProjectResources = nil
	task.ChatMessageAttachments = nil
	task.PriorSessionID = ""
	task.PriorWorkDir = ""
	task.WorkspaceContext = ""
	task.ProvisionManagedWorkdir = false
	task.ManagedWorkdirRelPath = ""
	task.ArealProxy = nil
	if task.Agent == nil {
		return task
	}
	agentCopy := *task.Agent
	agentCopy.Instructions = truncateRunes(agentCopy.Instructions, restrictedAgentInstructionsRunes)
	agentCopy.Skills = nil
	agentCopy.CustomArgs = nil
	agentCopy.McpConfig = nil
	agentCopy.Memories = compactRestrictedMemories(agentCopy.Memories, restrictedMemoryRunes)
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
		copyMemory.Content = truncateRunes(copyMemory.Content, remaining)
		used := len([]rune(copyMemory.Content))
		if used == 0 {
			continue
		}
		remaining -= used
		compacted = append(compacted, copyMemory)
	}
	return compacted
}

func truncateRunes(value string, limit int) string {
	if limit <= 0 {
		return ""
	}
	runes := []rune(value)
	if len(runes) <= limit {
		return value
	}
	return string(runes[:limit])
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
