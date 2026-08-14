package researchrun

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"strings"
)

const evaluationSubjectSchemaV1 = "research-eval-subject-v1"

type evaluationSourcePolicyEnvelope struct {
	EvaluationSubject *evaluationSubjectPolicy `json:"evaluation_subject,omitempty"`
}

type evaluationSubjectPolicy struct {
	SchemaVersion string   `json:"schema_version"`
	SubjectHash   string   `json:"subject_hash"`
	DocumentIDs   []string `json:"document_ids"`
}

type evaluationSourceMetadata struct {
	DocumentID  string `json:"evaluation_document_id"`
	SubjectHash string `json:"evaluation_subject_hash"`
}

type evaluationObservationDatum struct {
	FactKey   string `json:"evaluation_fact_key"`
	FactValue string `json:"evaluation_fact_value"`
}

// validateEvaluationSubjectResult is inert for ordinary Runs. An explicitly
// configured evaluation Run must prove every submitted Source and Observation
// belongs to the sealed Subject before atomic materialization can begin.
func validateEvaluationSubjectResult(sourcePolicy json.RawMessage, result ResultEnvelope) error {
	policy, enabled, err := parseEvaluationSubjectPolicy(sourcePolicy)
	if err != nil || !enabled {
		return err
	}
	documents := make(map[string]struct{}, len(policy.DocumentIDs))
	for _, id := range policy.DocumentIDs {
		id = strings.TrimSpace(id)
		if id == "" {
			return fmt.Errorf("%w: evaluation subject contains an empty document identity", ErrInvalidContract)
		}
		if _, duplicate := documents[id]; duplicate {
			return fmt.Errorf("%w: evaluation subject contains duplicate document %q", ErrInvalidContract, id)
		}
		documents[id] = struct{}{}
	}
	sources := make(map[string]struct{}, len(result.Sources))
	for _, source := range result.Sources {
		var metadata evaluationSourceMetadata
		if err = decodeStrictJSON(source.Metadata, &metadata); err != nil {
			return fmt.Errorf("%w: evaluation source %q metadata: %v", ErrInvalidResult, source.ClientKey, err)
		}
		if metadata.SubjectHash != policy.SubjectHash {
			return fmt.Errorf("%w: evaluation source %q has the wrong subject hash", ErrInvalidResult, source.ClientKey)
		}
		if _, allowed := documents[metadata.DocumentID]; !allowed {
			return fmt.Errorf("%w: evaluation source %q references unapproved document %q", ErrInvalidResult, source.ClientKey, metadata.DocumentID)
		}
		sources[source.ClientKey] = struct{}{}
	}
	for _, observation := range result.Observations {
		if _, exists := sources[observation.SourceKey]; !exists {
			return fmt.Errorf("%w: evaluation observation %q references an unbound source", ErrInvalidResult, observation.ClientKey)
		}
		var datum evaluationObservationDatum
		if err = decodeStrictJSON(observation.Datum, &datum); err != nil {
			return fmt.Errorf("%w: evaluation observation %q datum: %v", ErrInvalidResult, observation.ClientKey, err)
		}
		if strings.TrimSpace(datum.FactKey) == "" || strings.TrimSpace(datum.FactValue) == "" {
			return fmt.Errorf("%w: evaluation observation %q requires fact key and value", ErrInvalidResult, observation.ClientKey)
		}
	}
	return nil
}

func parseEvaluationSubjectPolicy(sourcePolicy json.RawMessage) (evaluationSubjectPolicy, bool, error) {
	if len(sourcePolicy) == 0 {
		return evaluationSubjectPolicy{}, false, nil
	}
	var raw map[string]json.RawMessage
	if err := json.Unmarshal(sourcePolicy, &raw); err != nil {
		return evaluationSubjectPolicy{}, false, fmt.Errorf("%w: source policy is malformed", ErrInvalidContract)
	}
	encoded, exists := raw["evaluation_subject"]
	if !exists {
		return evaluationSubjectPolicy{}, false, nil
	}
	var policy evaluationSubjectPolicy
	if err := decodeStrictJSON(encoded, &policy); err != nil {
		return evaluationSubjectPolicy{}, false, fmt.Errorf("%w: evaluation subject policy: %v", ErrInvalidContract, err)
	}
	if policy.SchemaVersion != evaluationSubjectSchemaV1 || !validEvaluationSubjectHash(policy.SubjectHash) || len(policy.DocumentIDs) == 0 {
		return evaluationSubjectPolicy{}, false, fmt.Errorf("%w: evaluation subject policy identity is invalid", ErrInvalidContract)
	}
	return policy, true, nil
}

func decodeStrictJSON(encoded []byte, target any) error {
	if len(encoded) == 0 {
		return fmt.Errorf("value is required")
	}
	decoder := json.NewDecoder(bytes.NewReader(encoded))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(target); err != nil {
		return err
	}
	if err := decoder.Decode(&struct{}{}); err != io.EOF {
		return fmt.Errorf("trailing JSON")
	}
	return nil
}

func validEvaluationSubjectHash(value string) bool {
	if !strings.HasPrefix(value, "sha256:") || len(value) != len("sha256:")+64 {
		return false
	}
	for _, character := range strings.TrimPrefix(value, "sha256:") {
		if !strings.ContainsRune("0123456789abcdef", character) {
			return false
		}
	}
	return true
}
