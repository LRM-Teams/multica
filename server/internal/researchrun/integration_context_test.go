package researchrun

import (
	"errors"
	"testing"
)

const integrationContextHash = "sha256:dddddddddddddddddddddddddddddddddddddddddddddddddddddddddddddddd"

func integrationContextRequestFixture() IntegrationContextRequest {
	return IntegrationContextRequest{
		PolicyVersion: IntegrationContextPolicyVersionV1,
		WorkspaceID:   "workspace-1", SessionID: "session-1", GoalVersion: 2, PlanVersion: 3,
		ThroughEventSequence: 50, ThroughStateVersion: 20,
		Clearance: ArtifactClearanceRaw, Purpose: ArtifactPurposeTaskExecution,
	}
}

func integrationSnapshotFixture(id string, round int, sequence, stateVersion int64) IntegrationSnapshotRef {
	return IntegrationSnapshotRef{
		SnapshotID: id, WorkspaceID: "workspace-1", SessionID: "session-1", GoalVersion: 2, PlanVersion: 3,
		RoundNumber: round, ThroughEventSequence: sequence, ThroughStateVersion: stateVersion,
		Status: IntegrationSnapshotCompleted, ArtifactPassportID: "passport-" + id,
		ArtifactVersionID: "version-" + id, ArtifactContentHash: integrationContextHash,
		InputHash: integrationContextHash, CanonicalStateHash: integrationContextHash,
		AccessLevel: ArtifactAccessRaw, ContributorAgentIDs: []string{"agent-2", "agent-1"},
	}
}

func TestSelectLatestIntegrationContextUsesExactLatestFrozenVersion(t *testing.T) {
	request := integrationContextRequestFixture()
	older := integrationSnapshotFixture("old", 1, 20, 10)
	latest := integrationSnapshotFixture("latest", 2, 40, 18)
	selection, err := SelectLatestIntegrationContext(request, []IntegrationSnapshotRef{latest, older})
	if err != nil {
		t.Fatal(err)
	}
	if selection.Snapshot == nil || selection.Snapshot.SnapshotID != "latest" || selection.OmissionReason != "" || len(selection.Fingerprint) != 71 {
		t.Fatalf("selection=%+v", selection)
	}
	if selection.Snapshot.ContributorAgentIDs[0] != "agent-1" {
		t.Fatalf("contributors not normalized: %+v", selection.Snapshot.ContributorAgentIDs)
	}
	reversed, err := SelectLatestIntegrationContext(request, []IntegrationSnapshotRef{older, latest})
	if err != nil {
		t.Fatal(err)
	}
	if reversed.Fingerprint != selection.Fingerprint {
		t.Fatalf("candidate order changed fingerprint: %q != %q", reversed.Fingerprint, selection.Fingerprint)
	}
}

func TestSelectLatestIntegrationContextNeverFallsBackFromLatest(t *testing.T) {
	for name, tc := range map[string]struct {
		mutate func(*IntegrationSnapshotRef)
		want   IntegrationContextOmissionReason
	}{
		"stale":      {func(snapshot *IntegrationSnapshotRef) { snapshot.Status = IntegrationSnapshotStale }, IntegrationContextStale},
		"superseded": {func(snapshot *IntegrationSnapshotRef) { snapshot.Status = IntegrationSnapshotSuperseded }, IntegrationContextSuperseded},
		"clearance":  {func(snapshot *IntegrationSnapshotRef) { snapshot.AccessLevel = ArtifactAccessRaw }, IntegrationContextInsufficientClearance},
		"private":    {func(snapshot *IntegrationSnapshotRef) { snapshot.EvaluationPrivate = true }, IntegrationContextEvaluationCompartment},
	} {
		t.Run(name, func(t *testing.T) {
			request := integrationContextRequestFixture()
			if name == "clearance" {
				request.Clearance = ArtifactClearanceVerifiedOnly
			}
			older := integrationSnapshotFixture("old", 1, 20, 10)
			latest := integrationSnapshotFixture("latest", 2, 40, 18)
			tc.mutate(&latest)
			selection, err := SelectLatestIntegrationContext(request, []IntegrationSnapshotRef{older, latest})
			if err != nil {
				t.Fatal(err)
			}
			if selection.Snapshot != nil || selection.OmissionReason != tc.want || selection.Fingerprint == "" {
				t.Fatalf("selection=%+v want omission=%s", selection, tc.want)
			}
		})
	}
}

