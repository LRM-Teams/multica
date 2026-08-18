package computer

import (
	"context"
	"encoding/json"
	"io"
	"net"
	"testing"
)

func TestRaftLocalControlOperationNames(t *testing.T) {
	want := map[string]bool{
		LocalControlRestartServiceOperation: true, LocalControlUpgradeStartOperation: true,
		LocalControlUpgradeStatusOperation: true, LocalControlUpgradeCancelOperation: true,
		LocalControlServiceStatusOperation: true, "service:start": true, "service:stop": true, "service:diagnostics": true,
		"workspace:list": true, "workspace:status": true, "workspace:start": true, "workspace:stop": true,
		"workspace:restart": true, "workspace:attach": true, "workspace:detach": true,
		LocalControlWorkspaceEnvironmentOperation: true, LocalControlWorkspaceCapacityOperation: true, LocalControlWorkspaceDiagnosticsOperation: true,
		LocalControlComputerControlOperation: true, "runner:start": true, "runner:stop": true,
		"runner:restart": true, LocalControlRunnerDrainOperation: true, LocalControlRunnerReleaseOperation: true, LocalControlRunnerReadyOperation: true,
		LocalControlWorkDigestOperation: true, LocalControlWorkJournalOperation: true,
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
	if got := localControlOperationSpecForMust(LocalControlServiceStatusOperation).Name; got != LocalControlServiceStatusOperation {
		t.Fatalf("operation name = %q", got)
	}
}

func TestLocalControlRegistryDispatchesTypedHandler(t *testing.T) {
	registry := NewLocalControlRegistry()
	if err := registry.Register(LocalControlRunnerReadyOperation, func(_ context.Context, headers map[string]string, args json.RawMessage) (any, error) {
		if headers["X-Test"] != "present" || string(args) != `{"workspaceId":"ws-1"}` {
			t.Fatalf("handler input = headers=%v args=%s", headers, args)
		}
		return map[string]any{"ready": true}, nil
	}); err != nil {
		t.Fatal(err)
	}
	if _, ok := registry.handler(LocalControlRunnerReadyOperation); !ok {
		t.Fatal("registered operation was not dispatchable")
	}
}

func TestLocalControlRPCMessageJSONUsesCamelCaseErrors(t *testing.T) {
	raw, err := json.Marshal(localControlRPCMessage{ErrorCode: "busy", ErrorMessage: "try later"})
	if err != nil {
		t.Fatal(err)
	}
	if got := string(raw); got != `{"ok":false,"errorCode":"busy","errorMessage":"try later"}` {
		t.Fatalf("RPC error JSON = %s", got)
	}
}

func TestLocalControlRegistryRejectsUnknownAndDuplicateOperations(t *testing.T) {
	registry := NewLocalControlRegistry()
	handler := func(context.Context, map[string]string, json.RawMessage) (any, error) { return nil, nil }
	if err := registry.Register("not-real", handler); err == nil {
		t.Fatal("unknown operation was accepted")
	}
	if err := registry.Register(LocalControlRunnerReadyOperation, handler); err != nil {
		t.Fatal(err)
	}
	if err := registry.Register(LocalControlRunnerReadyOperation, handler); err == nil {
		t.Fatal("duplicate operation was accepted")
	}
}

func TestLocalControlRPCServerReturnsTypedResultWithoutHTTPAdapter(t *testing.T) {
	registry := NewLocalControlRegistry()
	if err := registry.Register(LocalControlRunnerReadyOperation, func(_ context.Context, _ map[string]string, _ json.RawMessage) (any, error) {
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
	if err := writeLocalControlFrame(left, localControlRPCMessage{Operation: LocalControlRunnerReadyOperation}); err != nil {
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

func TestLocalControlUnknownOperationReturnsStructuredError(t *testing.T) {
	left, right := net.Pipe()
	defer left.Close()
	defer right.Close()
	go serveLocalControlRPCConnection(context.Background(), right, NewLocalControlRegistry())
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
