// SPDX-License-Identifier: Apache-2.0

package testutil

import (
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
)

func TestMixedRLFixtures_ProduceSchemaValidIdentities(t *testing.T) {
	t.Parallel()

	roster := MixedRLRosterFixture()
	if len(roster.Online) != 1 || len(roster.Offline) != 1 || len(roster.None) != 1 {
		t.Fatalf("expected one agent per training mode, got online=%d offline=%d none=%d", len(roster.Online), len(roster.Offline), len(roster.None))
	}

	for mode, agent := range map[string]MixedRLAgent{
		"online_rl":  roster.Online[0],
		"offline_rl": roster.Offline[0],
		"none":       roster.None[0],
	} {
		call := MixedRLProviderCallFixture(agent, 1)
		if mode == "online_rl" {
			if call.AReALSessionID == "" || call.AReALCallID == "" {
				t.Fatalf("%s call must include both AReaL identity fields", mode)
			}
		} else if call.AReALSessionID != "" || call.AReALCallID != "" {
			t.Fatalf("%s call must omit both AReaL identity fields, got session=%q call=%q", mode, call.AReALSessionID, call.AReALCallID)
		}
	}

	online := roster.Online[0]
	first := MixedRLProviderCallFixture(online, 1)
	second := MixedRLProviderCallFixture(online, 2)
	if first.AReALCallID == second.AReALCallID {
		t.Fatalf("two calls in one online session must have distinct AReaL call IDs, got %q", first.AReALCallID)
	}
	if first.AReALSessionID != second.AReALSessionID {
		t.Fatalf("two calls for one agent must retain one AReaL session, got %q and %q", first.AReALSessionID, second.AReALSessionID)
	}

	ordinalTen := MixedRLRunAgentFixture(10, "online_rl")
	for name, value := range map[string]string{
		"source agent":    ordinalTen.SourceAgentID,
		"execution agent": ordinalTen.ExecutionAgentID,
		"run agent":       ordinalTen.RunAgentID,
		"runtime":         ordinalTen.RuntimeID,
	} {
		if _, err := uuid.Parse(value); err != nil {
			t.Errorf("%s fixture ID %q is not a valid UUID: %v", name, value, err)
		}
	}
}

func TestMixedRLRosterFixture_HasOneAgentPerMode(t *testing.T) {
	roster := MixedRLRosterFixture()
	if len(roster.Online) != 1 || len(roster.Offline) != 1 || len(roster.None) != 1 {
		t.Fatalf("unexpected roster sizes: online=%d offline=%d none=%d", len(roster.Online), len(roster.Offline), len(roster.None))
	}
}

func TestMixedRLRunAgentFixture_HasDeterministicModeIdentity(t *testing.T) {
	first := MixedRLRunAgentFixture(7, "online_rl")
	second := MixedRLRunAgentFixture(7, "online_rl")
	if first != second {
		t.Fatalf("run-agent builder is not deterministic: %#v != %#v", first, second)
	}
	if first.AReALSessionID == "" || first.PiSessionID == "" {
		t.Fatalf("online run-agent must include Pi and AReaL sessions: %#v", first)
	}
}

func TestMixedRLProviderCallFixture_HasCompleteCredentialFreeCapture(t *testing.T) {
	agent := MixedRLRunAgentFixture(7, "offline_rl")
	call := MixedRLProviderCallFixture(agent, 3)
	if call.PiSessionID != agent.PiSessionID {
		t.Fatalf("provider call Pi session = %q, want %q", call.PiSessionID, agent.PiSessionID)
	}
	var request map[string]any
	if err := json.Unmarshal(call.RawProviderRequest, &request); err != nil {
		t.Fatalf("raw request is not an object: %v", err)
	}
	requestJSON := strings.ToLower(string(call.RawProviderRequest))
	for _, forbidden := range []string{"authorization", "api_key", "credential", "password"} {
		if strings.Contains(requestJSON, forbidden) {
			t.Fatalf("raw request contains forbidden field %q: %s", forbidden, requestJSON)
		}
	}
	var assistant struct {
		Role   string           `json:"role"`
		Blocks []map[string]any `json:"blocks"`
	}
	if err := json.Unmarshal(call.FinalAssistantMessage, &assistant); err != nil {
		t.Fatalf("final assistant message is invalid: %v", err)
	}
	if assistant.Role != "assistant" || len(assistant.Blocks) == 0 {
		t.Fatalf("final assistant message is not typed assistant content: %#v", assistant)
	}
	if len(call.NormalizedTrajectory) == 0 || call.NormalizationVersion == "" {
		t.Fatalf("offline call must include normalization metadata: %#v", call)
	}
}

