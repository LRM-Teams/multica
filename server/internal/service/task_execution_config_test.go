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

func containsJSONKey(contextJSON []byte, key string) bool {
	var contextMap map[string]json.RawMessage
	return json.Unmarshal(contextJSON, &contextMap) == nil && contextMap[key] != nil
}
