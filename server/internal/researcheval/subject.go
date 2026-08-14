package researcheval

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"slices"
	"strings"
)

const (
	SubjectSchemaVersion       = "research-eval-subject-v1"
	maximumSubjectDocuments    = 1000
	maximumDocumentBytes       = 1 << 20
	maximumSubjectContentBytes = 4 << 20
)

// SealedSubjectInput is the only evaluator-owned payload that may cross into a
// production Research Run. Its shape deliberately has no Oracle field. The
// hash binds the exact normalized Task, controlled Environment, and seed that
// an Agent is allowed to observe.
type SealedSubjectInput struct {
	SchemaVersion string       `json:"schema_version"`
	SubjectHash   string       `json:"subject_hash"`
	Seed          int64        `json:"seed"`
	Input         SubjectInput `json:"input"`
}

type subjectHashPayload struct {
	SchemaVersion string       `json:"schema_version"`
	Seed          int64        `json:"seed"`
	Input         SubjectInput `json:"input"`
}

// SealSubjectInput validates and deterministically normalizes the public half
// of an evaluation Case. Oracle never enters this API or its serialized form.
func SealSubjectInput(input SubjectInput, seed int64) (SealedSubjectInput, error) {
	normalized, err := normalizeSubjectInput(input)
	if err != nil {
		return SealedSubjectInput{}, err
	}
	payload := subjectHashPayload{SchemaVersion: SubjectSchemaVersion, Seed: seed, Input: normalized}
	encoded, err := json.Marshal(payload)
	if err != nil {
		return SealedSubjectInput{}, fmt.Errorf("%w: encode evaluation subject: %v", ErrInvalidEvaluation, err)
	}
	digest := sha256.Sum256(encoded)
	return SealedSubjectInput{
		SchemaVersion: SubjectSchemaVersion,
		SubjectHash:   "sha256:" + hex.EncodeToString(digest[:]),
		Seed:          seed,
		Input:         normalized,
	}, nil
}

// DecodeSealedSubjectInput is fail-closed: unknown fields (including oracle),
// trailing JSON, schema drift, or a content/hash mismatch are rejected.
func DecodeSealedSubjectInput(encoded []byte) (SealedSubjectInput, error) {
	decoder := json.NewDecoder(bytes.NewReader(encoded))
	decoder.DisallowUnknownFields()
	var sealed SealedSubjectInput
	if err := decoder.Decode(&sealed); err != nil {
		return SealedSubjectInput{}, fmt.Errorf("%w: decode sealed evaluation subject: %v", ErrInvalidEvaluation, err)
	}
	if err := decoder.Decode(&struct{}{}); err != io.EOF {
		return SealedSubjectInput{}, fmt.Errorf("%w: trailing evaluation subject JSON", ErrInvalidEvaluation)
	}
	if err := VerifySealedSubjectInput(sealed); err != nil {
		return SealedSubjectInput{}, err
	}
	// Return the canonical form rather than preserving harmless input ordering.
	return SealSubjectInput(sealed.Input, sealed.Seed)
}

func VerifySealedSubjectInput(sealed SealedSubjectInput) error {
	if sealed.SchemaVersion != SubjectSchemaVersion {
		return fmt.Errorf("%w: unsupported evaluation subject schema %q", ErrInvalidEvaluation, sealed.SchemaVersion)
	}
	want, err := SealSubjectInput(sealed.Input, sealed.Seed)
	if err != nil {
		return err
	}
	if sealed.SubjectHash != want.SubjectHash {
		return fmt.Errorf("%w: evaluation subject hash mismatch", ErrInvalidEvaluation)
	}
	return nil
}

