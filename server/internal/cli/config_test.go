package cli

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// TestCLIConfig_BackwardCompat_OldFileLoadsWithNilBackends verifies that a
// config.json written by an older daemon (no `backends` key at all) loads
// correctly into the new schema, with Backends == nil. This is the most
// important guarantee of issue #3875's PR: existing on-disk configs MUST
// continue to work byte-for-byte.
func TestCLIConfig_BackwardCompat_OldFileLoadsWithNilBackends(t *testing.T) {
	tmp := t.TempDir()
	t.Setenv("HOME", tmp)

	// Write a 4-field config exactly as the historical daemon would have.
	cfgDir := filepath.Join(tmp, ".multica")
	if err := os.MkdirAll(cfgDir, 0o755); err != nil {
		t.Fatal(err)
	}
	historical := `{
  "server_url": "https://api.multica.ai",
  "app_url": "https://app.multica.ai",
  "workspace_id": "ws-123",
  "token": "mul_abcdef"
}`
	if err := os.WriteFile(filepath.Join(cfgDir, "config.json"), []byte(historical), 0o600); err != nil {
		t.Fatal(err)
	}

	cfg, err := LoadCLIConfig()
	if err != nil {
		t.Fatalf("LoadCLIConfig on historical file: %v", err)
	}

	if cfg.ServerURL != "https://api.multica.ai" {
		t.Errorf("ServerURL: got %q, want historical value", cfg.ServerURL)
	}
	if cfg.Token != "mul_abcdef" {
		t.Errorf("Token: got %q, want historical value", cfg.Token)
	}
	if cfg.Backends != nil {
		t.Errorf("Backends should be nil for historical config, got %+v", cfg.Backends)
	}
}

// TestCLIConfig_BackwardCompat_NilBackendsOmittedFromJSON verifies that
// saving a config without backend overrides does NOT add a `backends` key
// to the on-disk JSON. This matters for users who never set overrides —
// their config files must stay byte-identical, so a future downgrade to
// an older daemon doesn't trip on an empty `backends: null` line.
func TestCLIConfig_BackwardCompat_NilBackendsOmittedFromJSON(t *testing.T) {
	tmp := t.TempDir()
	t.Setenv("HOME", tmp)

	cfg := CLIConfig{
		ServerURL: "https://api.multica.ai",
		Token:     "mul_xyz",
	}
	if err := SaveCLIConfig(cfg); err != nil {
		t.Fatal(err)
	}

	data, err := os.ReadFile(filepath.Join(tmp, ".multica", "config.json"))
	if err != nil {
		t.Fatal(err)
	}
	if string(data) == "" {
		t.Fatal("config file is empty")
	}

	// The omitempty tag on Backends should keep it out of the JSON entirely.
	var raw map[string]any
	if err := json.Unmarshal(data, &raw); err != nil {
		t.Fatalf("unmarshal saved config: %v", err)
	}
	if _, ok := raw["backends"]; ok {
		t.Errorf("backends key should be omitted when nil, got: %s", string(data))
	}
}

// TestCLIConfig_OpenClawOverride_RoundTrip verifies that setting BinaryPath
// and StateDir survives a save/load cycle.
func TestCLIConfig_OpenClawOverride_RoundTrip(t *testing.T) {
	tmp := t.TempDir()
	t.Setenv("HOME", tmp)

	original := CLIConfig{
		ServerURL: "https://api.multica.ai",
		Token:     "mul_xyz",
		Backends: &BackendOverrides{
			OpenClaw: &OpenClawOverride{
				BinaryPath: "/opt/openclaw-prod/bin/openclaw",
				StateDir:   "/var/lib/openclaw-prod",
			},
		},
	}
	if err := SaveCLIConfig(original); err != nil {
		t.Fatal(err)
	}

	loaded, err := LoadCLIConfig()
	if err != nil {
		t.Fatal(err)
	}

	if loaded.Backends == nil || loaded.Backends.OpenClaw == nil {
		t.Fatalf("Backends.OpenClaw should be non-nil after round-trip, got %+v", loaded.Backends)
	}
	if loaded.Backends.OpenClaw.BinaryPath != original.Backends.OpenClaw.BinaryPath {
		t.Errorf("BinaryPath round-trip: got %q, want %q",
			loaded.Backends.OpenClaw.BinaryPath, original.Backends.OpenClaw.BinaryPath)
	}
	if loaded.Backends.OpenClaw.StateDir != original.Backends.OpenClaw.StateDir {
		t.Errorf("StateDir round-trip: got %q, want %q",
			loaded.Backends.OpenClaw.StateDir, original.Backends.OpenClaw.StateDir)
	}
}

