package service

import (
	"encoding/json"
	"testing"
)

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
	for _, profile := range []string{ExecutionProfileAttentionProbe, ExecutionProfileProtocolTurn} {
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
