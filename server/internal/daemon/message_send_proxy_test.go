package daemon

import (
	"context"
	"encoding/json"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/multica-ai/multica/server/pkg/protocol"
)

// TestMessageSendHoldPresentationContract pins the warning-row strings that the
// frontend Activity tab renders for a soft-held send. These are the values the
// FE test (dax/fe-soft-hold-activity) asserts against, so any change here must
// be coordinated with the FE test copy.
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

// TestPrepareMessageSendDraftNewNormalSendMintsFreshKey documents the contrast:
// a brand-new normal send (no held Draft being replayed) mints a fresh uuid. It
// is specifically the send-draft replay path that must preserve identity — a
// held re-send can never be mistaken for a genuinely new intent.
func TestPrepareMessageSendDraftNewNormalSendMintsFreshKey(t *testing.T) {
	d, proxy := newDraftReuseTestDaemon(t)
	now := time.Now()

	// Seed a prior intent so we can assert a fresh normal send does not collide
	// with any existing client_message_id.
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
	seen := map[string]bool{"prior-intent-0001": true}
	for i := 0; i < 3; i++ {
		draft, status, err := d.prepareMessageSendDraft(context.Background(), proxy, cachedAgentCredential{}, request, now)
		if err != nil {
			t.Fatalf("prepareMessageSendDraft normal send %d: %v", i, err)
		}
		if status != 200 {
			t.Fatalf("prepareMessageSendDraft normal send %d status = %d", i, status)
		}
		if draft.ClientMessageID == "" {
			t.Fatalf("normal send %d must mint a client_message_id", i)
		}
		if seen[draft.ClientMessageID] {
			t.Fatalf("normal send %d reused an existing client_message_id %q", i, draft.ClientMessageID)
		}
		seen[draft.ClientMessageID] = true
	}
}