// TestCLIConfig_OpenClawOverride_PartialFieldsOmitted verifies that an
// override with only one field set does not emit empty strings for the
// unset field. Important so users can intentionally set only BinaryPath
// (or only StateDir) and have the other follow the historical default,
// without an empty string overriding env-var precedence.
func TestCLIConfig_OpenClawOverride_PartialFieldsOmitted(t *testing.T) {
	tmp := t.TempDir()
	t.Setenv("HOME", tmp)

	cfg := CLIConfig{
		ServerURL: "https://api.multica.ai",
		Token:     "mul_xyz",
		Backends: &BackendOverrides{
			OpenClaw: &OpenClawOverride{
				StateDir: "/var/lib/openclaw-prod",
				// BinaryPath intentionally left empty
			},
		},
	}
	if err := SaveCLIConfig(cfg); err != nil {
		t.Fatal(err)
	}

	data, err := os.ReadFile(filepath.Join(tmp, ".multica", "config.json"))
	if err != nil {
		t.Fatal(err)
	}

	var raw map[string]any
	if err := json.Unmarshal(data, &raw); err != nil {
		t.Fatal(err)
	}

	openclaw, ok := raw["backends"].(map[string]any)["openclaw"].(map[string]any)
	if !ok {
		t.Fatalf("could not navigate to backends.openclaw in: %s", string(data))
	}
	if _, present := openclaw["binary_path"]; present {
		t.Errorf("binary_path should be omitted when empty, got: %s", string(data))
	}
	if _, present := openclaw["state_dir"]; !present {
		t.Errorf("state_dir should be present when set, got: %s", string(data))
	}
}

func TestCLIConfig_ProxyConfig_RoundTrip(t *testing.T) {
	tmp := t.TempDir()
	t.Setenv("HOME", tmp)

	original := CLIConfig{
		ServerURL: "https://api.multica.ai",
		Proxy: &ProxyConfig{
			HTTP:    "http://proxy.internal:8080",
			HTTPS:   "http://secure-proxy.internal:8443",
			NoProxy: ".corp.example,metadata.internal",
		},
	}
	if err := SaveCLIConfig(original); err != nil {
		t.Fatal(err)
	}

	loaded, err := LoadCLIConfig()
	if err != nil {
		t.Fatal(err)
	}
	if loaded.Proxy == nil {
		t.Fatal("Proxy should be non-nil after round-trip")
	}
	if *loaded.Proxy != *original.Proxy {
		t.Fatalf("Proxy round-trip = %+v, want %+v", loaded.Proxy, original.Proxy)
	}
}

func TestCLIConfig_LegacyCloudConfigMigratesToEnvironmentSessions(t *testing.T) {
	tmp := t.TempDir()
	t.Setenv("HOME", tmp)
	configDir := filepath.Join(tmp, ".multica")
	if err := os.MkdirAll(configDir, 0o755); err != nil {
		t.Fatal(err)
	}
	legacy := `{
  "environment": "test",
  "release_channel": "latest",
  "server_url": "https://test.leagent.me",
  "app_url": "https://test.leagent.me",
  "workspace_id": "ws-test",
  "token": "test-session"
}`
	configPath := filepath.Join(configDir, "config.json")
	if err := os.WriteFile(configPath, []byte(legacy), 0o600); err != nil {
		t.Fatal(err)
	}

	cfg, err := LoadCLIConfig()
	if err != nil {
		t.Fatal(err)
	}
	if cfg.Environment != "test" || cfg.Token != "test-session" || cfg.WorkspaceID != "ws-test" {
		t.Fatalf("legacy effective projection = %+v", cfg)
	}
	channel, err := ResolveReleaseChannel(cfg)
	if err != nil || channel != ReleaseChannelAlpha {
		t.Fatalf("legacy release_channel must be ignored: got %q, %v", channel, err)
	}
	if err := SaveCLIConfig(cfg); err != nil {
		t.Fatal(err)
	}

	data, err := os.ReadFile(configPath)
	if err != nil {
		t.Fatal(err)
	}
	var raw map[string]any
	if err := json.Unmarshal(data, &raw); err != nil {
		t.Fatal(err)
	}
	if raw["active_environment"] != "test" {
		t.Fatalf("active_environment = %v, want test; file=%s", raw["active_environment"], data)
	}
	for _, retired := range []string{"environment", "release_channel", "server_url", "app_url", "workspace_id", "token"} {
		if _, exists := raw[retired]; exists {
			t.Fatalf("legacy key %q survived migration: %s", retired, data)
		}
	}
}

