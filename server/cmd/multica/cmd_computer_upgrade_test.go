package main

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"context"
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/multica-ai/multica/server/internal/cli"
	"github.com/multica-ai/multica/server/internal/computer"
)

const computerUpgradeSubprocessModeEnv = "MULTICA_TEST_COMPUTER_UPGRADE_SUBPROCESS_MODE"
const testComputerControlTokenFile = "machine-upgrade-control.token"
const testComputerUpgradeTargetEnv = "MULTICA_TEST_COMPUTER_UPGRADE_TARGET"
const testComputerUpgradeControlEndpointEnv = "MULTICA_TEST_COMPUTER_UPGRADE_CONTROL_ENDPOINT"

type computerUpgradeSubprocessRequest struct {
	RequestID     string `json:"request_id"`
	TargetVersion string `json:"target_version"`
}

func TestComputerUpgradeSubprocessLiveOwnerCreatesHumanIntentBeforeLocalExecution(t *testing.T) {
	home := t.TempDir()
	controlDir := filepath.Join(home, ".multica", "computer")
	if err := os.MkdirAll(controlDir, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(controlDir, testComputerControlTokenFile), []byte("owner-secret\n"), 0o600); err != nil {
		t.Fatal(err)
	}

	controlEndpoint, localExecutions := newComputerUpgradeSubprocessControlServer(t)

	var intentCount atomic.Int32
	intentServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/daemons/daemon-1/upgrades" {
			http.NotFound(w, r)
			return
		}
		if got := r.Header.Get("Authorization"); got != "Bearer human-token" {
			http.Error(w, "bad human token", http.StatusUnauthorized)
			return
		}
		if got := r.Header.Get("X-Workspace-ID"); got != "workspace-1" {
			http.Error(w, "bad workspace", http.StatusForbidden)
			return
		}
		var request computerUpgradeSubprocessRequest
		if err := json.NewDecoder(r.Body).Decode(&request); err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}
		attempt := intentCount.Add(1)
		// Socket-only create: the cloud POST already dispatched
		// computer:upgrade. There is no receipt id/phase.
		w.WriteHeader(http.StatusAccepted)
		_ = json.NewEncoder(w).Encode(map[string]any{
			"request_id": request.RequestID,
			"attempt":    attempt,
		})
	}))
	defer intentServer.Close()
	writeComputerUpgradeHumanConfig(t, home, intentServer.URL)

	targets := []struct {
		arg  string
		want string
	}{{want: "latest"}, {arg: "1.2.3", want: "v1.2.3"}}
	for attempt, target := range targets {
		output, err := runComputerUpgradeSubprocessWithTarget(t, home, controlEndpoint, "http://127.0.0.1:1/unreachable", target.arg)
		if err != nil {
			t.Fatalf("multica computer upgrade subprocess %d: %v\n%s", attempt+1, err, output)
		}
		if !strings.Contains(string(output), "live Computer owns download, verification, handoff, and convergence") {
			t.Fatalf("subprocess output = %q, want live-owner confirmation", output)
		}
		if !strings.Contains(string(output), target.want) {
			t.Fatalf("subprocess output = %q, want target %s", output, target.want)
		}
	}
	if got := intentCount.Load(); got != int32(len(targets)) {
		t.Fatalf("human lifecycle intents = %d, want %d", got, len(targets))
	}
	if got := localExecutions.Load(); got != 0 {
		t.Fatalf("local /machine-upgrades deliveries = %d, want 0", got)
	}
}

func TestComputerUpgradeSubprocessAbsentResidentInstallsForNextStart(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("the Windows adapter is cross-compiled separately; this subprocess fixture is a POSIX script")
	}
	home := t.TempDir()
	feed := newComputerUpgradeSubprocessReleaseFeed(t, "v1.2.3")
	defer feed.Close()
	controlEndpoint := unusedComputerUpgradeControlEndpoint(t)

	child := exec.Command(os.Args[0], "-test.run=^TestComputerUpgradeSubprocessHelper$")
	child.Env = append(os.Environ(),
		"HOME="+home,
		computerUpgradeSubprocessModeEnv+"=offline",
		testComputerUpgradeControlEndpointEnv+"="+controlEndpoint,
		"MULTICA_RELEASE_MANIFEST_BASE_URL="+feed.URL,
	)
	output, err := child.CombinedOutput()
	if err != nil {
		t.Fatalf("offline multica computer upgrade subprocess: %v\n%s", err, output)
	}
	if !strings.Contains(string(output), "Installed v1.2.3 for the next Computer start") ||
		!strings.Contains(string(output), "No running successor was proven") {
		t.Fatalf("offline output = %q, want precise next-start/no-successor wording", output)
	}

	installed := filepath.Join(home, ".local", "bin", "multica")
	got, err := os.ReadFile(installed)
	if err != nil {
		t.Fatalf("PATH computer after upgrade: %v\n%s", err, output)
	}
	if !strings.Contains(string(got), "multica v1.2.3") {
		t.Fatalf("PATH computer = %q, want released v1.2.3 bytes", got)
	}
}

