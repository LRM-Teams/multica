package daemon

import (
	"context"
	"errors"
	"net/http"
	"strings"

	"github.com/multica-ai/multica/server/pkg/agent"
	"github.com/multica-ai/multica/server/pkg/protocol"
)

func classifyManagedAgentStartFailure(err error) *protocol.AgentStartFailure {
	if err == nil || errors.Is(err, errManagedAgentStartStopped) {
		return nil
	}
	failure := &protocol.AgentStartFailure{
		ReasonCode: "provider_unavailable", RetryClass: protocol.AgentStartRetryTransient,
	}
	var requestErr *requestError
	if errors.As(err, &requestErr) {
		switch {
		case requestErr.StatusCode == http.StatusRequestTimeout,
			requestErr.StatusCode == http.StatusTooManyRequests,
			requestErr.StatusCode >= http.StatusInternalServerError:
			return failure
		case requestErr.StatusCode == http.StatusConflict:
			failure.ReasonCode = "launch_credential_conflict"
			failure.RetryClass = protocol.AgentStartRetryTerminal
			return failure
		case requestErr.StatusCode >= http.StatusBadRequest:
			failure.ReasonCode = "provider_configuration_invalid"
			failure.RetryClass = protocol.AgentStartRetryTerminal
			return failure
		}
	}
	message := strings.ToLower(err.Error())
	if strings.Contains(message, agent.ProviderAuthRequiredMarker) ||
		strings.Contains(message, "authenticate") || strings.Contains(message, "authentication") ||
		strings.Contains(message, "login") || strings.Contains(message, "not logged in") {
		failure.ReasonCode = "provider_auth_required"
		failure.RetryClass = protocol.AgentStartRetryTerminal
		return failure
	}
	if errors.Is(err, context.DeadlineExceeded) || strings.Contains(message, "timeout") || strings.Contains(message, "temporarily unavailable") {
		return failure
	}
	return failure
}
