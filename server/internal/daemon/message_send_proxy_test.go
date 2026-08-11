package daemon

import (
	"context"
	"encoding/json"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/multica-ai/multica/server/pkg/protocol"
)

// TestReuseClientMessageIDForIntentSameContent covers the positive case of the
// minimal fix: when a normal send re-drives the SAME intent (identical content)
// for a target that still holds an outstanding (non-expired, not-yet-cleared)
// local Draft, the client_message_id is reused instead of minting a fresh UUID
// that would bypass the server's (workspace,channel,author,client_message_id)
// dedup and duplicate the message (regression seq86/87).
func TestReuseClientMessageIDForIntentSameContent(t *testing.T) {
	existing := MessageDraft{
		Target:         "#test111222",
		Content:        "阿泰：1",
		IdempotencyKey: "stable-key-0001",
		SavedAt:        time.Date(2026, time.August, 8, 15, 30, 0, 0, time.UTC),
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
	existing := MessageDraft{
		Target:         "#test111222",
		Content:        "阿泰：1",
		IdempotencyKey: "stable-key-0002",
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
	existing := MessageDraft{
		Target:         "#test111222",
		Content:        "阿泰：1",
		IdempotencyKey: "stable-key-0003",
	}
	if _, ok := reuseClientMessageIDForIntent(existing, "阿泰：2"); ok {
		t.Fatalf("expected distinct content NOT to reuse client_message_id")
	}
}

// TestReuseClientMessageIDForIntentEmptyNew is the boundary (negative) case for
// an empty incoming send: no content to match, so no reuse — a fresh identity
// is minted (the request is invalid upstream anyway).
func TestReuseClientMessageIDForIntentEmptyNew(t *testing.T) {
	existing := MessageDraft{
		Target:         "#test111222",
		Content:        "阿泰：1",
		IdempotencyKey: "stable-key-0004",
	}
	if _, ok := reuseClientMessageIDForIntent(existing, ""); ok {
		t.Fatalf("expected empty content NOT to reuse client_message_id")
	}
}

// TestReuseClientMessageIDForIntentSurroundingWhitespaceIsTrimmed guards that
// the helper and the caller both normalize the same way, so a Draft saved with
// a trailing newline still matches a retry that trims it.
func TestReuseClientMessageIDForIntentSurroundingWhitespaceIsTrimmed(t *testing.T) {
	existing := MessageDraft{
		Target:         "#test111222",
		Content:        strings.TrimSpace("阿泰：1\n"),
		IdempotencyKey: "stable-key-0005",
	}
	if reused, ok := reuseClientMessageIDForIntent(existing, strings.TrimSpace("阿泰：1\n")); !ok || reused != "stable-key-0005" {
		t.Fatalf("expected trimmed same content to reuse key, got ok=%v reused=%q", ok, reused)
	}
}
func TestMessageSendHoldPresentationContract(t *testing.T) {
	if got := messageSendHoldTitle(); got != "Message held — review newer messages before sending" {
		t.Fatalf("messageSendHoldTitle=%q", got)
	}
	if got := messageSendHoldSubtext(3); got != "3 newer messages available — review then resend" {
		t.Fatalf("messageSendHoldSubtext(3)=%q", got)
	}
	if got := messageSendHoldSubtext(0); got != "Send held — review the channel before resending" {
		t.Fatalf("messageSendHoldSubtext(0)=%q", got)
	}
}

// TestObserveMessageSendHoldPublishesSystemActivityEntry verifies that observing
// a held send projects a fail-soft "system" activity entry (which the runner
// activity projection renders as a warning row with title/subtext) for the held
// agent, without erroring.
func TestObserveMessageSendHoldPublishesSystemActivityEntry(t *testing.T) {
	now := time.Date(2026, time.August, 8, 0, 0, 0, 0, time.UTC)
	var sent []protocol.AgentActivityPayload
	producer := newAgentActivityProducer("daemon-instance-1", func() time.Time { return now }, func(payload protocol.AgentActivityPayload) { sent = append(sent, payload) })
	installActivityProducerAgent(t, producer)

	d := New(Config{}, nil)
	d.runnerInstanceID = "daemon-instance-1"
	runner := installTestRunnerActivity(t, d, "workspace-1", producer)
	runner.processes.newID = func() string { return "launch-a" }
	if _, err := runner.processes.Start(agentProcessStartRequest{AgentID: "agent-a", RuntimeID: "runtime-1", StartDispatchID: "dispatch-a"}); err != nil {
		t.Fatal(err)
	}

	runner.observeMessageSendHold("agent-a", "#general", 3, "server_race")
	if len(sent) != 1 {
		t.Fatalf("sent payloads=%d, want 1", len(sent))
	}
	payload := sent[0]
	if payload.Snapshot.AgentID != "agent-a" {
		t.Fatalf("entry agent_id=%q, want agent-a", payload.Snapshot.AgentID)
	}
	if len(payload.Entries) != 1 {
		t.Fatalf("entries=%d, want 1 system entry", len(payload.Entries))
	}
	entry := payload.Entries[0]
	if entry.Kind != "system" {
		t.Fatalf("entry kind=%q, want system", entry.Kind)
	}
	var body protocol.AgentActivitySystemBody
	if err := json.Unmarshal(entry.Body, &body); err != nil {
		t.Fatalf("decode system entry body: %v", err)
	}
	if body.Title != messageSendHoldTitle() {
		t.Fatalf("entry title=%q, want %q", body.Title, messageSendHoldTitle())
	}
	if body.Text != messageSendHoldSubtext(3) {
		t.Fatalf("entry text=%q, want %q", body.Text, messageSendHoldSubtext(3))
	}
}

// TestObserveMessageSendHoldIsFailSoftWhenAgentNotManaged ensures the hold
// observation never errors or panics when the agent is not currently managed on
// this Runner; the projection is best-effort and the send outcome is untouched.
func TestObserveMessageSendHoldIsFailSoftWhenAgentNotManaged(t *testing.T) {
	d := New(Config{}, nil)
	d.runnerInstanceID = "daemon-instance-1"
	runner := installTestRunnerActivity(t, d, "workspace-unknown", newAgentActivityProducer("daemon-instance-1", time.Now, nil))
	// No managed launch exists for the Agent; observe must not panic.
	runner.observeMessageSendHold("agent-unknown", "#general", 0, "freshness_unknown")
}

// newDraftReuseTestDaemon builds a Daemon whose Credential Proxy is backed by a
// real MessageCoordinator rooted in a temp dir, so prepareMessageSendDraft can
// run its local Draft load/save/refresh path. A stub server answers the only
// network call both paths may make (target resolution) so normal-send also runs
// without a live backend.
func newDraftReuseTestDaemon(t *testing.T) (*Daemon, *CredentialProxy) {
	t.Helper()
	root := t.TempDir()
	coordinator, err := newTestMessageCoordinator(t, t.TempDir(), func(context.Context, []protocol.AgentMessageProjection) error {
		return nil
	}, nil)
	if err != nil {
		t.Fatalf("NewMessageCoordinator: %v", err)
	}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/agent/messages/target" {
			http.NotFound(w, r)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]string{"target": "#test", "context_target": "channel:test"})
	}))
	t.Cleanup(server.Close)
	d := &Daemon{
		cfg: Config{
			ServerBaseURL:  server.URL,
			WorkspacesRoot: root,
		},
		logger:            slog.Default(),
		messageDraftStore: NewMessageDraftStore(root),
	}
	registerTestInbox(t, d, InboxKey{WorkspaceID: "workspace-1", AgentID: "agent-1"}, "runtime-1", coordinator)
	return d, d.CredentialProxy()
}