func TestComputerUpgradeSubprocessLiveOwnerFailuresNeverMutateVersionStore(t *testing.T) {
	home := t.TempDir()
	writeComputerUpgradeControlToken(t, home, "wrong-owner-secret")
	intentServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/daemons/daemon-1/upgrades" {
			http.NotFound(w, r)
			return
		}
		http.Error(w, "invalid human session", http.StatusUnauthorized)
	}))
	defer intentServer.Close()
	writeComputerUpgradeHumanConfig(t, home, intentServer.URL)
	controlEndpoint, localExecutions := newComputerUpgradeSubprocessControlServer(t)

	feed := newComputerUpgradeSubprocessReleaseFeed(t, "v1.2.3")
	defer feed.Close()
	output, err := runComputerUpgradeSubprocess(t, home, controlEndpoint, feed.URL)
	if err == nil || !strings.Contains(string(output), "POST /api/daemons/daemon-1/upgrades returned 401") {
		t.Fatalf("subprocess error = %v output = %q, want cloud authorization failure", err, output)
	}
	if got := localExecutions.Load(); got != 0 {
		t.Fatalf("local executions after rejected cloud dispatch = %d, want zero", got)
	}
	assertComputerUpgradeVersionStoreUnchanged(t, home)
}

func TestComputerUpgradeSubprocessRejectsHumanIntentFailureBeforeLocalExecution(t *testing.T) {
	home := t.TempDir()
	writeComputerUpgradeControlToken(t, home, "owner-secret")
	intentServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		http.Error(w, "invalid human session", http.StatusUnauthorized)
	}))
	defer intentServer.Close()
	writeComputerUpgradeHumanConfig(t, home, intentServer.URL)

	controlEndpoint, localExecutions := newComputerUpgradeSubprocessControlServer(t)

	feed := newComputerUpgradeSubprocessReleaseFeed(t, "v1.2.3")
	defer feed.Close()
	output, err := runComputerUpgradeSubprocess(t, home, controlEndpoint, feed.URL)
	if err == nil || !strings.Contains(string(output), "POST /api/daemons/daemon-1/upgrades returned 401") {
		t.Fatalf("subprocess error = %v output = %q, want human authorization failure", err, output)
	}
	if got := localExecutions.Load(); got != 0 {
		t.Fatalf("local executions after rejected human intent = %d, want zero", got)
	}
	assertComputerUpgradeVersionStoreUnchanged(t, home)
}

func TestComputerUpgradeSubprocessLivePIDWithUnavailableControlFailsClosed(t *testing.T) {
	home := t.TempDir()
	pidDir := filepath.Join(home, ".multica", "computer", "run")
	if err := os.MkdirAll(pidDir, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(pidDir, "service.pid"), []byte(strconv.Itoa(os.Getpid())+"\n"), 0o600); err != nil {
		t.Fatal(err)
	}

	output, err := runComputerUpgradeSubprocess(t, home, unusedComputerUpgradeControlEndpoint(t), "http://127.0.0.1:1/unreachable")
	if err == nil || !strings.Contains(string(output), "upgrade_service_unreachable") ||
		!strings.Contains(string(output), "refusing offline activation") {
		t.Fatalf("subprocess error = %v output = %q, want fail-closed unavailable owner", err, output)
	}
	assertComputerUpgradeVersionStoreUnchanged(t, home)
}

