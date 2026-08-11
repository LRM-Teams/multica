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
	"testing"

	"github.com/multica-ai/multica/server/internal/cli"
)

const updateSubprocessModeEnv = "MULTICA_TEST_UPDATE_SUBPROCESS_MODE"

type updateSubprocessRequest struct {
	RequestID     string `json:"request_id"`
	TargetVersion string `json:"target_version"`
}

func TestUpdateSubprocessLiveOwnerRoutesWithoutReadingReleaseFeed(t *testing.T) {
	home := t.TempDir()
	controlDir := filepath.Join(home, ".multica", "computer")
	if err := os.MkdirAll(controlDir, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(controlDir, machineUpgradeControlTokenFile), []byte("owner-secret\n"), 0o600); err != nil {
		t.Fatal(err)
	}

	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen on Computer control port: %v", err)
	}
	defer listener.Close()
	controlPort := listener.Addr().(*net.TCPAddr).Port

	requestSeen := make(chan updateSubprocessRequest, 2)
	mux := http.NewServeMux()
	mux.HandleFunc("/health", func(w http.ResponseWriter, _ *http.Request) {
		_ = json.NewEncoder(w).Encode(map[string]any{"status": "running", "daemon_id": "daemon-1"})
	})
	mux.HandleFunc("/machine-upgrades", func(w http.ResponseWriter, r *http.Request) {
		if got := r.Header.Get("X-Multica-Control-Token"); got != "owner-secret" {
			http.Error(w, "bad owner token", http.StatusUnauthorized)
			return
		}
		var request updateSubprocessRequest
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

	for attempt := 0; attempt < 2; attempt++ {
		output, err := runUpdateSubprocess(t, home, controlPort, "http://127.0.0.1:1/unreachable")
		if err != nil {
			t.Fatalf("multica update subprocess replay %d: %v\n%s", attempt+1, err, output)
		}
		if !strings.Contains(string(output), "live daemon owns staging and handoff") {
			t.Fatalf("subprocess output = %q, want live-owner confirmation", output)
		}
	}

	for attempt := 0; attempt < 2; attempt++ {
		request := <-requestSeen
		if request.RequestID != "same-request" || request.TargetVersion != "latest" {
			t.Fatalf("canonical request = %+v, want same-request/latest", request)
		}
	}
}

func TestUpdateSubprocessAbsentDaemonInstallsForNextStart(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("the Windows adapter is cross-compiled separately; this subprocess fixture is a POSIX script")
	}
	home := t.TempDir()
	feed := newUpdateSubprocessReleaseFeed(t, "v1.2.3")
	defer feed.Close()
	controlPort := unusedUpdateControlPort(t)

	child := exec.Command(os.Args[0], "-test.run=^TestUpdateSubprocessHelper$")
	child.Env = append(os.Environ(),
		"HOME="+home,
		updateSubprocessModeEnv+"=offline",
		"MULTICA_TEST_UPDATE_CONTROL_PORT="+strconv.Itoa(controlPort),
		"MULTICA_RELEASE_MANIFEST_BASE_URL="+feed.URL,
	)
	output, err := child.CombinedOutput()
	if err != nil {
		t.Fatalf("offline multica update subprocess: %v\n%s", err, output)
	}
	if !strings.Contains(string(output), "Installed v1.2.3 for the next daemon start") ||
		!strings.Contains(string(output), "No running successor was proven") {
		t.Fatalf("offline output = %q, want precise next-start/no-successor wording", output)
	}

	store, err := cli.OpenVersionStore(filepath.Join(home, ".local", "share", "multica"))
	if err != nil {
		t.Fatal(err)
	}
	state, err := store.ReadActivationState()
	if err != nil {
		t.Fatal(err)
	}
	if state.ActiveVersion != "v1.2.3" || state.Generation != 1 {
		t.Fatalf("offline Active = %+v, want v1.2.3 generation 1", state)
	}
}

func TestUpdateSubprocessLiveOwnerFailuresNeverMutateVersionStore(t *testing.T) {
	tests := []struct {
		name       string
		statusCode int
		body       string
		want       string
	}{
		{name: "authentication failure", statusCode: http.StatusUnauthorized, body: "local control authentication failed", want: "upgrade_service_unreachable"},
		{name: "distinct mutation conflict", statusCode: http.StatusConflict, body: "upgrade_already_in_progress", want: "machine upgrade request rejected: upgrade_already_in_progress"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			home := t.TempDir()
			writeUpdateControlToken(t, home, "wrong-owner-secret")
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

			output, err := runUpdateSubprocess(t, home, controlPort, "http://127.0.0.1:1/unreachable")
			if err == nil || !strings.Contains(string(output), tt.want) {
				t.Fatalf("subprocess error = %v output = %q, want %q", err, output, tt.want)
			}
			assertUpdateVersionStoreUnchanged(t, home)
		})
	}
}

