package main

import (
	"encoding/json"
	"strings"
	"testing"
)

func TestBuildStartRuntimeInCubeCodeResetsFrozenDaemonIdentity(t *testing.T) {
	code := buildStartRuntimeInCubeCode(map[string]string{
		"MULTICA_TOKEN":     "tok",
		"MULTICA_DAEMON_ID": "aaaaaaaa-bbbb-cccc-dddd-eeeeeeeeeeee",
	})
	for _, want := range []string{
		"pkill -f 'multica daemon'",
		`daemon_file.write_text(daemon_id + "\n")`,
		`profiles.glob("*/daemon.id")`,
		"aaaaaaaa-bbbb-cccc-dddd-eeeeeeeeeeee",
		"/usr/local/bin/start-multica-runtime.sh",
	} {
		if !strings.Contains(code, want) {
			t.Fatalf("start runtime code missing %q\n%s", want, code)
		}
	}
}

func TestMergeRuntimeEnvMultiProvider(t *testing.T) {
	runtime := json.RawMessage(`{
		"providers": [
			{"provider":"openai","api_key":"sk-o","base_url":"https://o.example/v1","model":"gpt-5.5"},
			{"provider":"anthropic","api_key":"sk-a","base_url":"https://a.example","model":"claude-sonnet"}
		],
		"default_provider":"anthropic",
		"default_model":"claude-sonnet",
		"api_key":"sk-a",
		"model":"claude-sonnet"
	}`)
	out := mergeRuntimeEnv(map[string]string{"MULTICA_TOKEN": "tok"}, runtime)
	if out["TEAM_PROVIDER"] != "anthropic" {
		t.Fatalf("TEAM_PROVIDER = %q", out["TEAM_PROVIDER"])
	}
	if out["TEAM_API_KEY"] != "sk-a" {
		t.Fatalf("TEAM_API_KEY = %q", out["TEAM_API_KEY"])
	}
	if out["TEAM_MODEL"] != "claude-sonnet" {
		t.Fatalf("TEAM_MODEL = %q", out["TEAM_MODEL"])
	}
	if out["TEAM_PI_CONFIG"] == "" {
		t.Fatal("expected TEAM_PI_CONFIG")
	}
	var cfg teamPiConfig
	if err := json.Unmarshal([]byte(out["TEAM_PI_CONFIG"]), &cfg); err != nil {
		t.Fatalf("TEAM_PI_CONFIG: %v", err)
	}
	if len(cfg.Providers) != 2 {
		t.Fatalf("providers = %#v", cfg.Providers)
	}
	if cfg.DefaultProvider != "anthropic" || cfg.DefaultModel != "claude-sonnet" {
		t.Fatalf("defaults = %#v", cfg)
	}
	if !hasRuntimeModelConfig(runtime) {
		t.Fatal("expected hasRuntimeModelConfig")
	}
}

func TestMergeRuntimeEnvLegacyFlat(t *testing.T) {
	runtime := json.RawMessage(`{"api_key":"sk","base_url":"https://x/v1","model":"gpt-5.5"}`)
	out := mergeRuntimeEnv(nil, runtime)
	if out["TEAM_API_KEY"] != "sk" || out["TEAM_BASE_URL"] != "https://x/v1" || out["TEAM_MODEL"] != "gpt-5.5" {
		t.Fatalf("legacy merge = %#v", out)
	}
	if out["TEAM_PROVIDER"] != "openai" {
		t.Fatalf("default provider = %q", out["TEAM_PROVIDER"])
	}
	if out["TEAM_PI_CONFIG"] == "" {
		t.Fatal("expected TEAM_PI_CONFIG from legacy")
	}
}
