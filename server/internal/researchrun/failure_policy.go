package researchrun

import "github.com/multica-ai/multica/server/pkg/taskfailure"

// FailureClass is the research executor's action taxonomy. Agent Inbox keeps
// the provider/runtime-specific source reason; this value decides what the
// Research Run may do next and is persisted on the Attempt.
type FailureClass string

const (
	FailureResearchNegative FailureClass = "research_negative"
	FailureMethodInvalid    FailureClass = "method_invalid"
	FailureContractBlocked  FailureClass = "contract_blocked"
	FailureResultInvalid    FailureClass = "result_invalid"
	FailurePermission       FailureClass = "permission"
	FailureCredential       FailureClass = "credential"
	FailureRateLimited      FailureClass = "rate_limited"
	FailureNetwork          FailureClass = "network"
	FailureTimeout          FailureClass = "timeout"
	FailureTool             FailureClass = "tool_failure"
	FailureProvider         FailureClass = "provider_failure"
	FailureRuntimeLost      FailureClass = "runtime_lost"
	FailureConfiguration    FailureClass = "configuration"
	FailureCapability       FailureClass = "capability_unavailable"
	FailureTargetChanged    FailureClass = "target_changed"
	FailureInternal         FailureClass = "internal_invariant"
	FailureUnknown          FailureClass = "unknown"
)

type CircuitScope string

const (
	CircuitNone     CircuitScope = ""
	CircuitAgent    CircuitScope = "agent"
	CircuitRuntime  CircuitScope = "runtime"
	CircuitProvider CircuitScope = "provider"
	CircuitAdapter  CircuitScope = "adapter"
)

type RepairKind string

const (
	RepairNone                 RepairKind = ""
	RepairWaitForTarget        RepairKind = "wait_for_target"
	RepairRetryTarget          RepairKind = "retry_target"
	RepairRerouteTarget        RepairKind = "reroute_target"
	RepairFreshSession         RepairKind = "fresh_session"
	RepairRequestConfiguration RepairKind = "request_configuration"
	RepairRequestDecision      RepairKind = "request_decision"
)

type FailureDisposition struct {
	Class         FailureClass
	Retryable     bool
	MaxAttempts   int
	CircuitScope  CircuitScope
	ImmediateOpen bool
	Repair        RepairKind
}

func failureDisposition(class FailureClass) FailureDisposition {
	switch class {
	case FailureRuntimeLost:
		return FailureDisposition{Class: class, Retryable: true, MaxAttempts: 3, CircuitScope: CircuitRuntime, Repair: RepairWaitForTarget}
	case FailureTimeout:
		return FailureDisposition{Class: class, Retryable: true, MaxAttempts: 3, CircuitScope: CircuitAgent, Repair: RepairRetryTarget}
	case FailureRateLimited:
		return FailureDisposition{Class: class, Retryable: true, MaxAttempts: 4, CircuitScope: CircuitProvider, Repair: RepairWaitForTarget}
	case FailureProvider, FailureNetwork:
		return FailureDisposition{Class: class, Retryable: true, MaxAttempts: 3, CircuitScope: CircuitProvider, Repair: RepairRerouteTarget}
	case FailureCredential:
		return FailureDisposition{Class: class, MaxAttempts: 1, CircuitScope: CircuitProvider, ImmediateOpen: true, Repair: RepairRequestConfiguration}
	case FailureConfiguration:
		return FailureDisposition{Class: class, MaxAttempts: 1, CircuitScope: CircuitAgent, ImmediateOpen: true, Repair: RepairRequestConfiguration}
	case FailureTool:
		return FailureDisposition{Class: class, Retryable: true, MaxAttempts: 2, CircuitScope: CircuitAgent, Repair: RepairRerouteTarget}
	case FailureResultInvalid:
		return FailureDisposition{Class: class, MaxAttempts: 1, Repair: RepairFreshSession}
	case FailureContractBlocked, FailurePermission, FailureCapability:
		return FailureDisposition{Class: class, MaxAttempts: 1, Repair: RepairRequestDecision}
	case FailureTargetChanged:
		return FailureDisposition{Class: class, Retryable: true, MaxAttempts: 2, Repair: RepairRerouteTarget}
	case FailureInternal:
		return FailureDisposition{Class: class, MaxAttempts: 1, CircuitScope: CircuitAdapter, ImmediateOpen: true}
	case FailureUnknown:
		return FailureDisposition{Class: class, Retryable: true, MaxAttempts: 2, CircuitScope: CircuitAgent, Repair: RepairRetryTarget}
	default:
		return FailureDisposition{Class: class, MaxAttempts: 1}
	}
}

// ClassifyInboxFailure maps the durable Agent Inbox reason to the executor
// policy. runtimeRetryable is an additional terminal fact (for example the
// Agent already emitted user-visible output) and may only remove retryability.
func ClassifyInboxFailure(reason string, runtimeRetryable bool) FailureDisposition {
	class := FailureUnknown
	switch taskfailure.Reason(reason) {
	case taskfailure.ReasonQueuedExpired, taskfailure.ReasonRuntimeOffline, taskfailure.ReasonRuntimeRecovery:
		class = FailureRuntimeLost
	case taskfailure.ReasonTimeout, taskfailure.ReasonAgentTimeout:
		class = FailureTimeout
	case taskfailure.ReasonAgentProviderCapacityOrRateLimit:
		class = FailureRateLimited
	case taskfailure.ReasonAgentProviderServerError:
		class = FailureProvider
	case taskfailure.ReasonAgentProviderNetwork:
		class = FailureNetwork
	case taskfailure.ReasonAgentProviderAuthOrAccess, taskfailure.ReasonAgentProviderQuotaLimit:
		class = FailureCredential
	case taskfailure.ReasonAgentMissingConfig, taskfailure.ReasonAgentModelNotFoundOrUnavailable:
		class = FailureConfiguration
	case taskfailure.ReasonAgentRuntimeVersionUnsupported, taskfailure.ReasonAgentRuntimeMissingExecutable:
		class = FailureTool
	case taskfailure.ReasonAgentProcessFailure:
		class = FailureTool
	case taskfailure.ReasonAPIInvalidRequest, taskfailure.ReasonAgentContextOverflow, taskfailure.ReasonAgentEmptyOrUnparseableOutput:
		class = FailureResultInvalid
	case taskfailure.ReasonIterationLimit, taskfailure.ReasonAgentBlocked:
		class = FailureContractBlocked
	}
	disposition := failureDisposition(class)
	if !runtimeRetryable {
		disposition.Retryable = false
	}
	// Runtime installation/version defects cannot be repaired by another
	// attempt on the same Runtime and therefore open that target immediately.
	if taskfailure.Reason(reason) == taskfailure.ReasonAgentRuntimeVersionUnsupported || taskfailure.Reason(reason) == taskfailure.ReasonAgentRuntimeMissingExecutable {
		disposition.Retryable = false
		disposition.CircuitScope = CircuitRuntime
		disposition.ImmediateOpen = true
		disposition.Repair = RepairRequestConfiguration
	}
	// A provider quota lock has a reset-time wait action, while auth/access
	// requires a configuration change. Both remain credential failures.
	if taskfailure.Reason(reason) == taskfailure.ReasonAgentProviderQuotaLimit {
		disposition.Repair = RepairWaitForTarget
	}
	return disposition
}
