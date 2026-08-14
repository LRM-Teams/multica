package researchrun

import (
	"errors"
	"testing"
)

const (
	reporterWorkspaceID  = "10000000-0000-4000-8000-000000000001"
	reporterSessionID    = "10000000-0000-4000-8000-000000000002"
	reporterTaskID       = "10000000-0000-4000-8000-000000000003"
	reporterAttemptID    = "10000000-0000-4000-8000-000000000004"
	reporterAgentID      = "10000000-0000-4000-8000-000000000005"
	reporterSnapshotID   = "20000000-0000-4000-8000-000000000001"
	reporterSnapshotVer  = "20000000-0000-4000-8000-000000000002"
	reporterContributorA = "30000000-0000-4000-8000-000000000001"
	reporterContributorB = "30000000-0000-4000-8000-000000000002"
	reporterArtifactA    = "40000000-0000-4000-8000-000000000001"
	reporterArtifactB    = "40000000-0000-4000-8000-000000000002"
	reporterPassportA    = "50000000-0000-4000-8000-000000000001"
	reporterPassportB    = "50000000-0000-4000-8000-000000000002"
	reporterVersionA     = "60000000-0000-4000-8000-000000000001"
	reporterVersionB     = "60000000-0000-4000-8000-000000000002"
	reporterHashA        = "sha256:aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"
	reporterHashB        = "sha256:bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb"
	reporterStateHash    = "sha256:cccccccccccccccccccccccccccccccccccccccccccccccccccccccccccccccc"
)

func reporterInputFixture(t *testing.T) ReporterInputRequest {
	t.Helper()
	artifacts := []ReporterArtifactInput{
		{ArtifactID: reporterArtifactA, WorkspaceID: reporterWorkspaceID, SessionID: reporterSessionID, PassportID: reporterPassportA, VersionID: reporterVersionA, Kind: "claim", ContentHash: reporterHashA, GoalVersion: 2, PlanVersion: 3, ThroughEventSequence: 39, ThroughStateVersion: 10, Lifecycle: ArtifactLifecycleAccepted, AccessLevel: ArtifactAccessVerifiedOnly, Verified: true},
		{ArtifactID: reporterArtifactB, WorkspaceID: reporterWorkspaceID, SessionID: reporterSessionID, PassportID: reporterPassportB, VersionID: reporterVersionB, Kind: "insight", ContentHash: reporterHashB, GoalVersion: 2, PlanVersion: 3, ThroughEventSequence: 40, ThroughStateVersion: 11, Lifecycle: ArtifactLifecycleAccepted, AccessLevel: ArtifactAccessRedacted, Verified: true},
	}
	inputSetHash, err := reporterInputSetHash(artifacts)
	if err != nil {
		t.Fatal(err)
	}
	return ReporterInputRequest{
		PolicyVersion: ReporterInputPolicyV1,
		WorkspaceID:   reporterWorkspaceID, SessionID: reporterSessionID, TaskID: reporterTaskID, AttemptID: reporterAttemptID, ReporterAgentID: reporterAgentID,
		GoalVersion: 2, PlanVersion: 3, ThroughEventSequence: 42, ThroughStateVersion: 12, CanonicalStateHash: reporterStateHash, Clearance: ArtifactClearanceRedacted,
		Integration: ReporterIntegrationSnapshot{
			SnapshotID: reporterSnapshotID, WorkspaceID: reporterWorkspaceID, SessionID: reporterSessionID, ArtifactVersionID: reporterSnapshotVer, ContentHash: reporterHashA, InputSetHash: inputSetHash, CanonicalStateHash: reporterStateHash,
			GoalVersion: 2, PlanVersion: 3, ThroughEventSequence: 40, ThroughStateVersion: 11, Status: "completed", AccessLevel: ArtifactAccessRedacted, CanonicalLatest: true,
			ContributorAgentIDs: []string{reporterContributorA, reporterContributorB}, InputArtifactIDs: []string{reporterArtifactA, reporterArtifactB},
		},
		Artifacts: artifacts,
	}
}

func TestValidateReporterInputAcceptsLatestVerifiedSet(t *testing.T) {
	manifest, err := ValidateReporterInput(reporterInputFixture(t))
	if err != nil {
		t.Fatal(err)
	}
	if manifest.IntegrationSnapshotID != reporterSnapshotID || len(manifest.ArtifactVersionIDs) != 2 || len(manifest.Fingerprint) != 71 {
		t.Fatalf("manifest=%+v", manifest)
	}
}

