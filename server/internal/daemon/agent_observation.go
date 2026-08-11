package daemon

import (
	"errors"
	"fmt"
	"strings"
	"time"
)

// AgentObservationKind identifies an execution fact before it is projected
// into user-facing Activity. It is not an Activity entry or wire event name.
type AgentObservationKind string

const (
	AgentObservationAttached            AgentObservationKind = "attached"
	AgentObservationLaunchAccepted      AgentObservationKind = "launch_accepted"
	AgentObservationRuntimeReady        AgentObservationKind = "runtime_ready"
	AgentObservationRuntimeStarting     AgentObservationKind = "runtime_starting"
	AgentObservationRuntimeWorking      AgentObservationKind = "runtime_working"
	AgentObservationRuntimeThinking     AgentObservationKind = "runtime_thinking"
	AgentObservationRuntimeTool         AgentObservationKind = "runtime_tool"
	AgentObservationRuntimeCompacting   AgentObservationKind = "runtime_compacting"
	AgentObservationRuntimeCompacted    AgentObservationKind = "runtime_compacted"
	AgentObservationRuntimeIdle         AgentObservationKind = "runtime_idle"
	AgentObservationRuntimeDiagnostic   AgentObservationKind = "runtime_diagnostic"
	AgentObservationMessageBodyAccepted AgentObservationKind = "message_body_accepted"
	AgentObservationFreshnessHeld       AgentObservationKind = "freshness_held"
	AgentObservationError               AgentObservationKind = "error"
	AgentObservationStopped             AgentObservationKind = "stopped"
	AgentObservationDetached            AgentObservationKind = "detached"
)

// AgentObservationData seals the observation taxonomy to the execution facts
// defined in this package. Projection concerns cannot add arbitrary payloads.
type AgentObservationData interface {
	agentObservationData()
}

type AgentAttachmentObservationData struct {
	RuntimeID            string
	AttachmentGeneration AttachmentGeneration
}

func (AgentAttachmentObservationData) agentObservationData() {}

type AgentLaunchObservationData struct {
	RuntimeID       string
	StartDispatchID string
}

func (AgentLaunchObservationData) agentObservationData() {}

// AgentRuntimeObservationData keeps the identities of a logical launch, local
// process, provider session, provider turn, and Runtime CAS generation distinct.
type AgentRuntimeObservationData struct {
	RuntimeID         string
	ProcessInstanceID string
	ProviderSessionID string
	TurnID            string
	RuntimeGeneration int64
	ToolName          string
	ToolCallID        string
}

func (AgentRuntimeObservationData) agentObservationData() {}

// AgentRuntimeStageObservationData is a provider stage fact emitted before a
// concrete process/generation identity is available at this adapter seam.
// It cannot be used for RuntimeReady, which remains strictly process-fenced.
type AgentRuntimeStageObservationData struct {
	RuntimeID  string
	ToolName   string
	ToolCallID string
	ToolInput  map[string]any
}

func (AgentRuntimeStageObservationData) agentObservationData() {}

type AgentMessageAcceptanceObservationData struct {
	RuntimeID    string
	HandoffID    string
	MessageCount int
}

func (AgentMessageAcceptanceObservationData) agentObservationData() {}

type AgentFreshnessHoldObservationData struct {
	RuntimeID       string
	Target          string
	NewMessageCount int
	ReasonCode      string
}

func (AgentFreshnessHoldObservationData) agentObservationData() {}

type AgentErrorObservationData struct {
	RuntimeID         string
	ProcessInstanceID string
	ReasonCode        string
}

func (AgentErrorObservationData) agentObservationData() {}

type AgentStopObservationData struct {
	RuntimeID  string
	ReasonCode string
}

func (AgentStopObservationData) agentObservationData() {}

// AgentObservation carries one validated execution fact. The authenticated
// Workspace scope belongs to the producer; it is not duplicated here.
type AgentObservation struct {
	AgentID  string
	LaunchID string
	Kind     AgentObservationKind
	Data     AgentObservationData
	At       time.Time
}

