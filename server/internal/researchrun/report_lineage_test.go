package researchrun

import "testing"

func reportRevisionFixture() ReportRevisionCommand {
	return ReportRevisionCommand{ReportID: "r2", SupersedesReportID: "r1", GoalVersion: 2, PlanVersion: 3, StrategyVersion: "s1", IntegrationRoundID: "i1", InputArtifactIDs: []string{"claim-1", "insight-1"}, ClaimAnchorIDs: []string{"anchor-1"}, InsightReferences: []ReportInsightReference{{InsightID: "insight-1", Fresh: true}}, DisputeReferences: []ReportDisputeReference{{DisputeID: "d1", Status: "conditionally_resolved", Disclosed: true, ResidualUncertainty: "regional boundary unknown"}}, RevisionCauses: []ReportRevisionCause{{Kind: ReportCauseEvaluationDefect, TargetID: "defect-1", RequiredChange: "repair claim anchor"}}}
}

func TestValidateReportRevisionCommandRequiresLatestLineageAndAddressableCause(t *testing.T) {
	command := reportRevisionFixture()
	if err := ValidateReportRevisionCommand(command, "r1"); err != nil {
		t.Fatal(err)
	}
	command.SupersedesReportID = "old"
	if err := ValidateReportRevisionCommand(command, "r1"); err == nil {
		t.Fatal("expected non-latest supersession rejection")
	}
	command = reportRevisionFixture()
	command.RevisionCauses = nil
	if err := ValidateReportRevisionCommand(command, "r1"); err == nil {
		t.Fatal("expected generic revision rejection")
	}
}

func TestValidateReportRevisionCommandRejectsStaleInsightAndUndisclosedDispute(t *testing.T) {
	command := reportRevisionFixture()
	command.InsightReferences[0].Fresh = false
	if err := ValidateReportRevisionCommand(command, "r1"); err == nil {
		t.Fatal("expected stale Insight rejection")
	}
	command = reportRevisionFixture()
	command.DisputeReferences[0].Disclosed = false
	if err := ValidateReportRevisionCommand(command, "r1"); err == nil {
		t.Fatal("expected undisclosed Dispute rejection")
	}
}

func TestValidateReportRevisionClosuresRequiresEveryCauseAndConcreteRepair(t *testing.T) {
	causes := []ReportRevisionCause{{Kind: ReportCauseEvaluationDefect, TargetID: "defect", RequiredChange: "fix"}, {Kind: ReportCauseDispute, TargetID: "d1", RequiredChange: "disclose"}}
	closures := []ReportRevisionClosure{{Cause: causes[0], Closed: true, ChangedClaimAnchorIDs: []string{"a1"}}, {Cause: causes[1], Closed: true, ChangedSectionIDs: []string{"limitations"}}}
	if err := ValidateReportRevisionClosures(causes, closures); err != nil {
		t.Fatal(err)
	}
	if err := ValidateReportRevisionClosures(causes, closures[:1]); err == nil {
		t.Fatal("expected incomplete closure rejection")
	}
	closures[1].ChangedSectionIDs = nil
	if err := ValidateReportRevisionClosures(causes, closures); err == nil {
		t.Fatal("expected evidence-free closure rejection")
	}
}
