package researchrun

import (
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"sort"
	"strings"

	"github.com/google/uuid"
)

const ReporterInputPolicyV1 = "research-reporter-input-v1"

type ReporterIntegrationSnapshot struct {
	SnapshotID           string
	WorkspaceID          string
	SessionID            string
	ArtifactVersionID    string
	ContentHash          string
	InputSetHash         string
	CanonicalStateHash   string
	GoalVersion          int64
	PlanVersion          int64
	ThroughEventSequence int64
	ThroughStateVersion  int64
	Status               string
	AccessLevel          ArtifactAccessLevel
	EvaluationPrivate    bool
	CanonicalLatest      bool
	ContributorAgentIDs  []string
	InputArtifactIDs     []string
}

type ReporterArtifactInput struct {
	ArtifactID           string
	WorkspaceID          string
	SessionID            string
	PassportID           string
	VersionID            string
	Kind                 string
	ContentHash          string
	GoalVersion          int64
	PlanVersion          int64
	ThroughEventSequence int64
	ThroughStateVersion  int64
	Lifecycle            ArtifactLifecycleStatus
	AccessLevel          ArtifactAccessLevel
	EvaluationPrivate    bool
	Verified             bool
}

type ReporterInputRequest struct {
	PolicyVersion        string
	WorkspaceID          string
	SessionID            string
	TaskID               string
	AttemptID            string
	ReporterAgentID      string
	GoalVersion          int64
	PlanVersion          int64
	ThroughEventSequence int64
	ThroughStateVersion  int64
	CanonicalStateHash   string
	Clearance            ArtifactClearance
	Integration          ReporterIntegrationSnapshot
	Artifacts            []ReporterArtifactInput
}

type ReporterInputManifest struct {
	IntegrationSnapshotID string
	IntegrationVersionID  string
	ArtifactVersionIDs    []string
	Fingerprint           string
}

