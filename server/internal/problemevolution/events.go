package problemevolution

import (
	"encoding/json"
	"errors"
	"fmt"
	"strings"
)

// Event types the external evolver may emit. Anything outside this list is
// dropped rather than forwarded, so a newer evolver cannot widen the surface
// the platform stores and renders.
const (
	EventBatchStarted      = "batch_started"
	EventCandidateStarted  = "candidate_started"
	EventCandidateArtifact = "candidate_artifact"
	EventCandidateScored   = "candidate_scored"
	EventCandidateFailed   = "candidate_failed"
	EventCandidateFinished = "candidate_finished"
	EventHarnessProposed   = "harness_proposed"
	EventProgress          = "progress"
	EventBatchFinished     = "batch_finished"
)

// MaxEventLineBytes bounds one NDJSON line; longer lines are truncated by the
// daemon and reported as a progress event instead of being parsed.
const MaxEventLineBytes = 64 * 1024

// MaxFreeTextBytes bounds evolver-supplied prose (`message`, `note`) before it
// is stored, on top of secret filtering.
const MaxFreeTextBytes = 1024

// ErrEventRejected marks an event the daemon must drop.
var ErrEventRejected = errors.New("problem evolution event rejected")

var allowedEventTypes = map[string]struct{}{
	EventBatchStarted:      {},
	EventCandidateStarted:  {},
	EventCandidateArtifact: {},
	EventCandidateScored:   {},
	EventCandidateFailed:   {},
	EventCandidateFinished: {},
	EventHarnessProposed:   {},
	EventProgress:          {},
	EventBatchFinished:     {},
}

// AllowedEventTypes lists the accepted event types in a stable order.
func AllowedEventTypes() []string {
	return []string{
		EventBatchStarted,
		EventCandidateStarted,
		EventCandidateArtifact,
		EventCandidateScored,
		EventCandidateFailed,
		EventCandidateFinished,
		EventHarnessProposed,
		EventProgress,
		EventBatchFinished,
	}
}

// IsAllowedEventType reports whether the platform stores this event type.
func IsAllowedEventType(eventType string) bool {
	_, ok := allowedEventTypes[eventType]
	return ok
}

// EvolverEvent is one NDJSON line written by the external program to stdout.
type EvolverEvent struct {
	SchemaVersion int             `json:"schema_version"`
	ClientEventID string          `json:"client_event_id"`
	EventType     string          `json:"event_type"`
	CandidateRef  string          `json:"candidate_id,omitempty"`
	At            string          `json:"at,omitempty"`
	Payload       json.RawMessage `json:"payload,omitempty"`
}

// MaxCandidateParents bounds one candidate's declared lineage, so a malformed
// batch cannot fan out into an unbounded edge write.
const MaxCandidateParents = 8

// CandidateStartedPayload describes a newly opened candidate. `Lane` is only a
// hint: when a relation is declared the platform derives the lane from it, so a
// crossover cannot be filed under `baseline`.
type CandidateStartedPayload struct {
	Lane       string   `json:"lane"`
	Operator   string   `json:"operator"`
	Relation   string   `json:"relation,omitempty"`
	Generation int      `json:"generation,omitempty"`
	ParentIDs  []string `json:"parent_ids,omitempty"`
}

func (p CandidateStartedPayload) validate() error {
	if p.Generation < 0 {
		return fmt.Errorf("%w: generation must not be negative", ErrEventRejected)
	}
	if len(p.ParentIDs) > MaxCandidateParents {
		return fmt.Errorf("%w: candidate declares more than %d parents", ErrEventRejected, MaxCandidateParents)
	}
	seen := make(map[string]struct{}, len(p.ParentIDs))
	for _, parent := range p.ParentIDs {
		if strings.TrimSpace(parent) == "" {
			return fmt.Errorf("%w: parent_ids contains an empty ref", ErrEventRejected)
		}
		if _, duplicate := seen[parent]; duplicate {
			return fmt.Errorf("%w: parent_ids contains %q twice", ErrEventRejected, parent)
		}
		seen[parent] = struct{}{}
	}
	if p.Relation == "" {
		if len(p.ParentIDs) > 0 {
			return fmt.Errorf("%w: parent_ids requires a relation", ErrEventRejected)
		}
		return nil
	}
	if !IsKnownRelation(p.Relation) {
		return fmt.Errorf("%w: unknown relation %q", ErrEventRejected, p.Relation)
	}
	if len(p.ParentIDs) == 0 {
		return fmt.Errorf("%w: relation %q requires at least one parent", ErrEventRejected, p.Relation)
	}
	// A crossover with one parent is a mutation wearing the wrong label, and it
	// would make the lineage graph lie about where the answer came from.
	if p.Relation == RelationCrossoverOf && len(p.ParentIDs) < 2 {
		return fmt.Errorf("%w: crossover_of requires at least two parents", ErrEventRejected)
	}
	return nil
}

