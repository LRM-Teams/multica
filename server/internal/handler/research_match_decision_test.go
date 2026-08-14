package handler

import (
	"encoding/json"
	"testing"

	db "github.com/multica-ai/multica/server/pkg/db/generated"
)

func TestExtractMatchDecisionFromMeta(t *testing.T) {
	msgID := "aaaaaaaa-aaaa-aaaa-aaaa-aaaaaaaaaaaa"
	conf := 0.82
	anchor := "bbbbbbbb-bbbb-bbbb-bbbb-bbbbbbbbbbbb"
	raw, _ := json.Marshal(map[string]any{
		"op": "chat",
		"match_decision": map[string]any{
			"confidence":             conf,
			"primary_anchor_node_id": anchor,
			"matched_node_ids":       []string{anchor, anchor, "cccccccc-cccc-cccc-cccc-cccccccccccc"},
			"decisions": []map[string]any{
				{"node_id": anchor, "action": "continue", "reason": "续研定价"},
				{"node_id": "cccccccc-cccc-cccc-cccc-cccccccccccc", "action": "deprecate", "reason": "方向不符"},
			},
		},
	})

	env := extractMatchDecisionFromMeta(json.RawMessage(raw), msgID)
	if env == nil {
		t.Fatal("expected envelope")
	}
	if env.UtteranceID != msgID {
		t.Fatalf("utterance_id default=%q", env.UtteranceID)
	}
	if env.Confidence == nil || *env.Confidence != conf {
		t.Fatalf("confidence=%v", env.Confidence)
	}
	if env.PrimaryAnchorNodeID == nil || *env.PrimaryAnchorNodeID != anchor {
		t.Fatalf("anchor=%v", env.PrimaryAnchorNodeID)
	}
	if len(env.MatchedNodeIDs) != 2 {
		t.Fatalf("matched dedupe=%v", env.MatchedNodeIDs)
	}
	if len(env.Decisions) != 2 || env.Decisions[0].Action != matchActionContinue || env.Decisions[1].Action != matchActionDeprecate {
		t.Fatalf("decisions=%+v", env.Decisions)
	}
}

func TestExtractMatchDecisionFromMetaOmitWhenMissing(t *testing.T) {
	if env := extractMatchDecisionFromMeta(json.RawMessage(`{}`), "x"); env != nil {
		t.Fatalf("empty meta must omit, got %+v", env)
	}
	if env := extractMatchDecisionFromMeta(json.RawMessage(`{"op":"chat"}`), "x"); env != nil {
		t.Fatalf("no key must omit, got %+v", env)
	}
}

func TestNormalizeMatchDecisionRejects(t *testing.T) {
	cases := []string{
		`{"decisions":[{"node_id":"n1","action":"continue"},{"node_id":"n2","action":"branch_after"}]}`,
		`{"decisions":[{"node_id":"n1","action":"deprecate"}]}`,
		`{"decisions":[{"node_id":"n1","action":"explode","reason":"x"}]}`,
		`{}`,
	}
	for _, c := range cases {
		if _, err := normalizeMatchDecision(json.RawMessage(c), "msg"); err == nil {
			t.Fatalf("expected reject for %s", c)
		}
	}
}

func TestMapMessagesProjectsMatchDecision(t *testing.T) {
	msgID := mustTestUUID("aaaaaaaa-aaaa-aaaa-aaaa-aaaaaaaaaaaa")
	sessionID := mustTestUUID("dddddddd-dddd-dddd-dddd-dddddddddddd")
	meta := []byte(`{"match_decision":{"decisions":[{"node_id":"n1","action":"pending_confirm","reason":"低置信"}],"matched_node_ids":["n1"]}}`)
	out := mapMessages([]db.ResearchMessage{{
		ID: msgID, SessionID: sessionID, SenderType: "user", Body: "改监管", CardKind: "chat", Meta: meta,
	}})
	if len(out) != 1 || out[0].MatchDecision == nil {
		t.Fatalf("expected projected match_decision, got %+v", out)
	}
	if out[0].MatchDecision.UtteranceID != uuidToString(msgID) {
		t.Fatalf("utterance_id=%q", out[0].MatchDecision.UtteranceID)
	}
	if out[0].MatchDecision.Decisions[0].Action != matchActionPendingConfirm {
		t.Fatalf("action=%q", out[0].MatchDecision.Decisions[0].Action)
	}

	plain := mapMessages([]db.ResearchMessage{{
		ID: msgID, SessionID: sessionID, SenderType: "user", Body: "hi", CardKind: "chat", Meta: []byte(`{}`),
	}})
	if plain[0].MatchDecision != nil {
		t.Fatalf("must omit when absent, got %+v", plain[0].MatchDecision)
	}
}