func TestComputerUpgradeSubprocessStaleDeadPIDAllowsOfflineFallback(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("the Windows adapter is cross-compiled separately; this subprocess fixture is a POSIX script")
	}
	home := t.TempDir()
	pidDir := filepath.Join(home, ".multica", "computer", "run")
	if err := os.MkdirAll(pidDir, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(pidDir, "service.pid"), []byte("2147483647\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	feed := newComputerUpgradeSubprocessReleaseFeed(t, "v1.2.3")
	defer feed.Close()

	output, err := runComputerUpgradeSubprocess(t, home, unusedComputerUpgradeControlEndpoint(t), feed.URL)
	if err != nil {
		t.Fatalf("offline fallback with stale PID: %v\n%s", err, output)
	}
	if !strings.Contains(string(output), "Installed v1.2.3 for the next Computer start") {
		t.Fatalf("stale-PID fallback output = %q", output)
	}
}

func TestComputerUpgradeSubprocessCloudDispatchConflictNeverHitsLocalOwner(t *testing.T) {
	home := t.TempDir()
	writeComputerUpgradeControlToken(t, home, "owner-secret")
	intentServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/daemons/daemon-1/upgrades" {
			http.NotFound(w, r)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusConflict)
		_ = json.NewEncoder(w).Encode(map[string]string{"code": "no_current_socket", "error": "Computer upgrade needs the current Binding socket"})
	}))
	defer intentServer.Close()
	writeComputerUpgradeHumanConfig(t, home, intentServer.URL)
	controlEndpoint, localExecutions := newComputerUpgradeSubprocessControlServer(t)

	feed := newComputerUpgradeSubprocessReleaseFeed(t, "v1.2.3")
	defer feed.Close()
	output, err := runComputerUpgradeSubprocess(t, home, controlEndpoint, feed.URL)
	if err == nil || !strings.Contains(string(output), "no_current_socket") {
		t.Fatalf("subprocess error = %v output = %q, want cloud no_current_socket", err, output)
	}
	if got := localExecutions.Load(); got != 0 {
		t.Fatalf("local executions after cloud conflict = %d, want zero", got)
	}
	assertComputerUpgradeVersionStoreUnchanged(t, home)
}

func runComputerUpgradeSubprocess(t *testing.T, home, controlEndpoint, releaseBaseURL string) ([]byte, error) {
	t.Helper()
	return runComputerUpgradeSubprocessWithTarget(t, home, controlEndpoint, releaseBaseURL, "")
}

func runComputerUpgradeSubprocessWithTarget(t *testing.T, home, controlEndpoint, releaseBaseURL, target string) ([]byte, error) {
	t.Helper()
	command := newComputerUpgradeSubprocessCommand(home, controlEndpoint, releaseBaseURL)
	if target != "" {
		command.Env = append(command.Env, testComputerUpgradeTargetEnv+"="+target)
	}
	return command.CombinedOutput()
}

func newComputerUpgradeSubprocessCommand(home, controlEndpoint, releaseBaseURL string) *exec.Cmd {
	child := exec.Command(os.Args[0], "-test.run=^TestComputerUpgradeSubprocessHelper$")
	child.Env = append(os.Environ(),
		"HOME="+home,
		computerUpgradeSubprocessModeEnv+"=run",
		testComputerUpgradeControlEndpointEnv+"="+controlEndpoint,
		"MULTICA_RELEASE_MANIFEST_BASE_URL="+releaseBaseURL,
	)
	return child
}

func writeComputerUpgradeControlToken(t *testing.T, home, token string) {
	t.Helper()
	controlDir := filepath.Join(home, ".multica", "computer")
	if err := os.MkdirAll(controlDir, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(controlDir, testComputerControlTokenFile), []byte(token+"\n"), 0o600); err != nil {
		t.Fatal(err)
	}
}

func writeComputerUpgradeHumanConfig(t *testing.T, home, serverURL string) {
	t.Helper()
	configDir := filepath.Join(home, ".multica")
	if err := os.MkdirAll(configDir, 0o700); err != nil {
		t.Fatal(err)
	}
	data, err := json.Marshal(map[string]any{
		"active_environment": "test",
		"environments": map[string]any{
			"test": map[string]string{
				"server_url": serverURL, "app_url": serverURL,
				"workspace_id": "workspace-1", "token": "human-token",
			},
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(configDir, "config.json"), data, 0o600); err != nil {
		t.Fatal(err)
	}
}

func newComputerUpgradeHumanIntentServer(t *testing.T) *httptest.Server {
	t.Helper()
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/daemons/daemon-1/upgrades" || r.Header.Get("Authorization") != "Bearer human-token" || r.Header.Get("X-Workspace-ID") != "workspace-1" {
			http.Error(w, "invalid human lifecycle intent request", http.StatusForbidden)
			return
		}
		var request computerUpgradeSubprocessRequest
		if err := json.NewDecoder(r.Body).Decode(&request); err != nil || strings.TrimSpace(request.RequestID) == "" {
			http.Error(w, "invalid lifecycle intent body", http.StatusBadRequest)
			return
		}
		w.WriteHeader(http.StatusAccepted)
		_ = json.NewEncoder(w).Encode(map[string]any{
			"request_id": request.RequestID,
		})
	}))
}