func TestCLIConfig_EnvironmentSwitchRestoresIndependentSessions(t *testing.T) {
	tmp := t.TempDir()
	t.Setenv("HOME", tmp)

	cfg := CLIConfig{}
	production, err := NewServiceTarget("production", "")
	if err != nil {
		t.Fatal(err)
	}
	cfg.PutServiceEnvironment(production)
	cfg.Token = "prod-session"
	cfg.WorkspaceID = "ws-prod"
	testTarget, err := NewServiceTarget("test", "https://test.leagent.me")
	if err != nil {
		t.Fatal(err)
	}
	cfg.PutServiceEnvironment(testTarget)
	cfg.Token = "test-session"
	cfg.WorkspaceID = "ws-test"
	if err := SaveCLIConfig(cfg); err != nil {
		t.Fatal(err)
	}

	loaded, err := LoadCLIConfig()
	if err != nil {
		t.Fatal(err)
	}
	if loaded.Environment != "test" || loaded.Token != "test-session" || loaded.WorkspaceID != "ws-test" {
		t.Fatalf("test projection = %+v", loaded)
	}
	if err := loaded.ActivateServiceEnvironment(ServiceEnvironmentProduction); err != nil {
		t.Fatal(err)
	}
	if loaded.Token != "prod-session" || loaded.WorkspaceID != "ws-prod" {
		t.Fatalf("production session was not restored: %+v", loaded)
	}
	if err := SaveCLIConfig(loaded); err != nil {
		t.Fatal(err)
	}
	reloaded, err := LoadCLIConfig()
	if err != nil {
		t.Fatal(err)
	}
	if reloaded.Environment != "production" || reloaded.Token != "prod-session" {
		t.Fatalf("saved production projection = %+v", reloaded)
	}
	if err := reloaded.ActivateServiceEnvironment(ServiceEnvironmentTest); err != nil {
		t.Fatal(err)
	}
	if reloaded.Token != "test-session" || reloaded.WorkspaceID != "ws-test" {
		t.Fatalf("test session did not survive round-trip: %+v", reloaded)
	}
}

func TestCLIConfig_ActivateRequiresConfiguredEnvironment(t *testing.T) {
	cfg := CLIConfig{}
	production, err := NewServiceTarget("production", "")
	if err != nil {
		t.Fatal(err)
	}
	cfg.PutServiceEnvironment(production)
	if err := cfg.ActivateServiceEnvironment(ServiceEnvironmentTest); err == nil || !strings.Contains(err.Error(), "multica setup --environment test") {
		t.Fatalf("ActivateServiceEnvironment(test) error = %v", err)
	}
}

// TestCLIConfig_UnknownFieldsArePreserved verifies forward-compat: a future
// daemon that adds, say, a `backends.codex` key should not have its data
// destroyed when an older daemon (without knowledge of that key) reads and
// re-saves the file. Today Go's encoding/json silently DROPS unknown fields
// on round-trip. This test documents the gap so future maintainers know.
//
// Skipped today (encoding/json does not preserve unknown fields), but the
// test is written so a future change to a preserve-unknown encoder
// (json.RawMessage, mapstructure, etc.) will pick it up.
func TestCLIConfig_UnknownFieldsArePreserved(t *testing.T) {
	t.Skip("documenting known limitation: encoding/json drops unknown fields on round-trip; future PR can switch to a preserving encoder")

	tmp := t.TempDir()
	t.Setenv("HOME", tmp)

	cfgDir := filepath.Join(tmp, ".multica")
	if err := os.MkdirAll(cfgDir, 0o755); err != nil {
		t.Fatal(err)
	}
	withFutureField := `{
  "server_url": "https://api.multica.ai",
  "token": "mul_xyz",
  "backends": {
    "openclaw": {"state_dir": "/x"},
    "future_backend_xyz": {"some_setting": "preserve me"}
  }
}`
	if err := os.WriteFile(filepath.Join(cfgDir, "config.json"), []byte(withFutureField), 0o600); err != nil {
		t.Fatal(err)
	}

	cfg, err := LoadCLIConfig()
	if err != nil {
		t.Fatal(err)
	}
	if err := SaveCLIConfig(cfg); err != nil {
		t.Fatal(err)
	}

	// After round-trip, future_backend_xyz should still be in the file.
	data, _ := os.ReadFile(filepath.Join(cfgDir, "config.json"))
	if !strings.Contains(string(data), "future_backend_xyz") {
		t.Error("unknown field future_backend_xyz was dropped on round-trip")
	}
}
