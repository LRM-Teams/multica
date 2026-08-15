package researchrun

import (
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"sort"
	"strings"
)

const IntegrationContextPolicyVersionV1 = "research-integration-context-v1"

type IntegrationSnapshotStatus string

const (
	IntegrationSnapshotCompleted  IntegrationSnapshotStatus = "completed"
	IntegrationSnapshotStale      IntegrationSnapshotStatus = "stale"
	IntegrationSnapshotSuperseded IntegrationSnapshotStatus = "superseded"
)

type IntegrationSnapshotRef struct {
	SnapshotID           string
	WorkspaceID          string
	SessionID            string
	GoalVersion          int64
	PlanVersion          int64
	RoundNumber          int
	ThroughEventSequence int64
	ThroughStateVersion  int64
	Status               IntegrationSnapshotStatus
	ArtifactPassportID   string
	ArtifactVersionID    string
	ArtifactContentHash  string
	InputHash            string
	CanonicalStateHash   string
	AccessLevel          ArtifactAccessLevel
	EvaluationPrivate    bool
	ContributorAgentIDs  []string
}

type IntegrationContextRequest struct {
	PolicyVersion        string
	WorkspaceID          string
	SessionID            string
	GoalVersion          int64
	PlanVersion          int64
	ThroughEventSequence int64
	ThroughStateVersion  int64
	Clearance            ArtifactClearance
	Purpose              ArtifactPurpose
}

type IntegrationContextOmissionReason string

const (
	IntegrationContextNoSnapshot            IntegrationContextOmissionReason = "no_snapshot"
	IntegrationContextStale                 IntegrationContextOmissionReason = "stale"
	IntegrationContextSuperseded            IntegrationContextOmissionReason = "superseded"
	IntegrationContextInsufficientClearance IntegrationContextOmissionReason = "insufficient_clearance"
	IntegrationContextEvaluationCompartment IntegrationContextOmissionReason = "evaluation_compartment"
)

type IntegrationContextSelection struct {
	Snapshot       *IntegrationSnapshotRef
	OmissionReason IntegrationContextOmissionReason
	Fingerprint    string
}

// SelectLatestIntegrationContext chooses at most one frozen cross-Agent
// Integration Snapshot for a later task. Selection is latest-first and access
// is checked only after that canonical latest version is known: an inaccessible
// or stale latest snapshot causes omission, never a silent fallback to older
// conclusions.
func SelectLatestIntegrationContext(request IntegrationContextRequest, candidates []IntegrationSnapshotRef) (IntegrationContextSelection, error) {
	if err := validateIntegrationContextRequest(request); err != nil {
		return IntegrationContextSelection{}, err
	}
	if len(candidates) > 4096 {
		return IntegrationContextSelection{}, fmt.Errorf("%w: Integration Context candidate set is oversized", ErrInvalidContract)
	}
	eligible := make([]IntegrationSnapshotRef, 0, len(candidates))
	seenSnapshots := make(map[string]struct{}, len(candidates))
	seenVersions := make(map[string]struct{}, len(candidates))
	for _, candidate := range candidates {
		normalized, err := normalizeIntegrationSnapshotRef(candidate)
		if err != nil {
			return IntegrationContextSelection{}, err
		}
		if normalized.WorkspaceID != request.WorkspaceID || normalized.SessionID != request.SessionID {
			return IntegrationContextSelection{}, fmt.Errorf("%w: cross-scope Integration Snapshot candidate", ErrInvalidContract)
		}
		if _, duplicate := seenSnapshots[normalized.SnapshotID]; duplicate {
			return IntegrationContextSelection{}, fmt.Errorf("%w: duplicate Integration Snapshot", ErrInvalidContract)
		}
		if _, duplicate := seenVersions[normalized.ArtifactVersionID]; duplicate {
			return IntegrationContextSelection{}, fmt.Errorf("%w: duplicate Integration Snapshot Artifact Version", ErrInvalidContract)
		}
		seenSnapshots[normalized.SnapshotID] = struct{}{}
		seenVersions[normalized.ArtifactVersionID] = struct{}{}
		if normalized.GoalVersion != request.GoalVersion || normalized.PlanVersion != request.PlanVersion ||
			normalized.ThroughEventSequence > request.ThroughEventSequence ||
			normalized.ThroughStateVersion > request.ThroughStateVersion {
			continue
		}
		eligible = append(eligible, normalized)
	}
	sort.Slice(eligible, func(i, j int) bool {
		if eligible[i].RoundNumber != eligible[j].RoundNumber {
			return eligible[i].RoundNumber > eligible[j].RoundNumber
		}
		if eligible[i].ThroughEventSequence != eligible[j].ThroughEventSequence {
			return eligible[i].ThroughEventSequence > eligible[j].ThroughEventSequence
		}
		if eligible[i].ThroughStateVersion != eligible[j].ThroughStateVersion {
			return eligible[i].ThroughStateVersion > eligible[j].ThroughStateVersion
		}
		return eligible[i].SnapshotID < eligible[j].SnapshotID
	})
	selection := IntegrationContextSelection{}
	var latest *IntegrationSnapshotRef
	if len(eligible) == 0 {
		selection.OmissionReason = IntegrationContextNoSnapshot
	} else {
		latest = &eligible[0]
		if len(eligible) > 1 && sameIntegrationSnapshotPosition(eligible[0], eligible[1]) {
			return IntegrationContextSelection{}, fmt.Errorf("%w: ambiguous latest Integration Snapshot", ErrInvalidContract)
		}
		switch latest.Status {
		case IntegrationSnapshotStale:
			selection.OmissionReason = IntegrationContextStale
		case IntegrationSnapshotSuperseded:
			selection.OmissionReason = IntegrationContextSuperseded
		case IntegrationSnapshotCompleted:
			if latest.EvaluationPrivate {
				// Integration Context is shared synthesis, never a transport for
				// grader-private Oracle material, including on evaluation tasks.
				selection.OmissionReason = IntegrationContextEvaluationCompartment
			} else if allowed, _ := (ArtifactPolicy{}).CanReadNormal(request.Clearance, latest.AccessLevel, request.Purpose, false); allowed {
				selected := *latest
				selection.Snapshot = &selected
			} else {
				selection.OmissionReason = IntegrationContextInsufficientClearance
			}
		}
	}
	fingerprintInput := struct {
		Request  IntegrationContextRequest
		Latest   *IntegrationSnapshotRef
		Omission IntegrationContextOmissionReason
	}{Request: request, Latest: latest, Omission: selection.OmissionReason}
	encoded, err := json.Marshal(fingerprintInput)
	if err != nil {
		return IntegrationContextSelection{}, err
	}
	digest := sha256.Sum256(encoded)
	selection.Fingerprint = fmt.Sprintf("sha256:%x", digest)
	return selection, nil
}

