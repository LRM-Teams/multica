package daemon

import (
	"encoding/json"
	"fmt"
	"regexp"
	"strings"
)

// AttentionDecision is the structured result of one agent's cheap "should I
// participate?" probe. It mirrors the bounded contract an agent returns with a
// tiny, tool-free context window. Optional fields must never make a probe fail:
// only Decision drives routing, everything else is best-effort metadata.
//
// This is deliberately lenient about optional fields compared to the earlier
// strict implementation. A malformed confidence, evidence array, or summary must
// not turn a valid decision into a failed probe (CodexLoom principle: one
// agent's parse trouble must not flip that agent's opportunity, and never the
// whole round).
type AttentionDecision struct {
	// Decision is the only required field: SILENT | ANSWER | CONTRIBUTE | COORDINATE.
	Decision     string   `json:"decision"`
	Confidence   *float64 `json:"confidence,omitempty"`
	ValueType    string   `json:"value_type,omitempty"`
	Summary      string   `json:"summary,omitempty"`
	EvidenceRefs []string `json:"evidence_refs,omitempty"`
	ModelVersion string   `json:"model_version,omitempty"`
	SeenUpToSeq  *int64   `json:"seen_up_to_seq,omitempty"`
}

// AttentionDecisionValues lists the valid decisions an agent may return.
var AttentionDecisionValues = []string{"SILENT", "ANSWER", "CONTRIBUTE", "COORDINATE"}

// errAttentionProbeUnusable is returned when an attention-probe execution
// produced output that could not be turned into a usable decision. The caller
// decides that single agent's fate; it must never auto-silence other agents.
var errAttentionProbeUnusable = fmt.Errorf("attention probe output not usable")

// ValidAttentionDecision reports whether d is a known decision value.
func ValidAttentionDecision(d string) bool {
	for _, v := range AttentionDecisionValues {
		if d == v {
			return true
		}
	}
	return false
}

// attentionProbeJSONObjectRE matches a braced JSON object even when the model
// wraps it in prose or a markdown code fence. A positioned scan keeps this
// robust against leading/trailing chatter.
var attentionProbeJSONObjectRE = regexp.MustCompile(`(?s)\{.*\}`)

// ParseAttentionProbeOutput converts an LLM attention-probe response into a
// structured AttentionDecision in a fault-tolerant way:
//
//   - It looks for the first JSON object in the response (tolerating prose,
//     markdown fences, and trailing notes).
//   - Decision is required and must be a known value; if it is missing or
//     invalid the parse fails (the caller decides that agent's fate — never
//     the round's as a whole).
//   - Every other field is best-effort: a malformed optional field is dropped
//     and the parse still succeeds, so a single noisy output does not block
//     the decision.
//
// The returned completed flag reports whether the decision was fully usable;
// issues holds non-fatal observations for audit only.
func ParseAttentionProbeOutput(output string) (AttentionDecision, bool, []string) {
	var issues []string
	cleaned := strings.TrimSpace(output)

	// If it is already a plain JSON object, parse it directly; otherwise scan
	// for the first braced object to tolerate prose/fences/trailing chatter.
	var rawMap map[string]json.RawMessage
	if err := json.Unmarshal([]byte(cleaned), &rawMap); err != nil || rawMap == nil {
		m := attentionProbeJSONObjectRE.FindString(cleaned)
		if m == "" {
			issues = append(issues, "no JSON object found in probe output")
			return AttentionDecision{}, false, issues
		}
		if err := json.Unmarshal([]byte(m), &rawMap); err != nil || rawMap == nil {
			issues = append(issues, "probe output is not a JSON object")
			return AttentionDecision{}, false, issues
		}
	}

	decision := strings.ToUpper(strings.TrimSpace(attentionJSONString(rawMap, "decision")))
	if !ValidAttentionDecision(decision) {
		issues = append(issues, fmt.Sprintf("invalid or missing decision %q", decision))
		return AttentionDecision{}, false, issues
	}

	out := AttentionDecision{Decision: decision}
	if v, ok := attentionJSONFloat(rawMap, "confidence"); ok {
		out.Confidence = &v
	} else if _, present := rawMap["confidence"]; present {
		issues = append(issues, "confidence ignored: not a number")
	}
	out.ValueType = attentionJSONString(rawMap, "value_type")
	out.Summary = strings.TrimSpace(attentionJSONString(rawMap, "summary"))
	if refs, ok := attentionJSONStringSlice(rawMap, "evidence_refs"); ok {
		out.EvidenceRefs = refs
	} else if _, present := rawMap["evidence_refs"]; present {
		issues = append(issues, "evidence_refs ignored: not an array of strings")
	}
	out.ModelVersion = strings.TrimSpace(attentionJSONString(rawMap, "model_version"))
	if v, ok := attentionJSONInt(rawMap, "seen_up_to_seq"); ok {
		out.SeenUpToSeq = &v
	} else if _, present := rawMap["seen_up_to_seq"]; present {
		issues = append(issues, "seen_up_to_seq ignored: not a non-negative integer")
	}

	return out, true, issues
}