// ValidateReporterInput admits exactly the latest Integration Snapshot and
// its complete verified input set for one Reporter Attempt. Selection and
// access facts must be server-resolved before this boundary is called.
func ValidateReporterInput(request ReporterInputRequest) (ReporterInputManifest, error) {
	if !validReporterRequest(request) {
		return ReporterInputManifest{}, fmt.Errorf("%w: Reporter input request is invalid", ErrInvalidContract)
	}
	integration, err := normalizeReporterIntegration(request.Integration)
	if err != nil {
		return ReporterInputManifest{}, err
	}
	if integration.WorkspaceID != request.WorkspaceID || integration.SessionID != request.SessionID ||
		integration.GoalVersion != request.GoalVersion || integration.PlanVersion != request.PlanVersion ||
		integration.ThroughEventSequence > request.ThroughEventSequence || integration.ThroughStateVersion > request.ThroughStateVersion ||
		integration.CanonicalStateHash != request.CanonicalStateHash || integration.Status != "completed" || !integration.CanonicalLatest {
		return ReporterInputManifest{}, fmt.Errorf("%w: Reporter Integration Snapshot is no longer current", ErrControlTargetChanged)
	}
	if allowed, _ := (ArtifactPolicy{}).CanReadNormal(request.Clearance, integration.AccessLevel, ArtifactPurposeTaskExecution, integration.EvaluationPrivate); !allowed || integration.EvaluationPrivate {
		return ReporterInputManifest{}, fmt.Errorf("%w: Reporter cannot read Integration Snapshot", ErrInvalidContract)
	}

	artifacts := append([]ReporterArtifactInput(nil), request.Artifacts...)
	sort.Slice(artifacts, func(i, j int) bool { return artifacts[i].ArtifactID < artifacts[j].ArtifactID })
	if len(artifacts) == 0 || len(artifacts) != len(integration.InputArtifactIDs) || len(artifacts) > 4096 {
		return ReporterInputManifest{}, fmt.Errorf("%w: Reporter input set does not match Integration Snapshot", ErrInvalidContract)
	}
	versionIDs := make([]string, 0, len(artifacts))
	seenArtifacts, seenVersions := map[string]struct{}{}, map[string]struct{}{}
	for index, artifact := range artifacts {
		if artifact.ArtifactID != integration.InputArtifactIDs[index] || !validReporterArtifact(artifact, request, integration) {
			return ReporterInputManifest{}, fmt.Errorf("%w: Reporter artifact is not an admitted current input", ErrInvalidContract)
		}
		if _, duplicate := seenArtifacts[artifact.ArtifactID]; duplicate {
			return ReporterInputManifest{}, fmt.Errorf("%w: duplicate Reporter artifact", ErrInvalidContract)
		}
		if _, duplicate := seenVersions[artifact.VersionID]; duplicate {
			return ReporterInputManifest{}, fmt.Errorf("%w: duplicate Reporter artifact version", ErrInvalidContract)
		}
		seenArtifacts[artifact.ArtifactID], seenVersions[artifact.VersionID] = struct{}{}, struct{}{}
		versionIDs = append(versionIDs, artifact.VersionID)
	}
	inputSetHash, err := reporterInputSetHash(artifacts)
	if err != nil {
		return ReporterInputManifest{}, err
	}
	if inputSetHash != integration.InputSetHash {
		return ReporterInputManifest{}, fmt.Errorf("%w: Reporter artifact set does not match frozen Integration inputs", ErrControlTargetChanged)
	}
	manifest := ReporterInputManifest{
		IntegrationSnapshotID: integration.SnapshotID,
		IntegrationVersionID:  integration.ArtifactVersionID,
		ArtifactVersionIDs:    versionIDs,
	}
	request.Integration = integration
	request.Artifacts = artifacts
	encoded, err := json.Marshal(struct {
		Request     ReporterInputRequest
		Integration ReporterIntegrationSnapshot
		Artifacts   []ReporterArtifactInput
	}{request, integration, artifacts})
	if err != nil {
		return ReporterInputManifest{}, err
	}
	digest := sha256.Sum256(encoded)
	manifest.Fingerprint = fmt.Sprintf("sha256:%x", digest)
	return manifest, nil
}

func normalizeReporterIntegration(in ReporterIntegrationSnapshot) (ReporterIntegrationSnapshot, error) {
	if !validReporterUUID(in.SnapshotID) || !validReporterUUID(in.WorkspaceID) || !validReporterUUID(in.SessionID) ||
		!validReporterUUID(in.ArtifactVersionID) || !validReporterHash(in.ContentHash) ||
		!validReporterHash(in.InputSetHash) || !validReporterHash(in.CanonicalStateHash) || in.GoalVersion < 1 || in.PlanVersion < 1 ||
		in.ThroughEventSequence < 1 || in.ThroughStateVersion < 1 || len(in.ContributorAgentIDs) < 2 || len(in.ContributorAgentIDs) > 64 ||
		len(in.InputArtifactIDs) == 0 || len(in.InputArtifactIDs) > 4096 {
		return ReporterIntegrationSnapshot{}, fmt.Errorf("%w: Reporter Integration Snapshot is invalid", ErrInvalidContract)
	}
	if in.Status != "completed" && in.Status != "stale" && in.Status != "superseded" {
		return ReporterIntegrationSnapshot{}, fmt.Errorf("%w: Reporter Integration Snapshot status is invalid", ErrInvalidContract)
	}
	normalized := in
	normalized.ContributorAgentIDs = append([]string(nil), in.ContributorAgentIDs...)
	normalized.InputArtifactIDs = append([]string(nil), in.InputArtifactIDs...)
	sort.Strings(normalized.ContributorAgentIDs)
	sort.Strings(normalized.InputArtifactIDs)
	for index, id := range normalized.ContributorAgentIDs {
		if !validReporterUUID(id) || index > 0 && normalized.ContributorAgentIDs[index-1] == id {
			return ReporterIntegrationSnapshot{}, fmt.Errorf("%w: Reporter Integration contributors are invalid", ErrInvalidContract)
		}
	}
	for index, id := range normalized.InputArtifactIDs {
		if !validReporterUUID(id) || index > 0 && normalized.InputArtifactIDs[index-1] == id {
			return ReporterIntegrationSnapshot{}, fmt.Errorf("%w: Reporter Integration inputs are invalid", ErrInvalidContract)
		}
	}
	return normalized, nil
}