func TestSelectLatestIntegrationContextFiltersVersionAndWatermarkHistory(t *testing.T) {
	request := integrationContextRequestFixture()
	current := integrationSnapshotFixture("current", 2, 40, 18)
	oldPlan := integrationSnapshotFixture("old-plan", 9, 45, 19)
	oldPlan.PlanVersion = 2
	future := integrationSnapshotFixture("future", 10, 51, 21)
	selection, err := SelectLatestIntegrationContext(request, []IntegrationSnapshotRef{future, oldPlan, current})
	if err != nil {
		t.Fatal(err)
	}
	if selection.Snapshot == nil || selection.Snapshot.SnapshotID != "current" {
		t.Fatalf("selection=%+v", selection)
	}
}

func TestSelectLatestIntegrationContextNeverTransportsEvaluationPrivateMaterial(t *testing.T) {
	request := integrationContextRequestFixture()
	request.Purpose = ArtifactPurposeEvaluation
	snapshot := integrationSnapshotFixture("grader-visible", 2, 40, 18)
	snapshot.EvaluationPrivate = true
	selection, err := SelectLatestIntegrationContext(request, []IntegrationSnapshotRef{snapshot})
	if err != nil {
		t.Fatal(err)
	}
	if selection.Snapshot != nil || selection.OmissionReason != IntegrationContextEvaluationCompartment {
		t.Fatalf("selection=%+v", selection)
	}
}

func TestSelectLatestIntegrationContextRejectsAmbiguousOrInvalidCandidates(t *testing.T) {
	for name, mutate := range map[string]func(*IntegrationSnapshotRef, *IntegrationSnapshotRef){
		"ambiguous latest": func(first, second *IntegrationSnapshotRef) {
			second.RoundNumber, second.ThroughEventSequence, second.ThroughStateVersion = first.RoundNumber, first.ThroughEventSequence, first.ThroughStateVersion
		},
		"cross workspace":       func(_, second *IntegrationSnapshotRef) { second.WorkspaceID = "workspace-2" },
		"duplicate snapshot":    func(first, second *IntegrationSnapshotRef) { second.SnapshotID = first.SnapshotID },
		"duplicate version":     func(first, second *IntegrationSnapshotRef) { second.ArtifactVersionID = first.ArtifactVersionID },
		"duplicate contributor": func(_, second *IntegrationSnapshotRef) { second.ContributorAgentIDs = []string{"agent-1", "agent-1"} },
		"single contributor":    func(_, second *IntegrationSnapshotRef) { second.ContributorAgentIDs = []string{"agent-1"} },
		"invalid hash":          func(_, second *IntegrationSnapshotRef) { second.InputHash = "sha256:nope" },
		"unknown status":        func(_, second *IntegrationSnapshotRef) { second.Status = "future" },
	} {
		t.Run(name, func(t *testing.T) {
			first := integrationSnapshotFixture("first", 2, 40, 18)
			second := integrationSnapshotFixture("second", 3, 45, 19)
			mutate(&first, &second)
			if _, err := SelectLatestIntegrationContext(integrationContextRequestFixture(), []IntegrationSnapshotRef{first, second}); !errors.Is(err, ErrInvalidContract) {
				t.Fatalf("err=%v want ErrInvalidContract", err)
			}
		})
	}
}

func TestSelectLatestIntegrationContextRejectsUnknownPolicy(t *testing.T) {
	request := integrationContextRequestFixture()
	request.PolicyVersion = "future"
	if _, err := SelectLatestIntegrationContext(request, nil); !errors.Is(err, ErrInvalidContract) {
		t.Fatalf("err=%v want ErrInvalidContract", err)
	}
}

func TestSelectLatestIntegrationContextReturnsAuditableNoSnapshot(t *testing.T) {
	selection, err := SelectLatestIntegrationContext(integrationContextRequestFixture(), nil)
	if err != nil {
		t.Fatal(err)
	}
	if selection.Snapshot != nil || selection.OmissionReason != IntegrationContextNoSnapshot || len(selection.Fingerprint) != 71 {
		t.Fatalf("selection=%+v", selection)
	}
}
