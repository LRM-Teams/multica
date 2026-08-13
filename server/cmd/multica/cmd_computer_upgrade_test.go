package main

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
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
)

const computerUpgradeSubprocessModeEnv = "MULTICA_TEST_COMPUTER_UPGRADE_SUBPROCESS_MODE"
const testComputerControlTokenFile = "machine-upgrade-control.token"
const testComputerUpgradeTargetEnv = "MULTICA_TEST_COMPUTER_UPGRADE_TARGET"
const testComputerUpgradeControlPortEnv = "MULTICA_TEST_COMPUTER_UPGRADE_CONTROL_PORT"

type computerUpgradeSubprocessRequest struct {
	RequestID     string `json:"request_id"`
	TargetVersion string `json:"target_version"`
}

func TestComputerUpgradeSubprocessLiveOwnerRoutesWithoutReadingReleaseFeed(t *testing.T) {
	home := t.TempDir()
	controlDir := filepath.Join(home, ".multica", "computer")
	if err := os.MkdirAll(controlDir, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(controlDir, testComputerControlTokenFile), []byte("owner-secret\n"), 0o600); err != nil {
		t.Fatal(err)
	}

	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen on Computer control port: %v", err)
	}
	defer listener.Close()
	controlPort := listener.Addr().(*net.TCPAddr).Port

	requestSeen := make(chan computerUpgradeSubprocessRequest, 2)
	mux := http.NewServeMux()
	mux.HandleFunc("/health", func(w http.ResponseWriter, _ *http.Request) {
		_ = json.NewEncoder(w).Encode(map[string]any{"status": "running", "daemon_id": "daemon-1"})
	})
	mux.HandleFunc("/machine-upgrades", func(w http.ResponseWriter, r *http.Request) {
		if got := r.Header.Get("X-Multica-Control-Token"); got != "owner-secret" {
			http.Error(w, "bad owner token", http.StatusUnauthorized)
			return
		}
		var request computerUpgradeSubprocessRequest
		if err := json.NewDecoder(r.Body).Decode(&request); err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}
		requestSeen <- request
		_ = json.NewEncoder(w).Encode(map[string]any{"id": "upgrade-1", "phase": "queued"})
	})
	server := &http.Server{Handler: mux}
	go func() { _ = server.Serve(listener) }()
	defer server.Close()

	targets := []struct {
		arg  string
		want string
	}{{want: "latest"}, {arg: "1.2.3", want: "v1.2.3"}}
	for attempt, target := range targets {
		output, err := runComputerUpgradeSubprocessWithTarget(t, home, controlPort, "http://127.0.0.1:1/unreachable", target.arg)
		if err != nil {
			t.Fatalf("multica computer upgrade subprocess %d: %v\n%s", attempt+1, err, output)
		}
		if !strings.Contains(string(output), "live Computer owns download, verification, handoff, and convergence") {
			t.Fatalf("subprocess output = %q, want live-owner confirmation", output)
		}
	}

	for attempt, target := range targets {
		request := <-requestSeen
		if strings.TrimSpace(request.RequestID) == "" || request.TargetVersion != target.want {
			t.Fatalf("canonical request %d = %+v, want generated request ID and target %s", attempt+1, request, target.want)
		}
	}
}

func TestComputerUpgradeSubprocessAbsentResidentInstallsForNextStart(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("the Windows adapter is cross-compiled separately; this subprocess fixture is a POSIX script")
	}
	home := t.TempDir()
	feed := newComputerUpgradeSubprocessReleaseFeed(t, "v1.2.3")
	defer feed.Close()
	controlPort := unusedComputerUpgradeControlPort(t)

	child := exec.Command(os.Args[0], "-test.run=^TestComputerUpgradeSubprocessHelper$")
	child.Env = append(os.Environ(),
		"HOME="+home,
		computerUpgradeSubprocessModeEnv+"=offline",
		testComputerUpgradeControlPortEnv+"="+strconv.Itoa(controlPort),
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
	tests := []struct {
		name       string
		statusCode int
		body       string
		want       string
	}{
		{name: "authentication failure", statusCode: http.StatusUnauthorized, body: "local control authentication failed", want: "upgrade_service_unreachable"},
		{name: "distinct mutation conflict", statusCode: http.StatusConflict, body: "upgrade_already_in_progress", want: "machine upgrade request rejected: upgrade_already_in_progress"},
		{name: "malformed canonical response", statusCode: http.StatusOK, body: "{}", want: "response is missing operation id"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			home := t.TempDir()
			writeComputerUpgradeControlToken(t, home, "wrong-owner-secret")
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				switch r.URL.Path {
				case "/health":
					_ = json.NewEncoder(w).Encode(map[string]any{"status": "running", "daemon_id": "daemon-1"})
				case "/machine-upgrades":
					http.Error(w, tt.body, tt.statusCode)
				default:
					http.NotFound(w, r)
				}
			}))
			defer server.Close()
			controlPort := server.Listener.Addr().(*net.TCPAddr).Port

			output, err := runComputerUpgradeSubprocess(t, home, controlPort, "http://127.0.0.1:1/unreachable")
			if err == nil || !strings.Contains(string(output), tt.want) {
				t.Fatalf("subprocess error = %v output = %q, want %q", err, output, tt.want)
			}
			assertComputerUpgradeVersionStoreUnchanged(t, home)
		})
	}
}