// CandidateArtifactPayload declares a produced artifact inside the workdir.
type CandidateArtifactPayload struct {
	Kind         string `json:"kind"`
	RelativePath string `json:"relative_path"`
	ContentHash  string `json:"content_hash"`
	SizeBytes    int64  `json:"size_bytes"`
	Summary      string `json:"summary,omitempty"`
}

// CandidateScoredPayload carries the scorecard for one candidate.
type CandidateScoredPayload struct {
	Score           Score           `json:"score"`
	BehaviorProfile BehaviorProfile `json:"behavior_profile"`
}

// CandidateFailedPayload classifies a failed candidate.
type CandidateFailedPayload struct {
	FailureClass string `json:"failure_class"`
	Message      string `json:"message,omitempty"`
}

// CandidateFinishedPayload carries the terminal candidate status.
type CandidateFinishedPayload struct {
	Status string `json:"status"`
}

// HarnessProposedPayload is one JIT harness proposal awaiting the static gate.
type HarnessProposedPayload struct {
	Spec       HarnessSpec `json:"spec"`
	PriorScore float64     `json:"prior_score,omitempty"`
	// GatePassed exists only so a proposal claiming it can be rejected.
	GatePassed bool `json:"gate_passed,omitempty"`
}

// ProgressPayload carries bounded progress notes and usage counters.
type ProgressPayload struct {
	Note       string  `json:"note,omitempty"`
	Tokens     int64   `json:"tokens,omitempty"`
	Cost       float64 `json:"cost,omitempty"`
	ModelCalls int     `json:"model_calls,omitempty"`
}

// BatchFinishedPayload closes one evolver invocation.
type BatchFinishedPayload struct {
	BestCandidateRef string `json:"best_candidate_id,omitempty"`
	Produced         int    `json:"produced"`
}

// Candidate terminal statuses the evolver may request. The platform owns
// selection semantics (`elite`, `selected`, `pruned`), so the evolver cannot
// promote its own candidate.
var evolverCandidateStatuses = map[string]struct{}{
	"selectable": {},
	"failed":     {},
	"timeout":    {},
	"infeasible": {},
}

// Validate checks the envelope. Payload validation is per event type and
// happens in ValidatePayload so the daemon can drop one bad event without
// aborting the batch.
func (e EvolverEvent) Validate() error {
	if e.SchemaVersion != SchemaVersion {
		return fmt.Errorf("%w: unsupported schema_version %d", ErrEventRejected, e.SchemaVersion)
	}
	if strings.TrimSpace(e.ClientEventID) == "" {
		return fmt.Errorf("%w: client_event_id is required", ErrEventRejected)
	}
	if len(e.ClientEventID) > 128 {
		return fmt.Errorf("%w: client_event_id is too long", ErrEventRejected)
	}
	if !IsAllowedEventType(e.EventType) {
		return fmt.Errorf("%w: unknown event_type %q", ErrEventRejected, e.EventType)
	}
	if candidateScopedEvent(e.EventType) && strings.TrimSpace(e.CandidateRef) == "" {
		return fmt.Errorf("%w: %s requires candidate_id", ErrEventRejected, e.EventType)
	}
	if len(e.CandidateRef) > 128 {
		return fmt.Errorf("%w: candidate_id is too long", ErrEventRejected)
	}
	return nil
}

