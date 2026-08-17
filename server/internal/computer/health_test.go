package computer

import (
	"context"
	"encoding/json"
	"testing"
)

func TestRequestShutdownSendsAuditMetadata(t *testing.T) {
	requests := make(chan map[string]string, 1)
	endpoint := localControlTestServer(t, func(_ context.Context, operation string, headers map[string]string, _ json.RawMessage) (any, error) {
		if operation != "restart-service" {
			t.Fatalf("operation = %q", operation)
		}
		requests <- headers
		return nil, nil
	})

	err := RequestShutdown(endpoint, ShutdownRequest{Source: "desktop", Action: "restart", RequestPID: 8123})
	if err != nil {
		t.Fatal(err)
	}
	headers := <-requests
	if got := headers[shutdownSourceHeader]; got != "desktop" {
		t.Fatalf("shutdown source = %q, want desktop", got)
	}
	if got := headers[shutdownActionHeader]; got != "restart" {
		t.Fatalf("shutdown action = %q, want restart", got)
	}
	if got := headers[shutdownRequestPIDHeader]; got != "8123" {
		t.Fatalf("shutdown request PID = %q, want 8123", got)
	}
}
