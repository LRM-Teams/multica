// SPDX-License-Identifier: Apache-2.0

package skillevolution

import (
	"errors"
	"fmt"
	"regexp"
)

var (
	// ErrTrajectoryForbiddenType marks a message type that is neither on
	// the observable allowlist nor on the known-diagnostic denylist: new
	// message shapes must be explicitly classified before they may enter
	// (or be excluded from) the evolution corpus.
	ErrTrajectoryForbiddenType = errors.New("trajectory message type is not classified")
	// ErrTrajectoryVisibility marks an unknown visibility class; silently
	// admitting it could widen the corpus beyond user-facing content.
	ErrTrajectoryVisibility = errors.New("trajectory message visibility is unknown")
	// ErrTrajectoryInconsistentSanitizer marks a corpus mixing sanitizer
	// or policy versions across segments.
	ErrTrajectoryInconsistentSanitizer = errors.New("trajectory segments disagree on sanitizer or policy version")
	// ErrTrajectorySequence marks non-monotonic message order inside one
	// segment.
	ErrTrajectorySequence = errors.New("trajectory message sequence is not monotonic")
	// ErrTrajectoryArtifactRef marks a malformed content-addressed
	// artifact reference (backing-blob existence is checked by the service
	// against the blob store; the shape is checked here).
	ErrTrajectoryArtifactRef = errors.New("trajectory artifact ref is malformed")
)

// ObservableKind is the closed event vocabulary of the unified trajectory
// view (spec §12.2).
type ObservableKind string

const (
	KindMessage           ObservableKind = "message"
	KindAction            ObservableKind = "action"
	KindToolCall          ObservableKind = "tool_call"
	KindToolResult        ObservableKind = "tool_result"
	KindEnvironmentChange ObservableKind = "environment_change"
	KindArtifactRef       ObservableKind = "artifact_ref"
	KindFinalResponse     ObservableKind = "final_response"
)

// observableSourceTypes maps persisted message types onto observable
// kinds. Everything here must already be sanitized and user-facing.
var observableSourceTypes = map[string]ObservableKind{
	"user":               KindMessage,
	"assistant":          KindMessage,
	"text":               KindMessage,
	"tool_use":           KindToolCall,
	"tool_call":          KindToolCall,
	"tool_output":        KindToolResult,
	"tool_result":        KindToolResult,
	"action":             KindAction,
	"environment_change": KindEnvironmentChange,
	"artifact_ref":       KindArtifactRef,
	"final_response":     KindFinalResponse,
}

// diagnosticSourceTypes are the known shapes that are legitimate to
// persist but must never enter the evolution corpus: provider thinking
// and scratchpad-style reasoning, logs, telemetry, transport bookkeeping,
// system prompts, and lifecycle markers. They are excluded and counted,
// not errors.
var diagnosticSourceTypes = map[string]struct{}{
	"thinking":            {},
	"scratchpad":          {},
	"reasoning":           {},
	"log":                 {},
	"telemetry":           {},
	"transport":           {},
	"system":              {},
	"session_init":        {},
	"turn_end":            {},
	"compaction_started":  {},
	"compaction_finished": {},
	"wake_attempt":        {},
}

const (
	visibilityUserFacing = "user_facing"
	visibilityDiagnostic = "diagnostic_only"
)

var artifactRefPattern = regexp.MustCompile(`^artifact:sha256:[0-9a-f]{64}$`)

// SourceMessage is the neutral, already-sanitized projection of one
// persisted message: the six sanitizer fields plus the write-time
// visibility class. Redaction, binary rejection, size capping, and
// artifact externalization happen in the shared sanitizer before this
// struct exists; the projector never re-implements them.
type SourceMessage struct {
	Sequence   int64
	Type       string
	Tool       string
	Content    string
	Input      string
	Output     string
	Visibility string
}

// SourceSegment is one sanitized Segment of the run's trajectory.
type SourceSegment struct {
	SegmentID        string
	SanitizerVersion string
	PolicyVersion    string
	Messages         []SourceMessage
	ArtifactRefs     []string
}

// ObservableEvent is one row of the evolution-eligible corpus view.
type ObservableEvent struct {
	Sequence  int64
	SegmentID string
	Kind      ObservableKind
	Tool      string
	Content   string
}

