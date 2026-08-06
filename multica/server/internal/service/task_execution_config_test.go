package service

import (
	"encoding/json"
	"errors"
	"testing"
)

func TestRequireAgentModelRejectsEmpty(t *testing.T) {
	if err := RequireAgentModel(""); !errors.Is(err, ErrAgentModelRequired) {
		t.Fatalf("RequireAgentModel(\"\") = %v, want ErrAgentModelRequired", err)
	}
	if err := RequireAgentModel("   "); !errors.Is(err, ErrAgentModelRequired) {
		t.Fatalf("RequireAgentModel(blank) = %v, want ErrAgentModelRequired", err)
	}
	if err := RequireAgentModel("gpt-5.4"); err != nil {
		t.Fatalf("RequireAgentModel(explicit) = %v", err)
	}
}

func TestTaskExecutionConfigRejectsEmptyModel(t *testing.T) {
	if _, err := WithTaskExecutionConfig(nil, "", ""); !errors.Is(err, ErrAgentModelRequired) {
		t.Fatalf("WithTaskExecutionConfig(empty model) = %v, want ErrAgentModelRequired", err)
	}
}

func TestTaskExecutionConfigPreservesExistingContext(t *testing.T) {
	contextJSON, err := WithTaskExecutionConfig([]byte(`{"squad_id":"squad-1"}`), "queued-model", "high")
	if err != nil {
		t.Fatalf("WithTaskExecutionConfig: %v", err)
	}
	if string(contextJSON) == "" || !containsJSONKey(contextJSON, "squad_id") {
		t.Fatalf("existing task context was not preserved: %s", contextJSON)
	}
	config, ok := TaskExecutionConfigFromContext(contextJSON)
	if !ok {
		t.Fatal("TaskExecutionConfigFromContext returned no snapshot")
	}
	if config.Model != "queued-model" || config.ThinkingLevel != "high" {
		t.Fatalf("config = %#v", config)
	}
	if config.ExecutionProfile != ExecutionProfileFull {
		t.Fatalf("execution profile = %q, want %q", config.ExecutionProfile, ExecutionProfileFull)
	}
}

func TestTaskExecutionConfigLegacyContextFallsBack(t *testing.T) {
	for _, contextJSON := range [][]byte{nil, []byte(`{}`), []byte(`{"execution_config":{"snapshotted":false}}`), []byte(`not-json`)} {
		if _, ok := TaskExecutionConfigFromContext(contextJSON); ok {
			t.Fatalf("legacy context %q unexpectedly produced a snapshot", contextJSON)
		}
	}
}

func TestTaskExecutionConfigHandlesNullContext(t *testing.T) {
	contextJSON, err := WithTaskExecutionConfig([]byte(`null`), "queued-model", "high")
	if err != nil {
		t.Fatalf("WithTaskExecutionConfig(null): %v", err)
	}
	if _, ok := TaskExecutionConfigFromContext(contextJSON); !ok {
		t.Fatalf("null context did not receive a snapshot: %s", contextJSON)
	}
}

func TestTaskExecutionConfigRestrictedProfilesRoundTrip(t *testing.T) {
	for _, profile := range []string{ExecutionProfileProtocolTurn} {
		contextJSON, err := WithTaskExecutionProfile(nil, "queued-model", "low", profile)
		if err != nil {
			t.Fatalf("WithTaskExecutionProfile(%q): %v", profile, err)
		}
		config, ok := TaskExecutionConfigFromContext(contextJSON)
		if !ok {
			t.Fatalf("profile %q did not round trip", profile)
		}
		if config.ExecutionProfile != profile {
			t.Fatalf("execution profile = %q, want %q", config.ExecutionProfile, profile)
		}
		if config.ContextMessages != 8 || config.MemoryBudgetBytes != 4*1024 || config.MaxOutputTokens != 96 {
			t.Fatalf("restricted bounds = %#v", config)
		}
		if config.ToolsEnabled {
			t.Fatal("restricted snapshot enabled tools")
		}
		var contextMap map[string]json.RawMessage
		var wireConfig map[string]json.RawMessage
		if err := json.Unmarshal(contextJSON, &contextMap); err != nil {
			t.Fatalf("unmarshal context: %v", err)
		}
		if err := json.Unmarshal(contextMap[taskExecutionConfigKey], &wireConfig); err != nil {
			t.Fatalf("unmarshal execution config: %v", err)
		}
		if raw, present := wireConfig["tools_enabled"]; !present || string(raw) != "false" {
			t.Fatalf("tools_enabled wire value = %s, present=%v", raw, present)
		}
	}
}

func TestTaskExecutionConfigParsesProtocolTurnRuntimeBounds(t *testing.T) {
	config, ok := TaskExecutionConfigFromContext([]byte(`{"execution_config":{"model":"m","thinking_level":"low","execution_profile":"protocol_turn","context_messages":4,"memory_budget_bytes":2048,"max_output_tokens":48,"tools_enabled":false,"snapshotted":true}}`))
	if !ok {
		t.Fatal("protocol turn runtime config was not parsed")
	}
	if config.ContextMessages != 4 || config.MemoryBudgetBytes != 2048 || config.MaxOutputTokens != 48 || config.ToolsEnabled {
		t.Fatalf("config = %#v", config)
	}
}

func TestTaskExecutionConfigRejectsUnknownProfile(t *testing.T) {
	if _, err := WithTaskExecutionProfile(nil, "queued-model", "low", "surprise_profile"); err == nil {
		t.Fatal("unknown execution profile was accepted")
	}
	config, ok := TaskExecutionConfigFromContext([]byte(`{"execution_config":{"model":"m","thinking_level":"low","execution_profile":"surprise_profile","snapshotted":true}}`))
	if !ok || config.ExecutionProfile != "surprise_profile" {
		t.Fatalf("unknown persisted execution profile was hidden: config=%#v ok=%v", config, ok)
	}
}

func TestTaskExecutionConfigLegacySnapshotDefaultsToFull(t *testing.T) {
	config, ok := TaskExecutionConfigFromContext([]byte(`{"execution_config":{"model":"m","thinking_level":"low","snapshotted":true}}`))
	if !ok {
		t.Fatal("legacy snapshot was rejected")
	}
	if config.ExecutionProfile != ExecutionProfileFull {
		t.Fatalf("execution profile = %q, want %q", config.ExecutionProfile, ExecutionProfileFull)
	}
}

func containsJSONKey(contextJSON []byte, key string) bool {
	var contextMap map[string]json.RawMessage
	return json.Unmarshal(contextJSON, &contextMap) == nil && contextMap[key] != nil
}