func TestMixedRLVisibleActionFixture_HasDeterministicTimestamp(t *testing.T) {
	call := MixedRLProviderCallFixture(MixedRLRunAgentFixture(7, "offline_rl"), 3)
	first := MixedRLVisibleActionFixture(call)
	second := MixedRLVisibleActionFixture(call)
	if first != second {
		t.Fatalf("visible-action builder is not deterministic: %#v != %#v", first, second)
	}
	if !first.CreatedAt.Equal(call.CompletedAt.Add(time.Second)) {
		t.Fatalf("action created_at = %s, want %s", first.CreatedAt, call.CompletedAt.Add(time.Second))
	}
}

func TestMixedRLConsumptionFixture_HasDeterministicTimestamp(t *testing.T) {
	call := MixedRLProviderCallFixture(MixedRLRunAgentFixture(7, "offline_rl"), 3)
	first := MixedRLConsumptionFixture(call)
	second := MixedRLConsumptionFixture(call)
	if first != second {
		t.Fatalf("consumption builder is not deterministic: %#v != %#v", first, second)
	}
	if !first.ConsumedAt.Equal(call.StartedAt.Add(-time.Second)) {
		t.Fatalf("consumed_at = %s, want %s", first.ConsumedAt, call.StartedAt.Add(-time.Second))
	}
}

func TestMixedRLFrozenSnapshotFixture_HasCountsAndCanonicalManifest(t *testing.T) {
	call := MixedRLProviderCallFixture(MixedRLRunAgentFixture(7, "offline_rl"), 3)
	snapshot := MixedRLFrozenSnapshotFixture(call)
	if snapshot.SegmentCount != int64(len(snapshot.SegmentIDs)) ||
		snapshot.CallCount != int64(len(snapshot.ProviderCallIDs)) ||
		snapshot.EdgeCount != int64(len(snapshot.EdgeIDs)) {
		t.Fatalf("snapshot integrity counts do not match IDs: %#v", snapshot)
	}
	var manifest struct {
		Segments []string `json:"segments"`
		Calls    []string `json:"calls"`
		Edges    []string `json:"edges"`
	}
	if err := json.Unmarshal(snapshot.CanonicalManifest, &manifest); err != nil {
		t.Fatalf("canonical manifest is invalid: %v", err)
	}
	if len(manifest.Segments) != int(snapshot.SegmentCount) || len(manifest.Calls) != int(snapshot.CallCount) || len(manifest.Edges) != int(snapshot.EdgeCount) {
		t.Fatalf("canonical manifest does not match integrity counts: %#v", manifest)
	}
}

func TestMixedRLFrozenSnapshotFixture_IsContentAddressed(t *testing.T) {
	agent := MixedRLRunAgentFixture(7, "offline_rl")
	first := MixedRLFrozenSnapshotFixture(MixedRLProviderCallFixture(agent, 1))
	second := MixedRLFrozenSnapshotFixture(MixedRLProviderCallFixture(agent, 2))

	if string(first.CanonicalManifest) == string(second.CanonicalManifest) {
		t.Fatal("test setup requires distinct canonical manifests")
	}
	if first.SnapshotID == second.SnapshotID {
		t.Fatalf("distinct manifests produced the same snapshot ID %q", first.SnapshotID)
	}
	if first.SnapshotHash == second.SnapshotHash {
		t.Fatalf("distinct manifests produced the same snapshot hash %q", first.SnapshotHash)
	}
	for _, snapshot := range []MixedRLFrozenSnapshot{first, second} {
		if snapshot.SnapshotID != snapshot.SnapshotHash {
			t.Fatalf("snapshot ID %q must equal its content hash %q", snapshot.SnapshotID, snapshot.SnapshotHash)
		}
		if !strings.HasPrefix(snapshot.SnapshotHash, "sha256:") {
			t.Fatalf("snapshot hash %q must use the sha256 scheme", snapshot.SnapshotHash)
		}
	}
}
