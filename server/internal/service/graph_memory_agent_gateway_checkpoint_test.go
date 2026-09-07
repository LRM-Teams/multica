package service

import (
	"encoding/json"
	"errors"
	"testing"
)

func TestNormalizeGraphMemoryAutoCheckpointBodyBindsActiveTrajectory(t *testing.T) {
	body, err := normalizeGraphMemoryAutoCheckpointBody([]byte(`{"idempotency_key":"auto:m1"}`), "trajectory-active")
	if err != nil {
		t.Fatal(err)
	}
	var got struct {
		TrajectoryID   string          `json:"trajectory_id"`
		State          json.RawMessage `json:"state"`
		IdempotencyKey string          `json:"idempotency_key"`
	}
	if err := json.Unmarshal(body, &got); err != nil {
		t.Fatal(err)
	}
	if got.TrajectoryID != "trajectory-active" || got.IdempotencyKey != "auto:m1" || string(got.State) != `{}` {
		t.Fatalf("normalized checkpoint = %s", body)
	}
	_, err = normalizeGraphMemoryAutoCheckpointBody([]byte(`{"idempotency_key":"auto:m1","trajectory_id":"other"}`), "trajectory-active")
	if !errors.Is(err, ErrGraphMemoryAgentGatewayForbidden) {
		t.Fatalf("mismatched trajectory error = %v", err)
	}
	_, err = normalizeGraphMemoryAutoCheckpointBody([]byte(`{}`), "trajectory-active")
	if err == nil {
		t.Fatal("missing idempotency key was accepted")
	}
}