func newComputerUpgradeSubprocessControlServer(t *testing.T) (string, *atomic.Int32) {
	t.Helper()
	localExecutions := new(atomic.Int32)
	registry := computer.NewLocalControlRegistry()
	if err := registry.Register(computer.LocalControlServiceStatusOperation,
		func(context.Context, map[string]string, json.RawMessage) (any, error) {
			return map[string]string{"status": "running", "daemonId": "daemon-1"}, nil
		}); err != nil {
		t.Fatal(err)
	}
	if err := registry.Register(computer.LocalControlComputerControlOperation,
		func(context.Context, map[string]string, json.RawMessage) (any, error) {
			localExecutions.Add(1)
			return nil, fmt.Errorf("test must not re-deliver a socket-dispatched upgrade")
		}); err != nil {
		t.Fatal(err)
	}
	endpoint := computer.ServiceControlEndpoint(t.TempDir())
	listener, err := computer.ListenLocalControl(endpoint)
	if err != nil {
		t.Fatal(err)
	}
	go computer.ServeLocalControlRPC(context.Background(), listener, registry)
	t.Cleanup(func() { _ = listener.Close() })
	return endpoint, localExecutions
}

func assertComputerUpgradeVersionStoreUnchanged(t *testing.T, home string) {
	t.Helper()
	root := filepath.Join(home, ".local", "share", "multica")
	if _, err := os.Stat(root); !os.IsNotExist(err) {
		t.Fatalf("VersionStore changed on failed live-owner request: stat %s: %v", root, err)
	}
}

func newComputerUpgradeSubprocessReleaseFeed(t *testing.T, tag string) *httptest.Server {
	t.Helper()
	script := []byte("#!/bin/sh\nprintf 'multica " + tag + "\\n'\n")
	var archive bytes.Buffer
	gzipWriter := gzip.NewWriter(&archive)
	tarWriter := tar.NewWriter(gzipWriter)
	if err := tarWriter.WriteHeader(&tar.Header{Name: "multica", Mode: 0o755, Size: int64(len(script))}); err != nil {
		t.Fatal(err)
	}
	if _, err := tarWriter.Write(script); err != nil {
		t.Fatal(err)
	}
	if err := tarWriter.Close(); err != nil {
		t.Fatal(err)
	}
	if err := gzipWriter.Close(); err != nil {
		t.Fatal(err)
	}
	digest := fmt.Sprintf("%x", sha256.Sum256(archive.Bytes()))
	versionNumber := strings.TrimPrefix(tag, "v")
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	baseURL := "http://" + listener.Addr().String()
	feed := httptest.NewUnstartedServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		manifest := cli.ReleaseManifest{
			TagName: tag,
			Version: versionNumber,
			Platforms: map[string]cli.ReleaseAsset{
				runtime.GOOS + "-" + runtime.GOARCH: {URL: baseURL + "/asset.tar.gz", SHA256: digest},
			},
		}
		switch r.URL.Path {
		case "/metainfo.json":
			_ = json.NewEncoder(w).Encode(cli.ReleaseMetainfo{SchemaVersion: 1, Environments: map[string]cli.ReleaseManifest{"production": manifest, "test": manifest}})
		case "/" + versionNumber + "/manifest.json":
			_ = json.NewEncoder(w).Encode(manifest)
		case "/asset.tar.gz":
			_, _ = w.Write(archive.Bytes())
		default:
			http.NotFound(w, r)
		}
	}))
	feed.Listener = listener
	feed.Start()
	return feed
}

func unusedComputerUpgradeControlEndpoint(t *testing.T) string {
	t.Helper()
	return filepath.Join(t.TempDir(), "service.sock")
}

func TestComputerUpgradeSubprocessHelper(t *testing.T) {
	if os.Getenv(computerUpgradeSubprocessModeEnv) == "" {
		return
	}
	controlEndpoint := strings.TrimSpace(os.Getenv(testComputerUpgradeControlEndpointEnv))
	if controlEndpoint == "" {
		t.Fatal("missing subprocess control endpoint")
	}
	computerUpgradeServiceEndpoint = func(string) string { return controlEndpoint }
	args := []string{"computer", "upgrade"}
	if target := strings.TrimSpace(os.Getenv(testComputerUpgradeTargetEnv)); target != "" {
		args = append(args, "--target-version", target)
	}
	rootCmd.SetArgs(args)
	if err := rootCmd.Execute(); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(2)
	}
}