func (observation AgentObservation) Validate() error {
	if strings.TrimSpace(observation.AgentID) == "" {
		return errors.New("Agent Observation Agent identity is required")
	}
	if observation.At.IsZero() {
		return errors.New("Agent Observation timestamp is required")
	}

	switch observation.Kind {
	case AgentObservationAttached, AgentObservationDetached:
		if strings.TrimSpace(observation.LaunchID) != "" {
			return errors.New("Agent Attachment observations cannot carry a launch identity")
		}
		data, ok := observation.Data.(AgentAttachmentObservationData)
		if !ok {
			return observationDataTypeError(observation.Kind)
		}
		if strings.TrimSpace(data.RuntimeID) == "" || data.AttachmentGeneration < 1 {
			return errors.New("Agent Attachment observation Runtime and generation are required")
		}
		return nil

	case AgentObservationLaunchAccepted:
		if err := observation.validateLaunchID(); err != nil {
			return err
		}
		data, ok := observation.Data.(AgentLaunchObservationData)
		if !ok {
			return observationDataTypeError(observation.Kind)
		}
		if strings.TrimSpace(data.RuntimeID) == "" || strings.TrimSpace(data.StartDispatchID) == "" {
			return errors.New("Agent launch observation Runtime and start dispatch identities are required")
		}
		return nil

	case AgentObservationRuntimeReady, AgentObservationRuntimeWorking:
		if err := observation.validateLaunchID(); err != nil {
			return err
		}
		data, ok := observation.Data.(AgentRuntimeObservationData)
		if !ok {
			return observationDataTypeError(observation.Kind)
		}
		if err := data.validateRuntimeIdentity(); err != nil {
			return err
		}
		if strings.TrimSpace(data.ToolName) != "" || strings.TrimSpace(data.ToolCallID) != "" {
			return errors.New("non-tool Agent Runtime observation cannot carry tool identity")
		}
		return nil

	case AgentObservationRuntimeStarting, AgentObservationRuntimeThinking, AgentObservationRuntimeTool, AgentObservationRuntimeCompacting, AgentObservationRuntimeCompacted, AgentObservationRuntimeIdle, AgentObservationRuntimeDiagnostic:
		if err := observation.validateLaunchID(); err != nil {
			return err
		}
		data, ok := observation.Data.(AgentRuntimeStageObservationData)
		if !ok || strings.TrimSpace(data.RuntimeID) == "" {
			return observationDataTypeError(observation.Kind)
		}
		if observation.Kind == AgentObservationRuntimeTool && strings.TrimSpace(data.ToolName) == "" {
			return errors.New("Agent tool observation tool name is required")
		}
		if observation.Kind != AgentObservationRuntimeTool && (strings.TrimSpace(data.ToolName) != "" || strings.TrimSpace(data.ToolCallID) != "" || len(data.ToolInput) != 0) {
			return errors.New("non-tool Agent Runtime stage observation cannot carry tool identity")
		}
		return nil

	case AgentObservationMessageBodyAccepted:
		if err := observation.validateLaunchID(); err != nil {
			return err
		}
		data, ok := observation.Data.(AgentMessageAcceptanceObservationData)
		if !ok {
			return observationDataTypeError(observation.Kind)
		}
		if strings.TrimSpace(data.RuntimeID) == "" || strings.TrimSpace(data.HandoffID) == "" || data.MessageCount < 1 {
			return errors.New("Agent Message acceptance Runtime, handoff, and count are required")
		}
		return nil

	case AgentObservationFreshnessHeld:
		if err := observation.validateLaunchID(); err != nil {
			return err
		}
		data, ok := observation.Data.(AgentFreshnessHoldObservationData)
		if !ok {
			return observationDataTypeError(observation.Kind)
		}
		if strings.TrimSpace(data.RuntimeID) == "" || strings.TrimSpace(data.Target) == "" || strings.TrimSpace(data.ReasonCode) == "" || data.NewMessageCount < 0 {
			return errors.New("Agent freshness hold Runtime, target, non-negative count, and reason are required")
		}
		return nil

	case AgentObservationError:
		if err := observation.validateLaunchID(); err != nil {
			return err
		}
		data, ok := observation.Data.(AgentErrorObservationData)
		if !ok {
			return observationDataTypeError(observation.Kind)
		}
		if strings.TrimSpace(data.RuntimeID) == "" || strings.TrimSpace(data.ReasonCode) == "" {
			return errors.New("Agent error observation Runtime and reason are required")
		}
		return nil

	case AgentObservationStopped:
		if err := observation.validateLaunchID(); err != nil {
			return err
		}
		data, ok := observation.Data.(AgentStopObservationData)
		if !ok {
			return observationDataTypeError(observation.Kind)
		}
		if strings.TrimSpace(data.RuntimeID) == "" || strings.TrimSpace(data.ReasonCode) == "" {
			return errors.New("Agent stop observation Runtime and reason are required")
		}
		return nil

	default:
		return fmt.Errorf("unknown Agent Observation kind %q", observation.Kind)
	}
}

func (observation AgentObservation) validateLaunchID() error {
	if strings.TrimSpace(observation.LaunchID) == "" {
		return errors.New("Agent execution observation launch identity is required")
	}
	return nil
}

func (data AgentRuntimeObservationData) validateRuntimeIdentity() error {
	if strings.TrimSpace(data.RuntimeID) == "" || strings.TrimSpace(data.ProcessInstanceID) == "" || data.RuntimeGeneration < 1 {
		return errors.New("Agent Runtime observation Runtime, process, and generation identities are required")
	}
	return nil
}

func observationDataTypeError(kind AgentObservationKind) error {
	return fmt.Errorf("Agent Observation %q has incompatible data", kind)
}