func validReporterArtifact(artifact ReporterArtifactInput, request ReporterInputRequest, integration ReporterIntegrationSnapshot) bool {
	if artifact.WorkspaceID != request.WorkspaceID || artifact.SessionID != request.SessionID ||
		!validReporterUUID(artifact.ArtifactID) || !validReporterUUID(artifact.WorkspaceID) || !validReporterUUID(artifact.SessionID) ||
		!validReporterUUID(artifact.PassportID) || !validReporterUUID(artifact.VersionID) ||
		!validReporterArtifactKind(artifact.Kind) || !validReporterHash(artifact.ContentHash) ||
		artifact.GoalVersion != request.GoalVersion || artifact.PlanVersion != request.PlanVersion ||
		artifact.ThroughEventSequence > integration.ThroughEventSequence || artifact.ThroughStateVersion > integration.ThroughStateVersion ||
		artifact.ThroughEventSequence < 1 || artifact.ThroughStateVersion < 1 || !artifact.Verified ||
		artifact.Lifecycle != ArtifactLifecycleAccepted || artifact.EvaluationPrivate {
		return false
	}
	allowed, _ := (ArtifactPolicy{}).CanReadNormal(request.Clearance, artifact.AccessLevel, ArtifactPurposeTaskExecution, artifact.EvaluationPrivate)
	return allowed
}

func reporterInputSetHash(artifacts []ReporterArtifactInput) (string, error) {
	type identity struct {
		WorkspaceID string
		SessionID   string
		ArtifactID  string
		PassportID  string
		VersionID   string
		ContentHash string
	}
	values := make([]identity, 0, len(artifacts))
	for _, artifact := range artifacts {
		values = append(values, identity{
			WorkspaceID: artifact.WorkspaceID,
			SessionID:   artifact.SessionID,
			ArtifactID:  artifact.ArtifactID,
			PassportID:  artifact.PassportID,
			VersionID:   artifact.VersionID,
			ContentHash: artifact.ContentHash,
		})
	}
	encoded, err := json.Marshal(values)
	if err != nil {
		return "", err
	}
	digest := sha256.Sum256(encoded)
	return fmt.Sprintf("sha256:%x", digest), nil
}

func validReporterRequest(request ReporterInputRequest) bool {
	if request.PolicyVersion != ReporterInputPolicyV1 || !validReporterUUID(request.WorkspaceID) || !validReporterUUID(request.SessionID) ||
		!validReporterUUID(request.TaskID) || !validReporterUUID(request.AttemptID) || !validReporterUUID(request.ReporterAgentID) ||
		request.GoalVersion < 1 || request.PlanVersion < 1 || request.ThroughEventSequence < 1 || request.ThroughStateVersion < 1 ||
		!validReporterHash(request.CanonicalStateHash) {
		return false
	}
	switch request.Clearance {
	case ArtifactClearanceVerifiedOnly, ArtifactClearanceRedacted, ArtifactClearanceRaw:
		return true
	default:
		return false
	}
}

func validReporterArtifactKind(kind string) bool {
	switch kind {
	case "source_snapshot", "observation", "claim", "evidence_link", "question", "hypothesis", "insight", "dispute", "method_decision":
		return true
	default:
		return false
	}
}

func validReporterUUID(value string) bool {
	_, err := uuid.Parse(value)
	return err == nil
}

func validReporterHash(value string) bool {
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