func TestComputerUpgradeSubprocessLivePIDWithUnavailableControlFailsClosed(t *testing.T) {
	home := t.TempDir()
	pidDir := filepath.Join(home, ".multica", "computer")
	if err := os.MkdirAll(pidDir, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(pidDir, "daemon.pid"), []byte(strconv.Itoa(os.Getpid())+"\n"), 0o600); err != nil {
		t.Fatal(err)
	}

	output, err := runComputerUpgradeSubprocess(t, home, unusedComputerUpgradeControlPort(t), "http://127.0.0.1:1/unreachable")
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
	pidDir := filepath.Join(home, ".multica", "computer")
	if err := os.MkdirAll(pidDir, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(pidDir, "daemon.pid"), []byte("2147483647\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	feed := newComputerUpgradeSubprocessReleaseFeed(t, "v1.2.3")
	defer feed.Close()

	output, err := runComputerUpgradeSubprocess(t, home, unusedComputerUpgradeControlPort(t), feed.URL)
	if err != nil {
		t.Fatalf("offline fallback with stale PID: %v\n%s", err, output)
	}
	if !strings.Contains(string(output), "Installed v1.2.3 for the next Computer start") {
		t.Fatalf("stale-PID fallback output = %q", output)
	}
}

func TestComputerUpgradeSubprocessDistinctConcurrentMutationGetsStableConflict(t *testing.T) {
	home := t.TempDir()
	writeComputerUpgradeControlToken(t, home, "owner-secret")
	firstEntered := make(chan struct{})
	secondRejected := make(chan struct{})
	var requestCount atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/health":
			_ = json.NewEncoder(w).Encode(map[string]any{"status": "running", "daemon_id": "daemon-1"})
		case "/machine-upgrades":
			var request computerUpgradeSubprocessRequest
			if err := json.NewDecoder(r.Body).Decode(&request); err != nil {
				http.Error(w, err.Error(), http.StatusBadRequest)
				return
			}
			if requestCount.Add(1) == 1 {
				close(firstEntered)
				<-secondRejected
				_ = json.NewEncoder(w).Encode(map[string]any{"id": "upgrade-a", "phase": "queued"})
				return
			}
			http.Error(w, "upgrade_already_in_progress", http.StatusConflict)
			close(secondRejected)
		default:
			http.NotFound(w, r)
		}
	}))
	defer server.Close()
	controlPort := server.Listener.Addr().(*net.TCPAddr).Port

	type result struct {
		output []byte
		err    error
	}
	firstResult := make(chan result, 1)
	go func() {
		output, err := newComputerUpgradeSubprocessCommand(home, controlPort, "http://127.0.0.1:1/unreachable").CombinedOutput()
		firstResult <- result{output: output, err: err}
	}()
	<-firstEntered
	secondOutput, secondErr := newComputerUpgradeSubprocessCommand(home, controlPort, "http://127.0.0.1:1/unreachable").CombinedOutput()
	first := <-firstResult
	if first.err != nil || !strings.Contains(string(first.output), "live Computer owns download, verification, handoff, and convergence") {
		t.Fatalf("first mutation = %v output %q, want canonical success", first.err, first.output)
	}
	if secondErr == nil || !strings.Contains(string(secondOutput), "machine upgrade request rejected: upgrade_already_in_progress") {
		t.Fatalf("second mutation = %v output %q, want stable conflict", secondErr, secondOutput)
	}
	assertComputerUpgradeVersionStoreUnchanged(t, home)
}

func runComputerUpgradeSubprocess(t *testing.T, home string, controlPort int, releaseBaseURL string) ([]byte, error) {
	t.Helper()
	return runComputerUpgradeSubprocessWithTarget(t, home, controlPort, releaseBaseURL, "")
}

func runComputerUpgradeSubprocessWithTarget(t *testing.T, home string, controlPort int, releaseBaseURL, target string) ([]byte, error) {
	t.Helper()
	command := newComputerUpgradeSubprocessCommand(home, controlPort, releaseBaseURL)
	if target != "" {
		command.Env = append(command.Env, testComputerUpgradeTargetEnv+"="+target)
	}
	return command.CombinedOutput()
}

func newComputerUpgradeSubprocessCommand(home string, controlPort int, releaseBaseURL string) *exec.Cmd {
	child := exec.Command(os.Args[0], "-test.run=^TestComputerUpgradeSubprocessHelper$")
	child.Env = append(os.Environ(),
		"HOME="+home,
		computerUpgradeSubprocessModeEnv+"=run",
		testComputerUpgradeControlPortEnv+"="+strconv.Itoa(controlPort),
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

func unusedComputerUpgradeControlPort(t *testing.T) int {
	t.Helper()
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	port := listener.Addr().(*net.TCPAddr).Port
	if err := listener.Close(); err != nil {
		t.Fatal(err)
	}
	return port
}

func TestComputerUpgradeSubprocessHelper(t *testing.T) {
	if os.Getenv(computerUpgradeSubprocessModeEnv) == "" {
		return
	}
	controlPort, err := strconv.Atoi(os.Getenv(testComputerUpgradeControlPortEnv))
	if err != nil || controlPort <= 0 {
		t.Fatalf("invalid subprocess control port: %v", err)
	}
	computerUpgradeControlPort = func(string) int { return controlPort }
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
