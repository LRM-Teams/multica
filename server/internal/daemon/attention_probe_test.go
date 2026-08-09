package daemon

import (
	"encoding/json"
	"strings"
	"testing"
)

func TestParseAttentionProbeOutputCleanJSON(t *testing.T) {
	dec, ok, issues := ParseAttentionProbeOutput(`{"decision":"ANSWER","confidence":0.93,"value_type":"unique_evidence","summary":"the pool timeout is a config mismatch","evidence_refs":["memory:a","message-12"],"model_version":"some-model","seen_up_to_seq":42}`)
	if !ok {
		t.Fatalf("expected parse to succeed, issues=%v", issues)
	}
	if dec.Decision != "ANSWER" {
		t.Fatalf("decision=%q want ANSWER", dec.Decision)
	}
	if dec.Confidence == nil || *dec.Confidence != 0.93 {
		t.Fatalf("confidence not parsed: %v", dec.Confidence)
	}
	if dec.ValueType != "unique_evidence" {
		t.Fatalf("value_type=%q", dec.ValueType)
	}
	if len(dec.EvidenceRefs) != 2 || dec.EvidenceRefs[0] != "memory:a" {
		t.Fatalf("evidence_refs=%v", dec.EvidenceRefs)
	}
	if dec.SeenUpToSeq == nil || *dec.SeenUpToSeq != 42 {
		t.Fatalf("seen_up_to_seq=%v", dec.SeenUpToSeq)
	}
	if len(issues) != 0 {
		t.Fatalf("unexpected issues: %v", issues)
	}
}

// The old strict parser rejected these; the whole point of the rewrite is that
// a single noisy optional field must not fail the decision.
func TestParseAttentionProbeOutputToleratesMalformedOptionalFields(t *testing.T) {
	cases := []string{
		// confidence out of range string
		`{"decision":"SILENT","confidence":"high"}`,
		// evidence_refs wrong type
		`{"decision":"CONTRIBUTE","evidence_refs":"not-an-array"}`,
		// negative seen_up_to_seq
		`{"decision":"ANSWER","seen_up_to_seq":-5}`,
		// missing optional fields entirely
		`{"decision":"COORDINATE"}`,
		// confidence out of [0,1] numeric (should clamp, not fail)
		`{"decision":"ANSWER","confidence":7}`,
	}
	for _, c := range cases {
		dec, ok, issues := ParseAttentionProbeOutput(c)
		if !ok {
			t.Fatalf("case %q failed (want tolerant parse): issues=%v", c, issues)
		}
		if dec.Decision == "" || !ValidAttentionDecision(dec.Decision) {
			t.Fatalf("case %q produced invalid decision %q", c, dec.Decision)
		}
	}
}

// Tolerate prose / markdown fences / trailing chatter around the JSON object.
func TestParseAttentionProbeOutputToleratesWrapping(t *testing.T) {
	cases := []string{
		"Here is my judgment:\n```json\n{\"decision\":\"ANSWER\",\"summary\":\"I can help\"}\n```\n",
		"What I think: {\"decision\":\"SILENT\"} (nothing more)",
		"\n\n{\"decision\":\"CONTRIBUTE\",\"value_type\":\"correction\"}\n\nthanks",
	}
	for _, c := range cases {
		dec, ok, issues := ParseAttentionProbeOutput(c)
		if !ok {
			t.Fatalf("case %q failed: issues=%v", c, issues)
		}
		if !ValidAttentionDecision(dec.Decision) {
			t.Fatalf("case %q invalid decision %q", c, dec.Decision)
		}
		if len(issues) != 0 {
			t.Fatalf("case %q produced issues: %v", c, issues)
		}
	}
}

func TestParseAttentionProbeOutputFailsOnlyOnBadDecision(t *testing.T) {
	// Empty output
	if _, ok, _ := ParseAttentionProbeOutput(""); ok {
		t.Fatal("empty output should fail")
	}
	// No JSON object
	if _, ok, _ := ParseAttentionProbeOutput("just prose no decision at all"); ok {
		t.Fatal("prose without JSON should fail")
	}
	// Invalid decision value
	if _, ok, issues := ParseAttentionProbeOutput(`{"decision":"MAYBE"}`); ok {
		t.Fatal("unknown decision should fail")
	} else if !strings.Contains(strings.Join(issues, " "), "decision") {
		t.Fatalf("issues should mention decision: %v", issues)
	}
}

func TestAttentionDecisionCanonicalJSONStable(t *testing.T) {
	dec, ok, _ := ParseAttentionProbeOutput(`{"decision":"ANSWER","confidence":0.5,"summary":"x","value_type":"direct_answer"}`)
	if !ok {
		t.Fatal("parse failed")
	}
	raw, err := dec.CanonicalJSON()
	if err != nil {
		t.Fatalf("canonical json: %v", err)
	}
	var m map[string]any
	if err := json.Unmarshal(raw, &m); err != nil {
		t.Fatalf("canonical json unmarshal: %v", err)
	}
	if m["decision"] != "ANSWER" {
		t.Fatalf("decision = %v", m["decision"])
	}
	if conf, ok := m["confidence"].(float64); !ok || conf != 0.5 {
		t.Fatalf("confidence = %v (%T)", m["confidence"], m["confidence"])
	}
	if _, err := (AttentionDecision{Decision: "BOGUS"}).CanonicalJSON(); err == nil {
		t.Fatal("invalid decision should fail to canonicalize")
	}
}
