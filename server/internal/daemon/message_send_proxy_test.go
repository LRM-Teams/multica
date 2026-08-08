package daemon

import (
	"strings"
	"testing"
	"time"
)

// TestReuseClientMessageIDForIntentSameContent covers the positive case of the
// minimal fix: when a normal send re-drives the SAME intent (identical content)
// for a target that still holds an outstanding (non-expired, not-yet-cleared)
// local Draft, the client_message_id is reused instead of minting a fresh UUID
// that would bypass the server's (workspace,channel,author,client_message_id)
// dedup and duplicate the message (regression seq86/87).
func TestReuseClientMessageIDForIntentSameContent(t *testing.T) {
	existing := messageDraft{
		Target:          "#test111222",
		Content:         "阿泰：1",
		ClientMessageID: "stable-key-0001",
		SavedAt:         time.Date(2026, time.August, 8, 15, 30, 0, 0, time.UTC),
	}
	reused, ok := reuseClientMessageIDForIntent(existing, "阿泰：1")
	if !ok {
		t.Fatalf("expected same-content intent to reuse client_message_id, got reuse=false")
	}
	if reused != "stable-key-0001" {
		t.Fatalf("reused client_message_id = %q, want %q", reused, "stable-key-0001")
	}
}

// TestReuseClientMessageIDForIntentTrimsContent ensures content matching is
// normalization-agnostic to surrounding whitespace, matching how the daemon
// trims request content before saving a Draft.
func TestReuseClientMessageIDForIntentTrimsContent(t *testing.T) {
	existing := messageDraft{
		Target:          "#test111222",
		Content:         "阿泰：1",
		ClientMessageID: "stable-key-0002",
	}
	reused, ok := reuseClientMessageIDForIntent(existing, "  阿泰：1  \n")
	if !ok {
		t.Fatalf("expected trimmed same-content to reuse client_message_id")
	}
	if reused != "stable-key-0002" {
		t.Fatalf("reused client_message_id = %q, want %q", reused, "stable-key-0002")
	}
}

// TestReuseClientMessageIDForIntentDistinctContent is the boundary (negative)
// case: a genuinely different intent (different content) to the same target
// must NOT reuse the outstanding Draft's identity — it is a new send and keeps
// a fresh client_message_id. This pins down the fix coverage boundary so the
// regression cannot "turn green" by over-collapsing distinct messages.
func TestReuseClientMessageIDForIntentDistinctContent(t *testing.T) {
	existing := messageDraft{
		Target:          "#test111222",
		Content:         "阿泰：1",
		ClientMessageID: "stable-key-0003",
	}
	if _, ok := reuseClientMessageIDForIntent(existing, "阿泰：2"); ok {
		t.Fatalf("expected distinct content NOT to reuse client_message_id")
	}
}

// TestReuseClientMessageIDForIntentEmptyNew is the boundary (negative) case for
// an empty incoming send: no content to match, so no reuse — a fresh identity
// is minted (the request is invalid upstream anyway).
func TestReuseClientMessageIDForIntentEmptyNew(t *testing.T) {
	existing := messageDraft{
		Target:          "#test111222",
		Content:         "阿泰：1",
		ClientMessageID: "stable-key-0004",
	}
	if _, ok := reuseClientMessageIDForIntent(existing, ""); ok {
		t.Fatalf("expected empty content NOT to reuse client_message_id")
	}
}

// TestReuseClientMessageIDForIntentSurroundingWhitespaceIsTrimmed guards that
// the helper and the caller both normalize the same way, so a Draft saved with
// a trailing newline still matches a retry that trims it.
func TestReuseClientMessageIDForIntentSurroundingWhitespaceIsTrimmed(t *testing.T) {
	existing := messageDraft{
		Target:          "#test111222",
		Content:         strings.TrimSpace("阿泰：1\n"),
		ClientMessageID: "stable-key-0005",
	}
	if reused, ok := reuseClientMessageIDForIntent(existing, strings.TrimSpace("阿泰：1\n")); !ok || reused != "stable-key-0005" {
		t.Fatalf("expected trimmed same content to reuse key, got ok=%v reused=%q", ok, reused)
	}
}