func normalizeSubjectInput(input SubjectInput) (SubjectInput, error) {
	task := input.Task
	task.ID = strings.TrimSpace(task.ID)
	task.Goal = strings.TrimSpace(task.Goal)
	task.Language = strings.TrimSpace(task.Language)
	if task.ID == "" || task.Goal == "" || len(task.Goal) > 32<<10 || task.Language == "" || !slices.Contains(AllResearchModes, task.Mode) {
		return SubjectInput{}, fmt.Errorf("%w: evaluation task identity, mode, bounded goal, and language are required", ErrInvalidEvaluation)
	}
	task.AllowedTools = normalizedUniqueStrings(task.AllowedTools)
	task.Tags = normalizedUniqueStrings(task.Tags)
	if len(task.AllowedTools) == 0 {
		return SubjectInput{}, fmt.Errorf("%w: evaluation task must allow at least one controlled tool", ErrInvalidEvaluation)
	}

	if len(input.Environment.Documents) == 0 || len(input.Environment.Documents) > maximumSubjectDocuments {
		return SubjectInput{}, fmt.Errorf("%w: evaluation environment document count is outside bounds", ErrInvalidEvaluation)
	}
	environment := Environment{
		Documents: append([]Document(nil), input.Environment.Documents...),
		Faults:    append([]Fault(nil), input.Environment.Faults...),
	}
	documentIDs := make(map[string]struct{}, len(environment.Documents))
	totalBytes := 0
	for index := range environment.Documents {
		document := &environment.Documents[index]
		document.ID = strings.TrimSpace(document.ID)
		document.Family = strings.TrimSpace(document.Family)
		document.Title = strings.TrimSpace(document.Title)
		document.Version = strings.TrimSpace(document.Version)
		document.Published = strings.TrimSpace(document.Published)
		document.Traits = normalizedUniqueStrings(document.Traits)
		if document.ID == "" || document.Family == "" || document.Title == "" || strings.TrimSpace(document.Content) == "" || len(document.Content) > maximumDocumentBytes {
			return SubjectInput{}, fmt.Errorf("%w: evaluation document %d is incomplete or exceeds bounds", ErrInvalidEvaluation, index)
		}
		if _, duplicate := documentIDs[document.ID]; duplicate {
			return SubjectInput{}, fmt.Errorf("%w: duplicate evaluation document %q", ErrInvalidEvaluation, document.ID)
		}
		documentIDs[document.ID] = struct{}{}
		totalBytes += len(document.Content)
		if totalBytes > maximumSubjectContentBytes {
			return SubjectInput{}, fmt.Errorf("%w: evaluation environment content exceeds bounds", ErrInvalidEvaluation)
		}
	}
	slices.SortFunc(environment.Documents, func(left, right Document) int { return strings.Compare(left.ID, right.ID) })
	for index := range environment.Faults {
		fault := &environment.Faults[index]
		fault.Kind = strings.TrimSpace(fault.Kind)
		fault.TargetID = strings.TrimSpace(fault.TargetID)
		fault.Trigger = strings.TrimSpace(fault.Trigger)
		if fault.Kind == "" {
			return SubjectInput{}, fmt.Errorf("%w: evaluation fault kind is required", ErrInvalidEvaluation)
		}
		if fault.TargetID != "" {
			if _, exists := documentIDs[fault.TargetID]; !exists {
				return SubjectInput{}, fmt.Errorf("%w: evaluation fault references unknown document %q", ErrInvalidEvaluation, fault.TargetID)
			}
		}
	}
	slices.SortFunc(environment.Faults, func(left, right Fault) int {
		return strings.Compare(left.Kind+"\x00"+left.TargetID+"\x00"+left.Trigger, right.Kind+"\x00"+right.TargetID+"\x00"+right.Trigger)
	})
	return SubjectInput{Task: task, Environment: environment}, nil
}

func normalizedUniqueStrings(values []string) []string {
	out := make([]string, 0, len(values))
	seen := map[string]struct{}{}
	for _, value := range values {
		value = strings.TrimSpace(value)
		if value == "" {
			continue
		}
		if _, duplicate := seen[value]; duplicate {
			continue
		}
		seen[value] = struct{}{}
		out = append(out, value)
	}
	slices.Sort(out)
	return out
}
