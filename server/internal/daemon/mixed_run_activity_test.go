package daemon

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/multica-ai/multica/server/pkg/protocol"
)

func TestMixedRunActivityOutboxPersistsBeforeSendReplaysAfterRestartAndClearsOnAck(t *testing.T) {
	const (
		workspaceID = "11111111-1111-4111-8111-111111111111"
		agentID     = "22222222-2222-4222-8222-222222222222"
		runtimeID   = "33333333-3333-4333-8333-333333333333"
		runID       = "44444444-4444-4444-8444-444444444444"
		runAgentID  = "55555555-5555-4555-8555-555555555555"
		transition  = "turn:stable:active:start"
	)
	root := t.TempDir()
	configure := func(d *Daemon) {
		d.mu.Lock()
		d.runtimeIndex[runtimeID] = Runtime{ID: runtimeID, WorkspaceID: workspaceID}
		d.mu.Unlock()
	}

	// With no Runner connection the transition is still durably queued before
	// any send is attempted.
	first := New(Config{WorkspacesRoot: root}, nil)
	configure(first)
	if !first.reportMixedRunActivity(agentID, runtimeID, runID, runAgentID, transition, protocol.MixedRunActivityActiveTurn, 1) {
		t.Fatal("durably queued transition was reported as lost")
	}
	data, err := os.ReadFile(filepath.Join(root, ".multica", "mixed-run-activity-outbox.json"))
	if err != nil {
		t.Fatalf("activity transition was not durably persisted: %v", err)
	}
	if !strings.Contains(string(data), transition) {
		t.Fatalf("durable outbox = %s, missing %q", data, transition)
	}
	if first.reportMixedRunActivity(agentID, runtimeID, runID, runAgentID, transition, protocol.MixedRunActivityActiveTurn, -1) {
		t.Fatal("same transition identity accepted with a colliding payload")
	}
	if first.reportMixedRunActivity(agentID, "unknown-runtime", runID, runAgentID, transition, protocol.MixedRunActivityActiveTurn, 1) {
		t.Fatal("transition without a known workspace was reported as queued")
	}

	restarted := New(Config{WorkspacesRoot: root}, nil)
	configure(restarted)
	var replayed []protocol.MixedRunActivityTransitionPayload
	restarted.replayMixedRunActivity(workspaceID, func(eventType string, payload any) error {
		if eventType == protocol.EventMixedRunActivityTransition {
			replayed = append(replayed, payload.(protocol.MixedRunActivityTransitionPayload))
		}
		return nil
	})
	if len(replayed) != 1 || replayed[0].TransitionID != transition || replayed[0].Delta != 1 {
		t.Fatalf("restart replay = %+v, want the original transition exactly once", replayed)
	}

	// A failed send stops the replay; the entry stays durable and is retried on
	// the next attachment.
	replayed = nil
	restarted.replayMixedRunActivity(workspaceID, func(string, any) error {
		return errors.New("injected transport failure")
	})
	restarted.replayMixedRunActivity(workspaceID, func(eventType string, payload any) error {
		if eventType == protocol.EventMixedRunActivityTransition {
			replayed = append(replayed, payload.(protocol.MixedRunActivityTransitionPayload))
		}
		return nil
	})
	if len(replayed) != 1 || replayed[0].TransitionID != transition {
		t.Fatalf("replay after transport failure = %+v, want the durable transition retried", replayed)
	}
	if err := restarted.ackMixedRunActivity(protocol.MixedRunActivityTransitionAckPayload{RunID: runID, TransitionID: transition}); err != nil {
		t.Fatalf("commit activity acknowledgement: %v", err)
	}

	afterAckRestart := New(Config{WorkspacesRoot: root}, nil)
	configure(afterAckRestart)
	replaysAfterAck := 0
	afterAckRestart.replayMixedRunActivity(workspaceID, func(eventType string, _ any) error {
		if eventType == protocol.EventMixedRunActivityTransition {
			replaysAfterAck++
		}
		return nil
	})
	if replaysAfterAck != 0 {
		t.Fatalf("acknowledged transition replayed %d times", replaysAfterAck)
	}
}
