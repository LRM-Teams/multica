package service

import (
	"encoding/json"
)

const taskExecutionConfigKey = "execution_config"

// TaskExecutionConfig is the task-scoped runtime configuration. New work
// snapshots it at enqueue time so later edits to an agent affect only work
// created after the edit.
type TaskExecutionConfig struct {
	Model         string `json:"model"`
	ThinkingLevel string `json:"thinking_level"`
	Snapshotted   bool   `json:"snapshotted"`
}

// WithTaskExecutionConfig preserves every existing task context key while
// adding the immutable runtime configuration used by the daemon claim path.
func WithTaskExecutionConfig(contextJSON []byte, model, thinkingLevel string) ([]byte, error) {
	contextMap := map[string]json.RawMessage{}
	if len(contextJSON) > 0 {
		if err := json.Unmarshal(contextJSON, &contextMap); err != nil {
			return nil, err
		}
	}
	if contextMap == nil {
		contextMap = map[string]json.RawMessage{}
	}
	config, err := json.Marshal(TaskExecutionConfig{
		Model:         model,
		ThinkingLevel: thinkingLevel,
		Snapshotted:   true,
	})
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
	return config, true
}