func validateIntegrationContextRequest(request IntegrationContextRequest) error {
	if request.PolicyVersion != IntegrationContextPolicyVersionV1 ||
		!validIntegrationContextIdentity(request.WorkspaceID) || !validIntegrationContextIdentity(request.SessionID) ||
		request.GoalVersion < 1 || request.PlanVersion < 1 || request.ThroughEventSequence < 1 || request.ThroughStateVersion < 1 {
		return fmt.Errorf("%w: Integration Context request is invalid", ErrInvalidContract)
	}
	switch request.Clearance {
	case ArtifactClearanceVerifiedOnly, ArtifactClearanceRedacted, ArtifactClearanceRaw:
	default:
		return fmt.Errorf("%w: Integration Context clearance is invalid", ErrInvalidContract)
	}
	if request.Purpose != ArtifactPurposeTaskExecution && request.Purpose != ArtifactPurposeEvaluation {
		return fmt.Errorf("%w: Integration Context purpose is invalid", ErrInvalidContract)
	}
	return nil
}

func normalizeIntegrationSnapshotRef(candidate IntegrationSnapshotRef) (IntegrationSnapshotRef, error) {
	if !validIntegrationContextIdentity(candidate.SnapshotID) || !validIntegrationContextIdentity(candidate.WorkspaceID) ||
		!validIntegrationContextIdentity(candidate.SessionID) || candidate.GoalVersion < 1 || candidate.PlanVersion < 1 ||
		candidate.RoundNumber < 1 || candidate.ThroughEventSequence < 1 || candidate.ThroughStateVersion < 1 ||
		!validIntegrationContextIdentity(candidate.ArtifactPassportID) || !validIntegrationContextIdentity(candidate.ArtifactVersionID) ||
		!validIntegrationContextHash(candidate.ArtifactContentHash) || !validIntegrationContextHash(candidate.InputHash) ||
		!validIntegrationContextHash(candidate.CanonicalStateHash) || len(candidate.ContributorAgentIDs) < 2 || len(candidate.ContributorAgentIDs) > 64 {
		return IntegrationSnapshotRef{}, fmt.Errorf("%w: Integration Snapshot reference is invalid", ErrInvalidContract)
	}
	if candidate.Status != IntegrationSnapshotCompleted && candidate.Status != IntegrationSnapshotStale && candidate.Status != IntegrationSnapshotSuperseded {
		return IntegrationSnapshotRef{}, fmt.Errorf("%w: Integration Snapshot status is invalid", ErrInvalidContract)
	}
	switch candidate.AccessLevel {
	case ArtifactAccessVerifiedOnly, ArtifactAccessRedacted, ArtifactAccessRaw:
	default:
		return IntegrationSnapshotRef{}, fmt.Errorf("%w: Integration Snapshot access level is invalid", ErrInvalidContract)
	}
	normalized := candidate
	normalized.ContributorAgentIDs = append([]string(nil), candidate.ContributorAgentIDs...)
	sort.Strings(normalized.ContributorAgentIDs)
	for index, agentID := range normalized.ContributorAgentIDs {
		if !validIntegrationContextIdentity(agentID) || (index > 0 && normalized.ContributorAgentIDs[index-1] == agentID) {
			return IntegrationSnapshotRef{}, fmt.Errorf("%w: Integration Snapshot contributors are invalid", ErrInvalidContract)
		}
	}
	return normalized, nil
}

func sameIntegrationSnapshotPosition(left, right IntegrationSnapshotRef) bool {
	return left.RoundNumber == right.RoundNumber &&
		left.ThroughEventSequence == right.ThroughEventSequence &&
		left.ThroughStateVersion == right.ThroughStateVersion
}

func validIntegrationContextIdentity(value string) bool {
	return value != "" && len(value) <= 512 && strings.TrimSpace(value) == value
}

func validIntegrationContextHash(value string) bool {
	if !strings.HasPrefix(value, "sha256:") || len(value) != len("sha256:")+sha256.Size*2 {
		return false
	}
	for _, r := range strings.TrimPrefix(value, "sha256:") {
		if (r < '0' || r > '9') && (r < 'a' || r > 'f') {
			return false
		}
	}
	return true
}
