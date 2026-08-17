package computer

import (
	"context"
	"encoding/json"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestRaftLocalControlOperationNames(t *testing.T) {
	want := map[string]bool{
		"machine-attestation": true, "restart-service": true, "upgrade-start": true,
		"upgrade-status": true, "upgrade-cancel": true,
		"service-status": true, "service-start": true, "service-stop": true, "service-diagnostics": true,
		"workspace-list": true, "workspace-status": true, "workspace-start": true, "workspace-stop": true,
		"workspace-restart": true, "workspace-attach": true, "workspace-detach": true,
		"workspace-environment": true, "workspace-capacity": true, "workspace-diagnostics": true,
		"runner-attestation": true, "runner-status": true, "runner-start": true, "runner-stop": true,
		"runner-restart": true, "runner-drain": true, "runner-release": true, "runner-ready": true,
	}
	for operation := range want {
		if _, ok := localControlOperationSpecFor(operation); !ok {
			t.Errorf("Raft operation %q is not registered", operation)
		}
	}
}

func TestLocalControlOperationUsesTypedResultAndStructuredError(t *testing.T) {
	if _, ok := localControlOperationSpecFor("/health"); ok {
		t.Fatal("local control operation lookup accepted an HTTP path")
	}
	if got := localControlOperationSpecForMust("service-status").Name; got != "service-status" {
		t.Fatalf("operation name = %q", got)
	}
}

func TestLocalControlRegistryDispatchesTypedHandler(t *testing.T) {
	registry := NewLocalControlRegistry()
	if err := registry.Register("runner-ready", func(_ context.Context, headers map[string]string, args json.RawMessage) (any, error) {
		if headers["X-Test"] != "present" || string(args) != `{"workspace_id":"ws-1"}` {
			t.Fatalf("handler input = headers=%v args=%s", headers, args)
		}
		return map[string]any{"ready": true}, nil
	}); err != nil {
		t.Fatal(err)
	}
	if _, ok := registry.handler("runner-ready"); !ok {
		t.Fatal("registered operation was not dispatchable")
	}
}

func TestLocalControlRegistryRejectsUnknownAndDuplicateOperations(t *testing.T) {
	registry := NewLocalControlRegistry()
	handler := func(context.Context, map[string]string, json.RawMessage) (any, error) { return nil, nil }
	if err := registry.Register("not-real", handler); err == nil {
		t.Fatal("unknown operation was accepted")
	}
	if err := registry.Register("runner-ready", handler); err != nil {
		t.Fatal(err)
	}
	if err := registry.Register("runner-ready", handler); err == nil {
		t.Fatal("duplicate operation was accepted")
	}
}

func TestLocalControlRPCServerReturnsTypedResultWithoutHTTPAdapter(t *testing.T) {
	registry := NewLocalControlRegistry()
	if err := registry.Register("runner-ready", func(_ context.Context, _ map[string]string, _ json.RawMessage) (any, error) {
		return struct {
			Ready bool `json:"ready"`
		}{Ready: true}, nil
	}); err != nil {
		t.Fatal(err)
	}
	left, right := net.Pipe()
	defer left.Close()
	defer right.Close()
	go serveLocalControlRPCConnection(context.Background(), right, registry)
	if err := writeLocalControlFrame(left, localControlRPCMessage{Operation: "runner-ready"}); err != nil {
		t.Fatal(err)
	}
	var response localControlRPCMessage
	if err := readLocalControlFrame(left, &response); err != nil {
		t.Fatal(err)
	}
	if !response.OK || string(response.Result) != `{"ready":true}` {
		t.Fatalf("response = %+v", response)
	}
}

func TestLocalControlClientCallsByOperationName(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/health" || r.Method != http.MethodGet {
			t.Fatalf("request = %s %s", r.Method, r.URL.Path)
		}
		_, _ = w.Write([]byte(`{"status":"running"}`))
	}))
	defer server.Close()
	client, _, err := localControlClientFor(server.URL, 0)
	if err != nil {
		t.Fatal(err)
	}
	var result map[string]string
	if err := client.Call(context.Background(), "service-status", nil, nil, &result); err != nil {
		t.Fatal(err)
	}
	if result["status"] != "running" {
		t.Fatalf("result = %+v", result)
	}
}

func TestLocalControlFrameDispatchesTypedOperationAndArgs(t *testing.T) {
	left, right := net.Pipe()
	defer left.Close()
	defer right.Close()
	go serveLocalControlConnection(right, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost || r.URL.Path != "/workspace-start" {
			t.Errorf("request = %s %s", r.Method, r.URL.Path)
		}
		var args struct {
			WorkspaceID string `json:"workspace_id"`
		}
		if err := json.NewDecoder(r.Body).Decode(&args); err != nil {
			t.Errorf("decode args: %v", err)
		}
		if args.WorkspaceID != "ws-1" {
			t.Errorf("workspace_id = %q", args.WorkspaceID)
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"started":true}`))
	}))

	args, err := json.Marshal(map[string]string{"workspace_id": "ws-1"})
	if err != nil {
		t.Fatal(err)
	}
	if err := writeLocalControlFrame(left, localControlRPCMessage{Operation: "workspace-start", Args: args}); err != nil {
		t.Fatal(err)
	}
	var response localControlRPCMessage
	if err := readLocalControlFrame(left, &response); err != nil {
		t.Fatal(err)
	}
	if !response.OK || string(response.Result) != `{"started":true}` {
		t.Fatalf("response = %+v", response)
	}
}

func TestLocalControlUnknownOperationReturnsStructuredError(t *testing.T) {
	left, right := net.Pipe()
	defer left.Close()
	defer right.Close()
	go serveLocalControlConnection(right, http.NotFoundHandler())
	if err := writeLocalControlFrame(left, localControlRPCMessage{Operation: "not-a-real-operation"}); err != nil {
		t.Fatal(err)
	}
	var response localControlRPCMessage
	if err := readLocalControlFrame(left, &response); err != nil {
		t.Fatal(err)
	}
	if response.OK || response.ErrorCode != "unknown_operation" {
		t.Fatalf("response = %+v", response)
	}
}

func TestLocalControlFrameRejectsOversizedAndMalformedFrames(t *testing.T) {
	if err := writeLocalControlFrame(io.Discard, localControlRPCMessage{Args: make([]byte, localControlMaxFrame)}); err == nil {
		t.Fatal("oversized frame was accepted")
	}
	var malformed [4]byte
	malformed[3] = 1
	if err := readLocalControlFrame(bytesReader(malformed[:]), new(localControlRPCMessage)); err == nil {
		t.Fatal("malformed JSON frame was accepted")
	}
}

type bytesReader []byte

func (r bytesReader) Read(p []byte) (int, error) {
	if len(r) == 0 {
		return 0, io.EOF
	}
	n := copy(p, r)
	return n, nil
}