func candidateScopedEvent(eventType string) bool {
	switch eventType {
	case EventCandidateStarted, EventCandidateArtifact, EventCandidateScored,
		EventCandidateFailed, EventCandidateFinished, EventHarnessProposed:
		return true
	default:
		return false
	}
}

// ValidatePayload decodes and checks the payload for the event type.
func (e EvolverEvent) ValidatePayload() error {
	switch e.EventType {
	case EventCandidateStarted:
		var payload CandidateStartedPayload
		if err := decodePayload(e.Payload, &payload); err != nil {
			return err
		}
		return payload.validate()
	case EventCandidateArtifact:
		var payload CandidateArtifactPayload
		if err := decodePayload(e.Payload, &payload); err != nil {
			return err
		}
		if strings.TrimSpace(payload.RelativePath) == "" {
			return fmt.Errorf("%w: candidate_artifact needs relative_path", ErrEventRejected)
		}
		if strings.TrimSpace(payload.ContentHash) == "" {
			return fmt.Errorf("%w: candidate_artifact needs content_hash", ErrEventRejected)
		}
		return nil
	case EventCandidateScored:
		var payload CandidateScoredPayload
		if err := decodePayload(e.Payload, &payload); err != nil {
			return err
		}
		if err := payload.Score.Validate(); err != nil {
			return fmt.Errorf("%w: %s", ErrEventRejected, err)
		}
		if err := payload.BehaviorProfile.Validate(); err != nil {
			return fmt.Errorf("%w: %s", ErrEventRejected, err)
		}
		return nil
	case EventCandidateFailed:
		var payload CandidateFailedPayload
		if err := decodePayload(e.Payload, &payload); err != nil {
			return err
		}
		if strings.TrimSpace(payload.FailureClass) == "" {
			return fmt.Errorf("%w: candidate_failed needs failure_class", ErrEventRejected)
		}
		return nil
	case EventCandidateFinished:
		var payload CandidateFinishedPayload
		if err := decodePayload(e.Payload, &payload); err != nil {
			return err
		}
		if _, ok := evolverCandidateStatuses[payload.Status]; !ok {
			return fmt.Errorf("%w: candidate_finished status %q is not evolver-settable", ErrEventRejected, payload.Status)
		}
		return nil
	case EventHarnessProposed:
		var payload HarnessProposedPayload
		if err := decodePayload(e.Payload, &payload); err != nil {
			return err
		}
		if strings.TrimSpace(payload.Spec.HarnessID) == "" {
			return fmt.Errorf("%w: harness_proposed needs a harness_id", ErrEventRejected)
		}
		// The gate verdict is the platform's to make. Accepting a self-reported
		// pass would turn the gate into a suggestion.
		if payload.GatePassed {
			return fmt.Errorf("%w: harness_proposed may not assert gate_passed", ErrEventRejected)
		}
		return nil
	case EventProgress:
		var payload ProgressPayload
		if err := decodePayload(e.Payload, &payload); err != nil {
			return err
		}
		if payload.ModelCalls < 0 || payload.Tokens < 0 || payload.Cost < 0 {
			return fmt.Errorf("%w: progress counters cannot be negative", ErrEventRejected)
		}
		return nil
	case EventBatchStarted, EventBatchFinished:
		if len(e.Payload) == 0 {
			return nil
		}
		var payload map[string]any
		return decodePayload(e.Payload, &payload)
	default:
		return fmt.Errorf("%w: unknown event_type %q", ErrEventRejected, e.EventType)
	}
}

func decodePayload(raw json.RawMessage, target any) error {
	if len(raw) == 0 {
		return fmt.Errorf("%w: payload is required", ErrEventRejected)
	}
	if err := json.Unmarshal(raw, target); err != nil {
		return fmt.Errorf("%w: %s", ErrEventRejected, err)
	}
	return nil
}

// TruncateFreeText bounds evolver prose before it is persisted or rendered.
func TruncateFreeText(text string) string {
	trimmed := strings.TrimSpace(text)
	if len(trimmed) <= MaxFreeTextBytes {
		return trimmed
	}
	return strings.ToValidUTF8(trimmed[:MaxFreeTextBytes], "") + "…"
}
