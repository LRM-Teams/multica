package main

import (
	"bytes"
	"context"
	"encoding/json"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

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

func TestStartRuntimeInCubeRejectsNDJSONErrorAndSuppressesDetails(t *testing.T) {
	const sensitive = "synthetic-provider-detail"
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost || r.URL.Path != "/execute" {
			t.Fatalf("unexpected Cube request %s %s", r.Method, r.URL.Path)
		}
		_, _ = w.Write([]byte(`{"type":"error","name":"RuntimeError","value":"` + sensitive + `"}` + "\n"))
	}))
	defer server.Close()
	client := &sandboxdClient{cfg: sandboxdConfig{CubeProxyHTTP: server.URL, CubeDomain: "cube.test"}, http: server.Client()}
	err := client.startRuntimeInCube(context.Background(), "cube-1", map[string]string{"MULTICA_TOKEN": "tok"})
	if err == nil || err.Error() != "cube runtime bootstrap failed" {
		t.Fatalf("start error = %v", err)
	}
	if strings.Contains(err.Error(), sensitive) {
		t.Fatal("bootstrap error disclosed child details")
	}
}

func TestStartRuntimeInCubeAcceptsSuccessfulNDJSON(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(`{"type":"stdout","text":"started"}` + "\n"))
	}))
	defer server.Close()
	client := &sandboxdClient{cfg: sandboxdConfig{CubeProxyHTTP: server.URL, CubeDomain: "cube.test"}, http: server.Client()}
	if err := client.startRuntimeInCube(context.Background(), "cube-1", map[string]string{"MULTICA_TOKEN": "tok"}); err != nil {
		t.Fatalf("start: %v", err)
	}
}

func TestStartRuntimeInCubeSuppressesNon2xxResponseBody(t *testing.T) {
	const sensitive = "synthetic-sensitive-bootstrap-body"
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusBadGateway)
		_, _ = w.Write([]byte(sensitive))
	}))
	defer server.Close()
	client := &sandboxdClient{cfg: sandboxdConfig{CubeProxyHTTP: server.URL, CubeDomain: "cube.test"}, http: server.Client()}
	err := client.startRuntimeInCube(context.Background(), "cube-1", map[string]string{"MULTICA_TOKEN": "tok"})
	if err == nil || err.Error() != "cube runtime bootstrap failed" {
		t.Fatalf("start error = %v", err)
	}
	if strings.Contains(err.Error(), sensitive) {
		t.Fatal("bootstrap error disclosed non-2xx response body")
	}
}

func TestCreateCubeSandboxRetriesProvisionalDeleteWithoutLeakingDetails(t *testing.T) {
	const sensitive = "synthetic-sensitive-delete-body"
	var deletes atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.Method == http.MethodPost && r.URL.Path == "/sandboxes":
			_, _ = w.Write([]byte(`{"sandboxID":"cube-provisional"}`))
		case r.Method == http.MethodPost && r.URL.Path == "/execute":
			_, _ = w.Write([]byte(`{"type":"error","value":"bootstrap failed"}` + "\n"))
		case r.Method == http.MethodDelete && r.URL.Path == "/sandboxes/cube-provisional":
			deletes.Add(1)
			w.WriteHeader(http.StatusBadGateway)
			_, _ = w.Write([]byte(sensitive))
		default:
			t.Fatalf("unexpected Cube request %s %s", r.Method, r.URL.Path)
		}
	}))
	defer server.Close()
	var logs bytes.Buffer
	client := &sandboxdClient{
		cfg:    sandboxdConfig{SandboxServer: server.URL, CubeProxyHTTP: server.URL, CubeDomain: "cube.test"},
		http:   server.Client(),
		logger: slog.New(slog.NewTextHandler(&logs, nil)),
	}
	_, err := client.createCubeSandbox(context.Background(), sandboxJob{InstanceID: "instance-1"}, sandboxJobPayload{
		Template: "template-1", RuntimeEnv: map[string]string{"MULTICA_TOKEN": "tok"},
	})
	if err == nil || err.Error() != "cube runtime bootstrap failed" {
		t.Fatalf("create error = %v", err)
	}
	if got := deletes.Load(); got != 3 {
		t.Fatalf("provisional delete attempts = %d, want 3", got)
	}
	if strings.Contains(logs.String(), sensitive) {
		t.Fatal("provisional cleanup log disclosed provider response body")
	}
	if !strings.Contains(logs.String(), "cube_provisional_delete_failed") {
		t.Fatalf("missing fixed cleanup category in log: %s", logs.String())
	}
}

