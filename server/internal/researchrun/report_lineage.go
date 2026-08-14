package researchrun

import "fmt"

type ReportRevisionCauseKind string

const (
	ReportCauseEvaluationDefect ReportRevisionCauseKind = "evaluation_defect"
	ReportCauseDispute          ReportRevisionCauseKind = "dispute"
	ReportCauseStaleGraph       ReportRevisionCauseKind = "stale_graph"
)

type ReportRevisionCause struct {
	Kind           ReportRevisionCauseKind
	TargetID       string
	RequiredChange string
}

type ReportInsightReference struct {
	InsightID string
	Fresh     bool
}

type ReportDisputeReference struct {
	DisputeID           string
	Status              string
	Disclosed           bool
	ResidualUncertainty string
}

type ReportRevisionCommand struct {
	ReportID           string
	SupersedesReportID string
	GoalVersion        int
	PlanVersion        int
	StrategyVersion    string
	IntegrationRoundID string
	InputArtifactIDs   []string
	ClaimAnchorIDs     []string
	InsightReferences  []ReportInsightReference
	DisputeReferences  []ReportDisputeReference
	RevisionCauses     []ReportRevisionCause
}

// ValidateReportRevisionCommand is the post-envelope policy for a V6 report.
// It adds server-resolved IDs and freshness facts without changing wire fields.
func ValidateReportRevisionCommand(command ReportRevisionCommand, expectedPreviousReportID string) error {
	if command.ReportID == "" || command.GoalVersion <= 0 || command.PlanVersion <= 0 || command.StrategyVersion == "" || command.IntegrationRoundID == "" || len(command.InputArtifactIDs) == 0 || len(command.ClaimAnchorIDs) == 0 {
		return fmt.Errorf("%w: report revision identity, versions, integration input, artifacts, and anchors are required", ErrInvalidContract)
	}
	if expectedPreviousReportID == "" {
		if command.SupersedesReportID != "" || len(command.RevisionCauses) > 0 {
			return fmt.Errorf("%w: initial report cannot supersede a revision", ErrInvalidContract)
		}
	} else {
		if command.SupersedesReportID != expectedPreviousReportID {
			return fmt.Errorf("%w: report must supersede the latest revision", ErrInvalidTransition)
		}
		if len(command.RevisionCauses) == 0 {
			return fmt.Errorf("%w: report revision requires addressable causes", ErrInvalidContract)
		}
	}
	if duplicateReportValues(command.InputArtifactIDs) || duplicateReportValues(command.ClaimAnchorIDs) {
		return fmt.Errorf("%w: report inputs and anchors must be unique", ErrInvalidContract)
	}
	for _, insight := range command.InsightReferences {
		if insight.InsightID == "" || !insight.Fresh {
			return fmt.Errorf("%w: stale or unidentified Insight cannot enter a report", ErrInvalidTransition)
		}
	}
	for _, dispute := range command.DisputeReferences {
		if dispute.DisputeID == "" {
			return fmt.Errorf("%w: report Dispute id is required", ErrInvalidContract)
		}
		if dispute.Status == "conditionally_resolved" || dispute.Status == "irreducible" {
			if !dispute.Disclosed || dispute.ResidualUncertainty == "" {
				return fmt.Errorf("%w: conditional or irreducible Dispute requires report disclosure and residual uncertainty", ErrInvalidContract)
			}
		}
	}
	seenCauses := make(map[string]struct{}, len(command.RevisionCauses))
	for _, cause := range command.RevisionCauses {
		if !validReportCause(cause.Kind) || cause.TargetID == "" || cause.RequiredChange == "" {
			return fmt.Errorf("%w: report revision cause must be addressable", ErrInvalidContract)
		}
		key := string(cause.Kind) + ":" + cause.TargetID
		if _, exists := seenCauses[key]; exists {
			return fmt.Errorf("%w: duplicate report revision cause %q", ErrInvalidContract, key)
		}
		seenCauses[key] = struct{}{}
	}
	return nil
}

type ReportRevisionClosure struct {
	Cause                 ReportRevisionCause
	Closed                bool
	ChangedClaimAnchorIDs []string
	ChangedSectionIDs     []string
}

func ValidateReportRevisionClosures(causes []ReportRevisionCause, closures []ReportRevisionClosure) error {
	byKey := make(map[string]ReportRevisionCause, len(causes))
	for _, cause := range causes {
		byKey[string(cause.Kind)+":"+cause.TargetID] = cause
	}
	seen := make(map[string]struct{}, len(closures))
	for _, closure := range closures {
		key := string(closure.Cause.Kind) + ":" + closure.Cause.TargetID
		if _, exists := byKey[key]; !exists {
			return fmt.Errorf("%w: closure references unknown revision cause %q", ErrInvalidContract, key)
		}
		if _, exists := seen[key]; exists {
			return fmt.Errorf("%w: duplicate revision closure %q", ErrInvalidContract, key)
		}
		seen[key] = struct{}{}
		if !closure.Closed || len(closure.ChangedClaimAnchorIDs)+len(closure.ChangedSectionIDs) == 0 {
			return fmt.Errorf("%w: revision cause %q lacks an addressable repair", ErrInvalidTransition, key)
		}
	}
	if len(seen) != len(byKey) {
		return fmt.Errorf("%w: not every report revision cause was closed", ErrInvalidTransition)
	}
	return nil
}

func validReportCause(kind ReportRevisionCauseKind) bool {
	return kind == ReportCauseEvaluationDefect || kind == ReportCauseDispute || kind == ReportCauseStaleGraph
}
func duplicateReportValues(values []string) bool {
	seen := map[string]struct{}{}
	for _, value := range values {
		if value == "" {
			return true
		}
		if _, ok := seen[value]; ok {
			return true
		}
		seen[value] = struct{}{}
	}
	return false
}
