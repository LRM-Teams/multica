package researchrun

import (
	"errors"
	"testing"

	"github.com/multica-ai/multica/server/pkg/taskfailure"
)

func TestClassifyInboxFailureUsesCanonicalTaskFailureReasons(t *testing.T) {
	tests := []struct {
		name          string
		reason        taskfailure.Reason
		class         FailureClass
		retryable     bool
		scope         CircuitScope
		immediateOpen bool
		repair        RepairKind
	}{
		{"runtime offline", taskfailure.ReasonRuntimeOffline, FailureRuntimeLost, true, CircuitRuntime, false, RepairWaitForTarget},
		{"runtime recovery", taskfailure.ReasonRuntimeRecovery, FailureRuntimeLost, true, CircuitRuntime, false, RepairWaitForTarget},
		{"timeout", taskfailure.ReasonTimeout, FailureTimeout, true, CircuitAgent, false, RepairRetryTarget},
		{"provider rate limit", taskfailure.ReasonAgentProviderCapacityOrRateLimit, FailureRateLimited, true, CircuitProvider, false, RepairWaitForTarget},
		{"provider server", taskfailure.ReasonAgentProviderServerError, FailureProvider, true, CircuitProvider, false, RepairRerouteTarget},
		{"provider network", taskfailure.ReasonAgentProviderNetwork, FailureNetwork, true, CircuitProvider, false, RepairRerouteTarget},
		{"auth", taskfailure.ReasonAgentProviderAuthOrAccess, FailureCredential, false, CircuitProvider, true, RepairRequestConfiguration},
		{"quota", taskfailure.ReasonAgentProviderQuotaLimit, FailureCredential, false, CircuitProvider, true, RepairWaitForTarget},
		{"missing config", taskfailure.ReasonAgentMissingConfig, FailureConfiguration, false, CircuitAgent, true, RepairRequestConfiguration},
		{"model unavailable", taskfailure.ReasonAgentModelNotFoundOrUnavailable, FailureConfiguration, false, CircuitAgent, true, RepairRequestConfiguration},
		{"runtime binary", taskfailure.ReasonAgentRuntimeMissingExecutable, FailureTool, false, CircuitRuntime, true, RepairRequestConfiguration},
		{"context overflow", taskfailure.ReasonAgentContextOverflow, FailureResultInvalid, false, CircuitNone, false, RepairFreshSession},
		{"invalid request", taskfailure.ReasonAPIInvalidRequest, FailureResultInvalid, false, CircuitNone, false, RepairFreshSession},
		{"blocked", taskfailure.ReasonAgentBlocked, FailureContractBlocked, false, CircuitNone, false, RepairRequestDecision},
		{"unknown", taskfailure.ReasonAgentUnknown, FailureUnknown, true, CircuitAgent, false, RepairRetryTarget},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got := ClassifyInboxFailure(tc.reason.String(), true)
			if got.Class != tc.class || got.Retryable != tc.retryable || got.CircuitScope != tc.scope || got.ImmediateOpen != tc.immediateOpen || got.Repair != tc.repair {
				t.Fatalf("classification=%+v", got)
			}
		})
	}
}

func TestClassifyInboxFailureRespectsRuntimeNonRetryableFact(t *testing.T) {
	got := ClassifyInboxFailure(taskfailure.ReasonRuntimeOffline.String(), false)
	if got.Retryable {
		t.Fatalf("runtime terminal fact was overridden: %+v", got)
	}
}

func TestClassifyInboxFailureDoesNotGuessUnknownString(t *testing.T) {
	got := ClassifyInboxFailure("new_provider_copy_not_in_taxonomy", true)
	if got.Class != FailureUnknown || got.CircuitScope != CircuitAgent || got.MaxAttempts != 2 {
		t.Fatalf("unknown classification=%+v", got)
	}
}

func TestClassifiedDispatchErrorCarriesPolicy(t *testing.T) {
	cause := errors.New("runtime is not configured")
	err := NewDispatchFailure(cause, FailureConfiguration, false)
	if !errors.Is(err, cause) {
		t.Fatalf("cause was not preserved: %v", err)
	}
	got := DispatchFailurePolicy(err)
	if got.Class != FailureConfiguration || got.Retryable || got.CircuitScope != CircuitAgent || !got.ImmediateOpen {
		t.Fatalf("dispatch policy=%+v", got)
	}
}

func TestUnclassifiedDispatchErrorStaysUnknownWithSmallBudget(t *testing.T) {
	got := DispatchFailurePolicy(errors.New("opaque adapter error"))
	if got.Class != FailureUnknown || !got.Retryable || got.MaxAttempts != 2 {
		t.Fatalf("dispatch policy=%+v", got)
	}
}
