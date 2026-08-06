package researchrun

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
)

// dispatchRequestFingerprintV1 is immutable. Adding fields to Run or Task must
// not invalidate already committed outbox rows. A change to external dispatch
// semantics requires a V2 fingerprint and request_version migration.
type dispatchRequestFingerprintV1 struct {
	WorkspaceID         string          `json:"workspace_id"`
	SessionID           string          `json:"session_id"`
	RunGoalVersion      int             `json:"run_goal_version"`
	RunPlanVersion      int             `json:"run_plan_version"`
	OrchestratorVersion string          `json:"orchestrator_version"`
	TaskID              string          `json:"task_id"`
	TaskKind            TaskKind        `json:"task_kind"`
	TaskGoalVersion     int             `json:"task_goal_version"`
	TaskPlanVersion     int             `json:"task_plan_version"`
	TimeoutSeconds      int             `json:"timeout_seconds"`
	AcceptanceCriteria  json.RawMessage `json:"acceptance_criteria"`
	AttemptID           string          `json:"attempt_id"`
	AgentID             string          `json:"agent_id"`
	Prompt              string          `json:"prompt"`
	Key                 string          `json:"key"`
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

func encodeDispatchRequest(request DispatchRequest) ([]byte, string, error) {
	hash, err := HashDispatchRequest(request)
	if err != nil {
		return nil, "", err
	}
	request.RequestHash = hash
	encoded, err := json.Marshal(request)
	return encoded, hash, err
}