func TestCredentialProxyDraftStoreDoesNotRequireMessageCoordinator(t *testing.T) {
	root := t.TempDir()
	d := &Daemon{
		cfg:               Config{WorkspacesRoot: root},
		messageDraftStore: NewMessageDraftStore(root),
	}
	proxy := d.CredentialProxy()
	now := time.Date(2026, time.August, 10, 12, 0, 0, 0, time.UTC)

	saved, err := proxy.SaveNormalMessageDraft("workspace-1", "agent-1", MessageDraft{
		Target: "#test", Content: "hello", IdempotencyKey: "stable-intent-1",
	}, now)
	if err != nil {
		t.Fatalf("SaveNormalMessageDraft: %v", err)
	}
	loaded, found, err := proxy.LoadMessageDraft("workspace-1", "agent-1", "#test", now.Add(time.Minute))
	if err != nil || !found {
		t.Fatalf("LoadMessageDraft: found=%v err=%v", found, err)
	}
	if loaded.IdempotencyKey != saved.IdempotencyKey || loaded.Content != saved.Content {
		t.Fatalf("loaded Draft = %+v, want %+v", loaded, saved)
	}
	if _, err := proxy.SaveNormalMessageDraft("workspace-1", "agent-1", MessageDraft{
		Target: "#test", Content: "replacement", IdempotencyKey: "stable-intent-2",
	}, now.Add(2*time.Minute)); err != nil {
		t.Fatalf("replace Draft: %v", err)
	}
	if err := proxy.ClearMessageDraft("workspace-1", "agent-1", "#test", saved.IdempotencyKey); err != nil {
		t.Fatalf("stale ClearMessageDraft: %v", err)
	}
	current, found, err := proxy.LoadMessageDraft("workspace-1", "agent-1", "#test", now.Add(3*time.Minute))
	if err != nil || !found || current.IdempotencyKey != "stable-intent-2" {
		t.Fatalf("replacement after stale clear = %+v found=%v err=%v", current, found, err)
	}
}

