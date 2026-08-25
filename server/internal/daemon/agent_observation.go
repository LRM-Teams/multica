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
	AgentObservationRuntimeReady           AgentObservationKind = "runtime_ready"
	AgentObservationRuntimeStarting        AgentObservationKind = "runtime_starting"
	AgentObservationRuntimeWorking         AgentObservationKind = "runtime_working"
	AgentObservationRuntimeThinking        AgentObservationKind = "runtime_thinking"
	AgentObservationRuntimeTool            AgentObservationKind = "runtime_tool"
	AgentObservationRuntimeCompacting      AgentObservationKind = "runtime_compacting"
	AgentObservationRuntimeCompacted       AgentObservationKind = "runtime_compacted"
	AgentObservationRuntimeCompactionStale AgentObservationKind = "runtime_compaction_stale"
	AgentObservationRuntimeIdle            AgentObservationKind = "runtime_idle"
	AgentObservationRuntimeDiagnostic      AgentObservationKind = "runtime_diagnostic"
	AgentObservationRuntimeStalled         AgentObservationKind = "runtime_stalled"
	AgentObservationMessageBodyAccepted    AgentObservationKind = "message_body_accepted"
	AgentObservationFreshnessHeld          AgentObservationKind = "freshness_held"
	AgentObservationDraftSent              AgentObservationKind = "draft_sent"
	AgentObservationError                  AgentObservationKind = "error"
	AgentObservationOffline                AgentObservationKind = "offline"
)

// AgentObservationData seals the observation taxonomy to the execution facts
// defined in this package. Projection concerns cannot add arbitrary payloads.
type AgentObservationData interface {
	agentObservationData()
}

// AgentRuntimeObservationData keeps process, provider session, provider turn,
// and Runtime CAS generation identities distinct.
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
	StaleFor   time.Duration
	ProviderEventAt time.Time
}

func (AgentRuntimeStageObservationData) agentObservationData() {}

// AgentRuntimeDiagnosticObservationData keeps the provider's typed warning
// separate from generic runtime-stage facts. The daemon sanitizes these
// fields before they become Activity presentation.
type AgentRuntimeDiagnosticObservationData struct {
	RuntimeID string
	Source    string
	Reference string
	Name      string
	Kind      string
	Detail    string
}

func (AgentRuntimeDiagnosticObservationData) agentObservationData() {}

type AgentMessageAcceptanceObservationData struct {
	RuntimeID string
	AcceptedAt time.Time
}

func (AgentMessageAcceptanceObservationData) agentObservationData() {}

type AgentFreshnessHoldObservationData struct {
	RuntimeID       string
	Target          string
	NewMessageCount int
	ReasonCode      string
}

func (AgentFreshnessHoldObservationData) agentObservationData() {}

type AgentDraftSentObservationData struct {
	RuntimeID string
	Target    string
	Anyway    bool
}

func (AgentDraftSentObservationData) agentObservationData() {}

type AgentErrorObservationData struct {
	RuntimeID         string
	ProcessInstanceID string
	ReasonCode        string
	Message           string
}

func (AgentErrorObservationData) agentObservationData() {}

// AgentObservation carries one validated execution fact. The authenticated
// Workspace scope belongs to the producer; it is not duplicated here.
type AgentObservation struct {
	AgentID         string
	AgentInstanceID string
	Kind            AgentObservationKind
	Data            AgentObservationData
	At              time.Time
}

func (observation AgentObservation) Validate() error {
	if strings.TrimSpace(observation.AgentID) == "" {
		return errors.New("Agent Observation Agent identity is required")
	}
	if observation.At.IsZero() {
		return errors.New("Agent Observation timestamp is required")
	}

	switch observation.Kind {
	case AgentObservationRuntimeReady:
		if err := observation.validateAgentInstanceID(); err != nil {
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

	case AgentObservationRuntimeStarting, AgentObservationRuntimeWorking, AgentObservationRuntimeThinking, AgentObservationRuntimeTool, AgentObservationRuntimeCompacting, AgentObservationRuntimeCompacted, AgentObservationRuntimeCompactionStale, AgentObservationRuntimeIdle, AgentObservationRuntimeStalled:
		if err := observation.validateAgentInstanceID(); err != nil {
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

	case AgentObservationRuntimeDiagnostic:
		if err := observation.validateAgentInstanceID(); err != nil {
			return err
		}
		data, ok := observation.Data.(AgentRuntimeDiagnosticObservationData)
		if !ok || strings.TrimSpace(data.RuntimeID) == "" || strings.TrimSpace(data.Name) == "" {
			return observationDataTypeError(observation.Kind)
		}
		return nil

	case AgentObservationMessageBodyAccepted:
		if err := observation.validateAgentInstanceID(); err != nil {
			return err
		}
		data, ok := observation.Data.(AgentMessageAcceptanceObservationData)
		if !ok {
			return observationDataTypeError(observation.Kind)
		}
		if strings.TrimSpace(data.RuntimeID) == "" {
			return errors.New("Agent Message acceptance Runtime is required")
		}
		return nil

	case AgentObservationFreshnessHeld:
		if err := observation.validateAgentInstanceID(); err != nil {
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

	case AgentObservationDraftSent:
		if err := observation.validateAgentInstanceID(); err != nil {
			return err
		}
		data, ok := observation.Data.(AgentDraftSentObservationData)
		if !ok {
			return observationDataTypeError(observation.Kind)
		}
		if strings.TrimSpace(data.RuntimeID) == "" || strings.TrimSpace(data.Target) == "" {
			return errors.New("Agent Draft send Runtime and target are required")
		}
		return nil

	case AgentObservationError, AgentObservationOffline:
		if err := observation.validateAgentInstanceID(); err != nil {
			return err
		}
		data, ok := observation.Data.(AgentErrorObservationData)
		if !ok {
			return observationDataTypeError(observation.Kind)
		}
		if strings.TrimSpace(data.RuntimeID) == "" || strings.TrimSpace(data.ReasonCode) == "" {
			return errors.New("Agent error observation Runtime and reason are required")
		}
		if observation.Kind == AgentObservationError && strings.TrimSpace(data.Message) == "" {
			return errors.New("Agent error observation message is required")
		}
		return nil

	default:
		return fmt.Errorf("unknown Agent Observation kind %q", observation.Kind)
	}
}

func (observation AgentObservation) validateAgentInstanceID() error {
	if strings.TrimSpace(observation.AgentInstanceID) == "" {
		return errors.New("Agent execution observation local instance identity is required")
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
