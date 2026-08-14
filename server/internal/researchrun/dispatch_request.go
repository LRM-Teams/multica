package researchrun

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"strings"
)

// dispatchRequestFingerprintV1 is immutable. Adding fields to Run or Task must
// not invalidate already committed outbox rows. Target is an optional compatible
// extension: nil marshals to the exact historical fingerprint, while every new
// target field participates in the request hash.
type dispatchRequestFingerprintV1 struct {
	WorkspaceID         string                       `json:"workspace_id"`
	SessionID           string                       `json:"session_id"`
	RunGoalVersion      int                          `json:"run_goal_version"`
	RunPlanVersion      int                          `json:"run_plan_version"`
	OrchestratorVersion string                       `json:"orchestrator_version"`
	TaskID              string                       `json:"task_id"`
	TaskKind            TaskKind                     `json:"task_kind"`
	TaskGoalVersion     int                          `json:"task_goal_version"`
	TaskPlanVersion     int                          `json:"task_plan_version"`
	TimeoutSeconds      int                          `json:"timeout_seconds"`
	AcceptanceCriteria  json.RawMessage              `json:"acceptance_criteria"`
	AttemptID           string                       `json:"attempt_id"`
	AgentID             string                       `json:"agent_id"`
	Prompt              string                       `json:"prompt"`
	Key                 string                       `json:"key"`
	Target              *ExecutionTarget             `json:"target,omitempty"`
	Manifest            *dispatchManifestFingerprint `json:"manifest,omitempty"`
}

type dispatchManifestFingerprint struct {
	ID   string `json:"id"`
	Hash string `json:"hash"`
}

// HashDispatchRequest fingerprints the immutable request body without its own
// hash field. Both the outbox and external Inbox adapter enforce this value.
func HashDispatchRequest(request DispatchRequest) (string, error) {
	fingerprint := dispatchRequestFingerprintV1{
		WorkspaceID: request.Run.WorkspaceID, SessionID: request.Run.SessionID,
		RunGoalVersion: request.Run.GoalVersion, RunPlanVersion: request.Run.PlanVersion,
		OrchestratorVersion: request.Run.OrchestratorVersion,
		TaskID:              request.Task.ID, TaskKind: request.Task.Kind,
		TaskGoalVersion: request.Task.GoalVersion, TaskPlanVersion: request.Task.PlanVersion,
		TimeoutSeconds: request.Task.TimeoutSeconds, AcceptanceCriteria: request.Task.AcceptanceCriteria,
		AttemptID: request.AttemptID, AgentID: request.AgentID, Prompt: request.Prompt, Key: request.Key,
	}
	if request.Target != (ExecutionTarget{}) {
		if err := ValidateExecutionTarget(request.Target, request.AgentID); err != nil {
			return "", err
		}
		target := request.Target
		fingerprint.Target = &target
	}
	if request.ManifestID != "" || request.ManifestHash != "" {
		if request.ManifestID == "" || request.ManifestHash == "" ||
			request.ManifestID != strings.TrimSpace(request.ManifestID) ||
			request.ManifestHash != strings.TrimSpace(request.ManifestHash) ||
			!strings.HasPrefix(request.ManifestHash, "sha256:") {
			return "", fmt.Errorf("invalid dispatch manifest identity")
		}
		fingerprint.Manifest = &dispatchManifestFingerprint{ID: request.ManifestID, Hash: request.ManifestHash}
	}
	encoded, err := json.Marshal(fingerprint)
	if err != nil {
		return "", err
	}
	decoder := json.NewDecoder(bytes.NewReader(encoded))
	decoder.UseNumber()
	var value any
	if err = decoder.Decode(&value); err != nil {
		return "", err
	}
	canonical, err := json.Marshal(value)
	if err != nil {
		return "", err
	}
	sum := sha256.Sum256(canonical)
	return hex.EncodeToString(sum[:]), nil
}

func ValidateExecutionTarget(target ExecutionTarget, agentID string) error {
	if strings.TrimSpace(target.Adapter) == "" || strings.TrimSpace(target.AgentID) == "" || target.AgentID != agentID {
		return fmt.Errorf("invalid dispatch execution target")
	}
	return nil
}

// ExecutionTargetFingerprint hashes configuration identity without storing
// credentials. Callers include only dispatch-affecting configuration and the
// daemon-supplied provider fingerprint; heartbeat and display state must not
// release a deterministic circuit.
func ExecutionTargetFingerprint(parts ...string) string {
	hash := sha256.New()
	for _, part := range parts {
		_, _ = hash.Write([]byte(strings.TrimSpace(part)))
		_, _ = hash.Write([]byte{0})
	}
	return hex.EncodeToString(hash.Sum(nil))
}

type ExecutionTargetConfigIdentity struct {
	RuntimeMode              string
	RuntimePinnedVersion     string
	ProviderStateFingerprint string
	RuntimeConfig            string
	CustomEnv                string
	CustomArgs               string
	MCPConfig                string
	ThinkingLevel            string
}

// FingerprintExecutionTarget adds scoped circuit identities while preserving
// the aggregate fingerprint protocol already frozen into active Attempts.
func FingerprintExecutionTarget(target ExecutionTarget, config ExecutionTargetConfigIdentity) ExecutionTarget {
	target.AgentConfigFingerprint = ExecutionTargetFingerprint(
		target.AgentID, target.Model, config.RuntimeConfig, config.CustomEnv,
		config.CustomArgs, config.MCPConfig, config.ThinkingLevel,
	)
	target.RuntimeConfigFingerprint = ExecutionTargetFingerprint(
		target.RuntimeID, config.RuntimeMode, config.RuntimePinnedVersion,
	)
	target.ProviderConfigFingerprint = ExecutionTargetFingerprint(
		target.RuntimeID, target.Provider, config.ProviderStateFingerprint,
		config.RuntimeConfig, config.CustomEnv,
	)
	target.ConfigFingerprint = ExecutionTargetFingerprint(
		target.AgentID, target.RuntimeID, target.Provider, target.Model,
		config.RuntimeMode, config.RuntimePinnedVersion, config.ProviderStateFingerprint,
		config.RuntimeConfig, config.CustomEnv, config.CustomArgs, config.MCPConfig,
		config.ThinkingLevel,
	)
	return target
}

func encodeDispatchRequest(request DispatchRequest) ([]byte, string, error) {
	hash, err := HashDispatchRequest(request)
	if err != nil {
		return nil, "", err
	}
	request.RequestHash = hash
	encoded, err := json.Marshal(request)
	return encoded, hash, err
}
