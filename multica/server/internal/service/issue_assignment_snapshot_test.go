package service

import (
	"encoding/json"
	"strings"
	"testing"
)

func TestIssueAssignmentSnapshotRoundTripPreservesContext(t *testing.T) {
	description := "Frozen description"
	contextJSON, err := withIssueAssignmentSnapshot(
		[]byte(`{"execution_config":{"model":"queued-model","snapshotted":true},"other":"kept"}`),
		issueAssignmentSnapshotContext{
			Version:            issueAssignmentSnapshotVersion,
			Title:              "Frozen title",
			Description:        &description,
			AcceptanceCriteria: []string{"First criterion"},
			Metadata:           map[string]any{"owner": "backend"},
			CommentCount:       2,
		},
	)
	if err != nil {
		t.Fatalf("withIssueAssignmentSnapshot: %v", err)
	}
	for _, key := range []string{"execution_config", "other", issueAssignmentSnapshotKey} {
		if !containsJSONKey(contextJSON, key) {
			t.Fatalf("context key %q was not preserved: %s", key, contextJSON)
		}
	}

	snapshot, found, err := IssueAssignmentSnapshotFromContext(contextJSON)
	if err != nil {
		t.Fatalf("IssueAssignmentSnapshotFromContext: %v", err)
	}
	if !found {
		t.Fatal("snapshot was not found")
	}
	if snapshot.Title != "Frozen title" || snapshot.Status != "" || snapshot.CommentCount != 2 {
		t.Fatalf("snapshot = %#v", snapshot)
	}
	if snapshot.Description == nil || *snapshot.Description != description {
		t.Fatalf("description = %#v", snapshot.Description)
	}
}

func TestIssueAssignmentSnapshotHistoricalAbsenceFallsBack(t *testing.T) {
	for _, contextJSON := range [][]byte{nil, []byte(`{}`), []byte(`{"execution_config":{}}`)} {
		snapshot, found, err := IssueAssignmentSnapshotFromContext(contextJSON)
		if err != nil || found {
			t.Fatalf("context %q returned snapshot=%#v found=%v err=%v", contextJSON, snapshot, found, err)
		}
	}
}

func TestIssueAssignmentSnapshotRejectsInvalidPresentPayload(t *testing.T) {
	valid := map[string]any{
		"version":             issueAssignmentSnapshotVersion,
		"title":               "Title",
		"description":         nil,
		"acceptance_criteria": []any{},
		"metadata":            map[string]any{},
		"comment_count":       float64(0),
	}
	tests := []struct {
		name   string
		mutate func(map[string]any)
		want   string
	}{
		{"unsupported version", func(v map[string]any) { v["version"] = float64(99) }, "unsupported"},
		{"empty title", func(v map[string]any) { v["title"] = "" }, "title is empty"},
		{"null acceptance criteria", func(v map[string]any) { v["acceptance_criteria"] = nil }, "acceptance criteria is null"},
		{"null metadata", func(v map[string]any) { v["metadata"] = nil }, "metadata is null"},
		{"negative comment count", func(v map[string]any) { v["comment_count"] = float64(-1) }, "comment count is negative"},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			payload := make(map[string]any, len(valid))
			for key, value := range valid {
				payload[key] = value
			}
			tc.mutate(payload)
			raw, err := json.Marshal(map[string]any{issueAssignmentSnapshotKey: payload})
			if err != nil {
				t.Fatal(err)
			}
			_, found, err := IssueAssignmentSnapshotFromContext(raw)
			if !found || err == nil || !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("found=%v err=%v, want error containing %q", found, err, tc.want)
			}
		})
	}
}
