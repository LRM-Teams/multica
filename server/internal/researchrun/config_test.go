package researchrun

import (
	"encoding/json"
	"errors"
	"testing"
)

func TestResolveRunConfigAppliesStrictValidatedPatch(t *testing.T) {
	base := DefaultRunConfig("standard")
	got, err := resolveRunConfig(base, json.RawMessage(`{"max_parallel_tasks":7,"max_run_seconds":3600}`))
	if err != nil {
		t.Fatal(err)
	}
	if got.MaxParallelTasks != 7 || got.MaxRunSeconds != 3600 || got.MaxTasks != base.MaxTasks {
		t.Fatalf("resolved config=%+v", got)
	}
	for _, raw := range []json.RawMessage{
		json.RawMessage(`{"unknown":1}`),
		json.RawMessage(`{"max_parallel_tasks":0}`),
		json.RawMessage(`{"max_result_bytes":1024}`),
	} {
		if _, err = resolveRunConfig(base, raw); !errors.Is(err, ErrInvalidContract) {
			t.Fatalf("patch %s error=%v", raw, err)
		}
	}
}
