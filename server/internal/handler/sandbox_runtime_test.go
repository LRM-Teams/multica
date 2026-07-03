package handler

import (
	"encoding/json"
	"testing"

	"github.com/jackc/pgx/v5/pgtype"
)

func TestMergeRuntimeEnvMetadata(t *testing.T) {
	t.Parallel()
	base := json.RawMessage(`{"name":"sandbox-a"}`)
	out := mergeRuntimeEnvMetadata(base, map[string]string{
		"MULTICA_TOKEN": "tok",
		"TEAM_MODEL":    "gpt-5.5",
	})
	var meta map[string]any
	if err := json.Unmarshal(out, &meta); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if meta["name"] != "sandbox-a" {
		t.Fatalf("name = %v", meta["name"])
	}
	env, ok := meta["runtime_env"].(map[string]any)
	if !ok || env["MULTICA_TOKEN"] != "tok" {
		t.Fatalf("runtime_env = %#v", meta["runtime_env"])
	}
}

func TestShouldEnqueueSandboxReconfigure(t *testing.T) {
	t.Parallel()
	localRef := pgtype.Text{String: "cube-123", Valid: true}
	if !shouldEnqueueSandboxReconfigure(localRef, json.RawMessage(`{"api_key":"k"}`)) {
		t.Fatal("expected reconfigure when local_ref and runtime present")
	}
	if shouldEnqueueSandboxReconfigure(pgtype.Text{}, json.RawMessage(`{"api_key":"k"}`)) {
		t.Fatal("expected no reconfigure without local_ref")
	}
	if shouldEnqueueSandboxReconfigure(localRef, json.RawMessage(`{}`)) {
		t.Fatal("expected no reconfigure for empty runtime")
	}
}

func TestRuntimeFromMetadata(t *testing.T) {
	t.Parallel()
	raw := json.RawMessage(`{"runtime":{"api_key":"k","model":"m"}}`)
	rt := runtimeFromMetadata(raw)
	if string(rt) != `{"api_key":"k","model":"m"}` {
		t.Fatalf("runtime = %s", string(rt))
	}
}