func TestCreateCubeSandboxDockerDeleteSerializesByInstance(t *testing.T) {
	binDir := t.TempDir()
	stateDir := t.TempDir()
	script := `#!/bin/sh
set -eu
printf '%s\n' "$*" >> "$DOCKER_TEST_STATE/commands"
case "$1" in
  ps)
    if [ -f "$DOCKER_TEST_STATE/container" ]; then cat "$DOCKER_TEST_STATE/container"; fi
    ;;
  run)
    touch "$DOCKER_TEST_STATE/run_started"
    while [ ! -f "$DOCKER_TEST_STATE/release_run" ]; do sleep 0.01; done
    printf '%s\n' 'container-1' > "$DOCKER_TEST_STATE/container"
    printf '%s\n' 'container-1'
    ;;
  port)
    exit 1
    ;;
  rm)
    rm -f "$DOCKER_TEST_STATE/container"
    touch "$DOCKER_TEST_STATE/deleted"
    ;;
  *)
    exit 1
    ;;
esac
`
	dockerPath := filepath.Join(binDir, "docker")
	if err := os.WriteFile(dockerPath, []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", binDir+string(os.PathListSeparator)+os.Getenv("PATH"))
	t.Setenv("DOCKER_TEST_STATE", stateDir)

	client := &sandboxdClient{logger: slog.Default()}
	createDone := make(chan error, 1)
	go func() {
		_, err := client.callCube(context.Background(), sandboxJob{
			InstanceID: "instance-1", WorkspaceID: "workspace-1", Type: "create",
			Payload: json.RawMessage(`{"docker_image":"runtime:test","runtime_env":{"MULTICA_TOKEN":"tok"}}`),
		})
		createDone <- err
	}()
	runStarted := filepath.Join(stateDir, "run_started")
	deadline := time.Now().Add(2 * time.Second)
	for {
		if _, err := os.Stat(runStarted); err == nil {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("docker create did not reach run")
		}
		time.Sleep(10 * time.Millisecond)
	}

	deleteDone := make(chan error, 1)
	go func() {
		_, err := client.callCube(context.Background(), sandboxJob{
			InstanceID: "instance-1", WorkspaceID: "workspace-1", Type: "delete",
			Payload: json.RawMessage(`{"template":"docker:runtime:test"}`),
		})
		deleteDone <- err
	}()
	time.Sleep(50 * time.Millisecond)
	if _, err := os.Stat(filepath.Join(stateDir, "deleted")); err == nil {
		t.Fatal("delete ran before create completed")
	}
	if err := os.WriteFile(filepath.Join(stateDir, "release_run"), []byte("release"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := <-createDone; err != nil {
		t.Fatalf("create: %v", err)
	}
	if err := <-deleteDone; err != nil {
		t.Fatalf("delete: %v", err)
	}
	if _, err := os.Stat(filepath.Join(stateDir, "deleted")); err != nil {
		t.Fatalf("container was not deleted by instance label: %v", err)
	}
}

func TestDockerRuntimeEntrypointKeepsContainerAlive(t *testing.T) {
	script := dockerRuntimeEntrypointScript()
	if !strings.Contains(script, "/etc/cont-init.d/99-browser-vnc") {
		t.Fatalf("entrypoint missing browser/VNC/pi-web init:\n%s", script)
	}
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

func TestParseDockerPublishedPort(t *testing.T) {
	cases := map[string]string{
		"0.0.0.0:32768\n":           "32768",
		"0.0.0.0:32768\n[::]:32768": "32768",
		"[::]:40123\n":              "40123",
		"127.0.0.1:8080":            "8080",
		"":                          "",
	}
	for in, want := range cases {
		if got := parseDockerPublishedPort(in); got != want {
			t.Fatalf("parseDockerPublishedPort(%q)=%q, want %q", in, got, want)
		}
	}
}

func TestDockerContainerNameForJobUsesPayloadHostName(t *testing.T) {
	job := sandboxJob{InstanceID: "11111111-2222-3333-4444-555555555555"}
	payload := sandboxJobPayload{DockerContainerName: "Multica-dev-multica-ai-alpha-jian40-Chrome Box"}
	if got := dockerContainerNameForJob(job, payload); got != "multica-dev-multica-ai-alpha-jian40-chrome-box" {
		t.Fatalf("container name = %q", got)
	}
}

func TestDockerContainerNameForJobFallsBackToMetadata(t *testing.T) {
	job := sandboxJob{InstanceID: "11111111-2222-3333-4444-555555555555"}
	payload := sandboxJobPayload{Metadata: json.RawMessage(`{"docker_container_name":"multica-prod-ws-user-box"}`)}
	if got := dockerContainerNameForJob(job, payload); got != "multica-prod-ws-user-box" {
		t.Fatalf("container name = %q", got)
	}
}

func TestBuildDockerEndpointInfoIncludesServiceURLs(t *testing.T) {
	endpoint := buildDockerEndpointInfo("cid", "multica-abc", "img:latest", "10.0.0.8", map[string]string{
		"6079": "32768",
		"6080": "32769",
	})
	if endpoint["kind"] != "docker" {
		t.Fatalf("kind = %v", endpoint["kind"])
	}
	if endpoint["pi_web_url"] != "http://10.0.0.8:32768/" {
		t.Fatalf("pi_web_url = %v", endpoint["pi_web_url"])
	}
	if endpoint["term_url"] != "http://10.0.0.8:32768/term" {
		t.Fatalf("term_url = %v", endpoint["term_url"])
	}
	if endpoint["novnc_url"] != "http://10.0.0.8:32769/" {
		t.Fatalf("novnc_url = %v", endpoint["novnc_url"])
	}
}

func TestEnsureDockerDesktopEnvDefaults(t *testing.T) {
	env := map[string]string{"MULTICA_TOKEN": "tok"}
	ensureDockerDesktopEnv(env)
	if env["DISPLAY"] != ":0" || env["PI_WEB_PORT"] != "6079" || env["NOVNC_PORT"] != "6080" {
		t.Fatalf("desktop env = %#v", env)
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

func TestMergeRuntimeEnvPreservesSharedCatalogProviderAliases(t *testing.T) {
	runtime := json.RawMessage(`{
		"providers": [
			{"provider":"env-leader-agent-1","api_key":"placeholder-a","base_url":"https://a.invalid/v1","model":"glm-5.2"},
			{"provider":"env-peer-agent-2","api_key":"placeholder-b","base_url":"https://b.invalid/v1","model":"model-b"}
		],
		"default_provider":"env-leader-agent-1",
		"default_model":"glm-5.2"
	}`)
	out := mergeRuntimeEnv(nil, runtime)
	if out["TEAM_PROVIDER"] != "env-leader-agent-1" || out["TEAM_MODEL"] != "glm-5.2" {
		t.Fatal("custom shared-catalog default alias was not selected")
	}
	var cfg teamPiConfig
	if err := json.Unmarshal([]byte(out["TEAM_PI_CONFIG"]), &cfg); err != nil {
		t.Fatalf("decode TEAM_PI_CONFIG: %v", err)
	}
	if len(cfg.Providers) != 2 {
		t.Fatalf("TEAM_PI_CONFIG provider count = %d, want 2", len(cfg.Providers))
	}
	if cfg.Providers[0].Name != "env-leader-agent-1" || cfg.Providers[1].Name != "env-peer-agent-2" {
		t.Fatal("TEAM_PI_CONFIG rewrote deterministic provider aliases")
	}
	if cfg.Providers[0].BaseURL != "https://a.invalid/v1" || cfg.Providers[1].BaseURL != "https://b.invalid/v1" ||
		cfg.Providers[0].Model != "glm-5.2" || cfg.Providers[1].Model != "model-b" {
		t.Fatal("TEAM_PI_CONFIG lost an aliased provider route")
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

// Daemon-less Cube sandboxes (template builders, image holders) carry no
// minted MULTICA_TOKEN; create must skip the runtime start instead of
// failing the job.
func TestCreateCubeSandboxWithoutTokenSkipsRuntimeStart(t *testing.T) {
	var paths []string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		paths = append(paths, r.Method+" "+r.URL.Path)
		switch {
		case r.Method == http.MethodPost && r.URL.Path == "/sandboxes":
			_, _ = w.Write([]byte(`{"sandboxID":"builder-cube-id"}`))
		default:
			t.Fatalf("unexpected Cube request %s %s", r.Method, r.URL.Path)
		}
	}))
	defer server.Close()

	client := &sandboxdClient{cfg: sandboxdConfig{
		SandboxServer: server.URL, CubeProxyHTTP: server.URL, CubeDomain: "cube.test", TemplateID: "tpl-parent",
	}, http: server.Client()}
	result, err := client.callCube(context.Background(), sandboxJob{
		InstanceID: "builder-instance", WorkspaceID: "workspace", Type: "create",
		Payload: json.RawMessage(`{"template":"tpl-parent","limits":{"timeout":60}}`),
	})
	if err != nil {
		t.Fatalf("daemon-less create: %v", err)
	}
	if result["local_ref"] != "builder-cube-id" {
		t.Fatalf("create local_ref = %v", result["local_ref"])
	}
	if joined := strings.Join(paths, ","); strings.Contains(joined, "/execute") {
		t.Fatalf("daemon-less create must not start the runtime, requests = %q", joined)
	}
}

// The Cube /execute response is an NDJSON event stream: stdout events
// accumulate into the job result, an error event fails the exec job.
func TestExecCubeSandboxParsesNDJSONStream(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodPost && r.URL.Path == "/execute" {
			_, _ = w.Write([]byte("{\"type\":\"stdout\",\"text\":\"clone ok\\n\"}\n{\"type\":\"stdout\",\"text\":\"__EXIT_CODE__=0\\n\"}\n"))
			return
		}
		t.Fatalf("unexpected Cube request %s %s", r.Method, r.URL.Path)
	}))
	defer server.Close()

	client := &sandboxdClient{cfg: sandboxdConfig{
		SandboxServer: server.URL, CubeProxyHTTP: server.URL, CubeDomain: "cube.test",
	}, http: server.Client()}
	result, err := client.callCube(context.Background(), sandboxJob{
		InstanceID: "builder-instance", WorkspaceID: "workspace", Type: "exec",
		Payload: json.RawMessage(`{"local_ref":"builder-cube-id","code":"print(1)","language":"python","timeout_seconds":60}`),
	})
	if err != nil {
		t.Fatalf("exec: %v", err)
	}
	res, _ := result["result"].(map[string]any)
	stdout, _ := res["stdout"].(string)
	if !strings.Contains(stdout, "clone ok") || !strings.Contains(stdout, "__EXIT_CODE__=0") {
		t.Fatalf("exec stdout = %q", stdout)
	}
}

func TestExecCubeSandboxFailsOnErrorEvent(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodPost && r.URL.Path == "/execute" {
			_, _ = w.Write([]byte("{\"type\":\"stdout\",\"text\":\"fatal: checkout failed\\n\"}\n{\"type\":\"error\",\"name\":\"SystemExit\",\"value\":\"1\"}\n"))
			return
		}
		t.Fatalf("unexpected Cube request %s %s", r.Method, r.URL.Path)
	}))
	defer server.Close()

	client := &sandboxdClient{cfg: sandboxdConfig{
		SandboxServer: server.URL, CubeProxyHTTP: server.URL, CubeDomain: "cube.test",
	}, http: server.Client()}
	_, err := client.callCube(context.Background(), sandboxJob{
		InstanceID: "builder-instance", WorkspaceID: "workspace", Type: "exec",
		Payload: json.RawMessage(`{"local_ref":"builder-cube-id","code":"raise SystemExit(1)","language":"python","timeout_seconds":60}`),
	})
	if err == nil || !strings.Contains(err.Error(), "SystemExit") {
		t.Fatalf("exec error = %v, want the error event surfaced", err)
	}
}