// CanonicalJSON serializes a successfully parsed decision into the canonical
// stored shape, or returns an error if the decision is not usable.
func (d AttentionDecision) CanonicalJSON() (json.RawMessage, error) {
	if !ValidAttentionDecision(d.Decision) {
		return nil, fmt.Errorf("attention probe: invalid decision %q", d.Decision)
	}
	// Always emit decision; keep outputs stable and predictable for receivers.
	fixed := struct {
		Decision     string   `json:"decision"`
		Confidence   *float64 `json:"confidence,omitempty"`
		ValueType    string   `json:"value_type,omitempty"`
		Summary      string   `json:"summary,omitempty"`
		EvidenceRefs []string `json:"evidence_refs,omitempty"`
		ModelVersion string   `json:"model_version,omitempty"`
		SeenUpToSeq  *int64   `json:"seen_up_to_seq,omitempty"`
	}{
		Decision:     d.Decision,
		Confidence:   d.Confidence,
		ValueType:    d.ValueType,
		Summary:      d.Summary,
		EvidenceRefs: d.EvidenceRefs,
		ModelVersion: d.ModelVersion,
		SeenUpToSeq:  d.SeenUpToSeq,
	}
	return json.Marshal(fixed)
}

// --- lenient field extractors -------------------------------------------------
// These are attention-specific helpers. They are prefixed attentionJSON* so
// they do not collide with the existing jsonString/jsonStringSlice helpers in
// this package (which have different, single-value signatures).

func attentionJSONString(m map[string]json.RawMessage, key string) string {
	raw, ok := m[key]
	if !ok {
		return ""
	}
	var s string
	if err := json.Unmarshal(raw, &s); err != nil {
		return ""
	}
	return s
}

func attentionJSONFloat(m map[string]json.RawMessage, key string) (float64, bool) {
	raw, ok := m[key]
	if !ok {
		return 0, false
	}
	var f float64
	if err := json.Unmarshal(raw, &f); err != nil {
		return 0, false
	}
	// Clamp to [0,1] for a confidence value.
	if f < 0 {
		f = 0
	}
	if f > 1 {
		f = 1
	}
	return f, true
}

func attentionJSONInt(m map[string]json.RawMessage, key string) (int64, bool) {
	raw, ok := m[key]
	if !ok {
		return 0, false
	}
	var i int64
	if err := json.Unmarshal(raw, &i); err != nil || i < 0 {
		return 0, false
	}
	return i, true
}

func attentionJSONStringSlice(m map[string]json.RawMessage, key string) ([]string, bool) {
	raw, ok := m[key]
	if !ok {
		return nil, false
	}
	var arr []json.RawMessage
	if err := json.Unmarshal(raw, &arr); err != nil {
		return nil, false
	}
	out := make([]string, 0, len(arr))
	for _, item := range arr {
		var s string
		if err := json.Unmarshal(item, &s); err != nil {
			return nil, false
		}
		out = append(out, s)
	}
	return out, true
}
