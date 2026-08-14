package researchrun

import (
	"errors"
	"testing"
	"time"
)

const (
	monitorDiffWorkspace = "10000000-0000-4000-8000-000000000001"
	monitorDiffSession   = "10000000-0000-4000-8000-000000000002"
	monitorDiffMonitor   = "10000000-0000-4000-8000-000000000003"
	monitorDiffCycle     = "10000000-0000-4000-8000-000000000004"
	monitorDiffPlan      = "10000000-0000-4000-8000-000000000005"
	monitorDiffExecA     = "20000000-0000-4000-8000-000000000001"
	monitorDiffExecB     = "20000000-0000-4000-8000-000000000002"
	monitorDiffArtifactA = "30000000-0000-4000-8000-000000000001"
	monitorDiffArtifactB = "30000000-0000-4000-8000-000000000002"
	monitorDiffVersionA1 = "40000000-0000-4000-8000-000000000001"
	monitorDiffVersionA2 = "40000000-0000-4000-8000-000000000002"
	monitorDiffVersionB  = "40000000-0000-4000-8000-000000000003"
	monitorDiffHashA     = "sha256:aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"
	monitorDiffHashB     = "sha256:bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb"
)

func monitoringDiffFixture() MonitoringDiffRequest {
	started := time.Date(2026, 8, 14, 1, 0, 0, 0, time.UTC)
	baseExecution := MonitoringQueryExecution{
		WorkspaceID: monitorDiffWorkspace, SessionID: monitorDiffSession, MonitorID: monitorDiffMonitor, CycleID: monitorDiffCycle,
		SearchPlanID: monitorDiffPlan, SearchPlanVersion: 3, Status: "succeeded", StartedAt: started, CompletedAt: started.Add(time.Minute),
	}
	return MonitoringDiffRequest{
		PolicyVersion: MonitoringDiffPolicyV1, WorkspaceID: monitorDiffWorkspace, SessionID: monitorDiffSession,
		MonitorID: monitorDiffMonitor, CycleID: monitorDiffCycle, SearchPlanID: monitorDiffPlan, SearchPlanVersion: 3,
		ExpectedQueries: []MonitoringExpectedQuery{
			{QueryKey: "market", CanonicalQueryHash: monitorDiffHashA},
			{QueryKey: "policy", CanonicalQueryHash: monitorDiffHashB},
		},
		Executions: []MonitoringQueryExecution{
			func() MonitoringQueryExecution {
				value := baseExecution
				value.ExecutionID = monitorDiffExecA
				value.QueryKey = "market"
				value.CanonicalQueryHash = monitorDiffHashA
				value.ResultArtifactIDs = []string{monitorDiffArtifactA}
				return value
			}(),
			func() MonitoringQueryExecution {
				value := baseExecution
				value.ExecutionID = monitorDiffExecB
				value.QueryKey = "policy"
				value.CanonicalQueryHash = monitorDiffHashB
				value.ResultArtifactIDs = []string{monitorDiffArtifactB}
				return value
			}(),
		},
		ArtifactDiffs: []MonitoringArtifactDiff{
			{ArtifactID: monitorDiffArtifactA, WorkspaceID: monitorDiffWorkspace, SessionID: monitorDiffSession, QueryExecutionID: monitorDiffExecA, PreviousVersionID: monitorDiffVersionA1, PreviousContentHash: monitorDiffHashA, CurrentVersionID: monitorDiffVersionA2, CurrentContentHash: monitorDiffHashB, ChangeKind: "modified", ContentSimilarity: .5},
			{ArtifactID: monitorDiffArtifactB, WorkspaceID: monitorDiffWorkspace, SessionID: monitorDiffSession, QueryExecutionID: monitorDiffExecB, PreviousVersionID: monitorDiffVersionB, PreviousContentHash: monitorDiffHashB, CurrentVersionID: monitorDiffVersionB, CurrentContentHash: monitorDiffHashB, ChangeKind: "unchanged", ContentSimilarity: 1},
		},
	}
}

func TestValidateMonitoringDiffComputesMaterialityFromVersionDiffs(t *testing.T) {
	manifest, err := ValidateMonitoringDiff(monitoringDiffFixture())
	if err != nil {
		t.Fatal(err)
	}
	if manifest.MaterialityScore != .25 || len(manifest.ChangedArtifactIDs) != 1 || manifest.ChangedArtifactIDs[0] != monitorDiffArtifactA || len(manifest.Fingerprint) != 71 {
		t.Fatalf("manifest=%+v", manifest)
	}
}

func TestValidateMonitoringDiffIsOrderStable(t *testing.T) {
	input := monitoringDiffFixture()
	first, err := ValidateMonitoringDiff(input)
	if err != nil {
		t.Fatal(err)
	}
	input.ExpectedQueries[0], input.ExpectedQueries[1] = input.ExpectedQueries[1], input.ExpectedQueries[0]
	input.Executions[0], input.Executions[1] = input.Executions[1], input.Executions[0]
	input.ArtifactDiffs[0], input.ArtifactDiffs[1] = input.ArtifactDiffs[1], input.ArtifactDiffs[0]
	second, err := ValidateMonitoringDiff(input)
	if err != nil {
		t.Fatal(err)
	}
	if first.Fingerprint != second.Fingerprint {
		t.Fatalf("fingerprints differ: %s != %s", first.Fingerprint, second.Fingerprint)
	}
}

