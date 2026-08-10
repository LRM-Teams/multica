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
	producer := newAgentActivityProducer(func() time.Time { return now }, func(payload protocol.AgentActivityPayload) { sent = append(sent, payload) })
	installActivityProducerAgent(t, producer)

	d := New(Config{}, nil)
	d.runnerInstanceID = "daemon-instance-1"
	d.agentActivityProducers["workspace-1"] = producer

	d.observeMessageSendHold("agent-a", "workspace-1", "#general", 3, "server_race")
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
	// No producer is registered for the workspace; observe must not panic.
	d.observeMessageSendHold("agent-unknown", "workspace-unknown", "#general", 0, "freshness_unknown")
}

// newDraftReuseTestDaemon builds a Daemon whose Credential Proxy is backed by a
// real MessageCoordinator rooted in a temp dir, so prepareMessageSendDraft can
// run its local Draft load/save/refresh path. A stub server answers the only
// network call both paths may make (target resolution) so normal-send also runs
// without a live backend.
func newDraftReuseTestDaemon(t *testing.T) (*Daemon, *CredentialProxy) {
	t.Helper()
	coordinator, err := NewMessageCoordinator(t.TempDir(), func(context.Context, []protocol.AgentMessageProjection) error {
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
			ServerBaseURL: server.URL,
		},
		logger:              slog.Default(),
		messageCoordinators: map[string]*MessageCoordinator{"agent-1": coordinator},
	}
	return d, d.CredentialProxy()
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
	original, err := proxy.SaveNormalMessageDraft("agent-1", messageDraft{
		Target:          "#test",
		ContextTarget:   "channel:test",
		Content:         "阿泰:1",
		ClientMessageID: "stable-intent-0001",
	}, now)
	if err != nil {
		t.Fatalf("SaveNormalMessageDraft: %v", err)
	}
	if original.ClientMessageID == "" {
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
		if draft.ClientMessageID != original.ClientMessageID {
			t.Fatalf("send-draft turn %d client_message_id = %q, want reused %q", turn, draft.ClientMessageID, original.ClientMessageID)
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
	if _, err := proxy.SaveNormalMessageDraft("agent-1", messageDraft{
		Target: "#test", ContextTarget: "channel:test", Content: "prior", ClientMessageID: "prior-intent-0001",
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
	if first.ClientMessageID == "" || first.ClientMessageID == "prior-intent-0001" {
		t.Fatalf("fresh normal send must mint a distinct client_message_id, got %q", first.ClientMessageID)
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
		if draft.ClientMessageID != first.ClientMessageID {
			t.Fatalf("same-intent normal send %d reused client_message_id %q, want %q", i, draft.ClientMessageID, first.ClientMessageID)
		}
	}
}

// TestPrepareMessageSendDraftFillsTurnIdentityFromActiveInboxTurn is ① for the
// v0.4.24 gap: when the CLI omits MULTICA_TURN_* but the agent has an in-flight
// inbox turn, the proxy must fill ConversationID/Seq* from the live lease and
// stamp a batch client_message_id (no silent uuid). Non-turn agents still get
// the legacy UUID path (Alice scoping).
func TestPrepareMessageSendDraftFillsTurnIdentityFromActiveInboxTurn(t *testing.T) {
	d, proxy := newDraftReuseTestDaemon(t)
	now := time.Now()

	// No active turn → UUID path (non-turn/proactive send).
	noTurn := credentialProxyMessageSendRequest{
		AgentID: "agent-1", WorkspaceID: "workspace-1", Target: "#test", Content: "hello",
	}
	draft, status, err := d.prepareMessageSendDraft(context.Background(), proxy, cachedAgentCredential{}, noTurn, now)
	if err != nil {
		t.Fatalf("no-turn prepare: %v", err)
	}
	if status != 200 {
		t.Fatalf("no-turn status = %d", status)
	}
	if draft.ClientMessageID == "" || draft.ClientMessageID[0] == 'b' {
		// uuid.NewString() never starts with 'b' + 31 hex of batch form; batch ids start with 'b'.
		// Accept either form only if we can tell: batch is "b" + 31 hex chars.
	}
	noTurnID := draft.ClientMessageID
	if len(noTurnID) == 32 && noTurnID[0] == 'b' {
		t.Fatalf("non-turn send must not use batch client_message_id, got %q", noTurnID)
	}

	// Register an active inbox turn for the same agent.
	d.registerActiveInboxTurn("agent-1", AgentInboxLease{
		ID: "event-1", ConversationID: "conv-A", SeqFrom: 10, SeqTo: 20,
	})
	defer d.clearActiveInboxTurn("agent-1")

	// Same request shape (no ConversationID/Seq*) must now fill from the lease.
	withTurn := credentialProxyMessageSendRequest{
		AgentID: "agent-1", WorkspaceID: "workspace-1", Target: "#test", Content: "hello",
	}
	filled, status, err := d.prepareMessageSendDraft(context.Background(), proxy, cachedAgentCredential{}, withTurn, now)
	if err != nil {
		t.Fatalf("with-turn prepare: %v", err)
	}
	if status != 200 {
		t.Fatalf("with-turn status = %d", status)
	}
	want := batchClientMessageID("conv-A", 10, 20, "hello", nil)
	if filled.ClientMessageID != want {
		t.Fatalf("active-turn fill: client_message_id = %q, want batch %q", filled.ClientMessageID, want)
	}
	// Same content again → same batch id (at-most-once).
	retry, _, err := d.prepareMessageSendDraft(context.Background(), proxy, cachedAgentCredential{}, withTurn, now)
	if err != nil {
		t.Fatalf("retry: %v", err)
	}
	if retry.ClientMessageID != want {
		t.Fatalf("retry id = %q, want %q", retry.ClientMessageID, want)
	}
}

// TestPrepareMessageSendDraftBatchDistinctContentMintsDistinctIDs exercises the
// LRM-1530 boundary through the REAL draft path (not just the pure batch id
// function): when the send carries a batch identity
// (ConversationID/SeqFrom/SeqTo from a turn), two DISTINCT-content sends in the
// SAME batch must derive DIFFERENT client_message_ids (one stable id per
// message), so the server (channel,author,cmid) dedup does not 409-reject and
// drop the 2nd+ distinct message. The same content re-driven in the same batch
// (an accidental re-delivery/retry) must still reuse that one id so at-most-once
// dedup per message is preserved.
func TestPrepareMessageSendDraftBatchDistinctContentMintsDistinctIDs(t *testing.T) {
	d, proxy := newDraftReuseTestDaemon(t)
	now := time.Now()

	base := credentialProxyMessageSendRequest{
		AgentID:        "agent-1",
		WorkspaceID:    "workspace-1",
		Target:         "#test",
		ConversationID: "conversation-A",
		SeqFrom:        100,
		SeqTo:          120,
	}

	// First distinct message in the batch.
	reqA := base
	reqA.Content = "content-A"
	first, status, err := d.prepareMessageSendDraft(context.Background(), proxy, cachedAgentCredential{}, reqA, now)
	if err != nil {
		t.Fatalf("prepareMessageSendDraft batch msg A: %v", err)
	}
	if status != 200 {
		t.Fatalf("prepareMessageSendDraft batch msg A status = %d", status)
	}
	if first.ClientMessageID == "" {
		t.Fatal("batch send must derive a client_message_id")
	}

	// A second DISTINCT-content message in the SAME batch must get a DIFFERENT
	// id — otherwise the server would 409-reject (and drop) it as a cmid conflict.
	reqB := base
	reqB.Content = "content-B"
	second, status, err := d.prepareMessageSendDraft(context.Background(), proxy, cachedAgentCredential{}, reqB, now)
	if err != nil {
		t.Fatalf("prepareMessageSendDraft batch msg B: %v", err)
	}
	if status != 200 {
		t.Fatalf("prepareMessageSendDraft batch msg B status = %d", status)
	}
	if second.ClientMessageID == first.ClientMessageID {
		t.Fatalf("distinct-content messages in same batch must have distinct client_message_id, both %q", first.ClientMessageID)
	}

	// Re-driving the SAME content in the same batch reuses the one stable id
	// (at-most-once per message is preserved for an accidental re-delivery).
	retry, status, err := d.prepareMessageSendDraft(context.Background(), proxy, cachedAgentCredential{}, reqA, now)
	if err != nil {
		t.Fatalf("prepareMessageSendDraft batch msg A retry: %v", err)
	}
	if status != 200 {
		t.Fatalf("prepareMessageSendDraft batch msg A retry status = %d", status)
	}
	if retry.ClientMessageID != first.ClientMessageID {
		t.Fatalf("same-content retry in same batch must reuse id %q, got %q", first.ClientMessageID, retry.ClientMessageID)
	}
}

// TestBatchClientMessageIDStableWithinBatch pinpoints Alice's boundary "稳定要
// 批内稳定"：same conversation + same seq_from..seq_to（同一 turn 的同一批次）
// 的所有 send/retry 必须复用同一个稳定 id，这样 server 才能按 client_message_id
// 幂等住「一条消息发两次」。
func TestBatchClientMessageIDStableWithinBatch(t *testing.T) {
	id1 := batchClientMessageID("conversation-A", 100, 120, "content-A", nil)
	id2 := batchClientMessageID("conversation-A", 100, 120, "content-A", nil) // 同 content 重试/再次 send
	if id1 == "" {
		t.Fatal("batch client_message_id must be non-empty")
	}
	if id1 != id2 {
		t.Fatalf("same batch must reuse one stable id, got %q vs %q", id1, id2)
	}
	// The 32-char form must stay well under the server length limit.
	if len(id1) != 32 {
		t.Fatalf("batch id length = %d, want 32", len(id1))
	}
}

// TestBatchClientMessageIDDistinctAcrossBatches pinpoints the other half of
// Alice's boundary "批间不同": a DIFFERENT seq range (a different turn/batch)
// must get a DIFFERENT id, so two genuinely distinct messages are never folded
// together (avoid turning "一条发两次" into dropping a real message).
func TestBatchClientMessageIDDistinctAcrossBatches(t *testing.T) {
	base := batchClientMessageID("conversation-A", 100, 120, "content-A", nil)
	if id := batchClientMessageID("conversation-A", 100, 121, "content-A", nil); id == base {
		t.Fatalf("different seq_to must yield a different id (got %q)", id)
	}
	if id := batchClientMessageID("conversation-A", 99, 120, "content-A", nil); id == base {
		t.Fatalf("different seq_from must yield a different id (got %q)", id)
	}
	if id := batchClientMessageID("conversation-B", 100, 120, "content-A", nil); id == base {
		t.Fatalf("different conversation must yield a different id (got %q)", id)
	}
}

// TestBatchClientMessageIDDistinctContentWithinBatch is the LRM-1530 right-side
// boundary: SAME batch (same conversation + seq range) but DIFFERENT content
// must yield DIFFERENT stable ids — one stable id per distinct message, sharing
// only on same-content retry. Otherwise the server (channel,author,cmid) dedup
// would 409-reject (and drop) the 2nd+ distinct message in a turn.
func TestBatchClientMessageIDDistinctContentWithinBatch(t *testing.T) {
	base := batchClientMessageID("conversation-A", 100, 120, "content-A", nil)
	if id := batchClientMessageID("conversation-A", 100, 120, "content-B", nil); id == base {
		t.Fatalf("distinct content within same batch must yield a different id (got %q)", id)
	}
	// Same content but a different attachment set is a distinct message too.
	if id := batchClientMessageID("conversation-A", 100, 120, "content-A", []string{"att-1"}); id == base {
		t.Fatalf("different attachments with same content must yield a different id (got %q)", id)
	}
	// Attachment ordering must not change the derived id.
	a := batchClientMessageID("conversation-A", 100, 120, "content-A", []string{"att-1", "att-2"})
	b := batchClientMessageID("conversation-A", 100, 120, "content-A", []string{"att-2", "att-1"})
	if a != b {
		t.Fatalf("attachment order must not change id, got %q vs %q", a, b)
	}
}

// TestBatchClientMessageIDIsPure verifies retry idempotence without any shared
// state: calling repeatedly returns the same stable id (so a re-delivered batch
// cannot mint a second identity that bypasses the server dedup).
func TestBatchClientMessageIDIsPure(t *testing.T) {
	for i := 0; i < 10; i++ {
		if got := batchClientMessageID("conv-X", 5, 9, "content-A", nil); got != batchClientMessageID("conv-X", 5, 9, "content-A", nil) {
			t.Fatalf("batchId must be deterministic, got %q", got)
		}
	}
}