// ObservableTrajectory is the unified read-only view of one eligible run.
type ObservableTrajectory struct {
	RunID       string
	WorkspaceID string
	Eligibility TrajectoryEligibility
	Outcome     OutcomeRecord

	SanitizerVersion string
	PolicyVersion    string
	Events           []ObservableEvent
	ArtifactRefs     []string

	// DiagnosticExclusions counts denylisted types and diagnostic-only
	// messages that were omitted, so audits can tell "clean run" from
	// "everything was stripped".
	DiagnosticExclusions int
}

// ProjectObservableTrajectory builds the evolution-eligible view from
// sanitized segments under the run-start eligibility snapshot. Fail-closed
// rules: the run must be eligible and unrevoked, every message type must
// be classified, visibility must be a known class, segments must agree on
// sanitizer/policy versions, sequences must be monotonic per segment, and
// artifact refs must be content-addressed shapes.
func ProjectObservableTrajectory(eligibility TrajectoryEligibility, outcome OutcomeRecord, segments []SourceSegment) (ObservableTrajectory, error) {
	if err := eligibility.Validate(); err != nil {
		return ObservableTrajectory{}, err
	}
	if !eligibility.EvolutionEligible || eligibility.Revoked() {
		return ObservableTrajectory{}, fmt.Errorf("%w: run %s", ErrTrajectoryNotEligible, eligibility.RunID)
	}
	if err := outcome.Validate(); err != nil {
		return ObservableTrajectory{}, err
	}
	trajectory := ObservableTrajectory{
		RunID: eligibility.RunID, WorkspaceID: eligibility.WorkspaceID,
		Eligibility: eligibility, Outcome: outcome,
	}
	for _, segment := range segments {
		if segment.SanitizerVersion == "" || segment.PolicyVersion == "" {
			return ObservableTrajectory{}, fmt.Errorf("%w: segment %s is missing sanitizer provenance", ErrTrajectoryInconsistentSanitizer, segment.SegmentID)
		}
		if trajectory.SanitizerVersion == "" {
			trajectory.SanitizerVersion = segment.SanitizerVersion
			trajectory.PolicyVersion = segment.PolicyVersion
		} else if trajectory.SanitizerVersion != segment.SanitizerVersion || trajectory.PolicyVersion != segment.PolicyVersion {
			return ObservableTrajectory{}, fmt.Errorf("%w: segment %s carries %s/%s, corpus pinned %s/%s",
				ErrTrajectoryInconsistentSanitizer, segment.SegmentID,
				segment.SanitizerVersion, segment.PolicyVersion,
				trajectory.SanitizerVersion, trajectory.PolicyVersion)
		}
		previous := int64(-1)
		for _, message := range segment.Messages {
			if message.Sequence <= previous {
				return ObservableTrajectory{}, fmt.Errorf("%w: segment %s regressed to %d after %d",
					ErrTrajectorySequence, segment.SegmentID, message.Sequence, previous)
			}
			previous = message.Sequence

			if _, diagnostic := diagnosticSourceTypes[message.Type]; diagnostic {
				trajectory.DiagnosticExclusions++
				continue
			}
			if message.Visibility == visibilityDiagnostic {
				trajectory.DiagnosticExclusions++
				continue
			}
			if message.Visibility != visibilityUserFacing {
				return ObservableTrajectory{}, fmt.Errorf("%w: %q on segment %s seq %d",
					ErrTrajectoryVisibility, message.Visibility, segment.SegmentID, message.Sequence)
			}
			kind, observable := observableSourceTypes[message.Type]
			if !observable {
				return ObservableTrajectory{}, fmt.Errorf("%w: %q on segment %s seq %d",
					ErrTrajectoryForbiddenType, message.Type, segment.SegmentID, message.Sequence)
			}
			trajectory.Events = append(trajectory.Events, ObservableEvent{
				Sequence: message.Sequence, SegmentID: segment.SegmentID,
				Kind: kind, Tool: message.Tool, Content: message.Content,
			})
		}
		for _, ref := range segment.ArtifactRefs {
			if !artifactRefPattern.MatchString(ref) {
				return ObservableTrajectory{}, fmt.Errorf("%w: %q on segment %s", ErrTrajectoryArtifactRef, ref, segment.SegmentID)
			}
			trajectory.ArtifactRefs = append(trajectory.ArtifactRefs, ref)
		}
	}
	return trajectory, nil
}