func TestValidateMonitoringDiffRejectsIncompleteOrDriftedPlan(t *testing.T) {
	for name, test := range map[string]struct {
		mutate func(*MonitoringDiffRequest)
		target error
	}{
		"missing query":    {func(input *MonitoringDiffRequest) { input.Executions = input.Executions[:1] }, ErrInvalidContract},
		"query drift":      {func(input *MonitoringDiffRequest) { input.Executions[0].QueryKey = "other" }, ErrControlTargetChanged},
		"query hash drift": {func(input *MonitoringDiffRequest) { input.Executions[0].CanonicalQueryHash = monitorDiffHashB }, ErrControlTargetChanged},
		"plan drift":       {func(input *MonitoringDiffRequest) { input.Executions[0].SearchPlanVersion++ }, ErrInvalidContract},
		"cross session": {func(input *MonitoringDiffRequest) {
			input.Executions[0].SessionID = "90000000-0000-4000-8000-000000000001"
		}, ErrInvalidContract},
	} {
		t.Run(name, func(t *testing.T) {
			input := monitoringDiffFixture()
			test.mutate(&input)
			if _, err := ValidateMonitoringDiff(input); !errors.Is(err, test.target) {
				t.Fatalf("err=%v want %v", err, test.target)
			}
		})
	}
}

func TestValidateMonitoringDiffRejectsUnprovenArtifactChanges(t *testing.T) {
	for name, mutate := range map[string]func(*MonitoringDiffRequest){
		"missing diff": func(input *MonitoringDiffRequest) { input.ArtifactDiffs = input.ArtifactDiffs[:1] },
		"same modified hash": func(input *MonitoringDiffRequest) {
			input.ArtifactDiffs[0].CurrentContentHash = input.ArtifactDiffs[0].PreviousContentHash
		},
		"wrong execution":                func(input *MonitoringDiffRequest) { input.ArtifactDiffs[0].QueryExecutionID = monitorDiffExecB },
		"self claimed changed unchanged": func(input *MonitoringDiffRequest) { input.ArtifactDiffs[1].ChangeKind = "modified" },
		"cross workspace": func(input *MonitoringDiffRequest) {
			input.ArtifactDiffs[0].WorkspaceID = "90000000-0000-4000-8000-000000000001"
		},
	} {
		t.Run(name, func(t *testing.T) {
			input := monitoringDiffFixture()
			mutate(&input)
			if _, err := ValidateMonitoringDiff(input); !errors.Is(err, ErrInvalidContract) {
				t.Fatalf("err=%v want ErrInvalidContract", err)
			}
		})
	}
}

func TestValidateMonitoringDiffAdmitsNoResultExecutionWithoutFabricatedDiff(t *testing.T) {
	input := monitoringDiffFixture()
	input.Executions[1].Status = "no_results"
	input.Executions[1].ResultArtifactIDs = nil
	input.ArtifactDiffs = input.ArtifactDiffs[:1]
	manifest, err := ValidateMonitoringDiff(input)
	if err != nil {
		t.Fatal(err)
	}
	if manifest.MaterialityScore != .5 {
		t.Fatalf("manifest=%+v", manifest)
	}
}

func TestValidateMonitoringDiffAdmitsAddedAndRemovedVersions(t *testing.T) {
	input := monitoringDiffFixture()
	input.Executions[0].ResultArtifactIDs = []string{monitorDiffArtifactA}
	input.Executions[1].Status = "no_results"
	input.Executions[1].ResultArtifactIDs = nil
	input.ArtifactDiffs[0] = MonitoringArtifactDiff{
		ArtifactID: monitorDiffArtifactA, WorkspaceID: monitorDiffWorkspace, SessionID: monitorDiffSession,
		QueryExecutionID: monitorDiffExecA, CurrentVersionID: monitorDiffVersionA2, CurrentContentHash: monitorDiffHashB, ChangeKind: "added",
	}
	input.ArtifactDiffs[1] = MonitoringArtifactDiff{
		ArtifactID: monitorDiffArtifactB, WorkspaceID: monitorDiffWorkspace, SessionID: monitorDiffSession,
		QueryExecutionID: monitorDiffExecB, PreviousVersionID: monitorDiffVersionB, PreviousContentHash: monitorDiffHashB, ChangeKind: "removed",
	}
	manifest, err := ValidateMonitoringDiff(input)
	if err != nil {
		t.Fatal(err)
	}
	if manifest.MaterialityScore != 1 || len(manifest.ChangedArtifactIDs) != 2 {
		t.Fatalf("manifest=%+v", manifest)
	}
}

func TestValidateMonitoringDiffRejectsPartiallyAbsentVersionIdentity(t *testing.T) {
	input := monitoringDiffFixture()
	input.ArtifactDiffs[0].ChangeKind = "added"
	input.ArtifactDiffs[0].PreviousContentHash = ""
	input.ArtifactDiffs[0].ContentSimilarity = 0
	if _, err := ValidateMonitoringDiff(input); !errors.Is(err, ErrInvalidContract) {
		t.Fatalf("err=%v want ErrInvalidContract", err)
	}
}
