package researchrun

import (
	"errors"
	"fmt"
	"strings"
	"time"
)

var (
	ErrCircuitProbeLeaseLost = errors.New("research execution circuit probe lease lost")
	ErrCircuitUnavailable    = errors.New("research execution circuit unavailable")
)

type CircuitState string

const (
	CircuitClosed   CircuitState = "closed"
	CircuitOpen     CircuitState = "open"
	CircuitHalfOpen CircuitState = "half_open"
)

type CircuitTarget struct {
	Scope             CircuitScope `json:"scope"`
	Key               string       `json:"key"`
	Label             string       `json:"label"`
	ConfigFingerprint string       `json:"config_fingerprint"`
}

type ExecutionCircuit struct {
	ID                  string
	WorkspaceID         string
	Target              CircuitTarget
	State               CircuitState
	Generation          int64
	ConsecutiveFailures int
	WindowStartedAt     *time.Time
	OpenedAt            *time.Time
	NextProbeAt         *time.Time
	ProbeToken          string
	ProbeLeaseExpiresAt *time.Time
	LastFailureClass    FailureClass
	LastSourceReason    string
	LastDiagnostics     string
	LastAttemptID       string
	LastSessionID       string
	LastFailedAt        *time.Time
	LastSucceededAt     *time.Time
}

type CircuitTransition struct {
	ID                string
	WorkspaceID       string
	CircuitID         string
	SessionID         string
	AttemptID         string
	Generation        int64
	FromState         CircuitState
	ToState           CircuitState
	Cause             string
	FailureClass      FailureClass
	SourceReason      string
	Diagnostics       string
	ConfigFingerprint string
	CreatedAt         time.Time
}

type CircuitFailureInput struct {
	WorkspaceID  string
	SessionID    string
	AttemptID    string
	Target       ExecutionTarget
	Disposition  FailureDisposition
	SourceReason string
	Diagnostics  string
}

type CircuitSuccessInput struct {
	WorkspaceID string
	SessionID   string
	AttemptID   string
	Target      ExecutionTarget
	Scope       CircuitScope
}

type CircuitProbeLease struct {
	CircuitID   string
	WorkspaceID string
	SessionID   string
	Target      CircuitTarget
	Token       string
	Generation  int64
	ExpiresAt   time.Time
}

type CircuitBlock struct {
	CircuitID  string       `json:"circuit_id"`
	Scope      CircuitScope `json:"scope"`
	State      CircuitState `json:"state"`
	Generation int64        `json:"generation"`
	RetryAt    *time.Time   `json:"retry_at,omitempty"`
}

type ExecutionTargetHealth struct {
	AgentID       string          `json:"agent_id"`
	Dispatchable  bool            `json:"dispatchable"`
	ProbeTargets  []CircuitTarget `json:"probe_targets,omitempty"`
	Blocking      []CircuitBlock  `json:"blocking,omitempty"`
	RetryAt       *time.Time      `json:"retry_at,omitempty"`
	BlockedReason string          `json:"blocked_reason,omitempty"`
}

type AttemptCircuitProbe struct {
	ID             string
	WorkspaceID    string
	SessionID      string
	AttemptID      string
	CircuitID      string
	Target         CircuitTarget
	Token          string
	Generation     int64
	LeaseExpiresAt time.Time
	Status         string
	FailureClass   FailureClass
	SourceReason   string
	Diagnostics    string
	ResolvedAt     *time.Time
}

type circuitPolicy struct {
	Threshold    int
	Window       time.Duration
	OpenDuration time.Duration
}

func policyForCircuitFailure(disposition FailureDisposition) (circuitPolicy, bool) {
	if disposition.CircuitScope == CircuitNone {
		return circuitPolicy{}, false
	}
	if disposition.ImmediateOpen {
		switch disposition.Class {
		case FailureInternal:
			return circuitPolicy{Threshold: 1, Window: 10 * time.Minute, OpenDuration: 10 * time.Minute}, true
		case FailureTool:
			return circuitPolicy{Threshold: 1, Window: 15 * time.Minute, OpenDuration: 15 * time.Minute}, true
		default:
			return circuitPolicy{Threshold: 1, Window: 15 * time.Minute, OpenDuration: 15 * time.Minute}, true
		}
	}
	switch disposition.Class {
	case FailureRateLimited:
		return circuitPolicy{Threshold: 2, Window: time.Minute, OpenDuration: time.Minute}, true
	case FailureProvider, FailureNetwork:
		return circuitPolicy{Threshold: 3, Window: 2 * time.Minute, OpenDuration: 2 * time.Minute}, true
	case FailureRuntimeLost:
		return circuitPolicy{Threshold: 2, Window: 2 * time.Minute, OpenDuration: time.Minute}, true
	case FailureTimeout:
		return circuitPolicy{Threshold: 3, Window: 5 * time.Minute, OpenDuration: 2 * time.Minute}, true
	case FailureTool:
		return circuitPolicy{Threshold: 2, Window: 5 * time.Minute, OpenDuration: 5 * time.Minute}, true
	case FailureUnknown:
		return circuitPolicy{Threshold: 2, Window: 5 * time.Minute, OpenDuration: time.Minute}, true
	default:
		return circuitPolicy{}, false
	}
}

func CircuitTargetForExecution(target ExecutionTarget, scope CircuitScope) (CircuitTarget, error) {
	var identity, label, fingerprint string
	switch scope {
	case CircuitAgent:
		identity, label, fingerprint = target.AgentID, target.AgentID, target.AgentConfigFingerprint
		if fingerprint == "" {
			fingerprint = target.ConfigFingerprint
		}
	case CircuitRuntime:
		identity, label, fingerprint = target.RuntimeID, target.RuntimeID, target.RuntimeConfigFingerprint
		if fingerprint == "" {
			fingerprint = target.ConfigFingerprint
		}
	case CircuitProvider:
		identity = strings.Join([]string{target.RuntimeID, strings.ToLower(strings.TrimSpace(target.Provider)), target.ProviderConfigFingerprint}, "\x00")
		label = strings.TrimSpace(target.Provider)
		fingerprint = target.ProviderConfigFingerprint
		if fingerprint == "" {
			fingerprint = target.ConfigFingerprint
		}
	case CircuitAdapter:
		identity, label = strings.ToLower(strings.TrimSpace(target.Adapter)), strings.TrimSpace(target.Adapter)
		fingerprint = ExecutionTargetFingerprint(identity)
	default:
		return CircuitTarget{}, fmt.Errorf("%w: invalid circuit scope %q", ErrInvalidTransition, scope)
	}
	if strings.Trim(identity, "\x00 ") == "" {
		return CircuitTarget{}, fmt.Errorf("%w: execution target lacks %s identity", ErrInvalidTransition, scope)
	}
	return CircuitTarget{
		Scope: scope, Key: ExecutionTargetFingerprint(string(scope), identity),
		Label: label, ConfigFingerprint: fingerprint,
	}, nil
}