func TestValidateReporterInputIsOrderStable(t *testing.T) {
	input := reporterInputFixture(t)
	first, err := ValidateReporterInput(input)
	if err != nil {
		t.Fatal(err)
	}
	input.Artifacts[0], input.Artifacts[1] = input.Artifacts[1], input.Artifacts[0]
	input.Integration.InputArtifactIDs[0], input.Integration.InputArtifactIDs[1] = input.Integration.InputArtifactIDs[1], input.Integration.InputArtifactIDs[0]
	input.Integration.ContributorAgentIDs[0], input.Integration.ContributorAgentIDs[1] = input.Integration.ContributorAgentIDs[1], input.Integration.ContributorAgentIDs[0]
	second, err := ValidateReporterInput(input)
	if err != nil {
		t.Fatal(err)
	}
	if first.Fingerprint != second.Fingerprint {
		t.Fatalf("unstable fingerprints: %s != %s", first.Fingerprint, second.Fingerprint)
	}
}

func TestValidateReporterInputRejectsChangedIntegrationTarget(t *testing.T) {
	for name, mutate := range map[string]func(*ReporterInputRequest){
		"not latest":       func(input *ReporterInputRequest) { input.Integration.CanonicalLatest = false },
		"stale":            func(input *ReporterInputRequest) { input.Integration.Status = "stale" },
		"goal changed":     func(input *ReporterInputRequest) { input.Integration.GoalVersion-- },
		"state changed":    func(input *ReporterInputRequest) { input.Integration.CanonicalStateHash = reporterHashB },
		"set hash changed": func(input *ReporterInputRequest) { input.Integration.InputSetHash = reporterHashB },
	} {
		t.Run(name, func(t *testing.T) {
			input := reporterInputFixture(t)
			mutate(&input)
			if _, err := ValidateReporterInput(input); !errors.Is(err, ErrControlTargetChanged) {
				t.Fatalf("err=%v want ErrControlTargetChanged", err)
			}
		})
	}
}

func TestValidateReporterInputRejectsUntrustedArtifacts(t *testing.T) {
	for name, mutate := range map[string]func(*ReporterInputRequest){
		"unverified":             func(input *ReporterInputRequest) { input.Artifacts[0].Verified = false },
		"not accepted":           func(input *ReporterInputRequest) { input.Artifacts[0].Lifecycle = ArtifactLifecycleRegistered },
		"evaluation private":     func(input *ReporterInputRequest) { input.Artifacts[0].EvaluationPrivate = true },
		"insufficient clearance": func(input *ReporterInputRequest) { input.Artifacts[0].AccessLevel = ArtifactAccessRaw },
		"future of integration":  func(input *ReporterInputRequest) { input.Artifacts[0].ThroughStateVersion = 12 },
		"extra artifact":         func(input *ReporterInputRequest) { input.Artifacts = append(input.Artifacts, input.Artifacts[0]) },
		"unknown kind":           func(input *ReporterInputRequest) { input.Artifacts[0].Kind = "stage_evaluation" },
		"cross workspace": func(input *ReporterInputRequest) {
			input.Artifacts[0].WorkspaceID = "90000000-0000-4000-8000-000000000001"
		},
		"cross session": func(input *ReporterInputRequest) {
			input.Artifacts[0].SessionID = "90000000-0000-4000-8000-000000000002"
		},
	} {
		t.Run(name, func(t *testing.T) {
			input := reporterInputFixture(t)
			mutate(&input)
			if _, err := ValidateReporterInput(input); !errors.Is(err, ErrInvalidContract) {
				t.Fatalf("err=%v want ErrInvalidContract", err)
			}
		})
	}
}

func TestValidateReporterInputRejectsCrossScopeIntegration(t *testing.T) {
	for name, mutate := range map[string]func(*ReporterInputRequest){
		"workspace": func(input *ReporterInputRequest) {
			input.Integration.WorkspaceID = "90000000-0000-4000-8000-000000000001"
		},
		"session": func(input *ReporterInputRequest) {
			input.Integration.SessionID = "90000000-0000-4000-8000-000000000002"
		},
	} {
		t.Run(name, func(t *testing.T) {
			input := reporterInputFixture(t)
			mutate(&input)
			if _, err := ValidateReporterInput(input); !errors.Is(err, ErrControlTargetChanged) {
				t.Fatalf("err=%v want ErrControlTargetChanged", err)
			}
		})
	}
}

func TestValidateReporterInputRejectsEvaluationPrivateIntegration(t *testing.T) {
	input := reporterInputFixture(t)
	input.Integration.EvaluationPrivate = true
	if _, err := ValidateReporterInput(input); !errors.Is(err, ErrInvalidContract) {
		t.Fatalf("err=%v want ErrInvalidContract", err)
	}
}