// TestPrepareMessageSendDraftSendDraftReusesClientMessageID locks the core
// invariant behind the seq86/87 duplicate fix (LRM-1526): explicit send-draft
// replay (freshness / local_hold → Draft → re-send) must reuse the originally
// saved client_message_id across turns instead of minting a new uuid on every
// retry. Reusing the stable identity is what lets the server-side
// (workspace, channel, author, client_message_id) unique-key dedup collapse the
// replayed delivery into one canonical Message.
func TestPrepareMessageSendDraftSendDraftReusesClientMessageID(t *testing.T) {
	d, proxy := newDraftReuseTestDaemon(t)
	now := time.Now()

	// First delivery of this intent is a normal send that gets held as a local
	// Draft (the freshness / local_hold path) carrying its identity-bearing
	// client_message_id.
	original, err := proxy.SaveNormalMessageDraft("workspace-1", "agent-1", MessageDraft{
		Target:         "#test",
		ContextTarget:  "channel:test",
		Content:        "阿泰:1",
		IdempotencyKey: "stable-intent-0001",
	}, now)
	if err != nil {
		t.Fatalf("SaveNormalMessageDraft: %v", err)
	}
	if original.IdempotencyKey == "" {
		t.Fatal("saved Draft must carry a client_message_id")
	}

	// The held delivery is replayed as an explicit send-draft across several
	// turns. Each replay must resolve to the SAME saved client_message_id and
	// must never mint a fresh uuid.
	request := credentialProxyMessageSendRequest{
		AgentID:     "agent-1",
		WorkspaceID: "workspace-1",
		Target:      "#test",
		SendDraft:   true,
	}
	for turn := 0; turn < 3; turn++ {
		draft, status, err := d.prepareMessageSendDraft(context.Background(), proxy, cachedAgentCredential{}, request, now)
		if err != nil {
			t.Fatalf("prepareMessageSendDraft send-draft turn %d: %v", turn, err)
		}
		if status != 200 {
			t.Fatalf("prepareMessageSendDraft send-draft turn %d status = %d", turn, status)
		}
		if draft.IdempotencyKey != original.IdempotencyKey {
			t.Fatalf("send-draft turn %d client_message_id = %q, want reused %q", turn, draft.IdempotencyKey, original.IdempotencyKey)
		}
		if draft.Content != original.Content {
			t.Fatalf("send-draft turn %d content = %q, want %q (payload must not be mutated)", turn, draft.Content, original.Content)
		}
	}
}

