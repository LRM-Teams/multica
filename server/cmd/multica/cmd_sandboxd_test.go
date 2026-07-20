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
