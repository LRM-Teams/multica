package researchrun

import (
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"sort"
	"strings"

	"github.com/google/uuid"
)

const DisputeDeliveryGatePolicyV1 = "research-dispute-delivery-gate-v1"

type DisputeReportDisclosure struct {
	ReportRevisionID    string
	Anchor              string
	Condition           string
	ResidualUncertainty string
	Impact              string
}

type DisputeDeliveryGateItem struct {
	DisputeID             string
	Severity              string
	Status                string
	Condition             string
	ResidualUncertainty   string
	Impact                string
	HumanDecisionRequired bool
	HumanDecisionRecorded bool
	Disclosure            *DisputeReportDisclosure
}

type DisputeDeliveryGateInput struct {
	PolicyVersion    string
	ReportRevisionID string
	ReportHash       string
	Disputes         []DisputeDeliveryGateItem
}

type DisputeDeliveryFinding struct {
	DisputeID string
	Code      string
}

type DisputeDeliveryGateResult struct {
	Passed      bool
	Findings    []DisputeDeliveryFinding
	Fingerprint string
}

// EvaluateDisputeDeliveryGate verifies the complete current Dispute set
// against one immutable report revision. It does not trust report prose as a
// replacement for canonical residual-condition fields.
func EvaluateDisputeDeliveryGate(input DisputeDeliveryGateInput) (DisputeDeliveryGateResult, error) {
	if input.PolicyVersion != DisputeDeliveryGatePolicyV1 ||
		!validDisputeDeliveryUUID(input.ReportRevisionID) || !validDisputeDeliveryHash(input.ReportHash) ||
		len(input.Disputes) > 4096 {
		return DisputeDeliveryGateResult{}, fmt.Errorf("%w: Dispute delivery gate input is invalid", ErrInvalidContract)
	}

	items := append([]DisputeDeliveryGateItem(nil), input.Disputes...)
	sort.Slice(items, func(i, j int) bool { return items[i].DisputeID < items[j].DisputeID })
	findings := make([]DisputeDeliveryFinding, 0)
	seen := make(map[string]struct{}, len(items))
	for _, item := range items {
		if !validDisputeDeliveryItem(item) {
			return DisputeDeliveryGateResult{}, fmt.Errorf("%w: Dispute delivery item is invalid", ErrInvalidContract)
		}
		if _, duplicate := seen[item.DisputeID]; duplicate {
			return DisputeDeliveryGateResult{}, fmt.Errorf("%w: duplicate Dispute delivery item", ErrInvalidContract)
		}
		seen[item.DisputeID] = struct{}{}

		if item.Severity == "blocking" && (item.Status == "open" || item.Status == "investigating") {
			findings = append(findings, DisputeDeliveryFinding{DisputeID: item.DisputeID, Code: "blocking_dispute"})
		}
		if item.HumanDecisionRequired && !item.HumanDecisionRecorded {
			findings = append(findings, DisputeDeliveryFinding{DisputeID: item.DisputeID, Code: "human_gate_pending"})
		}
		if item.Status == "conditionally_resolved" || item.Status == "irreducible" {
			findings = append(findings, disputeDisclosureFindings(input.ReportRevisionID, item)...)
		}
	}
	sort.Slice(findings, func(i, j int) bool {
		if findings[i].DisputeID == findings[j].DisputeID {
			return findings[i].Code < findings[j].Code
		}
		return findings[i].DisputeID < findings[j].DisputeID
	})
	result := DisputeDeliveryGateResult{Passed: len(findings) == 0, Findings: findings}
	encoded, err := json.Marshal(struct {
		PolicyVersion    string
		ReportRevisionID string
		ReportHash       string
		Disputes         []DisputeDeliveryGateItem
		Findings         []DisputeDeliveryFinding
	}{input.PolicyVersion, input.ReportRevisionID, input.ReportHash, items, findings})
	if err != nil {
		return DisputeDeliveryGateResult{}, err
	}
	digest := sha256.Sum256(encoded)
	result.Fingerprint = fmt.Sprintf("sha256:%x", digest)
	return result, nil
}

func disputeDisclosureFindings(reportRevisionID string, item DisputeDeliveryGateItem) []DisputeDeliveryFinding {
	disclosure := item.Disclosure
	if disclosure == nil {
		return []DisputeDeliveryFinding{{DisputeID: item.DisputeID, Code: "disclosure_missing"}}
	}
	findings := make([]DisputeDeliveryFinding, 0, 2)
	if disclosure.ReportRevisionID != reportRevisionID {
		findings = append(findings, DisputeDeliveryFinding{DisputeID: item.DisputeID, Code: "disclosure_stale"})
	}
	if disclosure.Condition != item.Condition || disclosure.ResidualUncertainty != item.ResidualUncertainty || disclosure.Impact != item.Impact {
		findings = append(findings, DisputeDeliveryFinding{DisputeID: item.DisputeID, Code: "disclosure_mismatch"})
	}
	return findings
}

func validDisputeDeliveryItem(item DisputeDeliveryGateItem) bool {
	if !validDisputeDeliveryUUID(item.DisputeID) || (item.Severity != "advisory" && item.Severity != "blocking") || !validDisputeDeliveryStatus(item.Status) {
		return false
	}
	resolvedWithResidual := item.Status == "conditionally_resolved" || item.Status == "irreducible"
	if resolvedWithResidual && (!validDisputeDeliveryText(item.Condition, 4096) || !validDisputeDeliveryText(item.ResidualUncertainty, 4096) || !validDisputeDeliveryText(item.Impact, 4096)) {
		return false
	}
	if !resolvedWithResidual && (item.Condition != "" || item.ResidualUncertainty != "" || item.Disclosure != nil) {
		return false
	}
	if item.Impact != "" && !validDisputeDeliveryText(item.Impact, 4096) {
		return false
	}
	if item.Disclosure != nil && (!validDisputeDeliveryUUID(item.Disclosure.ReportRevisionID) || !validDisputeDeliveryText(item.Disclosure.Anchor, 1024) ||
		!validDisputeDeliveryText(item.Disclosure.Condition, 4096) || !validDisputeDeliveryText(item.Disclosure.ResidualUncertainty, 4096) || !validDisputeDeliveryText(item.Disclosure.Impact, 4096)) {
		return false
	}
	return true
}

func validDisputeDeliveryStatus(status string) bool {
	switch status {
	case "open", "investigating", "resolved", "conditionally_resolved", "irreducible", "obsolete":
		return true
	default:
		return false
	}
}

func validDisputeDeliveryText(value string, limit int) bool {
	return len(value) <= limit && strings.TrimSpace(value) == value && substantiveRuneCount(value) >= 8
}

func validDisputeDeliveryUUID(value string) bool {
	_, err := uuid.Parse(value)
	return err == nil
}

func validDisputeDeliveryHash(value string) bool {
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
