package daemon

import (
	"errors"
	"net/http"
	"testing"

	"github.com/multica-ai/multica/server/pkg/agent"
	"github.com/multica-ai/multica/server/pkg/protocol"
)

func TestClassifyManagedAgentStartFailureStopsDeterministicRetries(t *testing.T) {
	tests := []struct {
		name   string
		err    error
		reason string
	}{
		{name: "provider login", err: errors.New("cursor ACP authenticate: login required"), reason: "provider_auth_required"},
		{name: "credential conflict", err: &requestError{StatusCode: http.StatusConflict}, reason: "launch_credential_conflict"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			failure := classifyManagedAgentStartFailure(test.err)
			if failure == nil || failure.RetryClass != protocol.AgentStartRetryTerminal || failure.ReasonCode != test.reason {
				t.Fatalf("classification=%+v, want terminal %s", failure, test.reason)
			}
		})
	}
	failure := classifyManagedAgentStartFailure(errors.New(agent.ProviderAuthRequiredMarker))
	if failure == nil || failure.RetryClass != protocol.AgentStartRetryTerminal || failure.ReasonCode != "provider_auth_required" {
		t.Fatalf("provider auth marker classification=%+v, want terminal provider_auth_required", failure)
	}
}

func TestClassifyManagedAgentStartFailureRetriesTransportFailures(t *testing.T) {
	failure := classifyManagedAgentStartFailure(&requestError{StatusCode: http.StatusBadGateway})
	if failure == nil || failure.RetryClass != protocol.AgentStartRetryTransient {
		t.Fatalf("classification=%+v, want transient", failure)
	}
}
