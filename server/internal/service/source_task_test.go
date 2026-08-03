package service

import (
	"encoding/json"
	"strings"
	"testing"
)

func TestParseSourceTaskIssueCanonicalHashIsStable(t *testing.T) {
	first, err := ParseSourceTask("issue", json.RawMessage(`{"description":"d","title":"t"}`))
	if err != nil {
		t.Fatalf("parse first issue: %v", err)
	}
	second, err := ParseSourceTask("issue", json.RawMessage(`{"title":"t","description":"d"}`))
	if err != nil {
		t.Fatalf("parse second issue: %v", err)
	}
	if first.ContentHash == "" {
		t.Fatal("content hash is empty")
	}
	if first.ContentHash != second.ContentHash {
		t.Fatalf("canonical hashes differ: %q != %q", first.ContentHash, second.ContentHash)
	}
	if string(first.Payload) != string(second.Payload) {
		t.Fatalf("canonical payloads differ: %s != %s", first.Payload, second.Payload)
	}
}

func TestParseSourceTaskRejectsMalformedPayload(t *testing.T) {
	_, err := ParseSourceTask("message", json.RawMessage(`{"content":""}`))
	if err == nil || !strings.Contains(err.Error(), "content") {
		t.Fatalf("empty message error = %v, want content validation error", err)
	}
	_, err = ParseSourceTask("issue", json.RawMessage(`[]`))
	if err == nil || !strings.Contains(err.Error(), "object") {
		t.Fatalf("array issue error = %v, want object validation error", err)
	}
}

func TestParseSourceTaskRejectsMalformedSweLegoDate(t *testing.T) {
	_, err := ParseSourceTask("issue", json.RawMessage(`{"title":"t","description":"d","issue_date":"not-a-date"}`))
	if err == nil || !strings.Contains(err.Error(), "issue_date") {
		t.Fatalf("invalid issue_date error = %v", err)
	}
}
