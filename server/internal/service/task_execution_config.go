package service

import (
	"encoding/json"
	"errors"
	"fmt"
	"strings"
)

const taskExecutionConfigKey = "execution_config"

const (
	ExecutionProfileFull         = "full"
	ExecutionProfileProtocolTurn = "protocol_turn"
)

// ErrAgentModelRequired is returned when work would snapshot an empty agent
// model. Empty models previously fell through to a broken provider launch on
// some runtimes (looking like mute); callers must set an explicit model rather
// than silently substituting a provider-specific default such as "auto".
var ErrAgentModelRequired = errors.New("agent model is required")

// RequireAgentModel rejects blank model identifiers.
func RequireAgentModel(model string) error {
	if strings.TrimSpace(model) == "" {
		return ErrAgentModelRequired
	}
	return nil
}

// TaskExecutionConfig is the task-scoped runtime configuration. New work
// snapshots it at enqueue time so later edits to an agent affect only work
// created after the edit.
type TaskExecutionConfig struct {
	Model             string `json:"model"`
	ThinkingLevel     string `json:"thinking_level"`
	ExecutionProfile  string `json:"execution_profile"`
	ContextMessages   int    `json:"context_messages,omitempty"`
	MemoryBudgetBytes int    `json:"memory_budget_bytes,omitempty"`
	MaxOutputTokens   int    `json:"max_output_tokens,omitempty"`
	ToolsEnabled      bool   `json:"tools_enabled"`
	Snapshotted       bool   `json:"snapshotted"`
}

// WithTaskExecutionConfig preserves every existing task context key while
// adding the immutable runtime configuration used by the daemon claim path.
func WithTaskExecutionConfig(contextJSON []byte, model, thinkingLevel string) ([]byte, error) {
	return WithTaskExecutionProfile(contextJSON, model, thinkingLevel, ExecutionProfileFull)
}

// WithTaskExecutionProfile snapshots both the model settings and the runtime
// isolation contract. Restricted profiles are consumed by the daemon and must
// never silently degrade to a full tool-capable run.
func WithTaskExecutionProfile(contextJSON []byte, model, thinkingLevel, executionProfile string) ([]byte, error) {
	if err := RequireAgentModel(model); err != nil {
		return nil, err
	}
	contextMap := map[string]json.RawMessage{}
	if len(contextJSON) > 0 {
		if err := json.Unmarshal(contextJSON, &contextMap); err != nil {
			return nil, err
		}
	}
	if contextMap == nil {
		contextMap = map[string]json.RawMessage{}
	}
	profile, ok := NormalizeExecutionProfile(executionProfile)
	if !ok {
		return nil, fmt.Errorf("unsupported execution profile %q", executionProfile)
	}
	configSnapshot := TaskExecutionConfig{
		Model:            strings.TrimSpace(model),
		ThinkingLevel:    thinkingLevel,
		ExecutionProfile: profile,
		Snapshotted:      true,
	}
	if profile == ExecutionProfileProtocolTurn {
		configSnapshot.ContextMessages = 8
		configSnapshot.MemoryBudgetBytes = 4 * 1024
		configSnapshot.MaxOutputTokens = 96
		configSnapshot.ToolsEnabled = false
	}
	config, err := json.Marshal(configSnapshot)
	if err != nil {
		return nil, err
	}
	contextMap[taskExecutionConfigKey] = config
	return json.Marshal(contextMap)
}

// TaskExecutionConfigFromContext returns false for historical task rows that
// predate task-scoped configuration snapshots.
func TaskExecutionConfigFromContext(contextJSON []byte) (TaskExecutionConfig, bool) {
	var contextMap map[string]json.RawMessage
	if len(contextJSON) == 0 || json.Unmarshal(contextJSON, &contextMap) != nil {
		return TaskExecutionConfig{}, false
	}
	raw, ok := contextMap[taskExecutionConfigKey]
	var config TaskExecutionConfig
	if !ok || json.Unmarshal(raw, &config) != nil || !config.Snapshotted {
		return TaskExecutionConfig{}, false
	}
	profile, valid := NormalizeExecutionProfile(config.ExecutionProfile)
	if valid {
		config.ExecutionProfile = profile
	}
	// Preserve an unknown persisted value so the daemon can reject the run.
	// Treating it as "no snapshot" here would silently fall back to a full
	// execution, which is the unsafe direction for an isolation contract.
	return config, true
}

// NormalizeExecutionProfile treats an omitted profile as the historical full
// execution contract while rejecting unknown future values fail-closed.
func NormalizeExecutionProfile(profile string) (string, bool) {
	switch profile {
	case "", ExecutionProfileFull:
		return ExecutionProfileFull, true
	case ExecutionProfileProtocolTurn:
		return profile, true
	default:
		return "", false
	}
}
