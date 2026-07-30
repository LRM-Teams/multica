package main

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestCallCubeCloneSnapshotsCreatesAndDeletesSnapshot(t *testing.T) {
	var paths []string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		paths = append(paths, r.Method+" "+r.URL.Path)
		switch {
		case r.Method == http.MethodPost && r.URL.Path == "/sandboxes/source-external-id/snapshots":
			var body map[string]any
			if err := json.NewDecoder(r.Body).Decode(&body); err != nil || len(body) != 0 {
				t.Fatalf("snapshot body = %#v, err = %v", body, err)
			}
			_, _ = w.Write([]byte(`{"templateID":"snapshot-id"}`))
		case r.Method == http.MethodPost && r.URL.Path == "/sandboxes":
			var body map[string]any
			if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
				t.Fatal(err)
			}
			if body["templateID"] != "snapshot-id" {
				t.Fatalf("create templateID = %v", body["templateID"])
			}
			_, _ = w.Write([]byte(`{"sandboxID":"destination-external-id"}`))
		case r.Method == http.MethodPost && r.URL.Path == "/execute":
			w.WriteHeader(http.StatusOK)
		case r.Method == http.MethodDelete && r.URL.Path == "/templates/snapshot-id":
			w.WriteHeader(http.StatusNoContent)
		default:
			t.Fatalf("unexpected Cube request %s %s", r.Method, r.URL.Path)
		}
	}))
	defer server.Close()

	client := &sandboxdClient{cfg: sandboxdConfig{
		SandboxServer: server.URL, CubeProxyHTTP: server.URL, CubeDomain: "cube.test",
	}, http: server.Client()}
	result, err := client.callCube(context.Background(), sandboxJob{
		InstanceID: "destination-instance", WorkspaceID: "workspace", Type: "clone",
		Payload: json.RawMessage(`{
			"source_external_id":"source-external-id",
			"create_payload":{"template":"default","limits":{"timeout":60},"runtime_env":{"MULTICA_TOKEN":"token"}}
		}`),
	})
	if err != nil {
		t.Fatalf("callCube clone: %v", err)
	}
	if result["local_ref"] != "destination-external-id" {
		t.Fatalf("clone local_ref = %v", result["local_ref"])
	}
	joined := strings.Join(paths, ",")
	for _, required := range []string{
		"POST /sandboxes/source-external-id/snapshots",
		"POST /sandboxes",
		"DELETE /templates/snapshot-id",
	} {
		if !strings.Contains(joined, required) {
			t.Fatalf("clone request sequence %q missing %q", joined, required)
		}
	}
}

func TestBuildStartRuntimeInCubeCodeResetsFrozenDaemonIdentity(t *testing.T) {
	code := buildStartRuntimeInCubeCode(map[string]string{
		"MULTICA_TOKEN":     "tok",
		"MULTICA_DAEMON_ID": "aaaaaaaa-bbbb-cccc-dddd-eeeeeeeeeeee",
	})
	for _, want := range []string{
		"pkill -f '[m]ultica daemon'",
		`daemon_file.write_text(daemon_id + "\n")`,
		`profiles.glob("*/daemon.id")`,
		"aaaaaaaa-bbbb-cccc-dddd-eeeeeeeeeeee",
		"/usr/local/bin/start-multica-runtime.sh",
	} {
		if !strings.Contains(code, want) {
			t.Fatalf("start runtime code missing %q\n%s", want, code)
		}
	}
	// A literal "multica daemon" substring in python -c argv would make pkill
	// kill the reconfigure process itself before models.json is written.
	if strings.Contains(code, "pkill -f 'multica daemon'") {
		t.Fatal("pkill pattern must use [m]ultica trick to avoid self-match under python -c")
	}
}

func TestDockerRuntimeEntrypointKeepsContainerAlive(t *testing.T) {
	script := dockerRuntimeEntrypointScript()
	if !strings.Contains(script, "/usr/local/bin/start-multica-runtime.sh") {
		t.Fatalf("entrypoint missing runtime start:\n%s", script)
	}
	if !strings.Contains(script, "tail -f /dev/null") {
		t.Fatalf("entrypoint should keep PID 1 alive for in-place reconfigure:\n%s", script)
	}
	if strings.Contains(script, "exit 1") {
		t.Fatalf("entrypoint must not exit when daemon stops:\n%s", script)
	}
}

func TestDockerEntrypointKeepaliveDoesNotMatchDaemonPkill(t *testing.T) {
	// Keepalive must satisfy legacy `pgrep -f 'multica .*daemon start'` while
	// surviving `pkill -f '[m]ultica daemon'` from buildStartRuntimeInCubeCode.
	if !strings.Contains(dockerEntrypointKeepaliveCmdline, "multica") ||
		!strings.Contains(dockerEntrypointKeepaliveCmdline, "daemon start") {
		t.Fatalf("keepalive %q should match entrypoint pgrep pattern", dockerEntrypointKeepaliveCmdline)
	}
	if strings.Contains(dockerEntrypointKeepaliveCmdline, "multica daemon") {
		t.Fatalf("keepalive %q must not match pkill -f '[m]ultica daemon'", dockerEntrypointKeepaliveCmdline)
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

func TestMergeRuntimeEnvDefaultsOpenAIModel(t *testing.T) {
	runtime := json.RawMessage(`{"providers":[{"provider":"openai","api_key":"sk","base_url":"https://x/v1"}],"default_provider":"openai"}`)
	out := mergeRuntimeEnv(nil, runtime)
	if out["TEAM_MODEL"] != "gpt-5.5" {
		t.Fatalf("TEAM_MODEL = %q", out["TEAM_MODEL"])
	}
	var cfg teamPiConfig
	if err := json.Unmarshal([]byte(out["TEAM_PI_CONFIG"]), &cfg); err != nil {
		t.Fatalf("TEAM_PI_CONFIG: %v", err)
	}
	if cfg.DefaultModel != "gpt-5.5" || cfg.Providers[0].Model != "gpt-5.5" {
		t.Fatalf("cfg = %#v", cfg)
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