func TestUpdateSubprocessLivePIDWithUnavailableControlFailsClosed(t *testing.T) {
	home := t.TempDir()
	pidDir := filepath.Join(home, ".multica", "computer")
	if err := os.MkdirAll(pidDir, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(pidDir, "daemon.pid"), []byte("4242\n"), 0o600); err != nil {
		t.Fatal(err)
	}

	output, err := runUpdateSubprocess(t, home, unusedUpdateControlPort(t), "http://127.0.0.1:1/unreachable")
	if err == nil || !strings.Contains(string(output), "upgrade_service_unreachable") ||
		!strings.Contains(string(output), "refusing offline activation") {
		t.Fatalf("subprocess error = %v output = %q, want fail-closed unavailable owner", err, output)
	}
	assertUpdateVersionStoreUnchanged(t, home)
}

func TestUpdateSubprocessDistinctConcurrentMutationGetsStableConflict(t *testing.T) {
	home := t.TempDir()
	writeUpdateControlToken(t, home, "owner-secret")
	firstEntered := make(chan struct{})
	secondRejected := make(chan struct{})
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/health":
			_ = json.NewEncoder(w).Encode(map[string]any{"status": "running", "daemon_id": "daemon-1"})
		case "/machine-upgrades":
			var request updateSubprocessRequest
			if err := json.NewDecoder(r.Body).Decode(&request); err != nil {
				http.Error(w, err.Error(), http.StatusBadRequest)
				return
			}
			if request.RequestID == "request-a" {
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
		output, err := newUpdateSubprocessCommand(home, controlPort, "http://127.0.0.1:1/unreachable", "request-a").CombinedOutput()
		firstResult <- result{output: output, err: err}
	}()
	<-firstEntered
	secondOutput, secondErr := newUpdateSubprocessCommand(home, controlPort, "http://127.0.0.1:1/unreachable", "request-b").CombinedOutput()
	first := <-firstResult
	if first.err != nil || !strings.Contains(string(first.output), "live daemon owns staging and handoff") {
		t.Fatalf("first mutation = %v output %q, want canonical success", first.err, first.output)
	}
	if secondErr == nil || !strings.Contains(string(secondOutput), "machine upgrade request rejected: upgrade_already_in_progress") {
		t.Fatalf("second mutation = %v output %q, want stable conflict", secondErr, secondOutput)
	}
	assertUpdateVersionStoreUnchanged(t, home)
}

func runUpdateSubprocess(t *testing.T, home string, controlPort int, releaseBaseURL string) ([]byte, error) {
	t.Helper()
	return newUpdateSubprocessCommand(home, controlPort, releaseBaseURL, "same-request").CombinedOutput()
}

func newUpdateSubprocessCommand(home string, controlPort int, releaseBaseURL, requestID string) *exec.Cmd {
	child := exec.Command(os.Args[0], "-test.run=^TestUpdateSubprocessHelper$")
	child.Env = append(os.Environ(),
		"HOME="+home,
		updateSubprocessModeEnv+"=run",
		"MULTICA_TEST_UPDATE_CONTROL_PORT="+strconv.Itoa(controlPort),
		"MULTICA_TEST_UPDATE_REQUEST_ID="+requestID,
		"MULTICA_RELEASE_MANIFEST_BASE_URL="+releaseBaseURL,
	)
	return child
}

func writeUpdateControlToken(t *testing.T, home, token string) {
	t.Helper()
	controlDir := filepath.Join(home, ".multica", "computer")
	if err := os.MkdirAll(controlDir, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(controlDir, machineUpgradeControlTokenFile), []byte(token+"\n"), 0o600); err != nil {
		t.Fatal(err)
	}
}

func assertUpdateVersionStoreUnchanged(t *testing.T, home string) {
	t.Helper()
	root := filepath.Join(home, ".local", "share", "multica")
	if _, err := os.Stat(root); !os.IsNotExist(err) {
		t.Fatalf("VersionStore changed on failed live-owner request: stat %s: %v", root, err)
	}
}

func newUpdateSubprocessReleaseFeed(t *testing.T, tag string) *httptest.Server {
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

func unusedUpdateControlPort(t *testing.T) int {
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

func TestUpdateSubprocessHelper(t *testing.T) {
	if os.Getenv(updateSubprocessModeEnv) == "" {
		return
	}
	controlPort, err := strconv.Atoi(os.Getenv("MULTICA_TEST_UPDATE_CONTROL_PORT"))
	if err != nil || controlPort <= 0 {
		t.Fatalf("invalid subprocess control port: %v", err)
	}
	updateComputerHealthPort = func(string) int { return controlPort }
	requestID := strings.TrimSpace(os.Getenv("MULTICA_TEST_UPDATE_REQUEST_ID"))
	if requestID == "" {
		requestID = "same-request"
	}
	rootCmd.SetArgs([]string{"update", "--request-id", requestID})
	if err := rootCmd.Execute(); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(2)
	}
}
