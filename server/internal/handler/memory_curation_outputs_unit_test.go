package handler

import (
	"encoding/json"
	"testing"
)

func TestCurationOutputMetadataPreservesApplicability(t *testing.T) {
	got := curationOutputMetadata(
		json.RawMessage(`{"reviewed":true}`),
		"team_curation",
		json.RawMessage(`{"project_ids":["project-1"],"channel_ids":["channel-1"],"task_types":["chat"],"expires_at":"2099-01-01T00:00:00Z"}`),
	)
	var metadata struct {
		Source   string `json:"source"`
		Reviewed bool   `json:"reviewed"`
		Applies  struct {
			ProjectIDs []string `json:"project_ids"`
			ChannelIDs []string `json:"channel_ids"`
			TaskTypes  []string `json:"task_types"`
			ExpiresAt  string   `json:"expires_at"`
		} `json:"applies"`
	}
	if err := json.Unmarshal(got, &metadata); err != nil {
		t.Fatal(err)
	}
	if metadata.Source != "team_curation" || !metadata.Reviewed {
		t.Fatalf("base metadata = %+v", metadata)
	}
	if len(metadata.Applies.ProjectIDs) != 1 || metadata.Applies.ProjectIDs[0] != "project-1" ||
		len(metadata.Applies.ChannelIDs) != 1 || metadata.Applies.ChannelIDs[0] != "channel-1" ||
		len(metadata.Applies.TaskTypes) != 1 || metadata.Applies.TaskTypes[0] != "chat" ||
		metadata.Applies.ExpiresAt != "2099-01-01T00:00:00Z" {
		t.Fatalf("applicability metadata = %+v", metadata.Applies)
	}
}

func TestCurationOutputMetadataIgnoresInvalidApplicability(t *testing.T) {
	got := curationOutputMetadata(nil, "agent_self_review", json.RawMessage(`[]`))
	var metadata map[string]any
	if err := json.Unmarshal(got, &metadata); err != nil {
		t.Fatal(err)
	}
	if metadata["source"] != "agent_self_review" {
		t.Fatalf("source = %#v", metadata["source"])
	}
	if _, exists := metadata["applies"]; exists {
		t.Fatalf("invalid applies was retained: %#v", metadata)
	}
}

func TestCountMemoryCurationChildStatusesSkipsDoNotBlock(t *testing.T) {
	got := countMemoryCurationChildStatuses([]string{
		"succeeded", "succeeded", "failed", "skipped", "skipped", "waiting_runtime", "queued",
	})
	if got.success != 2 || got.failed != 1 || got.skipped != 2 || got.pending != 2 {
		t.Fatalf("counts = %+v", got)
	}
	terminal := countMemoryCurationChildStatuses([]string{"succeeded", "skipped", "failed"})
	if terminal.pending != 0 || terminal.success != 1 || terminal.skipped != 1 || terminal.failed != 1 {
		t.Fatalf("terminal counts = %+v", terminal)
	}
}
