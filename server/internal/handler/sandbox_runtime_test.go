package handler

import (
	"encoding/json"
	"testing"

	"github.com/jackc/pgx/v5/pgtype"
	db "github.com/multica-ai/multica/server/pkg/db/generated"
)

func TestMergeRuntimeEnvMetadata(t *testing.T) {
	t.Parallel()
	base := json.RawMessage(`{"name":"sandbox-a"}`)
	out := mergeRuntimeEnvMetadata(base, map[string]string{
		"MULTICA_TOKEN":     "tok",
		"MULTICA_DAEMON_ID": "daemon-1",
		"TEAM_MODEL":        "gpt-5.5",
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
	if env["MULTICA_DAEMON_ID"] != "daemon-1" {
		t.Fatalf("MULTICA_DAEMON_ID = %#v", env["MULTICA_DAEMON_ID"])
	}
}

func TestApplySandboxDisplayNameToRuntimeEnv(t *testing.T) {
	t.Parallel()
	env := map[string]string{"MULTICA_TOKEN": "tok"}
	applySandboxDisplayNameToRuntimeEnv(env, "  my-sandbox  ")
	if env["MULTICA_DAEMON_DEVICE_NAME"] != "my-sandbox" {
		t.Fatalf("MULTICA_DAEMON_DEVICE_NAME = %q", env["MULTICA_DAEMON_DEVICE_NAME"])
	}
	if env["MULTICA_SANDBOX_NAME"] != "my-sandbox" {
		t.Fatalf("MULTICA_SANDBOX_NAME = %q", env["MULTICA_SANDBOX_NAME"])
	}
	applySandboxDisplayNameToRuntimeEnv(env, "   ")
	if env["MULTICA_DAEMON_DEVICE_NAME"] != "my-sandbox" {
		t.Fatalf("empty name should not clear existing device name, got %q", env["MULTICA_DAEMON_DEVICE_NAME"])
	}
}

func TestSyncSandboxDisplayNameInRuntimeEnvMetadata(t *testing.T) {
	t.Parallel()
	base := json.RawMessage(`{"name":"old","runtime_env":{"MULTICA_TOKEN":"tok","MULTICA_DAEMON_DEVICE_NAME":"old"}}`)
	out := syncSandboxDisplayNameInRuntimeEnvMetadata(base, "new-name")
	var meta map[string]any
	if err := json.Unmarshal(out, &meta); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	env, ok := meta["runtime_env"].(map[string]any)
	if !ok {
		t.Fatalf("runtime_env missing: %#v", meta)
	}
	if env["MULTICA_DAEMON_DEVICE_NAME"] != "new-name" || env["MULTICA_SANDBOX_NAME"] != "new-name" {
		t.Fatalf("runtime_env = %#v", env)
	}
	if env["MULTICA_TOKEN"] != "tok" {
		t.Fatalf("token cleared: %#v", env)
	}
}

func TestSandboxDisplayNameFromMetadata(t *testing.T) {
	t.Parallel()
	if got := sandboxDisplayNameFromMetadata(json.RawMessage(`{"name":" sandbox-x "}`)); got != "sandbox-x" {
		t.Fatalf("got %q", got)
	}
	if got := sandboxDisplayNameFromMetadata(json.RawMessage(`{}`)); got != "" {
		t.Fatalf("got %q", got)
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

func TestBuildSandboxDockerContainerNameIncludesContext(t *testing.T) {
	t.Parallel()
	got := buildSandboxDockerContainerName(
		"https://dev.multica.ai:8443/app",
		db.Workspace{Slug: "alpha-team", Name: "Alpha Team"},
		db.User{Name: "jian40", DisplayName: "Jian"},
		"Chrome Box",
	)
	want := "multica-dev-multica-ai-alpha-team-jian40-chrome-box"
	if got != want {
		t.Fatalf("container name = %q, want %q", got, want)
	}
}

func TestBuildSandboxDockerContainerNameFallsBackForNonASCII(t *testing.T) {
	t.Parallel()
	got := buildSandboxDockerContainerName(
		"http://127.0.0.1:8080",
		db.Workspace{Name: "工作区"},
		db.User{DisplayName: "用户", Email: "owner@example.com"},
		"容器",
	)
	want := "multica-127-0-0-1-workspace-owner-example-com-container"
	if got != want {
		t.Fatalf("container name = %q, want %q", got, want)
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
