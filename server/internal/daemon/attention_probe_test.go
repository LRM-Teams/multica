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

func TestParseAttentionProbeOutputRejectsMalformedOrOutOfContractFields(t *testing.T) {
	cases := []string{
		`{"decision":"SILENT","confidence":"high"}`,
		`{"decision":"CONTRIBUTE","evidence_refs":"not-an-array"}`,
		`{"decision":"ANSWER","seen_up_to_seq":-5}`,
		`{"decision":"ANSWER","confidence":7}`,
		`{"decision":"ANSWER","value_type":"invented"}`,
		`{"decision":"ANSWER","unknown":true}`,
	}
	for _, c := range cases {
		if _, ok, _ := ParseAttentionProbeOutput(c); ok {
			t.Fatalf("out-of-contract case %q unexpectedly parsed", c)
		}
	}
	if dec, ok, issues := ParseAttentionProbeOutput(`{"decision":"COORDINATE"}`); !ok || dec.Decision != "COORDINATE" {
		t.Fatalf("minimal valid object failed: decision=%q issues=%v", dec.Decision, issues)
	}
}

func TestParseAttentionProbeOutputRejectsWrappingAndTrailingContent(t *testing.T) {
	cases := []string{
		"Here is my judgment:\n```json\n{\"decision\":\"ANSWER\",\"summary\":\"I can help\"}\n```\n",
		"What I think: {\"decision\":\"SILENT\"} (nothing more)",
		"\n\n{\"decision\":\"CONTRIBUTE\",\"value_type\":\"correction\"}\n\nthanks",
	}
	for _, c := range cases {
		if _, ok, _ := ParseAttentionProbeOutput(c); ok {
			t.Fatalf("wrapped case %q unexpectedly parsed", c)
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
