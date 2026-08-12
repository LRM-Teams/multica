// SPDX-License-Identifier: Apache-2.0

// Package testutil provides synthetic fixtures shared by server tests.
package testutil

import (
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"time"

	"github.com/google/uuid"
)

const (
	MixedRLRunID       = "70000000-0000-4000-8000-000000000001"
	MixedRLProjectID   = "70000000-0000-4000-8000-000000000002"
	MixedRLWorkspaceID = "70000000-0000-4000-8000-000000000003"
	MixedRLSnapshotID  = "sha256:synthetic-mixed-rl-snapshot"
)

// MixedRLAgent describes one source agent and its run-isolated execution identity.
type MixedRLAgent struct {
	SourceAgentID    string
	ExecutionAgentID string
	RunAgentID       string
	RuntimeID        string
	PiSessionID      string
	AReALSessionID   string
	TrainingMode     string
}

// MixedRLRoster groups the three supported run training classifications.
type MixedRLRoster struct {
	Online  []MixedRLAgent
	Offline []MixedRLAgent
	None    []MixedRLAgent
}

// MixedRLRosterFixture returns one synthetic agent in each training mode.
func MixedRLRosterFixture() MixedRLRoster {
	return MixedRLRoster{
		Online:  []MixedRLAgent{MixedRLRunAgentFixture(1, "online_rl")},
		Offline: []MixedRLAgent{MixedRLRunAgentFixture(2, "offline_rl")},
		None:    []MixedRLAgent{MixedRLRunAgentFixture(3, "none")},
	}
}

// MixedRLRunAgentFixture creates a deterministic run-agent fixture.
func MixedRLRunAgentFixture(ordinal int, mode string) MixedRLAgent {
	const maxFixtureOrdinal = 1<<40 - 1
	if ordinal < 0 || uint64(ordinal) > maxFixtureOrdinal {
		panic("mixed-RL fixture ordinal must fit in 40 bits")
	}
	id := func(prefix uint64) string {
		value := prefix<<40 | uint64(ordinal)
		return fmt.Sprintf("70000000-0000-4000-8000-%012x", value)
	}
	arealSessionID := ""
	if mode == "online_rl" {
		arealSessionID = fmt.Sprintf("areal-session-%d", ordinal)
	}
	return MixedRLAgent{
		SourceAgentID:    id(1),
		ExecutionAgentID: id(2),
		RunAgentID:       id(3),
		RuntimeID:        id(4),
		PiSessionID:      fmt.Sprintf("pi-session-%d", ordinal),
		AReALSessionID:   arealSessionID,
		TrainingMode:     mode,
	}
}

// MixedRLProviderCall describes one captured logical provider call.
type MixedRLProviderCall struct {
	CallID                string
	RunID                 string
	RunAgentID            string
	TurnID                string
	PiSessionID           string
	CallOrdinal           int64
	Provider              string
	Model                 string
	APIKind               string
	RawProviderRequest    []byte
	FinalAssistantMessage []byte
	NormalizedTrajectory  []byte
	NormalizationVersion  string
	Status                string
	StopReason            string
	ResponseComplete      bool
	TrainingEligible      bool
	AReALSessionID        string
	AReALCallID           string
	RequestHash           string
	ResponseHash          string
	StartedAt             time.Time
	CompletedAt           time.Time
}

// MixedRLProviderCallFixture returns a complete, credential-free provider call.
func MixedRLProviderCallFixture(agent MixedRLAgent, ordinal int64) MixedRLProviderCall {
	startedAt := time.Date(2026, time.August, 10, 2, 0, int(ordinal), 0, time.UTC)
	callID := fmt.Sprintf("call-synthetic-%s-%d", agent.RunAgentID, ordinal)
	arealSessionID := ""
	arealCallID := ""
	if agent.TrainingMode == "online_rl" {
		arealSessionID = agent.AReALSessionID
		arealCallID = fmt.Sprintf("areal-call-%d", ordinal)
	}
	rawRequest := []byte(`{"system":[{"type":"text","text":"synthetic system"}],"messages":[{"role":"user","content":[{"type":"text","text":"synthetic request"}]}],"tools":[]}`)
	finalAssistantMessage := []byte(`{"role":"assistant","blocks":[{"type":"thinking","thinking":"synthetic reasoning"},{"type":"text","text":"synthetic response"}]}`)
	var normalizedTrajectory []byte
	normalizationVersion := ""
	if agent.TrainingMode == "offline_rl" {
		normalizedTrajectory = []byte(`{"version":"1","system":{"blocks":[{"type":"text","text":"synthetic system"}]},"messages":[{"role":"user","blocks":[{"type":"text","text":"synthetic request"}]}],"tools":[],"output":{"role":"assistant","blocks":[{"type":"thinking","thinking":"synthetic reasoning"},{"type":"text","text":"synthetic response"}]}}`)
		normalizationVersion = "1"
	}
	return MixedRLProviderCall{
		CallID:                callID,
		RunID:                 MixedRLRunID,
		RunAgentID:            agent.RunAgentID,
		TurnID:                deterministicMixedRLUUID("turn:" + agent.RunAgentID),
		PiSessionID:           agent.PiSessionID,
		CallOrdinal:           ordinal,
		Provider:              "synthetic-provider",
		Model:                 "synthetic-model",
		APIKind:               "messages",
		RawProviderRequest:    rawRequest,
		FinalAssistantMessage: finalAssistantMessage,
		NormalizedTrajectory:  normalizedTrajectory,
		NormalizationVersion:  normalizationVersion,
		Status:                "completed",
		StopReason:            "stop",
		ResponseComplete:      true,
		TrainingEligible:      true,
		AReALSessionID:        arealSessionID,
		AReALCallID:           arealCallID,
		RequestHash:           "sha256:synthetic-request-" + callID,
		ResponseHash:          "sha256:synthetic-response-" + callID,
		StartedAt:             startedAt,
		CompletedAt:           startedAt.Add(time.Second),
	}
}