// TestPrepareMessageSendDraftNormalSendReusesKeyOnSameIntent pins the LRM-1526
// final behavior for normal sends, which supersedes the earlier send-draft-only
// framing: a normal send to the same target keeps reusing the stable
// client_message_id while it re-drives the same content (same intent), so the
// server (workspace,channel,author,client_message_id) dedup collapses the
// retry into one canonical Message (regression seq86/87). A DIFFERENT content
// (different intent) is still a genuinely new send and mints a fresh key — it
// must never collide with a prior intent.
func TestPrepareMessageSendDraftNormalSendReusesKeyOnSameIntent(t *testing.T) {
	d, proxy := newDraftReuseTestDaemon(t)
	now := time.Now()

	// Seed a prior DISTINCT intent so we can assert a fresh normal send of other
	// content does not collide with any existing client_message_id.
	if _, err := proxy.SaveNormalMessageDraft("workspace-1", "agent-1", MessageDraft{
		Target: "#test", ContextTarget: "channel:test", Content: "prior", IdempotencyKey: "prior-intent-0001",
	}, now); err != nil {
		t.Fatalf("SaveNormalMessageDraft: %v", err)
	}

	request := credentialProxyMessageSendRequest{
		AgentID:     "agent-1",
		WorkspaceID: "workspace-1",
		Target:      "#test",
		Content:     "阿泰:2",
	}
	// First normal send of this content mints a fresh identity (distinct from
	// the seeded 'prior' draft — different content = new intent).
	first, status, err := d.prepareMessageSendDraft(context.Background(), proxy, cachedAgentCredential{}, request, now)
	if err != nil {
		t.Fatalf("prepareMessageSendDraft normal send: %v", err)
	}
	if status != 200 {
		t.Fatalf("prepareMessageSendDraft normal send status = %d", status)
	}
	if first.IdempotencyKey == "" || first.IdempotencyKey == "prior-intent-0001" {
		t.Fatalf("fresh normal send must mint a distinct client_message_id, got %q", first.IdempotencyKey)
	}

	// Re-driving the SAME content (same intent) reuses the stable key instead of
	// minting another uuid — this is the seq86/87 dedup fix.
	for i := 0; i < 3; i++ {
		draft, status, err := d.prepareMessageSendDraft(context.Background(), proxy, cachedAgentCredential{}, request, now)
		if err != nil {
			t.Fatalf("prepareMessageSendDraft normal send %d: %v", i, err)
		}
		if status != 200 {
			t.Fatalf("prepareMessageSendDraft normal send %d status = %d", i, status)
		}
		if draft.IdempotencyKey != first.IdempotencyKey {
			t.Fatalf("same-intent normal send %d reused client_message_id %q, want %q", i, draft.IdempotencyKey, first.IdempotencyKey)
		}
	}
}

// TestPrepareMessageSendDraftNeverUsesBatchFormClientMessageID locks the
// Raft-aligned decision: chat send identities are independent uuids (or draft
// reuse), never turn-coordinate batch ids (former "b"+hex form).
func TestPrepareMessageSendDraftNeverUsesBatchFormClientMessageID(t *testing.T) {
	d, proxy := newDraftReuseTestDaemon(t)
	now := time.Now()
	req := credentialProxyMessageSendRequest{
		AgentID: "agent-1", WorkspaceID: "workspace-1", Target: "#test", Content: "hello",
	}
	draft, status, err := d.prepareMessageSendDraft(context.Background(), proxy, cachedAgentCredential{}, req, now)
	if err != nil {
		t.Fatalf("prepare: %v", err)
	}
	if status != 200 {
		t.Fatalf("status = %d", status)
	}
	if draft.IdempotencyKey == "" {
		t.Fatal("expected a client_message_id")
	}
	// Former batch ids were "b" + 31 hex chars (len 32). UUIDs use hyphens.
	if len(draft.IdempotencyKey) == 32 && draft.IdempotencyKey[0] == 'b' {
		t.Fatalf("must not mint batch-form client_message_id, got %q", draft.IdempotencyKey)
	}
}
