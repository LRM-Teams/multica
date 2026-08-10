package daemon

import (
	"encoding/json"
	"fmt"
	"strings"
)

// AttentionDecision is the structured result of one agent's cheap "should I
// participate?" probe. It mirrors the bounded contract an agent returns with a
// tiny, tool-free context window. The entire object is schema-validated before
// Decision may influence routing; rejection remains isolated to this agent.
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

// AttentionValueTypeValues lists the optional value classifications accepted
// by the persistence contract.
var AttentionValueTypeValues = []string{"none", "direct_answer", "unique_evidence", "correction", "task_claim", "needs_protocol"}

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

// ParseAttentionProbeOutput converts an LLM attention-probe response into a
// structured AttentionDecision using the restricted-profile wire contract:
//
//   - the top-level value must be exactly one JSON object;
//   - unknown fields, malformed optional fields, and trailing prose fail closed;
//   - decision and persisted enum/range fields must satisfy the DB contract.
//
// The returned completed flag reports whether the decision was fully usable;
// issues holds the rejection reason for audit. A rejected probe affects only
// that participant; the round resolver never treats it as another agent's vote.
func ParseAttentionProbeOutput(output string) (AttentionDecision, bool, []string) {
	var out AttentionDecision
	if _, err := decodeStrictJSONObject(output, &out); err != nil {
		return AttentionDecision{}, false, []string{err.Error()}
	}
	out.Decision = strings.ToUpper(strings.TrimSpace(out.Decision))
	out.ValueType = strings.TrimSpace(out.ValueType)
	out.Summary = strings.TrimSpace(out.Summary)
	out.ModelVersion = strings.TrimSpace(out.ModelVersion)
	if err := out.validate(); err != nil {
		return AttentionDecision{}, false, []string{err.Error()}
	}
	return out, true, nil
}

// CanonicalJSON serializes a successfully parsed decision into the canonical
// stored shape, or returns an error if the decision is not usable.
func (d AttentionDecision) CanonicalJSON() (json.RawMessage, error) {
	if err := d.validate(); err != nil {
		return nil, fmt.Errorf("attention probe: %w", err)
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

func (d AttentionDecision) validate() error {
	if !ValidAttentionDecision(d.Decision) {
		return fmt.Errorf("invalid or missing decision %q", d.Decision)
	}
	if d.Confidence != nil && (*d.Confidence < 0 || *d.Confidence > 1) {
		return fmt.Errorf("confidence must be between 0 and 1")
	}
	if d.ValueType != "" && !containsAttentionValue(AttentionValueTypeValues, d.ValueType) {
		return fmt.Errorf("invalid value_type %q", d.ValueType)
	}
	if d.SeenUpToSeq != nil && *d.SeenUpToSeq < 0 {
		return fmt.Errorf("seen_up_to_seq must be non-negative")
	}
	return nil
}

func containsAttentionValue(values []string, candidate string) bool {
	for _, value := range values {
		if value == candidate {
			return true
		}
	}
	return false
}