// MixedRLVisibleAction describes one trusted canonical action.
type MixedRLVisibleAction struct {
	ActionID       string
	RunID          string
	RunAgentID     string
	TurnID         string
	Kind           string
	CanonicalID    string
	ProducerCallID string
	ActionOrdinal  int64
	Status         string
	CreatedAt      time.Time
}

// MixedRLVisibleActionFixture returns a successful synthetic message action.
func MixedRLVisibleActionFixture(call MixedRLProviderCall) MixedRLVisibleAction {
	return MixedRLVisibleAction{
		ActionID:       deterministicMixedRLUUID("action:" + call.CallID),
		RunID:          call.RunID,
		RunAgentID:     call.RunAgentID,
		TurnID:         call.TurnID,
		Kind:           "message",
		CanonicalID:    deterministicMixedRLUUID("message:" + call.CallID),
		ProducerCallID: call.CallID,
		ActionOrdinal:  1,
		Status:         "succeeded",
		CreatedAt:      call.CompletedAt.Add(time.Second),
	}
}

// MixedRLConsumption describes concrete message consumption evidence.
type MixedRLConsumption struct {
	ConsumptionID       string
	RunID               string
	RunAgentID          string
	TurnID              string
	ChannelMessageID    string
	Source              string
	EffectiveFromCallID string
	ConsumedAt          time.Time
}

// MixedRLConsumptionFixture returns synthetic message-check evidence.
func MixedRLConsumptionFixture(call MixedRLProviderCall) MixedRLConsumption {
	return MixedRLConsumption{
		ConsumptionID:       deterministicMixedRLUUID("consumption:" + call.CallID),
		RunID:               call.RunID,
		RunAgentID:          call.RunAgentID,
		TurnID:              call.TurnID,
		ChannelMessageID:    deterministicMixedRLUUID("consumed-message:" + call.CallID),
		Source:              "message_check",
		EffectiveFromCallID: call.CallID,
		ConsumedAt:          call.StartedAt.Add(-time.Second),
	}
}

// MixedRLFrozenSnapshot describes a deterministic immutable snapshot fixture.
type MixedRLFrozenSnapshot struct {
	SnapshotID           string
	RunID                string
	RunStatus            string
	SchemaVersion        string
	NormalizationVersion string
	SegmentIDs           []string
	ProviderCallIDs      []string
	EdgeIDs              []string
	SegmentCount         int64
	CallCount            int64
	EdgeCount            int64
	CanonicalManifest    []byte
	SnapshotHash         string
}

// MixedRLFrozenSnapshotFixture builds a snapshot containing the supplied call.
func MixedRLFrozenSnapshotFixture(call MixedRLProviderCall) MixedRLFrozenSnapshot {
	action := MixedRLVisibleActionFixture(call)
	segmentIDs := []string{"message:" + action.CanonicalID}
	providerCallIDs := []string{call.CallID}
	edgeIDs := []string{}
	manifest, err := json.Marshal(map[string][]string{
		"calls": providerCallIDs, "edges": edgeIDs, "segments": segmentIDs,
	})
	if err != nil {
		panic(err)
	}
	digest := sha256.Sum256(manifest)
	snapshotHash := fmt.Sprintf("sha256:%x", digest)
	return MixedRLFrozenSnapshot{
		SnapshotID:           snapshotHash,
		RunID:                call.RunID,
		RunStatus:            "completed",
		SchemaVersion:        "1",
		NormalizationVersion: "1",
		SegmentIDs:           segmentIDs,
		ProviderCallIDs:      providerCallIDs,
		EdgeIDs:              edgeIDs,
		SegmentCount:         int64(len(segmentIDs)),
		CallCount:            int64(len(providerCallIDs)),
		EdgeCount:            int64(len(edgeIDs)),
		CanonicalManifest:    manifest,
		SnapshotHash:         snapshotHash,
	}
}

func deterministicMixedRLUUID(name string) string {
	return uuid.NewSHA1(uuid.NameSpaceOID, []byte("multica-mixed-rl-fixture:"+name)).String()
}
